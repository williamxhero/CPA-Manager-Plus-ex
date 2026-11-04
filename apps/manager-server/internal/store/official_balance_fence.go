package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// OfficialBalanceFence is the exact durable observation identity used by a
// provider-policy guard. It is host-private and must never be returned by an
// HTTP handler.
type OfficialBalanceFence struct {
	SourceID    string
	Version     int64
	Attempt     string
	LastSuccess string
}

var ErrProviderPolicyEvidenceConflict = errors.New("provider policy evidence conflict")

// OfficialBalanceWithFence reads the observation and its fence from one
// SQLite statement so callers cannot pair a parsed status with a newer row.
func (s *Store) OfficialBalanceWithFence(ctx context.Context, id string) (OfficialBalanceRecord, OfficialBalanceFence, error) {
	r := OfficialBalanceRecord{ID: id}
	f := OfficialBalanceFence{SourceID: id}
	var cipher string
	err := s.db.QueryRowContext(ctx, `SELECT version, config, secret_ref, secret_cipher, attempt, last_success FROM official_balance_sources WHERE id=?`, id).Scan(&r.Version, &r.Config, &r.SecretRef, &cipher, &r.Attempt, &r.LastSuccess)
	if errors.Is(err, sql.ErrNoRows) {
		return r, f, nil
	}
	r.Configured = cipher != ""
	f.Version, f.Attempt, f.LastSuccess = r.Version, r.Attempt, r.LastSuccess
	return r, f, err
}

// ValidateProviderPolicyEvidence closes the local side of a monetary guard in
// one SQLite transaction. It validates the exact policy document, live lease,
// and every source fence supplied by the caller. It deliberately does not
// require every source to be successful: the policy classifier owns that
// decision, while this method only prevents mixing snapshots.
func (s *Store) ValidateProviderPolicyEvidence(ctx context.Context, record ProviderPolicyRecord, lease CredentialPolicyLease, now int64, fences []OfficialBalanceFence) error {
	if record.ID == "" || len(fences) == 0 || len(fences) > 64 || now <= 0 {
		return ErrProviderPolicyEvidenceConflict
	}
	seen := make(map[string]struct{}, len(fences))
	for _, f := range fences {
		if f.SourceID == "" || f.Version < 0 || !jsonObjectOrEmpty(f.Attempt) || !jsonObjectOrEmpty(f.LastSuccess) {
			return ErrProviderPolicyEvidenceConflict
		}
		if _, ok := seen[f.SourceID]; ok {
			return ErrProviderPolicyEvidenceConflict
		}
		seen[f.SourceID] = struct{}{}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var generation int64
	var document string
	var enabled bool
	if err := tx.QueryRowContext(ctx, `SELECT generation,document,COALESCE(json_extract(document,'$.enabled'),0)=1 FROM provider_policies WHERE id=?`, record.ID).Scan(&generation, &document, &enabled); err != nil {
		return ErrProviderPolicyEvidenceConflict
	}
	if generation != record.Generation || document != record.Document || !enabled {
		return ErrProviderPolicyEvidenceConflict
	}
	var valid bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM credential_policy_lease WHERE singleton=1 AND holder=? AND epoch=? AND expires_ms>?)`, lease.Holder, lease.Epoch, now).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return ErrPolicyLease
	}
	for _, f := range fences {
		var version int64
		var cipher, attempt, lastSuccess string
		if err := tx.QueryRowContext(ctx, `SELECT version,secret_cipher,attempt,last_success FROM official_balance_sources WHERE id=?`, f.SourceID).Scan(&version, &cipher, &attempt, &lastSuccess); err != nil {
			return ErrProviderPolicyEvidenceConflict
		}
		if version != f.Version || cipher == "" || attempt != f.Attempt || lastSuccess != f.LastSuccess {
			return ErrProviderPolicyEvidenceConflict
		}
	}
	return tx.Commit()
}

func jsonObjectOrEmpty(v string) bool {
	return v == "" || strings.HasPrefix(strings.TrimSpace(v), "{")
}
