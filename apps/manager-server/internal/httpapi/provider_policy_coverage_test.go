package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
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

func TestProviderPolicyCompleteMembersAndSeparateCurrencies(t *testing.T) {
	base := moneyPolicyCore(t, 2)
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
	if err := db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: base, ManagementKey: "MONEY-CORE-MANAGEMENT"}}); err != nil {
		t.Fatal(err)
	}
	manager := New(cfg, db, collector.NewManager(cfg, db))
	h := manager.Handler()
	api := func(method, path string, q any, want int) map[string]any {
		t.Helper()
		body := ""
		if q != nil {
			raw, _ := json.Marshal(q)
			body = string(raw)
		}
		r := testutil.Request(t, h, method, path, body, testutil.AdminKey)
		testutil.RequireStatus(t, r, want)
		var out map[string]any
		testutil.DecodeJSON(t, r, &out)
		return out
	}
	inv := manager.AppContext().ResourceCapabilityService.Inventory(context.Background(), true)
	if inv.Snapshot == nil {
		t.Fatal("actual inventory unavailable")
	}
	id := ""
	for _, row := range inv.Snapshot.Resources {
		if row.Kind == "provider" {
			id = row.ResourceID
		}
	}
	path := "/usage-service/provider-policy?providerId=" + id
	state := api("GET", path, nil, 200)
	group := map[string]any{"scope": state["scope"], "targets": state["targets"], "version": 0, "adapter": "deepseek", "endpoint": "https://api.deepseek.com/user/balance", "privateTargets": []string{"127.0.0.1"}, "secret": "MONEY-SYNTHETIC-INFERENCE"}
	api("PUT", balancePath, group, 200)
	update := func(enabled bool, generation int, want int) map[string]any {
		t.Helper()
		return api("PUT", "/usage-service/provider-policy", map[string]any{"scope": state["scope"], "targets": state["targets"], "providerId": id, "expectedGeneration": generation, "enabled": enabled}, want)
	}
	update(true, 0, 503) // One verified member is not an all-provider proof.
	// Discover the second key's issued tuple by testing the public source proof,
	// never by relying on list index or naming order.
	targets := state["targets"].([]any)
	var member map[string]any
	for _, candidate := range targets[1:] {
		q := map[string]any{"scope": state["scope"], "targets": []any{candidate}, "version": 0, "adapter": "deepseek", "endpoint": "https://api.deepseek.com/user/balance", "privateTargets": []string{"127.0.0.1"}, "secret": "MONEY-SYNTHETIC-INFERENCE-2"}
		api("PUT", balancePath, q, 200)
		read := api("GET", path, nil, 200)
		if read["locked"] == false {
			member = q
			break
		}
		// This deliberately nonmatching configuration remains visible but cannot
		// earn coverage. Its failed matching must not poison the actual member.
	}
	if member == nil {
		t.Fatal("second real member was not proven")
	}
	state = update(true, 0, 200)
	type sourceBarrier struct{ arrived, release chan struct{} }
	var barrier atomic.Pointer[sourceBarrier]
	var groupHigh atomic.Bool
	var secondHigh atomic.Bool
	secondHigh.Store(true)
	network := policyMoneyNetwork(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/user/balance" {
			t.Error("non-read balance request")
			w.WriteHeader(400)
			return
		}
		amount := "0.1"
		if r.Header.Get("Authorization") == "Bearer MONEY-SYNTHETIC-INFERENCE" {
			if groupHigh.Load() {
				amount = "0.100000000000000000000000000001"
			}
			if b := barrier.Load(); b != nil {
				close(b.arrived)
				select {
				case <-b.release:
				case <-r.Context().Done():
					return
				}
			}
		}
		if r.Header.Get("Authorization") == "Bearer MONEY-SYNTHETIC-INFERENCE-2" && secondHigh.Load() {
			amount = "0.100000000000000000000000000001"
		}
		fmt.Fprintf(w, `{"is_available":true,"balance_infos":[{"currency":"USD","total_balance":"0.1","granted_balance":"0","topped_up_balance":"0.1"},{"currency":"CNY","total_balance":%q,"granted_balance":"0","topped_up_balance":%q}]}`, amount, amount)
	}))
	manager.AppContext().OfficialBalanceService.Querier = balance.NewWithNetwork(network)
	query := func(binding map[string]any) string {
		t.Helper()
		job := api("POST", balancePath, map[string]any{"scope": binding["scope"], "targets": binding["targets"], "version": 1}, 202)
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
			got := api("GET", balancePath+"?job="+job["id"].(string), nil, 200)
			if got["state"] == "complete" {
				return job["id"].(string)
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("actual source query did not complete")
		return ""
	}
	query(group)
	if read := api("GET", path, nil, 200); read["ownPaused"] == true {
		t.Fatal("unqueried member was treated as zero")
	}
	query(member)
	if read := api("GET", path, nil, 200); read["ownPaused"] == true {
		t.Fatal("cross-currency high source was ignored")
	}
	secondHigh.Store(false)
	query(member)
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		state = api("GET", path, nil, 200)
		if state["ownPaused"] == true {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if state["ownPaused"] != true || state["applied"] != true {
		t.Fatalf("two members and currencies each0.1 were summed or not covered: %+v", state)
	}
	// Verify the monetary owner causes actual native admission rejection.
	req, _ := http.NewRequest("POST", base+"/v1/chat/completions", strings.NewReader(`{"model":"controlled-model","messages":[{"role":"user","content":"no upstream send"}]}`))
	req.Header.Set("Authorization", "Bearer MONEY-CLIENT")
	req.Header.Set("Content-Type", "application/json")
	res, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 503 {
		t.Fatalf("money pause did not reject real admission: %d", res.StatusCode)
	}
	completions := make(chan string, 32)
	manager.AppContext().FullRefreshService.Subscribe(func(_ context.Context, e fr.CompletedObservation) { completions <- e.AttemptID })
	awaitCompletion := func(id string) {
		t.Helper()
		timer := time.NewTimer(15 * time.Second)
		defer timer.Stop()
		for {
			select {
			case actual := <-completions:
				if actual == id {
					return
				}
			case <-timer.C:
				t.Fatal("reordered source completion did not settle")
				return
			}
		}
	}
	expectPause := func(want bool) {
		t.Helper()
		for until := time.Now().Add(10 * time.Second); time.Now().Before(until); {
			state = api("GET", path, nil, 200)
			if state["ownPaused"] == want {
				return
			}
		}
		t.Fatalf("cross-source start-order suppressed valid result, wantPaused=%v got=%+v", want, state)
	}
	reordered := func(high bool, wantPaused bool) {
		t.Helper()
		groupHigh.Store(high)
		b := &sourceBarrier{make(chan struct{}), make(chan struct{})}
		barrier.Store(b)
		t.Cleanup(func() {
			select {
			case <-b.release:
			default:
				close(b.release)
			}
		})
		job := api("POST", balancePath, map[string]any{"scope": group["scope"], "targets": group["targets"], "version": 1}, 202)
		select {
		case <-b.arrived:
		case <-time.After(5 * time.Second):
			t.Fatal("group source never reached barrier")
		}
		later := query(member)
		awaitCompletion(later)
		close(b.release)
		barrier.Store(nil)
		awaitCompletion(job["id"].(string))
		expectPause(wantPaused)
	}
	// Independent source A starts first but completes after B. Global start
	// order must not discard A's current generation high or all-low reading.
	reordered(true, false)
	reordered(false, true)
	state = update(false, 1, 200)
	if state["enabled"] != false || state["ownPaused"] != true {
		t.Fatal("OFF implicitly resumed automatic pause")
	}
	secondHigh.Store(true)
	query(member)
	// Re-enable does not consume an OFF-generation high result as fresh recovery.
	state = update(true, 2, 200)
	if state["ownPaused"] != true {
		t.Fatal("saved ON consumed cached OFF observation")
	}
	query(member)
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		state = api("GET", path, nil, 200)
		if state["ownPaused"] == false {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if state["ownPaused"] != false {
		t.Fatal("one newly successful route did not clear this owner")
	}
}
