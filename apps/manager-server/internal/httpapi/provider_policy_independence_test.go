package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	fr "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fullrefresh"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

func TestProviderPolicyPendingADoesNotStarveProviderB(t *testing.T) {
	core := moneyPolicyCore(t, 1, 2)
	destination, _ := url.Parse(core)
	proxy := httputil.NewSingleHostReverseProxy(destination)
	var denied atomic.Value
	denied.Store("")
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/v8/management/inference-gates/operations" {
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				w.WriteHeader(400)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(raw))
			var command struct {
				Targets []struct {
					ResourceID string `json:"resource_id"`
				} `json:"targets"`
			}
			if json.Unmarshal(raw, &command) != nil || len(command.Targets) == 0 {
				w.WriteHeader(400)
				return
			}
			if command.Targets[0].ResourceID == denied.Load().(string) {
				w.WriteHeader(503)
				fmt.Fprint(w, `{"error":"controlled_provider_A_outage"}`)
				return
			}
		}
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
	defer shutdownProviderPolicyManager(t, manager, nil)
	api := func(method, path string, query any, want int) map[string]any {
		t.Helper()
		body := ""
		if query != nil {
			raw, _ := json.Marshal(query)
			body = string(raw)
		}
		r := testutil.Request(t, manager.Handler(), method, path, body, testutil.AdminKey)
		testutil.RequireStatus(t, r, want)
		var out map[string]any
		testutil.DecodeJSON(t, r, &out)
		return out
	}
	inv := manager.AppContext().ResourceCapabilityService.Inventory(context.Background(), true)
	ids := []string{}
	if inv.Snapshot != nil {
		for _, row := range inv.Snapshot.Resources {
			if row.Kind == "provider" {
				ids = append(ids, row.ResourceID)
			}
		}
	}
	if len(ids) != 2 {
		t.Fatal("two real providers required")
	}
	denied.Store(ids[0])
	states := []map[string]any{}
	for _, id := range ids {
		state := api("GET", "/usage-service/provider-policy?providerId="+id, nil, 200)
		query := map[string]any{"scope": state["scope"], "targets": state["targets"], "version": 0, "adapter": "deepseek", "endpoint": "https://api.deepseek.com/user/balance", "privateTargets": []string{"127.0.0.1"}, "secret": "MONEY-SYNTHETIC-INFERENCE"}
		configured := api("PUT", balancePath, query, 200)
		read := api("GET", "/usage-service/provider-policy?providerId="+id, nil, 200)
		if read["locked"] == true {
			query["version"] = configured["version"]
			query["secret"] = "MONEY-SYNTHETIC-INFERENCE-B"
			configured = api("PUT", balancePath, query, 200)
		}
		state = api("PUT", "/usage-service/provider-policy", map[string]any{"scope": state["scope"], "targets": state["targets"], "providerId": id, "expectedGeneration": 0, "enabled": true}, 200)
		state["queryVersion"] = configured["version"]
		states = append(states, state)
	}
	network := policyMoneyNetwork(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/user/balance" {
			t.Error("invalid official read")
			w.WriteHeader(400)
			return
		}
		fmt.Fprint(w, `{"is_available":true,"balance_infos":[{"currency":"USD","total_balance":"0.05","granted_balance":"0","topped_up_balance":"0.05"}]}`)
	}))
	manager.AppContext().OfficialBalanceService.Querier = balance.NewWithNetwork(network)
	stopPolicy := startProviderPolicyRuntime(t, manager)
	defer stopPolicy() // Registered after DB/coordinator defers: worker joins first.
	completed := make(chan string, 4)
	manager.AppContext().FullRefreshService.Subscribe(func(_ context.Context, e fr.CompletedObservation) { completed <- e.AttemptID })
	for _, state := range states {
		job := api("POST", balancePath, map[string]any{"scope": state["scope"], "targets": state["targets"], "version": state["queryVersion"]}, 202)
		select {
		case id := <-completed:
			if id != job["id"] {
				t.Fatal("completion crossed providers")
			}
		case <-time.After(15 * time.Second):
			t.Fatal("completion did not return")
		}
	}
	// Only actual Run consumes both persisted completions; no rescue Reconcile
	// or extra acquisition may hide A starving B's independent durable work.
	awaitProviderPolicyCondition(t, "actual runtime left independent provider B starved", func() bool {
		b := api("GET", "/usage-service/provider-policy?providerId="+ids[1], nil, 200)
		return b["ownPaused"] == true && b["applied"] == true
	})
	a := api("GET", "/usage-service/provider-policy?providerId="+ids[0], nil, 200)
	b := api("GET", "/usage-service/provider-policy?providerId="+ids[1], nil, 200)
	if a["ownPaused"] == true || a["applied"] == true {
		t.Fatal("unaccepted A operation was optimistic")
	}
	if b["ownPaused"] != true || b["applied"] != true {
		t.Fatalf("pending provider A starved independent B: %+v", b)
	}
}
