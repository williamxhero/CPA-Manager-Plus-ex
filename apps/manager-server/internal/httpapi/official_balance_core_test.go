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
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
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

// Opt-in final public chain: actual immutable Core process, actual Manager HTTP,
// temporary SQLite, and production balance transport against controlled TLS.
// No real configuration, credentials, account or service is accessed.
func TestOfficialBalanceActualCoreChain(t *testing.T) {
	testOfficialBalanceActualCoreChain(t, "deepseek")
}
func TestOfficialBalanceDeepInfraActualCoreChain(t *testing.T) {
	testOfficialBalanceActualCoreChain(t, "deepinfra")
}
func testOfficialBalanceActualCoreChain(t *testing.T, adapter string) {
	binary := os.Getenv("CORE_OFFICIAL_QUERY_TEST_BINARY")
	if binary == "" {
		t.Skip("explicitly enable with immutable Core identity binary")
	}
	expectedSHA := strings.ToLower(strings.TrimSpace(os.Getenv("CORE_OFFICIAL_QUERY_EXPECTED_SHA256")))
	expectedRevision := strings.ToLower(strings.TrimSpace(os.Getenv("CORE_OFFICIAL_QUERY_EXPECTED_REVISION")))
	shaBytes, shaErr := hex.DecodeString(expectedSHA)
	revisionBytes, revisionErr := hex.DecodeString(expectedRevision)
	if shaErr != nil || len(shaBytes) != 32 || revisionErr != nil || len(revisionBytes) != 20 {
		t.Fatal("binary was set but independent expected SHA256/source revision provenance is missing or invalid")
	}
	raw, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	sha := hex.EncodeToString(digest[:])
	if sha != expectedSHA {
		t.Fatal("not the approved immutable producer binary")
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
	if revision != expectedRevision || modified != "false" {
		t.Fatal("unverified producer source metadata")
	}
	t.Logf("actual Core sha256=%s source=%s modified=%s", sha, revision, modified)

	var denied, inferenceCalls, upstreamCalls atomic.Int64
	deny := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.IsAbs() || r.Method == "CONNECT" {
			denied.Add(1)
		} else {
			inferenceCalls.Add(1)
		}
		w.WriteHeader(503)
	}))
	defer deny.Close()
	var mode atomic.Int64
	entered, release, responded := make(chan struct{}, 1), make(chan struct{}), make(chan struct{}, 1)
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	firstWallet := `{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"-12345678901234567890.000000001","granted_balance":"0.000000001","topped_up_balance":"-12345678901234567890.000000002"},{"currency":"USD","total_balance":"1.00","granted_balance":"0.00","topped_up_balance":"1.00"}]}`
	freshWallet := strings.Replace(firstWallet, "-12345678901234567890.000000001", "9.123456789012345678901234567890", 1)
	route := "/user/balance"
	if adapter == "deepinfra" {
		route = "/payment/checklist"
		firstWallet = `{"stripe_balance":-1,"recent":2.5}`
		freshWallet = `{"stripe_balance":-12345678901234567890.000000001,"recent":0.0000000001}`
	}
	assertMoney := func(obs *balance.Observation, fresh bool) bool {
		if obs == nil || obs.QueryStatus != "success" {
			return false
		}
		if adapter == "deepinfra" {
			want := "-1.5"
			if fresh {
				want = "12345678901234567890.0000000009"
			}
			return obs.Semantic == "signed_prepaid_net" && obs.AmountDecimal == want && obs.Currency == "USD" && len(obs.Balances) == 0 && obs.Available == nil
		}
		if len(obs.Balances) != 2 {
			return false
		}
		want := "-12345678901234567890.000000001"
		if fresh {
			want = "9.123456789012345678901234567890"
		}
		return obs.Balances[0].Total == want && obs.Balances[1].Currency == "USD"
	}
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		if r.Method != "GET" || r.URL.Path != route || r.Header.Get("Authorization") != "Bearer synthetic-query-canary" {
			t.Error("unexpected billing protocol")
			w.WriteHeader(400)
			return
		}
		switch mode.Load() {
		case 1:
			w.WriteHeader(401)
			fmt.Fprint(w, "synthetic-query-canary")
			return
		case 2:
			entered <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			fmt.Fprint(w, firstWallet)
			responded <- struct{}{}
			return
		case 3:
			fmt.Fprint(w, freshWallet)
			return
		}
		fmt.Fprint(w, firstWallet)
	}))
	defer upstream.Close()

	dir := t.TempDir()
	authDir := filepath.Join(dir, "auth")
	if err := os.Mkdir(authDir, 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	configPath := filepath.Join(dir, "config.yaml")
	configText := fmt.Sprintf("config-version: 8\nserver: {host: 127.0.0.1, port: %d}\nmanagement: {secret-key: synthetic-core-key, disable-control-panel: true, disable-auto-update-panel: true}\noauth: {auth-dir: %q}\nresource-catalog: {enabled: true, state-dir: catalog}\nplugins: {enabled: false}\napi-keys:\n  openai-compatibility:\n    - name: synthetic-deepseek\n      base-url: %s\n      disabled: true\n      keys: [{api-key: CANARY-SYNTHETIC-INFERENCE, disabled: true}]\n", port, filepath.ToSlash(authDir), deny.URL)
	if err := os.WriteFile(configPath, []byte(configText), 0600); err != nil {
		t.Fatal(err)
	}
	coreBase := fmt.Sprintf("http://127.0.0.1:%d", port)
	cfg := testutil.NewConfig(t)
	protector, _ := security.NewProtector([]byte("0123456789abcdef0123456789abcdef"))
	db, err := store.Open(cfg.DBPath, protector)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	testutil.EnsureAdminCredential(t, db)
	if err := db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: coreBase, ManagementKey: "synthetic-core-key"}}); err != nil {
		t.Fatal(err)
	}
	manager := New(cfg, db, collector.NewManager(cfg, db))
	roots := x509.NewCertPool()
	roots.AddCert(upstream.Certificate())
	manager.AppContext().OfficialBalanceService.Querier = balance.NewWithNetwork(balance.NetworkOptions{RootCAs: roots})
	server := httptest.NewServer(manager.Handler())
	defer server.Close()
	client := &http.Client{Timeout: 15 * time.Second}
	api := func(method, path string, body any) (int, []byte) {
		t.Helper()
		var data []byte
		if body != nil {
			data, _ = json.Marshal(body)
		}
		req, err := http.NewRequest(method, server.URL+path, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+testutil.AdminKey)
		req.Header.Set("Content-Type", "application/json")
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		out, err := io.ReadAll(io.LimitReader(res.Body, (2<<20)+1))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(out, []byte("synthetic-query-canary")) || bytes.Contains(out, []byte("CANARY-SYNTHETIC")) || bytes.Contains(out, []byte("synthetic-core-key")) {
			t.Fatal("management response leaked a secret")
		}
		if strings.HasPrefix(path, balancePath) {
			var envelope map[string]any
			if json.Unmarshal(out, &envelope) == nil {
				var inspect func(map[string]any)
				inspect = func(value map[string]any) {
					for k, v := range value {
						if k == "protectionAck" || k == "gateAck" || k == "policyAck" || k == "applied" && v == true {
							t.Fatal("billing read returned a protection application ACK")
						}
						if child, ok := v.(map[string]any); ok {
							inspect(child)
						}
					}
				}
				inspect(envelope)
			}
		}
		return res.StatusCode, out
	}
	negotiate := func() *rc.Snapshot {
		code, data := api("GET", negotiatedPath, nil)
		if code != 200 {
			t.Fatalf("negotiation HTTP %d", code)
		}
		var response struct{ Negotiated rc.Negotiated }
		if json.Unmarshal(data, &response) != nil {
			t.Fatal("invalid negotiation")
		}
		n := response.Negotiated

		if n.Identity.State != "available" {
			return nil
		}
		return n.Snapshot
	}
	start := func() func() {
		cmd := exec.Command(binary, "-config", configPath, "-local-model")
		cmd.Dir = dir
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
		for _, key := range []string{"PATH", "SystemRoot", "WINDIR", "TEMP", "TMP", "COMSPEC"} {
			if value := os.Getenv(key); value != "" {
				cmd.Env = append(cmd.Env, key+"="+value)
			}
		}
		cmd.Env = append(cmd.Env, "HOME="+dir, "USERPROFILE="+dir, "APPDATA="+dir, "LOCALAPPDATA="+dir, "HTTP_PROXY="+deny.URL, "HTTPS_PROXY="+deny.URL, "NO_PROXY=127.0.0.1,localhost")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		stopped := false
		stop := func() {
			if !stopped {
				stopped = true
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}
		}
		t.Cleanup(stop)
		until := time.Now().Add(20 * time.Second)
		for time.Now().Before(until) {
			if snapshot := negotiate(); snapshot != nil && snapshot.Complete {
				return stop
			}
			time.Sleep(25 * time.Millisecond)
		}
		t.Fatal("actual Core did not publish a complete valid identity inventory")
		return stop
	}
	bindingFrom := func(snapshot *rc.Snapshot) ob.Binding {
		if snapshot == nil || len(snapshot.Resources) != 2 {
			t.Fatal("actual native provider/member inventory missing")
		}
		var provider rc.Row
		for _, row := range snapshot.Resources {
			if row.Kind == "provider" && row.Family == "openai-compatibility" {
				provider = row
			}
		}
		if provider.ResourceID == "" || !provider.Manual.Disabled || len(provider.Members) != 1 {
			t.Fatal("disabled native provider identity missing")
		}
		targets := []rc.Target{provider.Target}
		for _, id := range provider.Members {
			found := false
			for _, row := range snapshot.Resources {
				if row.ResourceID == id && row.ParentID == provider.ResourceID {
					targets = append(targets, row.Target)
					found = true
				}
			}
			if !found {
				t.Fatal("native member marker is not issued in source inventory")
			}
		}
		return ob.Binding{Scope: snapshot.Scope, Targets: targets}
	}
	// Availability may legitimately become true in a later Core. A read must
	// leave capabilities/source/manual state unchanged, not force them false.
	type protectionState struct {
		Caps struct {
			Version                         int
			Inventory, Durable, Query, Gate bool
			PolicyLocked                    bool `json:"policy_locked"`
			Reason                          string
		}
		Snapshot rc.Snapshot
	}
	protection := func() protectionState {
		var state protectionState
		req, err := http.NewRequest("GET", coreBase+"/v8/management/resource-capabilities", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer synthetic-core-key")
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		err = json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(&state.Caps)
		res.Body.Close()
		if err != nil || res.StatusCode != 200 {
			t.Fatal("actual Core capabilities read failed")
		}
		snapshot := negotiate()
		if snapshot == nil {
			t.Fatal("actual Core identity unavailable during protection comparison")
		}
		state.Snapshot = *snapshot
		return state
	}
	stop := start()
	protectionBefore := protection()
	before := bindingFrom(&protectionBefore.Snapshot)
	read := func(binding ob.Binding) ob.Status {
		t.Helper()
		data, _ := json.Marshal(binding)
		code, body := api("GET", balancePath+"?binding="+url.QueryEscape(string(data)), nil)
		if code != 200 {
			t.Fatalf("status HTTP %d: %s", code, body)
		}
		var out ob.Status
		if json.Unmarshal(body, &out) != nil {
			t.Fatal("invalid balance status")
		}
		return out
	}
	query := func(binding ob.Binding) ob.Job {
		t.Helper()
		code, data := api("POST", balancePath, ob.Query{Binding: binding, Version: 1})
		if code != 202 {
			t.Fatalf("query HTTP %d: %s", code, data)
		}
		var job ob.Job
		if json.Unmarshal(data, &job) != nil {
			t.Fatal("invalid job")
		}
		return job
	}
	wait := func(id string) ob.Job {
		t.Helper()
		until := time.Now().Add(16 * time.Second)
		for time.Now().Before(until) {
			code, data := api("GET", balancePath+"?job="+id, nil)
			if code != 200 {
				t.Fatalf("job HTTP %d: %s", code, data)
			}
			var job ob.Job
			json.Unmarshal(data, &job)
			if job.State != "running" {
				return job
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("job exceeded hard test wait")
		return ob.Job{}
	}
	update := ob.Update{Config: ob.Config{Binding: before, Adapter: adapter, Endpoint: upstream.URL + route, PrivateTargets: []string{"127.0.0.1"}}, Secret: "synthetic-query-canary"}
	code, data := api("PUT", balancePath, update)
	if code != 200 {
		t.Fatalf("configure HTTP %d: %s", code, data)
	}
	success := wait(query(before).ID)
	if success.Status == nil || !assertMoney(success.Status.LastSuccess, false) {
		t.Fatal("real public chain lost exact wallet", success)
	}
	count := upstreamCalls.Load()
	read(before)
	read(before)
	if upstreamCalls.Load() != count {
		t.Fatal("status GET queried billing platform")
	}
	mode.Store(1)
	failure := wait(query(before).ID)
	if failure.Status == nil || failure.Status.State != "auth_error" || !failure.Status.Stale || !assertMoney(failure.Status.LastSuccess, false) {
		t.Fatal("actual-chain auth failure lost last success", failure)
	}
	if afterFailure := protection(); !reflect.DeepEqual(protectionBefore, afterFailure) {
		t.Fatal("billing configure/query/read changed Core capabilities or source/manual state")
	}
	mode.Store(2)
	old := query(before)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("old in-flight TLS query did not arrive")
	}
	stop()
	stop = start()
	protectionAfterRestart := protection()
	after := bindingFrom(&protectionAfterRestart.Snapshot)
	if before.Scope.InstanceID != after.Scope.InstanceID || before.Scope.InstanceIncarnation != after.Scope.InstanceIncarnation || before.Scope.BootEpoch == after.Scope.BootEpoch || !reflect.DeepEqual(before.Targets, after.Targets) {
		t.Fatal("actual restart changed durable credential authority instead of only operation context")
	}
	rebound := read(after)
	if !rebound.Configured || rebound.Version != 1 || !rebound.Stale {
		t.Fatal("verified actual restart lost encrypted source configuration or relabeled old evidence fresh")
	}
	mode.Store(3)
	fresh := query(after)
	if fresh.ID == old.ID {
		t.Fatal("new boot joined old operation")
	}
	newResult := wait(fresh.ID)
	if newResult.Status == nil || !assertMoney(newResult.Status.LastSuccess, true) {
		t.Fatal("fresh-boot query did not reuse preserved explicit billing secret")
	}
	close(release)
	select {
	case <-responded:
	case <-time.After(5 * time.Second):
		t.Fatal("old TLS response did not complete")
	}
	// Observe beyond the entire old operation budget; no internal worker mocks or
	// private map reads are needed to claim that the late result cannot overwrite.
	if remaining := time.Until(time.UnixMilli(old.DeadlineMS)); remaining > 0 {
		time.Sleep(remaining + 20*time.Millisecond)
	}
	code, _ = api("GET", balancePath+"?job="+old.ID, nil)
	if code != 409 {
		t.Fatal("preboot job escaped full scope fence")
	}
	final := read(after)
	if !assertMoney(final.LastSuccess, true) || !final.DirectQuery {
		t.Fatal("late preboot result overwrote current target")
	}
	if afterQueries := protection(); !reflect.DeepEqual(protectionAfterRestart, afterQueries) {
		t.Fatal("billing refresh/rebind changed Core capabilities or source/manual state")
	}
	stop()
	t.Logf("ACTUAL_CHAIN_PASS: adapter=%s full native provider/member targets; exact monetary observation; auth failure retains success; GET billing calls=0; restart rebind secret resubmissions=0; old in-flight result fenced; Core capabilities/source/manual state unchanged; no protection ACK; denied startup=%d inference calls=%d billing calls=%d", adapter, denied.Load(), inferenceCalls.Load(), upstreamCalls.Load())
	if inferenceCalls.Load() != 0 {
		t.Fatal("fixture performed inference")
	}
}
