package httpapi

import (
	"context"
	"encoding/json"
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
	caps "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

// This opt-in regression uses a separately built, unmodified real Core. Every
// config, source and DB is synthetic and temporary. No live connection is used.
func TestResourceInventoryRealCoreLifecycle(t *testing.T) {
	binary := os.Getenv("CORE_IDENTITY_TEST_BINARY")
	if binary == "" {
		t.Skip("set CORE_IDENTITY_TEST_BINARY to an independently built Core cmd/server binary")
	}
	var upstreamCalls, blockedStartupCalls atomic.Int64
	deny := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.IsAbs() || r.Method == http.MethodConnect {
			blockedStartupCalls.Add(1)
		} else {
			upstreamCalls.Add(1)
		}
		http.Error(w, "synthetic upstream denied", 503)
	}))
	defer deny.Close()
	requireBindings := os.Getenv("CORE_IDENTITY_REQUIRE_BINDINGS") == "1"
	dir := t.TempDir()
	authDir := filepath.Join(dir, "auth")
	if err := os.Mkdir(authDir, 0700); err != nil {
		t.Fatal(err)
	}
	if requireBindings {
		if err := os.WriteFile(filepath.Join(authDir, "identity-bound.json"), []byte(`{"type":"codex","access_token":"CANARY-FILE-TOKEN","disabled":true}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	path := filepath.Join(dir, "config.yaml")
	original := fmt.Sprintf("config-version: 8\nserver: {host: 127.0.0.1, port: %d}\nmanagement: {secret-key: synthetic-core-key, disable-control-panel: true, disable-auto-update-panel: true}\noauth: {auth-dir: %q}\nresource-catalog: {enabled: true, state-dir: catalog}\nplugins: {enabled: false}\napi-keys:\n  codex:\n    - name: CANARY-NAME\n      base-url: %s\n      keys: [{api-key: CANARY-KEY, disabled: true}]\n", port, filepath.ToSlash(authDir), deny.URL)
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	start := func() func() {
		cmd := exec.Command(binary, "-config", path, "-local-model")
		cmd.Dir = dir
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
		// Do not inherit live tokens, user config, custom catalog URLs or store settings.
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
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			req, _ := http.NewRequestWithContext(ctx, "GET", base+"/v8/management/resource-capabilities", nil)
			req.Header.Set("Authorization", "Bearer synthetic-core-key")
			res, err := http.DefaultClient.Do(req)
			if err == nil {
				io.Copy(io.Discard, res.Body)
				res.Body.Close()
				if res.StatusCode == 200 {
					return stop
				}
			}
			select {
			case <-ctx.Done():
				t.Fatal("isolated Core did not become ready")
			case <-ticker.C:
			}
		}
	}
	stop := start()
	cfg := testutil.NewConfig(t)
	db := testutil.NewStore(t, cfg)
	if err := db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: base, ManagementKey: "synthetic-core-key"}}); err != nil {
		t.Fatal(err)
	}
	read := func() caps.Snapshot {
		handler := New(cfg, db, collector.NewManager(cfg, db)).Handler()
		route := negotiatedPath
		if requireBindings {
			route += "&bindings=1"
		}
		rr := testutil.Request(t, handler, "GET", route, "", testutil.AdminKey)
		testutil.RequireStatus(t, rr, 200)
		var got struct{ Negotiated caps.Negotiated }
		if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.Negotiated.Identity.State != "available" || got.Negotiated.Snapshot == nil || got.Negotiated.IndependentGate.State != "unavailable" {
			t.Fatalf("real Core inventory missing: %s", rr.Body.String())
		}
		if strings.Contains(rr.Body.String(), "CANARY") || strings.Contains(rr.Body.String(), "synthetic-core-key") {
			t.Fatal("source secret leaked")
		}
		if requireBindings {
			if got.Negotiated.BindingCapability == nil || got.Negotiated.BindingCapability.State != "available" || len(got.Negotiated.Bindings) == 0 {
				t.Fatalf("delivered producer lacks host bindings: %s", rr.Body.String())
			}
			// Obtain the real current-runtime selector from Core's public metadata API,
			// independently of the binding response. Names locate this synthetic fixture
			// only; they are never used to construct durable resource identity.
			req, _ := http.NewRequest("GET", base+"/v8/management/auth-files", nil)
			req.Header.Set("Authorization", "Bearer synthetic-core-key")
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			var files struct {
				Files []struct {
					Name  string
					Index string `json:"auth_index"`
				}
			}
			err = json.NewDecoder(res.Body).Decode(&files)
			res.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			index := ""
			for _, file := range files.Files {
				if file.Name == "identity-bound.json" {
					index = file.Index
				}
			}
			if index == "" {
				t.Fatal("real runtime fixture was not published")
			}
			matched := false
			for _, binding := range got.Negotiated.Bindings {
				if binding.AuthIndex != index {
					continue
				}
				matched = true
				if binding.Scope != got.Negotiated.Snapshot.Scope || len(binding.Targets) != 1 || binding.RuntimeAlias != binding.Targets[0].ResourceID {
					t.Fatal("runtime binding scope/target lost")
				}
				target := binding.Targets[0]
				found := false
				for _, row := range got.Negotiated.Snapshot.Resources {
					if row.Target == target && row.Kind == "credential" && row.ParentID == "" && row.Manual.Disabled {
						found = true
					}
				}
				if !found {
					t.Fatal("host association does not identify the disabled fixture")
				}
			}
			if !matched {
				t.Fatal("actual runtime selector has no current Manager-consumed binding")
			}
		}
		return *got.Negotiated.Snapshot
	}
	before := read()
	wantRows := 2
	if requireBindings {
		wantRows = 3
	}
	if len(before.Resources) != wantRows {
		t.Fatalf("want provider and disabled member, got %d", len(before.Resources))
	}
	var member caps.Row
	for _, row := range before.Resources {
		if row.Kind == "credential" && row.ParentID != "" {
			member = row
		}
	}
	if !member.Manual.Disabled || member.ParentID == "" {
		t.Fatal("disabled member missing")
	}
	stop()
	// Preserve Core-owned enrollment markers while renaming and replacing key.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "CANARY-NAME", "RENAMED", 1))
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	stop = start()
	renamed := read()
	for _, row := range renamed.Resources {
		if row.ResourceID == member.ResourceID && (row.Incarnation != member.Incarnation || row.IdentityVersion != member.IdentityVersion || row.CredentialVersion != member.CredentialVersion) {
			t.Fatal("rename changed credential identity (config version may legitimately advance)")
		}
	}
	if renamed.InstanceID != before.InstanceID || renamed.BootEpoch == before.BootEpoch {
		t.Fatal("restart scope continuity/fence lost")
	}
	stop()
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "CANARY-KEY", "CANARY-REPLACED", 1))
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	stop = start()
	replaced := read()
	for _, row := range replaced.Resources {
		if row.ResourceID == member.ResourceID && (row.CredentialVersion == member.CredentialVersion || row.Incarnation == member.Incarnation) {
			t.Fatal("key replacement reused old observation preconditions")
		}
	}
	stop()
	// Current plugin mode is an honest blocker, never a partial success with plugins dropped.
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "enabled: false", "enabled: true", 1))
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	stop = start()
	rr := testutil.Request(t, New(cfg, db, collector.NewManager(cfg, db)).Handler(), "GET", negotiatedPath, "", testutil.AdminKey)
	req, _ := http.NewRequest("GET", base+"/v8/management/resource-capabilities", nil)
	req.Header.Set("Authorization", "Bearer synthetic-core-key")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var advertised struct {
		Inventory bool
		Reason    string
	}
	err = json.NewDecoder(res.Body).Decode(&advertised)
	res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	// Baseline Core rejects plugin mode; the parallel producer can add support.
	// Assert negotiated truth, not a permanently hardcoded plugin exclusion.
	if advertised.Inventory {
		if !strings.Contains(rr.Body.String(), `"snapshot":`) || !strings.Contains(rr.Body.String(), `"reason":"durable_inventory"`) {
			t.Fatalf("supported plugin mode lost: %s", rr.Body.String())
		}
	} else if !strings.Contains(rr.Body.String(), `"reason":"`+advertised.Reason+`"`) || strings.Contains(rr.Body.String(), `"snapshot":`) {
		t.Fatalf("unsupported plugin mode misrepresented: %s", rr.Body.String())
	}
	stop()
	t.Logf("controlled proxy denied %d startup/background requests; provider inventory calls=%d", blockedStartupCalls.Load(), upstreamCalls.Load())
	if upstreamCalls.Load() != 0 {
		t.Fatalf("inventory contacted upstream %d times", upstreamCalls.Load())
	}
}
