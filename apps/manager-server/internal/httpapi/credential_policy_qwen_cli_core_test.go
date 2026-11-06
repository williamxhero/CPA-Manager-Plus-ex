package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
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
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	cp "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy"
	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	fr "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fullrefresh"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

const qwenPolicyModel = "synthetic-qwen-policy-model"

// Reuse the existing opt-in artifact verifier, actual Manager API fixture,
// temporary SQLite and production policy worker. Core runs its real production
// bootstrap, including the actual Qwen DLL; no query/gate/router is substituted.
type qwenCLIPolicyFixture struct {
	*nativeAdmissionFixture
	dir, evidence, responseFile, callsFile, cache, cli, helper, mode string
	sends, denied                                                    atomic.Int64
	sequence                                                         int
}

func TestCredentialPolicyQwenConfiguredCLIActualCore(t *testing.T) {
	if os.Getenv("SPEC92_QWEN_INTEGRATION") != "1" {
		t.Skip("opt-in: run the bounded qwen-policy-integration runner with verified development artifacts")
	}
	t.Run("unenrolled", func(t *testing.T) {
		f := newQwenCLIPolicyFixture(t, "live")
		source := f.sources["A"]
		if source.State != "needs_configuration" || source.Reason != "account_authority_unverified" || source.AccountProvenance != "unverified" {
			t.Fatalf("unenrolled source gained CLI authority: %+v", source)
		}
		// A real acquisition request must also fail closed, not only the GET.
		observed := f.refresh("unenrolled-query", "needs_configuration")
		if observed.LastSuccess != nil {
			t.Fatal("unenrolled query acquired policy evidence")
		}
		var rejected map[string]any
		generation, enabled := int64(0), true
		f.api("PUT", "/usage-service/credential-policy", cp.Update{Binding: source.Binding, AuthIndex: source.AuthIndex, SourceVersion: source.SourceVersion, ExpectedGeneration: &generation, Enabled: &enabled}, 503, &rejected)
		if rejected["error"] == "" || rejected["error"] == nil {
			t.Fatal("ON failure has no diagnostic")
		}
		status := f.status("A")
		if status.Enabled || status.Generation != 0 || status.Applied || status.Operation != nil {
			t.Fatal("unenrolled failed ON persisted intent/action")
		}
		var gates cp.GateSnapshot
		f.coreAPI("GET", "inference-gates", nil, &gates)
		if !gates.Ready || gates.GateRevision != 0 || len(gates.Gates) != 0 {
			t.Fatalf("unenrolled case requires a real ready empty gate, not capability failure: %+v", gates)
		}
		if f.cliCalls() != 0 {
			t.Fatal("unenrolled Core invoked the configured CLI")
		}
		if _, err := os.Stat(f.cache); !os.IsNotExist(err) {
			t.Fatal("unenrolled Core produced a cache")
		}
		f.send(false, true)
		f.persist("unenrolled-final", status)
		f.save("unenrolled-assertions.json", map[string]any{"cliInvocations": f.cliCalls(), "businessSends": f.sends.Load(), "gateRevision": gates.GateRevision, "failedON": rejected})
	})
	for _, mode := range []string{"live", "cache_only"} {
		t.Run("enrolled_"+mode, func(t *testing.T) {
			f := newQwenCLIPolicyFixture(t, mode)
			f.enroll()
			// Prerequisite is an original API-started job while intent is OFF.
			if mode == "cache_only" {
				f.produce("prerequisite")
			}
			before := f.refresh("prerequisite", "success")
			f.requireEvidence(before, "recover")
			initial := f.status("A")
			generation, enabled := initial.Generation, true
			var armed cp.Status
			f.api("PUT", "/usage-service/credential-policy", cp.Update{Binding: initial.Binding, AuthIndex: initial.AuthIndex, SourceVersion: initial.SourceVersion, ExpectedGeneration: &generation, Enabled: &enabled}, 200, &armed)
			if !armed.Enabled || armed.Generation != generation+1 || armed.Operation != nil {
				t.Fatal("actual original query did not qualify independent ON")
			}
			f.persist("armed", armed)
			f.snapshotRuntime("before-first-inference")
			f.send(false, true)
			f.snapshotRuntime("after-first-inference")
			f.writeCLIResponse(100)
			if mode == "cache_only" {
				f.produce("depleted")
			}
			depleted := f.refresh("depleted", "success")
			f.requireEvidence(depleted, "pause")
			// The original API-started acquisition already discharged the ON
			// scheduling hint. Startup consumes its durable completion, without
			// racing a separate first-cycle query against this selected job.
			f.startPolicyWorker()
			paused := f.waitOperation("pause")
			f.persist("paused", paused)
			var pausedGates cp.GateSnapshot
			f.coreAPI("GET", "inference-gates", nil, &pausedGates)
			f.save("paused-core-gates.json", pausedGates)
			if !pausedGates.Ready || pausedGates.GateRevision != 1 || len(pausedGates.Gates) != 1 || pausedGates.Gates[0].Owner != paused.Owner || !reflect.DeepEqual(pausedGates.Gates[0].Targets, paused.Targets) || pausedGates.Gates[0].SourceVersion != paused.SourceVersion || pausedGates.Gates[0].Applicability != "current" {
				t.Fatal("actual Core ledger lacks the exact current policy-owned pause")
			}
			sent := f.sends.Load()
			f.send(false, false)
			f.send(true, false)
			if f.sends.Load() != sent {
				t.Fatal("actual paused Qwen credential admitted new HTTP/SSE")
			}
			f.writeCLIResponse(25)
			if mode == "cache_only" {
				f.produce("recovered")
			}
			recovered := f.refresh("recovered", "success")
			f.requireEvidence(recovered, "recover")
			cleared := f.waitOperation("clear")
			if cleared.Owner != paused.Owner || cleared.Generation != paused.Generation || !cleared.Enabled || !reflect.DeepEqual(cleared.Targets, paused.Targets) || cleared.SourceVersion != paused.SourceVersion || cleared.Operation.OpID == paused.Operation.OpID {
				t.Fatal("recovery changed policy owner, intent, authority or operation identity")
			}
			f.send(false, true)
			f.persist("recovered", cleared)
			var gates cp.GateSnapshot
			f.coreAPI("GET", "inference-gates", nil, &gates)
			f.save("recovered-core-gates.json", gates)
			if !gates.Ready || gates.GateRevision != 2 || len(gates.Gates) != 0 || f.sends.Load() != 2 || f.cliCalls() != 3 {
				t.Fatalf("unexpected actual end state: gates=%+v sends=%d CLI=%d", gates, f.sends.Load(), f.cliCalls())
			}
			f.save("positive-assertions.json", map[string]any{"mode": mode, "cliInvocations": f.cliCalls(), "businessSends": f.sends.Load(), "gateRevision": gates.GateRevision, "policyGeneration": cleared.Generation, "ownerPreserved": true, "ordinaryAndSSEBlockedWhilePaused": true, "explicitProducerOutsideCacheRead": mode == "cache_only"})
		})
	}
}

