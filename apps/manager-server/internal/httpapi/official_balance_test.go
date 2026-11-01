package httpapi

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/app"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const balancePath = "/usage-service/official-balance"
const balanceBinding = `"scope":{` + testScopeJSON + `},"targets":[{` + testTargetJSON + `}]`

func balanceCore(t *testing.T) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-core-key" {
			w.WriteHeader(401)
			return
		}
		if r.URL.Path == "/v8/management/resource-capabilities" {
			w.Write([]byte(testCapsJSON))
			return
		}
		w.Write([]byte(testSnapshotJSON))
	}))
	t.Cleanup(s.Close)
	return s
}
func TestOfficialBalanceConfigurationCAS(t *testing.T) {
	core := balanceCore(t)
	cfg := testutil.NewConfig(t)
	protector, _ := security.NewProtector([]byte("0123456789abcdef0123456789abcdef"))
	db, err := store.Open(cfg.DBPath, protector)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	testutil.EnsureAdminCredential(t, db)
	if err := db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: core.URL, ManagementKey: "synthetic-core-key"}}); err != nil {
		t.Fatal(err)
	}
	h := New(cfg, db, collector.NewManager(cfg, db)).Handler()
	body := `{` + balanceBinding + `,"version":0,"adapter":"deepseek","endpoint":"https://api.deepseek.com/user/balance","secret":"synthetic-query-canary"}`
	rr := testutil.Request(t, h, "PUT", balancePath, body, testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	testutil.RequireStatus(t, testutil.Request(t, h, "PUT", balancePath, body, testutil.AdminKey), 409)
}

