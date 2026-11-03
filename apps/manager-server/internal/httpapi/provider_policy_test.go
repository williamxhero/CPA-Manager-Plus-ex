package httpapi

import (
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

// The policy switch is tested against issued identities from an ordinary Core,
// not a fabricated capability response or a mock routing ACK.
func TestProviderPolicyPublicDefaultOffAndDurableIntent(t *testing.T) {
	base := moneyPolicyCore(t)
	cfg := testutil.NewConfig(t)
	protector, err := security.NewProtector([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(cfg.DBPath, protector)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	testutil.EnsureAdminCredential(t, db)
	if err := db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: base, ManagementKey: "MONEY-CORE-MANAGEMENT"}}); err != nil {
		t.Fatal(err)
	}
	server := New(cfg, db, collector.NewManager(cfg, db))
	inv := server.AppContext().ResourceCapabilityService.Inventory(context.Background(), true)
	if inv.Snapshot == nil || inv.Identity.State != "available" {
		t.Fatalf("actual identity unavailable: %+v", inv)
	}
	provider := ""
	for _, row := range inv.Snapshot.Resources {
		if row.Kind == "provider" {
			provider = row.ResourceID
			break
		}
	}
	if provider == "" {
		t.Fatal("actual provider missing")
	}
	path := "/usage-service/provider-policy?providerId=" + provider
	h := server.Handler()
	testutil.RequireStatus(t, testutil.Request(t, h, "GET", path, "", ""), 401)
	response := testutil.Request(t, h, "GET", path, "", testutil.AdminKey)
	testutil.RequireStatus(t, response, 200)
	var state struct {
		Scope      rc.Scope    `json:"scope"`
		Targets    []rc.Target `json:"targets"`
		ProviderID string      `json:"providerId"`
		Enabled    bool        `json:"enabled"`
		Effective  bool        `json:"effective"`
		Generation int64       `json:"generation"`
		Owner      string      `json:"owner"`
		State      string      `json:"state"`
	}
	testutil.DecodeJSON(t, response, &state)
	if state.Enabled || state.Effective || state.Generation != 0 || state.State != "off" || len(state.Targets) != 2 || state.Owner == "" {
		t.Fatalf("unsafe default: %+v", state)
	}
	body, _ := json.Marshal(map[string]any{"scope": state.Scope, "targets": state.Targets, "providerId": provider, "expectedGeneration": 0, "enabled": false})
	testutil.RequireStatus(t, testutil.Request(t, h, "PUT", "/usage-service/provider-policy", string(body), testutil.AdminKey), 200)
	testutil.RequireStatus(t, testutil.Request(t, h, "PUT", "/usage-service/provider-policy", string(body), testutil.AdminKey), 409)
	owner := state.Owner
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(cfg.DBPath, protector)
	if err != nil {
		t.Fatal(err)
	}
	h = New(cfg, db, collector.NewManager(cfg, db)).Handler()
	response = testutil.Request(t, h, "GET", path, "", testutil.AdminKey)
	testutil.RequireStatus(t, response, 200)
	testutil.DecodeJSON(t, response, &state)
	if state.Generation != 1 || state.Enabled || state.Effective || state.Owner != owner {
		t.Fatalf("SQLite restart changed intent or owner: %+v", state)
	}
	// A configured switch cannot claim runtime/funding authority which is absent.
	body, _ = json.Marshal(map[string]any{"scope": state.Scope, "targets": state.Targets, "providerId": provider, "expectedGeneration": 1, "enabled": true})
	testutil.RequireStatus(t, testutil.Request(t, h, "PUT", "/usage-service/provider-policy", string(body), testutil.AdminKey), 503)
	testutil.RequireStatus(t, testutil.Request(t, h, "GET", "/usage-service/provider-policy?providerId=not-issued", "", testutil.AdminKey), 409)
}

