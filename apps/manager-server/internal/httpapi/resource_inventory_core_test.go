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
	cfg := testutil.NewConfig(t)
	db := testutil.NewStore(t, cfg)
	if err := db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: base, ManagementKey: "synthetic-core-key"}}); err != nil {
		t.Fatal(err)
	}
	start := func(allowUnsupported bool) func() {
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
		// A 200 capability response is transport readiness, not catalog publication.
		// Await the same full Manager negotiation that the lifecycle assertions use;
		// keep the existing startup deadline and never accept persistent not_ready.
		handler := New(cfg, db, collector.NewManager(cfg, db)).Handler()
		route := negotiatedPath
		if requireBindings {
			route += "&bindings=1"
		}
		last := "no negotiation"
		for {
			req := httptest.NewRequest("GET", route, nil).WithContext(ctx)
			req.Header.Set("Authorization", "Bearer "+testutil.AdminKey)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				t.Fatalf("Manager startup negotiation HTTP %d", rr.Code)
			}
			var got struct{ Negotiated caps.Negotiated }
			if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
				t.Fatal("invalid Manager startup negotiation JSON")
			}
			n := got.Negotiated
			if strings.Contains(rr.Body.String(), "CANARY") || strings.Contains(rr.Body.String(), "synthetic-core-key") {
				t.Fatal("startup negotiation leaked source secrets")
			}
			if n.IndependentGate.State != "unavailable" || !n.PolicyLocked {
				t.Fatal("startup negotiation claimed gate support or unlocked policy")
			}
			last = n.Identity.State + "/" + n.Identity.Reason
			if n.BindingCapability != nil {
				last += " bindings=" + n.BindingCapability.State + "/" + n.BindingCapability.Reason
			}
			if n.Identity.State == "available" && n.Snapshot != nil && n.Snapshot.Complete &&
				(!requireBindings || (n.BindingCapability != nil && n.BindingCapability.State == "available" && len(n.Bindings) > 0)) {
				return stop
			}
			if allowUnsupported && n.Identity.State == "unavailable" && n.Snapshot == nil &&
				(n.Identity.Reason == "unsupported_mode" || n.Identity.Reason == "unsupported_storage" || n.Identity.Reason == "unsupported_filesystem" || n.Identity.Reason == "not_enabled") {
				return stop // Explicit negative mode: assertions below must still agree with Core.
			}
			// Authentication/schema errors are real failures, not startup transients.
			if n.Identity.Reason == "core_auth_error" || n.Identity.Reason == "invalid_contract" || n.Identity.Reason == "unsupported_contract" ||
				(n.BindingCapability != nil && n.BindingCapability.Reason == "invalid_binding_contract") {
				t.Fatalf("invalid startup negotiation: %s", last)
			}

			select {
			case <-ctx.Done():
				t.Fatalf("isolated Core full Manager negotiation did not become ready before startup deadline: %s", last)
			case <-ticker.C:
			}
		}
	}
	stop := start(false)
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
			req, _ := http.NewRequest("GET", base+"/v8/management/credentials", nil)
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
	stop = start(false)
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
	stop = start(false)
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
	stop = start(true)
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
