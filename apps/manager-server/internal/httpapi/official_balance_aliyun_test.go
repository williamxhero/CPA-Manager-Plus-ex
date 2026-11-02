package httpapi

import (
	"bytes"
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
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
	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

const aliyunAdapter = "aliyun-prepaid-cash-v2017-12-14"
const aliyunAK = "SYNTHETIC-ALIYUN-BILLING-AK-CANARY"
const aliyunSK = "SYNTHETIC-ALIYUN-BILLING-SK-CANARY"
const aliyunWallet = `{"Code":"200","Success":true,"RequestId":"synthetic-bss-request","Data":{"AvailableCashAmount":"9007199254740993.000000000000000001","Currency":"CNY","AvailableAmount":"9007199254741093.000000000000000001","CreditAmount":"100.00"}}`
const aliyunIdentity = `{"AccountId":"1968132000123456","IdentityType":"RAMUser","RequestId":"synthetic-sts-request","PrincipalId":"synthetic-principal"}`

// Agreed public seam: real authenticated Manager HTTP -> reopened encrypted temp
// SQLite -> independently pinned actual Core -> production secure TLS transport.
func TestOfficialBalanceAliyunActualCoreCashSuccess(t *testing.T) {
	f := newAliyunFixture(t)
	code, raw := f.api("PUT", balancePath, f.config(0, f.secret()))
	if code != 200 {
		t.Fatalf("public profile configuration HTTP%d: %s", code, raw)
	}
	if !bytes.Contains(raw, []byte(`"managerDirectQuery":true`)) || !bytes.Contains(raw, []byte(`"accountID":"1968132000123456"`)) {
		t.Fatal("safe public profile/capability readback missing", string(raw))
	}
	job := f.query(1)
	if job.Status == nil || job.Status.LastSuccess == nil {
		t.Fatalf("no successful public cash read: %+v", job.Status)
	}
	st, obs := job.Status, job.Status.LastSuccess
	if obs.AmountDecimal != "9007199254740993.000000000000000001" || obs.Currency != "CNY" || obs.Semantic != "account_wallet" || obs.NativeQuota != nil || obs.WindowsJSON != "" || st.Stale || st.CoreGate || st.CoreQuery {
		t.Fatalf("cash precision/semantics/qualification failed: %+v", st)
	}
	if f.calls.Load() != 1 || f.identityCalls.Load() != 1 || f.inference.Load() != 0 {
		t.Fatal("unexpected upstream traffic")
	}
	_, raw = f.api("GET", balancePath+"?binding="+url.QueryEscape(string(mustAliyunJSON(f.binding))), nil)
	for _, expected := range []string{"caller_account_matched_operator_asserted_source_binding_not_key_or_payer_verified", `"identityType":"RAMUser"`} {
		if !bytes.Contains(raw, []byte(expected)) {
			t.Fatal("caller proof/selected key limitation missing")
		}
	}
	f.restartManager()
	if st := f.status(); st.LastSuccess == nil || st.LastSuccess.AmountDecimal != obs.AmountDecimal || f.calls.Load() != 1 {
		t.Fatal("durable readback lost or GET sent billing traffic")
	}
	if next := f.query(1); next.Status == nil || next.Status.State != "success" || f.calls.Load() != 2 || f.identityCalls.Load() != 2 {
		t.Fatal("encrypted same credential did not survive restart")
	}
	code, raw = f.api("GET", "/usage-service/full-refresh", nil)
	var inventory fr.Inventory
	if code != 200 || json.Unmarshal(raw, &inventory) != nil {
		t.Fatal("refresh inventory unavailable")
	}
	var sources []fr.SourceStatus
	for _, source := range inventory.Sources {
		if source.Request.Kind == fr.Official {
			sources = append(sources, source)
		}
	}
	if len(sources) != 1 || !sources[0].ManualDisabled || !sources[0].QueryCapable || sources[0].WalletProof != nil {
		t.Fatal("shared scheduler inventory or unsupported wallet membership changed")
	}
	t.Log("PUBLIC_CHAIN_PASS: exact cash not credit-inclusive; same immutable AK/SK STS account match; fixed RPC/TLS; encrypted SQLite restart; actual disabled Core source; no inference, payer proof or gate ACK")
}
func mustAliyunJSON(v any) []byte { raw, _ := json.Marshal(v); return raw }

func TestOfficialBalanceAliyunBusinessFailureRetainsDebt(t *testing.T) {
	f := newAliyunFixture(t)
	if code, raw := f.api("PUT", balancePath, f.config(0, f.secret())); code != 200 {
		t.Fatalf("configure HTTP%d: %s", code, raw)
	}
	f.response.Store(strings.Replace(aliyunWallet, "9007199254740993.000000000000000001", "-0.000000000000000001", 1))
	first := f.query(1).Status.LastSuccess
	if first == nil || first.AmountDecimal != "-0.000000000000000001" {
		t.Fatal("exact native debt lost")
	}
	// HTTP200 is not business success, even with a plausible cash payload.
	f.response.Store(strings.Replace(strings.Replace(aliyunWallet, `"Code":"200"`, `"Code":"NoPermission","Message":"`+aliyunSK+`"`, 1), `"Success":true`, `"Success":false`, 1))
	st := f.query(1).Status
	if st == nil || st.Attempt == nil || st.State != "auth_error" || st.Attempt.ErrorCode != "UPSTREAM_AUTH" || !st.Stale || st.LastSuccess == nil || st.LastSuccess.QueryID != first.QueryID || st.LastSuccess.ObservedAtMS != first.ObservedAtMS || st.LastSuccess.AmountDecimal != first.AmountDecimal || st.Attempt.AmountDecimal != "" {
		t.Fatalf("business denial attributed cash or lost stale debt: %+v", st)
	}
	// A bounded set of the accepted cash/permission contract's failure shapes,
	// not another provider/security/scheduler matrix.
	for _, tc := range []struct {
		name, body, state, code string
		http                    int64
	}{
		{"cash missing despite available credit", strings.Replace(aliyunWallet, `"AvailableCashAmount":"9007199254740993.000000000000000001",`, "", 1), "parse_error", "INVALID_BALANCE", 0},
		{"cash null", strings.Replace(aliyunWallet, `"9007199254740993.000000000000000001"`, "null", 1), "parse_error", "INVALID_BALANCE", 0},
		{"cash numeric", strings.Replace(aliyunWallet, `"9007199254740993.000000000000000001"`, "0", 1), "parse_error", "INVALID_BALANCE", 0},
		{"cash invalid decimal", strings.Replace(aliyunWallet, `"9007199254740993.000000000000000001"`, `"1e3"`, 1), "parse_error", "INVALID_BALANCE", 0},
		{"currency missing", strings.Replace(aliyunWallet, `"Currency":"CNY",`, "", 1), "parse_error", "INVALID_BALANCE", 0},
		{"HTTP auth denial", aliyunSK, "auth_error", "UPSTREAM_AUTH", 403},
		{"RPC HTTP400 denial", `{"Code":"NoPermission","Message":"` + aliyunSK + `"}`, "auth_error", "UPSTREAM_AUTH", 400},
		{"business false with success code", strings.Replace(aliyunWallet, `"Success":true`, `"Success":false`, 1), "parse_error", "ALIYUN_UPSTREAM_ERROR", 0},
		{"site failure never switches endpoints", `{"Code":"AuthSiteFail"}`, "needs_configuration", "ALIYUN_SITE_MISMATCH", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.response.Store(tc.body)
			f.httpCode.Store(tc.http)
			st := f.query(1).Status
			assertAliyunStale(t, st, first, tc.state, tc.code)
		})
	}
	f.restartManager()
	if st := f.status(); st.LastSuccess == nil || st.LastSuccess.QueryID != first.QueryID || !st.Stale {
		t.Fatal("stale exact debt did not survive SQLite reopen")
	}
}