func moneyPolicyCore(t *testing.T, memberCount ...int) string {
	t.Helper()
	binary := os.Getenv("PROVIDER82_CORE_BINARY")
	if binary == "" {
		t.Skip("requires pinned ordinary isolated Core")
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != os.Getenv("PROVIDER82_CORE_SHA256") {
		t.Fatal("Core digest mismatch")
	}
	info, err := buildinfo.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	settings := map[string]string{}
	for _, item := range info.Settings {
		settings[item.Key] = item.Value
	}
	if settings["vcs.revision"] != os.Getenv("PROVIDER82_CORE_REVISION") || settings["vcs.modified"] != "false" || settings["-tags"] != "" || settings["-buildmode"] != "exe" {
		t.Fatal("ordinary clean source identity required")
	}
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
	keys := []string{"{api-key: MONEY-SYNTHETIC-INFERENCE}"}
	if len(memberCount) > 0 && memberCount[0] == 2 {
		keys = append(keys, "{api-key: MONEY-SYNTHETIC-INFERENCE-2}")
	}
	config := fmt.Sprintf("config-version: 8\nserver: {host: 127.0.0.1, port: %d}\nmanagement: {secret-key: MONEY-CORE-MANAGEMENT, disable-control-panel: true, disable-auto-update-panel: true}\noauth: {auth-dir: %q}\nresource-catalog: {enabled: true}\naccess: {api-keys: [MONEY-CLIENT]}\nplugins: {enabled: false}\napi-keys:\n  openai-compatibility:\n    - name: money-provider\n      base-url: https://api.deepseek.com/v1\n      models: [{name: controlled-model}]\n      keys: [%s]\n", port, filepath.ToSlash(filepath.Join(dir, "auth")), strings.Join(keys, ","))
	if len(memberCount) > 1 && memberCount[1] == 2 {
		config += "    - name: isolated-money-provider-b\n      base-url: https://api.deepseek.com/v1\n      models: [{name: controlled-model-b}]\n      keys: [{api-key: MONEY-SYNTHETIC-INFERENCE-B}]\n"
	}
	if len(memberCount) == 3 {
		if memberCount[0] != 1 || memberCount[1] != 1 || memberCount[2] != 1 {
			t.Fatal("three-provider scheduler fixture requires one real member per group")
		}
		for _, suffix := range []string{"b", "c"} {
			config += fmt.Sprintf("    - name: isolated-money-provider-%s\n      base-url: https://api.deepseek.com/v1\n      models: [{name: controlled-model-%s}]\n      keys: [{api-key: MONEY-SYNTHETIC-INFERENCE-%s}]\n", suffix, suffix, strings.ToUpper(suffix))
		}
	}
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	deny := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("blocked background request: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(502)
	}))
	t.Cleanup(deny.Close)
	log, err := os.Create(filepath.Join(dir, "core.log"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "-config", path, "-local-model")
	cmd.Dir, cmd.Stdout, cmd.Stderr = dir, log, log
	for _, key := range []string{"PATH", "SystemRoot", "WINDIR", "TEMP", "TMP", "COMSPEC"} {
		if value := os.Getenv(key); value != "" {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	cmd.Env = append(cmd.Env, "HOME="+dir, "USERPROFILE="+dir, "APPDATA="+dir, "LOCALAPPDATA="+dir, "HTTP_PROXY="+deny.URL, "HTTPS_PROXY="+deny.URL, "NO_PROXY=127.0.0.1,localhost")
	if err := cmd.Start(); err != nil {
		_ = log.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		err := cmd.Wait()
		_ = log.Close()
		t.Logf("owned ordinary Core pid=%d deliberately killed and waited: %v", cmd.Process.Pid, err)
	})
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{Timeout: time.Second}
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
		req, _ := http.NewRequest("GET", base+"/v8/management/resource-capabilities", nil)
		req.Header.Set("Authorization", "Bearer MONEY-CORE-MANAGEMENT")
		res, err := client.Do(req)
		if err == nil {
			_ = res.Body.Close()
			if res.StatusCode == 200 {
				return base
			}
		}
		time.Sleep(20 * time.Millisecond) // Bounded process readiness, not policy ordering.
	}
	t.Fatal("ordinary Core did not become ready")
	return ""
}
