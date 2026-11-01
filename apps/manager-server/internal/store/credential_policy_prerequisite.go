package store

import (
	"context"
	"database/sql"

	"github.com/google/uuid"
)

// CaptureCredentialPolicyPrerequisite records an original default-OFF query
// before I/O. It cannot enroll intent or satisfy the enabled-only action guard.
func (s *Store) CaptureCredentialPolicyPrerequisite(ctx context.Context, f CredentialPolicyQueryFence) error {
	if id, err := uuid.Parse(f.AttemptID); err != nil || id.String() != f.AttemptID || f.Enabled || f.Generation != 0 || len(f.Source) > 32<<10 {
		return ErrPolicyConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var valid bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM credential_quota_jobs j JOIN credential_policy_identity i ON i.singleton=1 WHERE j.id=? AND j.source_id=? AND json_extract(j.document,'$.state')='running' AND json(json_extract(j.document,'$.query'))=json(?) AND ?='cpa-manager.credential-policy.v1/'||i.installation_id AND NOT EXISTS(SELECT 1 FROM credential_policies WHERE id=?))`, f.AttemptID, f.SubjectID, f.Source, f.Owner, f.SubjectID).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return ErrPolicyConflict
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO credential_policy_query_fences(attempt_id,subject_id,generation,owner,source,enabled) VALUES(?,?,?,?,?,0) ON CONFLICT(attempt_id) DO NOTHING`, f.AttemptID, f.SubjectID, f.Generation, f.Owner, f.Source)
	if err != nil {
		return err
	}
	var old CredentialPolicyQueryFence
	err = tx.QueryRowContext(ctx, `SELECT attempt_id,subject_id,generation,owner,source,enabled FROM credential_policy_query_fences WHERE attempt_id=?`, f.AttemptID).Scan(&old.AttemptID, &old.SubjectID, &old.Generation, &old.Owner, &old.Source, &old.Enabled)
	if err != nil {
		return err
	}
	if old != f {
		return ErrPolicyConflict
	}
	return tx.Commit()
}

// checkPrerequisite accepts either a literal existing intent generation or the
// still-absent default generation. It never promotes that fence to enabled.
func checkPrerequisite(ctx context.Context, tx *sql.Tx, f CredentialPolicyQueryFence, completed string) error {
	var valid bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM credential_policy_query_fences f JOIN credential_policy_identity i ON i.singleton=1 JOIN credential_quota_jobs j ON j.id=f.attempt_id JOIN credential_quota_sources q ON q.id=j.source_id LEFT JOIN credential_policies p ON p.id=f.subject_id WHERE f.attempt_id=? AND f.subject_id=? AND f.generation=? AND f.owner=? AND f.source=? AND f.enabled=? AND f.owner='cpa-manager.credential-policy.v1/'||i.installation_id AND j.source_id=f.subject_id AND j.document=? AND json_extract(j.document,'$.state')='complete' AND q.version=json_extract(j.document,'$.query.version') AND json_extract(q.attempt,'$.queryId')=f.attempt_id AND json_extract(q.attempt,'$.queryStatus')='success' AND json_extract(q.last_success,'$.queryId')=f.attempt_id AND ((p.id IS NULL AND f.generation=0 AND f.enabled=0) OR (p.generation=f.generation AND json_extract(p.document,'$.owner')=f.owner AND json_extract(p.document,'$.enabled')=f.enabled)))`, f.AttemptID, f.SubjectID, f.Generation, f.Owner, f.Source, f.Enabled, completed).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return ErrPolicyConflict
	}
	return nil
}

// SaveCredentialPolicyWithPrerequisite commits ON and its original completed
// job/intent fence atomically. Validation of current Core authority and evidence
// is the service's responsibility, not an inference from SQLite booleans.
func (s *Store) SaveCredentialPolicyWithPrerequisite(ctx context.Context, r CredentialPolicyRecord, expected int64, f CredentialPolicyQueryFence, completed string) error {
	if r.ID != f.SubjectID || expected != f.Generation {
		return ErrPolicyConflict
	}
	return s.saveCredentialPolicy(ctx, r, expected, func(tx *sql.Tx) error { return checkPrerequisite(ctx, tx, f, completed) })
}
