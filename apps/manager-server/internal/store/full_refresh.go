package store

import (
	"context"
	"database/sql"
	"errors"
)

func (s *Store) SaveFullRefresh(ctx context.Context, id, state, document string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO full_refresh_runs(id,state,document) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET state=excluded.state,document=excluded.document`, id, state, document)
	return err
}
func (s *Store) FullRefresh(ctx context.Context, id string) (string, error) {
	var doc string
	err := s.db.QueryRowContext(ctx, `SELECT document FROM full_refresh_runs WHERE id=?`, id).Scan(&doc)
	return doc, err
}
func (s *Store) InterruptedFullRefresh(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT document FROM full_refresh_runs WHERE state='running'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var doc string
		if err = rows.Scan(&doc); err != nil {
			return nil, err
		}
		out = append(out, doc)
	}
	return out, rows.Err()
}
func (s *Store) FullRefreshDue(ctx context.Context, id string) (int64, error) {
	var due int64
	err := s.db.QueryRowContext(ctx, `SELECT next_due_ms FROM full_refresh_schedule WHERE id=?`, id).Scan(&due)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return due, err
}
func (s *Store) SaveFullRefreshDue(ctx context.Context, id string, due int64) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO full_refresh_schedule(id,next_due_ms) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET next_due_ms=excluded.next_due_ms`, id, due)
	return err
}

func (s *Store) LatestFullRefresh(ctx context.Context) (string, error) {
	var doc string
	err := s.db.QueryRowContext(ctx, `SELECT document FROM full_refresh_runs ORDER BY rowid DESC LIMIT 1`).Scan(&doc)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return doc, err
}

func (s *Store) ManagementQueryRetryAt(ctx context.Context, id string) (int64, error) {
	var due int64
	err := s.db.QueryRowContext(ctx, `SELECT not_before_ms FROM management_query_backoff WHERE id=?`, id).Scan(&due)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return due, err
}
func (s *Store) SaveManagementQueryRetryAt(ctx context.Context, id string, due int64) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO management_query_backoff(id,not_before_ms) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET not_before_ms=MAX(not_before_ms,excluded.not_before_ms)`, id, due)
	return err
}
