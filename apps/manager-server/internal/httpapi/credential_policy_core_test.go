package httpapi

import (
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"github.com/google/uuid"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	cp "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy"
	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	mc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/managerconfig"
	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

// This proves actual issued-binding restart fencing, NOT positive gate ON/ACK.
// The prior ON document is explicitly a local coordinator fixture.
func TestCredentialPolicyActualCoreRestartRebindsWithoutUISave(t *testing.T) {
	var stop, restart func()
	base := policyOldCore(t, &stop, &restart)
	cfg := testutil.NewConfig(t)
	db := testutil.NewStore(t, cfg)
	ctx := context.Background()
	if err := db.SaveManagerConfig(ctx, store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: base, ManagementKey: "test-password"}}); err != nil {
		t.Fatal(err)
	}
	connection := mc.New(cfg, db, nil)
	quota := cq.New(db, rc.New(nil, connection))
	coordinator := cp.New(db, quota, connection, false)
	sources, err := quota.Sources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var old cq.Source
	for _, source := range sources {
		if source.State == "ready" && source.AuthIndex != "" {
			old = source
			break
		}
	}
	if old.AuthIndex == "" {
		t.Fatal("no actual ready source")
	}
	installation, err := db.CredentialPolicyInstallation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	id := cp.SubjectID(old)
	state := cp.Status{Source: old, SubjectID: id, Generation: 1, Owner: cp.OwnerNamespace + installation, Enabled: true}
	document, _ := json.Marshal(state)
	if err := db.SaveCredentialPolicy(ctx, store.CredentialPolicyRecord{ID: id, Generation: 1, Document: string(document)}, 0); err != nil {
		t.Fatal(err)
	}
	oldQuery := cq.Query{Binding: old.Binding, SourceVersion: old.SourceVersion, AuthIndex: old.AuthIndex, Version: 1}
	oldAttempt := uuid.NewString()
	if err := coordinator.CaptureCredentialQueryStart(ctx, oldQuery, oldAttempt); err != nil {
		t.Fatal(err)
	}
	restart()
	sources, err = quota.Sources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var current cq.Source
	for _, source := range sources {
		if cp.SubjectID(source) == id {
			current = source
			break
		}
	}
	if current.AuthIndex == "" || current.Scope.BootEpoch == old.Scope.BootEpoch {
		t.Fatal("restart did not issue fresh boot for same authority")
	}
	query := cq.Query{Binding: current.Binding, SourceVersion: current.SourceVersion, AuthIndex: current.AuthIndex, Version: 2}
	attempt := uuid.NewString()
	if err := coordinator.CaptureCredentialQueryStart(ctx, query, attempt); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(query)
	if err := db.CheckCredentialPolicyQuery(ctx, store.CredentialPolicyQueryFence{AttemptID: attempt, SubjectID: id, Generation: 1, Owner: state.Owner, Source: string(body), Enabled: true}); err != nil {
		t.Fatalf("new boot never captured active policy without UI resave: %v", err)
	}
	local, err := coordinator.StoredStatus(ctx, id)
	if err != nil || !local.Enabled || local.Generation != 1 || local.Scope.BootEpoch != current.Scope.BootEpoch {
		t.Fatal("current binding not durably rebound preserving intent")
	}
	if err := coordinator.CaptureCredentialQueryStart(ctx, oldQuery, uuid.NewString()); err == nil {
		t.Fatal("old boot start accepted")
	}
	out, err := coordinator.CompleteCredentialQuery(ctx, oldQuery, cq.Status{Source: old}, oldAttempt)
	if err != nil || out.Decision.Action != "none" || out.Decision.Reason != "source_authority_changed" {
		t.Fatalf("old boot completion not independently fenced: %+v %v", out, err)
	}
}

