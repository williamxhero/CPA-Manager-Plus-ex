package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	fr "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fullrefresh"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

// Only external Core/CLI/enrollment and scheduler time are controlled. Each
// manager is a distinct OS process, with no surviving service objects or maps.
// The SQLite path/key survive; all observations are read through the public API.
type webRuntimeDependencies struct {
	blocked               atomic.Int64
	core, browser         *httptest.Server
	snapshot, caps, stage atomic.Value
	reads                 atomic.Int64
	mu                    sync.Mutex
	enrollment            balance.WebEnrollment
	page                  balance.WebPage
}

func newWebRuntimeDependencies(t *testing.T) *webRuntimeDependencies {
	d := &webRuntimeDependencies{}
	d.snapshot.Store(testSnapshotJSON)
	d.caps.Store(testCapsJSON)
	d.stage.Store("")
	d.core = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-core-key" {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/v8/management/resource-capabilities":
			fmt.Fprint(w, d.caps.Load())
		case "/v8/management/resource-bindings":
			fmt.Fprint(w, `{"bindings":[]}`)
		case "/v8/management/resources":
			fmt.Fprint(w, d.snapshot.Load())
		default:
			w.WriteHeader(404)
		}
	}))
	d.browser = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testutil.AdminKey {
			w.WriteHeader(401)
			return
		}
		if r.URL.Path == "/enrollment" {
			d.mu.Lock()
			defer d.mu.Unlock()
			json.NewEncoder(w).Encode(d.enrollment)
			return
		}
		var args []string
		if json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&args) != nil || len(args) == 0 {
			w.WriteHeader(400)
			return
		}
		switch args[0] {
		case "browsers":
			if d.stage.Load() == "closed" {
				fmt.Fprint(w, `[]`)
				return
			}
			fmt.Fprint(w, `[{"instance_id":"synthetic-edge-instance","unresponsive":false}]`)
		case "session":
			if len(args) < 2 {
				w.WriteHeader(400)
				return
			}
			if args[1] == "start" {
				fmt.Fprint(w, `{"session_id":"synthetic-runtime-session","browser_instance_id":"synthetic-edge-instance"}`)
			} else if len(args) != 3 || args[1] != "stop" || args[2] != "synthetic-runtime-session" {
				w.WriteHeader(400)
			}
		case "navigate":
		case "observe":
			d.reads.Add(1)
			if d.stage.Load() == "blocked" {
				d.blocked.Add(1)
				<-r.Context().Done()
				return
			}
			d.mu.Lock()
			defer d.mu.Unlock()
			json.NewEncoder(w).Encode(d.page)
		default:
			w.WriteHeader(400)
		}
	}))
	t.Cleanup(d.core.Close)
	t.Cleanup(d.browser.Close)
	return d
}

func (d *webRuntimeDependencies) enroll(st ob.Status) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.enrollment = balance.WebEnrollment{Authority: st.WebSourceAuthority, Source: *st.Web, ProfileVerified: true, ExecutionUserVerified: true, ParserVerified: true, ProofReference: "synthetic-runtime-enrollment", EvidenceKind: "controlled_fixture"}
	amount := "-12345678901234567890.00000000100"
	d.page = balance.WebPage{URL: balance.LaysoBillingPage, AccountID: st.Web.AccountID, TenantID: st.Web.TenantID, Role: st.Web.Role, IdentityVerified: true, RoleVerified: true, Currency: "USD", Amount: &amount}
}

type webRuntimeConfig struct {
	DB, Core, Browser, Ready string
	NowMS                    int64
}
type webRuntimeReady struct {
	API, Control string
	PID          int
}

