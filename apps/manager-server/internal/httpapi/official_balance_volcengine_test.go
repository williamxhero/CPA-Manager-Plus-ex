package httpapi

import (
	"bytes"
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
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

const volcengineAdapter = "volcengine-account-balance-v2022-01-01"
const volcengineAK = "SYNTHETIC-BILLING-AK-CANARY"
const volcengineSK = "SYNTHETIC-BILLING-SK-CANARY"
const volcengineWallet = `{"ResponseMetadata":{"Action":"QueryBalanceAcct","Version":"2022-01-01","Service":"billing"},"Result":{"AccountID":9007199254740993,"AvailableBalance":"77.01","CashBalance":"83.01","CreditLimit":"0.01","FreezeAmount":"5.01","ArrearsBalance":"1.01"}}`

// This is the agreed public seam, not an adapter/signing unit test: authenticated
// Manager HTTP -> encrypted temporary SQLite -> independently pinned real Core
// identity -> production secure transport -> controlled, certificate-valid TLS.
func TestOfficialBalanceVolcengineActualCoreSuccess(t *testing.T) {
	f := newVolcengineFixture(t)
	code, raw := f.api("PUT", balancePath, f.config(0, f.secret()))
	if code != 200 {
		t.Fatalf("public profile configuration HTTP %d: %s", code, raw)
	}
	if !bytes.Contains(raw, []byte(`"managerDirectQuery":true`)) || !bytes.Contains(raw, []byte(`"accountID":"9007199254740993"`)) {
		t.Fatal("profile or direct-read capability missing:", string(raw))
	}
	job := f.query(1)
	if job.Status == nil || job.Status.LastSuccess == nil {
		t.Fatal("public chain has no successful balance", job)
	}
	obs := job.Status.LastSuccess
	if obs.Semantic != "account_wallet" || obs.AmountDecimal != "77.01" || obs.Currency != "CNY" || obs.NativeQuota != nil || obs.WindowsJSON != "" || job.Status.CoreGate || job.Status.CoreQuery || job.Status.Stale {
		t.Fatalf("not exact direct AvailableBalance / prepaid account semantics: %+v", job.Status)
	}
	if f.calls.Load() != 1 || f.inference.Load() != 0 {
		t.Fatalf("unexpected billing=%d or inference=%d traffic", f.calls.Load(), f.inference.Load())
	}
	// A new Manager and reopened SQLite must safely read the persisted result,
	// then resolve the SAME encrypted AK/SK for a new real TLS query, without PUT.
	f.restartManager()
	status := f.status()
	if status.LastSuccess == nil || status.LastSuccess.AmountDecimal != "77.01" || f.calls.Load() != 1 {
		t.Fatal("durable readback lost or GET sent billing traffic")
	}
	if again := f.query(1); again.Status == nil || again.Status.State != "success" || f.calls.Load() != 2 {
		t.Fatal("encrypted billing credential did not survive host restart")
	}
	code, raw = f.api("GET", "/usage-service/full-refresh", nil)
	var inventory fr.Inventory
	if code != 200 || json.Unmarshal(raw, &inventory) != nil {
		t.Fatal("invalid refresh inventory:", string(raw))
	}
	var official []fr.SourceStatus
	for _, source := range inventory.Sources {
		if source.Request.Kind == fr.Official {
			official = append(official, source)
		}
	}
	if len(official) != 1 || !official[0].ManualDisabled || !official[0].QueryCapable || official[0].WalletProof != nil {
		t.Fatal("existing scheduler inventory lost disabled source, or invented wallet membership:", string(raw))
	}
	t.Log("PUBLIC_CHAIN_PASS: fixed official host/GET/action/version; independently checked HMAC; exact direct available; encrypted SQLite restart; safe config/readback; real Core disabled provider/member identity; no inference or gate ACK")
}

func TestOfficialBalanceVolcengineAccountMismatchRetainsSuccess(t *testing.T) {
	f := newVolcengineFixture(t)
	if code, raw := f.api("PUT", balancePath, f.config(0, f.secret())); code != 200 {
		t.Fatalf("configure HTTP %d: %s", code, raw)
	}
	first := f.query(1).Status.LastSuccess
	f.response.Store(strings.Replace(volcengineWallet, "9007199254740993", "9007199254740994", 1))
	job := f.query(1)
	if job.Status == nil || job.Status.Attempt == nil || job.Status.State != "parse_error" || job.Status.Attempt.ErrorCode != "VOLCENGINE_ACCOUNT_MISMATCH" || !job.Status.Stale || job.Status.LastSuccess == nil || job.Status.LastSuccess.QueryID != first.QueryID || job.Status.LastSuccess.ObservedAtMS != first.ObservedAtMS || job.Status.LastSuccess.AmountDecimal != "77.01" || job.Status.Attempt.AmountDecimal != "" {
		t.Fatal("mismatched billing account was attributed, or last success lost:", job.Status)
	}
}

func TestOfficialBalanceVolcengineFailuresRetainDebt(t *testing.T) {
	f := newVolcengineFixture(t)
	if code, raw := f.api("PUT", balancePath, f.config(0, f.secret())); code != 200 {
		t.Fatalf("configure HTTP %d: %s", code, raw)
	}
	debt := strings.Replace(volcengineWallet, `"77.01"`, `"-0.000000000000000001"`, 1)
	f.response.Store(debt)
	first := f.query(1).Status.LastSuccess
	if first == nil || first.AmountDecimal != "-0.000000000000000001" {
		t.Fatal("exact signed debt lost")
	}
	for _, tc := range []struct {
		name, body, status, code string
		http                     int64
	}{
		{"missing amount", strings.Replace(debt, `"AvailableBalance":"-0.000000000000000001",`, "", 1), "parse_error", "INVALID_BALANCE", 0},
		{"null amount", strings.Replace(debt, `"-0.000000000000000001"`, "null", 1), "parse_error", "INVALID_BALANCE", 0},
		{"numeric amount", strings.Replace(debt, `"-0.000000000000000001"`, "0", 1), "parse_error", "INVALID_BALANCE", 0},
		{"nonfinite amount", strings.Replace(debt, `"-0.000000000000000001"`, `"NaN"`, 1), "parse_error", "INVALID_BALANCE", 0},
		{"bill permission denied", volcengineSK, "auth_error", "UPSTREAM_AUTH", 403},
		{"upstream failure", volcengineSK, "network_error", "UPSTREAM_STATUS", 503},
		{"error envelope with plausible result", strings.Replace(debt, `"Service":"billing"`, `"Service":"billing","Error":{"Code":"RecordNoFound","Message":"`+volcengineSK+`"}`, 1), "needs_configuration", "VOLCENGINE_ACCOUNT_UNAVAILABLE", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.response.Store(tc.body)
			f.httpCode.Store(tc.http)
			job := f.query(1)
			st := job.Status
			if st == nil || st.Attempt == nil || st.LastSuccess == nil || st.State != tc.status || st.Attempt.ErrorCode != tc.code || !st.Stale || st.Attempt.AmountDecimal != "" || st.LastSuccess.QueryID != first.QueryID || st.LastSuccess.ObservedAtMS != first.ObservedAtMS || st.LastSuccess.AmountDecimal != "-0.000000000000000001" {
				t.Fatal("failure replaced exact last success/time or leaked a fabricated amount:", st)
			}
		})
	}
	f.restartManager()
	if st := f.status(); st.LastSuccess == nil || st.LastSuccess.QueryID != first.QueryID || !st.Stale {
		t.Fatal("stale debt did not survive actual SQLite reopen")
	}
}