// Only actual compiled Core is used for positive identity. This initial fixture
// deliberately has NO gate producer; it cannot prove positive gate admission.
func policyOldCore(t *testing.T, stopOut ...*func()) string {
	t.Helper()
	binary := os.Getenv("CORE_QUOTA_TEST_BINARY")
	if binary == "" {
		t.Skip("requires independent immutable actual Core manifest")
	}
	raw, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(raw)
	if hex.EncodeToString(hash[:]) != os.Getenv("CORE_QUOTA_EXPECTED_SHA256") {
		t.Fatal("actual Core hash mismatch")
	}
	info, err := buildinfo.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	revision, modified := "", ""
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			revision = s.Value
		}
		if s.Key == "vcs.modified" {
			modified = s.Value
		}
	}
	if revision != os.Getenv("CORE_QUOTA_EXPECTED_REVISION") || len(revision) != 40 || modified != "false" {
		t.Fatal("actual Core revision unverified")
	}
	t.Logf("actual Core %s sha256=%s modified=%s", revision, os.Getenv("CORE_QUOTA_EXPECTED_SHA256"), modified)
	pluginFile := filepath.Join(os.Getenv("CORE_QUOTA_PLUGIN_DIR"), "opencode-go-cliproxyapi.dll")
	pluginBytes, err := os.ReadFile(pluginFile)
	if err != nil {
		t.Fatal(err)
	}
	pluginHash := sha256.Sum256(pluginBytes)
	if hex.EncodeToString(pluginHash[:]) != os.Getenv("CORE_QUOTA_PLUGIN_EXPECTED_SHA256") {
		t.Fatal("actual plugin hash mismatch")
	}
	pluginInfo, err := buildinfo.ReadFile(pluginFile)
	if err != nil {
		t.Fatal(err)
	}
	pr, pm := "", ""
	for _, setting := range pluginInfo.Settings {
		if setting.Key == "vcs.revision" {
			pr = setting.Value
		}
		if setting.Key == "vcs.modified" {
			pm = setting.Value
		}
	}
	if pr != os.Getenv("CORE_QUOTA_PLUGIN_EXPECTED_REVISION") || len(pr) != 40 || pm != "false" || os.Getenv("CORE_QUOTA_PLUGIN_SDK_REVISION") != revision {
		t.Fatal("actual plugin source/SDK provenance unverified")
	}
	t.Logf("actual plugin %s sha256=%s modified=%s SDK=%s", pr, os.Getenv("CORE_QUOTA_PLUGIN_EXPECTED_SHA256"), pm, revision)
	dir := t.TempDir()
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("policy status unexpectedly queried upstream")
		w.WriteHeader(500)
	}))
	t.Cleanup(upstream.Close)
	ca := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: upstream.TLS.Certificates[0].Certificate[0]}), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	state := filepath.Join(dir, "state")
	if err := os.MkdirAll(state, 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "-test.run=^TestCredentialQuotaProcessFixture$", "-test.timeout=120s")
	cmd.Dir = dir
	for _, k := range []string{"PATH", "SystemRoot", "WINDIR", "TEMP", "TMP"} {
		if v := os.Getenv(k); v != "" {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	cmd.Env = append(cmd.Env, "HOME="+dir, "USERPROFILE="+dir, "QUOTA_FIXTURE_LISTEN="+address, "QUOTA_FIXTURE_UPSTREAM="+upstream.Listener.Addr().String(), "QUOTA_FIXTURE_CA="+ca, "QUOTA_FIXTURE_PLUGIN_DIR="+os.Getenv("CORE_QUOTA_PLUGIN_DIR"), "QUOTA_FIXTURE_STATE_DIR="+state)
	log, err := os.Create(filepath.Join(dir, "core.log"))
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = log
	cmd.Stderr = log
	if err := cmd.Start(); err != nil {
		log.Close()
		t.Fatal(err)
	}
	var stopOnce sync.Once
	stop := func() { stopOnce.Do(func() { cmd.Process.Kill(); cmd.Wait() }) }
	if len(stopOut) > 0 {
		*stopOut[0] = stop
	}
	t.Cleanup(func() {
		stop()
		log.Close()
		if t.Failed() {
			b, _ := os.ReadFile(log.Name())
			t.Log(string(b))
		}
	})
	base := "http://" + address
	client := &http.Client{Timeout: time.Second}
	waitReady := func() {
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
				t.Fatal("actual Core source readiness failed")
			}
			time.Sleep(25 * time.Millisecond)
		}
	}
	if len(stopOut) > 1 {
		*stopOut[1] = func() {
			stop()
			next := exec.Command(binary, "-test.run=^TestCredentialQuotaProcessFixture$", "-test.timeout=120s")
			next.Dir, next.Env, next.Stdout, next.Stderr = cmd.Dir, cmd.Env, log, log
			cmd = next
			stopOnce = sync.Once{}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			waitReady()
		}
	}
	waitReady()
	return base
}