func newQwenCLIPolicyFixture(t *testing.T, mode string) *qwenCLIPolicyFixture {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Fatal("this verified DLL/controlled .cmd development cohort requires Windows")
	}
	for _, item := range []struct{ path, sha, revision string }{
		{os.Getenv("SPEC92_QWEN_CORE"), os.Getenv("SPEC92_QWEN_CORE_SHA256"), os.Getenv("SPEC92_QWEN_CORE_REVISION")},
		{os.Getenv("SPEC92_QWEN_PLUGIN"), os.Getenv("SPEC92_QWEN_PLUGIN_SHA256"), os.Getenv("SPEC92_QWEN_PLUGIN_REVISION")},
		{os.Getenv("SPEC92_QWEN_HELPER"), os.Getenv("SPEC92_QWEN_HELPER_SHA256"), os.Getenv("SPEC92_QWEN_PLUGIN_REVISION")},
	} {
		verifiedRefreshArtifact(t, item.path, item.sha, item.revision)
	}
	dir := t.TempDir()
	name := t.Name()[strings.LastIndex(t.Name(), "/")+1:]
	evidence := filepath.Join(os.Getenv("SPEC92_QWEN_EVIDENCE"), name)
	if evidence == "" || os.Getenv("SPEC92_QWEN_EVIDENCE") == "" {
		t.Fatal("explicit isolated evidence directory required")
	}
	if err := os.MkdirAll(evidence, 0700); err != nil {
		t.Fatal(err)
	}
	f := &qwenCLIPolicyFixture{
		nativeAdmissionFixture: &nativeAdmissionFixture{t: t, client: &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 15 * time.Second}, sources: map[string]cq.Source{}, completed: make(chan fr.CompletedObservation, 16)},
		dir:                    dir, evidence: evidence, mode: mode, helper: os.Getenv("SPEC92_QWEN_HELPER"),
		responseFile: filepath.Join(dir, "cli-response.json"), callsFile: filepath.Join(dir, "cli-calls.log"),
		cache: filepath.Join(dir, "coding-plan.json"), cli: filepath.Join(dir, "synthetic-bl.cmd"),
	}
	t.Cleanup(f.client.CloseIdleConnections)
	f.writeCLIResponse(25)
	// The controlled CLI uses shell builtins only: no installed CLI/profile,
	// credential file, login/renewal command, Node, or network implementation.
	shim := fmt.Sprintf("@echo off\r\nif not \"%%~1\"==\"usage\" exit /b 2\r\nif not \"%%~2\"==\"coding-plan\" exit /b 2\r\nif not \"%%~3\"==\"--output\" exit /b 2\r\nif not \"%%~4\"==\"json\" exit /b 2\r\nif not \"%%~5\"==\"\" exit /b 2\r\n>>\"%s\" echo usage coding-plan --output json\r\ntype \"%s\"\r\nexit /b 0\r\n", f.callsFile, f.responseFile)
	if err := os.WriteFile(f.cli, []byte(shim), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(evidence, "synthetic-bl.cmd"), []byte(shim), 0600); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			fmt.Fprintf(w, `{"object":"list","data":[{"id":%q,"object":"model","owned_by":"synthetic"}]}`, qwenPolicyModel)
		case "/v1/chat/completions":
			if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer SYNTHETIC-QWEN-INFERENCE-A" {
				t.Error("wrong synthetic inference credential/method")
				w.WriteHeader(403)
				return
			}
			f.sends.Add(1)
			io.Copy(io.Discard, r.Body)
			fmt.Fprintf(w, `{"id":"synthetic-qwen","object":"chat.completion","model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":"synthetic-ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`, qwenPolicyModel)
		default:
			f.denied.Add(1)
			t.Errorf("unexpected controlled upstream path: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(upstream.Close)
	// Every permitted proxy target is one exact literal-loopback upstream. All
	// CONNECT/external/background targets fail without DNS or forwarding.
	origin, _ := url.Parse(upstream.URL)
	proxyTransport := &http.Transport{Proxy: nil}
	t.Cleanup(proxyTransport.CloseIdleConnections)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "CONNECT" || r.URL.Scheme != "http" || r.URL.Host != origin.Host {
			f.denied.Add(1)
			w.WriteHeader(502)
			return
		}
		out := r.Clone(r.Context())
		out.RequestURI = ""
		out.Header.Del("Proxy-Authorization")
		response, err := proxyTransport.RoundTrip(out)
		if err != nil {
			w.WriteHeader(502)
			return
		}
		defer response.Body.Close()
		for k, values := range response.Header {
			for _, value := range values {
				w.Header().Add(k, value)
			}
		}
		w.WriteHeader(response.StatusCode)
		io.Copy(w, response.Body)
	}))
	t.Cleanup(proxy.Close)
	pluginDir := filepath.Join(dir, "plugins")
	authDir := filepath.Join(dir, "auth")
	for _, p := range []string{pluginDir, authDir, filepath.Join(dir, "AppData", "Local"), filepath.Join(dir, "AppData", "Roaming")} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	plugin, err := os.ReadFile(os.Getenv("SPEC92_QWEN_PLUGIN"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "cpa-credential-qwen.dll"), plugin, 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	f.base = fmt.Sprintf("http://127.0.0.1:%d", port)
	args := []string{"--source", "coding-plan", "--protocol", "qwen-coding-plan-v1", "--bl-path", f.cli, "--cache-file", f.cache}
	if mode == "cache_only" {
		args = []string{"--source", "coding-plan", "--protocol", "qwen-coding-plan-v1", "--cache-only", "--cache-file", f.cache, "--max-age", "5m"}
	}
	encodedArgs, _ := json.Marshal(args)
	config := fmt.Sprintf(`config-version: 8
server: {host: 127.0.0.1, port: %d, discovery: {enabled: false}}
management: {secret-key: test-password, allow-remote: false, disable-control-panel: true}
oauth: {auth-dir: %q}
resource-catalog: {enabled: true}
access: {api-keys: [synthetic-client]}
requests: {proxy-url: %q}
plugins:
  enabled: true
  dir: %q
  configs:
    cpa-credential-qwen:
      enabled: true
      allow-http: true
      base-url: %q
      api-keys: [{name: synthetic-a, value: SYNTHETIC-QWEN-INFERENCE-A}]
      quota-source: command
      command: %q
      command-args: %s
      command-timeout: 10s
      catalog: {refresh-interval: 24h}
`, port, filepath.ToSlash(authDir), proxy.URL, filepath.ToSlash(pluginDir), upstream.URL+"/v1", filepath.ToSlash(f.helper), encodedArgs)
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Getenv("SPEC92_QWEN_CORE"), "-config", path, "-local-model", "-no-browser")
	cmd.Dir = dir
	// Do not inherit real provider/profile/account or proxy environment.
	for _, key := range []string{"SystemRoot", "WINDIR", "COMSPEC", "SYSTEMDRIVE"} {
		if value := os.Getenv(key); value != "" {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	cmd.Env = append(cmd.Env, "PATH="+filepath.Join(os.Getenv("SystemRoot"), "System32"), "HOME="+dir, "USERPROFILE="+dir, "APPDATA="+filepath.Join(dir, "AppData", "Roaming"), "LOCALAPPDATA="+filepath.Join(dir, "AppData", "Local"), "BAILIAN_CONFIG_DIR="+filepath.Join(dir, "empty-bailian"), "TEMP="+dir, "TMP="+dir, "HTTP_PROXY="+proxy.URL, "HTTPS_PROXY="+proxy.URL, "ALL_PROXY="+proxy.URL, "NO_PROXY=127.0.0.1,localhost")
	log, err := os.Create(filepath.Join(evidence, "core.log"))
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		log.Close()
		t.Fatal(err)
	}
	f.save("core-process.json", map[string]any{"pid": cmd.Process.Pid, "baseURL": f.base, "argv": cmd.Args, "mode": mode, "productionBootstrap": true})
	t.Cleanup(func() {
		cmd.Process.Kill()
		waitErr := cmd.Wait()
		log.Close()
		f.save("core-exit.json", map[string]any{"pid": cmd.Process.Pid, "waitReturned": true, "processStateAvailable": cmd.ProcessState != nil, "waitError": fmt.Sprint(waitErr)})
		f.save("external-boundary-counts.json", map[string]any{"businessSends": f.sends.Load(), "rejectedTargets": f.denied.Load(), "cliInvocations": f.cliCalls()})
		if t.Failed() {
			body, _ := os.ReadFile(log.Name())
			t.Logf("isolated Core diagnostic:\n%s", body)
		}
	})
	deadline := time.Now().Add(20 * time.Second)
	for {
		request, _ := http.NewRequest("GET", f.base+"/v8/management/credential-quota/sources", nil)
		request.Header.Set("Authorization", "Bearer test-password")
		response, requestErr := f.client.Do(request)
		if requestErr == nil {
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			var inventory struct{ Sources []cq.Source }
			if response.StatusCode == 200 && json.Unmarshal(body, &inventory) == nil {
				for _, src := range inventory.Sources {
					if src.Protocol == "qwen-coding-plan-cli" && src.AuthIndex != "" && len(src.Targets) > 0 {
						if f.sources["A"].AuthIndex != "" {
							t.Fatal("ambiguous Qwen source; never infer enrollment from order/label")
						}
						f.sources["A"] = src
					}
				}
				if f.sources["A"].AuthIndex != "" {
					break
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("actual isolated Qwen production bootstrap not ready")
		}
		time.Sleep(25 * time.Millisecond)
	}
	cfg := testutil.NewConfig(t)
	f.db = testutil.NewStore(t, cfg)
	if err := f.db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: f.base, ManagementKey: "test-password"}}); err != nil {
		t.Fatal(err)
	}
	f.manager = New(cfg, f.db, collector.NewManager(cfg, f.db))
	f.manager.AppContext().FullRefreshService.Subscribe(func(_ context.Context, event fr.CompletedObservation) { f.completed <- event })
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := f.manager.AppContext().FullRefreshService.Shutdown(ctx); err != nil {
			t.Error(err)
		}
		if err := f.db.Close(); err != nil {
			t.Error(err)
		}
		if raw, err := os.ReadFile(cfg.DBPath); err == nil {
			if err := os.WriteFile(filepath.Join(evidence, "manager.sqlite"), raw, 0600); err != nil {
				t.Error(err)
			}
		} else {
			t.Error(err)
		}
	})
	f.readSource()
	f.status("A") // Remember the original issued source before any job starts.
	if executable, err := os.Executable(); err == nil {
		if raw, err := os.ReadFile(executable); err == nil {
			sum := sha256.Sum256(raw)
			f.save("manager-test-host.json", map[string]any{"path": executable, "sha256": hex.EncodeToString(sum[:]), "productionManagerBinaryExecuted": false})
		}
	}
	return f
}

