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

// SaveCredentialPolicy is a database CAS, not a process-local check. Pending
// intent must use this commit before any corresponding external gate write.
func (s *Store) SaveCredentialPolicy(ctx context.Context, r CredentialPolicyRecord, expected int64) error {
	var result sql.Result
	var err error
	if expected == 0 {
		result, err = s.db.ExecContext(ctx, `INSERT INTO credential_policies(id,generation,document) VALUES(?,?,?) ON CONFLICT(id) DO NOTHING`, r.ID, r.Generation, r.Document)
	} else {
		result, err = s.db.ExecContext(ctx, `UPDATE credential_policies SET generation=?,document=? WHERE id=? AND generation=?`, r.Generation, r.Document, r.ID, expected)
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
	return nil
}