func TestCredentialPolicyKnownSubjectCanStopOffline(t *testing.T) {
	var stop func()
	base := policyOldCore(t, &stop)
	u, _ := url.Parse(base)
	proxy := httputil.NewSingleHostReverseProxy(u)
	var requests atomic.Int64
	edge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); proxy.ServeHTTP(w, r) }))
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
	var src cq.Source
	for _, candidate := range sources.Sources {
		if candidate.AuthIndex != "" {
			src = candidate
			break
		}
	}
	if src.AuthIndex == "" {
		t.Fatal("no actual source")
	}
	rr = testutil.Request(t, h, "PUT", "/usage-service/credential-policy", policyBody(t, src, 0, false), testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	var saved map[string]any
	testutil.DecodeJSON(t, rr, &saved)
	stop()
	if r, err := http.Get(base + "/v8/management/resources"); err == nil {
		r.Body.Close()
		t.Fatal("actual Core did not stop")
	}
	before := requests.Load()
	rr = testutil.Request(t, h, "PUT", "/usage-service/credential-policy", policyBody(t, src, 1, false), testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	var stopped map[string]any
	testutil.DecodeJSON(t, rr, &stopped)
	if stopped["enabled"] != false || stopped["generation"] != float64(2) || stopped["applied"] != false || stopped["bindingCurrent"] != false {
		t.Fatalf("local OFF misreported: %s", rr.Body.String())
	}
	if requests.Load() != before {
		t.Fatal("emergency local OFF contacted offline Core")
	}
	rr = testutil.Request(t, h, "PUT", "/usage-service/credential-policy", policyBody(t, src, 1, false), testutil.AdminKey)
	testutil.RequireStatus(t, rr, 409)
	var foreign map[string]any
	json.Unmarshal([]byte(policyBody(t, src, 2, false)), &foreign)
	foreign["owner"] = "cpa-manager.credential-policy.v1/00000000-0000-4000-8000-000000000099"
	body, _ := json.Marshal(foreign)
	rr = testutil.Request(t, h, "PUT", "/usage-service/credential-policy", string(body), testutil.AdminKey)
	if rr.Code != 400 && rr.Code != 409 {
		t.Fatalf("foreign owner accepted: %s", rr.Body.String())
	}
	subject, ok := stopped["subjectId"].(string)
	if !ok || subject == "" {
		t.Fatal("no persistent subject handle")
	}
	rr = testutil.Request(t, h, "GET", "/usage-service/credential-policy?subjectId="+subject, "", testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	var readback map[string]any
	testutil.DecodeJSON(t, rr, &readback)
	if readback["generation"] != float64(2) || readback["enabled"] != false || readback["owner"] != saved["owner"] {
		t.Fatal("local OFF not durably readable")
	}
	if requests.Load() != before {
		t.Fatal("local read/CAS/owner rejection contacted offline Core")
	}
}

func TestCredentialPolicyActualCoreDefaultOff(t *testing.T) {
	base := policyOldCore(t)
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
	count := 0
	for _, src := range sources.Sources {
		if src.AuthIndex == "" {
			continue
		}
		count++
		rr = testutil.Request(t, h, "GET", "/usage-service/credential-policy?authIndex="+src.AuthIndex, "", testutil.AdminKey)
		testutil.RequireStatus(t, rr, 200)
		var status struct {
			Enabled    bool   `json:"enabled"`
			Generation int64  `json:"generation"`
			State      string `json:"state"`
			Reason     string `json:"reason"`
			Applied    bool   `json:"applied"`
		}
		testutil.DecodeJSON(t, rr, &status)
		if status.Enabled || status.Generation != 0 || status.State != "off" || status.Applied || status.Reason != "gate_contract_unavailable" {
			t.Fatalf("new credential is not honestly OFF: %s", rr.Body.String())
		}
		if strings.Contains(rr.Body.String(), "CANARY") {
			t.Fatal("secret leaked")
		}
	}
	if count < 2 {
		t.Fatal("actual fixture did not include multiple credential bindings")
	}
}

func TestCredentialPolicyActualCoreOffPersistsWithCAS(t *testing.T) {
	base := policyOldCore(t)
	cfg := testutil.NewConfig(t)
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	testutil.EnsureAdminCredential(t, db)
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
	for _, src := range sources.Sources {
		if src.AuthIndex != "" {
			source = src
			break
		}
	}
	if source.AuthIndex == "" {
		t.Fatal("no actual source")
	}
	body := policyBody(t, source, 0, false)
	rr = testutil.Request(t, h, "PUT", "/usage-service/credential-policy", body, testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	var status struct {
		Enabled    bool  `json:"enabled"`
		Generation int64 `json:"generation"`
	}
	testutil.DecodeJSON(t, rr, &status)
	if status.Enabled || status.Generation != 1 {
		t.Fatalf("OFF not saved: %s", rr.Body.String())
	}
	rr = testutil.Request(t, h, "PUT", "/usage-service/credential-policy", body, testutil.AdminKey)
	testutil.RequireStatus(t, rr, 409)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	h = New(cfg, db, collector.NewManager(cfg, db)).Handler()
	rr = testutil.Request(t, h, "GET", "/usage-service/credential-policy?authIndex="+source.AuthIndex, "", testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	testutil.DecodeJSON(t, rr, &status)
	if status.Enabled || status.Generation != 1 {
		t.Fatalf("OFF persistence lost: %s", rr.Body.String())
	}
}

func TestCredentialPolicyActualCoreGateAuthenticationError(t *testing.T) {
	base := policyOldCore(t)
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	edge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v8/management/inference-gates/capabilities" {
			w.WriteHeader(401)
			w.Write([]byte("POLICY-SECRET-CANARY"))
			return
		}
		proxy.ServeHTTP(w, r)
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
	for _, src := range sources.Sources {
		if src.AuthIndex == "" {
			continue
		}
		rr = testutil.Request(t, h, "GET", "/usage-service/credential-policy?authIndex="+src.AuthIndex, "", testutil.AdminKey)
		testutil.RequireStatus(t, rr, 200)
		if !strings.Contains(rr.Body.String(), `"reason":"gate_auth_error"`) || strings.Contains(rr.Body.String(), "CANARY") {
			t.Fatalf("gate auth error hidden/leaked: %s", rr.Body.String())
		}
		rr = testutil.Request(t, h, "PUT", "/usage-service/credential-policy", policyBody(t, src, 0, true), testutil.AdminKey)
		testutil.RequireStatus(t, rr, 503)
		if !strings.Contains(rr.Body.String(), "gate_auth_error") {
			t.Fatalf("ON did not explain auth failure: %s", rr.Body.String())
		}
		rr = testutil.Request(t, h, "GET", "/usage-service/credential-policy?authIndex="+src.AuthIndex, "", testutil.AdminKey)
		testutil.RequireStatus(t, rr, 200)
		if !strings.Contains(rr.Body.String(), `"enabled":false`) || !strings.Contains(rr.Body.String(), `"generation":0`) {
			t.Fatal("failed ON changed durable intent")
		}
		break
	}
}

func TestCredentialPolicyActualCoreEnvironmentLock(t *testing.T) {
	base := policyOldCore(t)
	cfg := testutil.NewConfig(t)
	cfg.QuotaCooldownEnvSet = true
	cfg.QuotaCooldownEnabled = false
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
	for _, src := range sources.Sources {
		if src.AuthIndex != "" {
			rr = testutil.Request(t, h, "PUT", "/usage-service/credential-policy", policyBody(t, src, 0, true), testutil.AdminKey)
			testutil.RequireStatus(t, rr, 503)
			if !strings.Contains(rr.Body.String(), "environment_locked_off") {
				t.Fatalf("environment lock not explained: %s", rr.Body.String())
			}
			rr = testutil.Request(t, h, "PUT", "/usage-service/credential-policy", policyBody(t, src, 0, false), testutil.AdminKey)
			testutil.RequireStatus(t, rr, 200)
			return
		}
	}
	t.Fatal("no actual source")
}

func TestCredentialPolicyActualCoreConcurrentManagersAndSourceFences(t *testing.T) {
	base := policyOldCore(t)
	cfg := testutil.NewConfig(t)
	db := testutil.NewStore(t, cfg)
	if err := db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: base, ManagementKey: "test-password"}}); err != nil {
		t.Fatal(err)
	}
	second, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	handlers := []http.Handler{New(cfg, db, collector.NewManager(cfg, db)).Handler(), New(cfg, second, collector.NewManager(cfg, second)).Handler()}
	rr := testutil.Request(t, handlers[0], "GET", "/usage-service/credential-quota", "", testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	var sources struct {
		Sources []cq.Source `json:"sources"`
	}
	testutil.DecodeJSON(t, rr, &sources)
	var src cq.Source
	for _, candidate := range sources.Sources {
		if candidate.AuthIndex != "" {
			src = candidate
			break
		}
	}
	if src.AuthIndex == "" {
		t.Fatal("no actual source")
	}
	body := policyBody(t, src, 0, false)
	results := make(chan int, 8)
	barrier := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-barrier
			request := httptest.NewRequest("PUT", "/usage-service/credential-policy", strings.NewReader(body))
			request.Header.Set("Authorization", "Bearer "+testutil.AdminKey)
			result := httptest.NewRecorder()
			handlers[i%2].ServeHTTP(result, request)
			results <- result.Code
		}(i)
	}
	close(barrier)
	wg.Wait()
	close(results)
	winners := 0
	for code := range results {
		switch code {
		case 200:
			winners++
		case 409:
		default:
			t.Fatalf("concurrent policy CAS returned %d", code)
		}
	}
	if winners != 1 {
		t.Fatalf("CAS winners=%d, want exactly one", winners)
	}
	// A current runtime selector alone cannot bind a write. No subset target or
	// source/instance/version replacement may reuse the current policy CAS.
	for _, alter := range []func(*cq.Source){
		func(s *cq.Source) { s.Scope.InstanceIncarnation = "00000000-0000-4000-8000-000000000099" },
		func(s *cq.Source) { s.Scope.BootEpoch = "00000000-0000-4000-8000-000000000099" },
		func(s *cq.Source) { s.SourceVersion = strings.Repeat("0", 64) },
		func(s *cq.Source) { s.Targets[0].CredentialVersion++ },
		func(s *cq.Source) { s.Targets[0].IdentityVersion++ },
		func(s *cq.Source) { s.Targets = nil },
	} {
		changed := src
		changed.Targets = append(changed.Targets[:0:0], src.Targets...)
		alter(&changed)
		rr = testutil.Request(t, handlers[0], "PUT", "/usage-service/credential-policy", policyBody(t, changed, 1, false), testutil.AdminKey)
		testutil.RequireStatus(t, rr, 409)
	}
	rr = testutil.Request(t, handlers[1], "GET", "/usage-service/credential-policy?authIndex="+src.AuthIndex, "", testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	var state struct {
		Generation int64 `json:"generation"`
		Enabled    bool  `json:"enabled"`
	}
	testutil.DecodeJSON(t, rr, &state)
	if state.Generation != 1 || state.Enabled {
		t.Fatal("rejected source changed intent")
	}
	// Independent B includes configured API-key sources even when unsupported.
	for _, other := range sources.Sources {
		if other.AuthIndex != "" && other.AuthIndex != src.AuthIndex {
			rr = testutil.Request(t, handlers[0], "GET", "/usage-service/credential-policy?authIndex="+other.AuthIndex, "", testutil.AdminKey)
			testutil.RequireStatus(t, rr, 200)
			if err := json.Unmarshal(rr.Body.Bytes(), &state); err != nil {
				t.Fatal(err)
			}
			if state.Generation != 0 || state.Enabled {
				t.Fatal("A write bled into B")
			}
		}
	}
}

