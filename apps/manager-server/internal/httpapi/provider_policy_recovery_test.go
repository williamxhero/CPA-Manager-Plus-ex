package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	pp "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/providerpolicy"
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

func TestProviderPolicyRevalidatesMoneyAfterDispatchPreparation(t *testing.T) {
	providerPolicyRecoveryCase(t, "contrary_during_prepare")
}

func TestProviderPolicyRevalidatesSourceAfterDispatchPreparation(t *testing.T) {
	providerPolicyRecoveryCase(t, "source_replaced_during_prepare")
}

func TestProviderPolicyRevalidatesFailureAfterDispatchPreparation(t *testing.T) {
	providerPolicyRecoveryCase(t, "failure_during_prepare")
}

func providerPolicyRecoveryCase(t *testing.T, contrary string) {
	core := moneyPolicyCore(t)
	target, _ := url.Parse(core)
	proxy := httputil.NewSingleHostReverseProxy(target)
	var hideReceipts, dropACK, rejectBeforeForward atomic.Bool
	var operations atomic.Int64
	rejected := make(chan struct{}, 8)
	var db *store.Store
	var armPreparation, matchedPendingClear atomic.Bool
	preparationHeld := make(chan context.Context, 1)
	preparationResumed, releasePreparation := make(chan bool, 1), make(chan struct{}, 1)
	defer close(releasePreparation)
	pendingClear := func() bool {
		if db == nil {
			return false
		}
		policies, err := db.ProviderPolicies(context.Background())
		if err != nil {
			t.Error(err)
			return false
		}
		for _, policy := range policies {
			var document struct{ Pending *struct{ Action string } }
			if err := json.Unmarshal([]byte(policy.Document), &document); err != nil {
				t.Error(err)
				return false
			}
			if document.Pending != nil && document.Pending.Action == "clear" {
				return true
			}
		}
		return false
	}
	proxy.ModifyResponse = func(r *http.Response) error {
		if armPreparation.Load() && r.StatusCode == http.StatusOK && r.Request.Method == "POST" && r.Request.URL.Path == "/v8/management/inference-gates/providers/key-match" && pendingClear() {
			matchedPendingClear.Store(true)
		}
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
		// The pending clear's last funding challenge has finished. The next
		// ledger read belongs to dispatch preparation, after money was checked
		// but before the immutable command is journaled/sent. Hold only this
		// exchange; concurrent source acquisition remains real and available.
		if r.Method == "GET" && r.URL.Path == "/v8/management/inference-gates" && matchedPendingClear.CompareAndSwap(true, false) && armPreparation.CompareAndSwap(true, false) {
			preparationHeld <- r.Context()
			select {
			case <-releasePreparation:
				preparationResumed <- r.Context().Err() == nil
			case <-r.Context().Done():
				preparationResumed <- false
				return
			}
		}
		if rejectBeforeForward.Load() && r.Method == "POST" && r.URL.Path == "/v8/management/inference-gates/operations" {
			select {
			case rejected <- struct{}{}:
			default:
			}
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
	db, err = store.Open(cfg.DBPath, protector)
	if err != nil {
		t.Fatal(err)
	}
	testutil.EnsureAdminCredential(t, db)
	if err = db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: bridge.URL, ManagementKey: "MONEY-CORE-MANAGEMENT"}}); err != nil {
		t.Fatal(err)
	}
	manager := New(cfg, db, collector.NewManager(cfg, db))
	var stopPolicy func()
	t.Cleanup(func() {
		shutdownProviderPolicyManager(t, manager, stopPolicy)
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
	stopPolicy = startProviderPolicyRuntime(t, manager)
	hideReceipts.Store(true)
	dropACK.Store(true)
	await(start())
	awaitProviderPolicyCondition(t, "actual Run did not expose the lost-ACK pending pause", func() bool {
		return operations.Load() == 1
	})
	state = api("GET", path, nil, 200)
	if operations.Load() != 1 || state["ownPaused"] != true || state["applied"] == true || state["state"] != "pending" {
		t.Fatalf("lost ACK was optimistic or lost actual pause: %+v calls=%d", state, operations.Load())
	}
	// A GET must neither replay the immutable operation nor mint a new one.
	api("GET", path, nil, 200)
	if operations.Load() != 1 {
		t.Fatal("status GET performed a Core operation")
	}
	shutdownProviderPolicyManager(t, manager, stopPolicy)
	stopPolicy = nil
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
	stopPolicy = startProviderPolicyRuntime(t, manager)
	awaitProviderPolicyCondition(t, "restarted runtime did not confirm the original immutable operation", func() bool {
		r, err := db.ProviderPolicy(context.Background(), state["subjectId"].(string))
		if err != nil {
			t.Fatal(err)
		}
		var document struct{ Pending json.RawMessage }
		if err := json.Unmarshal([]byte(r.Document), &document); err != nil {
			t.Fatal(err)
		}
		return len(document.Pending) == 0
	})
	state = api("GET", path, nil, 200)
	if state["ownPaused"] != true || state["applied"] != true || operations.Load() != 1 {
		t.Fatal("restart failed to confirm original operation without duplicate")
	}
	// Missing monetary data must retain the low reading but not renew or clear it.
	response.Store("NULL")
	missing := start()
	await(missing)
	awaitProviderPolicyEvent(t, db, missing)
	state = api("GET", path, nil, 200)
	if state["ownPaused"] != true || operations.Load() != 1 {
		t.Fatal("NULL response became a recovery or zero")
	}
	if contrary != "" {
		response.Store("1.00")
		if strings.HasSuffix(contrary, "during_prepare") {
			// Bind this barrier to ONE explicit real recovery pass. No live
			// worker may cancel/retry and turn an expired hold into a false PASS.
			stopPolicy()
			stopPolicy = nil
			inventory := manager.AppContext().ResourceCapabilityService.Inventory(context.Background(), true)
			var sourceBinding ob.Binding
			raw, _ := json.Marshal(query)
			if err := json.Unmarshal(raw, &sourceBinding); err != nil {
				t.Fatal(err)
			}
			armPreparation.Store(true)
			await(start())
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			done := make(chan error, 1)
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				done <- manager.AppContext().ProviderPolicyService.Reconcile(ctx)
			}()
			defer func() { cancel(); <-finished }() // Join before SQLite cleanup, also on failure.
			var heldContext context.Context
			select {
			case heldContext = <-preparationHeld:
			case err := <-done:
				t.Fatalf("single real preparation ended before its controlled boundary: %v", err)
			case <-time.After(20 * time.Second):
				t.Fatal("real pending-clear dispatch preparation did not reach its controlled ledger read")
			}
			contraryAttempt := ""
			switch contrary {
			case "source_replaced_during_prepare":
				query["version"] = 1
				query["secret"] = "MONEY-NONMATCHING-QUERY-REPLACEMENT"
				api("PUT", balancePath, query, 200)
			case "failure_during_prepare":
				response.Store("NULL")
				contraryAttempt = start()
				await(contraryAttempt)
			default:
				response.Store("-0.05")
				contraryAttempt = start()
				await(contraryAttempt)
			}
			// Actual contrary observation/config is durable before resuming.
			current, err := manager.AppContext().OfficialBalanceService.InventoryStatus(context.Background(), sourceBinding, inventory)
			if err != nil {
				t.Fatal(err)
			}
			if contrary == "source_replaced_during_prepare" {
				if current.Version != 2 || current.LastSuccess != nil {
					t.Fatal("replacement source/version was not durable at the boundary")
				}
			} else if current.Attempt == nil || current.Attempt.QueryID != contraryAttempt || contrary == "failure_during_prepare" && current.Attempt.QueryStatus == "success" || contrary != "failure_during_prepare" && (current.LastSuccess == nil || current.LastSuccess.QueryID != contraryAttempt || current.LastSuccess.QueryStatus != "success") {
				t.Fatal("exact contrary source attempt/status was not durable at the boundary")
			}
			if ctx.Err() != nil || heldContext.Err() != nil {
				t.Fatal("preparation expired before contrary publication/release; not guard acceptance")
			}
			releasePreparation <- struct{}{}
			if !<-preparationResumed {
				t.Fatal("controlled exchange resumed through cancellation, not a live preparation")
			}
			select {
			case err := <-done:
				if ctx.Err() != nil {
					t.Fatal("preparation pass expired instead of validating the contrary source")
				}
				if operations.Load() == 1 && !errors.Is(err, pp.ErrUnavailable) {
					t.Fatalf("pass ended without an explicit policy evidence refusal: %v", err)
				}
			case <-ctx.Done():
				t.Fatal("single preparation pass did not finish within its real lifecycle budget")
			}
			state = api("GET", path, nil, 200)
			if operations.Load() != 1 || state["ownPaused"] != true {
				t.Fatalf("dispatch used money superseded during preparation: calls=%d state=%+v", operations.Load(), state)
			}
			return
		}
		rejectBeforeForward.Store(true)
		await(start())
		// A pending journal is not proof that a POST was attempted. Establish
		// the actual non-forwarding refusal, then cancel/join the worker so no
		// first attempt remains in flight while the retry precondition changes.
		select {
		case <-rejected:
		case <-time.After(20 * time.Second):
			t.Fatal("actual runtime never reached the controlled before-forward refusal")
		}
		stopPolicy()
		stopPolicy = nil
		state = api("GET", path, nil, 200)
		if state["state"] != "pending" || state["ownPaused"] != true {
			t.Fatal("refused clear lost its durable pending journal/actual pause")
		}
		if operations.Load() != 1 {
			t.Fatal("controlled rejected clear reached Core")
		}
		if contrary == "contrary_success" {
			response.Store("-0.05")
			await(start())
		} else {
			query["version"] = 1
			query["secret"] = "MONEY-NONMATCHING-QUERY-REPLACEMENT"
			api("PUT", balancePath, query, 200)
		}
		// Keep the refusal until contrary evidence/source replacement is durable.
		// Deliberately invoke the real recovery entry point with no concurrent
		// worker: this is the stale retry under test, not a rescue of live Run.
		rejectBeforeForward.Store(false)
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
	awaitProviderPolicyEvent(t, db, job)
	state = api("GET", path, nil, 200)
	if state["enabled"] != false || state["ownPaused"] != true || operations.Load() != 1 {
		t.Fatal("late ON completion resumed after OFF")
	}
	update(true)
	// Exercise stale-generation recovery without racing an already running
	// recovery entry point for the same mutex/lease and budget.
	stopPolicy()
	stopPolicy = nil
	reconcile()
	state = api("GET", path, nil, 200)
	if state["ownPaused"] != true || operations.Load() != 1 {
		t.Fatal("re-enabling consumed an old generation's success")
	}
	stopPolicy = startProviderPolicyRuntime(t, manager)
	fresh := start()
	await(fresh)
	awaitProviderPolicyEvent(t, db, fresh)
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
