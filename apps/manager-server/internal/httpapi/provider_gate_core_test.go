package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

// All provider fixtures use the same ordinary-artifact qualification. A digest
// pin alone cannot turn a tagged, unrelated, or unbound build into acceptance.
func providerCoreArtifact(t *testing.T) (binary, digest, revision string) {
	t.Helper()
	binary = os.Getenv("PROVIDER82_CORE_BINARY")
	if binary == "" {
		t.Skip("requires a digest-pinned ordinary isolated Core")
	}
	raw, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	digest = hex.EncodeToString(sum[:])
	if digest != os.Getenv("PROVIDER82_CORE_SHA256") {
		t.Fatal("Core artifact SHA256 mismatch")
	}
	info, err := buildinfo.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	settings := map[string]string{}
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	revision = os.Getenv("PROVIDER82_CORE_REVISION")
	if revision == "" || info.Path != "github.com/router-for-me/CLIProxyAPI/v8/cmd/server" || settings["vcs.revision"] != revision || settings["vcs.modified"] != "false" || settings["-tags"] != "" || settings["-buildmode"] != "exe" {
		t.Fatal("ordinary Core artifact lacks the required clean source/module/build metadata")
	}
	return binary, digest, revision
}

// This is an opt-in isolated ordinary Core process, not a simulated gate ACK.
func TestProviderGateActualCorePublicPersistenceAndIsolation(t *testing.T) {
	binary, digest, expectedRevision := providerCoreArtifact(t)
	var mu sync.Mutex
	sends := map[string]int{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		sends[r.Header.Get("Authorization")]++
		mu.Unlock()
		fmt.Fprint(w, `{"id":"controlled","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer upstream.Close()
	deny := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(502) }))
	defer deny.Close()
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
	config := fmt.Sprintf("config-version: 8\nserver: {host: 127.0.0.1, port: %d}\nmanagement: {secret-key: controlled-core-key, disable-control-panel: true, disable-auto-update-panel: true}\noauth: {auth-dir: %q}\nresource-catalog: {enabled: true}\naccess: {api-keys: [controlled-client-key]}\nplugins: {enabled: false}\napi-keys:\n  openai-compatibility:\n    - name: provider-a\n      prefix: a\n      base-url: %q\n      models: [{name: controlled-model}]\n      keys: [{api-key: CONTROLLED-A1}, {api-key: CONTROLLED-A2}]\n    - name: provider-b\n      prefix: b\n      base-url: %q\n      models: [{name: controlled-model}]\n      keys: [{api-key: CONTROLLED-B1}, {api-key: CONTROLLED-B2}]\n", port, filepath.ToSlash(filepath.Join(dir, "auth")), upstream.URL+"/v1", upstream.URL+"/v1")
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	logs := os.Getenv("PROVIDER82_PROCESS_LOG_DIR")
	if logs == "" {
		logs = t.TempDir()
	}
	if err := os.MkdirAll(logs, 0700); err != nil {
		t.Fatal(err)
	}
	stopCore := startProviderGateCore(t, binary, configPath, dir, logs, "initial", deny.URL, digest, expectedRevision)
	defer func() { stopCore() }()
	coreBase := fmt.Sprintf("http://127.0.0.1:%d", port)
	var proofMode, operationCalls atomic.Int32
	var responseFault atomic.Int32
	var injectCAS atomic.Bool
	coreURL, err := url.Parse(coreBase)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(coreURL)
	proxy.ModifyResponse = func(res *http.Response) error {
		if res.Request.URL.Path == "/v8/management/inference-gates/operations" && res.StatusCode == 200 && responseFault.CompareAndSwap(1, 0) {
			_ = res.Body.Close()
			return fmt.Errorf("controlled lost operation ACK")
		}
		mode := proofMode.Load()
		if res.Request.URL.Path != "/v8/management/inference-gates/providers/eligibility" || mode == 0 || res.StatusCode != 200 {
			return nil
		}
		defer res.Body.Close()
		var proof map[string]any
		if err := json.NewDecoder(res.Body).Decode(&proof); err != nil {
			return err
		}
		members := proof["members"].([]any)
		switch mode {
		case 1:
			proof["members"] = members[:1]
		case 2:
			member := members[0].(map[string]any)
			member["routes"] = member["routes"].([]any)[:2]
		case 3:
			members[0].(map[string]any)["routes"].([]any)[0].(map[string]any)["supported"] = false
		case 4:
			proof["authority"] = "funding"
		}
		raw, err := json.Marshal(proof)
		if err != nil {
			return err
		}
		res.Body = io.NopCloser(bytes.NewReader(raw))
		res.ContentLength = int64(len(raw))
		res.Header.Set("Content-Length", fmt.Sprint(len(raw)))
		return nil
	}
	managementProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/v8/management/inference-gates/operations" {
			operationCalls.Add(1)
			if responseFault.CompareAndSwap(2, 0) {
				w.WriteHeader(503)
				return
			}
			if injectCAS.CompareAndSwap(true, false) {
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					w.WriteHeader(500)
					return
				}
				r.Body = io.NopCloser(bytes.NewReader(raw))
				var concurrent map[string]any
				if err := json.Unmarshal(raw, &concurrent); err != nil {
					t.Error(err)
					w.WriteHeader(500)
					return
				}
				concurrent["opId"], concurrent["owner"], concurrent["action"] = "d8f20c41-69df-4282-b8c8-73297b183c80", "controlled-other-owner", "clear"
				body, _ := json.Marshal(concurrent)
				req, _ := http.NewRequest("POST", coreBase+r.URL.Path, bytes.NewReader(body))
				req.Header = r.Header.Clone()
				res, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
				if err != nil {
					t.Error(err)
					w.WriteHeader(500)
					return
				}
				_ = res.Body.Close()
				if res.StatusCode != 200 {
					t.Errorf("controlled concurrent owner returned %d", res.StatusCode)
					w.WriteHeader(500)
					return
				}
			}
		}
		proxy.ServeHTTP(w, r)
	}))
	defer managementProxy.Close()
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("redirect refused") }}
	waitReady := func() {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for {
			req, _ := http.NewRequest("GET", coreBase+"/v8/management/resources", nil)
			req.Header.Set("Authorization", "Bearer controlled-core-key")
			res, err := client.Do(req)
			if err == nil {
				_ = res.Body.Close()
				if res.StatusCode == 200 {
					break
				}
			}
			if time.Now().After(deadline) {
				t.Fatal("isolated Core not ready; inspect captured core-main.log")
			}
			time.Sleep(15 * time.Millisecond)
		}
	}
	waitReady()
	cfg := testutil.NewConfig(t)
	protector, err := security.NewProtector([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(cfg.DBPath, protector)
	if err != nil {
		t.Fatal(err)
	}
	testutil.EnsureAdminCredential(t, db)
	t.Cleanup(func() { _ = db.Close() })
	if err := db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: managementProxy.URL, ManagementKey: "controlled-core-key"}}); err != nil {
		t.Fatal(err)
	}
	manager := New(cfg, db, collector.NewManager(cfg, db))
	api := func(method, path string, body any, want int) map[string]any {
		t.Helper()
		data := ""
		if body != nil {
			b, _ := json.Marshal(body)
			data = string(b)
		}
		r := testutil.Request(t, manager.Handler(), method, path, data, testutil.AdminKey)
		testutil.RequireStatus(t, r, want)
		if bytes.Contains(r.Body.Bytes(), []byte("CONTROLLED-A")) || bytes.Contains(r.Body.Bytes(), []byte("controlled-core-key")) {
			t.Fatal("provider response leaked credentials")
		}
		var value map[string]any
		testutil.DecodeJSON(t, r, &value)
		return value
	}
	view := api("GET", "/usage-service/resource-capabilities?inventory=1&bindings=1", nil, 200)
	var env struct{ Negotiated rc.Negotiated }
	b, _ := json.Marshal(view)
	if err := json.Unmarshal(b, &env); err != nil || env.Negotiated.Snapshot == nil {
		t.Fatal("missing actual issued inventory")
	}
	var binding ob.Binding
	var providerID string
	// Source names are intentionally absent. The group is selected by making a
	// valid observed request, then correlating a native marker in the test config.
	savedConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	// The catalog migrated a provider marker immediately under provider-a.
	start := strings.Index(string(savedConfig), "name: provider-a")
	end := strings.Index(string(savedConfig)[start:], "name: provider-b")
	section := string(savedConfig)[start : start+end]
	for _, row := range env.Negotiated.Snapshot.Resources {
		if row.Kind == "provider" && strings.Contains(section, row.ResourceID) {
			providerID = row.ResourceID
		}
	}
	if providerID == "" {
		t.Fatal("missing migrated provider A marker")
	}
	binding.Scope = env.Negotiated.Snapshot.Scope
	for _, row := range env.Negotiated.Snapshot.Resources {
		if row.ResourceID == providerID || row.ParentID == providerID {
			binding.Targets = append(binding.Targets, row.Target)
		}
	}
	sort.Slice(binding.Targets, func(i, j int) bool { return binding.Targets[i].ResourceID < binding.Targets[j].ResourceID })
	if len(binding.Targets) != 3 {
		t.Fatal("provider A is not a complete two-member group")
	}
	query := map[string]any{"scope": binding.Scope, "targets": binding.Targets, "providerId": providerID}
	encoded, _ := json.Marshal(query)
	path := "/usage-service/provider-gate?binding=" + url.QueryEscape(string(encoded))
	status := api("GET", path, nil, 200)
	if status["generation"] != float64(0) || status["applied"] != false || status["ready"] != true {
		t.Fatalf("default provider gate is not unpaused/eligible: %+v", status)
	}
	send := func(prefix string, want int) {
		t.Helper()
		req, _ := http.NewRequest("POST", coreBase+"/v1/chat/completions", strings.NewReader(fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"controlled"}]}`, prefix+"/controlled-model")))
		req.Header.Set("Authorization", "Bearer controlled-client-key")
		req.Header.Set("Content-Type", "application/json")
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if want == 200 && (res.StatusCode != 200 || !bytes.Contains(body, []byte("ok"))) || want != 200 && res.StatusCode == 200 {
			t.Fatalf("inference %s status=%d want=%d body=%s", prefix, res.StatusCode, want, body)
		}
	}
	for range 4 {
		send("a", 200)
	}
	for range 4 {
		send("b", 200)
	}
	mu.Lock()
	for _, key := range []string{"Bearer CONTROLLED-A1", "Bearer CONTROLLED-A2", "Bearer CONTROLLED-B1", "Bearer CONTROLLED-B2"} {
		if sends[key] == 0 {
			mu.Unlock()
			t.Fatalf("member not exercised before pause: %s counts=%+v", key, sends)
		}
	}
	beforeA := sends["Bearer CONTROLLED-A1"] + sends["Bearer CONTROLLED-A2"]
	beforeB := sends["Bearer CONTROLLED-B1"] + sends["Bearer CONTROLLED-B2"]
	mu.Unlock()
	update := map[string]any{"version": 1, "scope": binding.Scope, "targets": binding.Targets, "providerId": providerID, "sourceVersion": status["sourceVersion"], "expectedGeneration": 0, "action": "pause"}
	t.Run("authorization_and_issued_identity", func(t *testing.T) {
		r := testutil.Request(t, manager.Handler(), "GET", path, "", "")
		testutil.RequireStatus(t, r, 401)
		update["supported"] = true
		api("PUT", "/usage-service/provider-gate", update, 400)
		delete(update, "supported")
		update["targets"] = binding.Targets[:2]
		api("PUT", "/usage-service/provider-gate", update, 409)
		update["targets"] = binding.Targets
		update["sourceVersion"] = strings.Repeat("0", 64)
		api("PUT", "/usage-service/provider-gate", update, 409)
		update["sourceVersion"] = status["sourceVersion"]
	})
	for i, name := range []string{"missing_member", "missing_route", "unsupported_route", "wrong_authority"} {
		t.Run(name+"_cannot_write", func(t *testing.T) {
			proofMode.Store(int32(i + 1))
			defer proofMode.Store(0)
			api("PUT", "/usage-service/provider-gate", update, 503)
		})
	}
	if operationCalls.Load() != 0 || api("GET", path, nil, 200)["generation"] != float64(0) {
		t.Fatal("rejected proof wrote an operation or SQLite intent")
	}
	var paused map[string]any
	t.Run("concurrent_managers_share_generation_cas", func(t *testing.T) {
		otherStore, err := store.Open(cfg.DBPath, protector)
		if err != nil {
			t.Fatal(err)
		}
		defer otherStore.Close()
		otherManager := New(cfg, otherStore, collector.NewManager(cfg, otherStore))
		body, _ := json.Marshal(update)
		start := make(chan struct{})
		results := make(chan *httptest.ResponseRecorder, 2)
		for _, server := range []*Server{manager, otherManager} {
			go func() {
				<-start
				results <- testutil.Request(t, server.Handler(), "PUT", "/usage-service/provider-gate", string(body), testutil.AdminKey)
			}()
		}
		close(start)
		counts := map[int]int{}
		for range 2 {
			r := <-results
			counts[r.Code]++
			if r.Code == 200 {
				testutil.DecodeJSON(t, r, &paused)
			}
		}
		if counts[200] != 1 || counts[409] != 1 || operationCalls.Load() != 1 {
			t.Fatalf("concurrent provider writes: status=%v operations=%d", counts, operationCalls.Load())
		}
	})
	if paused["applied"] != true || paused["ownPaused"] != true || paused["generation"] != float64(1) {
		t.Fatalf("no provider current applied ACK: %+v", paused)
	}
	t.Run("stale_generation_cannot_replace_pause", func(t *testing.T) {
		api("PUT", "/usage-service/provider-gate", update, 409)
		if operationCalls.Load() != 1 {
			t.Fatal("stale generation sent another Core operation")
		}
	})
	for range 4 {
		send("a", 503)
		send("b", 200)
	}
	mu.Lock()
	afterA := sends["Bearer CONTROLLED-A1"] + sends["Bearer CONTROLLED-A2"]
	afterB := sends["Bearer CONTROLLED-B1"] + sends["Bearer CONTROLLED-B2"]
	mu.Unlock()
	if afterA != beforeA || afterB != beforeB+4 {
		t.Fatalf("provider isolation failed A=%d->%d B=%d->%d", beforeA, afterA, beforeB, afterB)
	}
	// Reopen the temporary SQLite and construct a fresh Manager. Readback must
	// retain the operation identity and confirm the current real Core ledger.
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(cfg.DBPath, protector)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	manager = New(cfg, db, collector.NewManager(cfg, db))
	restored := api("GET", path, nil, 200)
	if restored["generation"] != paused["generation"] || restored["owner"] != paused["owner"] || restored["applied"] != true || restored["ownPaused"] != true {
		t.Fatalf("SQLite restart lost current provider restriction: %+v", restored)
	}
	var billingCalls atomic.Int32
	billing := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		billingCalls.Add(1)
		fmt.Fprint(w, `{"is_available":true,"balance_infos":[{"currency":"USD","total_balance":"0.0500","granted_balance":"0","topped_up_balance":"0.0500"}]}`)
	}))
	defer billing.Close()
	roots := x509.NewCertPool()
	roots.AddCert(billing.Certificate())
	manager.AppContext().OfficialBalanceService.Querier = balance.NewWithNetwork(balance.NetworkOptions{RootCAs: roots})
	api("PUT", balancePath, ob.Update{Config: ob.Config{Binding: binding, Adapter: "deepseek", Endpoint: billing.URL + "/user/balance", PrivateTargets: []string{"127.0.0.1"}}, Secret: "controlled-billing-key"}, 200)
	job := api("POST", balancePath, ob.Query{Binding: binding, Version: 1}, 202)
	deadline := time.Now().Add(6 * time.Second)
	for {
		state := api("GET", balancePath+"?job="+job["id"].(string), nil, 200)
		if state["state"] != "running" {
			status, ok := state["status"].(map[string]any)
			if !ok || status["state"] != "success" || billingCalls.Load() != 1 {
				t.Fatalf("paused provider billing was unavailable: %+v", state)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("controlled balance job did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if api("GET", path, nil, 200)["ownPaused"] != true {
		t.Fatal("billing observation implicitly cleared provider gate")
	}
	t.Run("current_pause_survives_actual_core_restart", func(t *testing.T) {
		stopCore()
		stopCore = startProviderGateCore(t, binary, configPath, dir, logs, "restarted", deny.URL, digest, expectedRevision)
		waitReady()
		api("GET", path, nil, 409)
		current := api("GET", "/usage-service/resource-capabilities?inventory=1&bindings=1", nil, 200)
		raw, _ := json.Marshal(current)
		var snapshot struct{ Negotiated rc.Negotiated }
		if json.Unmarshal(raw, &snapshot) != nil || snapshot.Negotiated.Snapshot == nil {
			t.Fatal("no restart inventory")
		}
		oldScope := binding.Scope
		binding.Scope = snapshot.Negotiated.Snapshot.Scope
		if oldScope.BootEpoch == binding.Scope.BootEpoch {
			t.Fatal("Core did not restart")
		}
		query["scope"], update["scope"] = binding.Scope, binding.Scope
		raw, _ = json.Marshal(query)
		path = "/usage-service/provider-gate?binding=" + url.QueryEscape(string(raw))
		current = api("GET", path, nil, 200)
		if current["applied"] != true || current["ownPaused"] != true || current["generation"] != float64(1) {
			t.Fatalf("current restart protection unconfirmed: %+v", current)
		}
		update["sourceVersion"] = current["sourceVersion"]
		send("a", 503)
		send("b", 200)
	})
	update["expectedGeneration"], update["action"] = 1, "clear"
	t.Run("definite_revision_conflict_can_be_reconciled", func(t *testing.T) {
		injectCAS.Store(true)
		api("PUT", "/usage-service/provider-gate", update, 409)
		pending := api("GET", path, nil, 200)
		if pending["applied"] != false || pending["ownPaused"] != true || pending["generation"] != float64(2) {
			t.Fatalf("conflict claimed applied: %+v", pending)
		}
		send("a", 503)
		update["expectedGeneration"] = 2
	})
	cleared := api("PUT", "/usage-service/provider-gate", update, 200)
	if cleared["applied"] != true || cleared["ownPaused"] != false || cleared["generation"] != float64(3) {
		t.Fatalf("explicit own clear not confirmed: %+v", cleared)
	}
	send("a", 200)
	t.Run("lost_ack_recovers_by_read_without_replay", func(t *testing.T) {
		update["expectedGeneration"], update["action"] = 3, "pause"
		responseFault.Store(1)
		api("PUT", "/usage-service/provider-gate", update, 503)
		calls := operationCalls.Load()
		current := api("GET", path, nil, 200)
		if current["applied"] != true || current["ownPaused"] != true || current["generation"] != float64(4) || operationCalls.Load() != calls {
			t.Fatalf("lost ACK did not recover current durable restriction by read: %+v", current)
		}
		send("a", 503)
		update["expectedGeneration"], update["action"] = 4, "clear"
		api("PUT", "/usage-service/provider-gate", update, 200)
	})
	t.Run("missing_operation_at_same_revision_stays_unknown", func(t *testing.T) {
		update["expectedGeneration"], update["action"] = 5, "pause"
		responseFault.Store(2)
		api("PUT", "/usage-service/provider-gate", update, 503)
		calls := operationCalls.Load()
		current := api("GET", path, nil, 200)
		if current["applied"] != false || current["state"] != "pending" || current["generation"] != float64(6) {
			t.Fatalf("unknown outcome claimed current protection: %+v", current)
		}
		update["expectedGeneration"], update["action"] = 6, "clear"
		api("PUT", "/usage-service/provider-gate", update, 503)
		if operationCalls.Load() != calls {
			t.Fatal("unknown operation was bypassed or replayed")
		}
	})
	t.Logf("PROVIDER_GATE_CHAIN_PASS A members=2 B members=2; A sends unchanged=%d after pause; B +4; SQLite reopened; billing calls=1; explicit clear confirmed; controlled fixture only, no funding or monetary automation proof", beforeA)
}

func startProviderGateCore(t *testing.T, binary, configPath, dir, logs, phase, denyURL, digest, revision string) func() {
	t.Helper()
	log, err := os.Create(filepath.Join(logs, phase+"-core-main.log"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "-config", configPath, "-local-model")
	cmd.Dir, cmd.Stdout, cmd.Stderr = dir, log, log
	for _, key := range []string{"PATH", "SystemRoot", "WINDIR", "TEMP", "TMP", "COMSPEC"} {
		if value := os.Getenv(key); value != "" {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	cmd.Env = append(cmd.Env, "HOME="+dir, "USERPROFILE="+dir, "APPDATA="+dir, "LOCALAPPDATA="+dir, "HTTP_PROXY="+denyURL, "HTTPS_PROXY="+denyURL, "NO_PROXY=127.0.0.1,localhost")
	if err := cmd.Start(); err != nil {
		_ = log.Close()
		t.Fatal(err)
	}
	var stopped atomic.Bool
	return func() {
		if !stopped.CompareAndSwap(false, true) {
			return
		}
		_ = cmd.Process.Kill()
		waitErr := cmd.Wait()
		_ = log.Close()
		exitCode := 0
		if failure, ok := waitErr.(*exec.ExitError); ok {
			exitCode = failure.ExitCode()
		}
		process, _ := json.MarshalIndent(map[string]any{"pid": cmd.Process.Pid, "command": cmd.Args, "artifactSHA256": digest, "exitCode": exitCode, "requestedStop": "owned_forced_fixture_cleanup", "ordinaryCore": true, "sourceCommit": revision}, "", "  ")
		if err := os.WriteFile(filepath.Join(logs, phase+"-PROCESS.json"), append(process, '\n'), 0600); err != nil {
			t.Error(err)
		}
		t.Logf("CONTROLLED_CORE_PROCESS pid=%d sha256=%s owned forced cleanup=%v", cmd.Process.Pid, digest, waitErr)
	}
}