func TestCredentialPolicyActualCoreIncompleteGateCannotEnable(t *testing.T) {
	base := policyOldCore(t)
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	for _, tc := range []struct{ name, body, reason string }{
		{"unknown-version", `{"version":2}`, "gate_contract_invalid"},
		{"missing-schema", `{}`, "gate_contract_invalid"},
		{"not-ready", `{"version":1,"ready":false,"mode":"standalone"}`, "gate_not_ready"},
		{"home", `{"version":1,"ready":true,"durable":true,"applied":true,"mode":"home"}`, "gate_mode_unsupported"},
		{"no-durable", `{"version":1,"ready":true,"durable":false,"applied":true,"mode":"standalone"}`, "gate_not_ready"},
		{"no-applied", `{"version":1,"ready":true,"durable":true,"applied":false,"mode":"standalone"}`, "gate_source_unsupported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			edge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Aggregate applied=false is not a scoped veto under the refined
				// contract. This case still has NO selected-source application proof.
				if tc.name == "no-applied" && r.URL.Path == "/v8/management/inference-gates/eligibility" && r.Method == "POST" {
					var q map[string]any
					json.NewDecoder(r.Body).Decode(&q)
					q["ready"] = false
					q["supported"] = false
					q["routes"] = []map[string]any{{"route": "all-current-source", "supported": false}}
					json.NewEncoder(w).Encode(q)
					return
				}
				if strings.HasPrefix(r.URL.Path, "/v8/management/inference-gates") {
					if r.Method != "GET" || r.URL.Path != "/v8/management/inference-gates/capabilities" {
						t.Error("incomplete gate caused a write/read fallback")
						w.WriteHeader(500)
						return
					}
					w.Write([]byte(tc.body))
					return
				}
				proxy.ServeHTTP(w, r)
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
			for _, src := range sources.Sources {
				if src.AuthIndex != "" {
					rr = testutil.Request(t, h, "PUT", "/usage-service/credential-policy", policyBody(t, src, 0, true), testutil.AdminKey)
					testutil.RequireStatus(t, rr, 503)
					if !strings.Contains(rr.Body.String(), tc.reason) {
						t.Fatalf("wrong unavailable reason: %s", rr.Body.String())
					}
					return
				}
			}
			t.Fatal("no actual source")
		})
	}
}