func assertAliyunStale(t *testing.T, st *ob.Status, first *balance.Observation, state, code string) {
	t.Helper()
	if st == nil || st.Attempt == nil || st.State != state || st.Attempt.ErrorCode != code || !st.Stale || st.LastSuccess == nil || st.LastSuccess.QueryID != first.QueryID || st.LastSuccess.ObservedAtMS != first.ObservedAtMS || st.LastSuccess.AmountDecimal != first.AmountDecimal || st.LastSuccess.Currency != first.Currency || st.Attempt.AmountDecimal != "" || st.Attempt.AliyunAccount != nil {
		t.Fatalf("failed query attributed cash/account or lost stale success: %+v", st)
	}
}

func TestOfficialBalanceAliyunCallerMismatchRetainsCash(t *testing.T) {
	f := newAliyunFixture(t)
	if code, _ := f.api("PUT", balancePath, f.config(0, f.secret())); code != 200 {
		t.Fatal("configuration rejected")
	}
	first := f.query(1).Status.LastSuccess
	if first == nil {
		t.Fatal("initial cash unavailable")
	}
	f.identityResponse.Store(strings.Replace(aliyunIdentity, "1968132000123456", "1968132000123457", 1))
	assertAliyunStale(t, f.query(1).Status, first, "parse_error", "ALIYUN_ACCOUNT_MISMATCH")
	if f.calls.Load() != 1 {
		t.Fatal("mismatched caller was allowed to query cash")
	}
	// Error envelopes cannot be bypassed by plausible AccountId fields.
	f.identityResponse.Store(strings.Replace(aliyunIdentity, `{"AccountId"`, `{"Code":"NoPermission","Message":"`+aliyunSK+`","AccountId"`, 1))
	assertAliyunStale(t, f.query(1).Status, first, "auth_error", "UPSTREAM_AUTH")
	if f.calls.Load() != 1 || f.identityCalls.Load() != 3 || f.inference.Load() != 0 {
		t.Fatal("identity failure queried another product/account")
	}
}

