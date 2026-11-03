package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
)

var ErrProviderGateConflict = errors.New("provider gate operation conflict")

// Provider gate ownership is independent of credential policy and manual flags.
func (s *Store) ProviderGateInstallation(ctx context.Context) (string, error) {
	_, err := s.db.ExecContext(ctx, `INSERT INTO provider_gate_identity(singleton,installation_id) VALUES(1,?) ON CONFLICT(singleton) DO NOTHING`, uuid.NewString())
	if err != nil {
		return "", err
	}
	var id string
	err = s.db.QueryRowContext(ctx, `SELECT installation_id FROM provider_gate_identity WHERE singleton=1`).Scan(&id)
	if err != nil || uuid.Validate(id) != nil {
		return "", errors.New("provider gate identity unavailable")
	}
	return id, nil
}

type ProviderGateRecord struct {
	ID         string
	Generation int64
	Document   string
}

func (s *Store) ProviderGate(ctx context.Context, id string) (ProviderGateRecord, error) {
	r := ProviderGateRecord{ID: id}
	err := s.db.QueryRowContext(ctx, `SELECT generation,document FROM provider_gate_operations WHERE id=?`, id).Scan(&r.Generation, &r.Document)
	if errors.Is(err, sql.ErrNoRows) {
		return r, nil
	}
	return r, err
}

// Freeze the complete operation before RPC. Generation CAS serializes writers
// across Manager processes; a late completion can only update its exact body.
func (s *Store) SaveProviderGate(ctx context.Context, next ProviderGateRecord, expected int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var result sql.Result
	if expected == 0 {
		result, err = tx.ExecContext(ctx, `INSERT INTO provider_gate_operations(id,generation,document) VALUES(?,?,?) ON CONFLICT(id) DO NOTHING`, next.ID, next.Generation, next.Document)
	} else {
		result, err = tx.ExecContext(ctx, `UPDATE provider_gate_operations SET generation=?,document=? WHERE id=? AND generation=?`, next.Generation, next.Document, next.ID, expected)
	}
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return ErrProviderGateConflict
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO provider_gate_history(id,generation,document) VALUES(?,?,?)`, next.ID, next.Generation, next.Document); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) CompleteProviderGate(ctx context.Context, previous ProviderGateRecord, document string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE provider_gate_operations SET document=? WHERE id=? AND generation=? AND document=?`, document, previous.ID, previous.Generation, previous.Document)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return ErrProviderGateConflict
	}
	if err != nil {
		return err
	}
	result, err = tx.ExecContext(ctx, `UPDATE provider_gate_history SET document=? WHERE id=? AND generation=? AND document=?`, document, previous.ID, previous.Generation, previous.Document)
	if err != nil {
		return err
	}
	n, err = result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrProviderGateConflict
	}
	return tx.Commit()
}
