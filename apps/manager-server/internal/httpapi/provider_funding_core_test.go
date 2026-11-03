package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
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
	"sync/atomic"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
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
	t                *testing.T
	manager          *Server
	db               *store.Store
	providerID       string
	base             string
	members          map[string]ob.Binding
	operations       atomic.Int32
	keyMatches       atomic.Int32
	keyRejected      atomic.Int32
	proofFault       atomic.Int32
	upstreamAttempts atomic.Int32
	beforeMatch      func()
	reopen           func()
	config           config.Config
	apiBase          string
	loseAck          atomic.Bool
	hideReceipt      atomic.Bool
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
	var r *httptest.ResponseRecorder
	if f.apiBase == "" {
		r = testutil.Request(f.t, f.manager.Handler(), method, path, string(data), testutil.AdminKey)
	} else {
		req, _ := http.NewRequest(method, f.apiBase+path, bytes.NewReader(data))
		req.Header.Set("Authorization", "Bearer "+testutil.AdminKey)
		req.Header.Set("Content-Type", "application/json")
		res, err := (&http.Client{Timeout: 25 * time.Second}).Do(req)
		if err != nil {
			f.t.Fatal(err)
		}
		defer res.Body.Close()
		r = httptest.NewRecorder()
		r.WriteHeader(res.StatusCode)
		_, _ = io.Copy(r, res.Body)
	}
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
	config := fmt.Sprintf("config-version: 8\nserver: {host: 127.0.0.1, port: %d}\nmanagement: {secret-key: controlled-core-key, disable-control-panel: true, disable-auto-update-panel: true}\noauth: {auth-dir: %q}\nresource-catalog: {enabled: true}\naccess: {api-keys: [FUNDING-SYNTHETIC-CLIENT]}\nplugins: {enabled: false}\napi-keys:\n  openai-compatibility:\n    - name: funding-a\n      prefix: a\n      base-url: https://api.deepseek.com/v1\n      models: [{name: controlled-model}]\n      keys: [{api-key: FUNDING-SYNTHETIC-A1}, {api-key: FUNDING-SYNTHETIC-A2}]\n    - name: funding-b\n      prefix: b\n      base-url: https://api.deepseek.com/v1\n      models: [{name: controlled-model}]\n      keys: [{api-key: FUNDING-SYNTHETIC-B1}]\n", port, filepath.ToSlash(filepath.Join(dir, "auth")))
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	f := &providerFundingFixture{t: t}
	deny := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.upstreamAttempts.Add(1); w.WriteHeader(502) }))
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
	f.base = base
	u, _ := url.Parse(base)
	proxy := httputil.NewSingleHostReverseProxy(u)
	proxy.ModifyResponse = func(res *http.Response) error {
		path := res.Request.URL.Path
		if path == "/v8/management/inference-gates/operations" && f.loseAck.CompareAndSwap(true, false) {
			_ = res.Body.Close()
			res.StatusCode = 503
			res.Body = io.NopCloser(strings.NewReader(`{"error":"controlled_lost_ack"}`))
			res.ContentLength = -1
			res.Header.Del("Content-Length")
			return nil
		}
		if res.StatusCode != 200 || path != "/v8/management/inference-gates/providers/key-match" && path != "/v8/management/inference-gates/providers/eligibility" {
			return nil
		}
		defer res.Body.Close()
		var body map[string]any
		decoder := json.NewDecoder(res.Body)
		decoder.UseNumber()
		if err := decoder.Decode(&body); err != nil {
			return err
		}
		fault := f.proofFault.Load()
		if path == "/v8/management/inference-gates/providers/key-match" {
			f.keyMatches.Add(1)
			if body["matched"] == false {
				f.keyRejected.Add(1)
			}
			switch fault {
			case 2:
				body["nonce"] = "00000000-0000-4000-8000-000000000000"
			case 3:
				body["sourceVersion"] = strings.Repeat("0", 64)
			case 4:
				body["authority"] = "funding"
			}
		} else if fault == 1 {
			body["members"] = body["members"].([]any)[:1]
		}
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		res.Body = io.NopCloser(bytes.NewReader(raw))
		res.ContentLength = int64(len(raw))
		res.Header.Set("Content-Length", fmt.Sprint(len(raw)))
		return nil
	}
	management := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v8/management/inference-gates/operations/") && f.hideReceipt.Load() {
			w.WriteHeader(503)
			return
		}
		// Fault injection edits JSON, so request its identity representation.
		// Production management transport and Core compression stay unchanged.
		r.Header.Set("Accept-Encoding", "identity")
		if r.Method == "POST" && r.URL.Path == "/v8/management/inference-gates/operations" {
			f.operations.Add(1)
		}
		if r.URL.Path == "/v8/management/inference-gates/providers/key-match" && f.beforeMatch != nil {
			f.beforeMatch()
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(management.Close)
	cfg := testutil.NewConfig(t)
	f.config = cfg
	protector, err := security.NewProtector([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	f.db, err = store.Open(cfg.DBPath, protector)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.shutdown(); _ = f.db.Close() })
	testutil.EnsureAdminCredential(t, f.db)
	if err := f.db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: management.URL, ManagementKey: "controlled-core-key"}}); err != nil {
		t.Fatal(err)
	}
	f.manager = New(cfg, f.db, collector.NewManager(cfg, f.db))
	f.reopen = func() {
		f.t.Helper()
		f.shutdown()
		if err := f.db.Close(); err != nil {
			f.t.Fatal(err)
		}
		f.db, err = store.Open(cfg.DBPath, protector)
		if err != nil {
			f.t.Fatal(err)
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
	f.members = map[string]ob.Binding{}
	for _, key := range []string{"FUNDING-SYNTHETIC-A1", "FUNDING-SYNTHETIC-A2"} {
		start := strings.Index(string(saved), "api-key: "+key)
		if start < 0 {
			t.Fatal("native fixture credential missing")
		}
		section := string(saved)[start:]
		marker := strings.Index(section, "resource-id:")
		if marker < 0 {
			t.Fatal("migrated member marker missing")
		}
		value := strings.TrimSpace(section[marker+len("resource-id:"):])
		if len(value) < 36 {
			t.Fatal("migrated member marker truncated")
		}
		id := value[:36]
		for _, row := range envelope.Negotiated.Snapshot.Resources {
			if row.ResourceID == id && row.ParentID == f.providerID {
				f.members[key] = ob.Binding{Scope: envelope.Negotiated.Snapshot.Scope, Targets: []rc.Target{row.Target}}
			}
		}
	}
	if len(f.members) != 2 {
		t.Fatal("expected both current issued members")
	}
	return f
}

func (f *providerFundingFixture) shutdown() {
	f.t.Helper()
	if f.manager == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := f.manager.AppContext().FullRefreshService.Shutdown(ctx); err != nil {
		f.t.Fatal(err)
	}
}