// This helper assembles the same HTTP host and FullRefresh.Run startup boundary
// as main. It deliberately does not start updater/collector/other background work.
// Neither fixture control endpoint nor dependency RPC exists in production.
func TestOfficialBalanceWebRuntimeProcess(t *testing.T) {
	raw := os.Getenv("SPEC97_RUNTIME_PROCESS")
	if raw == "" {
		t.Skip("child process fixture only")
	}
	var input webRuntimeConfig
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		t.Fatal(err)
	}
	cfg := testutil.NewConfig(t)
	cfg.DBPath, cfg.DataDir = input.DB, filepath.Dir(input.DB)
	p, err := security.NewProtector([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(cfg.DBPath, p)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	testutil.EnsureAdminCredential(t, db)
	if err := db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: input.Core, ManagementKey: "synthetic-core-key"}}); err != nil {
		t.Fatal(err)
	}
	s := New(cfg, db, collector.NewManager(cfg, db))
	client := &http.Client{Timeout: 15 * time.Second}
	rpc := func(ctx context.Context, path string, body any) ([]byte, error) {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, "POST", input.Browser+path, bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+testutil.AdminKey)
		res, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			return nil, errors.New("controlled dependency rejected request")
		}
		return io.ReadAll(io.LimitReader(res.Body, 2<<20))
	}
	s.AppContext().OfficialBalanceService.WebReader = balance.NewBrowserSkill(balance.BrowserSkillOptions{
		Run: func(ctx context.Context, args ...string) ([]byte, error) { return rpc(ctx, "/command", args) },
		Enrollment: func(q balance.WebRequest) (balance.WebEnrollment, bool) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			raw, err := rpc(ctx, "/enrollment", q)
			var e balance.WebEnrollment
			if err != nil || json.Unmarshal(raw, &e) != nil {
				return e, false
			}
			return e, true
		},
		Rules: map[string]func([]byte) (balance.WebPage, error){syntheticWebRule: func(raw []byte) (balance.WebPage, error) {
			var p balance.WebPage
			err := json.Unmarshal(raw, &p)
			return p, err
		}},
	})
	clock := &refreshClock{now: time.UnixMilli(input.NowMS), ticks: make(chan time.Time, 8)}
	app := s.AppContext()
	app.FullRefreshService = fr.New(db, app.ResourceCapabilityService, app.OfficialBalanceService, app.CredentialQuotaService, fr.WithBudget(app.ManagementQueryBudget), fr.WithClock(clock))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	stop := make(chan struct{}, 1)
	var once sync.Once
	var started atomic.Bool
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testutil.AdminKey || r.Method != "POST" {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/start":
			once.Do(func() { started.Store(true); go func() { done <- app.FullRefreshService.Run(ctx) }() })
		case "/tick":
			var ms int64
			if json.NewDecoder(r.Body).Decode(&ms) != nil {
				w.WriteHeader(400)
				return
			}
			clock.mu.Lock()
			clock.now = time.UnixMilli(ms)
			clock.mu.Unlock()
			clock.ticks <- clock.Now()
		case "/stop":
			select {
			case stop <- struct{}{}:
			default:
			}
		default:
			w.WriteHeader(404)
		}
	}))
	api := httptest.NewServer(s.Handler())
	defer api.Close()
	defer control.Close()
	ready, _ := json.Marshal(webRuntimeReady{API: api.URL, Control: control.URL, PID: os.Getpid()})
	if err := os.WriteFile(input.Ready, ready, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stop:
	case <-time.After(45 * time.Second):
		t.Error("parent did not stop child within 45s")
	}
	// Join control handlers before reading their startup state.
	control.Close()
	cancel()
	if started.Load() {
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("scheduler did not join")
		}
	}
	t.Logf("RUNTIME_PROCESS pid=%d stopped", os.Getpid())
}

type webRuntimeProcess struct {
	t       *testing.T
	ready   webRuntimeReady
	cmd     *exec.Cmd
	done    chan error
	dir     string
	stopped bool
}

