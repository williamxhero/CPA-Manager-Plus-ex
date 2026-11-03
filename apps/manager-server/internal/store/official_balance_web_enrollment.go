package store

import (
	"context"
	"database/sql"
	"errors"
)

// A registration is an operator's request for verification, never a trusted
// BrowserSkill enrollment. It stores no browser content, secrets or scripts.
type WebEnrollmentRegistration struct {
	Revision       int64
	SourceVersion  int64
	Authority      string
	ProofReference string
}

func (s *Store) OfficialBalanceWebEnrollment(ctx context.Context, id string, sourceVersion int64) (WebEnrollmentRegistration, error) {
	var out WebEnrollmentRegistration
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(e.revision,0),COALESCE(e.source_version,0),COALESCE(e.authority,''),COALESCE(e.proof_reference,'')
		FROM official_balance_sources s LEFT JOIN official_balance_web_enrollments e ON e.source_id=s.id
		WHERE s.id=? AND s.version=?`, id, sourceVersion).Scan(&out.Revision, &out.SourceVersion, &out.Authority, &out.ProofReference)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrBalanceConflict
	}
	return out, err
}

func (s *Store) SaveOfficialBalanceWebEnrollment(ctx context.Context, id string, expected int64, value WebEnrollmentRegistration) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Acquire the write lock and fence source replacement in the same SQLite
	// transaction as the registration CAS, including across manager processes.
	res, err := tx.ExecContext(ctx, `UPDATE official_balance_sources SET version=version WHERE id=? AND version=?`, id, value.SourceVersion)
	if err != nil {
		return err
	}
	count, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrBalanceConflict
	}
	var revision int64
	err = tx.QueryRowContext(ctx, `SELECT revision FROM official_balance_web_enrollments WHERE source_id=?`, id).Scan(&revision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if revision != expected {
		return ErrBalanceConflict
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO official_balance_web_enrollments(source_id,revision,source_version,authority,proof_reference)
		VALUES(?,?,?,?,?) ON CONFLICT(source_id) DO UPDATE SET revision=excluded.revision,source_version=excluded.source_version,authority=excluded.authority,proof_reference=excluded.proof_reference`,
		id, expected+1, value.SourceVersion, value.Authority, value.ProofReference)
	if err != nil {
		return err
	}
	return tx.Commit()
}