func (f *qwenCLIPolicyFixture) save(name string, value any) {
	f.t.Helper()
	body, err := json.MarshalIndent(value, "", "  ")
	if err == nil {
		err = os.WriteFile(filepath.Join(f.evidence, name), append(body, '\n'), 0600)
	}
	if err != nil {
		f.t.Error(err)
	}
}

// Snapshot only this fixture's synthetic account files and real catalog. These
// reads neither normalize auth JSON nor mutate enrollment/host-issued versions.
// Preserve the original failing scenario, including its first inference.
func (f *qwenCLIPolicyFixture) snapshotRuntime(stage string) {
	f.t.Helper()
	destination := filepath.Join(f.evidence, stage)
	if err := os.MkdirAll(destination, 0700); err != nil {
		f.t.Fatal(err)
	}
	for _, endpoint := range []string{"resources", "credential-quota/sources"} {
		var value any
		f.coreAPI("GET", endpoint, nil, &value)
		f.save(filepath.Join(stage, strings.ReplaceAll(endpoint, "/", "-")+".json"), value)
	}
	files := []map[string]any{}
	for _, folder := range []string{"auth", ".resource-catalog"} {
		paths, err := filepath.Glob(filepath.Join(f.dir, folder, "*.json"))
		if err != nil || len(paths) == 0 {
			f.t.Fatalf("missing synthetic %s snapshot: %v", folder, err)
		}
		for _, path := range paths {
			raw, err := os.ReadFile(path)
			if err != nil {
				f.t.Fatal(err)
			}
			relative := filepath.Join(folder, filepath.Base(path))
			target := filepath.Join(destination, relative)
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				f.t.Fatal(err)
			}
			if err := os.WriteFile(target, raw, 0600); err != nil {
				f.t.Fatal(err)
			}
			sum := sha256.Sum256(raw)
			files = append(files, map[string]any{"path": relative, "bytes": len(raw), "sha256": hex.EncodeToString(sum[:])})
		}
	}
	f.save(filepath.Join(stage, "snapshot.json"), map[string]any{"readOnly": true, "syntheticOnly": true, "sealKeyExcluded": true, "files": files})
}