func TestCredentialPolicyActualCoreOwnerBelongsToLogicalDatabase(t *testing.T) {
	base := policyOldCore(t)
	owners := []string{}
	for i := 0; i < 2; i++ {
		cfg := testutil.NewConfig(t)
		db, err := store.Open(cfg.DBPath)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		testutil.EnsureAdminCredential(t, db)
		if err := db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: base, ManagementKey: "test-password"}}); err != nil {
			db.Close()
			t.Fatal(err)
		}
		h := New(cfg, db, collector.NewManager(cfg, db)).Handler()
		rr := testutil.Request(t, h, "GET", "/usage-service/credential-quota", "", testutil.AdminKey)
		testutil.RequireStatus(t, rr, 200)
		var sources struct {
			Sources []cq.Source `json:"sources"`
		}
		testutil.DecodeJSON(t, rr, &sources)
		var index string
		for _, src := range sources.Sources {
			if src.AuthIndex != "" {
				index = src.AuthIndex
				break
			}
		}
		if index == "" {
			db.Close()
			t.Fatal("no actual source")
		}
		readOwner := func(h http.Handler) string {
			rr := testutil.Request(t, h, "GET", "/usage-service/credential-policy?authIndex="+index, "", testutil.AdminKey)
			testutil.RequireStatus(t, rr, 200)
			var status struct {
				Owner string `json:"owner"`
			}
			testutil.DecodeJSON(t, rr, &status)
			prefix := "cpa-manager.credential-policy.v1/"
			if !strings.HasPrefix(status.Owner, prefix) {
				t.Fatalf("owner not installation-qualified: %s", status.Owner)
			}
			if _, err := uuid.Parse(strings.TrimPrefix(status.Owner, prefix)); err != nil {
				t.Fatal("owner lacks opaque UUID")
			}
			return status.Owner
		}
		first := readOwner(h)
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		db, err = store.Open(cfg.DBPath)
		if err != nil {
			t.Fatal(err)
		}
		h = New(cfg, db, collector.NewManager(cfg, db)).Handler()
		second := readOwner(h)
		db.Close()
		if first != second {
			t.Fatal("manager restart changed durable owner")
		}
		owners = append(owners, first)
	}
	if owners[0] == owners[1] {
		t.Fatal("independent Manager DBs share owner and could clear one another")
	}
}

