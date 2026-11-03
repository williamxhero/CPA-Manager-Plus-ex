package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
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

// No fake policy evaluator or fabricated Core ACK is used: a loopback bridge
// forwards to the pinned ordinary Core and deliberately loses an actual ACK.
func TestProviderPolicyLostACKRestartAndLateOFFCompletion(t *testing.T) {
	providerPolicyRecoveryCase(t, "")
}
func TestProviderPolicyRejectsSupersededClearAfterUnacceptedPOST(t *testing.T) {
	for _, mode := range []string{"contrary_success", "source_replaced"} {
		t.Run(mode, func(t *testing.T) { providerPolicyRecoveryCase(t, mode) })
	}
}
func providerPolicyRecoveryCase(t *testing.T, contrary string) {
	core := moneyPolicyCore(t)
	target, _ := url.Parse(core)
	proxy := httputil.NewSingleHostReverseProxy(target)
	var hideReceipts, dropACK, rejectBeforeForward atomic.Bool
	var operations atomic.Int64
	proxy.ModifyResponse = func(r *http.Response) error {
		if r.Request.Method == "POST" && r.Request.URL.Path == "/v8/management/inference-gates/operations" {
			operations.Add(1)
			if dropACK.CompareAndSwap(true, false) {
				_, _ = io.Copy(io.Discard, r.Body)
				_ = r.Body.Close()
				r.StatusCode = 503
				r.Body = io.NopCloser(strings.NewReader(`{"error":"controlled_lost_ack"}`))
				r.ContentLength = -1
			}
		}
		return nil
	}
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rejectBeforeForward.Load() && r.Method == "POST" && r.URL.Path == "/v8/management/inference-gates/operations" {
			w.WriteHeader(503)
			return
		}
		if hideReceipts.Load() && r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/v8/management/inference-gates/operations/") {
			w.WriteHeader(503)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(bridge.Close)
	cfg := testutil.NewConfig(t)
	protector, err := security.NewProtector([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(cfg.DBPath, protector)
	if err != nil {
		t.Fatal(err)
	}
	testutil.EnsureAdminCredential(t, db)
	if err = db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: bridge.URL, ManagementKey: "MONEY-CORE-MANAGEMENT"}}); err != nil {
		t.Fatal(err)
	}
	manager := New(cfg, db, collector.NewManager(cfg, db))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := manager.AppContext().FullRefreshService.Shutdown(ctx); err != nil {
			t.Error(err)
		}
		_ = db.Close()
	})
	api := func(method, path string, q any, want int) map[string]any {
		t.Helper()
		body := ""
		if q != nil {
			raw, _ := json.Marshal(q)
			body = string(raw)
		}
		r := testutil.Request(t, manager.Handler(), method, path, body, testutil.AdminKey)
		testutil.RequireStatus(t, r, want)
		var out map[string]any
		testutil.DecodeJSON(t, r, &out)
		return out
	}
	inv := manager.AppContext().ResourceCapabilityService.Inventory(context.Background(), true)
	id := ""
	if inv.Snapshot != nil {
		for _, r := range inv.Snapshot.Resources {
			if r.Kind == "provider" {
				id = r.ResourceID
			}
		}
	}
	if id == "" {
		t.Fatal("actual issued provider absent")
	}
	path := "/usage-service/provider-policy?providerId=" + id
	state := api("GET", path, nil, 200)
	query := map[string]any{"scope": state["scope"], "targets": state["targets"], "version": 0, "adapter": "deepseek", "endpoint": "https://api.deepseek.com/user/balance", "privateTargets": []string{"127.0.0.1"}, "secret": "MONEY-SYNTHETIC-INFERENCE"}
	api("PUT", balancePath, query, 200)
	var response atomic.Value
	response.Store("-0.05")
	type blocker struct{ started, release chan struct{} }
	var held atomic.Pointer[blocker]
	network := policyMoneyNetwork(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/user/balance" || r.Header.Get("Authorization") != "Bearer MONEY-SYNTHETIC-INFERENCE" {
			t.Error("invalid controlled official request")
			w.WriteHeader(400)
			return
		}
		amount := response.Load().(string)
		if b := held.Load(); b != nil {
			close(b.started)
			select {
			case <-b.release:
			case <-r.Context().Done():
				return
			}
		}
		if amount == "NULL" {
			fmt.Fprint(w, `{"is_available":true,"balance_infos":[{"currency":"USD","total_balance":null}]}`)
			return
		}
		fmt.Fprintf(w, `{"is_available":true,"balance_infos":[{"currency":"USD","total_balance":%q,"granted_balance":"0","topped_up_balance":%q}]}`, amount, amount)
	}))
	manager.AppContext().OfficialBalanceService.Querier = balance.NewWithNetwork(network)
	completed := make(chan string, 32)
	subscribe := func() {
		manager.AppContext().FullRefreshService.Subscribe(func(_ context.Context, e fr.CompletedObservation) { completed <- e.AttemptID })
	}
	subscribe()
	update := func(enabled bool) {
		t.Helper()
		state = api("PUT", "/usage-service/provider-policy", map[string]any{"scope": state["scope"], "targets": state["targets"], "providerId": id, "expectedGeneration": state["generation"], "enabled": enabled}, 200)
	}
	start := func() string {
		t.Helper()
		j := api("POST", balancePath, map[string]any{"scope": state["scope"], "targets": state["targets"], "version": 1}, 202)
		return j["id"].(string)
	}
	await := func(id string) {
		t.Helper()
		deadline := time.NewTimer(15 * time.Second)
		defer deadline.Stop()
		for {
			select {
			case actual := <-completed:
				if actual == id {
					return
				}
			case <-deadline.C:
				t.Fatal("real source completion observer did not finish")
				return
			}
		}
	}
	reconcile := func() {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := manager.AppContext().ProviderPolicyService.Reconcile(ctx); err != nil {
			t.Fatal(err)
		}
	}
	update(true)
	hideReceipts.Store(true)
	dropACK.Store(true)
	await(start())
	state = api("GET", path, nil, 200)
	if operations.Load() != 1 || state["ownPaused"] != true || state["applied"] == true || state["state"] != "pending" {
		t.Fatalf("lost ACK was optimistic or lost actual pause: %+v calls=%d", state, operations.Load())
	}
	// A GET must neither replay the immutable operation nor mint a new one.
	api("GET", path, nil, 200)
	if operations.Load() != 1 {
		t.Fatal("status GET performed a Core operation")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := manager.AppContext().FullRefreshService.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(cfg.DBPath, protector)
	if err != nil {
		t.Fatal(err)
	}
	manager = New(cfg, db, collector.NewManager(cfg, db))
	manager.AppContext().OfficialBalanceService.Querier = balance.NewWithNetwork(network)
	subscribe()
	hideReceipts.Store(false)
	reconcile()
	state = api("GET", path, nil, 200)
	if state["ownPaused"] != true || state["applied"] != true || operations.Load() != 1 {
		t.Fatal("restart failed to confirm original operation without duplicate")
	}
	// Missing monetary data must retain the low reading but not renew or clear it.
	response.Store("NULL")
	await(start())
	state = api("GET", path, nil, 200)
	if state["ownPaused"] != true || operations.Load() != 1 {
		t.Fatal("NULL response became a recovery or zero")
	}
	if contrary != "" {
		response.Store("1.00")
		rejectBeforeForward.Store(true)
		await(start())
		if operations.Load() != 1 {
			t.Fatal("controlled rejected clear reached Core")
		}
		rejectBeforeForward.Store(false)
		if contrary == "contrary_success" {
			response.Store("-0.05")
			await(start())
		} else {
			query["version"] = 1
			query["secret"] = "MONEY-NONMATCHING-QUERY-REPLACEMENT"
			api("PUT", balancePath, query, 200)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		_ = manager.AppContext().ProviderPolicyService.Reconcile(ctx)
		cancel()
		state = api("GET", path, nil, 200)
		if operations.Load() != 1 || state["ownPaused"] != true || state["state"] != "pending" {
			t.Fatalf("superseded clear was first-committed: calls=%d state=%+v", operations.Load(), state)
		}
		return
	}
	// Capture a successful high read under ON, then turn OFF before it completes.
	response.Store("1.00")
	b := &blocker{make(chan struct{}), make(chan struct{})}
	held.Store(b)
	t.Cleanup(func() {
		select {
		case <-b.release:
		default:
			close(b.release)
		}
	})
	job := start()
	select {
	case <-b.started:
	case <-time.After(5 * time.Second):
		t.Fatal("controlled source was not reached")
	}
	update(false)
	close(b.release)
	held.Store(nil)
	await(job)
	state = api("GET", path, nil, 200)
	if state["enabled"] != false || state["ownPaused"] != true || operations.Load() != 1 {
		t.Fatal("late ON completion resumed after OFF")
	}
	update(true)
	reconcile()
	state = api("GET", path, nil, 200)
	if state["ownPaused"] != true || operations.Load() != 1 {
		t.Fatal("re-enabling consumed an old generation's success")
	}
	await(start())
	reconcile()
	state = api("GET", path, nil, 200)
	if state["ownPaused"] != false || state["applied"] != true || operations.Load() != 2 {
		t.Fatalf("fresh recovery did not clear only once: %+v calls=%d", state, operations.Load())
	}
	// The journal is durable and ordinary SQLite remains internally consistent.
	inspection, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer inspection.Close()
	var integrity string
	if err := inspection.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("SQLite integrity=%s error=%v", integrity, err)
	}
}
