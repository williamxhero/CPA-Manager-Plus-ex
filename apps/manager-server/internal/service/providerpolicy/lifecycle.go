package providerpolicy

import (
	"context"
	"encoding/json"
	pg "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/providergate"
	"time"
)

// Run owns receipt reconciliation only. Source acquisition and automatic
// decisions remain in the existing fullrefresh lifecycle/completion observers.
func (s *Service) Run(ctx context.Context) error {
	s.runMu.Lock()
	if s.running {
		s.runMu.Unlock()
		return ErrUnavailable
	}
	s.running = true
	s.runMu.Unlock()
	defer func() { s.runMu.Lock(); s.running = false; s.runMu.Unlock() }()
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		attempt, cancel := context.WithTimeout(ctx, 15*time.Second)
		_ = s.reconcile(attempt)
		cancel()
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}

func (s *Service) reconcile(ctx context.Context) error {
	rows, err := s.Store.ProviderPolicies(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return err
		}
		var saved intent
		if json.Unmarshal([]byte(row.Document), &saved) != nil || len(saved.Targets) < 2 {
			continue
		}
		// A stored command never authorizes adopting new members/versions.
		b, _, err := s.binding(ctx, saved.ProviderID)
		if err != nil || !sameAuthority(saved.Binding, b) {
			continue
		}
		if s.monetary != nil {
			_ = s.monetary.ReconcileMonetary(ctx, pg.Binding{Scope: b.Scope, Targets: b.Targets, ProviderID: b.ProviderID})
		}
	}
	return ctx.Err()
}
