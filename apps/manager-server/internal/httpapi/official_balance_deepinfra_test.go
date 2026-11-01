package httpapi

import (
	"encoding/json"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"

	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
)

// This seam uses the real host, encrypted temporary SQLite and production TLS
// transport. Only the remote provider/Core protocol is controlled.
func TestOfficialBalanceDeepInfraSignedPrepaid(t *testing.T) {
	f := newOfficialFixture(t, "deepinfra")
	f.response.Store(`{"stripe_balance":-12.3456,"recent":0.2345,"limit":null}`)
	f.configure("")
	var job officialbalance.Job
	if err := json.Unmarshal([]byte(f.wait(f.query())), &job); err != nil {
		t.Fatal(err)
	}
	if job.Status == nil || job.Status.LastSuccess == nil {
		t.Fatalf("no successful prepaid reading: %+v", job)
	}
	got := job.Status.LastSuccess
	if got.QueryStatus != "success" || got.Semantic != "signed_prepaid_net" || got.AmountDecimal != "12.1111" || got.Currency != "USD" || len(got.Balances) != 0 || got.Available != nil {
		t.Fatalf("fabricated wallet or wrong signed major units: %+v", got)
	}
	if !job.Status.DirectQuery || job.Status.CoreQuery || job.Status.CoreGate {
		t.Fatalf("untruthful capabilities: %+v", job.Status)
	}
}

func TestOfficialBalanceDeepInfraAmbiguousNumberFails(t *testing.T) {
	f := newOfficialFixture(t, "deepinfra")
	f.response.Store(`{"stripe_balance":-1,"recent":2,"recent":0}`)
	f.configure("")
	got := f.wait(f.query())
	if !strings.Contains(got, `"queryStatus":"parse_error"`) || strings.Contains(got, `"lastSuccess"`) {
		t.Fatal("ambiguous financial response accepted:", got)
	}
}

func TestOfficialBalanceDeepInfraExactNumbers(t *testing.T) {
	f := newOfficialFixture(t, "deepinfra")
	f.configure("")
	for _, tc := range []struct{ name, body, want string }{
		{"debt", `{"stripe_balance":-1,"recent":2.5}`, "-1.5"},
		{"zeros", `{"stripe_balance":-0,"recent":0.00}`, "0"},
		{"subcent", `{"stripe_balance":-0.000000000000000000000000000001,"recent":0}`, "0.000000000000000000000000000001"},
		{"large exact", `{"stripe_balance":-12345678901234567890.000000001,"recent":0.0000000001}`, "12345678901234567890.0000000009"},
		{"exponents", `{"stripe_balance":-123456e-4,"recent":2345E-4}`, "12.1111"},
		{"integer exponent", `{"stripe_balance":-1e+3,"recent":1E2}`, "900"},
		{"cancellation", `{"stripe_balance":-1.000000000000000000000000000001,"recent":1}`, "0.000000000000000000000000000001"},
		{"not limit or grant", `{"stripe_balance":-12.3456,"recent":0.2345,"limit":999999999999999999999999999999999999999,"remaining_cents":9999999,"suspended":true}`, "12.1111"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.response.Store(tc.body)
			var job officialbalance.Job
			if json.Unmarshal([]byte(f.wait(f.query())), &job) != nil || job.Status == nil || job.Status.LastSuccess == nil || job.Status.State != "success" || job.Status.LastSuccess.AmountDecimal != tc.want {
				t.Fatalf("incorrect exact result: %+v", job)
			}
		})
	}
	// Recreate Manager on the same temporary SQLite; GET performs no billing I/O.
	n := f.count.Load()
	f.handler = New(f.server.AppContext().Config, f.db, collector.NewManager(f.server.AppContext().Config, f.db)).Handler()
	if got := f.status(); !strings.Contains(got, `"amountDecimal":"12.1111"`) || f.count.Load() != n {
		t.Fatal("persistent scalar read lost:", got)
	}
}

