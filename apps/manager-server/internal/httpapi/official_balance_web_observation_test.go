package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

// This rule and every identity below describe a synthetic external CLI/page
// fixture. No production LAYSO rule, real DOM, browser or account is claimed.
const syntheticWebRule = "synthetic-observed-page-v1"

type webObservationFixture struct {
	f                 *officialFixture
	driver            *balance.BrowserSkill
	page              atomic.Value
	authority         atomic.Value
	extraAuthority    atomic.Value
	verified          atomic.Bool
	executionVerified atomic.Bool
	parserVerified    atomic.Bool
	stage             atomic.Value
	reads             atomic.Int64
	readHook          atomic.Value
	timeoutMS         int64
	mu                sync.Mutex
	calls             [][]string
}

func newWebObservationFixture(t *testing.T) *webObservationFixture {
	x := &webObservationFixture{f: newOfficialFixture(t)}
	x.authority.Store("")
	x.extraAuthority.Store("")
	x.verified.Store(true)
	x.executionVerified.Store(true)
	x.parserVerified.Store(true)
	x.stage.Store("")
	x.readHook.Store(func(context.Context, int64) error { return nil })
	var source balance.WebConfig
	if err := json.Unmarshal([]byte(webSourceJSON), &source); err != nil {
		t.Fatal(err)
	}
	source.ParsingRule = syntheticWebRule
	amount := "-12345678901234567890.00000000100"
	x.page.Store(balance.WebPage{URL: balance.LaysoBillingPage, AccountID: source.AccountID, TenantID: source.TenantID, Role: source.Role, IdentityVerified: true, RoleVerified: true, Currency: "USD", Amount: &amount})
	x.driver = balance.NewBrowserSkill(balance.BrowserSkillOptions{
		Run: func(ctx context.Context, args ...string) ([]byte, error) {
			x.mu.Lock()
			x.calls = append(x.calls, append([]string(nil), args...))
			x.mu.Unlock()
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if x.stage.Load().(string) == args[0] {
				return []byte("RAW-PAGE-CANARY"), errors.New("RAW-PAGE-CANARY")
			}
			stage := x.stage.Load().(string)
			switch args[0] {
			case "browsers":
				if stage == "browser-closed" {
					return []byte(`[]`), nil
				}
				if strings.HasPrefix(stage, "budget-session-") {
					body := `[{"instance_id":"synthetic-edge-instance","session_id":"foreign-list-session"}]`
					return []byte(body + strings.Repeat(" ", 2*1024*1024-32-len(body))), nil
				}
				if stage == "wrong-browser" {
					return []byte(`[{"instance_id":"different-instance","unresponsive":false}]`), nil
				}
				if stage == "revoke-on-browsers" {
					x.verified.Store(false)
				}
				return []byte(`[{"instance_id":"synthetic-edge-instance","unresponsive":false}]`), nil
			case "session":
				if args[1] == "start" {
					switch stage {
					case "budget-session-duplicate":
						return []byte(`{"session_id":"synthetic-owned-session","session_id":"foreign-session"}`), nil
					case "budget-session-escaped-duplicate":
						return []byte(`{"session_id":"synthetic-owned-session","session_\u0069d":"foreign-session"}`), nil
					case "budget-session-missing":
						return []byte(`{"browser_instance_id":"synthetic-edge-instance"}`), nil
					case "budget-session-nested":
						return []byte(`{"other":{"session_id":"foreign-session"}}`), nil
					case "budget-session-null":
						return []byte(`{"session_id":null,"padding":"force-over-budget"}`), nil
					case "budget-session-number":
						return []byte(`{"session_id":123,"padding":"force-over-budget"}`), nil
					case "budget-session-unsafe":
						return []byte(`{"session_id":"foreign-session --all"}`), nil
					case "budget-session-long":
						return []byte(`{"session_id":"` + strings.Repeat("x", 129) + `"}`), nil
					case "budget-session-malformed":
						return []byte(`{"session_id":"synthetic-owned-session",`), nil
					case "budget-session-trailing":
						return []byte(`{"session_id":"synthetic-owned-session"} {}`), nil
					case "budget-session-oversized":
						return []byte(`{"session_id":"synthetic-owned-session"}` + strings.Repeat(" ", 2*1024*1024)), nil
					case "budget-session-partial-start":
						return []byte(`{"session_id":"synthetic-owned-session"}`), errors.New("RAW-PAGE-CANARY")
					case "budget-session-malformed-browser":
						return []byte(`{"session_id":"synthetic-owned-session","browser_instance_id":123}`), nil
					case "budget-session-wrong-browser":
						return []byte(`{"session_id":"synthetic-owned-session","browser_instance_id":"different-instance"}`), nil
					case "budget-session-revoked":
						x.verified.Store(false)
					}
					if stage == "malformed-session" {
						return []byte(`{"session_id":"synthetic-owned-session","browser_instance_id":123}`), nil
					}
					if stage == "partial-start" {
						return []byte(`{"session_id":"synthetic-owned-session"}`), errors.New("RAW-PAGE-CANARY")
					}
					if stage == "wrong-session-browser" {
						return []byte(`{"session_id":"synthetic-owned-session","browser_instance_id":"different-instance"}`), nil
					}
					return []byte(`{"session_id":"synthetic-owned-session","browser_instance_id":"synthetic-edge-instance"}`), nil
				}
				if stage == "stop-failed" || stage == "budget-session-stop-failed" {
					return nil, errors.New("RAW-PAGE-CANARY")
				}
				if stage == "budget-session-stop-output" {
					return []byte(`{"ok":true}`), nil
				}
				if stage == "revoke-on-stop" {
					x.verified.Store(false)
				}
				return nil, nil
			case "navigate":
				return nil, nil
			case "observe":
				if err := x.readHook.Load().(func(context.Context, int64) error)(ctx, x.reads.Add(1)); err != nil {
					return nil, err
				}
				if stage == "oversized" {
					return []byte(strings.Repeat("X", 2*1024*1024+1)), nil
				}
				return json.Marshal(x.page.Load().(balance.WebPage))
			default:
				return nil, errors.New("forbidden command in controlled fixture")
			}
		},
		Enrollment: func(q balance.WebRequest) (balance.WebEnrollment, bool) {
			authority := x.authority.Load().(string)
			if extra := x.extraAuthority.Load().(string); extra != "" && extra == q.Authority {
				authority = extra
			}
			return balance.WebEnrollment{Authority: authority, Source: source, ProfileVerified: x.verified.Load(), ExecutionUserVerified: x.executionVerified.Load(), ParserVerified: x.parserVerified.Load(), ProofReference: "synthetic-host-enrollment", EvidenceKind: "controlled_fixture"}, true
		},
		Rules: map[string]func([]byte) (balance.WebPage, error){syntheticWebRule: func(raw []byte) (balance.WebPage, error) {
			var page balance.WebPage
			err := json.Unmarshal(raw, &page)
			return page, err
		}},
	})
	x.f.server.AppContext().OfficialBalanceService.WebReader = x.driver
	return x
}

