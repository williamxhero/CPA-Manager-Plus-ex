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
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
	cp "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy"
	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	fr "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fullrefresh"
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
	testutil.RequireStatus(t, rr, 400)
	var gates cp.GateSnapshot
	req, _ := http.NewRequest("GET", base+"/v8/management/inference-gates", nil)
	req.Header.Set("Authorization", "Bearer test-password")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if json.NewDecoder(response.Body).Decode(&gates) != nil || gates.GateRevision != 0 || len(gates.Gates) != 0 {
		t.Fatal("failed ON or refused disable mutated gate without quota evidence")
	}
}

func TestCredentialGateActualOwnResumePreservesOtherOwnerAndHistoricalOff(t *testing.T) {
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
	edge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v8/management/credential-quota/query" {
			proxy.ServeHTTP(w, r)
			return
		}
		// Supply a controlled positive quota observation at the query boundary.
		// Inventory, eligibility, gate writes, receipts and ledger are real Core.
		var query cq.Query
		if err := json.NewDecoder(r.Body).Decode(&query); err != nil {
			http.Error(w, "invalid quota fixture query", 400)
			return
		}
		now := time.Now()
		reset := now.Add(time.Hour).Format(time.RFC3339)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(cq.CoreObservation{
			Binding: query.Binding, SourceVersion: query.SourceVersion, Protocol: "codex-oauth-wham", ProtocolVersion: 1,
			QueryStatus: "success", Complete: true, RecoveryEligible: true, ObservedAtMS: now.UnixMilli(), ReceivedAtMS: now.UnixMilli(),
			AccountProvenance: "selected_oauth_account", TimestampProvenance: "upstream_http_date",
			Windows: []cq.Window{
				{Name: "primary", UsedPercent: 25, RemainingFraction: .75, SourceStatus: "allowed", ResetAt: reset},
				{Name: "secondary", UsedPercent: 25, RemainingFraction: .75, SourceStatus: "allowed", ResetAt: reset},
			},
		}); err != nil {
			t.Error(err)
		}
	}))
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
	if !source.ManualDisabled {
		t.Fatal("manual-disabled precondition was not actually present")
	}
	reopenWithHistoricalOff := func(cfg config.Config, db *store.Store) (*store.Store, http.Handler, cp.Status) {
		t.Helper()
		ctx := context.Background()
		installation, err := db.CredentialPolicyInstallation(ctx)
		if err != nil {
			t.Fatal(err)
		}
		// Seed a pre-upgrade document, with the sealed Core receipt and explicit
		// enabled:false from the embedded Status. Only startup may backfill it.
		state := cp.Status{Source: source, SubjectID: cp.SubjectID(source), Owner: cp.OwnerNamespace + installation, Generation: 1, Enabled: false, State: "off"}
		document, err := json.Marshal(struct {
			cp.Status
			IssuedSource *cq.Source `json:"issuedSource"`
		}{Status: state, IssuedSource: &source})
		if err != nil {
			t.Fatal(err)
		}
		if err := db.SaveCredentialPolicy(ctx, store.CredentialPolicyRecord{ID: state.SubjectID, Generation: state.Generation, Document: string(document)}, 0); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		db = testutil.NewStore(t, cfg)
		h := New(cfg, db, collector.NewManager(cfg, db)).Handler()
		rr := testutil.Request(t, h, "GET", "/usage-service/credential-policy?subjectId="+state.SubjectID, "", testutil.AdminKey)
		testutil.RequireStatus(t, rr, 200)
		var migrated cp.Status
		testutil.DecodeJSON(t, rr, &migrated)
		if !migrated.Enabled || migrated.Generation != state.Generation+1 || migrated.Owner != state.Owner || migrated.SubjectID != state.SubjectID || migrated.Applied || migrated.Operation != nil {
			t.Fatalf("startup did not backfill participation without changing owner or fabricating a receipt: %s", rr.Body.String())
		}
		return db, h, state
	}
	var saved cp.Status
	db, h, saved = reopenWithHistoricalOff(cfg, db)
	generation := saved.Generation + 1
	requirePolicyError := func(rr *httptest.ResponseRecorder, status int, want string) {
		t.Helper()
		testutil.RequireStatus(t, rr, status)
		var reply struct {
			Error string `json:"error"`
		}
		testutil.DecodeJSON(t, rr, &reply)
		if reply.Error != want {
			t.Fatalf("policy error = %q, want %q", reply.Error, want)
		}
	}
	rr = testutil.Request(t, h, "PUT", "/usage-service/credential-policy", policyBody(t, source, generation, false), testutil.AdminKey)
	requirePolicyError(rr, 400, "automatic_pause_always_on")
	cfg2 := testutil.NewConfig(t)
	db2 := testutil.NewStore(t, cfg2)
	if err := db2.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: base, ManagementKey: "test-password"}}); err != nil {
		t.Fatal(err)
	}
	_, h2, independent := reopenWithHistoricalOff(cfg2, db2)
	if independent.Owner == saved.Owner {
		t.Fatal("independent DB acquired another installation owner")
	}
	// Seed pauses through actual Core management, without claiming that the
	// pauses came from automatic exhaustion detection.
	pauseOwner := func(owner string, revision uint64) {
		t.Helper()
		op := cp.GateOperation{Version: 1, OpID: uuid.NewString(), ExpectedGateRevision: revision, Binding: source.Binding, AuthIndex: source.AuthIndex, SourceVersion: source.SourceVersion, Subject: "credential", Owner: owner, Action: "pause", Reason: "quota_windows_exhausted", EvidenceID: uuid.NewString()}
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
	pauseOwner(saved.Owner, 0)
	pauseOwner(independent.Owner, 1)
	readGates := func() cp.GateSnapshot {
		t.Helper()
		req, _ := http.NewRequest("GET", base+"/v8/management/inference-gates", nil)
		req.Header.Set("Authorization", "Bearer test-password")
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var gates cp.GateSnapshot
		if err := json.NewDecoder(response.Body).Decode(&gates); response.StatusCode != 200 || err != nil || !gates.Ready {
			t.Fatalf("real gate ledger unavailable: status=%d decode=%v", response.StatusCode, err)
		}
		return gates
	}
	before := readGates()
	var foreignGate cp.GateRestriction
	for _, gate := range before.Gates {
		if gate.Owner == independent.Owner {
			foreignGate = gate
		}
	}
	if before.GateRevision != 2 || len(before.Gates) != 2 || foreignGate.Owner == "" {
		t.Fatal("missing both real pauses before own resume")
	}
	resumeBody := func(owner string, expected int64, opID string) string {
		t.Helper()
		body, err := json.Marshal(cp.ResumeRequest{Binding: source.Binding, AuthIndex: source.AuthIndex, SourceVersion: source.SourceVersion, ExpectedGeneration: &expected, Owner: owner, OpID: opID})
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	opID := uuid.NewString()
	body := resumeBody(saved.Owner, generation, opID)
	stale := resumeBody(saved.Owner, saved.Generation, opID)
	rr = testutil.Request(t, h, "POST", "/usage-service/credential-policy/resume", body, "")
	testutil.RequireStatus(t, rr, 401)
	rr = testutil.Request(t, h, "POST", "/usage-service/credential-policy/resume", resumeBody(independent.Owner, generation, opID), testutil.AdminKey)
	requirePolicyError(rr, 409, "identity_changed")
	rr = testutil.Request(t, h, "POST", "/usage-service/credential-policy/resume", resumeBody(saved.Owner, generation+1, opID), testutil.AdminKey)
	requirePolicyError(rr, 409, "policy_generation_conflict")
	rr = testutil.Request(t, h, "POST", "/usage-service/credential-policy/resume", stale, testutil.AdminKey)
	requirePolicyError(rr, 409, "policy_generation_conflict")
	rr = testutil.Request(t, h, "POST", "/usage-service/credential-policy/resume", body, testutil.AdminKey)
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
	manager := New(cfg, db, collector.NewManager(cfg, db))
	h = manager.Handler()
	rr = testutil.Request(t, h, "POST", "/usage-service/credential-policy/resume", body, testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	testutil.DecodeJSON(t, rr, &resumed)
	if !resumed.Enabled || resumed.Generation != generation || !resumed.Applied || resumed.Operation == nil || resumed.Operation.PolicyGeneration != generation || resumed.Operation.Action != "clear" || !resumed.Operation.CurrentApplied || !resumed.Operation.ReceiptConfirmed {
		t.Fatalf("own resume lacked actual current ACK/readback or changed stored intent: %s", rr.Body.String())
	}
	if gates := readGates(); gates.GateRevision != 3 || len(gates.Gates) != 1 || !reflect.DeepEqual(gates.Gates[0], foreignGate) {
		t.Fatal("own resume cleared or changed the other owner's pause")
	}
	// The old generation cannot replay even after the same opId has a receipt.
	rr = testutil.Request(t, h, "POST", "/usage-service/credential-policy/resume", stale, testutil.AdminKey)
	requirePolicyError(rr, 409, "policy_generation_conflict")
	rr = testutil.Request(t, h, "POST", "/usage-service/credential-policy/resume", body, testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	testutil.DecodeJSON(t, rr, &resumed)
	if !resumed.Applied || resumed.Operation == nil || resumed.Operation.OpID != opID || resumed.Operation.GateRevision != 3 {
		t.Fatal("idempotent resume replay changed receipt")
	}
	rr = testutil.Request(t, h2, "POST", "/usage-service/credential-policy/resume", resumeBody(independent.Owner, independent.Generation+1, uuid.NewString()), testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	testutil.DecodeJSON(t, rr, &resumed)
	if !resumed.Applied || resumed.Operation == nil || resumed.Operation.GateRevision != 4 || !resumed.Enabled || resumed.Generation != independent.Generation+1 {
		t.Fatal("second DB could not clear only its own restriction without changing backfilled intent")
	}
	requireManualDisabled := func() {
		t.Helper()
		rr := testutil.Request(t, h, "GET", "/usage-service/credential-quota", "", testutil.AdminKey)
		testutil.RequireStatus(t, rr, 200)
		var inventory struct {
			Sources []cq.Source `json:"sources"`
		}
		testutil.DecodeJSON(t, rr, &inventory)
		for _, s := range inventory.Sources {
			if s.AuthIndex == source.AuthIndex && s.SourceVersion == source.SourceVersion {
				if !s.ManualDisabled {
					t.Fatal("clearing an automatic pause changed manualDisabled")
				}
				return
			}
		}
		t.Fatal("manually disabled credential missing after clear")
	}
	requireManualDisabled()

	// Now prove the automatic clear too, independently of explicit resume. Use
	// the real Manager start/completion path, with no plugin or provider access.
	pauseOwner(saved.Owner, 4)
	app := manager.AppContext()
	completed := make(chan fr.CompletedObservation, 1)
	app.FullRefreshService.Subscribe(func(_ context.Context, event fr.CompletedObservation) { completed <- event })
	rr = testutil.Request(t, h, "GET", "/usage-service/credential-quota?authIndex="+source.AuthIndex, "", testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	var current cq.Status
	testutil.DecodeJSON(t, rr, &current)
	query, err := json.Marshal(cq.Query{Binding: current.Binding, AuthIndex: current.AuthIndex, SourceID: current.SourceID, SubjectID: current.SubjectID, SourceVersion: current.SourceVersion, Version: current.Version})
	if err != nil {
		t.Fatal(err)
	}
	rr = testutil.Request(t, h, "POST", "/usage-service/credential-quota", string(query), testutil.AdminKey)
	testutil.RequireStatus(t, rr, 202)
	var job cq.Job
	testutil.DecodeJSON(t, rr, &job)
	select {
	case event := <-completed:
		if event.AttemptID != job.ID || event.Credential == nil || event.Credential.Stale || event.Credential.Attempt == nil || event.Credential.Attempt.QueryStatus != "success" {
			t.Fatalf("controlled quota query did not complete successfully: %+v", event)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("missing quota completion for automatic recovery")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := app.CredentialPolicyService.ProcessCompletions(ctx); err != nil {
		t.Fatal(err)
	}
	if err := app.CredentialPolicyService.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	rr = testutil.Request(t, h, "GET", "/usage-service/credential-policy?authIndex="+source.AuthIndex, "", testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	var recovered cp.Status
	testutil.DecodeJSON(t, rr, &recovered)
	if !recovered.Enabled || recovered.Generation != generation || !recovered.Applied || recovered.Operation == nil || recovered.Operation.Action != "clear" || !recovered.Operation.ReceiptConfirmed || !recovered.Operation.CurrentApplied || recovered.Operation.GateRevision != 6 {
		t.Fatalf("automatic clear not confirmed under backfilled intent: %s", rr.Body.String())
	}
	record, err := db.CredentialPolicyOperation(ctx, recovered.Operation.OpID)
	if err != nil || !record.Automatic || record.Generation != generation {
		t.Fatalf("recovery did not come from an automatic operation: automatic=%v generation=%d err=%v", record.Automatic, record.Generation, err)
	}
	if gates := readGates(); gates.GateRevision != 6 || len(gates.Gates) != 0 {
		t.Fatalf("expected no automatic pause after recovery: revision=%d gates=%d", gates.GateRevision, len(gates.Gates))
	}
	requireManualDisabled()
}