func TestOfficialBalanceDeepInfraFailuresRetainDebt(t *testing.T) {
	f := newOfficialFixture(t, "deepinfra")
	f.configure("")
	f.response.Store(`{"stripe_balance":-1,"recent":2.5}`)
	f.wait(f.query())
	for _, tc := range []struct{ name, body, status, code string }{
		{"401", "status:401", "auth_error", "UPSTREAM_AUTH"},
		{"403", "status:403", "auth_error", "UPSTREAM_AUTH"},
		{"429", "status:429", "rate_limited", "UPSTREAM_RATE_LIMITED"},
		{"500", "status:500", "network_error", "UPSTREAM_STATUS"},
		{"html", "<html>synthetic-query-canary</html>", "parse_error", "INVALID_BALANCE"},
		{"missing stripe", `{"recent":0}`, "parse_error", "INVALID_BALANCE"},
		{"missing recent", `{"stripe_balance":0}`, "parse_error", "INVALID_BALANCE"},
		{"old invented recent_usage", `{"stripe_balance":0,"recent_usage":0}`, "parse_error", "INVALID_BALANCE"},
		{"null", `{"stripe_balance":null,"recent":0}`, "parse_error", "INVALID_BALANCE"},
		{"string", `{"stripe_balance":"-1","recent":0}`, "parse_error", "INVALID_BALANCE"},
		{"bool", `{"stripe_balance":true,"recent":0}`, "parse_error", "INVALID_BALANCE"},
		{"array", `{"stripe_balance":[],"recent":0}`, "parse_error", "INVALID_BALANCE"},
		{"nonfinite", `{"stripe_balance":NaN,"recent":0}`, "parse_error", "INVALID_BALANCE"},
		{"infinite", `{"stripe_balance":-Infinity,"recent":0}`, "parse_error", "INVALID_BALANCE"},
		{"trailing", `{"stripe_balance":0,"recent":0} {}`, "parse_error", "INVALID_BALANCE"},
		{"duplicate", `{"stripe_balance":-1,"stripe_balance":0,"recent":0}`, "parse_error", "INVALID_BALANCE"},
		{"case", `{"Stripe_Balance":0,"recent":0}`, "parse_error", "INVALID_BALANCE"},
		{"leadingzero", `{"stripe_balance":01,"recent":0}`, "parse_error", "INVALID_BALANCE"},
		{"plus", `{"stripe_balance":+1,"recent":0}`, "parse_error", "INVALID_BALANCE"},
		{"overflow exponent", `{"stripe_balance":1e999,"recent":0}`, "parse_error", "INVALID_BALANCE"},
		{"underflow exponent", `{"stripe_balance":1e-999,"recent":0}`, "parse_error", "INVALID_BALANCE"},
		{"long exponent", `{"stripe_balance":1e0001,"recent":0}`, "parse_error", "INVALID_BALANCE"},
		{"long integer", `{"stripe_balance":` + strings.Repeat("9", 121) + `,"recent":0}`, "parse_error", "INVALID_BALANCE"},
		{"long fraction", `{"stripe_balance":0.` + strings.Repeat("1", 121) + `,"recent":0}`, "parse_error", "INVALID_BALANCE"},
		{"expanded integer", `{"stripe_balance":10e120,"recent":0}`, "parse_error", "INVALID_BALANCE"},
		{"expanded fraction", `{"stripe_balance":0.1e-120,"recent":0}`, "parse_error", "INVALID_BALANCE"},
		{"response cap", `{"stripe_balance":0,"recent":0,"padding":"` + strings.Repeat("x", 2<<20) + `"}`, "parse_error", "RESPONSE_TOO_LARGE"},
		{"redirect", "redirect", "network_error", "UPSTREAM_UNAVAILABLE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.response.Store(tc.body)
			var job officialbalance.Job
			if err := json.Unmarshal([]byte(f.wait(f.query())), &job); err != nil {
				t.Fatal(err)
			}
			if job.Status == nil || job.Status.Attempt == nil || job.Status.Attempt.QueryStatus != tc.status || job.Status.Attempt.ErrorCode != tc.code || !job.Status.Stale || job.Status.LastSuccess == nil || job.Status.LastSuccess.AmountDecimal != "-1.5" || job.Status.Attempt.AmountDecimal != "" {
				t.Fatalf("unsafe failure: %+v", job)
			}
			if strings.Contains(f.status(), "synthetic-query-canary") {
				t.Fatal("secret leaked")
			}
		})
	}
}

