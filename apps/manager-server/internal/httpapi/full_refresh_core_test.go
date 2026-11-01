package httpapi

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	fr "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fullrefresh"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type refreshFixture struct {
	t                         *testing.T
	manager                   *Server
	db                        *store.Store
	api                       func(string, string, any) (int, []byte)
	calls, peak, active, mode atomic.Int64
	nativeCalls               atomic.Int64
	entered                   chan struct{}
	release                   chan struct{}
	upstream                  *httptest.Server
}

func verifiedRefreshArtifact(t *testing.T, path, sha, revision string) {
	t.Helper()
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(raw)
	if len(sha) != 64 || hex.EncodeToString(sum[:]) != sha {
		t.Fatal("independent artifact hash missing/mismatch")
	}
	info, e := buildinfo.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	rev, modified := "", ""
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			rev = s.Value
		}
		if s.Key == "vcs.modified" {
			modified = s.Value
		}
	}
	if len(revision) != 40 || rev != revision || modified != "false" {
		t.Fatal("unverified actual source metadata")
	}
	t.Logf("actual artifact source=%s sha256=%s modified=false", rev, sha)
}
func newRefreshFixture(t *testing.T) *refreshFixture {
	t.Helper()
	binary := os.Getenv("CORE_QUOTA_TEST_BINARY")
	if binary == "" {
		t.Skip("immutable actual Core artifact manifest required")
	}
	verifiedRefreshArtifact(t, binary, os.Getenv("CORE_QUOTA_EXPECTED_SHA256"), os.Getenv("CORE_QUOTA_EXPECTED_REVISION"))
	plugin := os.Getenv("CORE_QUOTA_PLUGIN_DIR")
	verifiedRefreshArtifact(t, filepath.Join(plugin, "opencode-go-cliproxyapi.dll"), os.Getenv("CORE_QUOTA_PLUGIN_EXPECTED_SHA256"), os.Getenv("CORE_QUOTA_PLUGIN_EXPECTED_REVISION"))
	if os.Getenv("CORE_QUOTA_PLUGIN_SDK_REVISION") != os.Getenv("CORE_QUOTA_EXPECTED_REVISION") {
		t.Fatal("plugin SDK provenance mismatch")
	}
	f := &refreshFixture{t: t, entered: make(chan struct{}, 64), release: make(chan struct{})}
	t.Cleanup(func() {
		select {
		case <-f.release:
		default:
			close(f.release)
		}
	})
	f.upstream = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		if r.URL.Path != "/user/balance" {
			f.nativeCalls.Add(1)
		}
		a := f.active.Add(1)
		defer f.active.Add(-1)
		for {
			p := f.peak.Load()
			if a <= p || f.peak.CompareAndSwap(p, a) {
				break
			}
		}
		select {
		case f.entered <- struct{}{}:
		default:
		}
		if f.mode.Load() == 1 || f.mode.Load() == 4 && r.URL.Path == "/user/balance" {
			select {
			case <-f.release:
			case <-r.Context().Done():
				return
			}
		}
		if f.mode.Load() == 2 {
			w.WriteHeader(401)
			fmt.Fprint(w, "SECRET-CANARY")
			return
		}
		if f.mode.Load() == 3 {
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(429)
			return
		}
		switch r.URL.Path {
		case "/backend-api/wham/usage":
			if r.Header.Get("Authorization") != "Bearer QUOTA-SECRET-CANARY" {
				t.Error("wrong selected OAuth credential")
			}
			reset := time.Now().Add(time.Hour).Unix()
			fmt.Fprintf(w, `{"rate_limit":{"allowed":true,"limit_reached":false,"primary_window":{"used_percent":25,"reset_at":%d,"limit_window_seconds":18000},"secondary_window":{"used_percent":50,"reset_at":%d,"limit_window_seconds":604800}}}`, reset, reset)
		case "/zen/go/v1/usage":
			if r.Header.Get("Authorization") != "Bearer PLUGIN-SECRET-CANARY" {
				t.Error("wrong selected compiled plugin key")
			}
			reset := time.Now().Add(time.Hour).Format(time.RFC3339)
			fmt.Fprintf(w, `{"usage":{"rolling":{"status":"allowed","percent":20,"resetsAt":%q},"weekly":{"status":"allowed","percent":15,"resetsAt":%q},"monthly":{"status":"allowed","percent":10,"resetsAt":%q}}}`, reset, reset, reset)
		case "/user/balance":
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer explicit-billing-") {
				t.Error("query authorization was not configured explicit secret")
			}
			fmt.Fprint(w, `{"is_available":true,"balance_infos":[{"currency":"USD","total_balance":"12.000000001","granted_balance":"0","topped_up_balance":"12.000000001"},{"currency":"CNY","total_balance":"-2","granted_balance":"0","topped_up_balance":"-2"}]}`)
		default:
			t.Error("unexpected inference/unapproved upstream scan")
			w.WriteHeader(400)
		}
	}))
	seed := httptest.NewTLSServer(http.NotFoundHandler())
	cert := seed.TLS.Certificates[0]
	seed.Close()
	template, e := x509.ParseCertificate(cert.Certificate[0])
	if e != nil {
		t.Fatal(e)
	}
	template.DNSNames = []string{"chatgpt.com", "opencode.ai"}
	template.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
	key := cert.PrivateKey.(crypto.Signer)
	der, e := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if e != nil {
		t.Fatal(e)
	}
	cert.Certificate = [][]byte{der}
	f.upstream.TLS = seed.TLS.Clone()
	f.upstream.TLS.Certificates[0] = cert
	f.upstream.StartTLS()
	t.Cleanup(func() {
		select {
		case <-f.release:
		default:
			close(f.release)
		}
		f.upstream.Close()
	})
	dir := t.TempDir()
	ca := filepath.Join(dir, "ca.pem")
	if e = os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); e != nil {
		t.Fatal(e)
	}
	state := filepath.Join(dir, "core-state")
	os.MkdirAll(state, 0700)
	// Actual producer fixture accepts a preexisting config, allowing more than four
	// independently disabled provider groups without changing compiled Core.
	config := fmt.Sprintf("server: {port: 0}\nmanagement: {secret-key: test-password}\noauth: {auth-dir: %q}\nresource-catalog: {enabled: true}\nplugins:\n  enabled: true\n  dir: %q\n  configs:\n    opencode-go-cliproxyapi:\n      enabled: true\n      base-url: https://opencode.ai/zen/go/v1\n      api-keys: [{name: test, value: PLUGIN-SECRET-CANARY}]\napi-keys:\n  openai-compatibility:\n", filepath.ToSlash(filepath.Join(state, "auth")), filepath.ToSlash(plugin))
	for i := 0; i < 6; i++ {
		config += fmt.Sprintf("    - name: same-label-%d\n      base-url: https://unqueried.invalid/v1\n      disabled: true\n      keys: [{api-key: INFERENCE-SECRET-%d, disabled: true}]\n", i, i)
	}
	if e = os.WriteFile(filepath.Join(state, "config.yaml"), []byte(config), 0600); e != nil {
		t.Fatal(e)
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	address := listener.Addr().String()
	listener.Close()
	cmd := exec.Command(binary, "-test.run=^TestCredentialQuotaProcessFixture$", "-test.timeout=120s")
	cmd.Dir = dir
	for _, k := range []string{"PATH", "SystemRoot", "WINDIR", "TEMP", "TMP"} {
		if v := os.Getenv(k); v != "" {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	cmd.Env = append(cmd.Env, "HOME="+dir, "USERPROFILE="+dir, "QUOTA_FIXTURE_LISTEN="+address, "QUOTA_FIXTURE_STATE_DIR="+state, "QUOTA_FIXTURE_UPSTREAM="+f.upstream.Listener.Addr().String(), "QUOTA_FIXTURE_CA="+ca, "QUOTA_FIXTURE_PLUGIN_DIR="+plugin)
	var logs bytes.Buffer
	cmd.Stdout = &logs
	cmd.Stderr = &logs
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
		if t.Failed() {
			t.Log(logs.String())
		}
	})
	coreBase := "http://" + address
	client := &http.Client{Timeout: 15 * time.Second}
	until := time.Now().Add(20 * time.Second)
	for {
		req, _ := http.NewRequest("GET", coreBase+"/v8/management/resources", nil)
		req.Header.Set("Authorization", "Bearer test-password")
		r, e := client.Do(req)
		if e == nil {
			r.Body.Close()
			if r.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(until) {
			t.Fatal("actual Core not ready")
		}
		time.Sleep(25 * time.Millisecond)
	}
	cfg := testutil.NewConfig(t)
	protector, _ := security.NewProtector([]byte("0123456789abcdef0123456789abcdef"))
	db, e := store.Open(cfg.DBPath, protector)
	if e != nil {
		t.Fatal(e)
	}
	f.db = db
	t.Cleanup(func() { db.Close() })
	testutil.EnsureAdminCredential(t, db)
	if e = db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: coreBase, ManagementKey: "test-password"}}); e != nil {
		t.Fatal(e)
	}
	f.manager = New(cfg, db, collector.NewManager(cfg, db))
	roots := x509.NewCertPool()
	root, _ := x509.ParseCertificate(der)
	roots.AddCert(root)
	f.manager.AppContext().OfficialBalanceService.Querier = balance.NewWithNetwork(balance.NetworkOptions{RootCAs: roots})
	server := httptest.NewServer(f.manager.Handler())
	t.Cleanup(server.Close)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := f.manager.AppContext().FullRefreshService.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	f.api = func(method, path string, body any) (int, []byte) {
		t.Helper()
		data, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, server.URL+path, bytes.NewReader(data))
		req.Header.Set("Authorization", "Bearer "+testutil.AdminKey)
		r, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer r.Body.Close()
		out, e := io.ReadAll(r.Body)
		if e != nil {
			t.Fatal(e)
		}
		if bytes.Contains(out, []byte("SECRET")) || bytes.Contains(out, []byte("explicit-billing")) {
			t.Fatal("secret leaked")
		}
		return r.StatusCode, out
	}
	return f
}
func TestFullRefreshActualCoreInventory(t *testing.T) {
	f := newRefreshFixture(t)
	code, data := f.api("GET", "/usage-service/full-refresh", nil)
	if code != 200 {
		t.Fatalf("full inventory HTTP%d: %s", code, data)
	}
	var view struct {
		ResourceCount int `json:"resourceCount"`
		Sources       []struct {
			State, Reason string
			Queried       bool `json:"queried"`
		} `json:"sources"`
	}
	if json.Unmarshal(data, &view) != nil || view.ResourceCount < 16 || len(view.Sources) == 0 {
		t.Fatalf("complete actual catalog missing: %s", data)
	}
	if f.calls.Load() != 0 {
		t.Fatal("inventory GET queried provider")
	}
}