func TestOfficialBalanceAliyunProviderEntryRequired(t *testing.T) {
	f := newAliyunFixture(t, true)
	code, raw := f.api("GET", negotiatedPath, nil)
	var inventory struct{ Negotiated rc.Negotiated }
	if code != 200 || json.Unmarshal(raw, &inventory) != nil || inventory.Negotiated.Snapshot == nil {
		t.Fatal("native identity inventory unavailable")
	}
	var credential rc.Target
	for _, row := range inventory.Negotiated.Snapshot.Resources {
		if row.Kind == "credential" && row.ParentID == "" {
			credential = row.Target
		}
	}
	if credential.ResourceID == "" {
		t.Fatal("fixture lacks standalone credential")
	}
	config := f.config(0, f.secret())
	config["targets"] = []rc.Target{credential}
	if code, _ := f.api("PUT", balancePath, config); code != 400 || f.calls.Load() != 0 || f.identityCalls.Load() != 0 {
		t.Fatal("prepaid adapter accepted credential/Coding Plan entry")
	}
}

func TestOfficialBalanceAliyunConfigurationAndSecretSafety(t *testing.T) {
	f := newAliyunFixture(t)
	if code, _ := f.api("PUT", balancePath, f.config(0, f.secret()), ""); code != 401 {
		t.Fatal("unauthenticated write accepted")
	}
	for _, tc := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"missing profile", func(c map[string]any) { delete(c, "aliyun") }},
		{"international site", func(c map[string]any) { c["aliyun"].(map[string]any)["site"] = "international" }},
		{"unbound source", func(c map[string]any) { c["aliyun"].(map[string]any)["accountBindingConfirmed"] = false }},
		{"no source reference", func(c map[string]any) { delete(c["aliyun"].(map[string]any), "proofReference") }},
		{"bearer not AKSK", func(c map[string]any) { c["secret"] = "Bearer synthetic-inference-only" }},
		{"incomplete pair", func(c map[string]any) { c["secret"] = `{"accessKeyID":"SYNTHETIC-AK"}` }},
		{"no temp token extension", func(c map[string]any) {
			c["secret"] = strings.TrimSuffix(f.secret(), "}") + `,"securityToken":"synthetic-session"}`
		}},
		{"endpoint override", func(c map[string]any) { c["endpoint"] = "https://business.ap-southeast-1.aliyuncs.com/" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := f.config(0, f.secret())
			tc.change(c)
			if code, _ := f.api("PUT", balancePath, c); code != 400 {
				t.Fatalf("unsafe configuration accepted HTTP%d", code)
			}
		})
	}
	if st := f.status(); st.Version != 0 || st.Configured || f.calls.Load() != 0 || f.identityCalls.Load() != 0 {
		t.Fatal("invalid configuration mutated state or sent signed traffic")
	}
	if code, _ := f.api("PUT", balancePath, f.config(0, "")); code != 200 {
		t.Fatal("valid profile without credentials not saveable")
	}
	if st := f.query(1).Status; st == nil || st.State != "needs_configuration" || st.Configured || f.calls.Load() != 0 || f.identityCalls.Load() != 0 {
		t.Fatal("missing authorization was guessed")
	}
	if code, _ := f.api("PUT", balancePath, f.config(1, f.secret())); code != 200 {
		t.Fatal("encrypted independent credential update rejected")
	}
	if code, _ := f.api("PUT", balancePath, f.config(1, f.secret())); code != 409 {
		t.Fatal("stale configuration CAS accepted")
	}
	if code, _ := f.api("PUT", balancePath, f.config(2, "")); code != 200 || !f.status().Configured {
		t.Fatal("unchanged source contract lost write-only secret")
	}
	if st := f.query(3).Status; st == nil || st.State != "success" {
		t.Fatal("preserved credential cannot make real TLS reads")
	}
	n, identityN := f.calls.Load(), f.identityCalls.Load()
	changed := f.config(3, "")
	changed["aliyun"].(map[string]any)["accountID"] = "1968132000123457"
	if code, _ := f.api("PUT", balancePath, changed); code != 200 {
		t.Fatal("new profile authority rejected")
	}
	if st := f.query(4).Status; st == nil || st.Configured || st.State != "needs_configuration" || st.LastSuccess != nil || f.calls.Load() != n || f.identityCalls.Load() != identityN {
		t.Fatal("changed account silently inherited encrypted secret or previous result")
	}
}