func TestOfficialBalanceWebObservationFailuresRetainExactSuccess(t *testing.T) {
	x := newWebObservationFixture(t)
	x.configure(t)
	good := x.page.Load().(balance.WebPage)
	last := x.result(t)["lastSuccess"]
	for _, tc := range []struct {
		name   string
		edit   func(*balance.WebPage)
		status string
	}{
		{"missing", func(p *balance.WebPage) { p.Amount = nil }, "parse_error"},
		{"NaN", func(p *balance.WebPage) { s := "NaN"; p.Amount = &s }, "parse_error"},
		{"infinity", func(p *balance.WebPage) { s := "Infinity"; p.Amount = &s }, "parse_error"},
		{"abbreviation", func(p *balance.WebPage) { s := "1.2K"; p.Amount = &s }, "parse_error"},
		{"exponent", func(p *balance.WebPage) { s := "1e3"; p.Amount = &s }, "parse_error"},
		{"currency", func(p *balance.WebPage) { p.Currency = "CNY" }, "parse_error"},
		{"account", func(p *balance.WebPage) { p.AccountID = "other-account" }, "auth_error"},
		{"tenant", func(p *balance.WebPage) { p.TenantID = "other-tenant" }, "auth_error"},
		{"role", func(p *balance.WebPage) { p.Role = "other-role" }, "auth_error"},
		{"identity-unverified", func(p *balance.WebPage) { p.IdentityVerified = false }, "auth_error"},
		{"role-unverified", func(p *balance.WebPage) { p.RoleVerified = false }, "auth_error"},
		{"login-url", func(p *balance.WebPage) { p.URL = "https://layso.ai/login" }, "auth_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := good
			tc.edit(&page)
			x.page.Store(page)
			status := x.result(t)
			attempt := status["attempt"].(map[string]any)
			if attempt["queryStatus"] != tc.status || attempt["observedAtMs"] != nil || attempt["amountDecimal"] != nil || attempt["webEvidence"] != nil || status["stale"] != true || !reflect.DeepEqual(last, status["lastSuccess"]) {
				t.Fatal("failure changed/renewed the previous exact capture", status)
			}
		})
	}
	x.page.Store(good)
	x.stage.Store("observe")
	status := x.result(t)
	if status["stale"] != true || !reflect.DeepEqual(last, status["lastSuccess"]) || strings.Contains(webBody(t, status), "RAW-PAGE-CANARY") {
		t.Fatal("external failure leaked raw output or lost last_success", status)
	}
}

