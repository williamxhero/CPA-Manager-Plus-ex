package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	fr "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fullrefresh"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	pp "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/providerpolicy"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

// D1 exercises the actual public scheduler, not the monetary evaluator or a
// mocked observer. Three real low-balance jobs create three unaccepted Pending
// decisions. Without a reserved cycle budget, three slow receipt/operation
// exchanges consume the entire 15-second scheduling cycle before inventory.
//
// Fixture prerequisite: moneyPolicyCore(t, 1, 1, 1) must issue three providers,
// one member each, using the existing A key and the -B/-C synthetic keys. The
// provider order is deliberately not trusted; actual Core key-match selects
// each query credential below. A missing fixture fails, never seeds fake rows.
func TestProviderPolicySchedulerSlowPendingStillAdmitsPeriodicRound(t *testing.T) {
	core := moneyPolicyCore(t, 1, 1, 1)
	destination, err := url.Parse(core)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(destination)
	var slow atomic.Bool
	slowEntered := make(chan (<-chan struct{}), 16)
	release := make(chan struct{})
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const operations = "/v8/management/inference-gates/operations"
		operation := r.Method == http.MethodPost && r.URL.Path == operations
		receipt := r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, operations+"/")
		if operation || slow.Load() && receipt {
			// Consume the inbound body so client cancellation is observable by
			// the bridge. Never forward a gate write or fabricate a Core ACK.
			if _, err := io.Copy(io.Discard, r.Body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if slow.Load() {
				finished := make(chan struct{})
				defer close(finished)
				select {
				case slowEntered <- finished:
				case <-r.Context().Done():
					return
				}
				select {
				case <-r.Context().Done():
					return
				case <-release: // Failure cleanup only, never admission ordering.
				}
			}
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		// Catalog, bindings, eligibility, key-match and current ledger reads
		// are ordinary authenticated requests to the pinned actual Core.
		proxy.ServeHTTP(w, r)
	}))
	defer bridge.Close()

	cfg := testutil.NewConfig(t)
	protector, err := security.NewProtector([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(cfg.DBPath, protector)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	testutil.EnsureAdminCredential(t, db)
	if err := db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: bridge.URL, ManagementKey: "MONEY-CORE-MANAGEMENT"}}); err != nil {
		t.Fatal(err)
	}
	manager := New(cfg, db, collector.NewManager(cfg, db))
	app := manager.AppContext()
	// Reuse #76's existing scheduling-only clock seam and the production
	// fullrefresh service. Only the monetary subscriber is under test; this
	// is not coverage of #77's separate credential-policy runtime lifecycle.
	clock := &refreshClock{now: time.Now(), ticks: make(chan time.Time)}
	coordinator := fr.New(db, app.ResourceCapabilityService, app.OfficialBalanceService, app.CredentialQuotaService, fr.WithBudget(app.ManagementQueryBudget), fr.WithClock(clock))
	app.ProviderPolicyService.BindCoordinator(coordinator)
	app.FullRefreshService = coordinator
	defer func() {
		close(release)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := coordinator.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	}()
	api := func(method, path string, query, out any, want int) {
		t.Helper()
		body := ""
		if query != nil {
			raw, err := json.Marshal(query)
			if err != nil {
				t.Fatal(err)
			}
			body = string(raw)
		}
		response := testutil.Request(t, manager.Handler(), method, path, body, testutil.AdminKey)
		testutil.RequireStatus(t, response, want)
		testutil.DecodeJSON(t, response, out)
	}

	inv := app.ResourceCapabilityService.Inventory(context.Background(), true)
	var policies []pp.Status
	if inv.Snapshot != nil && inv.Identity.State == "available" {
		for _, row := range inv.Snapshot.Resources {
			if row.Kind == "provider" {
				var state pp.Status
				api("GET", "/usage-service/provider-policy?providerId="+row.ResourceID, nil, &state, 200)
				if len(state.Targets) != 2 {
					t.Fatal("scheduler fixture requires one actual member per provider")
				}
				policies = append(policies, state)
			}
		}
	}
	if len(policies) != 3 {
		t.Fatalf("three actual issued providers required; fixture supplied %d", len(policies))
	}
	secrets := []string{"MONEY-SYNTHETIC-INFERENCE", "MONEY-SYNTHETIC-INFERENCE-B", "MONEY-SYNTHETIC-INFERENCE-C"}
	queries := make([]ob.Query, 0, 3)
	for _, state := range policies {
		query := ob.Update{Config: ob.Config{Binding: ob.Binding{Scope: state.Scope, Targets: state.Targets}, Adapter: "deepseek", Endpoint: "https://api.deepseek.com/user/balance", PrivateTargets: []string{"127.0.0.1"}}}
		var configured ob.Status
		matched := false
		for _, secret := range secrets {
			query.Secret = secret
			api("PUT", balancePath, query, &configured, 200)
			query.Version = configured.Version
			api("GET", "/usage-service/provider-policy?providerId="+state.ProviderID, nil, &state, 200)
			if !state.Locked {
				matched = true
				break
			}
		}
		if !matched {
			t.Fatal("actual Core did not match any documented fixture key")
		}
		enabled, generation := true, state.Generation
		api("PUT", "/usage-service/provider-policy", pp.Update{Binding: state.Binding, Enabled: &enabled, ExpectedGeneration: &generation}, &state, 200)
		queries = append(queries, ob.Query{Binding: configured.Binding, Version: configured.Version})
	}

	upstream := make(chan struct{}, 8)
	network := policyMoneyNetwork(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		knownKey := false
		for _, secret := range secrets {
			knownKey = knownKey || r.Header.Get("Authorization") == "Bearer "+secret
		}
		if r.Method != "GET" || r.URL.Path != "/user/balance" || !knownKey {
			t.Error("invalid controlled official-host request")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if slow.Load() {
			upstream <- struct{}{}
		}
		fmt.Fprint(w, `{"is_available":true,"balance_infos":[{"currency":"USD","total_balance":"0.05","granted_balance":"0","topped_up_balance":"0.05"}]}`)
	}))
	app.OfficialBalanceService.Querier = balance.NewWithNetwork(network)
	completed := make(chan fr.CompletedObservation, 8)
	coordinator.Subscribe(func(_ context.Context, event fr.CompletedObservation) { completed <- event })
	initialJobs := map[string]bool{}
	for i, query := range queries {
		var job ob.Job
		api("POST", balancePath, query, &job, 202)
		initialJobs[job.ID] = true
		select {
		case event := <-completed:
			if event.AttemptID != job.ID || event.Official == nil || event.Official.LastSuccess == nil || event.Official.LastSuccess.QueryID != job.ID || event.Official.LastSuccess.QueryStatus != "success" {
				t.Fatal("real low-balance acquisition did not complete successfully")
			}
		case <-time.After(10 * time.Second):
			t.Fatal("actual source completion did not finish")
		}
		var state pp.Status
		api("GET", "/usage-service/provider-policy?providerId="+policies[i].ProviderID, nil, &state, 200)
		if !state.Enabled || state.State != "pending" || state.OwnPaused || state.Applied {
			t.Fatalf("unaccepted actual decision was not honestly Pending: state=%s enabled=%t paused=%t applied=%t", state.State, state.Enabled, state.OwnPaused, state.Applied)
		}
	}

	// Recheck the entire public baseline after all three completions; none of
	// the earlier Pending decisions may have disappeared during later work.
	for _, policy := range policies {
		var state pp.Status
		api("GET", "/usage-service/provider-policy?providerId="+policy.ProviderID, nil, &state, 200)
		if !state.Enabled || state.State != "pending" || state.OwnPaused || state.Applied {
			t.Fatal("three real unaccepted Pending decisions must coexist before Run")
		}
	}

	// Only scheduler time advances: prior manual reads are due again, while
	// money freshness, real observations, jobs and Core state remain untouched.
	clock.mu.Lock()
	clock.now = clock.now.Add(2 * time.Hour)
	clock.mu.Unlock()
	admitted := make(chan fr.StartedObservation, 8)
	coordinator.SubscribeStarted(func(_ context.Context, event fr.StartedObservation) error {
		if event.Purpose == fr.Periodic {
			admitted <- event
		}
		return nil
	})
	slow.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.FullRefreshService.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(10 * time.Second):
			t.Error("public scheduler did not cancel and join")
		}
	}()
	// The old observer consumes all 15s across three pending sources. The
	// repaired observer reserves at least 10s for inventory/admission. This
	// generous 12s bound observes a real Started event AND actual HTTPS I/O.
	deadline := time.NewTimer(12 * time.Second)
	defer deadline.Stop()
	var event fr.StartedObservation
	select {
	case event = <-admitted:
	case <-deadline.C:
		t.Fatal("slow Pending work exhausted the cycle before periodic source admission")
	}
	matched := false
	for _, query := range queries {
		matched = matched || event.Source.Official != nil && reflect.DeepEqual(*event.Source.Official, query)
	}
	if event.Source.Kind != fr.Official || !matched || event.AttemptID == "" || initialJobs[event.AttemptID] {
		t.Fatal("periodic admission was not a new job for an actual configured source")
	}
	select {
	case <-upstream:
	case <-deadline.C:
		t.Fatal("periodic source admission never reached actual controlled HTTPS")
	}
	select {
	case finished := <-slowEntered:
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Fatal("bounded scheduler attempt did not cancel the blocked bridge exchange")
		}
	default:
		t.Fatal("scheduler bypassed the required slow Pending exchange")
	}
	var refreshed fr.Inventory
	api("GET", "/usage-service/full-refresh", nil, &refreshed, 200)
	if refreshed.LatestRound == nil || refreshed.LatestRound.Purpose != fr.Periodic {
		t.Fatal("public refresh status did not expose the admitted periodic round")
	}
	found := false
	for _, item := range refreshed.LatestRound.Items {
		found = found || item.JobID == event.AttemptID && item.Queried && reflect.DeepEqual(item.Request, event.Source)
	}
	if !found {
		t.Fatal("public periodic progress did not contain the real admitted source job")
	}
}
