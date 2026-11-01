package store

import (
	"context"
	"database/sql"
	"errors"
	"github.com/google/uuid"
)

var ErrPolicyConflict = errors.New("credential policy generation conflict")

// CredentialPolicyInstallation is logical DB identity: backup/restore retains
// it; an independent DB generates a distinct ID. It is never a boot/service ID.
func (s *Store) CredentialPolicyInstallation(ctx context.Context) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT installation_id FROM credential_policy_identity WHERE singleton=1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = s.db.ExecContext(ctx, `INSERT INTO credential_policy_identity(singleton,installation_id) VALUES(1,?) ON CONFLICT(singleton) DO NOTHING`, uuid.NewString())
		if err != nil {
			return "", err
		}
		err = s.db.QueryRowContext(ctx, `SELECT installation_id FROM credential_policy_identity WHERE singleton=1`).Scan(&id)
	}
	if err != nil {
		return "", err
	}
	if parsed, err := uuid.Parse(id); err != nil || parsed.String() != id {
		return "", errors.New("credential policy installation unavailable")
	}
	return id, nil
}

// ID is a digest of issued instance/target identifiers, never secret material.
// Document contains only validated secret-free source and policy references.
type CredentialPolicyRecord struct {
	ID         string
	Generation int64
	Document   string
}

func (s *Store) CredentialPolicy(ctx context.Context, id string) (CredentialPolicyRecord, error) {
	r := CredentialPolicyRecord{ID: id}
	err := s.db.QueryRowContext(ctx, `SELECT generation,document FROM credential_policies WHERE id=?`, id).Scan(&r.Generation, &r.Document)
	if errors.Is(err, sql.ErrNoRows) {
		return r, nil
	}
	return r, err
}

// RebindCredentialPolicy changes only the issued operational scope/index after
// the consumer verifies current authority. Document CAS also fences same-
// generation rebinds; intent, owner, targets and evidence are never migrated.
func (s *Store) RebindCredentialPolicy(ctx context.Context, previous CredentialPolicyRecord, scope, index string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE credential_policies SET document=json_set(document,'$.scope',json(?),'$.auth_index',?) WHERE id=? AND generation=? AND document=?`, scope, index, previous.ID, previous.Generation, previous.Document)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrPolicyConflict
	}
	return nil
}

// SaveCredentialPolicy is a database CAS, not a process-local check. Pending
// intent must use this commit before any corresponding external gate write.
func (s *Store) SaveCredentialPolicy(ctx context.Context, r CredentialPolicyRecord, expected int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var result sql.Result
	if expected == 0 {
		result, err = tx.ExecContext(ctx, `INSERT INTO credential_policies(id,generation,document) VALUES(?,?,?) ON CONFLICT(id) DO NOTHING`, r.ID, r.Generation, r.Document)
	} else {
		result, err = tx.ExecContext(ctx, `UPDATE credential_policies SET generation=?,document=? WHERE id=? AND generation=?`, r.Generation, r.Document, r.ID, expected)
	}
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrPolicyConflict
	}
	// Every intent generation, including OFF, invalidates earlier automatic
	// actions atomically. OFF never emits a compensating Core clear.
	_, err = tx.ExecContext(ctx, `UPDATE credential_policy_operations SET state='superseded',revision=revision+1,last_error=CASE WHEN attempted=1 THEN 'outcome_unverified' ELSE 'policy_generation_changed' END WHERE subject_id=? AND automatic=1 AND generation<>? AND state IN ('queued','pending','error')`, r.ID, r.Generation)
	if err != nil {
		return err
	}
	return tx.Commit()
}
