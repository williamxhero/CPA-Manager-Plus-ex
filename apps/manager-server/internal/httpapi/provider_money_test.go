package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestProviderMoneyExplicitEnableRequiresCurrentFunding(t *testing.T) {
	f := newProviderFundingFixture(t)
	f.currentSources()
	before := f.ledger()
	state := f.api("GET", f.policyPath(), nil, 200)
	state = f.api("PUT", "/usage-service/provider-policy", map[string]any{
		"providerId": f.providerID, "scope": state["scope"], "targets": state["targets"],
		"expectedGeneration": 0, "enabled": true,
	}, 200)
	if state["enabled"] != true || state["effective"] != true || state["generation"] != float64(1) || state["locked"] != false {
		t.Fatalf("valid explicit enable was not effective: %+v", state)
	}
	f.unchanged(before)
	f.reopen()
	state = f.api("GET", f.policyPath(), nil, 200)
	if state["enabled"] != true || state["effective"] != true || state["generation"] != float64(1) {
		t.Fatalf("enabled generation did not survive SQLite reopen: %+v", state)
	}
	f.unchanged(before)
}

func TestProviderMoneyExactThresholdAndOffRetainsPause(t *testing.T) {
	f := newProviderFundingFixture(t)
	var total atomic.Value
	total.Store("0.1000000000000000000000000000")
	f.manager.AppContext().OfficialBalanceService.Querier = balance.NewWithNetwork(providerFundingNetwork(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"is_available":true,"balance_infos":[{"currency":"USD","total_balance":%q,"granted_balance":"0","topped_up_balance":"0"},{"currency":"CNY","total_balance":"0","granted_balance":"0","topped_up_balance":"0"}]}`, total.Load().(string))
	}))
	for _, key := range []string{"FUNDING-SYNTHETIC-A1", "FUNDING-SYNTHETIC-A2"} {
		f.configureAndRefresh(key, key, 0)
	}
	f.enableMoney(true)
	f.refreshMoney("FUNDING-SYNTHETIC-A1", 1)
	f.refreshMoney("FUNDING-SYNTHETIC-A2", 1)
	f.awaitMoneyPaused(true)
	for _, low := range []string{"0.0500", "0.1", "-0.0001"} {
		total.Store(low)
		f.awaitMoneyEvaluation(f.refreshMoney("FUNDING-SYNTHETIC-A1", 1))
		f.awaitMoneyPaused(true)
		if f.operations.Load() != 1 {
			t.Fatal("low values oscillated the gate")
		}
	}
	total.Store("0.1000000000000000000000000001")
	f.refreshMoney("FUNDING-SYNTHETIC-A1", 1)
	f.awaitMoneyPaused(false)
	if f.operations.Load() != 2 {
		t.Fatal("exactly-above threshold was rounded down or replayed")
	}
	total.Store("0.1")
	f.refreshMoney("FUNDING-SYNTHETIC-A1", 1)
	f.awaitMoneyPaused(true)
	if f.operations.Load() != 3 {
		t.Fatal("exact boundary did not pause again after recovery")
	}
	state := f.enableMoney(false)
	if state["enabled"] != false || state["effective"] != false || state["ownPaused"] != true || state["state"] != "historical_pause" {
		t.Fatalf("OFF hid or cleared historical own pause: %+v", state)
	}
	f.reopen()
	state = f.api("GET", f.policyPath(), nil, 200)
	if state["ownPaused"] != true || f.operations.Load() != 3 {
		t.Fatal("OFF/reopen lost or automatically cleared historical pause")
	}
}

func (f *providerFundingFixture) enableMoney(enabled bool) map[string]any {
	f.t.Helper()
	state := f.api("GET", f.policyPath(), nil, 200)
	return f.api("PUT", "/usage-service/provider-policy", map[string]any{
		"providerId": f.providerID, "scope": state["scope"], "targets": state["targets"],
		"expectedGeneration": state["generation"], "enabled": enabled,
	}, 200)
}

func TestProviderMoneyNewHighRecoversWithFailedPeerAndKeepsOtherOwner(t *testing.T) {
	f := newProviderFundingFixture(t)
	var high, failPeer atomic.Bool
	f.manager.AppContext().OfficialBalanceService.Querier = balance.NewWithNetwork(providerFundingNetwork(t, func(w http.ResponseWriter, r *http.Request) {
		if failPeer.Load() && r.Header.Get("Authorization") == "Bearer FUNDING-SYNTHETIC-A2" {
			w.WriteHeader(500)
			return
		}
		total := "0.0500"
		if high.Load() {
			total = "0.1000000000000000000001"
		}
		fmt.Fprintf(w, `{"is_available":true,"balance_infos":[{"currency":"USD","total_balance":%q,"granted_balance":"0","topped_up_balance":"0"}]}`, total)
	}))
	for _, key := range []string{"FUNDING-SYNTHETIC-A1", "FUNDING-SYNTHETIC-A2"} {
		f.configureAndRefresh(key, key, 0)
	}
	f.enableMoney(true)
	// Observe each durable completion before issuing another query or expensive
	// full status polling against the same authenticated Core. This is harness
	// backpressure; the product callback's 20s authorization budget is unchanged.
	refreshAndWait := func(key string) map[string]any {
		t.Helper()
		job := f.refreshMoney(key, 1)
		time.Sleep(8 * time.Second)
		f.awaitMoneyEvaluation(job)
		return job
	}
	refreshAndWait("FUNDING-SYNTHETIC-A1")
	refreshAndWait("FUNDING-SYNTHETIC-A2")
	f.awaitMoneyPaused(true)
	request := f.proofRequest()
	raw, _ := json.Marshal(request)
	gate := f.api("GET", "/usage-service/provider-gate?binding="+url.QueryEscape(string(raw)), nil, 200)
	request["version"], request["sourceVersion"], request["expectedGeneration"], request["action"] = 1, gate["sourceVersion"], gate["generation"], "pause"
	f.api("PUT", "/usage-service/provider-gate", request, 200)
	failPeer.Store(true)
	failed := refreshAndWait("FUNDING-SYNTHETIC-A2")["status"].(map[string]any)
	if failed["stale"] != true || failed["lastSuccess"].(map[string]any)["balances"].([]any)[0].(map[string]any)["total"] != "0.0500" {
		t.Fatal("failed peer lost its last success")
	}
	high.Store(true)
	refreshAndWait("FUNDING-SYNTHETIC-A1")
	f.awaitMoneyPaused(false)
	ledger := f.ledger()
	gates := ledger["gates"].([]any)
	if len(gates) != 1 || gates[0].(map[string]any)["owner"] != gate["owner"] || f.operations.Load() != 3 {
		t.Fatalf("recovery cleared another owner or failed to apply: %+v", ledger)
	}
}

func (f *providerFundingFixture) refreshMoney(key string, version int64) map[string]any {
	f.t.Helper()
	job := f.api("POST", balancePath, ob.Query{Binding: f.members[key], Version: version}, 202)
	for deadline := time.Now().Add(20 * time.Second); ; {
		current := f.api("GET", balancePath+"?job="+job["id"].(string), nil, 200)
		if current["state"] == "complete" {
			return current
		}
		if current["state"] != "running" || time.Now().After(deadline) {
			f.t.Fatalf("refresh did not complete: %+v", current)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (f *providerFundingFixture) awaitMoneyEvaluation(job map[string]any) map[string]any {
	f.t.Helper()
	for deadline := time.Now().Add(25 * time.Second); ; {
		state := f.api("GET", f.policyPath(), nil, 200)
		if evaluation, ok := state["evaluation"].(map[string]any); ok && evaluation["attemptId"] == job["id"] && evaluation["state"] != "pending" {
			return evaluation
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("durable evaluation missing for %v: %+v", job["id"], state)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestProviderMoneyRejectedEvidenceIsVisibleWithoutGateSideEffects(t *testing.T) {
	f := newProviderFundingFixture(t)
	var total atomic.Value
	total.Store("0.0500")
	f.manager.AppContext().OfficialBalanceService.Querier = balance.NewWithNetwork(providerFundingNetwork(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"is_available":true,"balance_infos":[{"currency":"USD","total_balance":%q,"granted_balance":"0","topped_up_balance":"0"}]}`, total.Load().(string))
	}))
	for _, key := range []string{"FUNDING-SYNTHETIC-A1", "FUNDING-SYNTHETIC-A2"} {
		f.configureAndRefresh(key, key, 0)
	}
	f.enableMoney(true)
	before := f.ledger()
	f.awaitMoneyEvaluation(f.refreshMoney("FUNDING-SYNTHETIC-A1", 1))
	f.enableMoney(false)
	f.enableMoney(true)
	f.awaitMoneyEvaluation(f.refreshMoney("FUNDING-SYNTHETIC-A2", 1))
	f.unchanged(before) // A1's old enabling generation cannot complete a new group proof
	f.awaitMoneyEvaluation(f.refreshMoney("FUNDING-SYNTHETIC-A1", 1))
	f.awaitMoneyPaused(true)
	paused := f.ledger()
	for _, invalid := range []string{"", "NaN", "Infinity"} {
		t.Run("invalid_"+invalid, func(t *testing.T) {
			parent := f.t
			f.t = t
			defer func() { f.t = parent }()
			total.Store(invalid)
			job := f.refreshMoney("FUNDING-SYNTHETIC-A1", 1)
			evaluation := f.awaitMoneyEvaluation(job)
			status := job["status"].(map[string]any)
			if evaluation["state"] != "rejected" || status["stale"] != true || status["lastSuccess"].(map[string]any)["balances"].([]any)[0].(map[string]any)["total"] != "0.0500" {
				t.Fatal("invalid amount became zero/success or lost lastSuccess")
			}
		})
	}
	total.Store("0.0500")
	f.manager.AppContext().ProviderPolicyService.Clock = func() time.Time { return time.Now().Add(time.Hour) }
	if f.awaitMoneyEvaluation(f.refreshMoney("FUNDING-SYNTHETIC-A1", 1))["state"] != "rejected" {
		t.Fatal("stale money was accepted")
	}
	f.manager.AppContext().ProviderPolicyService.Clock = time.Now
	f.proofFault.Store(1)
	if f.awaitMoneyEvaluation(f.refreshMoney("FUNDING-SYNTHETIC-A2", 1))["state"] != "rejected" {
		t.Fatal("partial route proof was accepted")
	}
	f.proofFault.Store(0)
	f.configureAndRefresh("FUNDING-SYNTHETIC-A1", "FUNDING-SYNTHETIC-A2", 1)
	f.awaitMoneyEvaluation(f.refreshMoney("FUNDING-SYNTHETIC-A1", 2))
	state := f.api("GET", f.policyPath(), nil, 200)
	f.api("PUT", "/usage-service/provider-policy", map[string]any{"scope": state["scope"], "targets": state["targets"], "providerId": f.providerID, "expectedGeneration": state["generation"], "enabled": true}, 503)
	if f.operations.Load() != 1 || !reflect.DeepEqual(paused, f.ledger()) {
		t.Fatal("rejected evidence changed the existing real Core gate")
	}
}