func TestOfficialBalanceExactQueryPersists(t *testing.T) {
	core := balanceCore(t)
	cfg := testutil.NewConfig(t)
	protector, _ := security.NewProtector([]byte("0123456789abcdef0123456789abcdef"))
	db, err := store.Open(cfg.DBPath, protector)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	testutil.EnsureAdminCredential(t, db)
	db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: core.URL, ManagementKey: "synthetic-core-key"}})
	var calls atomic.Int64
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.Path != "/user/balance" || r.Header.Get("Authorization") != "Bearer synthetic-query-canary" {
			t.Error("invalid protocol request")
		}
		w.Write([]byte(`{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"-12345678901234567890.000000001","granted_balance":"0.000000001","topped_up_balance":"-12345678901234567890.000000002"},{"currency":"USD","total_balance":"1.00","granted_balance":"0.00","topped_up_balance":"1.00"}]}`))
	}))
	defer upstream.Close()
	server := New(cfg, db, collector.NewManager(cfg, db))
	roots := x509.NewCertPool()
	roots.AddCert(upstream.Certificate())
	server.AppContext().OfficialBalanceService.Querier = balance.NewWithNetwork(balance.NetworkOptions{RootCAs: roots})
	h := server.Handler()
	body := `{` + balanceBinding + `,"version":0,"adapter":"deepseek","endpoint":"` + upstream.URL + `/user/balance","privateTargets":["127.0.0.1"],"secret":"synthetic-query-canary"}`
	testutil.RequireStatus(t, testutil.Request(t, h, "PUT", balancePath, body, testutil.AdminKey), 200)
	rr := testutil.Request(t, h, "POST", balancePath, `{`+balanceBinding+`,"version":1}`, testutil.AdminKey)
	testutil.RequireStatus(t, rr, 202)
	var job struct {
		ID string `json:"id"`
	}
	testutil.DecodeJSON(t, rr, &job)
	for i := 0; i < 200; i++ {
		rr = testutil.Request(t, h, "GET", balancePath+"?job="+job.ID, "", testutil.AdminKey)
		if strings.Contains(rr.Body.String(), `"state":"complete"`) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	var result struct {
		Status struct {
			LastSuccess struct {
				Balances []struct{ Currency, Total, Granted, ToppedUp string }
			} `json:"lastSuccess"`
		} `json:"status"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	rows := result.Status.LastSuccess.Balances
	if len(rows) != 2 || rows[0].Currency != "CNY" || rows[0].Total != "-12345678901234567890.000000001" || rows[1].Total != "1.00" {
		t.Fatalf("exact multi-currency wallet missing: %s", rr.Body.String())
	}
	// A new handler uses the same real SQLite, not an in-memory last-success.
	restarted := New(cfg, db, collector.NewManager(cfg, db)).Handler()
	binding := `{` + balanceBinding + `}`
	req := httptest.NewRequest("GET", balancePath, nil)
	q := req.URL.Query()
	q.Set("binding", binding)
	req.URL.RawQuery = q.Encode()
	req.Header.Set("Authorization", "Bearer "+testutil.AdminKey)
	read := httptest.NewRecorder()
	restarted.ServeHTTP(read, req)
	if !strings.Contains(read.Body.String(), "-12345678901234567890.000000001") || calls.Load() != 1 || strings.Contains(read.Body.String(), "synthetic-query-canary") {
		t.Fatalf("unsafe or lost persisted read: %s calls=%d", read.Body.String(), calls.Load())
	}
}

// Fixtures vary only external Core/HTTP/TLS dependencies. All operations cross
// the authenticated production controller, host, transport and real SQLite.
type officialFixture struct {
	t            *testing.T
	server       *Server
	db           *store.Store
	cfgPath      string
	adapter      string
	path         string
	upstream     *httptest.Server
	handler      http.Handler
	count        atomic.Int64
	response     atomic.Value
	delay        atomic.Int64
	coreSnapshot atomic.Value
	coreCaps     atomic.Value
	coreDelay    atomic.Int64
}

const goodWallet = `{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"-0.000000001","granted_balance":"0.00","topped_up_balance":"-0.000000001"}]}`

func newOfficialFixture(t *testing.T, adapters ...string) *officialFixture {
	f := &officialFixture{t: t, adapter: "deepseek", path: "/user/balance"}
	if len(adapters) > 0 && adapters[0] == "deepinfra" {
		f.adapter = "deepinfra"
		f.path = "/payment/checklist"
	}
	f.response.Store(goodWallet)
	f.coreSnapshot.Store(testSnapshotJSON)
	f.coreCaps.Store(testCapsJSON)
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if delay := f.coreDelay.Load(); delay > 0 {
			select {
			case <-time.After(time.Duration(delay) * time.Millisecond):
			case <-r.Context().Done():
				return
			}
		}
		if r.URL.Path == "/v8/management/resource-capabilities" {
			fmt.Fprint(w, f.coreCaps.Load())
			return
		}
		fmt.Fprint(w, f.coreSnapshot.Load())
	}))
	t.Cleanup(core.Close)
	f.upstream = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.count.Add(1)
		if r.Method != "GET" || r.URL.Path != f.path || r.Header.Get("Authorization") != "Bearer synthetic-query-canary" {
			t.Error("protocol or secret lost")
		}
		if d := f.delay.Load(); d > 0 {
			select {
			case <-time.After(time.Duration(d) * time.Millisecond):
			case <-r.Context().Done():
				return
			}
		}
		body := f.response.Load().(string)
		if body == "expire_revalidation" {
			f.coreDelay.Store(80)
			w.WriteHeader(401)
			return
		}
		if strings.HasPrefix(body, "status:") {
			var status int
			fmt.Sscanf(body, "status:%d", &status)
			w.WriteHeader(status)
			w.Write([]byte("synthetic-query-canary"))
			return
		}
		if strings.HasPrefix(body, "redirectto:") {
			w.Header().Set("Location", strings.TrimPrefix(body, "redirectto:"))
			w.WriteHeader(302)
			return
		}
		if body == "redirect" {
			w.Header().Set("Location", core.URL+"/credential-leak")
			w.WriteHeader(302)
			return
		}
		fmt.Fprint(w, body)
	}))
	t.Cleanup(f.upstream.Close)
	cfg := testutil.NewConfig(t)
	f.cfgPath = cfg.DBPath
	protector, _ := security.NewProtector([]byte("0123456789abcdef0123456789abcdef"))
	db, err := store.Open(cfg.DBPath, protector)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	f.db = db
	testutil.EnsureAdminCredential(t, db)
	if err := db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: core.URL, ManagementKey: "synthetic-core-key"}}); err != nil {
		t.Fatal(err)
	}
	f.server = New(cfg, db, collector.NewManager(cfg, db))
	roots := x509.NewCertPool()
	roots.AddCert(f.upstream.Certificate())
	f.server.AppContext().OfficialBalanceService.Querier = balance.NewWithNetwork(balance.NetworkOptions{RootCAs: roots})
	f.handler = f.server.Handler()
	return f
}
func (f *officialFixture) configure(extra string) {
	f.t.Helper()
	body := `{` + balanceBinding + `,"version":0,"adapter":"` + f.adapter + `","endpoint":"` + f.upstream.URL + f.path + `","privateTargets":["127.0.0.1"],"secret":"synthetic-query-canary"` + extra + `}`
	testutil.RequireStatus(f.t, testutil.Request(f.t, f.handler, "PUT", balancePath, body, testutil.AdminKey), 200)
}
func (f *officialFixture) query() string {
	f.t.Helper()
	rr := testutil.Request(f.t, f.handler, "POST", balancePath, `{`+balanceBinding+`,"version":1}`, testutil.AdminKey)
	testutil.RequireStatus(f.t, rr, 202)
	var job struct{ ID string }
	testutil.DecodeJSON(f.t, rr, &job)
	return job.ID
}
func (f *officialFixture) wait(id string) string {
	f.t.Helper()
	for i := 0; i < 250; i++ {
		rr := testutil.Request(f.t, f.handler, "GET", balancePath+"?job="+id, "", testutil.AdminKey)
		if !strings.Contains(rr.Body.String(), `"state":"running"`) {
			return rr.Body.String()
		}
		time.Sleep(5 * time.Millisecond)
	}
	f.t.Fatal("query did not terminate")
	return ""
}
func (f *officialFixture) status() string {
	f.t.Helper()
	rr := testutil.Request(f.t, f.handler, "GET", balancePath+"?binding="+url.QueryEscape(`{`+balanceBinding+`}`), "", testutil.AdminKey)
	testutil.RequireStatus(f.t, rr, 200)
	return rr.Body.String()
}
func TestOfficialBalanceTimeoutRetainsLastSuccess(t *testing.T) {
	f := newOfficialFixture(t)
	f.configure(`,"timeoutMs":50`)
	if got := f.wait(f.query()); !strings.Contains(got, `"queryStatus":"success"`) {
		t.Fatal(got)
	}
	f.delay.Store(200)
	got := f.wait(f.query())
	if !strings.Contains(got, `"queryStatus":"network_error"`) || !strings.Contains(got, `"lastSuccess"`) || !strings.Contains(got, `"stale":true`) {
		t.Fatalf("failed attempt lost or replaced last success: %s", got)
	}
}

func TestOfficialBalanceProtocolFailuresRetainExactSuccess(t *testing.T) {
	f := newOfficialFixture(t)
	f.configure("")
	if got := f.wait(f.query()); !strings.Contains(got, `"queryStatus":"success"`) {
		t.Fatal(got)
	}
	for _, tc := range []struct{ name, body, status string }{
		{"auth", "status:401", "auth_error"}, {"forbidden", "status:403", "auth_error"}, {"limited", "status:429", "rate_limited"},
		{"html", "<html>synthetic-query-canary</html>", "parse_error"},
		{"missing", `{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"0"}]}`, "parse_error"},
		{"null", `{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":null,"granted_balance":"0","topped_up_balance":"0"}]}`, "parse_error"},
		{"nan", strings.Replace(goodWallet, "-0.000000001", "NaN", 1), "parse_error"},
		{"infinite", strings.Replace(goodWallet, "-0.000000001", "Infinity", 1), "parse_error"},
		{"number", strings.Replace(goodWallet, `"-0.000000001"`, `1.25`, 1), "parse_error"},
		{"too large", strings.TrimSuffix(goodWallet, "}") + `,"padding":"` + strings.Repeat("x", 2<<20) + `"}`, "parse_error"},
		{"redirect", "redirect", "network_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.response.Store(tc.body)
			got := f.wait(f.query())
			if tc.name == "too large" && !strings.Contains(got, "RESPONSE_TOO_LARGE") {
				t.Fatal("2MiB response limit not enforced", got)
			}
			if !strings.Contains(got, `"queryStatus":"`+tc.status+`"`) || !strings.Contains(got, `"stale":true`) || !strings.Contains(got, `"total":"-0.000000001"`) || strings.Contains(got, "synthetic-query-canary") {
				t.Fatalf("unsafe failure semantics: %s", got)
			}
		})
	}
	before := f.count.Load()
	f.status()
	f.status()
	if f.count.Load() != before {
		t.Fatal("GET status queried platform")
	}
}
func TestOfficialBalanceSecretsAuthAndCAS(t *testing.T) {
	f := newOfficialFixture(t)
	f.configure("")
	for _, method := range []string{"GET", "PUT", "POST"} {
		rr := testutil.Request(t, f.handler, method, balancePath, `{`+balanceBinding+`}`, "")
		if rr.Code != 401 {
			t.Fatalf("unauthenticated %s: %d", method, rr.Code)
		}
	}
	// Empty input preserves the backend-only encrypted credential; explicit clear removes it.
	body := `{` + balanceBinding + `,"version":1,"adapter":"deepseek","endpoint":"` + f.upstream.URL + `/user/balance","privateTargets":["127.0.0.1"],"secret":""}`
	rr := testutil.Request(t, f.handler, "PUT", balancePath, body, testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	if !strings.Contains(rr.Body.String(), `"configured":true`) || strings.Contains(rr.Body.String(), "synthetic-query-canary") {
		t.Fatal(rr.Body.String())
	}
	testutil.RequireStatus(t, testutil.Request(t, f.handler, "PUT", balancePath, body, testutil.AdminKey), 409)
	body = strings.Replace(body, `"version":1`, `"version":2`, 1)
	body = strings.Replace(body, `"secret":""`, `"clearSecret":true`, 1)
	rr = testutil.Request(t, f.handler, "PUT", balancePath, body, testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	if !strings.Contains(rr.Body.String(), `"configured":false`) {
		t.Fatal(rr.Body.String())
	}
}
func TestOfficialBalanceSingleFlightAndSourceChangeFence(t *testing.T) {
	f := newOfficialFixture(t)
	f.configure("")
	f.delay.Store(150)
	id := f.query()
	second := f.query()
	if id != second {
		t.Fatal("duplicate manual refresh not coalesced")
	}
	body := `{` + balanceBinding + `,"version":1,"adapter":"deepseek","endpoint":"https://api.deepseek.com/user/balance","secret":""}`
	rr := testutil.Request(t, f.handler, "PUT", balancePath, body, testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	if !strings.Contains(rr.Body.String(), `"configured":false`) {
		t.Fatal("credential carried to changed source")
	}
	time.Sleep(200 * time.Millisecond)
	rr = testutil.Request(t, f.handler, "GET", balancePath+"?job="+id, "", testutil.AdminKey)
	testutil.RequireStatus(t, rr, 409)
	if got := f.status(); strings.Contains(got, "lastSuccess") {
		t.Fatal("late completion overwrote changed source")
	}
	if f.count.Load() > 1 {
		t.Fatal("duplicate upstream query")
	}
}
func TestOfficialBalanceDynamicCoreIdentityFence(t *testing.T) {
	f := newOfficialFixture(t)
	f.configure("")
	f.delay.Store(120)
	id := f.query()
	// Producer changes credential version without changing labels/row position.
	f.coreSnapshot.Store(strings.Replace(testSnapshotJSON, `"credential_version":3`, `"credential_version":4`, 1))
	time.Sleep(160 * time.Millisecond)
	rr := testutil.Request(t, f.handler, "GET", balancePath+"?job="+id, "", testutil.AdminKey)
	testutil.RequireStatus(t, rr, 409)
	rr = testutil.Request(t, f.handler, "GET", balancePath+"?binding="+url.QueryEscape(`{`+balanceBinding+`}`), "", testutil.AdminKey)
	testutil.RequireStatus(t, rr, 409)
}

func TestOfficialBalanceMetadataCannotBePermitted(t *testing.T) {
	f := newOfficialFixture(t)
	body := `{` + balanceBinding + `,"version":0,"adapter":"deepseek","endpoint":"https://168.63.129.16/user/balance","privateTargets":["168.63.129.16"],"secret":"synthetic-query-canary"}`
	testutil.RequireStatus(t, testutil.Request(t, f.handler, "PUT", balancePath, body, testutil.AdminKey), 400)
}

func TestOfficialBalanceProductionTransport(t *testing.T) {
	testOfficialBalanceProductionTransport(t, "deepseek")
}
func TestOfficialBalanceDeepInfraProductionTransport(t *testing.T) {
	testOfficialBalanceProductionTransport(t, "deepinfra")
}
func testOfficialBalanceProductionTransport(t *testing.T, adapter string) {
	for _, mode := range []string{"pin_numeric", "mixed_dns", "unpermitted_private", "untrusted_certificate", "plaintext_refused", "metadata_dns", "metadata_ipv6", "mapped_metadata"} {
		t.Run(mode, func(t *testing.T) {
			f := newOfficialFixture(t, adapter)
			endpoint := f.upstream.URL + f.path
			if adapter == "deepinfra" {
				f.response.Store(`{"stripe_balance":-1,"recent":2.5}`)
			}
			permits := `,"privateTargets":["127.0.0.1"]`
			roots := x509.NewCertPool()
			roots.AddCert(f.upstream.Certificate())
			var lookups, dials atomic.Int64
			n := balance.NetworkOptions{RootCAs: roots}
			if mode == "untrusted_certificate" {
				n.RootCAs = x509.NewCertPool()
			}
			if mode == "unpermitted_private" {
				permits = ""
			}
			if mode == "plaintext_refused" {
				endpoint = strings.Replace(endpoint, "https:", "http:", 1)
			}
			if mode == "pin_numeric" || mode == "mixed_dns" || strings.Contains(mode, "metadata") {
				endpoint = strings.Replace(endpoint, "127.0.0.1", "example.com", 1)
				n.LookupIP = func(context.Context, string) ([]net.IPAddr, error) {
					lookups.Add(1)
					if mode == "metadata_dns" {
						return []net.IPAddr{{IP: net.ParseIP("169.254.169.254")}}, nil
					}
					if mode == "metadata_ipv6" {
						return []net.IPAddr{{IP: net.ParseIP("fd00:ec2::254")}}, nil
					}
					if mode == "mapped_metadata" {
						return []net.IPAddr{{IP: net.ParseIP("::ffff:169.254.169.254")}}, nil
					}
					if mode == "mixed_dns" {
						return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}, {IP: net.ParseIP("169.254.169.254")}}, nil
					}
					if lookups.Load() > 1 {
						return []net.IPAddr{{IP: net.ParseIP("169.254.169.254")}}, nil
					}
					return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
				}
				n.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
					dials.Add(1)
					host, _, _ := net.SplitHostPort(address)
					if host != "127.0.0.1" {
						t.Errorf("dial was not pinned: %s", address)
					}
					return (&net.Dialer{}).DialContext(ctx, network, address)
				}
			}
			f.server.AppContext().OfficialBalanceService.Querier = balance.NewWithNetwork(n)
			body := `{` + balanceBinding + `,"version":0,"adapter":"` + adapter + `","endpoint":"` + endpoint + `"` + permits + `,"secret":"synthetic-query-canary"}`
			rr := testutil.Request(t, f.handler, "PUT", balancePath, body, testutil.AdminKey)
			if mode == "plaintext_refused" {
				testutil.RequireStatus(t, rr, 400)
				return
			}
			testutil.RequireStatus(t, rr, 200)
			got := f.wait(f.query())
			if mode == "pin_numeric" {
				if !strings.Contains(got, `"queryStatus":"success"`) || lookups.Load() != 1 || dials.Load() != 1 || f.count.Load() != 1 {
					t.Fatal("pinning did not use verified TLS numeric destination", got)
				}
				return
			}
			if !strings.Contains(got, `"queryStatus":"network_error"`) || strings.Contains(got, "synthetic-query-canary") || f.count.Load() != 0 || dials.Load() != 0 {
				t.Fatalf("unsafe transport: %s calls=%d dials=%d", got, f.count.Load(), dials.Load())
			}
		})
	}
}