func (f *refreshFixture) configuredSources() fr.Inventory {
	f.t.Helper()
	code, data := f.api("GET", "/usage-service/full-refresh", nil)
	if code != 200 {
		f.t.Fatalf("inventory %d %s", code, data)
	}
	var inv fr.Inventory
	if json.Unmarshal(data, &inv) != nil {
		f.t.Fatal("invalid inventory")
	}
	configured := 0
	for _, src := range inv.Sources {
		if src.Request.Kind != fr.Official || !src.ManualDisabled {
			continue
		}
		q := src.Request.Official
		u := ob.Update{Config: ob.Config{Binding: q.Binding, Version: q.Version, Adapter: "deepseek", Endpoint: f.upstream.URL + "/user/balance", PrivateTargets: []string{"127.0.0.1"}}, Secret: fmt.Sprintf("explicit-billing-%d", configured)}
		code, data = f.api("PUT", balancePath, u)
		if code != 200 {
			f.t.Fatalf("configure %d %s", code, data)
		}
		configured++
	}
	if configured < 6 {
		f.t.Fatalf("did not configure all disabled provider sources: %d", configured)
	}
	code, data = f.api("GET", "/usage-service/full-refresh", nil)
	if code != 200 {
		f.t.Fatalf("configured inventory %d %s", code, data)
	}
	if json.Unmarshal(data, &inv) != nil {
		f.t.Fatal("invalid inventory")
	}
	return inv
}
func TestFullRefreshActualCoreGlobalBudgetIncludesManualBothHosts(t *testing.T) {
	f := newRefreshFixture(t)
	inv := f.configuredSources()
	f.mode.Store(1)
	official := 0
	var native *fr.SourceRequest
	for _, src := range inv.Sources {
		if src.Request.Kind == fr.Credential && src.State == "ready" {
			r := src.Request
			native = &r
		}
		if src.Request.Kind != fr.Official || src.Request.Official.Version == 0 || official == 4 {
			continue
		}
		code, data := f.api("POST", balancePath, src.Request.Official)
		if code != 202 {
			t.Fatalf("admit%d %s", code, data)
		}
		official++
	}
	if official != 4 || native == nil {
		t.Fatal("missing real source hosts")
	}
	code, data := f.api("POST", "/usage-service/credential-quota", native.Credential)
	if code != 429 {
		t.Fatalf("global4 admission must refuse fifth mixed query; got %d: %s", code, data)
	}
	if f.peak.Load() > 4 {
		t.Fatalf("global peak %d", f.peak.Load())
	}
	close(f.release)
}