func (f *qwenCLIPolicyFixture) readSource() cq.Source {
	f.t.Helper()
	var inventory struct{ Sources []cq.Source }
	f.api("GET", "/usage-service/credential-quota", nil, 200, &inventory)
	index := f.sources["A"].AuthIndex
	for _, src := range inventory.Sources {
		if src.AuthIndex == index {
			f.sources["A"] = src
			return src
		}
	}
	f.t.Fatal("exact issued Qwen auth index disappeared")
	return cq.Source{}
}

func (f *qwenCLIPolicyFixture) enroll() {
	f.t.Helper()
	before := f.sources["A"]
	var configs map[string]map[string]any
	f.coreAPI("GET", "config/plugins/configs", nil, &configs)
	var bindings []map[string]any
	for _, target := range before.Targets {
		// Explicit zero values are preserved. Never predict the next config
		// version or rewrite a sealed catalog to bypass enrollment self-rotation.
		bindings = append(bindings, map[string]any{"resource-id": target.ResourceID, "identity-version": target.IdentityVersion, "credential-version": target.CredentialVersion, "config-version": target.ConfigVersion, "membership-version": target.MembershipVersion})
	}
	configs["cpa-credential-qwen"]["quota-resource-bindings"] = bindings
	var result any
	f.coreAPI("PUT", "config/plugins/configs", configs, &result)
	deadline := time.Now().Add(10 * time.Second)
	for {
		after := f.readSource()
		f.save("enrollment.json", map[string]any{"before": before, "after": after, "exactBindings": bindings})
		if !reflect.DeepEqual(before.Targets, after.Targets) {
			f.t.Fatal("Core enrollment rotated the exact target generations; requires the Core owner's fixed matched artifact, not test-side tolerance")
		}
		if after.State == "ready" && after.AccountProvenance == "configured_cli_profile" && after.SourceVersion != before.SourceVersion {
			return
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("exact public-API enrollment did not become ready: %+v", after)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func (f *qwenCLIPolicyFixture) writeCLIResponse(used int) {
	f.t.Helper()
	reset := time.Now().UTC().Add(2 * time.Hour).UnixMilli()
	window := map[string]any{"usedQuota": used, "totalQuota": 100, "percentage": float64(used) / 100, "resetTime": reset}
	body, err := json.Marshal(map[string]any{"instanceType": "synthetic-plan", "per5Hour": window, "perWeek": window, "perBillMonth": window})
	if err == nil {
		err = os.WriteFile(f.responseFile, body, 0600)
	}
	if err != nil {
		f.t.Fatal(err)
	}
}

func (f *qwenCLIPolicyFixture) cliCalls() int {
	raw, err := os.ReadFile(f.callsFile)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		f.t.Error(err)
	}
	return bytes.Count(raw, []byte("\n"))
}

func (f *qwenCLIPolicyFixture) produce(stage string) {
	f.t.Helper()
	cmd := exec.Command(f.helper, "--source", "coding-plan", "--protocol", "qwen-coding-plan-v1", "--bl-path", f.cli, "--cache-file", f.cache)
	cmd.Dir = f.dir
	cmd.Env = []string{"SystemRoot=" + os.Getenv("SystemRoot"), "COMSPEC=" + os.Getenv("COMSPEC"), "PATH=" + filepath.Join(os.Getenv("SystemRoot"), "System32"), "HOME=" + f.dir, "USERPROFILE=" + f.dir, "TEMP=" + f.dir, "TMP=" + f.dir}
	body, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("actual helper synthetic cache production failed: %v %s", err, body)
	}
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		f.t.Fatal(err)
	}
	f.save(stage+"-producer.json", value)
}

