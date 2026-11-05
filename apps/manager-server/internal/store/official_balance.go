package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
)

var ErrBalanceConflict = errors.New("balance configuration conflict")

// Record the accepted attempt and its job atomically before any upstream I/O.
// A hard deadline or process exit must not erase that a refresh was attempted.
func (s *Store) BeginOfficialBalanceJob(ctx context.Context, id string, version int64, jobID, document, attempt string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE official_balance_sources SET attempt=? WHERE id=? AND version=?`, attempt, id, version)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrBalanceConflict
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO official_balance_jobs(id,source_id,document) VALUES(?,?,?)`, jobID, id, document); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) SaveOfficialBalanceJob(ctx context.Context, id, source, document string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO official_balance_jobs(id,source_id,document) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET document=excluded.document`, id, source, document)
	return err
}
func (s *Store) OfficialBalanceJob(ctx context.Context, id string) (string, error) {
	var doc string
	err := s.db.QueryRowContext(ctx, `SELECT document FROM official_balance_jobs WHERE id=?`, id).Scan(&doc)
	return doc, err
}

// OfficialBalanceRecord is private host persistence, never an HTTP response.
// The opaque ref is owned here; callers cannot select someone else's secret.
type OfficialBalanceRecord struct {
	ID          string
	Version     int64
	Config      string
	SecretRef   string
	Configured  bool
	Attempt     string
	LastSuccess string
}

func (s *Store) ListOfficialBalances(ctx context.Context) ([]OfficialBalanceRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, version, config, secret_ref, secret_cipher, attempt, last_success FROM official_balance_sources`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OfficialBalanceRecord
	for rows.Next() {
		var r OfficialBalanceRecord
		var cipher string
		if err := rows.Scan(&r.ID, &r.Version, &r.Config, &r.SecretRef, &cipher, &r.Attempt, &r.LastSuccess); err != nil {
			return nil, err
		}
		r.Configured = cipher != ""
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) OfficialBalance(ctx context.Context, id string) (OfficialBalanceRecord, error) {
	r := OfficialBalanceRecord{ID: id}
	var cipher string
	err := s.db.QueryRowContext(ctx, `SELECT version, config, secret_ref, secret_cipher, attempt, last_success FROM official_balance_sources WHERE id=?`, id).Scan(&r.Version, &r.Config, &r.SecretRef, &cipher, &r.Attempt, &r.LastSuccess)
	if errors.Is(err, sql.ErrNoRows) {
		return r, nil
	}
	r.Configured = cipher != ""
	return r, err
}

func (s *Store) SaveOfficialBalance(ctx context.Context, r OfficialBalanceRecord, expected int64, secret string, clear bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int64
	var cipher, ref string
	err = tx.QueryRowContext(ctx, `SELECT version, secret_cipher, secret_ref FROM official_balance_sources WHERE id=?`, r.ID).Scan(&version, &cipher, &ref)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if version != expected {
		return ErrBalanceConflict
	}
	if clear {
		cipher = ""
		ref = ""
	}
	if secret != "" {
		if s.officialProtector == nil {
			return errors.New("balance secret encryption unavailable")
		}
		cipher, err = s.officialProtector.ProtectString(secret)
		if err != nil {
			return errors.New("balance secret encryption unavailable")
		}
		ref = uuid.NewString()
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO official_balance_sources(id,version,config,secret_ref,secret_cipher) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET version=excluded.version,config=excluded.config,secret_ref=excluded.secret_ref,secret_cipher=excluded.secret_cipher,attempt='',last_success=''`, r.ID, expected+1, r.Config, ref, cipher)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ResolveOfficialBalanceSecret(ctx context.Context, id string, version int64, ref string) (string, error) {
	var cipher string
	err := s.db.QueryRowContext(ctx, `SELECT secret_cipher FROM official_balance_sources WHERE id=? AND version=? AND secret_ref=?`, id, version, ref).Scan(&cipher)
	if err != nil {
		return "", err
	}
	if cipher == "" {
		return "", nil
	}
	if s.officialProtector == nil || !security.IsValidProtectedEnvelope(cipher) {
		return "", errors.New("balance secret unavailable")
	}
	return s.officialProtector.UnprotectString(cipher)
}

func (s *Store) CompleteOfficialBalance(ctx context.Context, id string, version int64, jobID, attempt string, success bool) error {
	query := `UPDATE official_balance_sources SET attempt=? WHERE id=? AND version=? AND json_extract(attempt,'$.queryId')=?`
	args := []any{attempt, id, version, jobID}
	if success {
		query = `UPDATE official_balance_sources SET attempt=?,last_success=? WHERE id=? AND version=? AND json_extract(attempt,'$.queryId')=?`
		args = []any{attempt, attempt, id, version, jobID}
	}
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n != 1 {
		return ErrBalanceConflict
	}
	return err
}
