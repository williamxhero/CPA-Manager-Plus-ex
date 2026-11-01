package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	cp "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy"
	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

// Opt-in ACTUAL production Core, with its real runtime executors/admission
// registered by production bootstrap (not the catalog-only quota process test).
func policyProductionGateCore(t *testing.T, manualDisabled ...bool) string {
	t.Helper()
	if os.Getenv("CORE_GATE_CONSUMER_ENABLED") != "1" {
		t.Skip("requires explicit merged gate artifact environment")
	}
	binary := os.Getenv("CORE_OFFICIAL_QUERY_TEST_BINARY")
	raw, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != os.Getenv("CORE_OFFICIAL_QUERY_EXPECTED_SHA256") {
		t.Fatal("actual production Core hash mismatch")
	}
	info, err := buildinfo.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	revision, modified := "", ""
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" {
			revision = setting.Value
		}
		if setting.Key == "vcs.modified" {
			modified = setting.Value
		}
	}
	if len(revision) != 40 || revision != os.Getenv("CORE_OFFICIAL_QUERY_EXPECTED_REVISION") || modified != "false" {
		t.Fatal("production provenance unverified")
	}
	t.Logf("actual production Core revision=%s sha256=%s modified=%s", revision, hex.EncodeToString(digest[:]), modified)
	dir := t.TempDir()
	auth := filepath.Join(dir, "auth")
	if err := os.MkdirAll(auth, 0700); err != nil {
		t.Fatal(err)
	}
	disabled := len(manualDisabled) > 0 && manualDisabled[0]
	if err := os.WriteFile(filepath.Join(auth, "native.json"), []byte(fmt.Sprintf(`{"type":"codex","access_token":"SYNTHETIC-CONSUMER-TOKEN","account_id":"synthetic-native-account","disabled":%t}`, disabled)), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	// Deny all external network, including unexpected background work. The
	// healthy intent test does not claim a completed quota observation.
	deny := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(502) }))
	t.Cleanup(deny.Close)
	config := fmt.Sprintf("config-version: 8\nserver: {host: 127.0.0.1, port: %d, discovery: {enabled: false}}\nmanagement: {secret-key: test-password, allow-remote: false, disable-control-panel: true}\noauth: {auth-dir: %q}\nresource-catalog: {enabled: true}\nrequests: {proxy-url: %q}\naccess: {api-keys: [synthetic-client]}\nplugins: {enabled: false}\n", port, filepath.ToSlash(auth), deny.URL)
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	log, err := os.Create(filepath.Join(dir, "core.log"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "-config", path, "-local-model")
	cmd.Dir = dir
	cmd.Stdout = log
	cmd.Stderr = log
	for _, key := range []string{"PATH", "SystemRoot", "WINDIR", "TEMP", "TMP"} {
		if value := os.Getenv(key); value != "" {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	cmd.Env = append(cmd.Env, "HOME="+dir, "USERPROFILE="+dir, "HTTP_PROXY="+deny.URL, "HTTPS_PROXY="+deny.URL, "NO_PROXY=127.0.0.1,localhost")
	if err := cmd.Start(); err != nil {
		log.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
		log.Close()
		if t.Failed() {
			body, _ := os.ReadFile(log.Name())
			t.Log(string(body))
		}
	})
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(20 * time.Second)
	for {
		req, _ := http.NewRequest("GET", base+"/v8/management/credential-quota/sources", nil)
		req.Header.Set("Authorization", "Bearer test-password")
		response, e := client.Do(req)
		if e == nil {
			response.Body.Close()
			if response.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("actual production readiness unavailable")
		}
		time.Sleep(25 * time.Millisecond)
	}
	return base
}

func TestCredentialGateActualNativeEligibilityCannotReplaceFreshONPrerequisite(t *testing.T) {
	base := policyProductionGateCore(t)
	cfg := testutil.NewConfig(t)
	db := testutil.NewStore(t, cfg)
	if err := db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: base, ManagementKey: "test-password"}}); err != nil {
		t.Fatal(err)
	}
	h := New(cfg, db, collector.NewManager(cfg, db)).Handler()
	rr := testutil.Request(t, h, "GET", "/usage-service/credential-quota", "", testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	var sources struct {
		Sources []cq.Source `json:"sources"`
	}
	testutil.DecodeJSON(t, rr, &sources)
	var source cq.Source
	for _, s := range sources.Sources {
		if s.Protocol == "codex-oauth-wham" && s.State == "ready" {
			source = s
			break
		}
	}
	if source.AuthIndex == "" {
		t.Fatal("no actual native query-capable source")
	}
	rr = testutil.Request(t, h, "PUT", "/usage-service/credential-policy", policyBody(t, source, 0, true), testutil.AdminKey)
	testutil.RequireStatus(t, rr, 503)
	var rejected struct {
		Error string `json:"error"`
	}
	testutil.DecodeJSON(t, rr, &rejected)
	if rejected.Error != "fresh_authoritative_evidence_required" {
		t.Fatalf("wrong ON prerequisite rejection: %s", rr.Body.String())
	}
	var status cp.Status
	// Reopen actual SQLite and recreate Manager facade, not Core.
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = testutil.NewStore(t, cfg)
	h = New(cfg, db, collector.NewManager(cfg, db)).Handler()
	rr = testutil.Request(t, h, "GET", "/usage-service/credential-policy?authIndex="+source.AuthIndex, "", testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	testutil.DecodeJSON(t, rr, &status)
	if status.Enabled || status.Generation != 0 || status.Applied || status.Operation != nil {
		t.Fatal("failed ON created intent or gate authority")
	}
	rr = testutil.Request(t, h, "PUT", "/usage-service/credential-policy", policyBody(t, source, 0, false), testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	var gates cp.GateSnapshot
	req, _ := http.NewRequest("GET", base+"/v8/management/inference-gates", nil)
	req.Header.Set("Authorization", "Bearer test-password")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if json.NewDecoder(response.Body).Decode(&gates) != nil || gates.GateRevision != 0 || len(gates.Gates) != 0 {
		t.Fatal("healthy ON/OFF mutated gate without quota evidence")
	}
}

func TestCredentialGateActualOwnResumePreservesOtherOwnerAndOFF(t *testing.T) {
	base := policyProductionGateCore(t, true)
	upstreamURL, _ := url.Parse(base)
	proxy := httputil.NewSingleHostReverseProxy(upstreamURL)
	var lost atomic.Bool
	proxy.ModifyResponse = func(response *http.Response) error {
		if response.Request.Method == "POST" && response.Request.URL.Path == "/v8/management/inference-gates/operations" && response.StatusCode == 200 && lost.CompareAndSwap(false, true) {
			return errors.New("controlled lost ACK after actual commit")
		}
		return nil
	}
	edge := httptest.NewServer(proxy)
	defer edge.Close()
	cfg := testutil.NewConfig(t)
	db := testutil.NewStore(t, cfg)
	if err := db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: edge.URL, ManagementKey: "test-password"}}); err != nil {
		t.Fatal(err)
	}
	h := New(cfg, db, collector.NewManager(cfg, db)).Handler()
	rr := testutil.Request(t, h, "GET", "/usage-service/credential-quota", "", testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	var sources struct {
		Sources []cq.Source `json:"sources"`
	}
	testutil.DecodeJSON(t, rr, &sources)
	var source cq.Source
	for _, s := range sources.Sources {
		if s.Protocol == "codex-oauth-wham" && s.State == "ready" {
			source = s
			break
		}
	}
	if source.AuthIndex == "" {
		t.Fatal("no actual native source")
	}
	rr = testutil.Request(t, h, "PUT", "/usage-service/credential-policy", policyBody(t, source, 0, false), testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	var saved cp.Status
	testutil.DecodeJSON(t, rr, &saved)
	if !source.ManualDisabled {
		t.Fatal("manual-disabled precondition was not actually present")
	}
	cfg2 := testutil.NewConfig(t)
	db2 := testutil.NewStore(t, cfg2)
	if err := db2.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: base, ManagementKey: "test-password"}}); err != nil {
		t.Fatal(err)
	}
	h2 := New(cfg2, db2, collector.NewManager(cfg2, db2)).Handler()
	rr = testutil.Request(t, h2, "PUT", "/usage-service/credential-policy", policyBody(t, source, 0, false), testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	var independent cp.Status
	testutil.DecodeJSON(t, rr, &independent)
	if independent.Owner == saved.Owner {
		t.Fatal("independent DB acquired another installation owner")
	}
	// Explicit REAL management seed: not synthetic quota evidence, not automatic
	// exhaustion proof. Every operation ACK/readback comes from actual Core99c.
	for i, owner := range []string{saved.Owner, independent.Owner} {
		op := cp.GateOperation{Version: 1, OpID: uuid.NewString(), ExpectedGateRevision: uint64(i), Binding: source.Binding, AuthIndex: source.AuthIndex, SourceVersion: source.SourceVersion, Subject: "credential", Owner: owner, Action: "pause", Reason: "quota_windows_exhausted", EvidenceID: uuid.NewString()}
		body, _ := json.Marshal(op)
		req, _ := http.NewRequest("POST", base+"/v8/management/inference-gates/operations", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer test-password")
		req.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var ack cp.GateResult
		err = json.NewDecoder(response.Body).Decode(&ack)
		response.Body.Close()
		if response.StatusCode != 200 || err != nil || !ack.Confirms(op) {
			t.Fatalf("real pause seed not durably applied: status=%d", response.StatusCode)
		}
	}
	request := map[string]any{"scope": source.Scope, "targets": source.Targets, "auth_index": source.AuthIndex, "sourceVersion": source.SourceVersion, "expectedGeneration": saved.Generation, "owner": saved.Owner, "opId": uuid.NewString()}
	body, _ := json.Marshal(request)
	rr = testutil.Request(t, h, "POST", "/usage-service/credential-policy/resume", string(body), "")
	testutil.RequireStatus(t, rr, 401)
	request["owner"] = independent.Owner
	foreign, _ := json.Marshal(request)
	rr = testutil.Request(t, h, "POST", "/usage-service/credential-policy/resume", string(foreign), testutil.AdminKey)
	testutil.RequireStatus(t, rr, 409)
	request["owner"] = saved.Owner
	request["expectedGeneration"] = saved.Generation + 1
	stale, _ := json.Marshal(request)
	rr = testutil.Request(t, h, "POST", "/usage-service/credential-policy/resume", string(stale), testutil.AdminKey)
	testutil.RequireStatus(t, rr, 409)
	request["expectedGeneration"] = saved.Generation
	rr = testutil.Request(t, h, "POST", "/usage-service/credential-policy/resume", string(body), testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	var resumed cp.Status
	testutil.DecodeJSON(t, rr, &resumed)
	if !lost.Load() || resumed.Applied || resumed.Operation == nil || resumed.Operation.ReceiptConfirmed || resumed.Operation.State != "pending" {
		t.Fatal("lost actual ACK was inferred applied")
	}
	// Reopen SQLite/Manager and reconcile the SAME durable opId/body.
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = testutil.NewStore(t, cfg)
	h = New(cfg, db, collector.NewManager(cfg, db)).Handler()
	rr = testutil.Request(t, h, "POST", "/usage-service/credential-policy/resume", string(body), testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	testutil.DecodeJSON(t, rr, &resumed)
	if resumed.Enabled || resumed.Generation != saved.Generation || !resumed.Applied || resumed.Operation == nil || resumed.Operation.Action != "clear" || !resumed.Operation.CurrentApplied || !resumed.Operation.ReceiptConfirmed {
		t.Fatalf("own resume lacked actual current ACK/readback or changed OFF: %s", rr.Body.String())
	}
	req, _ := http.NewRequest("GET", base+"/v8/management/inference-gates", nil)
	req.Header.Set("Authorization", "Bearer test-password")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var gates cp.GateSnapshot
	err = json.NewDecoder(response.Body).Decode(&gates)
	response.Body.Close()
	if err != nil || gates.GateRevision != 3 || len(gates.Gates) != 1 || gates.Gates[0].Owner != independent.Owner {
		t.Fatal("own resume cleared another owner or readback changed")
	}
	rr = testutil.Request(t, h, "POST", "/usage-service/credential-policy/resume", string(body), testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	testutil.DecodeJSON(t, rr, &resumed)
	if !resumed.Applied || resumed.Operation.OpID != request["opId"] || resumed.Operation.GateRevision != 3 {
		t.Fatal("idempotent resume replay changed receipt")
	}
	request["owner"] = independent.Owner
	request["opId"] = uuid.NewString()
	request["expectedGeneration"] = independent.Generation
	secondBody, _ := json.Marshal(request)
	rr = testutil.Request(t, h2, "POST", "/usage-service/credential-policy/resume", string(secondBody), testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	testutil.DecodeJSON(t, rr, &resumed)
	if !resumed.Applied || resumed.Operation.GateRevision != 4 || resumed.Enabled {
		t.Fatal("second DB could not clear only its own restriction")
	}
	rr = testutil.Request(t, h, "GET", "/usage-service/credential-quota", "", testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	testutil.DecodeJSON(t, rr, &sources)
	for _, s := range sources.Sources {
		if s.AuthIndex == source.AuthIndex && !s.ManualDisabled {
			t.Fatal("own management resume changed manualDisabled")
		}
	}
}
