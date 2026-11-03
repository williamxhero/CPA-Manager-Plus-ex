package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	fr "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fullrefresh"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	pg "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/providergate"
	pp "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/providerpolicy"
	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

const policyUnsentOperationsPath = "/v8/management/inference-gates/operations"

// This owns an opt-in ordinary Core process and its temporary durable directory.
// Pinning deliberately matches moneyPolicyCore; no test-mode/fabricated Core ACK.
type policyUnsentCore struct {
	t                          *testing.T
	base, binary, path, dir    string
	logs, deny, digest, commit string
	stop                       func()
	starts                     int
	denyConnects               atomic.Int64
	denyDeepSeekConnects       atomic.Int64
}

func newPolicyUnsentCore(t *testing.T) *policyUnsentCore {
	t.Helper()
	binary := os.Getenv("PROVIDER82_CORE_BINARY")
	if binary == "" {
		t.Skip("requires pinned ordinary isolated Core; not an executed acceptance without PROVIDER82_CORE_BINARY")
	}
	raw, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	if digest != os.Getenv("PROVIDER82_CORE_SHA256") {
		t.Fatal("Core artifact SHA256 mismatch")
	}
	info, err := buildinfo.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	settings := map[string]string{}
	for _, s := range info.Settings {
		settings[s.Key] = s.Value
	}
	commit := os.Getenv("PROVIDER82_CORE_REVISION")
	if commit == "" || info.Path != "github.com/router-for-me/CLIProxyAPI/v8/cmd/server" || settings["vcs.revision"] != commit || settings["vcs.modified"] != "false" || settings["-tags"] != "" || settings["-buildmode"] != "exe" {
		t.Fatal("ordinary clean-source Core module/revision/build metadata required")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "auth"), 0700); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.yaml")
	// The official/runtime origin remains DeepSeek. Inference can only reach the
	// deny proxy below; official balance DNS/TLS/dialing is independently pinned
	// to policyMoneyNetwork. Neither controlled network forwards to the Internet.
	document := fmt.Sprintf("config-version: 8\nserver: {host: 127.0.0.1, port: %d}\nmanagement: {secret-key: MONEY-CORE-MANAGEMENT, disable-control-panel: true, disable-auto-update-panel: true}\noauth: {auth-dir: %q}\nresource-catalog: {enabled: true}\naccess: {api-keys: [MONEY-CLIENT]}\nplugins: {enabled: false}\napi-keys:\n  openai-compatibility:\n    - name: money-provider\n      base-url: https://api.deepseek.com/v1\n      models: [{name: controlled-model}]\n      keys: [{api-key: MONEY-SYNTHETIC-INFERENCE}]\n", port, filepath.ToSlash(filepath.Join(dir, "auth")))
	if err := os.WriteFile(path, []byte(document), 0600); err != nil {
		t.Fatal(err)
	}
	c := &policyUnsentCore{t: t, base: fmt.Sprintf("http://127.0.0.1:%d", port), binary: binary, path: path, dir: dir, digest: digest, commit: commit}
	deny := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect {
			c.denyConnects.Add(1)
			// CONNECT exposes an authority, NOT the tunneled HTTP path. This
			// fixture's only inference origin is DeepSeek; count it separately
			// from background model/update hosts. NEVER establish a tunnel.
			host, port, err := net.SplitHostPort(r.Host)
			if err == nil && strings.EqualFold(host, "api.deepseek.com") && port == "443" {
				c.denyDeepSeekConnects.Add(1)
			}
		}
		t.Logf("controlled fixture refused proxy request (no forwarding): %s authority=%s path=%s", r.Method, r.Host, r.URL.Path)
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(deny.Close)
	logs := t.TempDir()
	if root := os.Getenv("PROVIDER82_PROCESS_LOG_DIR"); root != "" {
		if err := os.MkdirAll(root, 0700); err != nil {
			t.Fatal(err)
		}
		logs, err = os.MkdirTemp(root, "provider-policy-unsent-")
		if err != nil {
			t.Fatal(err)
		}
	}
	c.logs, c.deny = logs, deny.URL
	t.Cleanup(func() {
		if c.stop != nil {
			c.stop()
		}
	})
	c.start()
	return c
}

