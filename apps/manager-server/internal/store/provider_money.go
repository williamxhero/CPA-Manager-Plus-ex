package store

import (
	"context"
	"database/sql"
	"errors"
)

func (s *Store) ProviderPolicies(ctx context.Context) ([]ProviderPolicyRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,generation,document FROM provider_policies`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProviderPolicyRecord
	for rows.Next() {
		var r ProviderPolicyRecord
		if err := rows.Scan(&r.ID, &r.Generation, &r.Document); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Capture before external I/O against the exact enabled intent. A joined job
// or repeated callback cannot recapture a newer policy generation.
func (s *Store) CaptureProviderMoney(ctx context.Context, attempt string, r ProviderPolicyRecord, doc string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO provider_money_queries(attempt,policy_id,generation,document,consumed) SELECT ?,id,generation,?,'' FROM provider_policies WHERE id=? AND generation=? AND document=? ON CONFLICT(attempt) DO NOTHING`, attempt, doc, r.ID, r.Generation, r.Document)
	return err
}

func (s *Store) ProviderMoneyQuery(ctx context.Context, attempt string) (string, string, error) {
	var doc, consumed string
	err := s.db.QueryRowContext(ctx, `SELECT document,consumed FROM provider_money_queries WHERE attempt=?`, attempt).Scan(&doc, &consumed)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	return doc, consumed, err
}

// Consumption precedes any Core command. A crash or unknown ACK never grants
// permission to replay this evidence under a new immutable command ID.
func (s *Store) ConsumeProviderMoney(ctx context.Context, attempt, evidence, doc string, r ProviderPolicyRecord, lease CredentialPolicyLease, now int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var valid bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM credential_policy_lease l,provider_policies p WHERE l.singleton=1 AND l.holder=? AND l.epoch=? AND l.expires_ms>? AND p.id=? AND p.generation=? AND p.document=?)`, lease.Holder, lease.Epoch, now, r.ID, r.Generation, r.Document).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return ErrPolicyLease
	}
	res, err := tx.ExecContext(ctx, `UPDATE provider_money_queries SET consumed=? WHERE attempt=? AND policy_id=? AND generation=? AND consumed=''`, evidence, attempt, r.ID, r.Generation)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return ErrProviderPolicyConflict
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO provider_money_decisions(evidence,policy_id,generation,document) VALUES(?,?,?,?)`, evidence, r.ID, r.Generation, doc); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ValidateProviderMoney(ctx context.Context, r ProviderPolicyRecord, lease CredentialPolicyLease, now int64, fences []OfficialBalanceFence) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var valid bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM credential_policy_lease l,provider_policies p WHERE l.singleton=1 AND l.holder=? AND l.epoch=? AND l.expires_ms>? AND p.id=? AND p.generation=? AND p.document=?)`, lease.Holder, lease.Epoch, now, r.ID, r.Generation, r.Document).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return ErrPolicyLease
	}
	return validateOfficialBalanceFences(ctx, tx, fences)
}

// ValidateProviderRecovery fences a saved OFF intent without granting automatic
// money evaluation permission. ValidateProviderMoney still requires sources.
func (s *Store) ValidateProviderRecovery(ctx context.Context, r ProviderPolicyRecord, lease CredentialPolicyLease, now int64) error {
	var valid bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM credential_policy_lease l,provider_policies p WHERE l.singleton=1 AND l.holder=? AND l.epoch=? AND l.expires_ms>? AND p.id=? AND p.generation=? AND p.document=? AND json_type(p.document,'$.enabled')='false')`, lease.Holder, lease.Epoch, now, r.ID, r.Generation, r.Document).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return ErrPolicyLease
	}
	return nil
}

type ProviderMoneyEvaluation struct {
	AttemptID  string `json:"attemptId"`
	Generation int64  `json:"generation"`
	State      string `json:"state"`
	Consumed   bool   `json:"consumed"`
}

func (s *Store) FinishProviderMoney(ctx context.Context, attempt string, rejected bool) error {
	state := "evaluated"
	if rejected {
		state = "rejected"
	}
	_, err := s.db.ExecContext(ctx, `UPDATE provider_money_queries SET outcome=? WHERE attempt=? AND outcome='pending'`, state, attempt)
	return err
}

func (s *Store) ProviderMoneyEvaluation(ctx context.Context, id string) (*ProviderMoneyEvaluation, error) {
	var out ProviderMoneyEvaluation
	var consumed string
	err := s.db.QueryRowContext(ctx, `SELECT attempt,generation,outcome,consumed FROM provider_money_queries WHERE policy_id=? ORDER BY rowid DESC LIMIT 1`, id).Scan(&out.AttemptID, &out.Generation, &out.State, &consumed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	out.Consumed = consumed != ""
	return &out, err
}
