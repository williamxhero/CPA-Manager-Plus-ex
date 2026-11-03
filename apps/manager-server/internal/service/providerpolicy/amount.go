package providerpolicy

import (
	"context"
	"math/big"
	"regexp"

	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
)

var finiteAmount = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?$`)

// Compare each original currency independently, with no rounding or exchange
// rate. Exact rationals preserve precision around the fixed 0.1 boundary.
func classify(proof FundingProof) (bool, error) {
	allLow := true
	if !proof.Ready || len(proof.Sources) == 0 {
		return false, ErrUnavailable
	}
	for _, source := range proof.Sources {
		seen := map[string]bool{}
		if len(source.Balances) == 0 {
			return false, ErrUnavailable
		}
		for _, amount := range source.Balances {
			if amount.Currency == "" || seen[amount.Currency] || len(amount.Total) > 256 || !finiteAmount.MatchString(amount.Total) {
				return false, ErrUnavailable
			}
			seen[amount.Currency] = true
			value, ok := new(big.Rat).SetString(amount.Total)
			if !ok {
				return false, ErrUnavailable
			}
			if value.Cmp(big.NewRat(1, 10)) > 0 {
				allLow = false
			}
		}
	}
	return allLow, nil
}

func (s *Service) sourcesCurrent(ctx context.Context, b Binding, saved intent) bool {
	if s.Balance == nil || s.Clock == nil || len(saved.Versions) != len(b.Targets)-1 {
		return false
	}
	for _, target := range b.Targets[1:] {
		binding := ob.Binding{Scope: b.Scope, Targets: []rc.Target{target}}
		status, err := s.Balance.Status(ctx, binding)
		if err != nil || saved.Versions[target.ResourceID] != status.Version || !currentFunding(status, binding, s.Clock()) {
			return false
		}
		if _, err := classify(FundingProof{Ready: true, Sources: []FundingSource{{Balances: status.LastSuccess.Balances}}}); err != nil {
			return false
		}
	}
	return true
}