func TestFullRefreshActualCoreRoundPersistsAndJoinsManual(t *testing.T) {
	f := newRefreshFixture(t)
	inv := f.configuredSources()
	f.mode.Store(1)
	code, data := f.api("POST", "/usage-service/full-refresh", map[string]any{"purpose": "manual"})
	if code != 202 {
		t.Fatalf("round %d %s", code, data)
	}
	var round fr.Progress
	if json.Unmarshal(data, &round) != nil || round.ID == "" {
		t.Fatalf("no durable round %s", data)
	}
	code, data = f.api("POST", "/usage-service/full-refresh", map[string]any{"purpose": "periodic"})
	var joined fr.Progress
	json.Unmarshal(data, &joined)
	if code != 202 || joined.ID != round.ID {
		t.Fatalf("whole-round singleflight lost: %d %s", code, data)
	}
	// A source joins even after its turn finishes while other sources remain.
	for _, src := range inv.Sources {
		if src.Request.Kind == fr.Official && src.Request.Official.Version > 0 {
			code, data = f.api("POST", balancePath, src.Request.Official)
			if code != 202 && code != 429 {
				t.Fatalf("manual join %d %s", code, data)
			}
			break
		}
	}
	if f.peak.Load() > 4 {
		t.Fatalf("mixed scheduler/manual peak %d", f.peak.Load())
	}
	close(f.release)
	done := f.waitRound(round.ID)
	if done.ResourceCount != inv.ResourceCount || done.Succeeded < 8 || done.Failed != 0 || done.Pending != 0 {
		t.Fatalf("incomplete round: %+v", done)
	}
	// Reading progress is durable and has no quota side effects.
	before := f.calls.Load()
	code, data = f.api("GET", "/usage-service/full-refresh?id="+round.ID, nil)
	if code != 200 || f.calls.Load() != before {
		t.Fatalf("progress side effects %d %s", code, data)
	}
	for _, item := range done.Items {
		if item.Outcome == "success" && (item.JobID == "" || item.LastSuccess == nil || item.LastSuccess.QueryID != item.JobID) {
			t.Fatal("success missing persisted host attempt")
		}
	}
}
func (f *refreshFixture) waitRound(id string) fr.Progress {
	f.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		code, data := f.api("GET", "/usage-service/full-refresh?id="+id, nil)
		var p fr.Progress
		if code != 200 || json.Unmarshal(data, &p) != nil {
			f.t.Fatalf("progress %d %s", code, data)
		}
		if p.State != "running" {
			return p
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("round stuck %s", data)
		}
		time.Sleep(30 * time.Millisecond)
	}
}

