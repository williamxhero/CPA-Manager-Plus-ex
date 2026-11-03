package providerpolicy

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	pg "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/providergate"
	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
)

type FundingSource struct {
	MemberIDs    []string                  `json:"memberIds"`
	Version      int64                     `json:"version"`
	QueryID      string                    `json:"queryId"`
	ObservedAtMS int64                     `json:"observedAtMs"`
	Balances     []balance.CurrencyBalance `json:"balances"`
	Fresh        bool                      `json:"fresh"`
	KeyMatched   bool                      `json:"keyMatched"`
}

// FundingProof is an ephemeral read result. It is not an account/wallet ID,
// sum, policy activation, durable permission or applied gate acknowledgement.
type FundingProof struct {
	Binding
	Ready         bool            `json:"ready"`
	Authority     string          `json:"authority"`
	SourceVersion string          `json:"sourceVersion"`
	VerifiedAtMS  int64           `json:"verifiedAtMs"`
	Sources       []FundingSource `json:"sources"`
}

func currentFunding(source ob.Status, binding ob.Binding, now time.Time) bool {
	last := source.LastSuccess
	if !source.Configured || source.Version <= 0 || source.Adapter != "deepseek" ||
		source.Endpoint != "https://api.deepseek.com/user/balance" || source.Stale || last == nil ||
		last.QueryStatus != "success" || last.Semantic != "account_wallet" || last.Available == nil ||
		last.WindowsJSON != "" || len(last.Balances) == 0 || len(last.Balances) > 32 ||
		last.ObservedAtMS <= 0 || last.ObservedAtMS > now.UnixMilli() || now.UnixMilli()-last.ObservedAtMS >= int64(time.Hour/time.Millisecond) ||
		uuid.Validate(last.QueryID) != nil || source.Attempt == nil || source.Attempt.QueryID != last.QueryID {
		return false
	}
	var observed ob.Binding
	return json.Unmarshal(last.Binding, &observed) == nil && reflect.DeepEqual(observed, binding)
}

func (s *Service) ProveFunding(ctx context.Context, requested Binding) (FundingProof, error) {
	if s.Balance == nil || s.Gate == nil || s.Clock == nil {
		return FundingProof{}, ErrUnavailable
	}
	b, _, err := s.binding(ctx, requested.ProviderID)
	if err != nil || !reflect.DeepEqual(canonical(requested), b) {
		return FundingProof{}, ErrIdentity
	}
	sources := map[string]ob.Status{}
	version, err := s.Gate.VerifyRuntimeKeys(ctx, pg.Binding{Scope: b.Scope, Targets: b.Targets, ProviderID: b.ProviderID}, func(q pg.RuntimeKeyChallenge) (string, error) {
		binding := ob.Binding{Scope: b.Scope, Targets: []rc.Target{q.Target}}
		source, err := s.Balance.Status(ctx, binding)
		if err != nil || !currentFunding(source, binding, s.Clock()) {
			return "", ErrUnavailable
		}
		sources[q.Target.ResourceID] = source
		return s.Balance.RuntimeKeyDigest(ctx, binding, source.Version, q.Nonce, q.SourceVersion, q.AuthIndex)
	})
	if err != nil || len(sources) != len(b.Targets)-1 {
		return FundingProof{}, ErrUnavailable
	}
	out := FundingProof{Binding: b, Ready: true, Authority: "runtime_key_matched_official_balances", SourceVersion: version, VerifiedAtMS: s.Clock().UnixMilli()}
	for id, saved := range sources {
		current, err := s.Balance.Status(ctx, saved.Binding)
		if err != nil || !currentFunding(current, saved.Binding, s.Clock()) || !reflect.DeepEqual(current, saved) {
			return FundingProof{}, ErrUnavailable
		}
		last := current.LastSuccess
		out.Sources = append(out.Sources, FundingSource{[]string{id}, current.Version, last.QueryID, last.ObservedAtMS, last.Balances, true, true})
	}
	closing, _, err := s.binding(ctx, b.ProviderID)
	if err != nil || !reflect.DeepEqual(b, closing) || ctx.Err() != nil {
		return FundingProof{}, ErrIdentity
	}
	sort.Slice(out.Sources, func(i, j int) bool { return out.Sources[i].MemberIDs[0] < out.Sources[j].MemberIDs[0] })
	return out, nil
}