func TestOfficialBalanceRequiresFullProjectionTargets(t *testing.T) {
	f := newOfficialFixture(t)
	var snapshot map[string]any
	json.Unmarshal([]byte(testSnapshotJSON), &snapshot)
	a := snapshot["resources"].([]any)[0].(map[string]any)
	var peer map[string]any
	raw, _ := json.Marshal(a)
	json.Unmarshal(raw, &peer)
	peer["resource_id"] = "66666666-6666-4666-8666-666666666666"
	peer["incarnation"] = "77777777-7777-4777-8777-777777777777"
	var target map[string]any
	json.Unmarshal([]byte(`{`+testTargetJSON+`}`), &target)
	other := map[string]any{}
	for k, v := range target {
		other[k] = v
	}
	other["resource_id"] = peer["resource_id"]
	other["incarnation"] = peer["incarnation"]
	a["source_bindings"] = []any{other}
	peer["source_bindings"] = []any{target}
	a["binding_state"] = "verified_projection"
	peer["binding_state"] = "verified_projection"
	snapshot["resources"] = []any{a, peer}
	raw, _ = json.Marshal(snapshot)
	f.coreSnapshot.Store(string(raw))
	body := `{` + balanceBinding + `,"version":0,"adapter":"deepseek","endpoint":"https://api.deepseek.com/user/balance","secret":"synthetic-query-canary"}`
	testutil.RequireStatus(t, testutil.Request(t, f.handler, "PUT", balancePath, body, testutil.AdminKey), 409)
}