func (f *providerFundingFixture) awaitMoneyPaused(paused bool) map[string]any {
	f.t.Helper()
	for deadline := time.Now().Add(20 * time.Second); ; {
		state := f.api("GET", f.policyPath(), nil, 200)
		if state["ownPaused"] == paused && state["applied"] == true {
			return state
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("automatic monetary ACK missing, want pause %v: %+v", paused, state)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestProviderMoneyNewLowEvidencePausesOnlyOwnProviderAndPersists(t *testing.T) {
	f := newProviderFundingFixture(t)
	f.currentSources()
	f.enableMoney(true)
	before := f.ledger()
	f.awaitMoneyEvaluation(f.refreshMoney("FUNDING-SYNTHETIC-A1", 1))
	f.unchanged(before) // sibling's pre-enable cached low is not new evidence
	f.refreshMoney("FUNDING-SYNTHETIC-A2", 1)
	state := f.awaitMoneyPaused(true)
	if f.operations.Load() != 1 {
		t.Fatal("new low evidence did not emit exactly one operation")
	}
	ledger := f.ledger()
	gates := ledger["gates"].([]any)
	if len(gates) != 1 || gates[0].(map[string]any)["owner"] != state["owner"] || len(gates[0].(map[string]any)["targets"].([]any)) != 3 {
		t.Fatalf("actual Core did not durably restrict the full A closure alone: %+v", ledger)
	}
	f.reopen()
	f.awaitMoneyPaused(true)
	for _, phase := range []string{"a", "a", "b"} {
		before := f.upstreamAttempts.Load()
		request, _ := http.NewRequest("POST", f.base+"/v1/chat/completions", strings.NewReader(`{"model":"`+phase+`/controlled-model","messages":[{"role":"user","content":"controlled fixture"}]}`))
		request.Header.Set("Authorization", "Bearer FUNDING-SYNTHETIC-CLIENT")
		request.Header.Set("Content-Type", "application/json")
		res, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if phase == "a" && (res.StatusCode != 503 || f.upstreamAttempts.Load() != before) {
			t.Fatalf("A escaped its per-member restriction: status=%d %s", res.StatusCode, body)
		}
		if phase == "b" && f.upstreamAttempts.Load() <= before {
			t.Fatalf("B was incorrectly prevented from reaching the controlled deny proxy: status=%d %s", res.StatusCode, body)
		}
	}
	// Balance reads remain independently authorized while inference is paused.
	f.manager.AppContext().OfficialBalanceService.Querier = balance.NewWithNetwork(providerFundingProofNetwork(t))
	f.refreshMoney("FUNDING-SYNTHETIC-A1", 1)
	if f.operations.Load() != 1 {
		t.Fatal("reopen/source replacement replayed a monetary command")
	}
}

func TestProviderMoneyCachedHighCannotClearReenabledHistoricalPause(t *testing.T) {
	f := newProviderFundingFixture(t)
	var total atomic.Value
	total.Store("0.05")
	f.manager.AppContext().OfficialBalanceService.Querier = balance.NewWithNetwork(providerFundingNetwork(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"is_available":true,"balance_infos":[{"currency":"USD","total_balance":%q,"granted_balance":"0","topped_up_balance":"0"}]}`, total.Load().(string))
	}))
	for _, key := range []string{"FUNDING-SYNTHETIC-A1", "FUNDING-SYNTHETIC-A2"} {
		f.configureAndRefresh(key, key, 0)
	}
	f.enableMoney(true)
	f.awaitMoneyEvaluation(f.refreshMoney("FUNDING-SYNTHETIC-A1", 1))
	f.awaitMoneyEvaluation(f.refreshMoney("FUNDING-SYNTHETIC-A2", 1))
	f.awaitMoneyPaused(true)
	f.enableMoney(false)
	total.Store("0.2")
	f.refreshMoney("FUNDING-SYNTHETIC-A1", 1) // successful while OFF, no current enabling fence
	f.enableMoney(true)
	total.Store("0.05")
	f.awaitMoneyEvaluation(f.refreshMoney("FUNDING-SYNTHETIC-A2", 1))
	f.awaitMoneyPaused(true)
	if f.operations.Load() != 1 {
		t.Fatal("cached pre-enable high cleared a historical pause")
	}
	total.Store("0.2")
	f.awaitMoneyEvaluation(f.refreshMoney("FUNDING-SYNTHETIC-A1", 1))
	f.awaitMoneyPaused(false)
	if f.operations.Load() != 2 {
		t.Fatal("new post-enable high did not clear exactly once")
	}
}

func TestProviderMoneyFinalSourceClosingRejectsReplacementDuringOtherRead(t *testing.T) {
	f := newProviderFundingFixture(t)
	f.currentSources()
	proof := f.api("POST", fundingProofPath, f.proofRequest(), 200)
	firstID := proof["sources"].([]any)[0].(map[string]any)["memberIds"].([]any)[0].(string)
	firstKey := ""
	for key, binding := range f.members {
		if binding.Targets[0].ResourceID == firstID {
			firstKey = key
		}
	}
	if firstKey == "" {
		t.Fatal("first proof member missing")
	}
	f.enableMoney(true)
	f.awaitMoneyEvaluation(f.refreshMoney("FUNDING-SYNTHETIC-A1", 1))
	before := f.ledger()
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	var once sync.Once
	defer once.Do(func() { close(release) })
	// External time is paused at the second source in the final authorization:
	// one trigger check, five funding checks, two checks per authorization.
	f.manager.AppContext().ProviderPolicyService.Clock = func() time.Time {
		if calls.Add(1) == 10 {
			close(entered)
			<-release
		}
		return time.Now()
	}
	job := f.refreshMoney("FUNDING-SYNTHETIC-A2", 1)
	select {
	case <-entered:
	case <-time.After(20 * time.Second):
		t.Fatal("final source interleaving not reached")
	}
	f.api("PUT", balancePath, ob.Update{Config: ob.Config{Binding: f.members[firstKey], Version: 1, Adapter: "deepseek", Endpoint: "https://api.deepseek.com/user/balance", PrivateTargets: []string{"127.0.0.1"}}, Secret: firstKey}, 200)
	once.Do(func() { close(release) })
	evaluation := f.awaitMoneyEvaluation(job)
	if evaluation["state"] != "rejected" {
		t.Errorf("superseded monetary source was not rejected: %+v", evaluation)
	}
	f.unchanged(before)
	// A command proven unsent must not poison later policy generations.
	f.awaitMoneyEvaluation(f.refreshMoney(firstKey, 2))
	f.enableMoney(true)
	for _, key := range []string{"FUNDING-SYNTHETIC-A1", "FUNDING-SYNTHETIC-A2"} {
		version := int64(1)
		if key == firstKey {
			version = 2
		}
		f.awaitMoneyEvaluation(f.refreshMoney(key, version))
	}
	f.awaitMoneyPaused(true)
	if f.operations.Load() != 1 {
		t.Fatal("known non-dispatch was replayed or blocked new evidence")
	}
}

func TestProviderMoneyCanceledProofCannotSendAndRestartCanUseNewEvidence(t *testing.T) {
	f := newProviderFundingFixture(t)
	var blocking atomic.Bool
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer close(release)
	f.beforeMatch = func() {
		if blocking.Load() {
			once.Do(func() { close(entered) })
			<-release
		}
	}
	f.currentSources()
	f.enableMoney(true)
	f.awaitMoneyEvaluation(f.refreshMoney("FUNDING-SYNTHETIC-A1", 1))
	before := f.ledger()
	blocking.Store(true)
	job := f.refreshMoney("FUNDING-SYNTHETIC-A2", 1)
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("controlled proof did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := f.manager.AppContext().FullRefreshService.Shutdown(ctx); err != nil {
		cancel()
		t.Fatal(err)
	}
	cancel()
	if f.awaitMoneyEvaluation(job)["state"] != "rejected" {
		t.Fatal("canceled proof was marked evaluated")
	}
	f.unchanged(before)
	blocking.Store(false)
	// The blocked HTTP peer can finish after cancellation; it has no host send authority.
	f.reopen()
	f.manager.AppContext().OfficialBalanceService.Querier = balance.NewWithNetwork(providerFundingProofNetwork(t))
	f.awaitMoneyEvaluation(f.refreshMoney("FUNDING-SYNTHETIC-A1", 1))
	f.awaitMoneyPaused(true)
	if f.operations.Load() != 1 {
		t.Fatal("restart replayed old commands or failed to use new evidence")
	}
}