func TestFullRefreshActualCoreCompletedSourceJoinsUntilRoundEnds(t *testing.T) {
	f := newRefreshFixture(t)
	inv := f.configuredSources()
	f.mode.Store(4)
	code, data := f.api("POST", "/usage-service/full-refresh", map[string]any{"purpose": "manual"})
	if code != 202 {
		t.Fatalf("start %d %s", code, data)
	}
	var p fr.Progress
	json.Unmarshal(data, &p)
	until := time.Now().Add(10 * time.Second)
	var finished fr.Item
	for {
		_, data = f.api("GET", "/usage-service/full-refresh?id="+p.ID, nil)
		json.Unmarshal(data, &p)
		for _, item := range p.Items {
			if item.Request.Kind == fr.Credential && item.Outcome == "success" {
				finished = item
				break
			}
		}
		if finished.JobID != "" {
			break
		}
		if time.Now().After(until) {
			t.Fatal("native source did not complete ahead of slow official sources")
		}
		time.Sleep(30 * time.Millisecond)
	}
	before := f.nativeCalls.Load()
	var q any
	for _, src := range inv.Sources {
		if src.Request.Kind == fr.Credential && src.Request.Credential.AuthIndex == finished.Request.Credential.AuthIndex {
			q = src.Request.Credential
		}
	}
	code, data = f.api("POST", "/usage-service/credential-quota", q)
	var job struct{ ID string }
	json.Unmarshal(data, &job)
	if code != 202 || job.ID != finished.JobID || f.nativeCalls.Load() != before {
		t.Fatalf("completed source not joined for whole round %d %s", code, data)
	}
	close(f.release)
	f.waitRound(p.ID)
}

