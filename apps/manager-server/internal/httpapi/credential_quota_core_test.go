package httpapi

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

// Actual compiled Core, selected OAuth and real existing compiled OpenCode plugin.
// An independent hash/revision manifest is mandatory for final acceptance.
func TestCredentialQuotaActualCoreChain(t *testing.T) {
	credentialQuotaActualCoreChain(t, true)
}
func TestCredentialQuotaActualCoreOldPluginRefused(t *testing.T) {
	t.Setenv("CORE_QUOTA_PLUGIN_LEGACY", "1")
	credentialQuotaActualCoreChain(t, true)
}
func TestCredentialQuotaActualCoreDefaultEndpoint(t *testing.T) {
	t.Setenv("CORE_QUOTA_ENDPOINT_MODE", "omitted")
	credentialQuotaActualCoreChain(t, true)
}
func TestCredentialQuotaActualCoreExplicitFileEndpoint(t *testing.T) {
	t.Setenv("CORE_QUOTA_ENDPOINT_MODE", "file-explicit-default")
	credentialQuotaActualCoreChain(t, true)
}
func TestCredentialQuotaActualCoreMismatchedProjectionRefused(t *testing.T) {
	t.Setenv("CORE_QUOTA_ENDPOINT_MODE", "file-explicit-legacy")
	credentialQuotaActualCoreChain(t, true)
}
func TestCredentialQuotaActualCoreNative(t *testing.T) { credentialQuotaActualCoreChain(t, false) }
func credentialQuotaActualCoreChain(t *testing.T, requirePlugin bool) {
	binary := os.Getenv("CORE_QUOTA_TEST_BINARY")
	if binary == "" {
		t.Skip("explicit immutable current Core compiled route host required")
	}
	expected := os.Getenv("CORE_QUOTA_EXPECTED_SHA256")
	revision := os.Getenv("CORE_QUOTA_EXPECTED_REVISION")
	raw, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if len(expected) != 64 || hex.EncodeToString(sum[:]) != expected {
		t.Fatal("independent Core hash missing/mismatch")
	}
	info, err := buildinfo.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	got, modified := "", ""
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			got = s.Value
		}
		if s.Key == "vcs.modified" {
			modified = s.Value
		}
	}
	if len(revision) != 40 || got != revision || modified != "false" {
		t.Fatal("unverified Core source revision")
	}
	t.Logf("actual Core source=%s sha256=%s modified=%s", got, expected, modified)
	legacy := os.Getenv("CORE_QUOTA_PLUGIN_LEGACY") == "1"
	projectionMismatch := os.Getenv("CORE_QUOTA_ENDPOINT_MODE") == "file-explicit-legacy"
	refused := legacy || projectionMismatch
	pluginDir := os.Getenv("CORE_QUOTA_PLUGIN_DIR")
	pluginSHA := os.Getenv("CORE_QUOTA_PLUGIN_EXPECTED_SHA256")
	pluginRevision := os.Getenv("CORE_QUOTA_PLUGIN_EXPECTED_REVISION")
	if legacy {
		pluginDir = os.Getenv("CORE_QUOTA_OLD_PLUGIN_DIR")
		pluginSHA = os.Getenv("CORE_QUOTA_OLD_PLUGIN_EXPECTED_SHA256")
		pluginRevision = os.Getenv("CORE_QUOTA_OLD_PLUGIN_EXPECTED_REVISION")
	}
	pluginFile := filepath.Join(pluginDir, "opencode-go-cliproxyapi.dll")
	pluginBytes, err := os.ReadFile(pluginFile)
	if err != nil {
		t.Fatal(err)
	}
	pluginSum := sha256.Sum256(pluginBytes)
	if len(pluginSHA) != 64 || hex.EncodeToString(pluginSum[:]) != pluginSHA || len(pluginRevision) != 40 {
		t.Fatal("independent plugin provenance missing/mismatch")
	}
	pluginInfo, err := buildinfo.ReadFile(pluginFile)
	if err != nil {
		t.Fatal(err)
	}
	pr, pm := "", ""
	for _, s := range pluginInfo.Settings {
		if s.Key == "vcs.revision" {
			pr = s.Value
		}
		if s.Key == "vcs.modified" {
			pm = s.Value
		}
	}
	if pr != pluginRevision || pm != "false" || os.Getenv("CORE_QUOTA_PLUGIN_SDK_REVISION") != revision {
		t.Fatal("unverified plugin source/SDK provenance")
	}
	t.Logf("actual %s plugin source=%s sha256=%s modified=%s CoreSDK=%s", map[bool]string{true: "OLD", false: "NEW"}[legacy], pr, pluginSHA, pm, revision)
	dir := t.TempDir()
	var calls, mode atomic.Int64
	entered, release := make(chan struct{}, 1), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if mode.Load() == 3 {
			entered <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		if mode.Load() == 4 {
			<-r.Context().Done()
			return
		}
		if mode.Load() == 1 {
			w.WriteHeader(401)
			fmt.Fprint(w, "QUOTA-SECRET-CANARY")
			return
		}
		if mode.Load() == 2 {
			w.WriteHeader(429)
			return
		}
		if r.URL.Path == "/backend-api/wham/usage" {
			if r.Header.Get("Authorization") != "Bearer QUOTA-SECRET-CANARY" {
				t.Error("wrong OAuth credential")
			}
			reset := time.Now().Add(time.Hour).Unix()
			fmt.Fprintf(w, `{"rate_limit":{"allowed":true,"limit_reached":false,"primary_window":{"used_percent":25,"reset_at":%d,"limit_window_seconds":18000},"secondary_window":{"used_percent":50,"reset_at":%d,"limit_window_seconds":604800}}}`, reset, reset)
			return
		}
		if r.URL.Path == "/zen/go/v1/usage" || r.URL.Path == "/inference/go/v1/usage" {
			expectedPath := "/zen/go/v1/usage"
			if os.Getenv("CORE_QUOTA_ENDPOINT_MODE") != "" {
				expectedPath = "/inference/go/v1/usage"
			}
			if r.URL.Path != expectedPath || r.Host != "opencode.ai" {
				t.Error("selected plugin effective quota origin/path drifted")
			}
			if r.Header.Get("Authorization") != "Bearer PLUGIN-SECRET-CANARY" {
				t.Error("wrong selected plugin key")
			}
			switch mode.Load() {
			case 5:
				fmt.Fprintf(w, `{"usage":{"weekly":{"status":"allowed","percent":12.5,"resetsAt":%q}}}`, time.Now().Add(time.Hour).Format(time.RFC3339))
				return
			case 6:
				fmt.Fprint(w, `{"usage":{"weekly":{"status":"allowed","percent":null,"resetsAt":"2027-01-01T00:00:00Z"}}}`)
				return
			case 7:
				fmt.Fprint(w, `{"usage":{"weekly":{"status":"allowed","percent":20,"resetsAt":"bad-window"}}}`)
				return
			case 8:
				fmt.Fprint(w, `{"usage":{"rolling":{"status":"allowed","percent":20,"resetsAt":"2020-01-01T00:00:00Z"},"weekly":{"status":"allowed","percent":20,"resetsAt":"2020-01-01T00:00:00Z"},"monthly":{"status":"allowed","percent":20,"resetsAt":"2020-01-01T00:00:00Z"}}}`)
				return
			}
			fmt.Fprintf(w, `{"usage":{"rolling":{"status":"rate-limited","percent":42,"resetsAt":%q},"weekly":{"status":"allowed","percent":15,"resetsAt":%q},"monthly":{"status":"allowed","percent":10,"resetsAt":%q}}}`, time.Now().Add(time.Hour).Format(time.RFC3339), time.Now().Add(time.Hour).Format(time.RFC3339), time.Now().Add(time.Hour).Format(time.RFC3339))
			return
		}
		t.Error("unexpected upstream request/inference")
		w.WriteHeader(400)
	}))
	seed := httptest.NewTLSServer(http.NotFoundHandler())
	cert := seed.TLS.Certificates[0]
	seed.Close()
	template, _ := x509.ParseCertificate(cert.Certificate[0])
	template.DNSNames = []string{"chatgpt.com", "opencode.ai"}
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
	defer upstream.Close()
	ca := filepath.Join(dir, "ca.pem")
	os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	base := "http://" + address
	cmd := exec.Command(binary, "-test.run=^TestCredentialQuotaProcessFixture$", "-test.timeout=120s")
	cmd.Dir = dir
	for _, k := range []string{"PATH", "SystemRoot", "WINDIR", "TEMP", "TMP"} {
		if v := os.Getenv(k); v != "" {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	cmd.Env = append(cmd.Env, "HOME="+dir, "USERPROFILE="+dir, "QUOTA_FIXTURE_LISTEN="+address, "QUOTA_FIXTURE_UPSTREAM="+upstream.Listener.Addr().String(), "QUOTA_FIXTURE_CA="+ca, "QUOTA_FIXTURE_PLUGIN_DIR="+pluginDir, "QUOTA_FIXTURE_ENDPOINT_MODE="+os.Getenv("CORE_QUOTA_ENDPOINT_MODE"))
	stateDir := filepath.Join(dir, "core-state")
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		t.Fatal(err)
	}
	cmd.Env = append(cmd.Env, "QUOTA_FIXTURE_STATE_DIR="+stateDir)
	log, err := os.Create(filepath.Join(dir, "core.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cmd.Stdout = log
	cmd.Stderr = log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cmd.Process.Kill()
		cmd.Wait()
		if t.Failed() {
			b, _ := os.ReadFile(log.Name())
			t.Log(string(b))
		}
	}()
	client := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(20 * time.Second)
	for {
		req, _ := http.NewRequest("GET", base+"/v8/management/credential-quota/sources", nil)
		req.Header.Set("Authorization", "Bearer test-password")
		r, e := client.Do(req)
		if e == nil {
			r.Body.Close()
			if r.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("actual Core not ready")
		}
		time.Sleep(25 * time.Millisecond)
	}
	cfg := testutil.NewConfig(t)
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	testutil.EnsureAdminCredential(t, db)
	if err := db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: base, ManagementKey: "test-password"}}); err != nil {
		t.Fatal(err)
	}
	manager := New(cfg, db, collector.NewManager(cfg, db))
	server := httptest.NewServer(manager.Handler())
	defer server.Close()
	api := func(method, path string, body any) (int, []byte) {
		t.Helper()
		data, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, server.URL+path, bytes.NewReader(data))
		req.Header.Set("Authorization", "Bearer "+testutil.AdminKey)
		r, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer r.Body.Close()
		out, _ := io.ReadAll(r.Body)
		if bytes.Contains(out, []byte("CANARY")) {
			t.Fatal("secret leaked")
		}
		return r.StatusCode, out
	}
	code, data := api("GET", "/usage-service/credential-quota", nil)
	if code != 200 {
		t.Fatalf("source read HTTP %d: %s", code, data)
	}
	_, capData := api("GET", negotiatedPath, nil)
	if !strings.Contains(string(capData), `"credentialQuery":{"state":"available","reason":"scoped_credential_query_v1"}`) || !strings.Contains(string(capData), `"independentGate":{"state":"unavailable"`) {
		t.Fatalf("summary disagrees with actual scoped query: %s", capData)
	}
	var view struct {
		Sources []struct {
			rc.RuntimeBinding
			Protocol      string `json:"protocol"`
			SourceVersion string `json:"sourceVersion"`
			SourceID      string `json:"sourceId"`
			SubjectID     string `json:"subjectId"`
			State         string `json:"state"`
			Reason        string `json:"reason"`
		}
	}
	if json.Unmarshal(data, &view) != nil {
		t.Fatal("invalid source view")
	}
	if projectionMismatch {
		// The existing identity catalog refuses to bind this legacy file/default
		// combination. Its empty provider plus two credentials stay unavailable.
		// Do not fabricate a credential group to exercise querying.
		unbound, oauth := 0, 0
		for _, source := range view.Sources {
			if source.Protocol == "codex-oauth-wham" && source.State == "ready" {
				oauth++
			}
			if source.AuthIndex == "" && source.Protocol == "unavailable" && source.State == "unsupported" && source.Reason == "runtime_binding_unavailable" {
				unbound++
			}
			if source.Protocol == "opencode-go-usage" {
				t.Fatal("ambiguous projection unexpectedly bound")
			}
		}
		if oauth != 1 || unbound != 3 || len(view.Sources) != 5 || calls.Load() != 0 {
			t.Fatalf("unbound projection not truthfully retained: %+v calls=%d", view.Sources, calls.Load())
		}
		return
	}
	native, plugin := false, false
	var nativeBinding rc.RuntimeBinding
	var pluginBinding rc.RuntimeBinding
	var nativeSourceID, nativeSubjectID, pluginSourceID, pluginSubjectID string
	var pluginSourceVersion string
	for _, source := range view.Sources {
		if source.Protocol != "codex-oauth-wham" && source.Protocol != "opencode-go-usage" {
			continue
		}
		if !requirePlugin && source.Protocol == "opencode-go-usage" {
			continue
		}
		if source.Protocol == "opencode-go-usage" {
			plugin = true
			pluginSourceID = source.SourceID
			pluginSubjectID = source.SubjectID
			pluginBinding = source.RuntimeBinding
			pluginBinding.Targets = append([]rc.Target(nil), source.Targets...)
			pluginSourceVersion = source.SourceVersion
			expectedTargets := 2
			if os.Getenv("CORE_QUOTA_ENDPOINT_MODE") == "file-explicit-default" {
				expectedTargets = 1
			}
			if len(source.Targets) != expectedTargets {
				t.Fatal("full projection target group missing")
			}
			if legacy && (source.State != "unsupported" || source.Reason != "quota_operation_context_unavailable") {
				t.Fatal("old compiled plugin was not truthfully refused")
			}
			if projectionMismatch && (source.State != "needs_configuration" || source.Reason != "projection_endpoint_mismatch") {
				t.Fatal("mismatched effective projection was not refused")
			}
		} else {
			native = true
			nativeSourceID = source.SourceID
			nativeSubjectID = source.SubjectID
			nativeBinding = source.RuntimeBinding
			nativeBinding.Targets = append([]rc.Target(nil), source.Targets...)
		}
		path := "/usage-service/credential-quota?authIndex=" + source.AuthIndex
		code, data = api("GET", path, nil)
		if code != 200 {
			t.Fatal(string(data))
		}
		var state map[string]any
		json.Unmarshal(data, &state)
		q := map[string]any{"scope": source.Scope, "targets": source.Targets, "auth_index": source.AuthIndex, "sourceId": source.SourceID, "subjectId": source.SubjectID, "version": state["version"], "sourceVersion": source.SourceVersion}
		code, data = api("POST", "/usage-service/credential-quota", q)
		if code != 202 {
			t.Fatalf("start %d: %s", code, data)
		}
		var job struct {
			ID    string `json:"id"`
			State string `json:"state"`
		}
		json.Unmarshal(data, &job)
		deadline := time.Now().Add(10 * time.Second)
		for {
			code, data = api("GET", "/usage-service/credential-quota/jobs/"+job.ID, nil)
			json.Unmarshal(data, &job)
			if code != 200 {
				t.Fatalf("job %d %s", code, data)
			}
			if job.State != "running" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("job timeout")
			}
			time.Sleep(20 * time.Millisecond)
		}
		code, data = api("GET", path, nil)
		wantStatus := "success"
		if legacy && source.Protocol == "opencode-go-usage" {
			wantStatus = "unsupported"
		}
		if projectionMismatch && source.Protocol == "opencode-go-usage" {
			wantStatus = "needs_configuration"
		}
		if code != 200 || !strings.Contains(string(data), `"queryStatus":"`+wantStatus+`"`) {
			t.Fatalf("readback: %d %s", code, data)
		}
		if source.Protocol == "opencode-go-usage" && !refused {
			var persisted struct {
				LastSuccess struct {
					WindowsJSON string `json:"windowsJson"`
				} `json:"lastSuccess"`
			}
			json.Unmarshal(data, &persisted)
			var windows struct {
				Complete bool `json:"complete"`
				Recovery bool `json:"recoveryEligible"`
				Windows  []struct {
					Percent   float64 `json:"usedPercent"`
					Remaining float64 `json:"remainingFraction"`
					Status    string  `json:"sourceStatus"`
				} `json:"windows"`
			}
			json.Unmarshal([]byte(persisted.LastSuccess.WindowsJSON), &windows)
			if !windows.Complete || windows.Recovery || len(windows.Windows) != 3 || windows.Windows[0].Percent != 42 || windows.Windows[0].Remaining != 0 || windows.Windows[0].Status != "rate-limited" {
				t.Fatalf("compiled plugin lost source status: %s", data)
			}
		}
	}
	if !native || requirePlugin && !plugin {
		t.Fatalf("native/plugin coverage missing: %+v", view.Sources)
	}
	expectedCalls := int64(1)
	if requirePlugin && !refused {
		expectedCalls = 2
	}
	if calls.Load() != expectedCalls {
		t.Fatalf("GET or fallback duplicated query calls=%d", calls.Load())
	}
	if requirePlugin && !refused {
		pluginPath := "/usage-service/credential-quota?authIndex=" + pluginBinding.AuthIndex
		_, data = api("GET", pluginPath, nil)
		var sourceState map[string]any
		json.Unmarshal(data, &sourceState)
		query := map[string]any{"scope": pluginBinding.Scope, "targets": pluginBinding.Targets, "auth_index": pluginBinding.AuthIndex, "sourceId": pluginSourceID, "subjectId": pluginSubjectID, "version": sourceState["version"], "sourceVersion": pluginSourceVersion}
		query["sourceVersion"] = "changed-source"
		code, _ = api("POST", "/usage-service/credential-quota", query)
		if code != 409 {
			t.Fatal("stale source revision accepted")
		}
		query["sourceVersion"] = pluginSourceVersion
		if len(pluginBinding.Targets) > 1 {
			query["targets"] = pluginBinding.Targets[:1]
			code, _ = api("POST", "/usage-service/credential-quota", query)
			if code != 409 {
				t.Fatal("source projection subset accepted")
			}
			query["targets"] = pluginBinding.Targets
		}
		var lastPlugin []byte
		for _, test := range []struct {
			mode     int64
			state    string
			complete bool
		}{{5, "success", false}, {6, "parse_error", false}, {7, "parse_error", false}, {8, "success", true}} {
			mode.Store(test.mode)
			code, data = api("POST", "/usage-service/credential-quota", query)
			if code != 202 {
				t.Fatal(string(data))
			}
			var job struct {
				ID    string `json:"id"`
				State string `json:"state"`
			}
			json.Unmarshal(data, &job)
			deadline := time.Now().Add(5 * time.Second)
			for {
				code, data = api("GET", "/usage-service/credential-quota/jobs/"+job.ID, nil)
				if code != 200 {
					t.Fatal(string(data))
				}
				json.Unmarshal(data, &job)
				if job.State != "running" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("plugin negative job timeout")
				}
				time.Sleep(20 * time.Millisecond)
			}
			_, data = api("GET", pluginPath, nil)
			var state struct {
				Attempt struct {
					Status string `json:"queryStatus"`
				} `json:"attempt"`
				LastSuccess json.RawMessage `json:"lastSuccess"`
				Stale       bool            `json:"stale"`
			}
			json.Unmarshal(data, &state)
			if state.Attempt.Status != test.state {
				t.Fatalf("plugin attempt state %s: %s", state.Attempt.Status, data)
			}
			if test.state != "success" {
				if !state.Stale || !bytes.Equal(lastPlugin, state.LastSuccess) {
					t.Fatal("bad plugin window erased prior success")
				}
			} else {
				lastPlugin = append([]byte(nil), state.LastSuccess...)
				var success struct {
					WindowsJSON string `json:"windowsJson"`
				}
				json.Unmarshal(lastPlugin, &success)
				var observation struct {
					Recovery bool `json:"recoveryEligible"`
					Complete bool `json:"complete"`
				}
				json.Unmarshal([]byte(success.WindowsJSON), &observation)
				if observation.Recovery || observation.Complete != test.complete {
					t.Fatal("partial/expired plugin windows became recovery evidence")
				}
			}
			expectedCalls++
		}
		mode.Store(0)
		if liveDir := os.Getenv("CORE_QUOTA_PLUGIN_LIVE_DIR"); liveDir != "" {
			args := []string{"test", "-tags=debug,live", "-run=^TestLiveControlledScopedQuota$", "-count=1", "-v"}
			if mod := os.Getenv("CORE_QUOTA_PLUGIN_MODFILE"); mod != "" {
				args = append(args, "-modfile="+mod)
			}
			args = append(args, "./tests")
			live := exec.Command("go", args...)
			live.Dir = liveDir
			live.Env = append(os.Environ(), "CPA_QUOTA_CONTROLLED_BASE="+server.URL, "CPA_QUOTA_CONTROLLED_KEY="+testutil.AdminKey, "CPA_QUOTA_CONTROLLED_INDEX="+pluginBinding.AuthIndex)
			out, err := live.CombinedOutput()
			t.Log(string(out))
			if err != nil {
				t.Fatal("canonical controlled live smoke failed")
			}
			expectedCalls++
		}
		if calls.Load() != expectedCalls {
			t.Fatal("plugin failure/partial path used duplicate fallback")
		}
	}
	// Reopen the real SQLite host before exercising failure retention.
	server.Close()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	manager = New(cfg, db, collector.NewManager(cfg, db))
	var admissionOffset atomic.Int64
	if err := manager.AppContext().ManagementQueryBudget.SetClock(func() time.Time { return time.Now().Add(time.Duration(admissionOffset.Load())) }); err != nil {
		t.Fatal(err)
	}
	server = httptest.NewServer(manager.Handler())
	defer server.Close()
	path := "/usage-service/credential-quota?authIndex=" + nativeBinding.AuthIndex
	_, data = api("GET", path, nil)
	var before map[string]any
	json.Unmarshal(data, &before)
	last, _ := json.Marshal(before["lastSuccess"])
	if string(last) == "null" {
		t.Fatal("last success lost after SQLite reopen")
	}
	for _, failure := range []struct {
		mode   int64
		status string
	}{{1, "auth_error"}, {2, "rate_limited"}} {
		mode.Store(failure.mode)
		q := map[string]any{"scope": nativeBinding.Scope, "targets": nativeBinding.Targets, "auth_index": nativeBinding.AuthIndex, "sourceId": nativeSourceID, "subjectId": nativeSubjectID, "version": before["version"], "sourceVersion": before["sourceVersion"]}
		code, data = api("POST", "/usage-service/credential-quota", q)
		if code != 202 {
			t.Fatalf("refresh %d %s", code, data)
		}
		var job struct {
			ID    string `json:"id"`
			State string `json:"state"`
		}
		json.Unmarshal(data, &job)
		deadline := time.Now().Add(5 * time.Second)
		for {
			code, data = api("GET", "/usage-service/credential-quota/jobs/"+job.ID, nil)
			if code != 200 {
				t.Fatal(string(data))
			}
			json.Unmarshal(data, &job)
			if job.State != "running" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("failure job timed out")
			}
			time.Sleep(20 * time.Millisecond)
		}
		_, data = api("GET", path, nil)
		var state map[string]any
		json.Unmarshal(data, &state)
		retained, _ := json.Marshal(state["lastSuccess"])
		if string(retained) != string(last) || state["stale"] != true || !strings.Contains(string(data), `"queryStatus":"`+failure.status+`"`) {
			t.Fatalf("failed attempt erased success or time: %s", data)
		}
	}
	if calls.Load() != expectedCalls+2 {
		t.Fatal("failure path issued fallback/GET quota query")
	}
	//429 now persists a conservative management backoff. This subsequent
	// independent scenario advances ONLY admission time, never upstream freshness,
	// source versions, query deadlines or the unchanged timeout assertions below.
	admissionOffset.Store(int64(time.Hour))
	mode.Store(3)
	q := map[string]any{"scope": nativeBinding.Scope, "targets": nativeBinding.Targets, "auth_index": nativeBinding.AuthIndex, "sourceId": nativeSourceID, "subjectId": nativeSubjectID, "version": before["version"], "sourceVersion": before["sourceVersion"]}
	code, data = api("POST", "/usage-service/credential-quota", q)
	if code != 202 {
		t.Fatal(string(data))
	}
	var first struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}
	json.Unmarshal(data, &first)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("selected quota request did not enter TLS")
	}
	code, data = api("POST", "/usage-service/credential-quota", q)
	if code != 202 {
		t.Fatal(string(data))
	}
	var duplicate struct {
		ID string `json:"id"`
	}
	json.Unmarshal(data, &duplicate)
	if duplicate.ID != first.ID {
		t.Fatal("manual/recovery caller bypassed shared single-flight")
	}
	_, data = api("GET", path, nil)
	if !strings.Contains(string(data), `"queryStatus":"running"`) || !strings.Contains(string(data), `"lastSuccess"`) {
		t.Fatal("accepted attempt not durable before I/O completes")
	}
	mode.Store(0)
	close(release)
	waitJob := func(id string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			code, data = api("GET", "/usage-service/credential-quota/jobs/"+id, nil)
			var j struct {
				State string `json:"state"`
			}
			json.Unmarshal(data, &j)
			if code != 200 {
				t.Fatal(string(data))
			}
			if j.State != "running" {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("job failed to settle")
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	waitJob(first.ID)
	_, data = api("GET", path, nil)
	var settled map[string]any
	json.Unmarshal(data, &settled)
	last, _ = json.Marshal(settled["lastSuccess"])
	mode.Store(4)
	q["timeoutMs"] = 300
	code, data = api("POST", "/usage-service/credential-quota", q)
	if code != 202 {
		t.Fatal(string(data))
	}
	json.Unmarshal(data, &first)
	waitJob(first.ID)
	_, data = api("GET", path, nil)
	var timeout map[string]any
	json.Unmarshal(data, &timeout)
	retained, _ := json.Marshal(timeout["lastSuccess"])
	if string(retained) != string(last) || timeout["stale"] != true || !strings.Contains(string(data), `"queryStatus":"network_error"`) {
		t.Fatalf("timeout attempt not retained: %s", data)
	}
	if calls.Load() != expectedCalls+4 {
		t.Fatalf("single-flight/timeout duplicated queries calls=%d", calls.Load())
	}
	if node := os.Getenv("CORE_QUOTA_UI_NODE"); node != "" {
		mode.Store(0)
		control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "POST" {
				w.WriteHeader(405)
				return
			}
			mode.Store(1)
			w.WriteHeader(204)
		}))
		defer control.Close()
		fixture, _ := json.Marshal(map[string]any{"base": server.URL, "managementKey": testutil.AdminKey, "authIndex": nativeBinding.AuthIndex, "control": control.URL})
		fixturePath := filepath.Join(dir, "ui-fixture.json")
		if err := os.WriteFile(fixturePath, fixture, 0600); err != nil {
			t.Fatal(err)
		}
		cwd, _ := os.Getwd()
		web := filepath.Clean(filepath.Join(cwd, "../../../web"))
		vitest := filepath.Join(web, "../../node_modules/vitest/vitest.mjs")
		ui := exec.Command(node, vitest, "run", "src/components/quota/CredentialQuotaPanel.live.test.tsx")
		ui.Dir = web
		ui.Env = append(os.Environ(), "CREDENTIAL_QUOTA_UI_FIXTURE="+fixturePath)
		out, err := ui.CombinedOutput()
		t.Log(string(out))
		if err != nil {
			t.Fatal("live credential quota UI failed")
		}
		if calls.Load() != expectedCalls+6 {
			t.Fatal("live UI issued unbounded/duplicate quota query")
		}
	}
	// Actual Core restart: retain durable IDs/history but never old boot authority.
	authPath := filepath.Join(stateDir, "auth", "codex.json")
	beforeFile, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeInfo, err := os.Stat(authPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeHash := sha256.Sum256(beforeFile)
	frozenTargets, _ := json.Marshal(nativeBinding.Targets)
	_, data = api("GET", path, nil)
	var oldState map[string]any
	json.Unmarshal(data, &oldState)
	oldSuccess, _ := json.Marshal(oldState["lastSuccess"])
	oldEnv := append([]string(nil), cmd.Env...)
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	cmd = exec.Command(binary, "-test.run=^TestCredentialQuotaProcessFixture$", "-test.timeout=120s")
	cmd.Dir = dir
	cmd.Env = oldEnv
	cmd.Stdout = log
	cmd.Stderr = log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(20 * time.Second)
	for {
		req, _ := http.NewRequest("GET", base+"/v8/management/credential-quota/sources", nil)
		req.Header.Set("Authorization", "Bearer test-password")
		res, e := client.Do(req)
		if e == nil {
			res.Body.Close()
			if res.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("restarted Core not ready")
		}
		time.Sleep(25 * time.Millisecond)
	}
	code, data = api("GET", "/usage-service/credential-quota", nil)
	if code != 200 {
		t.Fatal(string(data))
	}
	view.Sources = nil
	json.Unmarshal(data, &view)
	var rebound rc.RuntimeBinding
	for _, source := range view.Sources {
		if source.Protocol == "codex-oauth-wham" {
			rebound = source.RuntimeBinding
		}
	}
	if rebound.Scope.BootEpoch == nativeBinding.Scope.BootEpoch || rebound.Scope.InstanceID != nativeBinding.Scope.InstanceID || !reflect.DeepEqual(rebound.Targets, nativeBinding.Targets) {
		t.Fatalf("restart lost durable identity or reused boot: before=%+v after=%+v", nativeBinding, rebound)
	}
	afterFile, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatal(err)
	}
	afterInfo, err := os.Stat(authPath)
	if err != nil {
		t.Fatal(err)
	}
	afterHash := sha256.Sum256(afterFile)
	reboundTargets, _ := json.Marshal(rebound.Targets)
	if beforeHash != afterHash || !os.SameFile(beforeInfo, afterInfo) || string(frozenTargets) != string(reboundTargets) {
		t.Fatal("actual restart changed source bytes/file identity/frozen target tuple")
	}
	t.Logf("BOOT_SOURCE_PROOF auth_sha256_before=%x after=%x same_physical_file=%t frozen_targets_unchanged=%t boot_changed=%t", beforeHash, afterHash, os.SameFile(beforeInfo, afterInfo), string(frozenTargets) == string(reboundTargets), rebound.Scope.BootEpoch != nativeBinding.Scope.BootEpoch)
	path = "/usage-service/credential-quota?authIndex=" + rebound.AuthIndex
	code, data = api("GET", path, nil)
	if code != 200 {
		t.Fatal(string(data))
	}
	var stale map[string]any
	json.Unmarshal(data, &stale)
	retained, _ = json.Marshal(stale["lastSuccess"])
	if stale["stale"] != true || string(retained) != string(oldSuccess) {
		t.Fatal("verified boot rebind did not retain stale history")
	}
	q = map[string]any{"scope": nativeBinding.Scope, "targets": nativeBinding.Targets, "auth_index": nativeBinding.AuthIndex, "sourceId": nativeSourceID, "subjectId": nativeSubjectID, "version": stale["version"], "sourceVersion": stale["sourceVersion"]}
	code, _ = api("POST", "/usage-service/credential-quota", q)
	if code != 409 {
		t.Fatal("old boot accepted query")
	}
	mode.Store(0)
	q["scope"] = rebound.Scope
	q["targets"] = rebound.Targets
	q["auth_index"] = rebound.AuthIndex
	code, data = api("POST", "/usage-service/credential-quota", q)
	if code != 202 {
		t.Fatal(string(data))
	}
	json.Unmarshal(data, &first)
	waitJob(first.ID)
	_, data = api("GET", path, nil)
	if !strings.Contains(string(data), `"stale":false`) || !strings.Contains(string(data), `"queryStatus":"success"`) {
		t.Fatalf("new boot did not obtain fresh selected quota: %s", data)
	}
}