func (f *qwenCLIPolicyFixture) refresh(stage, want string) cq.Status {
	f.t.Helper()
	if raw, err := os.ReadFile(f.responseFile); err == nil {
		if err := os.WriteFile(filepath.Join(f.evidence, stage+"-synthetic-cli-input.json"), raw, 0600); err != nil {
			f.t.Fatal(err)
		}
	} else {
		f.t.Fatal(err)
	}
	var before cq.Status
	f.api("GET", "/usage-service/credential-quota?authIndex="+f.sources["A"].AuthIndex, nil, 200, &before)
	cliBefore := f.cliCalls()
	var job cq.Job
	f.api("POST", "/usage-service/credential-quota", cq.Query{Binding: before.Binding, SourceID: before.SourceID, SubjectID: before.SubjectID, SourceVersion: before.SourceVersion, AuthIndex: before.AuthIndex, Version: before.Version}, 202, &job)
	timeout := time.After(15 * time.Second)
	for {
		select {
		case event := <-f.completed:
			if event.AttemptID != job.ID {
				continue
			}
			f.save(stage+"-completion.json", event)
			if event.Credential == nil || event.Credential.Attempt == nil || event.Credential.Attempt.QueryID != job.ID || event.Credential.Attempt.QueryStatus != want {
				f.t.Fatalf("actual selected Qwen completion: want %s, got %+v", want, event.Credential)
			}
			var current cq.Status
			f.api("GET", "/usage-service/credential-quota?authIndex="+before.AuthIndex, nil, 200, &current)
			f.save(stage+"-quota-api.json", current)
			delta := f.cliCalls() - cliBefore
			if want == "success" && f.mode == "live" && delta != 1 || f.mode == "cache_only" && delta != 0 || want != "success" && delta != 0 {
				f.t.Fatalf("unexpected selected CLI executions: mode=%s status=%s delta=%d", f.mode, want, delta)
			}
			if want == "success" {
				for _, suffix := range []string{"", ".attempt.json"} {
					raw, err := os.ReadFile(f.cache + suffix)
					if err != nil {
						f.t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(f.evidence, stage+"-cache.json"+suffix), raw, 0600); err != nil {
						f.t.Fatal(err)
					}
				}
				var cache struct {
					ObservedAt string `json:"observedAt"`
				}
				raw, _ := os.ReadFile(f.cache)
				if json.Unmarshal(raw, &cache) != nil {
					f.t.Fatal("missing actual helper cache")
				}
				stamp, err := time.Parse(time.RFC3339Nano, cache.ObservedAt)
				if err != nil || current.Attempt == nil || current.Attempt.ObservedAtMS != stamp.UnixMilli() {
					f.t.Fatal("Core/Manager restamped the original CLI cache acquisition")
				}
			}
			return current
		case <-timeout:
			f.t.Fatal("actual Qwen shared refresh completion missing")
		}
	}
}