func (c *policyUnsentCore) start() {
	c.t.Helper()
	c.starts++
	c.stop = startProviderGateCore(c.t, c.binary, c.path, c.dir, c.logs, fmt.Sprintf("policy-unsent-%d", c.starts), c.deny, c.digest, c.commit)
	// startProviderGateCore only starts the process. A successful authenticated
	// resources response, not a fixed sleep, is the readiness boundary.
	client := &http.Client{Timeout: time.Second}
	for until := time.Now().Add(20 * time.Second); time.Now().Before(until); {
		req, err := http.NewRequest(http.MethodGet, c.base+"/v8/management/resources", nil)
		if err != nil {
			c.t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer MONEY-CORE-MANAGEMENT")
		response, err := client.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				c.t.Logf("ordinary Core ready: start=%d logs=%s", c.starts, c.logs)
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	c.t.Fatalf("owned ordinary Core not ready; raw process logs: %s", c.logs)
}

func (c *policyUnsentCore) restart() {
	c.t.Helper()
	c.stop() // Waits for the owned process before reusing the SAME config/data/port.
	c.start()
}

type policyUnsentWire struct {
	rejectOperations  atomic.Bool
	rejectEligibility atomic.Bool
	mu                sync.Mutex
	attempted         [][]byte
	forwarded         [][]byte
	accepted          []string
}

func (w *policyUnsentWire) snapshot() (attempted, forwarded [][]byte, accepted []string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([][]byte(nil), w.attempted...), append([][]byte(nil), w.forwarded...), append([]string(nil), w.accepted...)
}

type policyUnsentFixture struct {
	t         *testing.T
	core      *policyUnsentCore
	wire      *policyUnsentWire
	bridge    *httptest.Server
	cfg       config.Config
	protector *security.Protector
	db        *store.Store
	manager   *Server
	network   balance.NetworkOptions
	amount    atomic.Value
	queries   atomic.Int64
	completed chan string
	state     pp.Status
	version   int64
}

func newPolicyUnsentFixture(t *testing.T) *policyUnsentFixture {
	t.Helper()
	f := &policyUnsentFixture{t: t, core: newPolicyUnsentCore(t), wire: &policyUnsentWire{}, cfg: testutil.NewConfig(t)}
	target, err := url.Parse(f.core.base)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext, ResponseHeaderTimeout: 5 * time.Second}
	t.Cleanup(proxy.Transport.(*http.Transport).CloseIdleConnections)
	proxy.ModifyResponse = func(r *http.Response) error {
		if r.Request.Method != http.MethodPost || r.Request.URL.Path != policyUnsentOperationsPath || r.StatusCode != http.StatusOK {
			return nil
		}
		raw, err := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if err != nil {
			return err
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		var receipt struct {
			OpID    string `json:"opId"`
			Durable bool   `json:"durable"`
			Applied bool   `json:"applied"`
		}
		if err := json.Unmarshal(raw, &receipt); err != nil {
			return err
		}
		if receipt.OpID == "" || !receipt.Durable || !receipt.Applied {
			t.Error("actual Core 200 lacked a durable applied receipt")
		}
		f.wire.mu.Lock()
		f.wire.accepted = append(f.wire.accepted, receipt.OpID)
		f.wire.mu.Unlock()
		return nil
	}
	f.bridge = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v8/management/inference-gates/providers/eligibility" && f.wire.rejectEligibility.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == policyUnsentOperationsPath {
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(raw))
			blocked := f.wire.rejectOperations.Load()
			f.wire.mu.Lock()
			f.wire.attempted = append(f.wire.attempted, raw)
			if !blocked {
				f.wire.forwarded = append(f.wire.forwarded, raw)
			}
			f.wire.mu.Unlock()
			if blocked {
				// This branch NEVER invokes the reverse proxy: not accepted-lost-ACK.
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(f.bridge.Close)
	f.amount.Store("0.05")
	f.network = policyMoneyNetwork(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/user/balance" || r.Header.Get("Authorization") != "Bearer MONEY-SYNTHETIC-INFERENCE" {
			t.Error("unexpected controlled official balance request")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.queries.Add(1)
		amount := f.amount.Load().(string)
		fmt.Fprintf(w, `{"is_available":true,"balance_infos":[{"currency":"USD","total_balance":%q,"granted_balance":"0","topped_up_balance":%q}]}`, amount, amount)
	}))
	f.protector, err = security.NewProtector([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	f.db, err = store.Open(f.cfg.DBPath, f.protector)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.closeManager)
	testutil.EnsureAdminCredential(t, f.db)
	if err := f.db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: f.bridge.URL, ManagementKey: "MONEY-CORE-MANAGEMENT"}}); err != nil {
		t.Fatal(err)
	}
	f.openManager()
	f.readIssuedBinding()
	f.configureOfficial()
	f.update(true)
	return f
}