// Opt-in, isolated live fixture for UI -> Manager -> SQLite -> real TLS tests.
// No runtime config/account access. A stop file ends this process cleanly.
func TestOfficialBalanceUIFixture(t *testing.T) {
	dir := os.Getenv("OFFICIAL_BALANCE_UI_FIXTURE")
	if dir == "" {
		t.Skip("opt-in synthetic UI fixture")
	}
	f := newOfficialFixture(t)
	manager := officialBrowserServer(t, f.handler)
	defer manager.Close()
	control := officialBrowserServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" {
			w.WriteHeader(405)
			return
		}
		var state struct{ Response string }
		if json.NewDecoder(r.Body).Decode(&state) != nil {
			w.WriteHeader(400)
			return
		}
		f.response.Store(state.Response)
		w.WriteHeader(204)
	}))
	defer control.Close()
	binding := fullOfficialUIBinding(t, f)
	data, _ := json.Marshal(map[string]any{"base": manager.URL, "managementKey": testutil.AdminKey, "binding": binding, "endpoint": f.upstream.URL + "/user/balance", "control": control.URL})
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

func TestOfficialBalanceTranslatedMetadataNeverDials(t *testing.T) {
	f := newOfficialFixture(t)
	f.configure("")
	var dials atomic.Int64
	f.server.AppContext().OfficialBalanceService.Querier = balance.NewWithNetwork(balance.NetworkOptions{
		LookupIP: func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("64:ff9b::a9fe:a9fe")}}, nil
		},
		// This dependency cannot connect: a RED must never become a live SSRF probe.
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			dials.Add(1)
			return nil, errors.New("synthetic dial refused")
		},
	})
	got := f.wait(f.query())
	if dials.Load() != 0 || f.count.Load() != 0 || !strings.Contains(got, `"queryStatus":"network_error"`) {
		t.Fatalf("translated metadata reached dial: dials=%d upstream=%d result=%s", dials.Load(), f.count.Load(), got)
	}
}