func TestOfficialBalanceVolcengineProviderEntryRequired(t *testing.T) {
	f := newVolcengineFixture(t, true)
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
		t.Fatal("fixture has no native standalone credential entry")
	}
	config := f.config(0, f.secret())
	config["targets"] = []rc.Target{credential}
	code, raw = f.api("PUT", balancePath, config)
	if code != 400 || f.calls.Load() != 0 {
		t.Fatalf("prepaid account adapter accepted a Coding Plan credential entry: HTTP%d %s", code, raw)
	}
}

func TestOfficialBalanceVolcengineConfigurationAndSecretSafety(t *testing.T) {
	f := newVolcengineFixture(t)
	if code, _ := f.api("PUT", balancePath, f.config(0, f.secret()), ""); code != 401 {
		t.Fatal("unauthenticated profile write was accepted")
	}
	for _, tc := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"missing profile", func(c map[string]any) { delete(c, "volcengine") }},
		{"currency unknown", func(c map[string]any) { delete(c["volcengine"].(map[string]any), "currency") }},
		{"unit unconfirmed", func(c map[string]any) { c["volcengine"].(map[string]any)["currencyUnitConfirmed"] = false }},
		{"account unbound", func(c map[string]any) { c["volcengine"].(map[string]any)["accountBindingConfirmed"] = false }},
		{"no binding proof", func(c map[string]any) { delete(c["volcengine"].(map[string]any), "proofReference") }},
		{"inference bearer not billing auth", func(c map[string]any) { c["secret"] = "Bearer synthetic-inference-only" }},
		{"incomplete AK SK pair", func(c map[string]any) { c["secret"] = `{"accessKeyID":"SYNTHETIC-AK"}` }},
		{"origin override", func(c map[string]any) { c["endpoint"] = "https://different.example/" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := f.config(0, f.secret())
			tc.change(config)
			if code, _ := f.api("PUT", balancePath, config); code != 400 {
				t.Fatalf("unsafe configuration accepted: HTTP%d", code)
			}
		})
	}
	if st := f.status(); st.Version != 0 || st.Configured || f.calls.Load() != 0 {
		t.Fatal("invalid writes mutated configuration or sent billing traffic")
	}
	// Saveable empty secret is explicit needs_configuration, not zero/unsupported.
	if code, _ := f.api("PUT", balancePath, f.config(0, "")); code != 200 {
		t.Fatal("valid profile without billing authorization was not saveable")
	}
	if job := f.query(1); job.Status == nil || job.Status.State != "needs_configuration" || job.Status.Configured || f.calls.Load() != 0 {
		t.Fatal("missing billing credential was guessed or queried")
	}
	if code, _ := f.api("PUT", balancePath, f.config(1, f.secret())); code != 200 {
		t.Fatal("independent encrypted billing credential write failed")
	}
	if code, _ := f.api("PUT", balancePath, f.config(1, f.secret())); code != 409 {
		t.Fatal("stale config CAS write succeeded")
	}
	if code, _ := f.api("PUT", balancePath, f.config(2, "")); code != 200 || !f.status().Configured {
		t.Fatal("unchanged authority did not preserve write-only encrypted secret")
	}
	if job := f.query(3); job.Status == nil || job.Status.State != "success" {
		t.Fatal("preserved secret cannot perform a real TLS read")
	}
	n := f.calls.Load()
	changed := f.config(3, "")
	changed["volcengine"].(map[string]any)["accountID"] = "9007199254740994"
	if code, _ := f.api("PUT", balancePath, changed); code != 200 {
		t.Fatal("profile authority update failed")
	}
	if job := f.query(4); job.Status == nil || job.Status.Configured || job.Status.State != "needs_configuration" || job.Status.LastSuccess != nil || f.calls.Load() != n {
		t.Fatal("changed account silently inherited previous encrypted auth/result")
	}
}

