package store

import (
	"context"
	"encoding/json"
)

// Discovery is a secret-free issued descriptor cache, NOT association evidence or
// readiness. Exact scope/targets and later real Core revalidation remain required.
func (s *Store) RememberCredentialPolicySource(ctx context.Context, id, document string) error {
	if id == "" || len(document) > 32<<10 || !json.Valid([]byte(document)) {
		return ErrPolicyConflict
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO credential_policy_discovery(id,document) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET document=excluded.document`, id, document)
	return err
}
func (s *Store) CredentialPolicySources(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT document FROM credential_policy_discovery ORDER BY rowid LIMIT 10000`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var doc string
		if err := rows.Scan(&doc); err != nil {
			return nil, err
		}
		out = append(out, doc)
	}
	return out, rows.Err()
}
func (s *Store) CredentialAssociationCapturesForJob(ctx context.Context, id string) ([]CredentialAssociationCapture, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT document FROM credential_association_captures WHERE job_id=? ORDER BY rowid`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CredentialAssociationCapture
	for rows.Next() {
		var doc string
		var c CredentialAssociationCapture
		if err := rows.Scan(&doc); err != nil {
			return nil, err
		}
		if json.Unmarshal([]byte(doc), &c) != nil {
			return nil, ErrPolicyOperationConflict
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
