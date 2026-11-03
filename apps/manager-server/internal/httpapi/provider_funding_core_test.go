package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

// Funding claims are observed through public APIs and the actual Core ledger.
// This fixture neither authenticates a real account nor queries the Internet.
func TestProviderFundingMissingProofCannotGateGroup(t *testing.T) {
	f := newProviderFundingFixture(t)
	state := f.api("GET", f.policyPath(), nil, 200)
	before := f.ledger()
	if state["enabled"] != false || state["locked"] != true {
		t.Fatalf("missing funding proof is not locked OFF: %+v", state)
	}
	// Persist an explicit OFF first so reopening SQLite checks a real saved
	// intent rather than merely reading the implicit default a second time.
	q := map[string]any{"providerId": f.providerID, "scope": state["scope"], "targets": state["targets"], "expectedGeneration": 0, "enabled": false}
	state = f.api("PUT", "/usage-service/provider-policy", q, 200)
	q["expectedGeneration"], q["enabled"] = 1, true
	f.api("PUT", "/usage-service/provider-policy", q, 503)
	f.reopen()
	current := f.api("GET", f.policyPath(), nil, 200)
	if current["generation"] != float64(1) || current["enabled"] != false || current["owner"] != state["owner"] {
		t.Fatalf("rejected missing proof altered durable intent: %+v", current)
	}
	if f.operations.Load() != 0 || !reflect.DeepEqual(before, f.ledger()) {
		t.Fatal("missing funding proof changed the real Core ledger")
	}
}

type providerFundingFixture struct {
	t          *testing.T
	manager    *Server
	db         *store.Store
	providerID string
	base       string
	operations atomic.Int32
	reopen     func()
}

func (f *providerFundingFixture) policyPath() string {
	return "/usage-service/provider-policy?providerId=" + f.providerID
}

func (f *providerFundingFixture) api(method, path string, body any, want int) map[string]any {
	f.t.Helper()
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			f.t.Fatal(err)
		}
	}
	r := testutil.Request(f.t, f.manager.Handler(), method, path, string(data), testutil.AdminKey)
	testutil.RequireStatus(f.t, r, want)
	if bytes.Contains(r.Body.Bytes(), []byte("FUNDING-SYNTHETIC-")) || bytes.Contains(r.Body.Bytes(), []byte("controlled-core-key")) {
		f.t.Fatal("funding response leaked a credential")
	}
	var out map[string]any
	testutil.DecodeJSON(f.t, r, &out)
	return out
}

func (f *providerFundingFixture) ledger() map[string]any {
	f.t.Helper()
	req, err := http.NewRequest("GET", f.base+"/v8/management/inference-gates", nil)
	if err != nil {
		f.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer controlled-core-key")
	res, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer res.Body.Close()
	var ledger map[string]any
	if res.StatusCode != 200 || json.NewDecoder(res.Body).Decode(&ledger) != nil {
		f.t.Fatal("actual Core ledger unavailable")
	}
	return ledger
}

func newProviderFundingFixture(t *testing.T) *providerFundingFixture {
	t.Helper()
	binary, hexDigest, revision := providerCoreArtifact(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "auth"), 0700); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	configPath := filepath.Join(dir, "config.yaml")
	config := fmt.Sprintf("config-version: 8\nserver: {host: 127.0.0.1, port: %d}\nmanagement: {secret-key: controlled-core-key, disable-control-panel: true, disable-auto-update-panel: true}\noauth: {auth-dir: %q}\nresource-catalog: {enabled: true}\naccess: {api-keys: [FUNDING-SYNTHETIC-CLIENT]}\nplugins: {enabled: false}\napi-keys:\n  openai-compatibility:\n    - name: funding-a\n      base-url: https://api.deepseek.com/v1\n      models: [{name: controlled-model}]\n      keys: [{api-key: FUNDING-SYNTHETIC-A1}, {api-key: FUNDING-SYNTHETIC-A2}]\n    - name: funding-b\n      base-url: https://api.deepseek.com/v1\n      models: [{name: controlled-model}]\n      keys: [{api-key: FUNDING-SYNTHETIC-B1}]\n", port, filepath.ToSlash(filepath.Join(dir, "auth")))
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	deny := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(502) }))
	t.Cleanup(deny.Close)
	logs := os.Getenv("PROVIDER82_PROCESS_LOG_DIR")
	if logs == "" {
		logs = t.TempDir()
	}
	logs = filepath.Join(logs, t.Name())
	if err := os.MkdirAll(logs, 0700); err != nil {
		t.Fatal(err)
	}
	stop := startProviderGateCore(t, binary, configPath, dir, logs, "funding", deny.URL, hexDigest, revision)
	t.Cleanup(stop)
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{Timeout: time.Second}
	for deadline := time.Now().Add(15 * time.Second); ; {
		req, _ := http.NewRequest("GET", base+"/v8/management/resources", nil)
		req.Header.Set("Authorization", "Bearer controlled-core-key")
		res, err := client.Do(req)
		if err == nil {
			_ = res.Body.Close()
			if res.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("isolated Core readiness timeout")
		}
		time.Sleep(20 * time.Millisecond)
	}
	f := &providerFundingFixture{t: t, base: base}
	u, _ := url.Parse(base)
	proxy := httputil.NewSingleHostReverseProxy(u)
	management := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/v8/management/inference-gates/operations" {
			f.operations.Add(1)
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(management.Close)
	cfg := testutil.NewConfig(t)
	protector, err := security.NewProtector([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	f.db, err = store.Open(cfg.DBPath, protector)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.db.Close() })
	testutil.EnsureAdminCredential(t, f.db)
	if err := f.db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: management.URL, ManagementKey: "controlled-core-key"}}); err != nil {
		t.Fatal(err)
	}
	f.manager = New(cfg, f.db, collector.NewManager(cfg, f.db))
	f.reopen = func() {
		t.Helper()
		if err := f.db.Close(); err != nil {
			t.Fatal(err)
		}
		f.db, err = store.Open(cfg.DBPath, protector)
		if err != nil {
			t.Fatal(err)
		}
		f.manager = New(cfg, f.db, collector.NewManager(cfg, f.db))
	}
	inv := f.api("GET", "/usage-service/resource-capabilities?inventory=1&bindings=1", nil, 200)
	var envelope struct{ Negotiated rc.Negotiated }
	encoded, _ := json.Marshal(inv)
	if json.Unmarshal(encoded, &envelope) != nil || envelope.Negotiated.Snapshot == nil {
		t.Fatal("actual issued inventory unavailable")
	}
	saved, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(saved), "name: funding-a")
	end := strings.Index(string(saved)[start:], "name: funding-b")
	section := string(saved)[start : start+end]
	for _, row := range envelope.Negotiated.Snapshot.Resources {
		if row.Kind == "provider" && strings.Contains(section, row.ResourceID) {
			f.providerID = row.ResourceID
		}
	}
	if f.providerID == "" {
		t.Fatal("actual issued funding-a provider missing")
	}
	return f
}