func (f *policyUnsentFixture) openManager() {
	f.t.Helper()
	if f.db == nil {
		var err error
		f.db, err = store.Open(f.cfg.DBPath, f.protector)
		if err != nil {
			f.t.Fatal(err)
		}
	}
	f.manager = New(f.cfg, f.db, collector.NewManager(f.cfg, f.db))
	f.manager.AppContext().OfficialBalanceService.Querier = balance.NewWithNetwork(f.network)
	f.completed = make(chan string, 16)
	completed := f.completed
	f.manager.AppContext().FullRefreshService.Subscribe(func(_ context.Context, event fr.CompletedObservation) {
		completed <- event.AttemptID // Subscribed LAST: policy completion already returned.
	})
}

func (f *policyUnsentFixture) closeManager() {
	if f.manager != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		if err := f.manager.AppContext().FullRefreshService.Shutdown(ctx); err != nil {
			f.t.Error(err)
		}
		cancel()
		f.manager = nil
	}
	if f.db != nil {
		if err := f.db.Close(); err != nil {
			f.t.Error(err)
		}
		f.db = nil
	}
}

func (f *policyUnsentFixture) api(method, path string, body any, want int, out any) {
	f.t.Helper()
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			f.t.Fatal(err)
		}
	}
	response := testutil.Request(f.t, f.manager.Handler(), method, path, string(raw), testutil.AdminKey)
	testutil.RequireStatus(f.t, response, want)
	for _, secret := range []string{"MONEY-SYNTHETIC-INFERENCE", "MONEY-CORE-MANAGEMENT"} {
		if bytes.Contains(response.Body.Bytes(), []byte(secret)) {
			f.t.Fatal("public API leaked controlled credentials")
		}
	}
	if out != nil {
		testutil.DecodeJSON(f.t, response, out)
	}
}

func (f *policyUnsentFixture) status() pp.Status {
	f.t.Helper()
	var state pp.Status
	f.api(http.MethodGet, "/usage-service/provider-policy?providerId="+url.QueryEscape(f.state.ProviderID), nil, http.StatusOK, &state)
	f.state = state
	return state
}

func (f *policyUnsentFixture) readIssuedBinding() {
	f.t.Helper()
	var response struct{ Negotiated rc.Negotiated }
	f.api(http.MethodGet, "/usage-service/resource-capabilities?inventory=1&bindings=1", nil, http.StatusOK, &response)
	n := response.Negotiated
	if n.Snapshot == nil || !n.Snapshot.Complete || n.Identity.State != "available" || n.BindingCapability == nil || n.BindingCapability.State != "available" {
		f.t.Fatal("management API did not return a complete actual issued inventory")
	}
	providers := []rc.Row{}
	for _, row := range n.Snapshot.Resources {
		if row.Kind == "provider" {
			providers = append(providers, row)
		}
	}
	if len(providers) != 1 || len(providers[0].Members) != 1 {
		f.t.Fatal("fixture requires exactly one actually issued one-member provider, not label matching")
	}
	parent := providers[0]
	if f.state.ProviderID != "" && parent.ResourceID != f.state.ProviderID {
		f.t.Fatal("durable provider identity changed across fixture restart")
	}
	f.state.ProviderID = parent.ResourceID
	state := f.status()
	if state.Scope != n.Snapshot.Scope || len(state.Targets) != 2 || state.Targets[0] != parent.Target {
		f.t.Fatal("public policy binding did not match actual issued parent scope/closure")
	}
	memberFound := false
	for _, row := range n.Snapshot.Resources {
		if row.ResourceID == parent.Members[0] && row.ParentID == parent.ResourceID && row.Target == state.Targets[1] {
			memberFound = true
		}
	}
	if !memberFound {
		f.t.Fatal("public policy binding lacks the actual issued member")
	}
}