type refreshClock struct {
	mu    sync.Mutex
	now   time.Time
	ticks chan time.Time
}

func (c *refreshClock) Now() time.Time          { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *refreshClock) Ticks() <-chan time.Time { return c.ticks }
func (c *refreshClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now
	c.mu.Unlock()
	c.ticks <- now
}
func TestFullRefreshActualCoreHourlyWithoutBrowserOrUsage(t *testing.T) {
	f := newRefreshFixture(t)
	f.configuredSources()
	clock := &refreshClock{now: time.Now(), ticks: make(chan time.Time, 8)}
	app := f.manager.AppContext()
	app.FullRefreshService = fr.New(f.db, app.ResourceCapabilityService, app.OfficialBalanceService, app.CredentialQuotaService, fr.WithBudget(app.ManagementQueryBudget), fr.WithClock(clock))
	events := make(chan fr.CompletedObservation, 64)
	app.FullRefreshService.Subscribe(func(ctx context.Context, e fr.CompletedObservation) {
		select {
		case events <- e:
		case <-ctx.Done():
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- app.FullRefreshService.Run(ctx) }()
	first := f.waitLatestRound()
	if first.Purpose != fr.Periodic || first.Succeeded < 8 {
		t.Fatalf("no startup periodic full inventory: %+v", first)
	}
	count := f.calls.Load()
	clock.advance(59 * time.Minute)
	// Synchronize on persisted scheduler acknowledgement, not an arbitrary sleep.
	until := time.Now().Add(5 * time.Second)
	for {
		code, data := f.api("GET", "/usage-service/full-refresh", nil)
		var inv fr.Inventory
		json.Unmarshal(data, &inv)
		if code == 200 && inv.SchedulerCheckedAtMS == clock.Now().UnixMilli() {
			break
		}
		if time.Now().After(until) {
			t.Fatal("scheduler did not process59minute tick")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if f.calls.Load() != count {
		t.Fatal("period shorter than60minutes")
	}
	clock.advance(time.Minute)
	second := f.waitNextRound(first.ID)
	if second.Purpose != fr.Periodic || second.Succeeded != first.Succeeded || f.calls.Load() != count+int64(first.Succeeded) {
		t.Fatalf("hourly coverage lost: %+v calls%d", second, f.calls.Load())
	}
	for _, item := range second.Items {
		if item.LastSuccess != nil && item.LastSuccess.ObservedAtMS > time.Now().Add(time.Minute).UnixMilli() {
			t.Fatal("scheduler time forged upstream freshness")
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("background lifecycle leaked")
	}
}
func (f *refreshFixture) waitLatestRound() fr.Progress { return f.waitNextRound("") }
func (f *refreshFixture) waitNextRound(previous string) fr.Progress {
	f.t.Helper()
	until := time.Now().Add(15 * time.Second)
	for {
		code, data := f.api("GET", "/usage-service/full-refresh", nil)
		var inv fr.Inventory
		if code == 200 && json.Unmarshal(data, &inv) == nil && inv.LatestRound != nil && inv.LatestRound.ID != previous {
			if inv.LatestRound.State != "running" {
				return *inv.LatestRound
			}
		}
		if time.Now().After(until) {
			f.t.Fatalf("scheduler round missing %d %s", code, data)
		}
		time.Sleep(30 * time.Millisecond)
	}
}
func TestFullRefreshActualCoreLegacyQuotaIsNotAParallelCaller(t *testing.T) {
	f := newRefreshFixture(t)
	code, data := f.api("POST", "/v0/management/api-call", map[string]any{"method": "GET", "url": f.upstream.URL + "/user/balance", "header": map[string]string{"Authorization": "Bearer explicit-billing-legacy"}})
	if code != 409 {
		t.Fatalf("capable Manager must refuse private legacy quota path, not independently fetch: %d %s", code, data)
	}
	if f.calls.Load() != 0 {
		t.Fatal("legacy caller bypassed shared source entry")
	}
}
func TestFullRefreshActualCoreFailureRetainsSuccessAndNotifiesRecoveryContext(t *testing.T) {
	f := newRefreshFixture(t)
	f.configuredSources()
	events := make(chan fr.CompletedObservation, 64)
	f.manager.AppContext().FullRefreshService.Subscribe(func(ctx context.Context, e fr.CompletedObservation) {
		select {
		case events <- e:
		case <-ctx.Done():
		}
	})
	code, data := f.api("POST", "/usage-service/full-refresh", map[string]any{"purpose": "manual"})
	if code != 202 {
		t.Fatalf("round %d %s", code, data)
	}
	var p fr.Progress
	json.Unmarshal(data, &p)
	first := f.waitRound(p.ID)
	old := map[string]string{}
	for _, item := range first.Items {
		if item.Outcome == "success" {
			old[item.ID] = item.LastSuccess.QueryID
		}
	}
	f.mode.Store(2)
	code, data = f.api("POST", "/usage-service/full-refresh", map[string]any{"purpose": "recovery"})
	if code != 202 {
		t.Fatalf("recovery %d %s", code, data)
	}
	json.Unmarshal(data, &p)
	failed := f.waitRound(p.ID)
	if failed.Succeeded != 0 || failed.Failed != len(old) || failed.State != "partial" {
		t.Fatalf("failure reported success %+v", failed)
	}
	for _, item := range failed.Items {
		if last, ok := old[item.ID]; ok {
			if !item.Stale || item.LastSuccess == nil || item.LastSuccess.QueryID != last || item.Attempt == nil || item.Attempt.QueryID == last || item.Attempt.QueryStatus != "auth_error" {
				t.Fatalf("failed source lost honest history %+v", item)
			}
		}
	}
	// Observe through the public completion seam, not a store side channel.
	count := 0
	until := time.After(5 * time.Second)
	for count < len(old) {
		select {
		case e := <-events:
			if e.Purpose != fr.Recovery {
				continue
			}
			count++
			if e.AttemptID == "" {
				t.Fatal("notification has no persisted attempt")
			}
			if e.Official != nil && (e.Source.Official.Version != e.Official.Version || e.Official.Attempt.QueryID != e.AttemptID) {
				t.Fatal("official notification context/version mismatch")
			}
			if e.Credential != nil && (e.Source.Credential.SourceVersion != e.Credential.SourceVersion || e.Credential.Attempt.QueryID != e.AttemptID) {
				t.Fatal("credential notification context/version mismatch")
			}
		case <-until:
			t.Fatalf("only%d/%d recovery attempt notifications", count, len(old))
		}
	}
}
func TestFullRefreshActualCoreCancelAndShutdownFenceLateWork(t *testing.T) {
	f := newRefreshFixture(t)
	f.configuredSources()
	f.mode.Store(1)
	code, data := f.api("POST", "/usage-service/full-refresh", map[string]any{"purpose": "manual"})
	if code != 202 {
		t.Fatalf("round %d %s", code, data)
	}
	var p fr.Progress
	json.Unmarshal(data, &p)
	select {
	case <-f.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("no upstream work")
	}
	code, data = f.api("DELETE", "/usage-service/full-refresh?id="+p.ID, nil)
	if code != 200 {
		t.Fatalf("cancel %d %s", code, data)
	}
	cancelled := f.waitRound(p.ID)
	if cancelled.State != "cancelled" || cancelled.Pending != 0 || cancelled.Succeeded != 0 {
		t.Fatalf("bad cancellation %+v", cancelled)
	}
	close(f.release)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := f.manager.AppContext().FullRefreshService.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	final := f.waitRound(p.ID)
	if final.State != "cancelled" || final.Succeeded != 0 {
		t.Fatal("late result changed cancelled progress")
	}
	// A replacement host must not resurrect an orphan round as running/success.
	old := f.manager.AppContext()
	replacement := fr.New(f.db, old.ResourceCapabilityService, old.OfficialBalanceService, old.CredentialQuotaService)
	read, err := replacement.GetProgress(context.Background(), p.ID)
	if err != nil || read.State != "cancelled" {
		t.Fatalf("restart lost cancellation %v %+v", err, read)
	}
}
func TestFullRefreshActualCoreRetryAfterBlocksManualAndRecovery(t *testing.T) {
	f := newRefreshFixture(t)
	inv := f.configuredSources()
	f.mode.Store(3)
	code, data := f.api("POST", "/usage-service/full-refresh", map[string]any{"purpose": "manual"})
	if code != 202 {
		t.Fatalf("round %d %s", code, data)
	}
	var p fr.Progress
	json.Unmarshal(data, &p)
	failed := f.waitRound(p.ID)
	if failed.Succeeded != 0 || failed.Failed < 8 {
		t.Fatal("429 falsely completed as success")
	}
	var source fr.SourceRequest
	for _, src := range inv.Sources {
		if src.Request.Kind == fr.Official && src.Request.Official.Version > 0 {
			source = src.Request
			break
		}
	}
	before := f.calls.Load()
	code, data = f.api("POST", balancePath, source.Official)
	if code != 429 || f.calls.Load() != before {
		t.Fatalf("manual ignored Retry-After: %d %s calls%d->%d", code, data, before, f.calls.Load())
	}
	code, data = f.api("POST", "/usage-service/full-refresh", map[string]any{"purpose": "recovery", "source": source})
	if code != 202 {
		t.Fatalf("recovery must expose shared failed progress %d %s", code, data)
	}
	json.Unmarshal(data, &p)
	recovery := f.waitRound(p.ID)
	if recovery.Succeeded != 0 || f.calls.Load() != before {
		t.Fatal("recovery bypassed source cooldown")
	}
}