func TestOfficialBalanceSpecialAddressClassesNeverDial(t *testing.T) {
	for _, address := range []string{"64:ff9b::a9fe:a9fe", "64:ff9b::808:808", "64:ff9b:1::a9fe:a9fe", "fec0::1", "2002:a9fe:a9fe::1", "2001:0:4136:e378:8000:63bf:5651:5651", "::169.254.169.254", "::ffff:169.254.169.254", "2001:4860:1234:1234:0:5efe:a9fe:a9fe"} {
		t.Run(address, func(t *testing.T) {
			f := newOfficialFixture(t)
			f.configure("")
			var dials atomic.Int64
			f.server.AppContext().OfficialBalanceService.Querier = balance.NewWithNetwork(balance.NetworkOptions{
				LookupIP: func(context.Context, string) ([]net.IPAddr, error) {
					return []net.IPAddr{{IP: net.ParseIP(address)}}, nil
				},
				DialContext: func(context.Context, string, string) (net.Conn, error) {
					dials.Add(1)
					return nil, errors.New("synthetic dial refused")
				},
			})
			got := f.wait(f.query())
			if dials.Load() != 0 || f.count.Load() != 0 || !strings.Contains(got, `"queryStatus":"network_error"`) {
				t.Fatalf("special address escaped ACL: dials=%d upstream=%d %s", dials.Load(), f.count.Load(), got)
			}
			// An exact administrator permit cannot override an unsupported transition.
			body := `{` + balanceBinding + `,"version":1,"adapter":"deepseek","endpoint":"` + f.upstream.URL + `/user/balance","privateTargets":["` + address + `"]}`
			testutil.RequireStatus(t, testutil.Request(t, f.handler, "PUT", balancePath, body, testutil.AdminKey), 400)
		})
	}
}
func TestOfficialBalanceMappedPrivatePermitStillExact(t *testing.T) {
	f := newOfficialFixture(t)
	f.configure("")
	roots := x509.NewCertPool()
	roots.AddCert(f.upstream.Certificate())
	f.server.AppContext().OfficialBalanceService.Querier = balance.NewWithNetwork(balance.NetworkOptions{RootCAs: roots, LookupIP: func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("::ffff:127.0.0.1")}}, nil
	}})
	if got := f.wait(f.query()); !strings.Contains(got, `"queryStatus":"success"`) || f.count.Load() != 1 {
		t.Fatal("mapped IPv4 lost exact permit semantics", got)
	}
}

