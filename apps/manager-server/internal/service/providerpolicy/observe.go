package providerpolicy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"reflect"
	"time"

	fr "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fullrefresh"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	pg "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/providergate"
	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

type queryFence struct {
	PolicyID   string   `json:"policyId"`
	Generation int64    `json:"generation"`
	Query      ob.Query `json:"query"`
}

func (s *Service) BindCoordinator(c *fr.Service) {
	c.SubscribeStarted(func(ctx context.Context, event fr.StartedObservation) error {
		if event.Source.Kind == fr.Official && event.Source.Official != nil {
			if err := s.capture(ctx, *event.Source.Official, event.AttemptID); err != nil {
				log.Print("provider monetary query association unavailable; independent billing preserved")
			}
		}
		return nil
	})
	_ = c.SubscribeBounded(func(ctx context.Context, event fr.CompletedObservation) {
		if err := s.observe(ctx, event); err != nil {
			log.Print("provider monetary completion unavailable; no automatic action confirmed")
		}
	}, 20*time.Second)
}

func (s *Service) capture(ctx context.Context, q ob.Query, attempt string) error {
	if len(q.Targets) != 1 {
		return nil
	}
	rows, err := s.Store.ProviderPolicies(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		var saved intent
		if json.Unmarshal([]byte(row.Document), &saved) != nil {
			return ErrUnavailable
		}
		if !saved.Enabled || len(saved.Targets) < 2 {
			continue
		}
		for _, target := range saved.Targets[1:] {
			if target != q.Targets[0] {
				continue
			}
			current := saved.Binding
			current.Scope = q.Scope
			if !sameAuthority(current, saved.Binding) {
				continue
			}
			doc, _ := json.Marshal(queryFence{row.ID, row.Generation, q})
			return s.Store.CaptureProviderMoney(ctx, attempt, row, string(doc))
		}
	}
	return nil
}

func (s *Service) fence(ctx context.Context, attempt string, row store.ProviderPolicyRecord, q ob.Query) bool {
	doc, _, err := s.Store.ProviderMoneyQuery(ctx, attempt)
	var f queryFence
	return err == nil && doc != "" && json.Unmarshal([]byte(doc), &f) == nil && f.PolicyID == row.ID && f.Generation == row.Generation && reflect.DeepEqual(f.Query, q)
}

func (s *Service) observe(ctx context.Context, event fr.CompletedObservation) (outcome error) {
	if event.Source.Kind != fr.Official || event.Source.Official == nil || event.Official == nil {
		return nil
	}
	doc, consumed, err := s.Store.ProviderMoneyQuery(ctx, event.AttemptID)
	if err != nil || doc == "" || consumed != "" {
		return err
	}
	defer func() {
		// Only the sanitized audit outcome gets a small finalization budget;
		// cancellation never authorizes a proof, decision or Core command.
		finish, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := s.Store.FinishProviderMoney(finish, event.AttemptID, outcome != nil); err != nil && outcome == nil {
			outcome = err
		}
	}()
	var f queryFence
	if json.Unmarshal([]byte(doc), &f) != nil || !reflect.DeepEqual(f.Query, *event.Source.Official) {
		return ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lease, err := s.Store.AcquireCredentialPolicyLease(ctx, s.holder, time.Now().UnixMilli(), 30000)
	if err != nil {
		return err
	}
	defer func() {
		release, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = s.Store.ReleaseCredentialPolicyLease(release, lease)
	}()
	row, err := s.Store.ProviderPolicy(ctx, f.PolicyID)
	if err != nil {
		return err
	}
	var saved intent
	if row.Generation != f.Generation || json.Unmarshal([]byte(row.Document), &saved) != nil || !saved.Enabled {
		return nil
	}
	b, disabled, err := s.binding(ctx, saved.ProviderID)
	if err != nil || !sameAuthority(b, saved.Binding) || disabled {
		return ErrUnavailable
	}
	// Only THIS completion's fresh exact high value can authorize recovery.
	// A cached high sibling cannot release a historical monetary restriction.
	status, err := s.Balance.Status(ctx, f.Query.Binding)
	if err != nil || status.Version != f.Query.Version || !currentFunding(status, f.Query.Binding, s.Clock()) || status.LastSuccess.QueryID != event.AttemptID {
		return ErrUnavailable
	}
	triggerLow, err := classify(FundingProof{Ready: true, Sources: []FundingSource{{Balances: status.LastSuccess.Balances}}})
	if err != nil {
		return err
	}
	only := ""
	if !triggerLow {
		only = f.Query.Targets[0].ResourceID
	}
	proof, err := s.proveFunding(ctx, b, only)
	if err != nil {
		return err
	}
	if proof.SourceVersion != saved.SourceVersion {
		return ErrUnavailable
	}
	low, err := classify(proof)
	if err != nil {
		return err
	}
	if low != triggerLow {
		return nil
	}
	trigger := false
	for _, source := range proof.Sources {
		id := source.MemberIDs[0]
		if source.Version != saved.Versions[id] {
			return ErrUnavailable
		}
		var q ob.Query
		for _, target := range b.Targets[1:] {
			if target.ResourceID == id {
				q = ob.Query{Binding: ob.Binding{Scope: b.Scope, Targets: []rc.Target{target}}, Version: source.Version}
			}
		}
		if low && !s.fence(ctx, source.QueryID, row, q) {
			return nil
		}
		if source.QueryID == event.AttemptID && reflect.DeepEqual(q, f.Query) {
			trigger = true
		}
	}
	if !trigger {
		return nil
	}
	gateBinding := pg.Binding{Scope: b.Scope, Targets: b.Targets, ProviderID: b.ProviderID}
	gate, err := s.monetary.Status(ctx, gateBinding)
	if err != nil {
		return err
	}
	action := "clear"
	if low {
		action = "pause"
	}
	decision, _ := json.Marshal(struct {
		Generation int64
		Proof      FundingProof
		Action     string
	}{row.Generation, proof, action})
	digest := sha256.Sum256(decision)
	evidence := hex.EncodeToString(digest[:])
	if err := s.Store.ConsumeProviderMoney(ctx, event.AttemptID, evidence, string(decision), row, lease, time.Now().UnixMilli()); err != nil {
		return err
	}
	if gate.OwnPaused == low {
		return nil
	}
	authorize := func(ctx context.Context) error {
		if err := s.Store.ValidateProviderMoney(ctx, row, lease, time.Now().UnixMilli(), proof.fences); err != nil {
			return err
		}
		inventory := s.Identity.Inventory(ctx, true)
		current, disabled, err := bindingInventory(inventory, b.ProviderID)
		if err != nil || disabled || !reflect.DeepEqual(b, current) || !s.Identity.SameInventoryConnection(proof.inventory, inventory) {
			return ErrIdentity
		}
		for _, source := range proof.Sources {
			var binding ob.Binding
			for _, target := range b.Targets[1:] {
				if target.ResourceID == source.MemberIDs[0] {
					binding = ob.Binding{Scope: b.Scope, Targets: []rc.Target{target}}
				}
			}
			status, err := s.Balance.Status(ctx, binding)
			if err != nil || status.Version != source.Version || !currentFunding(status, binding, s.Clock()) || status.LastSuccess.QueryID != source.QueryID {
				return ErrUnavailable
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return s.Store.ValidateProviderMoney(ctx, row, lease, time.Now().UnixMilli(), proof.fences)
	}
	_, err = s.monetary.ApplyMonetary(ctx, gateBinding, proof.SourceVersion, action, evidence, authorize)
	return err
}
