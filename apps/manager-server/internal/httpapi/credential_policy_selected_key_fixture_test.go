package httpapi

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
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

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	cp "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy"
	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	fr "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fullrefresh"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

// Uses only actual configured API-key rows and real Core native admission. The
// sole provider substitution is controlled TLS; no proof/ACK/selector is faked.
type selectedKeyPolicyFixture struct {
	*nativeAdmissionFixture
	nativeQuotaState atomic.Int32 // 0 finite, 1 unknown, 2 unlimited
	grants           atomic.Int32
	oauthSends       atomic.Int32
	restart          func()
	onPause          func(cp.Status)
	onEnd            func()
}

func newSelectedKeyPolicyFixture(t *testing.T, nativeGrant ...bool) (*selectedKeyPolicyFixture, ob.Binding, string) {
	t.Helper()
	binary := os.Getenv("CORE_QUOTA_TEST_BINARY")
	if binary == "" {
		t.Skip("requires verified actual Core association environment")
	}
	verifiedRefreshArtifact(t, binary, os.Getenv("CORE_QUOTA_EXPECTED_SHA256"), os.Getenv("CORE_QUOTA_EXPECTED_REVISION"))
	plugin := os.Getenv("CORE_QUOTA_PLUGIN_DIR")
	verifiedRefreshArtifact(t, filepath.Join(plugin, "opencode-go-cliproxyapi.dll"), os.Getenv("CORE_QUOTA_PLUGIN_EXPECTED_SHA256"), os.Getenv("CORE_QUOTA_PLUGIN_EXPECTED_REVISION"))
	if os.Getenv("CORE_QUOTA_PLUGIN_SDK_REVISION") != os.Getenv("CORE_QUOTA_EXPECTED_REVISION") {
		t.Fatal("SDK mismatch")
	}
	f := &selectedKeyPolicyFixture{nativeAdmissionFixture: &nativeAdmissionFixture{t: t, client: &http.Client{Timeout: 15 * time.Second}, sources: map[string]cq.Source{}, completed: make(chan fr.CompletedObservation, 32)}}
	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			if err := r.ParseForm(); err != nil || r.Form.Get("grant_type") != "refresh_token" {
				t.Error("not an actual native refresh grant")
				w.WriteHeader(400)
				return
			}
			n := f.grants.Add(1)
			jwt := "a." + base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"controlled-issuer","sub":"controlled-subject","https://api.openai.com/auth":{"chatgpt_account_id":"synthetic-continuity-account"}}`)) + ".b"
			fmt.Fprintf(w, `{"access_token":"SYNTHETIC-ROTATED-%d","refresh_token":"SYNTHETIC-GRANT-%d","id_token":%q,"token_type":"Bearer","expires_in":3600}`, n, n, jwt)
			return
		case "/api/usage/token/":
			if r.Header.Get("Authorization") != "Bearer SELECTED-KEY-A" || r.Header.Get("New-Api-User") != "" {
				t.Error("official key-only auth changed")
				w.WriteHeader(403)
				return
			}
			f.quotaCalls[0].Add(1)
			switch f.nativeQuotaState.Load() {
			case 1:
				io.WriteString(w, `{"code":true,"data":{"object":"token_usage","total_available":0}}`)
				return
			case 2:
				io.WriteString(w, `{"code":true,"data":{"object":"token_usage","total_available":0,"unlimited_quota":true}}`)
				return
			}
			remaining := "100"
			if f.depleted.Load() {
				remaining = "0"
			}
			fmt.Fprintf(w, `{"code":true,"data":{"object":"token_usage","total_available":%s,"unlimited_quota":false}}`, remaining)
		case "/v1/responses", "/backend-api/codex/responses":
			if token := r.Header.Get("Authorization"); token == "Bearer SYNTHETIC-INITIAL-ACCESS" || strings.HasPrefix(token, "Bearer SYNTHETIC-ROTATED-") {
				f.oauthSends.Add(1)
			} else {
				switch r.Header.Get("Authorization") {
				case "Bearer SELECTED-KEY-A":
					f.sends[0].Add(1)
				case "Bearer SELECTED-KEY-B":
					f.sends[1].Add(1)
				default:
					t.Error("business credential mismatch")
					w.WriteHeader(403)
					return
				}
			}
			io.Copy(io.Discard, r.Body)
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"selected-key-response\",\"status\":\"completed\",\"output\":[]}}\n\n")
		case "/zen/go/v1/usage":
			reset := time.Now().Add(time.Hour).Format(time.RFC3339)
			fmt.Fprintf(w, `{"usage":{"rolling":{"status":"allowed","percent":25,"resetsAt":%q},"weekly":{"status":"allowed","percent":25,"resetsAt":%q},"monthly":{"status":"allowed","percent":25,"resetsAt":%q}}}`, reset, reset, reset)
		case "/backend-api/wham/usage":
			reset := time.Now().Add(time.Hour).Unix()
			used := 25
			if f.depleted.Load() {
				used = 100
			}
			w.Header().Set("Date", time.Now().UTC().Format(http.TimeFormat))
			fmt.Fprintf(w, `{"rate_limit":{"allowed":%t,"limit_reached":%t,"primary_window":{"used_percent":%d,"reset_at":%d,"limit_window_seconds":18000},"secondary_window":{"used_percent":%d,"reset_at":%d,"limit_window_seconds":604800}}}`, used < 100, used == 100, used, reset, used, reset)
		default:
			t.Errorf("unexpected controlled upstream path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	// Keep verification enabled for configured loopback origins AND the real
	// native OAuth quota/token hostnames used through the isolated Core dialer.
	seed := httptest.NewTLSServer(http.NotFoundHandler())
	certificate := seed.TLS.Certificates[0]
	seed.Close()
	template, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	template.DNSNames = []string{"chatgpt.com", "auth.openai.com", "opencode.ai"}
	template.IPAddresses = []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
	key := certificate.PrivateKey.(crypto.Signer)
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	certificate.Certificate = [][]byte{der}
	certificate.Leaf = nil
	upstream.TLS = seed.TLS.Clone()
	upstream.TLS.Certificates[0] = certificate
	upstream.StartTLS()
	t.Cleanup(upstream.Close)
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	auth := filepath.Join(state, "auth")
	if err := os.MkdirAll(auth, 0700); err != nil {
		t.Fatal(err)
	}
	if len(nativeGrant) > 0 && nativeGrant[0] {
		jwt := "a." + base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"controlled-issuer","sub":"controlled-subject","https://api.openai.com/auth":{"chatgpt_account_id":"synthetic-continuity-account"}}`)) + ".b"
		raw, _ := json.Marshal(map[string]any{"type": "codex", "access_token": "SYNTHETIC-INITIAL-ACCESS", "refresh_token": "SYNTHETIC-INITIAL-GRANT", "account_id": "synthetic-continuity-account", "id_token": jwt, "expired": "2099-01-01T00:00:00Z", "disabled": false, "priority": 200})
		if err := os.WriteFile(filepath.Join(auth, "codex.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	config := fmt.Sprintf("server: {port: 0}\nmanagement: {secret-key: test-password}\noauth: {auth-dir: %q}\nresource-catalog: {enabled: true}\napi-keys:\n  codex:\n    - name: selected-key-fixture\n      base-url: %q\n      keys: [{api-key: SELECTED-KEY-A, priority: 100}, {api-key: SELECTED-KEY-B, priority: 0}]\nplugins:\n  enabled: true\n  dir: %q\n  configs:\n    opencode-go-cliproxyapi:\n      enabled: true\n      base-url: https://opencode.ai/zen/go/v1\n      api-keys: [{name: fixture-only, value: NATIVE-FIXTURE-PLUGIN}]\n", filepath.ToSlash(auth), upstream.URL+"/v1", filepath.ToSlash(plugin))
	if err := os.WriteFile(filepath.Join(state, "config.yaml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	ca := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: upstream.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	f.base = "http://" + address
	cmd := exec.Command(binary, "-test.run=^TestCredentialQuotaProcessFixture$", "-test.count=1", "-test.timeout=120s")
	cmd.Dir = dir
	for _, key := range []string{"PATH", "SystemRoot", "WINDIR", "TEMP", "TMP"} {
		if v := os.Getenv(key); v != "" {
			cmd.Env = append(cmd.Env, key+"="+v)
		}
	}
	cmd.Env = append(cmd.Env, "HOME="+dir, "USERPROFILE="+dir, "QUOTA_FIXTURE_LISTEN="+address, "QUOTA_FIXTURE_STATE_DIR="+state, "QUOTA_FIXTURE_UPSTREAM="+upstream.Listener.Addr().String(), "QUOTA_FIXTURE_CA="+ca, "QUOTA_FIXTURE_PLUGIN_DIR="+plugin, "GATE_FIXTURE_ROUTES=1", "GATE_FIXTURE_TLS_SERVER_NAME="+upstream.Certificate().DNSNames[0], "GATE_FIXTURE_MODEL=native-admission-model")
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
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
		log.Close()
		if t.Failed() {
			raw, _ := os.ReadFile(log.Name())
			t.Log(string(raw))
		}
	})
	var snapshot rc.Snapshot
	deadline := time.Now().Add(20 * time.Second)
	for {
		req, _ := http.NewRequest("GET", f.base+"/v8/management/resources", nil)
		req.Header.Set("Authorization", "Bearer test-password")
		response, err := f.client.Do(req)
		if err == nil {
			data, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if response.StatusCode == 200 && json.Unmarshal(data, &snapshot) == nil && snapshot.Complete {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("actual configured Core not ready")
		}
		time.Sleep(25 * time.Millisecond)
	}
	f.restart = func() {
		oldBoot := snapshot.Scope.BootEpoch
		cmd.Process.Kill()
		cmd.Wait()
		prior := cmd
		cmd = exec.Command(binary, prior.Args[1:]...)
		cmd.Dir = prior.Dir
		cmd.Env = append([]string(nil), prior.Env...)
		cmd.Stdout = log
		cmd.Stderr = log
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(20 * time.Second)
		for {
			request, _ := http.NewRequest("GET", f.base+"/v8/management/resources", nil)
			request.Header.Set("Authorization", "Bearer test-password")
			response, err := f.client.Do(request)
			if err == nil {
				raw, _ := io.ReadAll(response.Body)
				response.Body.Close()
				var current rc.Snapshot
				if response.StatusCode == 200 && json.Unmarshal(raw, &current) == nil && current.Complete && current.Scope.BootEpoch != oldBoot {
					snapshot = current
					return
				}
			}
			if time.Now().After(deadline) {
				t.Fatal("actual Core restart current boot not issued")
			}
			time.Sleep(25 * time.Millisecond)
		}
	}
	cfg := testutil.NewConfig(t)
	protector, _ := security.NewProtector([]byte("0123456789abcdef0123456789abcdef"))
	db, err := store.Open(cfg.DBPath, protector)
	if err != nil {
		t.Fatal(err)
	}
	f.db = db
	t.Cleanup(func() { db.Close() })
	testutil.EnsureAdminCredential(t, db)
	if err := db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: f.base, ManagementKey: "test-password"}}); err != nil {
		t.Fatal(err)
	}
	f.manager = New(cfg, db, collector.NewManager(cfg, db))
	roots := x509.NewCertPool()
	roots.AddCert(upstream.Certificate())
	f.manager.AppContext().OfficialBalanceService.Querier = balance.NewWithNetwork(balance.NetworkOptions{RootCAs: roots})
	f.manager.AppContext().FullRefreshService.Subscribe(func(_ context.Context, e fr.CompletedObservation) { f.completed <- e })
	// Select the unique issued two-member configured group, not a key hash,
	// filename or account label. Only the actual HMAC proof can distinguish A/B.
	var group ob.Binding
	for _, row := range snapshot.Resources {
		if row.Kind == "provider" && row.Family == "codex" && len(row.Members) == 2 {
			ids := map[string]bool{row.ResourceID: true}
			for _, id := range row.Members {
				ids[id] = true
			}
			for changed := true; changed; {
				changed = false
				for _, r := range snapshot.Resources {
					if !ids[r.ResourceID] {
						continue
					}
					for _, binding := range r.SourceBindings {
						if !ids[binding.ResourceID] {
							ids[binding.ResourceID] = true
							changed = true
						}
					}
				}
			}
			group.Scope = snapshot.Scope
			for _, r := range snapshot.Resources {
				if ids[r.ResourceID] {
					group.Targets = append(group.Targets, r.Target)
				}
			}
		}
	}
	if len(group.Targets) < 3 {
		t.Fatal("missing actual full configured group")
	}
	var inventory struct{ Sources []cq.Source }
	f.api("GET", "/usage-service/credential-quota", nil, 200, &inventory)
	for _, src := range inventory.Sources {
		if src.State == "ready" || src.AuthIndex == "" || len(src.Targets) != 1 {
			continue
		}
		for _, target := range group.Targets {
			if target == src.Targets[0] {
				f.sources[src.AuthIndex] = src
				break
			}
		}
	}
	if len(f.sources) != 2 {
		t.Fatal("missing full selected configured bindings")
	}
	for _, src := range f.sources {
		var status any
		f.api("GET", "/usage-service/credential-policy?authIndex="+src.AuthIndex, nil, 200, &status)
	}
	t.Cleanup(func() {
		t.Logf("selected-key actual counts official=%d native[A,B]=[%d,%d]", f.quotaCalls[0].Load(), f.sends[0].Load(), f.sends[1].Load())
	})
	return f, group, upstream.URL
}
