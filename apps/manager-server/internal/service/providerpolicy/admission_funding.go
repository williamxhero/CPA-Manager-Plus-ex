package providerpolicy

import (
	"context"
	"reflect"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	pg "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/providergate"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

// Mandatory exhaustion and the optional #12 policy share acquisition and the
// durable completion inbox. Their decision scopes and gate owners are separate.
func (s *Service) consumeFunding(ctx context.Context, event completion, lease store.CredentialPolicyLease) error {
	if s.official == nil || len(event.Query.Targets) == 0 {
		return nil
	}
	n := s.Identity.Inventory(ctx, true)
	if n == nil || n.Snapshot == nil || !n.Snapshot.Complete || n.Identity.State != "available" {
		return ErrUnavailable
	}
	if n.Snapshot.Scope != event.Query.Scope {
		return nil
	}
	status, err := s.official.InventoryStatus(ctx, event.Query.Binding, n)
	if err == ob.ErrIdentity {
		// Obsolete bindings cannot authorize a first commit.
		return nil
	}
	if err != nil {
		return err
	}
	adapter := ob.FundingAdapter(status)
	if !status.Configured || status.Version != event.Query.Version || adapter == "" ||
		status.Stale || status.Attempt == nil || status.Attempt.QueryID != event.AttemptID || status.Attempt.QueryStatus != "success" {
		return nil
	}
	owner := ""
	for _, row := range n.Snapshot.Resources {
		if row.Target == event.Query.Targets[0] {
			owner = row.ParentID
			if row.Kind == "provider" {
				owner = row.ResourceID
			}
			break
		}
	}
	if owner == "" {
		return nil
	}
	group, _, err := bindingFromInventory(n, owner)
	if err != nil || !containsSource(group, event.Query) {
		return nil
	}
	if len(event.Query.Targets) > 1 && !sameSourceBinding(event.Query.Binding, ob.Binding{Scope: group.Scope, Targets: group.Targets}) {
		return nil
	}
	verification, err := s.gate.OpenRuntimeKeyVerification(ctx, pg.Binding(group))
	if err != nil {
		return err
	}
	return s.official.WithFundingReading(ctx, event.Query, verification.Inventory(), event.AttemptID, func(reading balance.Observation, sign func(string, string, string) (string, error)) error {
		for _, member := range verification.Members() {
			if len(event.Query.Targets) == 1 && event.Query.Targets[0].ResourceID != owner && !reflect.DeepEqual(event.Query.Targets, member.Targets) {
				continue
			}
			if err := s.Store.CheckCredentialPolicyLease(ctx, lease, time.Now().UnixMilli()); err != nil {
				return err
			}
			if err := verification.PublishFundingReading(ctx, member.AuthIndex, adapter, reading, sign); err != nil {
				return err
			}
		}
		return nil
	})
}