func TestOfficialBalanceDeepInfraExactRoutePolicy(t *testing.T) {
	f := newOfficialFixture(t, "deepinfra")
	for _, adapter := range []string{"deepinfra", "deepseek"} {
		paths := []string{"/v1/billing/recent_usage", "/payment/checklist/", "/payment/checklist?x=1", "/payment/checklist?", "/payment/checklist#", "/payment/%63hecklist", "/payment/checklist/../checklist", "/user/balance"}
		if adapter == "deepseek" {
			paths = []string{"/payment/checklist"}
		}
		for _, path := range paths {
			body := `{` + balanceBinding + `,"version":0,"adapter":"` + adapter + `","endpoint":"` + f.upstream.URL + path + `","secret":"synthetic-query-canary"}`
			testutil.RequireStatus(t, testutil.Request(t, f.handler, "PUT", balancePath, body, testutil.AdminKey), 400)
		}
	}
	for _, endpoint := range []string{"http://api.deepinfra.com/payment/checklist", "https://user:pass@api.deepinfra.com/payment/checklist", "https://168.63.129.16/payment/checklist"} {
		body := `{` + balanceBinding + `,"version":0,"adapter":"deepinfra","endpoint":"` + endpoint + `","privateTargets":["168.63.129.16"],"secret":"synthetic-query-canary"}`
		testutil.RequireStatus(t, testutil.Request(t, f.handler, "PUT", balancePath, body, testutil.AdminKey), 400)
	}
	if f.count.Load() != 0 {
		t.Fatal("invalid config sent credentials")
	}
	body := `{` + balanceBinding + `,"version":0,"adapter":"deepinfra","secret":""}`
	rr := testutil.Request(t, f.handler, "PUT", balancePath, body, testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	if !strings.Contains(rr.Body.String(), `"endpoint":"https://api.deepinfra.com/payment/checklist"`) || !strings.Contains(rr.Body.String(), `"managerDirectQuery":true`) || !strings.Contains(rr.Body.String(), `"state":"needs_configuration"`) {
		t.Fatal(rr.Body.String())
	}
}

func TestOfficialBalanceDeepInfraConfigAndOldResult(t *testing.T) {
	f := newOfficialFixture(t, "deepinfra")
	f.configure("")
	for _, method := range []string{"GET", "PUT", "POST"} {
		testutil.RequireStatus(t, testutil.Request(t, f.handler, method, balancePath, `{`+balanceBinding+`}`, ""), 401)
	}
	body := `{` + balanceBinding + `,"version":1,"adapter":"deepinfra","endpoint":"` + f.upstream.URL + f.path + `","privateTargets":["127.0.0.1"],"secret":""}`
	rr := testutil.Request(t, f.handler, "PUT", balancePath, body, testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	if !strings.Contains(rr.Body.String(), `"configured":true`) {
		t.Fatal(rr.Body.String())
	}
	testutil.RequireStatus(t, testutil.Request(t, f.handler, "PUT", balancePath, body, testutil.AdminKey), 409)
	// Explicit clear is distinct from empty-preserve.
	clear := strings.Replace(body, `"version":1`, `"version":2`, 1)
	clear = strings.Replace(clear, `"secret":""`, `"clearSecret":true`, 1)
	rr = testutil.Request(t, f.handler, "PUT", balancePath, clear, testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	if !strings.Contains(rr.Body.String(), `"configured":false`) {
		t.Fatal(rr.Body.String())
	}
	// Restore, then block the actual TLS response while changing source config.
	body = strings.Replace(body, `"version":1`, `"version":3`, 1)
	body = strings.Replace(body, `"secret":""`, `"secret":"synthetic-query-canary"`, 1)
	testutil.RequireStatus(t, testutil.Request(t, f.handler, "PUT", balancePath, body, testutil.AdminKey), 200)
	f.response.Store(`{"stripe_balance":-99,"recent":0}`)
	f.delay.Store(150)
	rr = testutil.Request(t, f.handler, "POST", balancePath, `{`+balanceBinding+`,"version":4}`, testutil.AdminKey)
	testutil.RequireStatus(t, rr, 202)
	var job officialbalance.Job
	json.Unmarshal(rr.Body.Bytes(), &job)
	until := time.Now().Add(time.Second)
	for f.count.Load() == 0 && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	if f.count.Load() == 0 {
		t.Fatal("TLS request did not enter")
	}
	body = strings.Replace(body, `"version":3`, `"version":4`, 1)
	body = strings.Replace(body, f.upstream.URL+f.path, "https://api.deepinfra.com/payment/checklist", 1)
	body = strings.Replace(body, `"secret":"synthetic-query-canary"`, `"secret":""`, 1)
	rr = testutil.Request(t, f.handler, "PUT", balancePath, body, testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	if !strings.Contains(rr.Body.String(), `"configured":false`) {
		t.Fatal("secret carried to changed endpoint")
	}
	time.Sleep(200 * time.Millisecond)
	testutil.RequireStatus(t, testutil.Request(t, f.handler, "GET", balancePath+"?job="+job.ID, "", testutil.AdminKey), 409)
	if strings.Contains(f.status(), "lastSuccess") {
		t.Fatal("old result survived source change")
	}
}

func TestOfficialBalanceDeepInfraUIFixture(t *testing.T) {
	dir := os.Getenv("DEEPINFRA_BALANCE_UI_FIXTURE")
	if dir == "" {
		t.Skip("opt-in synthetic DeepInfra UI fixture")
	}
	f := newOfficialFixture(t, "deepinfra")
	binding := fullOfficialUIBinding(t, f)
	f.response.Store(`{"stripe_balance":-1,"recent":2.5}`)
	manager := officialBrowserServer(t, f.handler)
	defer manager.Close()
	control := officialBrowserServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var state struct{ Response string }
		if r.Method != "PUT" || json.NewDecoder(r.Body).Decode(&state) != nil {
			w.WriteHeader(400)
			return
		}
		f.response.Store(state.Response)
		w.WriteHeader(204)
	}))
	defer control.Close()

	data, _ := json.Marshal(map[string]any{"base": manager.URL, "managementKey": testutil.AdminKey, "binding": binding, "endpoint": f.upstream.URL + f.path, "control": control.URL})
	if err := os.WriteFile(filepath.Join(dir, "fixture.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2400; i++ {
		if _, err := os.Stat(filepath.Join(dir, "stop")); err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("UI fixture not stopped within 240 seconds")
}

func fullOfficialUIBinding(t *testing.T, f *officialFixture) officialbalance.Binding {
	t.Helper()
	var snapshot rc.Snapshot
	if err := json.Unmarshal([]byte(testSnapshotJSON), &snapshot); err != nil {
		t.Fatal(err)
	}
	provider := snapshot.Resources[0]
	provider.Kind = "provider"
	provider.Family = "openai-compatibility"
	provider.MembershipVersion = 1
	member := provider
	member.Kind = "credential"
	member.ResourceID = "66666666-6666-4666-8666-666666666666"
	member.Incarnation = "77777777-7777-4777-8777-777777777777"
	member.ParentID = provider.ResourceID
	provider.Members = []string{member.ResourceID}
	snapshot.Resources = []rc.Row{provider, member}
	issued, _ := json.Marshal(snapshot)
	f.coreSnapshot.Store(string(issued))

	return officialbalance.Binding{Scope: snapshot.Scope, Targets: []rc.Target{provider.Target, member.Target}}
}

// Windows ephemeral ports can include Fetch-standard forbidden ports (e.g.
// 3659). Allocate browser-facing synthetic helpers in an unblocked high range;
// never disable Fetch port safety or alter production transport for the fixture.
func officialBrowserServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	for i := 0; i < 100; i++ {
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", 40000+rand.IntN(20000)))
		if err != nil {
			continue
		}
		server := httptest.NewUnstartedServer(handler)
		server.Listener.Close()
		server.Listener = listener
		server.Start()
		return server
	}
	t.Fatal("cannot allocate browser-safe fixture port")
	return nil
}