func (f *policyUnsentFixture) configureOfficial() ob.Status {
	f.t.Helper()
	var result ob.Status
	f.api(http.MethodPut, balancePath, ob.Update{Config: ob.Config{Binding: ob.Binding{Scope: f.state.Scope, Targets: f.state.Targets}, Version: f.version, Adapter: "deepseek", Endpoint: "https://api.deepseek.com/user/balance", PrivateTargets: []string{"127.0.0.1"}}, Secret: "MONEY-SYNTHETIC-INFERENCE"}, http.StatusOK, &result)
	if result.Version != f.version+1 || !result.Configured {
		f.t.Fatal("official query configuration did not advance by public API CAS")
	}
	f.version = result.Version
	return result
}

func (f *policyUnsentFixture) update(enabled bool) pp.Status {
	f.t.Helper()
	before := f.status()
	var result pp.Status
	f.api(http.MethodPut, "/usage-service/provider-policy", pp.Update{Binding: before.Binding, ExpectedGeneration: &before.Generation, Enabled: &enabled}, http.StatusOK, &result)
	if result.Enabled != enabled || result.Generation != before.Generation+1 || result.Owner != before.Owner || result.SubjectID != before.SubjectID {
		f.t.Fatal("public intent write changed identity or failed to advance generation")
	}
	f.state = result
	return result
}

func (f *policyUnsentFixture) query(amount string) ob.Job {
	f.t.Helper()
	f.amount.Store(amount)
	var job ob.Job
	f.api(http.MethodPost, balancePath, ob.Query{Binding: ob.Binding{Scope: f.state.Scope, Targets: f.state.Targets}, Version: f.version}, http.StatusAccepted, &job)
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case id := <-f.completed:
			if id != job.ID {
				f.t.Fatalf("unexpected completion %s while awaiting newly requested query %s", id, job.ID)
			}
			var completed ob.Job
			f.api(http.MethodGet, balancePath+"?job="+url.QueryEscape(job.ID), nil, http.StatusOK, &completed)
			if completed.State != "complete" || completed.Version != f.version || completed.Status == nil || completed.Status.Attempt == nil || completed.Status.LastSuccess == nil || completed.Status.Attempt.QueryID != job.ID || completed.Status.Attempt.QueryStatus != "success" || completed.Status.LastSuccess.QueryID != job.ID || completed.Status.LastSuccess.ObservedAtMS <= 0 {
				f.t.Fatalf("actual controlled official query did not publish a new durable success: %+v", completed)
			}
			return completed
		case <-deadline.C:
			f.t.Fatal("controlled actual query completion subscribers did not finish")
			return ob.Job{}
		}
	}
}

func (f *policyUnsentFixture) monetaryGate() pg.Status {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	app := f.manager.AppContext()
	// Monetary gate status has no public administrative HTTP selector. This is
	// the exported read-only adapter used by production policy reconciliation.
	status, err := pg.NewMonetary(f.db, app.ManagerConfigService, app.ResourceCapabilityService).Status(ctx, pg.Binding(f.state.Binding))
	if err != nil {
		f.t.Fatal(err)
	}
	return status
}

func (f *policyUnsentFixture) reconcile() {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := f.manager.AppContext().ProviderPolicyService.Reconcile(ctx); err != nil {
		f.t.Fatalf("real policy reconciliation failed: %v", err)
	}
}

func (f *policyUnsentFixture) coreRequest(method, path string, raw []byte, want int, out any) {
	f.t.Helper()
	req, err := http.NewRequest(method, f.core.base+path, bytes.NewReader(raw))
	if err != nil {
		f.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer MONEY-CORE-MANAGEMENT")
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("fixture redirects refused") }}
	response, err := client.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		f.t.Fatal(err)
	}
	if response.StatusCode != want {
		f.t.Fatalf("actual Core %s %s returned %d, want %d: %s", method, path, response.StatusCode, want, body)
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			f.t.Fatal(err)
		}
	}
}

func (f *policyUnsentFixture) revision() uint64 {
	f.t.Helper()
	var ledger struct {
		GateRevision uint64   `json:"gateRevision"`
		Ready        bool     `json:"ready"`
		Scope        rc.Scope `json:"scope"`
	}
	f.coreRequest(http.MethodGet, "/v8/management/inference-gates", nil, http.StatusOK, &ledger)
	if !ledger.Ready || ledger.Scope != f.state.Scope {
		f.t.Fatal("actual current Core gate ledger scope/readiness unavailable")
	}
	return ledger.GateRevision
}

