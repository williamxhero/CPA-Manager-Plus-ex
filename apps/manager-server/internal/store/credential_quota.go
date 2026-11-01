package store

import (
	"context"
	"database/sql"
	"errors"
)

var ErrQuotaConflict = errors.New("credential quota conflict")

type CredentialQuotaRecord struct {
	ID                           string
	Version                      int64
	Source, Attempt, LastSuccess string
}

func (s *Store) CredentialQuota(ctx context.Context, id string) (CredentialQuotaRecord, error) {
	r := CredentialQuotaRecord{ID: id}
	err := s.db.QueryRowContext(ctx, `SELECT version,source,attempt,last_success FROM credential_quota_sources WHERE id=?`, id).Scan(&r.Version, &r.Source, &r.Attempt, &r.LastSuccess)
	if errors.Is(err, sql.ErrNoRows) {
		return r, nil
	}
	return r, err
}

// BeginCredentialQuota atomically fences source CAS, latest attempt and job.
// No caller may send upstream I/O until this transaction commits.
func (s *Store) BeginCredentialQuota(ctx context.Context, r CredentialQuotaRecord, expected int64, jobID, document string, keepSuccess bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int64
	var success string
	err = tx.QueryRowContext(ctx, `SELECT version,last_success FROM credential_quota_sources WHERE id=?`, r.ID).Scan(&version, &success)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if version != expected {
		return ErrQuotaConflict
	}
	if !keepSuccess {
		success = ""
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO credential_quota_sources(id,version,source,attempt,last_success) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET version=excluded.version,source=excluded.source,attempt=excluded.attempt,last_success=excluded.last_success`, r.ID, r.Version, r.Source, r.Attempt, success)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO credential_quota_jobs(id,source_id,document) VALUES(?,?,?)`, jobID, r.ID, document)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) CompleteCredentialQuota(ctx context.Context, id string, version int64, jobID, attempt, document string, success bool) error {
	_, err := s.CompleteCredentialQuotaSnapshot(ctx, id, version, jobID, attempt, document, success)
	return err
}

// CompleteCredentialQuotaSnapshot returns the exact terminal source record from
// the completion transaction, only after Commit succeeds. Consumers must not
// depend on a second, fallible read after that durable publication. All writes
// and snapshot acquisition retain the caller's deadline and source/job CAS.
func (s *Store) CompleteCredentialQuotaSnapshot(ctx context.Context, id string, version int64, jobID, attempt, document string, success bool) (CredentialQuotaRecord, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CredentialQuotaRecord{}, err
	}
	defer tx.Rollback()
	query := `UPDATE credential_quota_sources SET attempt=? WHERE id=? AND version=? AND json_extract(attempt,'$.queryId')=?`
	args := []any{attempt, id, version, jobID}
	if success {
		query = `UPDATE credential_quota_sources SET attempt=?,last_success=? WHERE id=? AND version=? AND json_extract(attempt,'$.queryId')=?`
		args = []any{attempt, attempt, id, version, jobID}
	}
	r := CredentialQuotaRecord{ID: id}
	err = tx.QueryRowContext(ctx, query+` RETURNING version,source,attempt,last_success`, args...).Scan(&r.Version, &r.Source, &r.Attempt, &r.LastSuccess)
	if errors.Is(err, sql.ErrNoRows) {
		return CredentialQuotaRecord{}, ErrQuotaConflict
	}
	if err != nil {
		return CredentialQuotaRecord{}, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE credential_quota_jobs SET document=? WHERE id=?`, document, jobID)
	if err != nil {
		return CredentialQuotaRecord{}, err
	}
	if err = tx.Commit(); err != nil {
		return CredentialQuotaRecord{}, err
	}
	return r, nil
}
func (s *Store) CredentialQuotaJob(ctx context.Context, id string) (string, error) {
	var doc string
	err := s.db.QueryRowContext(ctx, `SELECT document FROM credential_quota_jobs WHERE id=?`, id).Scan(&doc)
	return doc, err
}