func startWebRuntime(t *testing.T, d *webRuntimeDependencies, db string, now time.Time) *webRuntimeProcess {
	t.Helper()
	p := &webRuntimeProcess{t: t, dir: t.TempDir(), done: make(chan error, 1)}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(webRuntimeConfig{DB: db, Core: d.core.URL, Browser: d.browser.URL, Ready: filepath.Join(p.dir, "ready.json"), NowMS: now.UnixMilli()})
	p.cmd = exec.Command(exe, "-test.run=^TestOfficialBalanceWebRuntimeProcess$", "-test.v", "-test.timeout=55s")
	p.cmd.Env = append(os.Environ(), "SPEC97_RUNTIME_PROCESS="+string(input))
	for _, name := range []string{"stdout", "stderr"} {
		f, err := os.Create(filepath.Join(p.dir, name+".log"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { f.Close() })
		if name == "stdout" {
			p.cmd.Stdout = f
		} else {
			p.cmd.Stderr = f
		}
	}
	if err := p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { p.done <- p.cmd.Wait() }()
	t.Cleanup(func() { p.stop(false) })
	until := time.Now().Add(10 * time.Second)
	for {
		raw, err := os.ReadFile(filepath.Join(p.dir, "ready.json"))
		if err == nil && json.Unmarshal(raw, &p.ready) == nil {
			break
		}
		if time.Now().After(until) {
			t.Fatal("child manager not ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if p.ready.PID != p.cmd.Process.Pid {
		t.Fatal("child identity mismatch")
	}
	t.Logf("RUNTIME_PROCESS_READY pid=%d sqlite=%s", p.ready.PID, db)
	return p
}

func (p *webRuntimeProcess) stop(crash bool) {
	if p.stopped {
		return
	}
	p.stopped = true
	if crash {
		_ = p.cmd.Process.Kill()
	} else if p.ready.Control != "" {
		req, err := http.NewRequest("POST", p.ready.Control+"/stop", nil)
		if err == nil {
			req.Header.Set("Authorization", "Bearer "+testutil.AdminKey)
			client := &http.Client{Timeout: 2 * time.Second}
			var res *http.Response
			res, err = client.Do(req)
			if res != nil {
				res.Body.Close()
				if res.StatusCode != 200 {
					err = fmt.Errorf("stop status %d", res.StatusCode)
				}
			}
		}
		if err != nil {
			p.t.Error("graceful child stop", err)
			_ = p.cmd.Process.Kill()
		}
	} else {
		_ = p.cmd.Process.Kill()
	}
	select {
	case err := <-p.done:
		if !crash && err != nil {
			p.t.Error("child exit", err)
		}
	case <-time.After(7 * time.Second):
		_ = p.cmd.Process.Kill()
		<-p.done
		p.t.Error("child stop timed out")
	}
	for _, name := range []string{"stdout", "stderr"} {
		raw, _ := os.ReadFile(filepath.Join(p.dir, name+".log"))
		p.t.Logf("CHILD_%s pid=%d crash=%v\n%s", name, p.cmd.Process.Pid, crash, raw)
	}
}

func (p *webRuntimeProcess) request(method, path, body string, out any, status int) {
	p.t.Helper()
	req, err := http.NewRequest(method, path, strings.NewReader(body))
	if err != nil {
		p.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+testutil.AdminKey)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 4 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		p.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil || res.StatusCode != status {
		p.t.Fatalf("%s %s: %d want %d body=%s err=%v", method, path, res.StatusCode, status, raw, err)
	}
	if out != nil && json.Unmarshal(raw, out) != nil {
		p.t.Fatalf("invalid response %s", raw)
	}
}
func (p *webRuntimeProcess) inventory() fr.Inventory {
	var inv fr.Inventory
	p.request("GET", p.ready.API+webRefreshPath, "", &inv, 200)
	return inv
}
func (p *webRuntimeProcess) status(b ob.Binding) ob.Status {
	raw, _ := json.Marshal(b)
	var st ob.Status
	p.request("GET", p.ready.API+balancePath+"?binding="+url.QueryEscape(string(raw)), "", &st, 200)
	return st
}
func (p *webRuntimeProcess) configure(body map[string]any) ob.Status {
	var st ob.Status
	p.request("PUT", p.ready.API+balancePath, webBody(p.t, body), &st, 200)
	return st
}
func (p *webRuntimeProcess) start() { p.request("POST", p.ready.Control+"/start", "", nil, 200) }
func (p *webRuntimeProcess) tick(now time.Time) {
	p.request("POST", p.ready.Control+"/tick", fmt.Sprint(now.UnixMilli()), nil, 200)
	p.wait(func(inv fr.Inventory) bool { return inv.SchedulerCheckedAtMS == now.UnixMilli() })
}
func (p *webRuntimeProcess) wait(accept func(fr.Inventory) bool) fr.Inventory {
	p.t.Helper()
	until := time.Now().Add(6 * time.Second)
	for {
		inv := p.inventory()
		if accept(inv) {
			return inv
		}
		if time.Now().After(until) {
			raw, _ := json.Marshal(inv)
			p.t.Fatalf("runtime did not progress: %s", raw)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
func (p *webRuntimeProcess) round(previous string, running bool) fr.Progress {
	return *p.wait(func(inv fr.Inventory) bool {
		return inv.LatestRound != nil && inv.LatestRound.ID != previous && (running || inv.LatestRound.State != "running")
	}).LatestRound
}
func runtimeWebConfig() map[string]any {
	body := webConfiguration(0)
	body["web"].(map[string]any)["parsingRule"] = syntheticWebRule
	return body
}

func TestOfficialBalanceWebRuntimeRestartRestoresHourlyAndFailure(t *testing.T) {
	d := newWebRuntimeDependencies(t)
	db := filepath.Join(t.TempDir(), "runtime.sqlite")
	now := time.Now().Truncate(time.Millisecond)
	first := startWebRuntime(t, d, db, now)
	st := first.configure(runtimeWebConfig())
	d.enroll(st)
	first.start()
	initial := first.round("", false)
	if initial.Succeeded != 1 || initial.Items[0].LastSuccess == nil {
		t.Fatal("initial capture failed", initial)
	}
	last := first.status(st.Binding).LastSuccess
	due := first.inventory().NextDueMS
	first.stop(false)
	second := startWebRuntime(t, d, db, now.Add(30*time.Minute))
	restored := second.status(st.Binding)
	if restored.Version != 1 || !reflect.DeepEqual(restored.LastSuccess, last) || restored.LastSuccess.AmountDecimal != "-12345678901234567890.00000000100" {
		t.Fatal("restart lost exact persisted observation", restored)
	}
	second.start()
	second.wait(func(inv fr.Inventory) bool { return inv.SchedulerCheckedAtMS == now.Add(30*time.Minute).UnixMilli() })
	if inv := second.inventory(); inv.NextDueMS != due || inv.LatestRound.ID != initial.ID {
		t.Fatal("restart reset hourly anchor", inv)
	}
	second.tick(now.Add(59 * time.Minute))
	if second.inventory().LatestRound.ID != initial.ID {
		t.Fatal("restarted source refreshed early")
	}
	d.stage.Store("closed")
	second.tick(now.Add(time.Hour))
	failed := second.round(initial.ID, false)
	status := second.status(st.Binding)
	if failed.State != "partial" || failed.Failed != 1 || status.Attempt == nil || status.Attempt.ErrorCode != "WEB_BROWSER_UNAVAILABLE" || !status.Stale || !reflect.DeepEqual(status.LastSuccess, last) || status.Attempt.AmountDecimal != "" || status.Attempt.ObservedAtMS != 0 {
		t.Fatal("restarted failure lost/renewed last success", failed, status)
	}
	second.stop(false)
	third := startWebRuntime(t, d, db, now.Add(2*time.Hour))
	persisted := third.status(st.Binding)
	if !persisted.Stale || !reflect.DeepEqual(persisted.LastSuccess, last) || persisted.Attempt.ErrorCode != "WEB_BROWSER_UNAVAILABLE" {
		t.Fatal("failure not durable across second restart", persisted)
	}
	d.stage.Store("")
	third.start()
	recovered := third.round(failed.ID, false)
	if recovered.State != "complete" || recovered.Succeeded != 1 || third.status(st.Binding).Stale {
		t.Fatal("overdue startup did not recover", recovered)
	}
}

func TestOfficialBalanceWebRuntimeCrashDoesNotResumeOldJob(t *testing.T) {
	d := newWebRuntimeDependencies(t)
	db := filepath.Join(t.TempDir(), "runtime.sqlite")
	now := time.Now().Truncate(time.Millisecond)
	first := startWebRuntime(t, d, db, now)
	st := first.configure(runtimeWebConfig())
	d.enroll(st)
	first.start()
	initial := first.round("", false)
	last := first.status(st.Binding).LastSuccess
	d.stage.Store("blocked")
	first.tick(now.Add(time.Hour))
	running := first.wait(func(inv fr.Inventory) bool {
		return inv.LatestRound != nil && inv.LatestRound.ID != initial.ID && inv.LatestRound.State == "running" && len(inv.LatestRound.Items) == 1 && inv.LatestRound.Items[0].JobID != "" && d.blocked.Load() > 0
	}).LatestRound
	oldID := running.Items[0].JobID
	first.stop(true) // Kill only the exact child owned by this test.
	d.stage.Store("closed")
	second := startWebRuntime(t, d, db, now.Add(61*time.Minute))
	var old ob.Job
	second.request("GET", second.ready.API+balancePath+"?job="+oldID, "", &old, 200)
	if old.State != "interrupted" || old.Status == nil || !old.Status.Stale || !reflect.DeepEqual(old.Status.LastSuccess, last) {
		t.Fatal("crashed process job was resurrected or renewed", old)
	}
	second.start()
	second.wait(func(inv fr.Inventory) bool {
		return inv.SchedulerCheckedAtMS == now.Add(61*time.Minute).UnixMilli() && inv.LatestRound != nil && inv.LatestRound.State != "running"
	})
	var interrupted fr.Progress
	second.request("GET", second.ready.API+webRefreshPath+"?id="+running.ID, "", &interrupted, 200)
	if interrupted.State != "interrupted" || interrupted.Pending != 0 || interrupted.Items[0].Outcome != "interrupted" || !interrupted.Items[0].Stale {
		t.Fatal("crashed round did not become durable interrupted history", interrupted)
	}
	// Admission retains the prior hourly anchor: recovery must not flood the
	// same source before its next due time or join a dead process's local flight.
	previous := second.inventory().LatestRound.ID
	second.tick(now.Add(2 * time.Hour))
	failed := second.round(previous, false)
	status := second.status(st.Binding)
	if failed.Items[0].JobID == oldID || failed.Failed != 1 || status.Attempt.ErrorCode != "WEB_BROWSER_UNAVAILABLE" || !status.Stale || !reflect.DeepEqual(status.LastSuccess, last) {
		t.Fatal("next hourly attempt reused dead job or lost last success", failed, status)
	}
}

func TestOfficialBalanceWebRuntimeGracefulStopPersistsCancellation(t *testing.T) {
	d := newWebRuntimeDependencies(t)
	db := filepath.Join(t.TempDir(), "runtime.sqlite")
	now := time.Now().Truncate(time.Millisecond)
	first := startWebRuntime(t, d, db, now)
	st := first.configure(runtimeWebConfig())
	d.enroll(st)
	first.start()
	initial := first.round("", false)
	last := first.status(st.Binding).LastSuccess
	d.stage.Store("blocked")
	first.tick(now.Add(time.Hour))
	running := first.wait(func(inv fr.Inventory) bool {
		return inv.LatestRound != nil && inv.LatestRound.ID != initial.ID && len(inv.LatestRound.Items) == 1 && inv.LatestRound.Items[0].JobID != "" && d.blocked.Load() > 0
	}).LatestRound
	first.stop(false)
	second := startWebRuntime(t, d, db, now.Add(61*time.Minute))
	var job ob.Job
	second.request("GET", second.ready.API+balancePath+"?job="+running.Items[0].JobID, "", &job, 200)
	stopped := second.status(st.Binding)
	if job.State != "complete" || stopped.Attempt == nil || stopped.Attempt.ErrorCode != "WEB_CANCELLED" || stopped.Attempt.AmountDecimal != "" || stopped.Attempt.ObservedAtMS != 0 || !stopped.Stale || !reflect.DeepEqual(stopped.LastSuccess, last) {
		t.Fatal("graceful stop did not persist cancellation before process exit", job, stopped)
	}
}

func TestOfficialBalanceWebRuntimeRequiresEnrollmentAfterRestart(t *testing.T) {
	d := newWebRuntimeDependencies(t)
	db := filepath.Join(t.TempDir(), "runtime.sqlite")
	now := time.Now().Truncate(time.Millisecond)
	first := startWebRuntime(t, d, db, now)
	st := first.configure(runtimeWebConfig())
	d.enroll(st)
	first.start()
	initial := first.round("", false)
	last := first.status(st.Binding).LastSuccess
	first.stop(false)
	d.mu.Lock()
	d.enrollment = balance.WebEnrollment{}
	d.mu.Unlock()
	reads := d.reads.Load()
	second := startWebRuntime(t, d, db, now.Add(time.Hour))
	second.start()
	unverified := second.round(initial.ID, false)
	status := second.status(st.Binding)
	if unverified.State != "partial" || unverified.Succeeded != 0 || status.State != "unavailable" || status.DirectQuery || !status.Stale || !reflect.DeepEqual(status.LastSuccess, last) || d.reads.Load() != reads {
		t.Fatal("restart trusted persisted configuration without live enrollment", unverified, status)
	}
	d.enroll(status)
	second.tick(now.Add(61 * time.Minute))
	recovered := second.round(unverified.ID, false)
	if recovered.Succeeded != 1 || second.status(st.Binding).Stale {
		t.Fatal("verified source did not recover after enrollment", recovered)
	}
}

func TestOfficialBalanceWebRuntimeRejectsOldAuthorityAfterRestart(t *testing.T) {
	for _, kind := range []string{"boot", "identity-version", "credential-version", "config-version", "membership-version", "account", "tenant", "role"} {
		t.Run(kind, func(t *testing.T) {
			d := newWebRuntimeDependencies(t)
			db := filepath.Join(t.TempDir(), "runtime.sqlite")
			now := time.Now().Truncate(time.Millisecond)
			first := startWebRuntime(t, d, db, now)
			st := first.configure(runtimeWebConfig())
			d.enroll(st)
			first.start()
			initial := first.round("", false)
			last := first.status(st.Binding).LastSuccess
			d.stage.Store("blocked")
			first.tick(now.Add(time.Hour))
			running := first.wait(func(inv fr.Inventory) bool {
				return inv.LatestRound != nil && inv.LatestRound.ID != initial.ID && inv.LatestRound.State == "running" && len(inv.LatestRound.Items) == 1 && inv.LatestRound.Items[0].JobID != "" && d.blocked.Load() > 0
			}).LatestRound
			oldID := running.Items[0].JobID
			first.stop(true)
			readsBeforeRestart := d.reads.Load()
			body := runtimeWebConfig()
			body["version"] = 1
			switch kind {
			case "boot":
				d.snapshot.Store(strings.ReplaceAll(testSnapshotJSON, "33333333-3333-4333-8333-333333333333", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"))
				d.caps.Store(strings.ReplaceAll(testCapsJSON, "33333333-3333-4333-8333-333333333333", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"))
				body["scope"].(map[string]any)["boot_epoch"] = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
			case "account", "tenant", "role":
				field, value := "accountId", "synthetic-account-b"
				if kind == "tenant" {
					field, value = "tenantId", "synthetic-tenant-b"
				}
				if kind == "role" {
					field, value = "role", "billing-auditor"
				}
				body["web"].(map[string]any)[field] = value
			default:
				field := strings.ReplaceAll(kind, "-", "_")
				var snapshot map[string]any
				json.Unmarshal([]byte(testSnapshotJSON), &snapshot)
				row := snapshot["resources"].([]any)[0].(map[string]any)
				row[field] = row[field].(float64) + 1
				body["targets"].([]any)[0].(map[string]any)[field] = row[field]
				raw, _ := json.Marshal(snapshot)
				d.snapshot.Store(string(raw))
			}
			d.stage.Store("")
			second := startWebRuntime(t, d, db, now.Add(61*time.Minute))
			if kind != "account" && kind != "tenant" && kind != "role" {
				second.request("GET", second.ready.API+balancePath+"?job="+oldID, "", nil, 409)
				oldQuery, _ := json.Marshal(ob.Query{Binding: st.Binding, Version: 1})
				second.request("POST", second.ready.API+balancePath, string(oldQuery), nil, 409)
			}
			current := second.configure(body)
			if current.Version != 2 || current.DirectQuery || current.LastSuccess != nil || current.Attempt != nil {
				t.Fatal("new authority inherited old enrollment/result", current)
			}
			second.request("GET", second.ready.API+balancePath+"?job="+oldID, "", nil, 409)
			oldQuery, _ := json.Marshal(ob.Query{Binding: current.Binding, Version: 1})
			second.request("POST", second.ready.API+balancePath, string(oldQuery), nil, 409)
			// An absent matching enrollment fails closed after restart, even
			// though the same resource already had a successful page capture.
			second.start()
			second.wait(func(inv fr.Inventory) bool {
				return inv.SchedulerCheckedAtMS == now.Add(61*time.Minute).UnixMilli() && inv.LatestRound != nil && inv.LatestRound.State != "running"
			})
			if d.reads.Load() != readsBeforeRestart || second.status(current.Binding).LastSuccess != nil {
				t.Fatal("unverified new authority invoked page reader/reused success")
			}
			d.enroll(current)
			second.tick(now.Add(62 * time.Minute))
			recovered := second.wait(func(inv fr.Inventory) bool {
				return inv.LatestRound != nil && inv.LatestRound.State != "running" && inv.LatestRound.Succeeded == 1 && inv.LatestRound.Items[0].JobID != "" && inv.LatestRound.Items[0].JobID != oldID
			}).LatestRound
			fresh := second.status(current.Binding)
			if fresh.Version != 2 || fresh.Stale || fresh.LastSuccess == nil || reflect.DeepEqual(fresh.LastSuccess, last) || fresh.LastSuccess.QueryID != recovered.Items[0].JobID || fresh.Web.AccountID != body["web"].(map[string]any)["accountId"] || fresh.Web.Role != body["web"].(map[string]any)["role"] {
				t.Fatal("new authority did not get a separate verified observation", fresh, recovered)
			}
		})
	}
}
