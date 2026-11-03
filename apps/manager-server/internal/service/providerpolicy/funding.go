package providerpolicy

import (
	"context"
	"encoding/json"
	"math/big"
	"reflect"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	pg "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/providergate"
	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
)

type FundingStatus struct {
	MemberIDs    []string                  `json:"memberIds"`
	Query        ob.Query                  `json:"query"`
	KeyMatched   bool                      `json:"keyMatched"`
	Fresh        bool                      `json:"fresh"`
	Reason       string                    `json:"reason"`
	Balances     []balance.CurrencyBalance `json:"balances,omitempty"`
	ObservedAtMS int64                     `json:"observedAtMs,omitempty"`
	QueryID      string                    `json:"queryId,omitempty"`
	low          bool
	high         bool
}

type fundingView struct {
	gate    pg.Status
	sources []FundingStatus
	covered bool
	allLow  bool
}

var decimal = regexp.MustCompile(`^-?(0|[1-9][0-9]{0,59})(\.[0-9]{1,30})?$`)
var currency = regexp.MustCompile(`^[A-Z]{3}$`)

func monetary(status ob.Status, now time.Time) (bool, bool) {
	obs := status.LastSuccess
	if !status.Configured || status.Stale || status.Adapter != "deepseek" || status.Endpoint != "https://api.deepseek.com/user/balance" || obs == nil || status.Attempt == nil || obs.QueryID == "" || status.Attempt.QueryID != obs.QueryID || status.Attempt.QueryStatus != "success" || obs.QueryStatus != "success" || obs.CapabilityStatus != "supported" || obs.Semantic != "account_wallet" || obs.ObservedAtMS <= 0 || now.UnixMilli()-obs.ObservedAtMS >= int64(time.Hour/time.Millisecond) || obs.ObservedAtMS > now.Add(time.Minute).UnixMilli() || len(obs.Balances) == 0 || len(obs.Balances) > 32 {
		return false, false
	}
	var binding ob.Binding
	if json.Unmarshal(obs.Binding, &binding) != nil || !sameSourceBinding(binding, status.Binding) {
		return false, false
	}
	seen := map[string]bool{}
	high := false
	for _, b := range obs.Balances {
		if !currency.MatchString(b.Currency) || seen[b.Currency] || !decimal.MatchString(b.Total) {
			return false, false
		}
		seen[b.Currency] = true
		value, ok := new(big.Rat).SetString(b.Total)
		if !ok {
			return false, false
		}
		if value.Cmp(big.NewRat(1, 10)) > 0 {
			high = true
		}
	}
	return !high, high
}

func sameSourceBinding(a, b ob.Binding) bool {
	if a.Scope != b.Scope || len(a.Targets) != len(b.Targets) {
		return false
	}
	seen := map[rc.Target]bool{}
	for _, t := range a.Targets {
		if seen[t] {
			return false
		}
		seen[t] = true
	}
	for _, t := range b.Targets {
		if !seen[t] {
			return false
		}
	}
	return true
}

// Source configuration selects where to read; only current runtime key equality
// earns coverage. Never sum amounts, infer wallet ownership, or convert currency.
func (s *Service) funding(ctx context.Context, b Binding) (fundingView, error) {
	var out fundingView
	if s.gate == nil || s.official == nil {
		return out, ErrUnavailable
	}
	verification, err := s.gate.OpenRuntimeKeyVerification(ctx, pg.Binding(b))
	if err != nil {
		return out, err
	}
	state, members := verification.Status(), verification.Members()
	out.gate = state
	queries := []ob.Binding{{Scope: b.Scope, Targets: b.Targets}}
	for _, member := range members {
		queries = append(queries, ob.Binding{Scope: b.Scope, Targets: member.Targets})
	}
	covered := map[string]bool{}
	low := map[string]bool{}
	unknown := map[string]bool{}
	for _, binding := range queries {
		status, err := s.official.InventoryStatus(ctx, binding, verification.Inventory())
		if err != nil {
			return out, err
		}
		if !status.Configured || status.Adapter != "deepseek" || status.Endpoint != "https://api.deepseek.com/user/balance" {
			continue
		}
		q := ob.Query{Binding: status.Binding, Version: status.Version}
		source := FundingStatus{Query: q, Reason: "runtime_key_unverified", MemberIDs: []string{}}
		for _, member := range members {
			// A per-member source may not claim siblings. A full issued group is
			// only a candidate: its secret must independently match each member.
			if len(binding.Targets) == 1 && !reflect.DeepEqual(binding.Targets, member.Targets) {
				continue
			}
			nonce := uuid.NewString()
			digest, err := s.official.RuntimeKeyDigestForInventory(ctx, q, verification.Inventory(), nonce, state.SourceVersion, member.AuthIndex)
			if err != nil {
				continue
			}
			matched, err := verification.Match(ctx, member.AuthIndex, nonce, digest)
			if err != nil {
				return out, err
			}
			if matched {
				source.MemberIDs = append(source.MemberIDs, member.Targets[0].ResourceID)
				covered[member.Targets[0].ResourceID] = true
			}
		}
		source.KeyMatched = len(source.MemberIDs) > 0
		if source.KeyMatched {
			source.Reason = "current_runtime_key_matched"
			source.low, source.high = monetary(status, time.Now())
			source.Fresh = source.low || source.high
			if status.LastSuccess != nil {
				source.Balances = status.LastSuccess.Balances
				source.ObservedAtMS = status.LastSuccess.ObservedAtMS
				source.QueryID = status.LastSuccess.QueryID
			}
			for _, id := range source.MemberIDs {
				if source.low {
					low[id] = true
				} else {
					unknown[id] = true
				}
			}
		}
		out.sources = append(out.sources, source)
	}
	// A source replacement during a challenge invalidates the whole read. Do
	// not mix old monetary observations with a newly configured query secret.
	for _, source := range out.sources {
		current, e := s.official.InventoryStatus(ctx, source.Query.Binding, verification.Inventory())
		if e != nil || current.Version != source.Query.Version || !current.Configured {
			return out, ErrUnavailable
		}
		if source.KeyMatched {
			low, high := monetary(current, time.Now())
			if current.LastSuccess == nil && source.QueryID != "" || current.LastSuccess != nil && current.LastSuccess.QueryID != source.QueryID || low != source.low || high != source.high {
				return out, ErrUnavailable
			}
		}
	}
	if err := verification.Close(ctx); err != nil {
		return out, err
	}
	out.covered = len(members) > 0
	out.allLow = out.covered
	for _, m := range members {
		id := m.Targets[0].ResourceID
		out.covered = out.covered && covered[id]
		out.allLow = out.allLow && covered[id] && low[id] && !unknown[id]
	}
	return out, nil
}
