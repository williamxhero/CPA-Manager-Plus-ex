package balance_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
)

type integrationCredentialResolver struct {
	value string
	err   error
	calls int
}

func (r *integrationCredentialResolver) ResolveCredential(context.Context, string) (string, error) {
	r.calls++
	return r.value, r.err
}

// The merged work remains unwired to HTTP. These offline checks preserve its
// declared public failure contract without contacting any billing service.
func TestBalanceIntegrationCapabilityAndFailureContract(t *testing.T) {
	q := balance.New()
	caps, err := q.ListCapabilities(context.Background())
	if err != nil || len(caps) != 1 || caps[0].Adapter != "deepinfra" || !caps[0].Supported {
		t.Fatalf("capabilities: %+v %v", caps, err)
	}
	for _, tc := range []struct {
		name, adapter, value, url, code, status, capability string
		err                                                 error
		calls                                               int
	}{
		{name: "unknown adapter", adapter: "unknown", code: "ADAPTER_NOT_FOUND", status: "unsupported", capability: "unsupported", calls: 0},
		{name: "resolver failure", adapter: "deepinfra", err: errors.New("private resolver diagnostic"), code: "CREDENTIAL_RESOLVE_FAILED", status: "auth_error", capability: "supported", calls: 1},
		{name: "empty credential", adapter: " DeepInfra ", value: " ", code: "CREDENTIAL_MISSING", status: "needs_configuration", capability: "supported", calls: 1},
		{name: "private endpoint rejected offline", adapter: "deepinfra", value: "fixture-only", url: "https://127.0.0.1/billing", code: "INVALID_ENDPOINT", status: "network_error", capability: "supported", calls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resolver := &integrationCredentialResolver{value: tc.value, err: tc.err}
			got, err := q.Query(context.Background(), balance.QueryTarget{Adapter: tc.adapter, EndpointURL: tc.url}, resolver)
			if err != nil || got.ErrorCode != tc.code || got.QueryStatus != tc.status || got.CapabilityStatus != tc.capability || resolver.calls != tc.calls || got.QueryID == "" || got.ObservedAtMS <= 0 {
				t.Fatalf("got %+v err=%v resolver calls=%d", got, err, resolver.calls)
			}
			if got.AmountDecimal != "" || strings.Contains(got.ErrorMessage, "private resolver diagnostic") {
				t.Fatalf("fabricated amount or leaked error: %+v", got)
			}
		})
	}
}

func TestBalanceIntegrationModelNormalization(t *testing.T) {
	for _, tc := range []struct{ input, want string }{{"credential", "credential"}, {" AUTH ", "credential"}, {"key", "credential"}, {"provider", "provider"}, {" PROV ", "provider"}, {"unknown", ""}} {
		if got := model.NormalizeBalanceResourceType(tc.input); got != tc.want {
			t.Fatalf("resource %q=%q want %q", tc.input, got, tc.want)
		}
	}
	for _, tc := range []struct{ input, want string }{{"wallet", "account_wallet"}, {"key-limit", "key_remaining"}, {"credits", "credit_balance"}} {
		if got := model.NormalizeBalanceSemantic(tc.input); got != tc.want {
			t.Fatalf("semantic %q=%q want %q", tc.input, got, tc.want)
		}
	}
}
