package store

import (
	"context"
	"errors"
)

type ProviderPolicyMigrationStatus struct {
	Dialect        string `json:"dialect"`
	Ready          bool   `json:"ready"`
	PendingCount   int    `json:"pendingCount"`
	Reason         string `json:"reason,omitempty"`
	ProtectionHold bool   `json:"protectionHold"`
}

// ProviderPolicyMigrationReadiness is consulted before admitting provider
// inference traffic after upgrade/restart. Pending or unknown dialect with
// existing policies keeps the installation not-ready / fail-closed.
func (s *Store) ProviderPolicyMigrationReadiness(ctx context.Context) (ProviderPolicyMigrationStatus, error) {
	var out ProviderPolicyMigrationStatus
	dialect, err := s.providerPolicyDialect(ctx)
	if err != nil {
		return out, err
	}
	out.Dialect = dialect
	if err := s.ensureProviderPolicyMigrationTables(ctx); err != nil {
		return out, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM provider_policy_migration_pending`).Scan(&out.PendingCount); err != nil {
		return out, err
	}
	var policies int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM provider_policies`).Scan(&policies); err != nil {
		return out, err
	}
	switch {
	case out.PendingCount > 0:
		out.Reason = "migration_pending_rows"
		out.ProtectionHold = true
	case policies > 0 && dialect == ProviderPolicyDialectUnknown:
		out.Reason = "dialect_unknown_with_policies"
		out.ProtectionHold = true
	case dialect == ProviderPolicyDialectDesktopV1 || policies == 0:
		out.Ready = true
	default:
		out.Reason = "dialect_not_ready"
		out.ProtectionHold = true
	}
	return out, nil
}

var ErrProviderPolicyNotReady = errors.New("provider policy migration not ready")