func (f *policyUnsentFixture) assertNoPending() {
	f.t.Helper()
	record, err := f.db.ProviderPolicy(context.Background(), f.state.SubjectID)
	if err != nil {
		f.t.Fatal(err)
	}
	var document struct{ Pending json.RawMessage }
	if err := json.Unmarshal([]byte(record.Document), &document); err != nil {
		f.t.Fatal(err)
	}
	if len(document.Pending) != 0 && string(document.Pending) != "null" {
		f.t.Fatalf("durable obsolete policy Pending was not retired: %s", document.Pending)
	}
	if f.status().State == "pending" {
		f.t.Fatal("public policy remained permanently pending after obsolete decision retirement")
	}
}

func (f *policyUnsentFixture) assertFreshPauseAndRecovery(oldAttempt string, before uint64) {
	f.t.Helper()
	started := time.Now()
	queriesBefore := f.queries.Load()
	state := f.state
	var lastErr error
	var revision uint64
	attempts, unavailable := 0, 0
	fail := func(phase, reason string) {
		f.t.Helper()
		_, forwarded, accepted := f.wire.snapshot()
		f.t.Fatalf("fresh lifecycle phase=%s reason=%s elapsed=%s attempts=%d ErrUnavailableCount=%d lastReconcileErr=%v lastActualStatus=%+v actualCoreRevision=%d forwarded=%d actualReceipts=%v actualQueries=%d initialQueries=%d", phase, reason, time.Since(started), attempts, unavailable, lastErr, state, revision, len(forwarded), accepted, f.queries.Load(), queriesBefore)
	}
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: time.Second}).DialContext}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error {
		return fmt.Errorf("lifecycle evidence redirects refused")
	}}
	awaitApplied := func(phase string, wantPaused bool, wantRevision uint64) {
		f.t.Helper()
		// Each pause/clear phase owns a fresh 20s budget, shared by its real
		// Reconcile AND public status/Core ledger reads, never by source queries.
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		phaseStarted := time.Now()
		for {
			if err := ctx.Err(); err != nil {
				fail(phase, "phase 20s deadline: "+err.Error())
			}
			attempts++
			// Real exported lifecycle work; never replay the source query or repair DB state.
			lastErr = f.manager.AppContext().ProviderPolicyService.Reconcile(ctx)
			if lastErr != nil {
				f.t.Logf("fresh lifecycle phase=%s attempt=%d elapsed=%s reconcileErrorType=%T reconcileError=%v", phase, attempts, time.Since(started), lastErr, lastErr)
				if !errors.Is(lastErr, pp.ErrUnavailable) {
					fail(phase, "non-retryable reconciliation error")
				}
				unavailable++
			}
			// Actual public Manager GET, carrying this phase's deadline rather
			// than f.status()'s independently scoped request context.
			req := httptest.NewRequest(http.MethodGet, "/usage-service/provider-policy?providerId="+url.QueryEscape(f.state.ProviderID), nil).WithContext(ctx)
			req.Header.Set("Authorization", "Bearer "+testutil.AdminKey)
			response := httptest.NewRecorder()
			f.manager.Handler().ServeHTTP(response, req)
			for _, secret := range []string{"MONEY-SYNTHETIC-INFERENCE", "MONEY-CORE-MANAGEMENT"} {
				if bytes.Contains(response.Body.Bytes(), []byte(secret)) {
					fail(phase, "public status leaked controlled credentials")
				}
			}
			if response.Code != http.StatusOK {
				fail(phase, fmt.Sprintf("public status HTTP %d: %s", response.Code, response.Body.String()))
			}
			if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
				fail(phase, "invalid public status: "+err.Error())
			}
			f.state = state
			// Actual Core ledger on the SAME deadline, not a simulated ACK.
			coreReq, err := http.NewRequestWithContext(ctx, http.MethodGet, f.core.base+"/v8/management/inference-gates", nil)
			if err != nil {
				fail(phase, err.Error())
			}
			coreReq.Header.Set("Authorization", "Bearer MONEY-CORE-MANAGEMENT")
			coreResponse, err := client.Do(coreReq)
			if err != nil {
				fail(phase, "actual Core ledger read: "+err.Error())
			}
			raw, err := io.ReadAll(coreResponse.Body)
			_ = coreResponse.Body.Close()
			var ledger struct {
				GateRevision uint64   `json:"gateRevision"`
				Ready        bool     `json:"ready"`
				Scope        rc.Scope `json:"scope"`
			}
			if err != nil || coreResponse.StatusCode != http.StatusOK || json.Unmarshal(raw, &ledger) != nil || !ledger.Ready || ledger.Scope != f.state.Scope {
				fail(phase, fmt.Sprintf("actual Core ledger unavailable/invalid: status=%d readErr=%v body=%s", coreResponse.StatusCode, err, raw))
			}
			revision = ledger.GateRevision
			f.t.Logf("fresh lifecycle phase=%s attempt=%d phaseElapsed=%s totalElapsed=%s reconcileErr=%v state=%s reason=%q enabled=%t ownPaused=%t applied=%t actualCoreRevision=%d wantRevision=%d actualQueries=%d", phase, attempts, time.Since(phaseStarted), time.Since(started), lastErr, state.State, state.Reason, state.Enabled, state.OwnPaused, state.Applied, revision, wantRevision, f.queries.Load())
			if err := ctx.Err(); err != nil {
				fail(phase, "phase 20s deadline after evidence read: "+err.Error())
			}
			if revision > wantRevision {
				fail(phase, "extra actual Core mutation; retry cannot erase duplicate operations")
			}
			wantState := "observing"
			if wantPaused {
				wantState = "paused"
			}
			// ErrUnavailable is only a transient diagnostic, never success.
			if lastErr == nil && state.Enabled && state.OwnPaused == wantPaused && state.Applied && state.State == wantState && revision == wantRevision {
				return
			}
			timer := time.NewTimer(100 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				fail(phase, "phase 20s deadline awaiting applied state: "+ctx.Err().Error())
			case <-timer.C:
			}
		}
	}
	// Source queries are OUTSIDE the two polling budgets. Each f.query keeps
	// its original independent 15s completion wait and API/transport limits;
	// post-phase audit helpers likewise retain their original independent reads.
	low := f.query("0.05")
	if low.ID == oldAttempt {
		f.t.Fatal("fresh query reused superseded attempt")
	}
	awaitApplied("fresh-low-pause", true, before+1)
	f.assertNoPending()
	state = f.status()
	if !state.Enabled || !state.OwnPaused || !state.Applied || state.State != "paused" || f.revision() != before+1 {
		f.t.Fatalf("new successful query did not produce exactly one actual monetary pause: %+v", state)
	}
	high := f.query("1.00")
	if high.ID == low.ID || high.ID == oldAttempt {
		f.t.Fatal("fresh recovery reused prior attempt")
	}
	awaitApplied("fresh-high-clear", false, before+2)
	f.assertNoPending()
	state = f.status()
	if !state.Enabled || state.OwnPaused || !state.Applied || state.State != "observing" || f.revision() != before+2 {
		f.t.Fatalf("new successful high query did not clear exactly once: %+v", state)
	}
	_, forwarded, accepted := f.wire.snapshot()
	if len(forwarded) != 2 || len(accepted) != 2 || accepted[0] == accepted[1] {
		f.t.Fatalf("fresh lifecycle requires two distinct actual Core receipts, forwarded=%d accepted=%v", len(forwarded), accepted)
	}
	for _, raw := range forwarded {
		var operation struct {
			Scope rc.Scope `json:"scope"`
		}
		if err := json.Unmarshal(raw, &operation); err != nil || !reflect.DeepEqual(operation.Scope, f.state.Scope) {
			f.t.Fatal("fresh monetary command did not use the current issued boot/scope")
		}
	}
	if strings.Contains(fmt.Sprint(state), "MONEY-SYNTHETIC-INFERENCE") {
		f.t.Fatal("policy receipt leaked query credentials")
	}
	if f.queries.Load() != queriesBefore+2 {
		f.t.Fatalf("lifecycle retries repeated/skipped a query: initial=%d actual=%d", queriesBefore, f.queries.Load())
	}
	f.t.Logf("fresh lifecycle FINAL lowAttempt=%s highAttempt=%s actualStatus=%+v actualCoreRevision=%d actualReceipts=%v actualQueries=%d attempts=%d ErrUnavailableCount=%d elapsed=%s; bounded recovery evidence, not proof of the earlier parallel failure's root cause", low.ID, high.ID, state, revision, accepted, f.queries.Load(), attempts, unavailable, time.Since(started))
}
