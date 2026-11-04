package store

import (
	"context"
)

// ValidateProviderRecovery fences a saved OFF intent and live lease without
// granting automatic monetary evaluation authority or requiring fresh balances.
func (s *Store) ValidateProviderRecovery(ctx context.Context, r ProviderPolicyRecord, lease CredentialPolicyLease, now int64) error {
	if r.ID == "" || r.Generation <= 0 || now <= 0 {
		return ErrProviderPolicyEvidenceConflict
	}
	var valid bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM credential_policy_lease l,provider_policies p WHERE l.singleton=1 AND l.holder=? AND l.epoch=? AND l.expires_ms>? AND p.id=? AND p.generation=? AND p.document=? AND COALESCE(json_extract(p.document,'$.enabled'),0)=0)`, lease.Holder, lease.Epoch, now, r.ID, r.Generation, r.Document).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return ErrPolicyLease
	}
	return nil
}
