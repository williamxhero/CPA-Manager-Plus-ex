package store

import (
	"context"
	"database/sql"
	"errors"
)

var ErrProviderPolicyConflict = errors.New("provider policy generation conflict")

type ProviderPolicyRecord struct {
	ID         string
	Generation int64
	Document   string
}

func (s *Store) ProviderPolicy(ctx context.Context, id string) (ProviderPolicyRecord, error) {
	r := ProviderPolicyRecord{ID: id}
	err := s.db.QueryRowContext(ctx, `SELECT generation,document FROM provider_policies WHERE id=?`, id).Scan(&r.Generation, &r.Document)
	if errors.Is(err, sql.ErrNoRows) {
		return r, nil
	}
	return r, err
}

// Saving an intent and automatic decisions use the same fenced per-DB lease.
// This transaction changes no native/manual state and emits no Core command.
func (s *Store) SaveProviderPolicy(ctx context.Context, next ProviderPolicyRecord, previous ProviderPolicyRecord, lease CredentialPolicyLease, now int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var valid bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM credential_policy_lease WHERE singleton=1 AND holder=? AND epoch=? AND expires_ms>?)`, lease.Holder, lease.Epoch, now).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return ErrPolicyLease
	}
	var result sql.Result
	if previous.Generation == 0 {
		result, err = tx.ExecContext(ctx, `INSERT INTO provider_policies(id,generation,document) VALUES(?,?,?) ON CONFLICT(id) DO NOTHING`, next.ID, next.Generation, next.Document)
	} else {
		result, err = tx.ExecContext(ctx, `UPDATE provider_policies SET generation=?,document=? WHERE id=? AND generation=? AND document=?`, next.Generation, next.Document, next.ID, previous.Generation, previous.Document)
	}
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrProviderPolicyConflict
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO provider_policy_history(id,generation,document) VALUES(?,?,?)`, next.ID, next.Generation, next.Document)
	if err != nil {
		return err
	}
	return tx.Commit()
}