// Opt-in synthetic host for the parent's REAL Web -> Manager -> SQLite -> real
// Core/TLS acceptance. Same fixture.json/control PUT/stop convention as DeepInfra;
// never a production service or browser automation in this backend task.
func TestOfficialBalanceVolcengineUIFixture(t *testing.T) {
	dir := os.Getenv("VOLCENGINE_BALANCE_UI_FIXTURE")
	if dir == "" {
		t.Skip("opt-in synthetic Volcengine UI fixture")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	f := newVolcengineFixture(t)
	control := officialBrowserServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var state struct {
			Response string
			Status   int
		}
		if r.Method != "PUT" || json.NewDecoder(io.LimitReader(r.Body, 32<<10)).Decode(&state) != nil || state.Status != 0 && (state.Status < 200 || state.Status > 599) {
			w.WriteHeader(400)
			return
		}
		f.response.Store(state.Response)
		f.httpCode.Store(int64(state.Status))
		w.WriteHeader(204)
	}))
	defer control.Close()
	data, _ := json.Marshal(map[string]any{"synthetic": true, "base": f.server.URL, "managementKey": testutil.AdminKey, "binding": f.binding, "adapter": volcengineAdapter, "endpoint": "https://billing.volcengineapi.com/", "volcengine": f.config(0, "")["volcengine"], "secret": f.secret(), "control": control.URL})
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
	t.Fatal("UI fixture not stopped within 240 seconds")
}

