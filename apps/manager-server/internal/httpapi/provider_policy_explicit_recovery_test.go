package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

func TestProviderPolicyExplicitOFFRecoveryClearsOnlyMonetaryPause(t *testing.T) {
	base := moneyPolicyCore(t)
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
		t.Fatal("issued inventory unavailable")
	}
	id := ""
	for _, row := range inv.Snapshot.Resources {
		if row.Kind == "provider" {
			id = row.ResourceID
		}
	}
	if id == "" {
		t.Fatal("provider missing")
	}
	path := "/usage-service/provider-policy?providerId=" + id
	state := api("GET", path, nil, 200)
	query := map[string]any{"scope": state["scope"], "targets": state["targets"], "version": 0, "adapter": "deepseek", "endpoint": "https://api.deepseek.com/user/balance", "privateTargets": []string{"127.0.0.1"}, "secret": "MONEY-SYNTHETIC-INFERENCE"}
	api("PUT", balancePath, query, 200)
	var money atomic.Value
	money.Store("0.05")
	network := policyMoneyNetwork(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":%q,"granted_balance":"0","topped_up_balance":%q}]}`, money.Load().(string), money.Load().(string))
	}))
	manager.AppContext().OfficialBalanceService.Querier = balance.NewWithNetwork(network)
	stopPolicy := startProviderPolicyRuntime(t, manager)
	defer shutdownProviderPolicyManager(t, manager, stopPolicy)
	enable := map[string]any{"scope": state["scope"], "targets": state["targets"], "providerId": id, "expectedGeneration": 0, "enabled": true}
	state = api("PUT", "/usage-service/provider-policy", enable, 200)
	refresh := func(wantPaused bool) map[string]any {
		t.Helper()
		job := api("POST", balancePath, map[string]any{"scope": state["scope"], "targets": state["targets"], "version": 1}, 202)
		for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
			j := api("GET", balancePath+"?job="+job["id"].(string), nil, 200)
			if j["state"] == "complete" {
				awaitProviderPolicyEvent(t, db, job["id"].(string))
				for until := time.Now().Add(8 * time.Second); time.Now().Before(until); {
					s := api("GET", path, nil, 200)
					if s["ownPaused"] == wantPaused && s["applied"] == true {
						return s
					}
					time.Sleep(20 * time.Millisecond)
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("balance/policy completion did not settle")
		return nil
	}
	state = refresh(true)
	if state["ownPaused"] != true || state["applied"] != true {
		t.Fatalf("low balance did not pause: %+v", state)
	}
	off := map[string]any{"scope": state["scope"], "targets": state["targets"], "providerId": id, "expectedGeneration": state["generation"], "enabled": false}
	state = api("PUT", "/usage-service/provider-policy", off, 200)
	if state["enabled"] != false || state["ownPaused"] != true {
		t.Fatalf("OFF did not retain owned pause: %+v", state)
	}
	for deadline := time.Now().Add(20 * time.Second); ; {
		state = api("GET", path, nil, 200)
		if state["recoveryAvailable"] == true && state["gateGeneration"] != nil && state["sourceVersion"] != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("recovery remained unavailable: %+v", state)
		}
		time.Sleep(50 * time.Millisecond)
	}
	recoverBody := map[string]any{
		"scope":                    state["scope"],
		"targets":                  state["targets"],
		"providerId":               id,
		"expectedGeneration":       state["generation"],
		"expectedGateGeneration":   state["gateGeneration"],
		"sourceVersion":            state["sourceVersion"],
	}
	state = api("POST", "/usage-service/provider-policy/recover", recoverBody, 200)
	if state["enabled"] != false || state["ownPaused"] != false || state["applied"] != true || state["manualDisabled"] != false {
		t.Fatalf("explicit recovery changed intent/manual or lacked ACK: %+v", state)
	}
	api("POST", "/usage-service/provider-policy/recover", recoverBody, 409)
	state = api("GET", path, nil, 200)
	if state["enabled"] != false || state["ownPaused"] != false || state["recoveryAvailable"] == true {
		t.Fatalf("recovery was not durable/readback-closed: %+v", state)
	}
}
