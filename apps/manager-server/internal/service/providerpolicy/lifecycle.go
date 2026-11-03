package providerpolicy

import (
	"context"
	"encoding/json"
	pg "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/providergate"
	"sort"
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
	cursor := ""
	for {
		attempt, cancel := context.WithTimeout(ctx, 15*time.Second)
		_ = s.reconcile(attempt, &cursor)
		cancel()
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}

func (s *Service) reconcile(ctx context.Context, cursor *string) error {
	rows, err := s.Store.ProviderPolicies(ctx)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	// Advance before external I/O. One slow subject may consume this bounded
	// attempt, but never owns the head of every subsequent scan. The lexical
	// cursor remains valid as policies are inserted or removed between ticks.
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	next := sort.Search(len(rows), func(i int) bool { return rows[i].ID > *cursor })
	if next == len(rows) {
		next = 0
	}
	row := rows[next]
	*cursor = row.ID
	var saved intent
	if json.Unmarshal([]byte(row.Document), &saved) != nil || len(saved.Targets) < 2 {
		return nil
	}
	// A stored command never authorizes adopting new members/versions.
	b, _, err := s.binding(ctx, saved.ProviderID)
	if err != nil || !sameAuthority(saved.Binding, b) {
		return err
	}
	if s.monetary != nil {
		return s.monetary.ReconcileMonetary(ctx, pg.Binding{Scope: b.Scope, Targets: b.Targets, ProviderID: b.ProviderID})
	}
	return nil
}
