package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	fr "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fullrefresh"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
)

// Forward every byte to the isolated, provenance-checked real Core. This only
// models transport latency and records public management I/O; it invents no
// capabilities, identities, sources, bindings or upstream observations.
func (f *refreshFixture) probeManagement(delay time.Duration, before func(*http.Request, int)) func() map[string]int {
	f.t.Helper()
	cfg, _, err := f.db.LoadManagerConfig(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	target, err := url.Parse(cfg.CPAConnection.CPABaseURL)
	if err != nil {
		f.t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	var mu sync.Mutex
	counts := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		counts[r.Method+" "+r.URL.Path]++
		n := counts[r.Method+" "+r.URL.Path]
		mu.Unlock()
		if before != nil {
			before(r, n)
		}
		if delay > 0 {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-r.Context().Done():
				return
			case <-timer.C:
			}
		}
		proxy.ServeHTTP(w, r)
	}))
	f.t.Cleanup(server.Close)
	cfg.CPAConnection.CPABaseURL = server.URL
	if err = f.db.SaveManagerConfig(context.Background(), cfg); err != nil {
		f.t.Fatal(err)
	}
	return func() map[string]int {
		mu.Lock()
		defer mu.Unlock()
		out := map[string]int{}
		for k, v := range counts {
			out[k] = v
		}
		return out
	}
}

func TestFullRefreshActualCoreInventoryFitsHandlerBudgetAtDeploymentScale(t *testing.T) {
	// 20 compatibility providers + the real plugin provider; 25 keys + 4
	// credential files => 50 host-issued catalog resources, not a mock roster.
	f := newRefreshFixture(t, refreshFixtureScale{providers: 20, extraKeys: 4, extraOAuthFiles: 2})
	counts := f.probeManagement(200*time.Millisecond, nil)
	started := time.Now()
	code, data := f.api("GET", "/usage-service/full-refresh", nil)
	elapsed := time.Since(started)
	t.Logf("Manager GET /usage-service/full-refresh: HTTP%d elapsed=%s management=%v upstream_queries=%d", code, elapsed, counts(), f.calls.Load())
	if code != 200 {
		t.Fatalf("complete inventory must fit unchanged 15s handler budget: HTTP%d %s", code, data)
	}
	var inv fr.Inventory
	if err := json.Unmarshal(data, &inv); err != nil || inv.ResourceCount != 50 {
		t.Fatalf("expected complete real 50-resource catalog: %v %s", err, data)
	}
	providers, readyFiles := 0, 0
	covered := map[string]bool{}
	for _, source := range inv.Sources {
		if source.Request.Kind == fr.Official {
			providers++
		}
		if source.Request.Kind == fr.Credential && source.State == "ready" {
			// Plugin config adds one further ready credential source.
			readyFiles++
		}
		for _, target := range source.Targets {
			covered[target.ResourceID] = true
		}
	}
	t.Logf("complete resources=%d official_groups=%d ready_credential_sources=%d covered=%d", inv.ResourceCount, providers, readyFiles, len(covered))
	if providers != 21 || len(covered) != 50 || readyFiles < 4 || f.calls.Load() != 0 || elapsed >= 15*time.Second {
		t.Fatal("incomplete roster, unexpected query, or handler budget exhausted")
	}
}

