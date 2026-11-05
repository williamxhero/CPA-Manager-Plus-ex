package httpapi

import (
	"crypto/sha256"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

func TestProviderRuntimeMainFairReconciliationSkipsSlowSubject(t *testing.T) {
	if os.Getenv("PROVIDER82_MANAGER_BINARY") == "" {
		t.Skip("explicit built Manager artifact required")
	}
	f := newProviderFundingFixture(t)
	f.manager.AppContext().OfficialBalanceService.Querier = balance.NewWithNetwork(providerFundingNetwork(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"is_available":true,"balance_infos":[{"currency":"USD","total_balance":"0.0500","granted_balance":"0","topped_up_balance":"0.0500"}]}`)
	}))
	a := f.providerID
	for _, key := range []string{"FUNDING-SYNTHETIC-A1", "FUNDING-SYNTHETIC-A2"} {
		f.configureAndRefresh(key, key, 0)
	}
	f.enableMoney(true)
	f.awaitMoneyEvaluation(f.refreshMoney("FUNDING-SYNTHETIC-A1", 1))
	f.loseAck.Store(true)
	f.hideReceipt.Store(true)
	f.awaitMoneyEvaluation(f.refreshMoney("FUNDING-SYNTHETIC-A2", 1))
	inv := f.api("GET", "/usage-service/resource-capabilities?inventory=1&bindings=1", nil, 200)
	var envelope struct{ Negotiated rc.Negotiated }
	raw, _ := json.Marshal(inv)
	if json.Unmarshal(raw, &envelope) != nil || envelope.Negotiated.Snapshot == nil {
		t.Fatal("issued B inventory missing")
	}
	for _, row := range envelope.Negotiated.Snapshot.Resources {
		if row.Kind == "provider" && row.ResourceID != a {
			f.providerID = row.ResourceID
		}
	}
	if f.providerID == a {
		t.Fatal("B provider missing")
	}
	for _, row := range envelope.Negotiated.Snapshot.Resources {
		if row.Kind == "credential" && row.ParentID == f.providerID {
			f.members["FUNDING-SYNTHETIC-B1"] = ob.Binding{Scope: envelope.Negotiated.Snapshot.Scope, Targets: []rc.Target{row.Target}}
		}
	}
	f.configureAndRefresh("FUNDING-SYNTHETIC-B1", "FUNDING-SYNTHETIC-B1", 0)
	f.enableMoney(true)
	f.loseAck.Store(true)
	f.awaitMoneyEvaluation(f.refreshMoney("FUNDING-SYNTHETIC-B1", 1))
	if f.operations.Load() != 2 {
		t.Fatal("two pending provider commands were not established")
	}
	f.shutdown()
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	f.slowProvider.Store(&a)
	f.hideReceipt.Store(false)
	if ordinary := os.Getenv("PROVIDER82_ORDINARY_BINARY"); ordinary != "" {
		t.Setenv("PROVIDER82_MANAGER_BINARY", ordinary)
	}
	stop := f.startRuntimeMain("fair-reconciliation", "", "", "")
	defer stop()
	for deadline := time.Now().Add(38 * time.Second); ; {
		state := f.api("GET", f.policyPath(), nil, 200)
		gate, _ := state["coordination"].(map[string]any)
		if gate["receiptPersisted"] == true && f.slowReads.Load() > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("slow first provider starved healthy provider receipt persistence")
		}
		time.Sleep(300 * time.Millisecond)
	}
	if f.operations.Load() != 2 {
		t.Fatal("fair read-only reconciliation replayed a command")
	}
	// Wait for a NEW slow request after B's durable receipt. This proves shutdown
	// actually cancels blocked I/O rather than merely stopping between attempts.
	reads, canceled := f.slowReads.Load(), f.slowCanceled.Load()
	for deadline := time.Now().Add(25 * time.Second); ; {
		if f.slowReads.Load() > reads && f.slowActive.Load() > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("new blocked coordinator read not observed before shutdown")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Ordinary main must cancel/join before closing temporary SQLite. The proxy
	// independently observes the client context cancellation and handler exit.
	stop()
	for deadline := time.Now().Add(time.Second); ; {
		if f.slowCanceled.Load() > canceled && f.slowActive.Load() == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("shutdown left blocked external coordinator I/O active")
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Logf("blocked coordinator cancellation: reads=%d canceled=%d active=%d", f.slowReads.Load(), f.slowCanceled.Load(), f.slowActive.Load())
}

// This opt-in test executes built main artifacts, not a test server/helper main.
// API credentials/configuration use synthetic values in an owned temp SQLite.
func TestProviderRuntimeMainReconcilesLostACKAndRestoresDisplay(t *testing.T) {
	if os.Getenv("PROVIDER82_MANAGER_BINARY") == "" {
		t.Skip("explicit built Manager artifact required")
	}
	f := newProviderFundingFixture(t)
	var failed atomic.Bool
	_, der, address := providerFundingTLS(t, func(w http.ResponseWriter, r *http.Request) {
		if failed.Load() {
			w.WriteHeader(500)
			return
		}
		fmt.Fprint(w, `{"is_available":true,"balance_infos":[{"currency":"USD","total_balance":"0.0500","granted_balance":"0","topped_up_balance":"0.0500"}]}`)
	})
	ca := filepath.Join(f.config.DataDir, "fixture-ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	clock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/fixture/clock" {
			w.WriteHeader(404)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"NowMS": time.Now().UnixMilli(), "Revision": 1})
	}))
	defer clock.Close()
	f.shutdown()
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	stop := f.startRuntimeMain("first", address, ca, clock.URL)
	defer func() { stop() }()
	for _, key := range []string{"FUNDING-SYNTHETIC-A1", "FUNDING-SYNTHETIC-A2"} {
		f.configureAndRefresh(key, key, 0)
	}
	f.enableMoney(true)
	f.awaitMoneyEvaluation(f.refreshMoney("FUNDING-SYNTHETIC-A1", 1))
	f.loseAck.Store(true)
	f.hideReceipt.Store(true)
	f.awaitMoneyEvaluation(f.refreshMoney("FUNDING-SYNTHETIC-A2", 1))
	// Core applied the request, but no receipt was delivered to the Manager.
	if len(f.ledger()["gates"].([]any)) != 1 || f.operations.Load() != 1 {
		t.Fatal("main did not automatically apply exactly one monetary pause")
	}
	pending := f.api("GET", f.policyPath(), nil, 200)
	if pending["applied"] == true || pending["effective"] == true {
		t.Fatal("hidden ACK became optimistic success")
	}
	stop()
	stop = f.startRuntimeMain("restart", address, ca, clock.URL)
	// A running coordinator must not replay an unknown outcome while the
	// receipt endpoint remains unavailable, including after process restart.
	time.Sleep(6 * time.Second)
	pending = f.api("GET", f.policyPath(), nil, 200)
	if pending["applied"] == true || f.operations.Load() != 1 {
		t.Fatal("unknown receipt became applied or was replayed after restart")
	}
	f.proofFault.Store(1) // external Core returns incomplete routing coverage
	f.hideReceipt.Store(false)
	time.Sleep(6 * time.Second)
	pending = f.api("GET", f.policyPath(), nil, 200)
	if pending["coordination"] != nil || pending["effective"] == true || f.operations.Load() != 1 {
		t.Fatal("receipt reconciliation bypassed current routing proof")
	}
	f.proofFault.Store(0)
	var state map[string]any
	for deadline := time.Now().Add(25 * time.Second); ; {
		state = f.api("GET", f.policyPath(), nil, 200)
		gate, ok := state["coordination"].(map[string]any)
		if ok && gate["receiptPersisted"] == true && state["applied"] == true {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("runtime never persisted recovered receipt: %+v", state)
		}
		time.Sleep(250 * time.Millisecond)
	}
	if state["ownPaused"] != true || state["enabled"] != true || f.operations.Load() != 1 {
		t.Fatal("restart changed intent/gate or replayed operation")
	}
	sources, ok := state["sources"].([]any)
	if !ok || len(sources) != 2 {
		t.Fatal("public durable per-member display is missing")
	}
	for _, raw := range sources {
		source := raw.(map[string]any)
		last := source["lastSuccess"].(map[string]any)
		if last["balances"].([]any)[0].(map[string]any)["total"] != "0.0500" {
			t.Fatal("display lost native precision")
		}
	}
	failed.Store(true)
	f.awaitMoneyEvaluation(f.refreshMoney("FUNDING-SYNTHETIC-A1", 1))
	state = f.api("GET", f.policyPath(), nil, 200)
	if state["ownPaused"] != true || f.operations.Load() != 1 {
		t.Fatal("failed refresh cleared pause")
	}
	anyStale := false
	for _, raw := range state["sources"].([]any) {
		s := raw.(map[string]any)
		anyStale = anyStale || s["stale"] == true
	}
	if !anyStale {
		t.Fatal("failed source not displayed stale")
	}
	f.enableMoney(false)
	stop()
	stop = f.startRuntimeMain("off-restart", address, ca, clock.URL)
	state = f.api("GET", f.policyPath(), nil, 200)
	if state["state"] != "historical_pause" || state["enabled"] != false || f.operations.Load() != 1 {
		t.Fatal("OFF restart hid/cleared durable historical pause")
	}
	path := filepath.Join(os.Getenv("PROVIDER82_PROCESS_LOG_DIR"), "runtime-status.json")
	b, _ := json.MarshalIndent(state, "", "  ")
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	if ordinary := os.Getenv("PROVIDER82_ORDINARY_BINARY"); ordinary != "" {
		stop()
		t.Setenv("PROVIDER82_MANAGER_BINARY", ordinary)
		stop = f.startRuntimeMain("ordinary-restart", address, ca, clock.URL)
		state = f.api("GET", f.policyPath(), nil, 200)
		if state["state"] != "historical_pause" || state["enabled"] != false || f.operations.Load() != 1 {
			t.Fatal("ordinary main failed to read durable historical state")
		}
	}
	if fixture := os.Getenv("PROVIDER82_LIVE_UI"); fixture != "" {
		doc, _ := json.Marshal(map[string]any{"base": f.apiBase, "managementKey": testutil.AdminKey, "binding": map[string]any{"scope": state["scope"], "targets": state["targets"]}, "providerId": f.providerID})
		if err := os.WriteFile(fixture, doc, 0600); err != nil {
			t.Fatal(err)
		}
		for deadline := time.Now().Add(45 * time.Second); ; {
			if _, err := os.Stat(fixture + ".stop"); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("local UI boundary timed out")
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
}

func (f *providerFundingFixture) startRuntimeMain(label, address, ca, clock string) func() {
	f.t.Helper()
	binary := os.Getenv("PROVIDER82_MANAGER_BINARY")
	artifact, err := os.ReadFile(binary)
	if err != nil {
		f.t.Fatal(err)
	}
	artifactSHA := fmt.Sprintf("%x", sha256.Sum256(artifact))
	logs := filepath.Join(os.Getenv("PROVIDER82_PROCESS_LOG_DIR"), "manager-"+label)
	if err := os.MkdirAll(logs, 0700); err != nil {
		f.t.Fatal(err)
	}
	cfg := map[string]any{"httpAddr": "127.0.0.1:0", "dataDir": f.config.DataDir, "dbPath": f.config.DBPath,
		"dataKeyPath": filepath.Join(f.config.DataDir, "data.key"), "adminKeyFile": filepath.Join(f.config.DataDir, "absent"), "managementKeyFile": filepath.Join(f.config.DataDir, "absent"), "dataKeyFile": filepath.Join(f.config.DataDir, "absent")}
	raw, _ := json.Marshal(cfg)
	configPath := filepath.Join(logs, "config.json")
	if err := os.WriteFile(configPath, raw, 0600); err != nil {
		f.t.Fatal(err)
	}
	output, err := os.Create(filepath.Join(logs, "main.log"))
	if err != nil {
		f.t.Fatal(err)
	}
	cmd := exec.Command(binary)
	cmd.Dir = f.config.DataDir
	cmd.Stdout, cmd.Stderr = output, output
	for _, key := range []string{"PATH", "SystemRoot", "WINDIR", "TEMP", "TMP", "COMSPEC"} {
		if v := os.Getenv(key); v != "" {
			cmd.Env = append(cmd.Env, key+"="+v)
		}
	}
	cmd.Env = append(cmd.Env, "CPA_MANAGER_CONFIG="+configPath, "CPA_MANAGER_ADMIN_KEY="+testutil.AdminKey, "CPA_MANAGER_DATA_KEY=0123456789abcdef0123456789abcdef",
		"CPAMP_UPDATE_CHECK_ENABLED=false", "USAGE_ACCOUNT_ACTIONS_ENABLED=false", "USAGE_ACCOUNT_ACTIONS_AUTO_DISABLE=false",
		"CPAMP_ENTRY_FIXTURE_URL="+clock, "CPAMP_ENTRY_FIXTURE_TOKEN="+testutil.AdminKey, "CPAMP_PROVIDER_FIXTURE_ADDRESS="+address, "CPAMP_PROVIDER_FIXTURE_CA="+ca,
		"HTTP_PROXY=http://127.0.0.1:1", "HTTPS_PROXY=http://127.0.0.1:1", "NO_PROXY=127.0.0.1,localhost")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x210}
	if err := cmd.Start(); err != nil {
		_ = output.Close()
		f.t.Fatal(err)
	}
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		kernel := syscall.NewLazyDLL("kernel32.dll")
		kernel.NewProc("FreeConsole").Call()
		attached, _, _ := kernel.NewProc("AttachConsole").Call(uintptr(cmd.Process.Pid))
		if attached != 0 {
			kernel.NewProc("GenerateConsoleCtrlEvent").Call(1, uintptr(cmd.Process.Pid))
			kernel.NewProc("FreeConsole").Call()
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		var waitErr error
		forced := false
		select {
		case waitErr = <-done:
		case <-time.After(15 * time.Second):
			forced = true
			_ = cmd.Process.Kill()
			waitErr = <-done
		}
		_ = output.Close()
		code := 0
		if e, ok := waitErr.(*exec.ExitError); ok {
			code = e.ExitCode()
		} else if waitErr != nil {
			code = -1
		}
		doc, _ := json.MarshalIndent(map[string]any{"pid": cmd.Process.Pid, "command": cmd.Args, "fullMain": true, "exitCode": code, "forcedCleanup": forced, "requestedStop": "CTRL_BREAK_owned_process_group", "artifact": binary, "artifactSHA256": artifactSHA, "sourceCommit": os.Getenv("PROVIDER82_MANAGER_COMMIT")}, "", "  ")
		_ = os.WriteFile(filepath.Join(logs, "PROCESS.json"), doc, 0600)
		if forced || code != 0 {
			f.t.Errorf("main shutdown forced=%v exit=%d", forced, code)
		}
	}
	f.t.Cleanup(stop)
	pattern := regexp.MustCompile(`cpa-manager-plus listening on (127\.0\.0\.1:\d+)`)
	for deadline := time.Now().Add(20 * time.Second); ; {
		b, _ := os.ReadFile(filepath.Join(logs, "main.log"))
		match := pattern.FindStringSubmatch(string(b))
		if len(match) == 2 {
			f.apiBase = "http://" + match[1]
			break
		}
		if strings.Contains(string(b), "fatal") || time.Now().After(deadline) {
			f.t.Fatalf("actual main not ready: %s", b)
		}
		time.Sleep(30 * time.Millisecond)
	}
	if expected := os.Getenv("PROVIDER82_PANEL_SHA256"); expected != "" {
		response, err := http.Get(f.apiBase + "/management.html")
		if err != nil {
			f.t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil || response.StatusCode != 200 || fmt.Sprintf("%x", sha256.Sum256(body)) != expected {
			f.t.Fatal("main did not serve the matching newly built UI")
		}
	}
	return stop
}
