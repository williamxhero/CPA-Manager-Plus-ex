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
		if r.URL.Path == "/zen/go/v1/usage" {
			if r.Header.Get("Authorization") != "Bearer PLUGIN-SECRET-CANARY" {
				t.Error("wrong selected plugin key")
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
	cmd.Env = append(cmd.Env, "HOME="+dir, "USERPROFILE="+dir, "QUOTA_FIXTURE_LISTEN="+address, "QUOTA_FIXTURE_UPSTREAM="+upstream.Listener.Addr().String(), "QUOTA_FIXTURE_CA="+ca, "QUOTA_FIXTURE_PLUGIN_DIR="+os.Getenv("CORE_QUOTA_PLUGIN_DIR"))
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
			Protocol string `json:"protocol"`
		}
	}
	if json.Unmarshal(data, &view) != nil {
		t.Fatal("invalid source view")
	}
	native, plugin := false, false
	var nativeBinding rc.RuntimeBinding
	for _, source := range view.Sources {
		if source.Protocol != "codex-oauth-wham" && source.Protocol != "opencode-go-usage" {
			continue
		}
		if !requirePlugin && source.Protocol == "opencode-go-usage" {
			continue
		}
		if source.Protocol == "opencode-go-usage" {
			plugin = true
			if len(source.Targets) != 2 {
				t.Fatal("full projection target group missing")
			}
		} else {
			native = true
			nativeBinding = source.RuntimeBinding
		}
		path := "/usage-service/credential-quota?authIndex=" + source.AuthIndex
		code, data = api("GET", path, nil)
		if code != 200 {
			t.Fatal(string(data))
		}
		var state map[string]any
		json.Unmarshal(data, &state)
		q := map[string]any{"scope": source.Scope, "targets": source.Targets, "auth_index": source.AuthIndex, "version": state["version"]}
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
		if code != 200 || !strings.Contains(string(data), `"queryStatus":"success"`) {
			t.Fatalf("readback: %d %s", code, data)
		}
		if source.Protocol == "opencode-go-usage" && (!strings.Contains(string(data), `rate-limited`) || !strings.Contains(string(data), `\"usedPercent\":42`)) {
			t.Fatalf("compiled plugin lost source status: %s", data)
		}
	}
	if !native || requirePlugin && !plugin {
		t.Fatal("native/plugin coverage missing")
	}
	expectedCalls := int64(1)
	if requirePlugin {
		expectedCalls = 2
	}
	if calls.Load() != expectedCalls {
		t.Fatalf("GET or fallback duplicated query calls=%d", calls.Load())
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
		q := map[string]any{"scope": nativeBinding.Scope, "targets": nativeBinding.Targets, "auth_index": nativeBinding.AuthIndex, "version": before["version"]}
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
	mode.Store(3)
	q := map[string]any{"scope": nativeBinding.Scope, "targets": nativeBinding.Targets, "auth_index": nativeBinding.AuthIndex, "version": before["version"]}
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
}