// Diagnostic for the retained failed fixture, NOT a product behavior RED.
// encoding/json reuses slice backing arrays on a second decode. Source ordering
// can differ across catalog reads; an old shallow capture is not an immutable
// receipt. The acceptance test above freezes targets and compares actual bytes.
func TestCredentialQuotaFixtureFreezesTargetReceipts(t *testing.T) {
	var view struct {
		Sources []rc.RuntimeBinding `json:"sources"`
	}
	first := `{"sources":[{"auth_index":"a","targets":[{"resource_id":"issued-A"}]},{"auth_index":"b","targets":[{"resource_id":"issued-B"}]}]}`
	second := `{"sources":[{"auth_index":"b","targets":[{"resource_id":"issued-B"}]},{"auth_index":"a","targets":[{"resource_id":"issued-A"}]}]}`
	if err := json.Unmarshal([]byte(first), &view); err != nil {
		t.Fatal(err)
	}
	aliased := view.Sources[0]
	frozen := view.Sources[0]
	frozen.Targets = append([]rc.Target(nil), frozen.Targets...)
	if err := json.Unmarshal([]byte(second), &view); err != nil {
		t.Fatal(err)
	}
	if aliased.Targets[0].ResourceID != "issued-B" || frozen.Targets[0].ResourceID != "issued-A" {
		t.Fatal("diagnostic did not reproduce mutable fixture capture")
	}
	t.Log("FIXTURE_ALIAS_PROOF shallow capture changed issued-A -> issued-B on reordered JSON decode; immutable copy retained issued-A")
}