// Synthetic live host only; same control/fixture.json/stop convention as the
// existing UI fixture. No production service, credentials or runtime config.
func TestOfficialBalanceAliyunUIFixture(t *testing.T) {
	dir := os.Getenv("ALIYUN_BALANCE_UI_FIXTURE")
	if dir == "" {
		t.Skip("opt-in synthetic Alibaba UI host")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "fixture.json")); !os.IsNotExist(err) {
		t.Fatal("fresh fixture directory required")
	}
	f := newAliyunFixture(t)
	control := officialBrowserServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var state struct {
			Response         *string
			Status           *int
			IdentityResponse *string
			IdentityStatus   *int
		}
		if r.Method != "PUT" || json.NewDecoder(io.LimitReader(r.Body, 32<<10)).Decode(&state) != nil {
			w.WriteHeader(400)
			return
		}
		for _, code := range []*int{state.Status, state.IdentityStatus} {
			if code != nil && *code != 0 && (*code < 200 || *code > 599) {
				w.WriteHeader(400)
				return
			}
		}
		if state.Response != nil {
			f.response.Store(*state.Response)
		}
		if state.Status != nil {
			f.httpCode.Store(int64(*state.Status))
		}
		if state.IdentityResponse != nil {
			f.identityResponse.Store(*state.IdentityResponse)
		}
		if state.IdentityStatus != nil {
			f.identityCode.Store(int64(*state.IdentityStatus))
		}
		w.WriteHeader(204)
	}))
	defer control.Close()
	data, _ := json.Marshal(map[string]any{"synthetic": true, "base": f.server.URL, "managementKey": testutil.AdminKey, "binding": f.binding, "adapter": aliyunAdapter, "endpoint": "https://business.aliyuncs.com/", "aliyun": f.config(0, "")["aliyun"], "secret": f.secret(), "control": control.URL})
	path := filepath.Join(dir, "fixture.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Log("FIXTURE_READY:", path)
	for i := 0; i < 2400; i++ {
		if _, err := os.Stat(filepath.Join(dir, "stop")); err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("UI fixture not normally stopped within 240 seconds")
}

type aliyunFixture struct {
	t                *testing.T
	manager          *Server
	server           *httptest.Server
	db               *store.Store
	newDB            func() *store.Store
	network          balance.NetworkOptions
	binding          ob.Binding
	calls            atomic.Int64
	identityCalls    atomic.Int64
	identityResponse atomic.Value
	identityCode     atomic.Int64
	nonces           sync.Map
	inference        atomic.Int64
	denied           atomic.Int64
	response         atomic.Value
	httpCode         atomic.Int64
}

func newAliyunFixture(t *testing.T, standaloneCredential ...bool) *aliyunFixture {
	t.Helper()
	binary := os.Getenv("CORE_OFFICIAL_QUERY_TEST_BINARY")
	if binary == "" {
		t.Fatal("this acceptance requires the independently pinned real Core binary; it must not silently skip")
	}
	verifiedRefreshArtifact(t, binary, os.Getenv("CORE_OFFICIAL_QUERY_EXPECTED_SHA256"), os.Getenv("CORE_OFFICIAL_QUERY_EXPECTED_REVISION"))
	f := &aliyunFixture{t: t}
	f.response.Store(aliyunWallet)
	f.identityResponse.Store(aliyunIdentity)
	deny := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.IsAbs() || r.Method == "CONNECT" {
			f.denied.Add(1)
		} else {
			f.inference.Add(1)
		}
		w.WriteHeader(503)
	}))
	t.Cleanup(deny.Close)
	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !aliyunPublicRequestValid(r) {
			t.Error("TLS upstream rejected fixed host/action/signature; credentials omitted")
			w.WriteHeader(403)
			return
		}
		if _, loaded := f.nonces.LoadOrStore(r.URL.Query().Get("SignatureNonce"), true); loaded {
			t.Error("query reused a signature nonce")
			w.WriteHeader(403)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Host == "sts.cn-hangzhou.aliyuncs.com" {
			f.identityCalls.Add(1)
			if code := f.identityCode.Load(); code != 0 {
				w.WriteHeader(int(code))
			}
			fmt.Fprint(w, f.identityResponse.Load().(string))
		} else {
			f.calls.Add(1)
			if code := f.httpCode.Load(); code != 0 {
				w.WriteHeader(int(code))
			}
			fmt.Fprint(w, f.response.Load().(string))
		}
	}))
	// Reissue a test certificate for the real official hostname. Production TLS
	// still validates host and trust; only DNS, numeric dial and CA are injected.
	seed := httptest.NewTLSServer(http.NotFoundHandler())
	cert := seed.TLS.Certificates[0]
	seed.Close()
	template, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	template.DNSNames = []string{"business.aliyuncs.com", "sts.cn-hangzhou.aliyuncs.com"}
	template.IPAddresses = nil
	key := cert.PrivateKey.(crypto.Signer)
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	cert.Certificate = [][]byte{der}
	upstream.TLS = seed.TLS.Clone()
	upstream.TLS.Certificates[0] = cert
	upstream.StartTLS()
	t.Cleanup(upstream.Close)
	root, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(root)
	f.network = balance.NetworkOptions{RootCAs: pool, LookupIP: func(_ context.Context, host string) ([]net.IPAddr, error) {
		if host != "business.aliyuncs.com" && host != "sts.cn-hangzhou.aliyuncs.com" {
			return nil, fmt.Errorf("unapproved DNS name")
		}
		return []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}}, nil
	}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "1.1.1.1:443" {
			return nil, fmt.Errorf("unapproved pinned destination")
		}
		return (&net.Dialer{}).DialContext(ctx, network, upstream.Listener.Addr().String())
	}}
	dir := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	auth := filepath.Join(dir, "auth")
	if err := os.Mkdir(auth, 0700); err != nil {
		t.Fatal(err)
	}
	if len(standaloneCredential) > 0 && standaloneCredential[0] {
		if err := os.WriteFile(filepath.Join(auth, "synthetic-codex.json"), []byte(`{"type":"codex","access_token":"SYNTHETIC-OPTIONAL-OAUTH","account_id":"synthetic-standalone","disabled":true}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	config := fmt.Sprintf("config-version: 8\nserver: {host: 127.0.0.1, port: %d}\nmanagement: {secret-key: synthetic-aliyun-core, disable-control-panel: true, disable-auto-update-panel: true}\noauth: {auth-dir: %q}\nresource-catalog: {enabled: true, state-dir: catalog}\nplugins: {enabled: false}\napi-keys:\n  openai-compatibility:\n    - name: synthetic-aliyun\n      base-url: %s\n      disabled: true\n      keys: [{api-key: SYNTHETIC-INFERENCE-CANARY, disabled: true}]\n", port, filepath.ToSlash(auth), deny.URL)
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "-config", configPath, "-local-model")
	cmd.Dir, cmd.Stdout, cmd.Stderr = dir, io.Discard, io.Discard
	for _, name := range []string{"PATH", "SystemRoot", "WINDIR", "TEMP", "TMP", "COMSPEC"} {
		if value := os.Getenv(name); value != "" {
			cmd.Env = append(cmd.Env, name+"="+value)
		}
	}
	cmd.Env = append(cmd.Env, "HOME="+dir, "USERPROFILE="+dir, "APPDATA="+dir, "LOCALAPPDATA="+dir, "HTTP_PROXY="+deny.URL, "HTTPS_PROXY="+deny.URL, "NO_PROXY=127.0.0.1,localhost")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	cfg := testutil.NewConfig(t)
	protector, _ := security.NewProtector([]byte("0123456789abcdef0123456789abcdef"))
	f.newDB = func() *store.Store {
		db, err := store.Open(cfg.DBPath, protector)
		if err != nil {
			t.Fatal(err)
		}
		return db
	}
	f.db = f.newDB()
	testutil.EnsureAdminCredential(t, f.db)
	if err := f.db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: fmt.Sprintf("http://127.0.0.1:%d", port), ManagementKey: "synthetic-aliyun-core"}}); err != nil {
		t.Fatal(err)
	}
	f.manager = New(cfg, f.db, collector.NewManager(cfg, f.db))
	f.serve()
	t.Cleanup(func() { f.closeManager(); f.db.Close() })
	until := time.Now().Add(20 * time.Second)
	for time.Now().Before(until) {
		code, raw := f.api("GET", "/usage-service/full-refresh", nil)
		var inv fr.Inventory
		if code == 200 && json.Unmarshal(raw, &inv) == nil {
			for _, source := range inv.Sources {
				if source.Request.Kind == fr.Official && source.Request.Official != nil && len(source.Targets) == 2 && source.ManualDisabled {
					f.binding = source.Request.Official.Binding
					return f
				}
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("isolated actual Core did not publish the complete disabled source")
	return nil
}

func (f *aliyunFixture) serve() {
	f.manager.AppContext().OfficialBalanceService.Querier = balance.NewWithNetwork(f.network)
	if os.Getenv("ALIYUN_BALANCE_UI_FIXTURE") != "" {
		f.server = officialBrowserServer(f.t, f.manager.Handler())
	} else {
		f.server = httptest.NewServer(f.manager.Handler())
	}
}
func (f *aliyunFixture) closeManager() {
	f.server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := f.manager.AppContext().FullRefreshService.Shutdown(ctx); err != nil {
		f.t.Error(err)
	}
}
func (f *aliyunFixture) restartManager() {
	f.t.Helper()
	cfg := f.manager.AppContext().Config
	f.closeManager()
	f.db.Close()
	f.db = f.newDB()
	f.manager = New(cfg, f.db, collector.NewManager(cfg, f.db))
	f.serve()
}
func (f *aliyunFixture) api(method, path string, value any, auth ...string) (int, []byte) {
	f.t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		f.t.Fatal(err)
	}
	req, _ := http.NewRequest(method, f.server.URL+path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	key := testutil.AdminKey
	if len(auth) > 0 {
		key = auth[0]
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	res, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, (2<<20)+1))
	if err != nil {
		f.t.Fatal(err)
	}
	for _, canary := range []string{aliyunAK, aliyunSK, "SYNTHETIC-INFERENCE-CANARY", "synthetic-aliyun-core"} {
		if bytes.Contains(raw, []byte(canary)) {
			f.t.Fatal("secret appeared in public management readback")
		}
	}
	return res.StatusCode, raw
}
func (f *aliyunFixture) secret() string {
	raw, _ := json.Marshal(map[string]string{"accessKeyID": aliyunAK, "secretAccessKey": aliyunSK})
	return string(raw)
}
func (f *aliyunFixture) config(version int64, secret string) map[string]any {
	return map[string]any{"scope": f.binding.Scope, "targets": f.binding.Targets, "version": version, "adapter": aliyunAdapter,
		"aliyun": map[string]any{"site": "china", "accountID": "1968132000123456", "accountBindingConfirmed": true, "proofReference": "synthetic-source-account-reference"}, "secret": secret}
}
func (f *aliyunFixture) status() ob.Status {
	f.t.Helper()
	binding, _ := json.Marshal(f.binding)
	code, raw := f.api("GET", balancePath+"?binding="+url.QueryEscape(string(binding)), nil)
	var status ob.Status
	if code != 200 || json.Unmarshal(raw, &status) != nil {
		f.t.Fatalf("status HTTP %d: %s", code, raw)
	}
	return status
}
func (f *aliyunFixture) query(version int64) ob.Job {
	f.t.Helper()
	code, raw := f.api("POST", balancePath, ob.Query{Binding: f.binding, Version: version})
	var job ob.Job
	if code != 202 || json.Unmarshal(raw, &job) != nil {
		f.t.Fatalf("start HTTP %d: %s", code, raw)
	}
	until := time.Now().Add(16 * time.Second)
	for time.Now().Before(until) {
		code, raw = f.api("GET", balancePath+"?job="+job.ID, nil)
		if code != 200 || json.Unmarshal(raw, &job) != nil {
			f.t.Fatalf("job HTTP %d: %s", code, raw)
		}
		if job.State != "running" {
			return job
		}
		time.Sleep(10 * time.Millisecond)
	}
	f.t.Fatal("bounded public query did not terminate")
	return ob.Job{}
}

// External TLS verifier uses the first-party v1.63.107 SDK's form-map
// canonicalization (sdk/auth/rpc_signature_composer.go), independently of the
// production byte-wise RFC3986 encoder. It never calls production sign helpers.
func aliyunPublicRequestValid(r *http.Request) bool {
	if r.TLS == nil || r.Method != "GET" || r.URL.Path != "/" || r.Header.Get("Authorization") != "" {
		return false
	}
	p := r.URL.Query()
	action, version := "QueryAccountBalance", "2017-12-14"
	if r.Host == "sts.cn-hangzhou.aliyuncs.com" {
		action, version = "GetCallerIdentity", "2015-04-01"
	} else if r.Host != "business.aliyuncs.com" {
		return false
	}
	expected := map[string]string{"Action": action, "Version": version, "AccessKeyId": aliyunAK, "RegionId": "cn-hangzhou", "Format": "JSON", "SignatureMethod": "HMAC-SHA1", "SignatureVersion": "1.0", "SignatureType": ""}
	if len(p) != 11 {
		return false
	}
	for k, v := range expected {
		if len(p[k]) != 1 || p.Get(k) != v {
			return false
		}
	}
	when, err := time.Parse("2006-01-02T15:04:05Z", p.Get("Timestamp"))
	if err != nil || time.Since(when) > time.Minute || time.Until(when) > time.Minute || len(p.Get("SignatureNonce")) < 16 {
		return false
	}
	for _, vs := range p {
		if len(vs) != 1 {
			return false
		}
	}
	signature := p.Get("Signature")
	p.Del("Signature")
	canonical := strings.NewReplacer("+", "%20", "*", "%2A", "%7E", "~").Replace(p.Encode())
	mac := hmac.New(sha1.New, []byte(aliyunSK+"&"))
	mac.Write([]byte("GET&%2F&" + url.QueryEscape(canonical)))
	body, err := io.ReadAll(io.LimitReader(r.Body, 1))
	return err == nil && len(body) == 0 && hmac.Equal([]byte(signature), []byte(base64.StdEncoding.EncodeToString(mac.Sum(nil))))
}
