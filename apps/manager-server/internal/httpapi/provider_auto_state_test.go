package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

const providerAutoStateKey = "providerautostate-contract-key"

// The projection Core already publishes, including the nested reasons and
// members the panel must not read through a second, derived shape.
const providerAutoStateCoreBody = `{"version":1,"ready":true,"revision":12,"providers":[` +
	`{"resourceId":"11111111-1111-4111-8111-111111111111","state":"partially_auto_paused","memberCount":2,"pausedCount":1,` +
	`"reasons":[{"owner":"core.upstream-balance.v1","reason":"upstream_balance_exhausted","observedAtMs":1700000000000,"resourceId":"11111111-1111-4111-8111-111111111111"}],` +
	`"members":[` +
	`{"resourceId":"11111111-1111-4111-8111-111111111111","state":"paused","reasons":[{"owner":"core.upstream-balance.v1","reason":"upstream_balance_exhausted","observedAtMs":1700000000000,"resourceId":"11111111-1111-4111-8111-111111111111"}]},` +
	`{"resourceId":"22222222-2222-4222-8222-222222222222","state":"available","reasons":[]}]}]}`

func providerAutoStateServer(t *testing.T, coreBase string) *Server {
	t.Helper()
	cfg := testutil.NewConfig(t)
	db := testutil.NewStore(t, cfg)
	if err := db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{
		CPABaseURL:    coreBase,
		ManagementKey: providerAutoStateKey,
	}}); err != nil {
		t.Fatal(err)
	}
	return New(cfg, db, collector.NewManager(cfg, db))
}

// recordedProviderAutoStateServer records every management request Manager sends,
// so a test can prove the panel path performed exactly one read and no write.
func recordedProviderAutoStateServer(t *testing.T, handler http.HandlerFunc) (*Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var observed []string
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		observed = append(observed, r.Method+" "+r.URL.Path)
		mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(core.Close)
	return providerAutoStateServer(t, core.URL), func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), observed...)
	}
}

func TestProviderAutoStateForwardsCoreProjectionUnchanged(t *testing.T) {
	server, observed := recordedProviderAutoStateServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v8/management/inference-gates/auto-stop" {
			t.Errorf("core path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+providerAutoStateKey {
			t.Errorf("core authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, providerAutoStateCoreBody)
	})
	rr := testutil.Request(t, server.Handler(), "GET", "/usage-service/provider-auto-state", "", testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("cache control = %q", got)
	}
	var want, got any
	if err := json.Unmarshal([]byte(providerAutoStateCoreBody), &want); err != nil {
		t.Fatal(err)
	}
	testutil.DecodeJSON(t, rr, &got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("panel projection = %s, want the unmodified core projection %s", rr.Body.String(), providerAutoStateCoreBody)
	}
	if seen := observed(); !reflect.DeepEqual(seen, []string{"GET /v8/management/inference-gates/auto-stop"}) {
		t.Fatalf("management requests = %v", seen)
	}
}

func TestProviderAutoStateMapsCoreFailuresToExplicitCodes(t *testing.T) {
	for _, test := range []struct {
		name       string
		coreStatus int
		coreBody   string
		wantStatus int
		wantError  string
		wantReason string
	}{
		{name: "gate_unavailable", coreStatus: 503, coreBody: `{"error":"gate_unavailable"}`, wantStatus: 503, wantError: "provider_auto_state_unavailable", wantReason: "gate_unavailable"},
		{name: "invalid_json", coreStatus: 200, coreBody: `{"version":1,`, wantStatus: 502, wantError: "core_response_invalid", wantReason: "invalid_json"},
		{name: "unsupported_version", coreStatus: 200, coreBody: `{"version":2,"ready":true,"providers":[]}`, wantStatus: 502, wantError: "core_response_invalid", wantReason: "unsupported_contract_version"},
		{name: "providers_missing", coreStatus: 200, coreBody: `{"version":1,"ready":true,"revision":3}`, wantStatus: 502, wantError: "core_response_invalid", wantReason: "providers_missing"},
		{name: "ready_missing", coreStatus: 200, coreBody: `{"version":1,"revision":3,"providers":[]}`, wantStatus: 502, wantError: "core_response_invalid", wantReason: "ready_missing"},
		{name: "core_auth_error", coreStatus: 401, coreBody: `{"error":"unauthorized"}`, wantStatus: 502, wantError: "core_auth_error", wantReason: "core_auth_error"},
		{name: "core_internal_error", coreStatus: 500, coreBody: `{"error":"boom"}`, wantStatus: 503, wantError: "core_unavailable", wantReason: "core_status_500"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, observed := recordedProviderAutoStateServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.coreStatus)
				io.WriteString(w, test.coreBody)
			})
			rr := testutil.Request(t, server.Handler(), "GET", "/usage-service/provider-auto-state", "", testutil.AdminKey)
			testutil.RequireStatus(t, rr, test.wantStatus)
			var body map[string]any
			testutil.DecodeJSON(t, rr, &body)
			if body["error"] != test.wantError || body["reason"] != test.wantReason {
				t.Fatalf("failure body = %s", rr.Body.String())
			}
			if _, fabricated := body["providers"]; fabricated {
				t.Fatalf("failure body fabricated a projection: %s", rr.Body.String())
			}
			if seen := observed(); !reflect.DeepEqual(seen, []string{"GET /v8/management/inference-gates/auto-stop"}) {
				t.Fatalf("management requests = %v", seen)
			}
		})
	}
}

func TestProviderAutoStateReportsUnreachableCoreWithoutFakeState(t *testing.T) {
	closed := httptest.NewServer(http.NotFoundHandler())
	unreachable := closed.URL
	closed.Close()
	server := providerAutoStateServer(t, unreachable)
	rr := testutil.Request(t, server.Handler(), "GET", "/usage-service/provider-auto-state", "", testutil.AdminKey)
	testutil.RequireStatus(t, rr, 503)
	var body map[string]any
	testutil.DecodeJSON(t, rr, &body)
	if body["error"] != "core_unavailable" || body["reason"] != "core_unreachable" {
		t.Fatalf("failure body = %s", rr.Body.String())
	}
}

func TestProviderAutoStateRejectsUnauthorizedAndNonReadRequests(t *testing.T) {
	server, observed := recordedProviderAutoStateServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("management request was issued for a rejected panel call: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(500)
	})
	testutil.RequireStatus(t, testutil.Request(t, server.Handler(), "GET", "/usage-service/provider-auto-state", "", ""), 401)
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		rr := testutil.Request(t, server.Handler(), method, "/usage-service/provider-auto-state", `{}`, testutil.AdminKey)
		testutil.RequireStatus(t, rr, 405)
	}
	if seen := observed(); len(seen) != 0 {
		t.Fatalf("rejected panel calls reached core: %v", seen)
	}
}