func TestCredentialPolicyActualCoreConfiguredKeyExplainsQueryUnavailability(t *testing.T) {
	base := policyOldCore(t)
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
	for _, src := range sources.Sources {
		if src.AuthIndex != "" && src.State != "ready" {
			rr = testutil.Request(t, h, "GET", "/usage-service/credential-policy?authIndex="+src.AuthIndex, "", testutil.AdminKey)
			testutil.RequireStatus(t, rr, 200)
			var status struct {
				Enabled           bool                           `json:"enabled"`
				QueryAvailability struct{ State, Reason string } `json:"queryAvailability"`
			}
			testutil.DecodeJSON(t, rr, &status)
			if status.Enabled || status.QueryAvailability.State != src.State || status.QueryAvailability.Reason != src.Reason {
				t.Fatalf("configured key lost real query reason: %s", rr.Body.String())
			}
			return
		}
	}
	t.Fatal("fixture omitted unsupported configured key")
}

func TestCredentialPolicyActualCoreConnectionChangeDuringGateRead(t *testing.T) {
	base := policyOldCore(t)
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	entered, release := make(chan struct{}), make(chan struct{})
	edge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v8/management/inference-gates/capabilities" {
			close(entered)
			<-release
			w.WriteHeader(404)
			return
		}
		proxy.ServeHTTP(w, r)
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
	var src cq.Source
	for _, candidate := range sources.Sources {
		if candidate.AuthIndex != "" {
			src = candidate
			break
		}
	}
	if src.AuthIndex == "" {
		t.Fatal("no actual source")
	}
	result := make(chan int, 1)
	go func() {
		req := httptest.NewRequest("GET", "/usage-service/credential-policy?authIndex="+src.AuthIndex, nil)
		req.Header.Set("Authorization", "Bearer "+testutil.AdminKey)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		result <- rr.Code
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("gate read did not start")
	}
	if err := db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: base, ManagementKey: "wrong-new-instance-auth"}}); err != nil {
		close(release)
		t.Fatal(err)
	}
	close(release)
	select {
	case code := <-result:
		if code != 409 {
			t.Fatalf("stale source survived connection switch with HTTP %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("policy read hung")
	}
}

func TestCredentialPolicyActualCoreUnsupportedSelectedRouteCannotEnable(t *testing.T) {
	base := policyOldCore(t)
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	var eligibilityReads atomic.Int64
	edge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v8/management/inference-gates/capabilities":
			w.Write([]byte(`{"version":1,"mode":"standalone","ready":false,"durable":true,"applied":false,"subjects":["credential"],"routes":[{"route":"native","supported":true}]}`))
		case "/v8/management/inference-gates/eligibility":
			eligibilityReads.Add(1)
			var q map[string]any
			if json.NewDecoder(r.Body).Decode(&q) != nil {
				t.Error("invalid eligibility request")
			}
			if q["route"] != "all-current-source" {
				t.Error("wrong source route")
			}
			q["ready"] = false
			q["supported"] = false
			q["reason"] = "plugin_route_unsupported"
			q["routes"] = []map[string]any{{"route": "all-current-source", "supported": false, "reason": "plugin_route_unsupported"}}
			json.NewEncoder(w).Encode(q)
		default:
			if strings.HasPrefix(r.URL.Path, "/v8/management/inference-gates") {
				t.Error("unsupported selected source caused write/fallback")
				w.WriteHeader(500)
				return
			}
			proxy.ServeHTTP(w, r)
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
	for _, src := range sources.Sources {
		if src.AuthIndex != "" && src.State == "ready" {
			rr = testutil.Request(t, h, "PUT", "/usage-service/credential-policy", policyBody(t, src, 0, true), testutil.AdminKey)
			testutil.RequireStatus(t, rr, 503)
			if !strings.Contains(rr.Body.String(), "gate_source_unsupported") || eligibilityReads.Load() != 1 {
				t.Fatalf("global readiness substituted for selected route: %s checks=%d", rr.Body.String(), eligibilityReads.Load())
			}
			return
		}
	}
	t.Fatal("no actual ready source")
}

// Negative external gate envelopes only; actual identity still comes from Core.
// No successful operation/ACK or inference route is simulated here.
func TestCredentialPolicyCorruptLedgerCannotEnableDespiteEligibilityEnvelope(t *testing.T) {
	base := policyOldCore(t)
	u, _ := url.Parse(base)
	proxy := httputil.NewSingleHostReverseProxy(u)
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"unavailable", 503, `{"error":"gate_unavailable"}`},
		{"not-published", 200, `{"version":1,"ready":false,"gateRevision":0,"gates":[]}`},
		{"missing-ledger", 200, `{"version":1,"ready":true,"gateRevision":0}`},
		{"wrong-current-scope", 200, `{"version":1,"ready":true,"gateRevision":0,"gates":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			edge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v8/management/inference-gates/capabilities":
					w.Write([]byte(`{"version":1,"mode":"standalone","ready":false,"durable":true,"applied":false}`))
				case "/v8/management/inference-gates/eligibility":
					var q map[string]any
					json.NewDecoder(r.Body).Decode(&q)
					q["ready"] = true
					q["supported"] = true
					q["routes"] = []map[string]any{{"route": "all-current-source", "supported": true}}
					json.NewEncoder(w).Encode(q)
				case "/v8/management/inference-gates":
					w.WriteHeader(tc.status)
					w.Write([]byte(tc.body))
				default:
					if strings.HasPrefix(r.URL.Path, "/v8/management/inference-gates") {
						t.Error("invalid ledger caused an operation/fallback")
						w.WriteHeader(500)
						return
					}
					proxy.ServeHTTP(w, r)
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
			for _, src := range sources.Sources {
				if src.AuthIndex != "" && src.State == "ready" {
					rr = testutil.Request(t, h, "PUT", "/usage-service/credential-policy", policyBody(t, src, 0, true), testutil.AdminKey)
					testutil.RequireStatus(t, rr, 503)
					rr = testutil.Request(t, h, "GET", "/usage-service/credential-policy?authIndex="+src.AuthIndex, "", testutil.AdminKey)
					testutil.RequireStatus(t, rr, 200)
					var status struct {
						Enabled, Applied bool
						Generation       int64
					}
					testutil.DecodeJSON(t, rr, &status)
					if status.Enabled || status.Applied || status.Generation != 0 {
						t.Fatal("invalid/unpublished ledger claimed ON")
					}
					return
				}
			}
			t.Fatal("no actual source")
		})
	}
}