func TestOfficialBalanceVerifiedRebindPreservesDurableSecret(t *testing.T) {
	f := newOfficialFixture(t)
	f.configure("")
	if got := f.wait(f.query()); !strings.Contains(got, `"queryStatus":"success"`) {
		t.Fatal(got)
	}
	next := func(s string) string {
		return strings.ReplaceAll(strings.ReplaceAll(s, "33333333-3333-4333-8333-333333333333", "88888888-8888-4888-8888-888888888888"), `"snapshot_revision":7`, `"snapshot_revision":8`)
	}
	f.coreCaps.Store(next(testCapsJSON))
	f.coreSnapshot.Store(next(testSnapshotJSON))
	fresh := next(`{` + balanceBinding + `}`)
	rr := testutil.Request(t, f.handler, "GET", balancePath+"?binding="+url.QueryEscape(fresh), "", testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	if !strings.Contains(rr.Body.String(), `"configured":true`) {
		t.Fatalf("unchanged durable authority lost source credential after restart: %s", rr.Body.String())
	}
	old := testutil.Request(t, f.handler, "GET", balancePath+"?binding="+url.QueryEscape(`{`+balanceBinding+`}`), "", testutil.AdminKey)
	testutil.RequireStatus(t, old, 409)
	rr = testutil.Request(t, f.handler, "POST", balancePath, strings.TrimSuffix(fresh, "}")+`,"version":1}`, testutil.AdminKey)
	testutil.RequireStatus(t, rr, 202)
	var job struct{ ID string }
	testutil.DecodeJSON(t, rr, &job)
	got := f.wait(job.ID)
	if !strings.Contains(got, `"queryStatus":"success"`) || f.count.Load() != 2 {
		t.Fatalf("rebound source did not query with preserved encrypted secret: %s calls=%d", got, f.count.Load())
	}
}

func TestOfficialBalanceEncryptedStorageNeedsOriginalDataKey(t *testing.T) {
	f := newOfficialFixture(t)
	f.configure("")
	// Isolate the new secret store: no encrypted Core connection remains to make
	// the pre-existing connection guard accidentally satisfy this test.
	if err := f.db.SaveManagerConfig(context.Background(), store.ManagerConfig{}); err != nil {
		t.Fatal(err)
	}
	cfg := f.server.AppContext().Config
	cfg.DataKeyPath = filepath.Join(filepath.Dir(cfg.DBPath), "absent-balance-data.key")
	candidate, err := app.New(context.Background(), cfg, app.Options{})
	if err == nil {
		candidate.Store.Close()
		t.Fatal("startup recreated an unrelated key over encrypted balance credentials")
	}
	if _, err := os.Stat(cfg.DataKeyPath); !os.IsNotExist(err) {
		t.Fatal("startup wrote a replacement data key")
	}
	// Raw synthetic SQLite artifacts must contain no cleartext query secret.
	for _, name := range []string{cfg.DBPath, cfg.DBPath + "-wal"} {
		raw, e := os.ReadFile(name)
		if e == nil && strings.Contains(string(raw), "synthetic-query-canary") {
			t.Fatal("query credential is not encrypted at rest")
		}
	}
}

func TestOfficialBalanceRebindRejectsPreBootCompletion(t *testing.T) {
	f := newOfficialFixture(t)
	f.configure("")
	f.delay.Store(150)
	oldID := f.query()
	for i := 0; i < 100 && f.count.Load() == 0; i++ {
		time.Sleep(time.Millisecond)
	}
	fresh := func(s string) string {
		return strings.ReplaceAll(strings.ReplaceAll(s, "33333333-3333-4333-8333-333333333333", "88888888-8888-4888-8888-888888888888"), `"snapshot_revision":7`, `"snapshot_revision":8`)
	}
	f.coreCaps.Store(fresh(testCapsJSON))
	f.coreSnapshot.Store(fresh(testSnapshotJSON))
	f.delay.Store(0)
	binding := fresh(`{` + balanceBinding + `}`)
	rr := testutil.Request(t, f.handler, "POST", balancePath, strings.TrimSuffix(binding, "}")+`,"version":1}`, testutil.AdminKey)
	testutil.RequireStatus(t, rr, 202)
	var job struct{ ID string }
	testutil.DecodeJSON(t, rr, &job)
	if job.ID == oldID {
		t.Fatal("new boot joined preboot single-flight")
	}
	if got := f.wait(job.ID); !strings.Contains(got, `"queryStatus":"success"`) {
		t.Fatal(got)
	}
	time.Sleep(180 * time.Millisecond)
	testutil.RequireStatus(t, testutil.Request(t, f.handler, "GET", balancePath+"?job="+oldID, "", testutil.AdminKey), 409)
	rr = testutil.Request(t, f.handler, "GET", balancePath+"?binding="+url.QueryEscape(binding), "", testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	var status struct {
		LastSuccess struct {
			Binding struct {
				Scope struct {
					Boot string `json:"boot_epoch"`
				}
			} `json:"binding"`
		} `json:"lastSuccess"`
	}
	testutil.DecodeJSON(t, rr, &status)
	if status.LastSuccess.Binding.Scope.Boot != "88888888-8888-4888-8888-888888888888" {
		t.Fatal("preboot completion replaced fresh evidence", rr.Body.String())
	}
}
func TestOfficialBalanceAuthorityChangesNeverInheritSecrets(t *testing.T) {
	for _, change := range []struct{ old, new string }{
		{`"credential_version":3`, `"credential_version":4`}, {`"config_version":4`, `"config_version":5`},
		{`"identity_version":2`, `"identity_version":3`}, {`"membership_version":0`, `"membership_version":1`},
		{"55555555-5555-4555-8555-555555555555", "99999999-9999-4999-8999-999999999999"},
		{"22222222-2222-4222-8222-222222222222", "99999999-9999-4999-8999-999999999999"},
		{"11111111-1111-4111-8111-111111111111", "99999999-9999-4999-8999-999999999999"},
	} {
		t.Run(change.old, func(t *testing.T) {
			f := newOfficialFixture(t)
			f.configure("")
			f.coreSnapshot.Store(strings.ReplaceAll(testSnapshotJSON, change.old, change.new))
			f.coreCaps.Store(strings.ReplaceAll(testCapsJSON, change.old, change.new))
			fresh := strings.ReplaceAll(`{`+balanceBinding+`}`, change.old, change.new)
			rr := testutil.Request(t, f.handler, "GET", balancePath+"?binding="+url.QueryEscape(fresh), "", testutil.AdminKey)
			testutil.RequireStatus(t, rr, 200)
			if strings.Contains(rr.Body.String(), `"configured":true`) || strings.Contains(rr.Body.String(), "lastSuccess") {
				t.Fatal("authority change inherited old credentials/evidence")
			}
			testutil.RequireStatus(t, testutil.Request(t, f.handler, "GET", balancePath+"?binding="+url.QueryEscape(`{`+balanceBinding+`}`), "", testutil.AdminKey), 409)
		})
	}
}
func TestOfficialBalanceDeepInfraRejectsDeepSeekRoute(t *testing.T) {
	f := newOfficialFixture(t)
	body := `{` + balanceBinding + `,"version":0,"adapter":"deepinfra","endpoint":"` + f.upstream.URL + `/user/balance","privateTargets":["127.0.0.1"],"secret":"synthetic-query-canary"}`
	testutil.RequireStatus(t, testutil.Request(t, f.handler, "PUT", balancePath, body, testutil.AdminKey), 400)
	if f.count.Load() != 0 {
		t.Fatal("wrong protocol route queried")
	}
}

func TestOfficialBalanceNeverFollowsCredentialBearingRedirect(t *testing.T) {
	for _, sameOrigin := range []bool{false, true} {
		t.Run(fmt.Sprint(sameOrigin), func(t *testing.T) {
			f := newOfficialFixture(t)
			f.configure("")
			var sinkCalls atomic.Int64
			sink := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { sinkCalls.Add(1); w.WriteHeader(401) }))
			defer sink.Close()
			destination := sink.URL + "/capture"
			if sameOrigin {
				destination = f.upstream.URL + "/capture"
			}
			f.response.Store("redirectto:" + destination)
			got := f.wait(f.query())
			if !strings.Contains(got, `"queryStatus":"network_error"`) || f.count.Load() != 1 || sinkCalls.Load() != 0 || strings.Contains(got, "synthetic-query-canary") {
				t.Fatal("credential-bearing redirect followed", got)
			}
		})
	}
}
func TestOfficialBalanceConnectionRefusedKeepsSuccess(t *testing.T) {
	f := newOfficialFixture(t)
	f.configure("")
	f.wait(f.query())
	f.upstream.Close()
	got := f.wait(f.query())
	if !strings.Contains(got, `"queryStatus":"network_error"`) || !strings.Contains(got, `"stale":true`) || !strings.Contains(got, `"total":"-0.000000001"`) {
		t.Fatal("network failure fabricated or lost money", got)
	}
}

func TestOfficialBalanceExpiredBudgetKeepsDurableAttempt(t *testing.T) {
	f := newOfficialFixture(t)
	f.configure(`,"timeoutMs":50`)
	if got := f.wait(f.query()); !strings.Contains(got, `"queryStatus":"success"`) {
		t.Fatal(got)
	}
	f.response.Store("expire_revalidation")
	got := f.wait(f.query())
	if !strings.Contains(got, `"queryStatus":"network_error"`) || !strings.Contains(got, "lastSuccess") || !strings.Contains(got, `"stale":true`) {
		t.Fatalf("expired final verification lost durable attempt: %s", got)
	}
	if read := f.status(); !strings.Contains(read, `"stale":true`) || !strings.Contains(read, `"queryStatus":"network_error"`) {
		t.Fatal("status hid exhausted management attempt", read)
	}
}