func TestOfficialBalanceWebObservationEnrollmentRevocationFailsClosed(t *testing.T) {
	x := newWebObservationFixture(t)
	x.configure(t)
	last := x.result(t)["lastSuccess"]
	x.verified.Store(false)
	x.mu.Lock()
	before := len(x.calls)
	x.mu.Unlock()
	testutil.RequireStatus(t, testutil.Request(t, x.f.handler, "POST", balancePath, `{`+balanceBinding+`,"version":1}`, testutil.AdminKey), 400)
	var status map[string]any
	if err := json.Unmarshal([]byte(x.f.status()), &status); err != nil {
		t.Fatal(err)
	}
	if status["stale"] != true || status["managerDirectQuery"] != false || !reflect.DeepEqual(last, status["lastSuccess"]) {
		t.Fatal("revocation did not fail closed", status)
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if len(x.calls) != before {
		t.Fatal("unverified identity reached the browser")
	}
}

func TestOfficialBalanceWebObservationAPIFailuresNeverUseBrowser(t *testing.T) {
	x := newWebObservationFixture(t)
	x.f.configure("")
	for _, body := range []string{"status:401", "status:429", "status:503", "bad-json"} {
		x.f.response.Store(body)
		status := x.result(t)
		if status["lastSuccess"] != nil || status["attempt"].(map[string]any)["queryStatus"] == "success" {
			t.Fatal("API failure became a success", status)
		}
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if len(x.calls) != 0 {
		t.Fatal("API error silently switched to web", x.calls)
	}
}

func TestOfficialBalanceWebObservationUnverifiedAuthorityNeverInvokesCLI(t *testing.T) {
	for _, field := range []string{"profile", "execution-user", "parser", "authority", "core-version"} {
		t.Run(field, func(t *testing.T) {
			x := newWebObservationFixture(t)
			x.configure(t)
			code := 400
			switch field {
			case "profile":
				x.verified.Store(false)
			case "execution-user":
				x.executionVerified.Store(false)
			case "parser":
				x.parserVerified.Store(false)
			case "authority":
				x.authority.Store(strings.Repeat("0", 64))
			case "core-version":
				x.f.coreSnapshot.Store(strings.Replace(testSnapshotJSON, `"credential_version":3`, `"credential_version":4`, 1))
				code = 409
			}
			testutil.RequireStatus(t, testutil.Request(t, x.f.handler, "POST", balancePath, `{`+balanceBinding+`,"version":1}`, testutil.AdminKey), code)
			x.mu.Lock()
			defer x.mu.Unlock()
			if len(x.calls) != 0 {
				t.Fatal("unverified authority reached CLI", x.calls)
			}
		})
	}
}

func TestOfficialBalanceWebObservationRoleChangesDuringRead(t *testing.T) {
	x := newWebObservationFixture(t)
	x.configure(t)
	last := x.result(t)["lastSuccess"]
	x.reads.Store(0)
	x.readHook.Store(func(_ context.Context, n int64) error {
		if n == 2 {
			page := x.page.Load().(balance.WebPage)
			page.Role = "different-role"
			x.page.Store(page)
		}
		return nil
	})
	status := x.result(t)
	if status["attempt"].(map[string]any)["queryStatus"] != "auth_error" || status["stale"] != true || !reflect.DeepEqual(last, status["lastSuccess"]) {
		t.Fatal("mid-read role change became a success", status)
	}
}

func TestOfficialBalanceWebObservationBrowserFailuresKeepSuccess(t *testing.T) {
	for _, stage := range []string{"wrong-browser", "partial-start", "wrong-session-browser", "malformed-session", "navigate", "oversized", "stop-failed", "revoke-on-stop"} {
		t.Run(stage, func(t *testing.T) {
			x := newWebObservationFixture(t)
			x.configure(t)
			last := x.result(t)["lastSuccess"]
			x.stage.Store(stage)
			status := x.result(t)
			attempt := status["attempt"].(map[string]any)
			if attempt["queryStatus"] == "success" || attempt["amountDecimal"] != nil || attempt["observedAtMs"] != nil || status["stale"] != true || !reflect.DeepEqual(last, status["lastSuccess"]) || strings.Contains(webBody(t, status), "RAW-PAGE-CANARY") {
				t.Fatal("failed browser lifecycle published capture or leaked raw error", status)
			}
			if stage != "wrong-browser" {
				x.mu.Lock()
				defer x.mu.Unlock()
				if !reflect.DeepEqual(x.calls[len(x.calls)-1], []string{"session", "stop", "synthetic-owned-session"}) {
					t.Fatal("own session cleanup missing", x.calls)
				}
			}
		})
	}
}

func TestOfficialBalanceWebObservationTimeoutCleansOwnSession(t *testing.T) {
	x := newWebObservationFixture(t)
	x.timeoutMS = 1000
	x.configure(t)
	last := x.result(t)["lastSuccess"]
	x.readHook.Store(func(ctx context.Context, _ int64) error { <-ctx.Done(); return ctx.Err() })
	started := time.Now()
	status := x.result(t)
	if time.Since(started) > 2*time.Second || status["attempt"].(map[string]any)["errorCode"] == "WEB_CLEANUP_FAILED" || status["stale"] != true || !reflect.DeepEqual(last, status["lastSuccess"]) {
		t.Fatal("timeout exceeded budget, cancelled cleanup, or lost success", status)
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if !reflect.DeepEqual(x.calls[len(x.calls)-1], []string{"session", "stop", "synthetic-owned-session"}) {
		t.Fatal("timed-out own session not stopped", x.calls)
	}
}

func TestOfficialBalanceWebObservationUsesConfiguredBudget(t *testing.T) {
	x := newWebObservationFixture(t)
	x.timeoutMS = 60000
	x.configure(t)
	var remaining atomic.Int64
	x.readHook.Store(func(ctx context.Context, _ int64) error {
		deadline, ok := ctx.Deadline()
		if ok {
			remaining.Store(int64(time.Until(deadline)))
		}
		return nil
	})
	status := x.result(t)
	if status["lastSuccess"] == nil || time.Duration(remaining.Load()) < 40*time.Second {
		t.Fatalf("configured management budget silently shortened: remaining=%v status=%v", time.Duration(remaining.Load()), status)
	}
}

func TestOfficialBalanceWebObservationRevokedBeforeSessionNeverNavigates(t *testing.T) {
	x := newWebObservationFixture(t)
	x.configure(t)
	last := x.result(t)["lastSuccess"]
	x.mu.Lock()
	before := len(x.calls)
	x.mu.Unlock()
	x.stage.Store("revoke-on-browsers")
	status := x.result(t)
	if status["stale"] != true || !reflect.DeepEqual(last, status["lastSuccess"]) {
		t.Fatal("revoked enrollment renewed capture", status)
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if len(x.calls)-before != 1 {
		t.Fatal("revoked enrollment still opened/read a session", x.calls[before:])
	}
}

func TestOfficialBalanceWebObservationChangedSourceRejectsInflightResult(t *testing.T) {
	x := newWebObservationFixture(t)
	x.configure(t)
	x.result(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	x.readHook.Store(func(ctx context.Context, _ int64) error {
		once.Do(func() { close(entered) })
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	id := x.f.query()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("read never started")
	}
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	body := webConfiguration(1)
	source := body["web"].(map[string]any)
	source["parsingRule"] = syntheticWebRule
	source["accountId"] = "replacement-account"
	rr := testutil.Request(t, x.f.handler, "PUT", balancePath, webBody(t, body), testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	var status map[string]any
	testutil.DecodeJSON(t, rr, &status)
	if status["lastSuccess"] != nil || status["managerDirectQuery"] != false {
		t.Fatal("new version inherited authority/results", status)
	}
	close(release)
	x.awaitWorker(t, id)
	testutil.RequireStatus(t, testutil.Request(t, x.f.handler, "GET", balancePath+"?job="+id, "", testutil.AdminKey), 409)
	// The replacement remains unenrolled. Reading it cannot reveal the old
	// account capture or authorize a browser query, even after the old read ends.
	if err := json.Unmarshal([]byte(x.f.status()), &status); err != nil {
		t.Fatal(err)
	}
	if status["lastSuccess"] != nil || status["version"] != float64(2) {
		t.Fatal("old source crossed configuration CAS", status)
	}
}

func (x *webObservationFixture) awaitWorker(t *testing.T, id string) {
	t.Helper()
	// Synchronize completion only; persisted behavior is asserted exclusively
	// through the public API below, never by reading SQLite or injecting results.
	deadline := time.After(3 * time.Second)
	for x.f.server.AppContext().ManagementQueryBudget.Active(id) {
		select {
		case <-deadline:
			t.Fatal("controlled worker failed to terminate")
		case <-time.After(time.Millisecond):
		}
	}
}

func TestOfficialBalanceWebObservationCoreVersionChangesDuringRead(t *testing.T) {
	x := newWebObservationFixture(t)
	x.configure(t)
	last := x.result(t)["lastSuccess"]
	x.reads.Store(0)
	x.readHook.Store(func(_ context.Context, n int64) error {
		if n == 2 {
			x.f.coreSnapshot.Store(strings.Replace(testSnapshotJSON, `"config_version":4`, `"config_version":5`, 1))
		}
		return nil
	})
	id := x.f.query()
	x.awaitWorker(t, id)
	testutil.RequireStatus(t, testutil.Request(t, x.f.handler, "GET", balancePath+"?job="+id, "", testutil.AdminKey), 409)
	// Restore only the synthetic external identity after the worker has ended:
	// the rejected result cannot become visible when the old binding is readable.
	x.f.coreSnapshot.Store(testSnapshotJSON)
	var job struct {
		State  string
		Status map[string]any
	}
	rr := testutil.Request(t, x.f.handler, "GET", balancePath+"?job="+id, "", testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	testutil.DecodeJSON(t, rr, &job)
	if job.State != "discarded" || job.Status["stale"] != true || !reflect.DeepEqual(last, job.Status["lastSuccess"]) {
		t.Fatal("late web capture crossed live Core version fence", rr.Body.String())
	}
}

func (x *webObservationFixture) configure(t *testing.T) {
	t.Helper()
	body := webConfiguration(0)
	if x.timeoutMS != 0 {
		body["timeoutMs"] = x.timeoutMS
	}
	body["web"].(map[string]any)["parsingRule"] = syntheticWebRule
	rr := testutil.Request(t, x.f.handler, "PUT", balancePath, webBody(t, body), testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	var status map[string]any
	testutil.DecodeJSON(t, rr, &status)
	authority, ok := status["webSourceAuthority"].(string)
	if !ok || authority == "" {
		t.Fatal("missing full source/version authority", status)
	}
	x.authority.Store(authority)
}

func (x *webObservationFixture) result(t *testing.T) map[string]any {
	t.Helper()
	raw := x.f.wait(x.f.query())
	var job struct {
		Status map[string]any `json:"status"`
	}
	if err := json.Unmarshal([]byte(raw), &job); err != nil {
		t.Fatal(err)
	}
	if job.Status == nil {
		t.Fatal("no durable terminal status", raw)
	}
	return job.Status
}

func TestOfficialBalanceWebObservationSuccessPersists(t *testing.T) {
	x := newWebObservationFixture(t)
	x.configure(t)
	status := x.result(t)
	last, ok := status["lastSuccess"].(map[string]any)
	if !ok || last["amountDecimal"] != "-12345678901234567890.00000000100" || last["currency"] != "USD" || last["semantic"] != "web_account_balance" || status["stale"] != false {
		t.Fatal("exact native web observation missing", status)
	}
	when, _ := last["observedAtMs"].(float64)
	if when <= 0 || when > float64(time.Now().UnixMilli()) || last["evidenceHash"] == "" || last["webEvidence"].(map[string]any)["verification"] != "controlled_fixture" {
		t.Fatal("missing local time or explicit fixture provenance", last)
	}
	// Close/reopen temporary SQLite and reconstruct the public API. No direct
	// database query is used as an assertion or an observation injection path.
	f := x.f
	cfg := f.server.AppContext().Config
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	protector, _ := security.NewProtector([]byte("0123456789abcdef0123456789abcdef"))
	db, err := store.Open(cfg.DBPath, protector)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server := New(cfg, db, collector.NewManager(cfg, db))
	server.AppContext().OfficialBalanceService.WebReader = x.driver
	f.handler = server.Handler()
	var read map[string]any
	if err := json.Unmarshal([]byte(f.status()), &read); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(last, read["lastSuccess"]) || f.count.Load() != 0 {
		t.Fatal("reopen/cache renewed or lost the capture", read)
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if len(x.calls) != 6 || !reflect.DeepEqual(x.calls[1], []string{"session", "start", "--json", "--browser", "synthetic-edge-instance", "--no-focus"}) || !reflect.DeepEqual(x.calls[5], []string{"session", "stop", "synthetic-owned-session"}) {
		t.Fatal("wrong BrowserSkill lifecycle", x.calls)
	}
	for _, call := range x.calls {
		if strings.Contains(strings.Join(call, " "), "evaluate") {
			t.Fatal("script execution used")
		}
	}
}
