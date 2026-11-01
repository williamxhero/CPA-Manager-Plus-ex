package store

import (
	"context"
	"database/sql"
	"encoding/hex"
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
	// RefreshScheduleKey is a transaction-local work hint, never action proof or
	// part of the stored intent. Only a prerequisite-guarded ON may set it.
	RefreshScheduleKey string
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
	return s.saveCredentialPolicy(ctx, r, expected, nil)
}

func (s *Store) saveCredentialPolicy(ctx context.Context, r CredentialPolicyRecord, expected int64, guard func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if guard != nil {
		if err := guard(tx); err != nil {
			return err
		}
	}
	if r.RefreshScheduleKey != "" {
		key := r.RefreshScheduleKey
		if guard == nil || len(key) != len("source:")+64 || key[:7] != "source:" {
			return ErrPolicyConflict
		}
		if decoded, err := hex.DecodeString(key[7:]); err != nil || hex.EncodeToString(decoded) != key[7:] {
			return ErrPolicyConflict
		}
		var enabled bool
		if err := tx.QueryRowContext(ctx, `SELECT json_extract(?,'$.enabled')=1`, r.Document).Scan(&enabled); err != nil || !enabled {
			return ErrPolicyConflict
		}
	}
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
	// Stage work for the NEXT scheduler-cycle cutoff, never invalidate credit
	// beneath an already-running cycle's inventory/admission. Intent and hint
	// are one durable commit; OFF supersedes old hints without changing credit.
	if _, err := tx.ExecContext(ctx, `DELETE FROM credential_policy_schedule_hints WHERE subject_id=?`, r.ID); err != nil {
		return err
	}
	if r.RefreshScheduleKey != "" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO credential_policy_schedule_hints(subject_id,generation,source_key) VALUES(?,?,?)`, r.ID, r.Generation, r.RefreshScheduleKey); err != nil {
			return err
		}
	}
	// Every intent generation, including OFF, invalidates earlier automatic
	// actions atomically. OFF never emits a compensating Core clear.
	_, err = tx.ExecContext(ctx, `UPDATE credential_policy_operations SET state='superseded',revision=revision+1,last_error=CASE WHEN attempted=1 THEN 'outcome_unverified' ELSE 'policy_generation_changed' END WHERE subject_id=? AND automatic=1 AND generation<>? AND state IN ('queued','pending','error')`, r.ID, r.Generation)
	if err != nil {
		return err
	}
	return tx.Commit()
}