func (f *qwenCLIPolicyFixture) requireEvidence(status cq.Status, want string) {
	f.t.Helper()
	var core cq.CoreObservation
	if status.Stale || status.Attempt == nil || json.Unmarshal([]byte(status.Attempt.WindowsJSON), &core) != nil || core.Protocol != "qwen-coding-plan-cli" || core.AccountProvenance != "configured_cli_profile" || core.TimestampProvenance != "local_cli_acquisition_time" || !core.Complete || len(core.Windows) != 3 || !reflect.DeepEqual(core.Binding, status.Binding) || core.SourceVersion != status.SourceVersion {
		f.t.Fatal("actual CLI observation lost truthful provenance, complete windows, freshness or exact authority")
	}
	if decision := cp.ValidateCredentialEvidence(status, time.Now().UTC()); decision.Action != want {
		f.t.Fatalf("actual evidence was not a %s candidate: %+v", want, decision)
	}
}

func (f *qwenCLIPolicyFixture) startPolicyWorker() {
	f.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	// Use the same coordinated production lifecycle as the existing native
	// admission fixtures, not an independent policy-only scheduler.
	go func() { done <- f.manager.AppContext().FullRefreshService.Run(ctx) }()
	f.t.Cleanup(func() {
		cancel()
		shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := f.manager.AppContext().FullRefreshService.Shutdown(shutdown); err != nil {
			f.t.Error(err)
		}
		select {
		case err := <-done:
			if err != nil {
				f.t.Error(err)
			}
		case <-shutdown.Done():
			f.t.Error("actual coordinated policy/refresh lifecycle did not join")
		}
	})
}

