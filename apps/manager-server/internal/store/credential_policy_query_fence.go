package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
)

type CredentialPolicyQueryFence struct {
	AttemptID  string
	SubjectID  string
	Generation int64
	Owner      string
	Source     string
	Enabled    bool
}

func (s *Store) CredentialPolicyQuery(ctx context.Context, attempt string) (CredentialPolicyQueryFence, error) {
	var f CredentialPolicyQueryFence
	err := s.db.QueryRowContext(ctx, `SELECT attempt_id,subject_id,generation,owner,source,enabled FROM credential_policy_query_fences WHERE attempt_id=?`, attempt).Scan(&f.AttemptID, &f.SubjectID, &f.Generation, &f.Owner, &f.Source, &f.Enabled)
	return f, err
}

// Capture runs synchronously after the query job commit and before upstream I/O.
// A reused job may never refresh the captured policy generation.
func (s *Store) CaptureCredentialPolicyQuery(ctx context.Context, f CredentialPolicyQueryFence) error {
	if id, err := uuid.Parse(f.AttemptID); err != nil || id.String() != f.AttemptID || len(f.Source) > 32<<10 {
		return ErrPolicyConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var generation int64
	var owner, installation string
	var enabled bool
	err = tx.QueryRowContext(ctx, `SELECT generation,json_extract(document,'$.owner'),json_extract(document,'$.enabled'),(SELECT installation_id FROM credential_policy_identity WHERE singleton=1) FROM credential_policies WHERE id=?`, f.SubjectID).Scan(&generation, &owner, &enabled, &installation)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrPolicyConflict
	}
	if err != nil {
		return err
	}
	if generation != f.Generation || owner != f.Owner || owner != "cpa-manager.credential-policy.v1/"+installation || enabled != f.Enabled {
		return ErrPolicyConflict
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO credential_policy_query_fences(attempt_id,subject_id,generation,owner,source,enabled) VALUES(?,?,?,?,?,?) ON CONFLICT(attempt_id) DO NOTHING`, f.AttemptID, f.SubjectID, f.Generation, f.Owner, f.Source, f.Enabled)
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
func (s *Store) CheckCredentialPolicyQuery(ctx context.Context, f CredentialPolicyQueryFence) error {
	var valid bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM credential_policy_query_fences f JOIN credential_policies p ON p.id=f.subject_id JOIN credential_policy_identity i ON i.singleton=1 WHERE f.attempt_id=? AND f.subject_id=? AND f.generation=? AND f.owner=? AND f.source=? AND f.enabled=1 AND p.generation=f.generation AND json_extract(p.document,'$.enabled')=1 AND json_extract(p.document,'$.owner')=f.owner AND f.owner='cpa-manager.credential-policy.v1/'||i.installation_id)`, f.AttemptID, f.SubjectID, f.Generation, f.Owner, f.Source).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid || !f.Enabled {
		return ErrPolicyConflict
	}
	return nil
}
