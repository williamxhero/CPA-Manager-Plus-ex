package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
)

// BeginCredentialPolicyDecision allocates a genuine cryptographic UUIDv4 only
// ONCE inside the guarded planning transaction. The non-secret decision key is
// separate from the Core opId. Build must be pure/local; no I/O or another DB call.
// Legacy lookup preserves an old operation/body instead of silently replacing it.
func (s *Store) BeginCredentialPolicyDecision(ctx context.Context, key, legacyID string, op CredentialPolicyOperation, lease CredentialPolicyLease, now int64, build func(string, string) (string, error)) (CredentialPolicyOperation, error) {
	if key == "" || len(key) > 256 || !op.Automatic || build == nil {
		return op, ErrPolicyOperationConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return op, err
	}
	defer tx.Rollback()
	if err := policyOperationGuard(ctx, tx, op, lease, now); err != nil {
		return op, err
	}
	var id string
	err = tx.QueryRowContext(ctx, `SELECT op_id FROM credential_policy_decisions WHERE decision_key=?`, key).Scan(&id)
	mapped := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return op, err
	}
	var old CredentialPolicyOperation
	if !mapped {
		old, err = scanPolicyOperation(tx.QueryRowContext(ctx, `SELECT `+policyOperationColumns+` FROM credential_policy_operations WHERE id=?`, legacyID))
		if err == nil {
			id = old.ID
		} else if !errors.Is(err, sql.ErrNoRows) {
			return op, err
		}
	} else {
		old, err = scanPolicyOperation(tx.QueryRowContext(ctx, `SELECT `+policyOperationColumns+` FROM credential_policy_operations WHERE id=?`, id))
		if err != nil {
			return op, err
		}
	}
	if id != "" {
		if old.SubjectID != op.SubjectID || old.Generation != op.Generation || old.Owner != op.Owner || !old.Automatic {
			return op, ErrPolicyOperationConflict
		}
		body, err := build(old.ID, old.Body)
		if err != nil {
			return op, err
		}
		if body != old.Body {
			return op, ErrPolicyOperationConflict
		}
		if err := policyOperationGuard(ctx, tx, old, lease, now); err != nil {
			return op, err
		}
		if !mapped {
			if _, err := tx.ExecContext(ctx, `INSERT INTO credential_policy_decisions(decision_key,op_id) VALUES(?,?)`, key, old.ID); err != nil {
				return op, err
			}
		}
		return old, tx.Commit()
	}
	op.ID = uuid.NewString()
	op.Body, err = build(op.ID, "")
	if err != nil {
		return op, err
	}
	if len(op.Body) == 0 || len(op.Body) > 32<<10 {
		return op, ErrPolicyOperationConflict
	}
	if err := policyOperationGuard(ctx, tx, op, lease, now); err != nil {
		return op, err
	}
	op.State = "queued"
	op.Revision = 1
	op.Result = ""
	op.LastError = ""
	op.Attempted = false
	if _, err := tx.ExecContext(ctx, `INSERT INTO credential_policy_operations(`+policyOperationColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, op.ID, op.SubjectID, op.Generation, op.Owner, true, op.State, op.Body, "", "", op.Revision, false); err != nil {
		return op, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO credential_policy_decisions(decision_key,op_id) VALUES(?,?)`, key, op.ID); err != nil {
		return op, err
	}
	return op, tx.Commit()
}