func (f *qwenCLIPolicyFixture) waitOperation(action string) cp.Status {
	f.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		current := f.status("A")
		if current.Operation != nil && current.Operation.Action == action && current.Operation.CurrentApplied && current.Operation.ReceiptConfirmed {
			f.save(action+"-policy-api.json", current)
			return current
		}
		if time.Now().After(deadline) {
			f.save(action+"-timeout.json", current)
			f.t.Fatalf("actual policy worker did not confirm %s: %+v", action, current.Operation)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func (f *qwenCLIPolicyFixture) send(stream, allowed bool) {
	f.t.Helper()
	body, _ := json.Marshal(map[string]any{"model": qwenPolicyModel, "messages": []any{map[string]any{"role": "user", "content": "synthetic"}}, "stream": stream})
	request, _ := http.NewRequest("POST", f.base+"/v1/chat/completions", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer synthetic-client")
	before := f.sends.Load()
	response, err := f.client.Do(request)
	if err != nil {
		f.t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		f.t.Fatal(err)
	}
	f.sequence++
	f.save(fmt.Sprintf("inference-%02d.json", f.sequence), map[string]any{"status": response.StatusCode, "body": string(data), "stream": stream, "expectedAllowed": allowed, "sendsBefore": before, "sendsAfter": f.sends.Load()})
	if allowed {
		if response.StatusCode != 200 || f.sends.Load() != before+1 || !strings.Contains(string(data), "synthetic-ok") {
			f.t.Fatalf("real Qwen inference not admitted: status=%d body=%s", response.StatusCode, data)
		}
	} else if response.StatusCode < 400 || f.sends.Load() != before {
		f.t.Fatalf("real Qwen pause bypass: status=%d sends=%d -> %d", response.StatusCode, before, f.sends.Load())
	}
}

func (f *qwenCLIPolicyFixture) persist(stage string, api cp.Status) {
	f.t.Helper()
	// Read-only store APIs on the real DB, never synthetic inserted evidence.
	ctx := context.Background()
	policy, err := f.db.CredentialPolicy(ctx, api.SubjectID)
	if err != nil {
		f.t.Fatal(err)
	}
	quota, err := f.db.CredentialQuota(ctx, api.SubjectID)
	if err != nil {
		f.t.Fatal(err)
	}
	operation, err := f.db.LatestCredentialPolicyOperation(ctx, api.SubjectID)
	if err != nil {
		f.t.Fatal(err)
	}
	f.save(stage+"-sqlite-readback.json", map[string]any{"api": api, "policyRow": policy, "quotaRow": quota, "operation": operation, "operationBody": operation.Body, "operationResult": operation.Result})
	if policy.Generation != api.Generation || quota.Attempt == "" {
		f.t.Fatal("API and actual SQLite generation/observation disagree")
	}
	if api.Operation != nil && (operation.ID != api.Operation.OpID || !operation.Attempted || operation.Result == "") {
		f.t.Fatal("current applied API operation has no actual SQLite dispatch/receipt")
	}
	if !api.Enabled && (policy.Document != "" || operation.ID != "") {
		f.t.Fatal("failed unenrolled ON created durable policy or journal")
	}
}