type volcengineFixture struct {
	t         *testing.T
	manager   *Server
	server    *httptest.Server
	db        *store.Store
	newDB     func() *store.Store
	network   balance.NetworkOptions
	binding   ob.Binding
	calls     atomic.Int64
	inference atomic.Int64
	denied    atomic.Int64
	response  atomic.Value
	httpCode  atomic.Int64
}

func newVolcengineFixture(t *testing.T, standaloneCredential ...bool) *volcengineFixture {
	t.Helper()
	binary := os.Getenv("CORE_OFFICIAL_QUERY_TEST_BINARY")
	if binary == "" {
		t.Fatal("this acceptance requires the independently pinned real Core binary; it must not silently skip")
	}
	verifiedRefreshArtifact(t, binary, os.Getenv("CORE_OFFICIAL_QUERY_EXPECTED_SHA256"), os.Getenv("CORE_OFFICIAL_QUERY_EXPECTED_REVISION"))
	f := &volcengineFixture{t: t}
	f.response.Store(volcengineWallet)
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
		f.calls.Add(1)
		if !volcenginePublicRequestValid(r) {
			t.Error("TLS upstream rejected host/action/auth/signature; credentials deliberately omitted")
			w.WriteHeader(403)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if code := f.httpCode.Load(); code != 0 {
			w.WriteHeader(int(code))
		}
		fmt.Fprint(w, f.response.Load().(string))
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
	template.DNSNames = []string{"billing.volcengineapi.com"}
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
		if host != "billing.volcengineapi.com" {
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
	config := fmt.Sprintf("config-version: 8\nserver: {host: 127.0.0.1, port: %d}\nmanagement: {secret-key: synthetic-volc-core, disable-control-panel: true, disable-auto-update-panel: true}\noauth: {auth-dir: %q}\nresource-catalog: {enabled: true, state-dir: catalog}\nplugins: {enabled: false}\napi-keys:\n  openai-compatibility:\n    - name: synthetic-ark\n      base-url: %s\n      disabled: true\n      keys: [{api-key: SYNTHETIC-INFERENCE-CANARY, disabled: true}]\n", port, filepath.ToSlash(auth), deny.URL)
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
	if err := f.db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: fmt.Sprintf("http://127.0.0.1:%d", port), ManagementKey: "synthetic-volc-core"}}); err != nil {
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

func (f *volcengineFixture) serve() {
	f.manager.AppContext().OfficialBalanceService.Querier = balance.NewWithNetwork(f.network)
	if os.Getenv("VOLCENGINE_BALANCE_UI_FIXTURE") != "" {
		f.server = officialBrowserServer(f.t, f.manager.Handler())
	} else {
		f.server = httptest.NewServer(f.manager.Handler())
	}
}
func (f *volcengineFixture) closeManager() {
	f.server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := f.manager.AppContext().FullRefreshService.Shutdown(ctx); err != nil {
		f.t.Error(err)
	}
}
func (f *volcengineFixture) restartManager() {
	f.t.Helper()
	cfg := f.manager.AppContext().Config
	f.closeManager()
	f.db.Close()
	f.db = f.newDB()
	f.manager = New(cfg, f.db, collector.NewManager(cfg, f.db))
	f.serve()
}
func (f *volcengineFixture) api(method, path string, value any, auth ...string) (int, []byte) {
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
	for _, canary := range []string{volcengineAK, volcengineSK, "SYNTHETIC-INFERENCE-CANARY", "synthetic-volc-core"} {
		if bytes.Contains(raw, []byte(canary)) {
			f.t.Fatal("secret appeared in public management readback")
		}
	}
	return res.StatusCode, raw
}
func (f *volcengineFixture) secret() string {
	raw, _ := json.Marshal(map[string]string{"accessKeyID": volcengineAK, "secretAccessKey": volcengineSK})
	return string(raw)
}
func (f *volcengineFixture) config(version int64, secret string) map[string]any {
	return map[string]any{"scope": f.binding.Scope, "targets": f.binding.Targets, "version": version, "adapter": volcengineAdapter,
		"volcengine": map[string]any{"accountID": "9007199254740993", "currency": "CNY", "unit": "major", "currencyUnitConfirmed": true, "accountBindingConfirmed": true, "proofReference": "synthetic-source-account-proof"}, "secret": secret}
}
func (f *volcengineFixture) status() ob.Status {
	f.t.Helper()
	binding, _ := json.Marshal(f.binding)
	code, raw := f.api("GET", balancePath+"?binding="+url.QueryEscape(string(binding)), nil)
	var status ob.Status
	if code != 200 || json.Unmarshal(raw, &status) != nil {
		f.t.Fatalf("status HTTP %d: %s", code, raw)
	}
	return status
}
func (f *volcengineFixture) query(version int64) ob.Job {
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

// External server-side verifier follows the published protocol, never calls
// production signing helpers and never prints the Authorization or key material.
func volcenginePublicRequestValid(r *http.Request) bool {
	date := r.Header.Get("X-Date")
	when, err := time.Parse("20060102T150405Z", date)
	if err != nil || time.Since(when) > time.Minute || time.Until(when) > time.Minute || r.TLS == nil || r.Host != "billing.volcengineapi.com" || r.Method != "GET" || r.URL.Path != "/" || r.URL.RawQuery != "Action=QueryBalanceAcct&Version=2022-01-01" {
		return false
	}
	content, err := io.ReadAll(io.LimitReader(r.Body, 1))
	if err != nil || len(content) != 0 {
		return false
	}
	scope := date[:8] + "/cn-beijing/billing/request"
	emptyBodyHash := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	canonical := strings.Join([]string{"GET", "/", "Action=QueryBalanceAcct&Version=2022-01-01", "host:billing.volcengineapi.com\nx-date:" + date + "\n", "host;x-date", emptyBodyHash}, "\n")
	hash := sha256.Sum256([]byte(canonical))
	toSign := "HMAC-SHA256\n" + date + "\n" + scope + "\n" + hex.EncodeToString(hash[:])
	key := []byte(volcengineSK)
	for _, part := range []string{date[:8], "cn-beijing", "billing", "request", toSign} {
		mac := hmac.New(sha256.New, key)
		mac.Write([]byte(part))
		key = mac.Sum(nil)
	}
	want := "HMAC-SHA256 Credential=" + volcengineAK + "/" + scope + ", SignedHeaders=host;x-date, Signature=" + hex.EncodeToString(key)
	return hmac.Equal([]byte(r.Header.Get("Authorization")), []byte(want))
}
