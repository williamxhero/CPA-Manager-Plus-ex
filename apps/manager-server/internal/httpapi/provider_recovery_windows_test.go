package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
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
	if fixture := os.Getenv("PROVIDER82_RECOVERY_LIVE_UI"); fixture != "" {
		raw, _ := json.Marshal(map[string]any{"base": f.apiBase, "managementKey": testutil.AdminKey, "binding": map[string]any{"scope": state["scope"], "targets": state["targets"]}, "providerId": f.providerID})
		if err := os.WriteFile(fixture, raw, 0600); err != nil {
			t.Fatal(err)
		}
		for deadline := time.Now().Add(60 * time.Second); ; {
			if _, err := os.Stat(fixture + ".stop"); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("local recovery UI timed out")
			}
			time.Sleep(50 * time.Millisecond)
		}
		state = f.api("GET", f.policyPath(), nil, 200)
	} else {
		state = f.api("POST", recoveryPath, request, 200)
	}
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

func TestProviderRecoveryKnownUnsentAllowsFreshConfirmation(t *testing.T) {
	f, _ := historicalRecoveryFixture(t)
	f.reopen()
	state := f.api("GET", f.policyPath(), nil, 200)
	// The fifth resource read is the final authority fence after journalling,
	// before the only possible Core RPC. Fault only that external read.
	f.inventoryFailAt.Store(f.inventoryReads.Load() + 5)
	f.api("POST", recoveryPath, recoveryRequest(state), 409)
	state = f.api("GET", f.policyPath(), nil, 200)
	if state["coordination"].(map[string]any)["state"] != "discarded" || state["ownPaused"] != true || f.operations.Load() != 2 {
		t.Fatalf("known non-dispatch changed the actual pause: %+v", state)
	}
	if state["recoveryAvailable"] != true {
		t.Fatal("restored current authority cannot offer a fresh recovery confirmation")
	}
	state = f.api("POST", recoveryPath, recoveryRequest(state), 200)
	if state["enabled"] != false || state["state"] != "recovered_off" || f.operations.Load() != 3 {
		t.Fatal("fresh authorized recovery after known non-dispatch failed")
	}
}

func TestProviderRecoveryMainRejectsInvalidAndNeverReplaysUnknown(t *testing.T) {
	f, other := historicalRecoveryFixture(t)
	stop := f.startRuntimeMain("unknown-first", "", "", "")
	defer func() { stop() }()
	state := f.api("GET", f.policyPath(), nil, 200)
	request := recoveryRequest(state)
	raw, _ := json.Marshal(request)
	t.Run("authentication", func(t *testing.T) {
		req, _ := http.NewRequest("POST", f.apiBase+recoveryPath, bytes.NewReader(raw))
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != 401 {
			t.Fatalf("unauthenticated status=%d", res.StatusCode)
		}
	})
	t.Run("method", func(t *testing.T) { f.api("GET", recoveryPath, nil, 405) })
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
		code   int
	}{
		{"owner_injection", func(q map[string]any) { q["owner"] = "another-owner" }, 400},
		{"missing_generation", func(q map[string]any) { delete(q, "expectedGeneration") }, 400},
		{"policy_generation", func(q map[string]any) { q["expectedGeneration"] = 999 }, 409},
		{"gate_generation", func(q map[string]any) { q["expectedGateGeneration"] = 999 }, 409},
		{"source_version", func(q map[string]any) { q["sourceVersion"] = strings.Repeat("0", 64) }, 503},
		{"scope", func(q map[string]any) { q["scope"].(map[string]any)["instance_incarnation"] = "wrong" }, 409},
		{"member_version", func(q map[string]any) { q["targets"].([]any)[1].(map[string]any)["credential_version"] = 999 }, 409},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var q map[string]any
			if err := json.Unmarshal(raw, &q); err != nil {
				t.Fatal(err)
			}
			tc.mutate(q)
			f.api("POST", recoveryPath, q, tc.code)
		})
	}
	f.proofFault.Store(1)
	f.api("POST", recoveryPath, request, 503)
	f.proofFault.Store(0)
	if f.operations.Load() != 2 || len(f.ledger()["gates"].([]any)) != 2 {
		t.Fatal("refusal changed a Core gate")
	}
	f.loseAck.Store(true)
	f.hideReceipt.Store(true)
	f.api("POST", recoveryPath, request, 503)
	state = f.api("GET", f.policyPath(), nil, 200)
	if state["state"] != "recovery_pending" || state["applied"] != false || state["recoveryAvailable"] != false || state["enabled"] != false {
		t.Fatalf("unknown became success: %+v", state)
	}
	f.api("POST", recoveryPath, request, 409)
	f.api("POST", recoveryPath, recoveryRequest(state), 503)
	stop()
	stop = f.startRuntimeMain("unknown-restart", "", "", "")
	time.Sleep(6 * time.Second)
	state = f.api("GET", f.policyPath(), nil, 200)
	if state["state"] != "recovery_pending" || state["coordination"].(map[string]any)["receiptPersisted"] != false || f.operations.Load() != 3 {
		t.Fatal("unknown replayed or became applied after restart")
	}
	f.api("POST", recoveryPath, recoveryRequest(state), 503)
	f.hideReceipt.Store(false)
	for deadline := time.Now().Add(30 * time.Second); ; {
		state = f.api("GET", f.policyPath(), nil, 200)
		if state["state"] == "recovered_off" && state["coordination"].(map[string]any)["receiptPersisted"] == true {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("receipt never reconciled: %+v", state)
		}
		time.Sleep(250 * time.Millisecond)
	}
	gates := f.ledger()["gates"].([]any)
	if len(gates) != 1 || gates[0].(map[string]any)["owner"] != other || f.operations.Load() != 3 || state["enabled"] != false {
		t.Fatal("reconciliation changed owner/intent or resent")
	}
	raw, _ = json.MarshalIndent(state, "", "  ")
	if err := os.WriteFile(filepath.Join(os.Getenv("PROVIDER82_PROCESS_LOG_DIR"), "unknown-recovered-status.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestProviderRecoveryMainPreservesManualDisable(t *testing.T) {
	f, _ := historicalRecoveryFixture(t)
	stop := f.startRuntimeMain("manual-disabled", "", "", "")
	defer stop()
	state := f.api("GET", f.policyPath(), nil, 200)
	request := recoveryRequest(state)
	raw := []byte(`{"name":"funding-a","value":{"disabled":true}}`)
	req, _ := http.NewRequest("PATCH", f.base+"/v0/management/openai-compatibility", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer controlled-core-key")
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("native manual-disable status=%d", res.StatusCode)
	}
	state = f.api("GET", f.policyPath(), nil, 200)
	if state["manualDisabled"] != true || state["recoveryAvailable"] == true {
		t.Fatalf("manual-disable was not fail-closed: %+v", state)
	}
	// Disabling removes runtime route authority; the recovery must not fabricate
	// it or enable native config simply to obtain a clear receipt.
	raw, _ = json.Marshal(request)
	req, _ = http.NewRequest("POST", f.apiBase+recoveryPath, bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+testutil.AdminKey)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 409 && res.StatusCode != 503 {
		t.Fatalf("manual-disabled recovery status=%d", res.StatusCode)
	}
	state = f.api("GET", f.policyPath(), nil, 200)
	if state["manualDisabled"] != true || state["enabled"] != false || len(f.ledger()["gates"].([]any)) != 2 || f.operations.Load() != 2 {
		t.Fatal("recovery changed native manual state or gates")
	}
}
