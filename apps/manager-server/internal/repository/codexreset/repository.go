package codexreset

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

type LedgerEntry struct {
	CredentialKey   string
	CycleKey        string
	RedeemRequestID string
	Status          string
	AvailableCount  *int64
	DetailJSON      string
	ConsumedAtMS    int64
}

type Repository interface {
	RecordObservation(ctx context.Context, entry LedgerEntry) error
	ClaimConsumption(ctx context.Context, credentialKey, cycleKey, requestID string) (bool, error)
	MarkConsumed(ctx context.Context, credentialKey, cycleKey, requestID string, atMS int64) error
	MarkFailed(ctx context.Context, credentialKey, cycleKey, requestID string, detail string) error
}

type repository struct{ db *sql.DB }

func New(db *sql.DB) Repository { return &repository{db: db} }

func (r *repository) RecordObservation(ctx context.Context, entry LedgerEntry) error {
	if strings.TrimSpace(entry.CredentialKey) == "" || strings.TrimSpace(entry.CycleKey) == "" {
		return nil
	}
	_, err := r.db.ExecContext(ctx, `insert into codex_reset_credit_ledger
		(credential_key, cycle_key, redeem_request_id, status, available_count, detail_json, consumed_at_ms, created_at_ms, updated_at_ms)
		values (?, ?, ?, ?, ?, ?, ?, ?, ?)
		on conflict(credential_key, cycle_key, redeem_request_id) do update set
		status=excluded.status, available_count=excluded.available_count,
		detail_json=excluded.detail_json, updated_at_ms=excluded.updated_at_ms`,
		entry.CredentialKey, entry.CycleKey, entry.RedeemRequestID, entry.Status, entry.AvailableCount,
		entry.DetailJSON, entry.ConsumedAtMS, time.Now().UnixMilli(), time.Now().UnixMilli())
	return err
}

func (r *repository) ClaimConsumption(ctx context.Context, credentialKey, cycleKey, requestID string) (bool, error) {
	res, err := r.db.ExecContext(ctx, `insert or ignore into codex_reset_credit_ledger
		(credential_key, cycle_key, redeem_request_id, status, created_at_ms, updated_at_ms)
		select ?, ?, ?, 'claimed', ?, ?
		where not exists (select 1 from codex_reset_credit_ledger where credential_key=? and cycle_key=? and status in ('claimed', 'consumed'))`,
		credentialKey, cycleKey, requestID, time.Now().UnixMilli(), time.Now().UnixMilli(), credentialKey, cycleKey)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (r *repository) MarkConsumed(ctx context.Context, credentialKey, cycleKey, requestID string, atMS int64) error {
	_, err := r.db.ExecContext(ctx, `update codex_reset_credit_ledger set status='consumed', consumed_at_ms=?, updated_at_ms=?
		where credential_key=? and cycle_key=? and redeem_request_id=? and status='claimed'`, atMS, time.Now().UnixMilli(), credentialKey, cycleKey, requestID)
	return err
}

func (r *repository) MarkFailed(ctx context.Context, credentialKey, cycleKey, requestID string, detail string) error {
	_, err := r.db.ExecContext(ctx, `update codex_reset_credit_ledger set status='failed', detail_json=?, updated_at_ms=?
		where credential_key=? and cycle_key=? and redeem_request_id=? and status='claimed'`, detail, time.Now().UnixMilli(), credentialKey, cycleKey, requestID)
	return err
}
