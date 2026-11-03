package providerpolicy

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
)

// Deterministic decimal/freshness boundary coverage supplements (and does not
// replace) the public API -> SQLite -> ordinary Core monetary tests.
func TestMonetaryExactNativeCurrencyAndUnknown(t *testing.T) {
	now := time.UnixMilli(1793690000000)
	seed := func() ob.Status {
		binding := ob.Binding{Scope: rc.Scope{InstanceID: "controlled-instance"}, Targets: []rc.Target{{ResourceID: "member", CredentialVersion: 1}}}
		raw, _ := json.Marshal(binding)
		observation := &balance.Observation{QueryID: "actual-read", QueryStatus: "success", CapabilityStatus: "supported", Semantic: "account_wallet", ObservedAtMS: now.UnixMilli(), Binding: raw, Balances: []balance.CurrencyBalance{{Currency: "USD", Total: "0.1"}}}
		return ob.Status{Config: ob.Config{Binding: binding, Adapter: "deepseek", Endpoint: "https://api.deepseek.com/user/balance"}, Configured: true, Attempt: &balance.Observation{QueryID: "actual-read", QueryStatus: "success"}, LastSuccess: observation}
	}
	for _, tc := range []struct {
		name, value string
		low, high   bool
	}{
		{"debt", "-0.05", true, false}, {"negative_zero", "-0", true, false}, {"zero", "0", true, false},
		{"low", "0.05", true, false}, {"threshold", "0.1", true, false}, {"above_exact", "0.100000000000000000000000000001", false, true},
		{"high", "10.00", false, true}, {"missing", "", false, false}, {"NULL", "null", false, false},
		{"NaN", "NaN", false, false}, {"infinity", "Infinity", false, false}, {"exponent", "1e-1", false, false},
		{"fraction", "1/10", false, false}, {"leading_zero", "00.1", false, false}, {"overprecision", "0.1000000000000000000000000000001", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := seed()
			s.LastSuccess.Balances[0].Total = tc.value
			low, high := monetary(s, now)
			if low != tc.low || high != tc.high {
				t.Fatalf("got %v/%v want %v/%v", low, high, tc.low, tc.high)
			}
		})
	}
	for _, tc := range []struct {
		name   string
		mutate func(*ob.Status)
	}{
		{"unconfigured", func(s *ob.Status) { s.Configured = false }},
		{"stale", func(s *ob.Status) { s.Stale = true }},
		{"wrong_adapter", func(s *ob.Status) { s.Adapter = "deepinfra" }},
		{"wrong_endpoint", func(s *ob.Status) { s.Endpoint = "https://example.test/user/balance" }},
		{"no_success", func(s *ob.Status) { s.LastSuccess = nil }},
		{"no_attempt", func(s *ob.Status) { s.Attempt = nil }},
		{"new_failure", func(s *ob.Status) { s.Attempt.QueryStatus = "network_error" }},
		{"different_attempt", func(s *ob.Status) { s.Attempt.QueryID = "later-read" }},
		{"failed_success", func(s *ob.Status) { s.LastSuccess.QueryStatus = "parse_error" }},
		{"unsupported", func(s *ob.Status) { s.LastSuccess.CapabilityStatus = "unsupported" }},
		{"wrong_semantic", func(s *ob.Status) { s.LastSuccess.Semantic = "key_limit" }},
		{"one_hour_old", func(s *ob.Status) { s.LastSuccess.ObservedAtMS = now.Add(-time.Hour).UnixMilli() }},
		{"future", func(s *ob.Status) { s.LastSuccess.ObservedAtMS = now.Add(time.Minute + time.Millisecond).UnixMilli() }},
		{"no_balances", func(s *ob.Status) { s.LastSuccess.Balances = nil }},
		{"duplicate_currency", func(s *ob.Status) { s.LastSuccess.Balances = append(s.LastSuccess.Balances, s.LastSuccess.Balances[0]) }},
		{"bad_currency", func(s *ob.Status) { s.LastSuccess.Balances[0].Currency = "usd" }},
		{"old_binding", func(s *ob.Status) { s.Targets[0].CredentialVersion = 2 }},
		{"null_binding", func(s *ob.Status) { s.LastSuccess.Binding = json.RawMessage("null") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := seed()
			tc.mutate(&s)
			if low, high := monetary(s, now); low || high {
				t.Fatalf("unknown became a monetary signal: %v/%v", low, high)
			}
		})
	}
	t.Run("no_cross_currency_sum", func(t *testing.T) {
		s := seed()
		s.LastSuccess.Balances = append(s.LastSuccess.Balances, balance.CurrencyBalance{Currency: "CNY", Total: "0.1"})
		if low, high := monetary(s, now); !low || high {
			t.Fatal("two native thresholds were summed")
		}
		s.LastSuccess.Balances[1].Total = "0.11"
		if low, high := monetary(s, now); low || !high {
			t.Fatal("a genuinely high route was lost")
		}
	})
}