func TestFullRefreshActualCoreInventoryEvidenceDoesNotAuthorizeLaterWrites(t *testing.T) {
	f := newRefreshFixture(t)
	inv := f.configuredSources()
	var official *ob.Query
	var native *fr.SourceRequest
	for _, source := range inv.Sources {
		if source.Request.Official != nil && source.Request.Official.Version > 0 {
			official = source.Request.Official
		}
		if source.Request.Credential != nil && source.State == "ready" {
			request := source.Request
			native = &request
		}
	}
	if official == nil || native == nil {
		t.Fatal("missing independently authorized official/selected native source")
	}
	counts := f.probeManagement(0, nil)
	cfg, _, err := f.db.LoadManagerConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cfg.CPAConnection.ManagementKey = "revoked-test-management-key"
	if err = f.db.SaveManagerConfig(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	update := ob.Update{Config: ob.Config{Binding: official.Binding, Version: official.Version, Adapter: "deepseek", Endpoint: f.upstream.URL + "/user/balance", PrivateTargets: []string{"127.0.0.1"}}, Secret: "explicit-billing-new"}
	for _, request := range []struct {
		method, path string
		body         any
		want         int
	}{
		{"GET", "/usage-service/full-refresh", nil, 503},
		{"PUT", balancePath, update, 409},
		{"POST", balancePath, official, 409},
		{"POST", "/usage-service/credential-quota", native.Credential, 409},
	} {
		code, data := f.api(request.method, request.path, request.body)
		if code != request.want {
			t.Fatalf("%s %s reused earlier read authority: HTTP%d %s", request.method, request.path, code, data)
		}
	}
	t.Logf("revoked connection: management=%v upstream_queries=%d", counts(), f.calls.Load())
	if f.calls.Load() != 0 {
		t.Fatal("read evidence authorized upstream work after connection revocation")
	}
}

func TestFullRefreshActualCoreInventoryRetainsClosingLiveConnectionFence(t *testing.T) {
	f := newRefreshFixture(t)
	var changed atomic.Bool
	counts := f.probeManagement(0, func(r *http.Request, n int) {
		// The opening inventory has closed (two caps reads). Change the saved
		// generation during the full-refresh closing read while forwarding the
		// original authenticated request to the unchanged real Core.
		if r.URL.Path != "/v8/management/resource-capabilities" || n != 3 {
			return
		}
		cfg, _, err := f.db.LoadManagerConfig(context.Background())
		if err == nil {
			cfg.CPAConnection.ManagementKey = "replaced-test-management-key"
			err = f.db.SaveManagerConfig(context.Background(), cfg)
		}
		if err != nil {
			t.Error(err)
			return
		}
		changed.Store(true)
	})
	code, data := f.api("GET", "/usage-service/full-refresh", nil)
	t.Logf("closing connection fence: HTTP%d management=%v upstream_queries=%d", code, counts(), f.calls.Load())
	var failure struct {
		Error string `json:"error"`
	}
	if !changed.Load() || code != 503 || json.Unmarshal(data, &failure) != nil || failure.Error != "inventory_unavailable" || f.calls.Load() != 0 {
		t.Fatalf("late inventory survived connection replacement: HTTP%d %s", code, data)
	}
}

func TestFullRefreshActualCoreInventoryDoesNotReuseEvidenceAcrossRequestsOrInstances(t *testing.T) {
	f, other := newRefreshFixture(t), newRefreshFixture(t)
	code, data := f.api("GET", "/usage-service/full-refresh", nil)
	var first fr.Inventory
	if code != 200 || json.Unmarshal(data, &first) != nil {
		t.Fatalf("first inventory: HTTP%d %s", code, data)
	}
	code, data = other.api("GET", "/usage-service/full-refresh", nil)
	var second fr.Inventory
	if code != 200 || json.Unmarshal(data, &second) != nil || second.Scope.InstanceID == first.Scope.InstanceID {
		t.Fatalf("independent Core inventory: HTTP%d %s", code, data)
	}
	otherCfg, _, err := other.db.LoadManagerConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := f.db.LoadManagerConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cfg.CPAConnection = otherCfg.CPAConnection
	if err = f.db.SaveManagerConfig(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	counts := f.probeManagement(0, nil)
	for i := 0; i < 2; i++ {
		code, data = f.api("GET", "/usage-service/full-refresh", nil)
		var current fr.Inventory
		if code != 200 || json.Unmarshal(data, &current) != nil || current.Scope != second.Scope {
			t.Fatalf("later request reused prior instance evidence: HTTP%d %s", code, data)
		}
	}
	t.Logf("two fresh reads after instance replacement: management=%v upstream_queries=%d", counts(), f.calls.Load()+other.calls.Load())
}
