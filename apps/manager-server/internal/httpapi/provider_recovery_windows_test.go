package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
)

const recoveryPath = "/usage-service/provider-policy/recover"

func historicalRecoveryFixture(t *testing.T) (*providerFundingFixture, string) {
	t.Helper()
	if os.Getenv("PROVIDER82_MANAGER_BINARY") == "" {
		t.Skip("explicit ordinary Manager artifact required")
	}
	f := newProviderFundingFixture(t)
	f.manager.AppContext().OfficialBalanceService.Querier = balance.NewWithNetwork(providerFundingNetwork(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"is_available":true,"balance_infos":[{"currency":"USD","total_balance":"0.0500","granted_balance":"0","topped_up_balance":"0.0500"}]}`)
	}))
	for _, key := range []string{"FUNDING-SYNTHETIC-A1", "FUNDING-SYNTHETIC-A2"} {
		f.configureAndRefresh(key, key, 0)
	}
	f.enableMoney(true)
	for _, key := range []string{"FUNDING-SYNTHETIC-A1", "FUNDING-SYNTHETIC-A2"} {
		job := f.refreshMoney(key, 1)
		// Do not flood authenticated Core with full display reads while the
		// separately bounded proof callback is still running. This changes only
		// external polling cadence, never the product's authorization budget.
		time.Sleep(8 * time.Second)
		f.awaitMoneyEvaluation(job)
	}
	f.awaitMoneyPaused(true)
	request := f.proofRequest()
	raw, _ := json.Marshal(request)
	other := f.api("GET", "/usage-service/provider-gate?binding="+url.QueryEscape(string(raw)), nil, 200)
	request["version"], request["sourceVersion"], request["expectedGeneration"], request["action"] = 1, other["sourceVersion"], other["generation"], "pause"
	f.api("PUT", "/usage-service/provider-gate", request, 200)
	f.enableMoney(false)
	f.shutdown()
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	return f, other["owner"].(string)
}

func recoveryRequest(state map[string]any) map[string]any {
	gate := state["coordination"].(map[string]any)
	return map[string]any{"scope": state["scope"], "targets": state["targets"], "providerId": state["providerId"], "expectedGeneration": state["generation"], "expectedGateGeneration": gate["generation"], "sourceVersion": gate["sourceVersion"]}
}

func TestProviderRecoveryMainClearsOnlyMonetaryOwner(t *testing.T) {
	f, otherOwner := historicalRecoveryFixture(t)
	stop := f.startRuntimeMain("recovery-first", "", "", "")
	defer func() { stop() }()
	state := f.api("GET", f.policyPath(), nil, 200)
	if state["state"] != "historical_pause" || len(f.ledger()["gates"].([]any)) != 2 {
		t.Fatal("durable monetary and independent owner pauses were not established")
	}
	request := recoveryRequest(state)
	state = f.api("POST", recoveryPath, request, 200)
	if state["enabled"] != false || state["ownPaused"] != false || state["applied"] != true || state["state"] != "recovered_off" || state["manualDisabled"] != false {
		t.Fatalf("explicit recovery changed intent/manual state or lacked ACK: %+v", state)
	}
	assertOtherPause := func() {
		t.Helper()
		gates := f.ledger()["gates"].([]any)
		if len(gates) != 1 || gates[0].(map[string]any)["owner"] != otherOwner || f.operations.Load() != 3 {
			t.Fatalf("recovery changed another owner or replayed: %+v", gates)
		}
	}
	assertOtherPause()
	f.api("POST", recoveryPath, request, 409)
	stop()
	stop = f.startRuntimeMain("recovery-restart", "", "", "")
	state = f.api("GET", f.policyPath(), nil, 200)
	if state["enabled"] != false || state["ownPaused"] != false || state["state"] != "recovered_off" || state["coordination"].(map[string]any)["receiptPersisted"] != true {
		t.Fatalf("recovery was not durable across actual main restart: %+v", state)
	}
	assertOtherPause()
}
