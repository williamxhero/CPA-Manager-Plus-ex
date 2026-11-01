package httpapi

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	cp "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy"
	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	fr "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fullrefresh"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

// This fixture only substitutes the external provider. Manager subscriptions,
// SQLite, Core inventory, quota adapter, native executor and admission stay real.
type nativeAdmissionFixture struct {
	t          *testing.T
	manager    *Server
	db         *store.Store
	base       string
	client     *http.Client
	sources    map[string]cq.Source
	completed  chan fr.CompletedObservation
	quotaCalls [2]atomic.Int64
	sends      [2]atomic.Int64
	depleted   atomic.Bool
}

func newNativeAdmissionFixture(t *testing.T) *nativeAdmissionFixture {
	t.Helper()
	binary := os.Getenv("CORE_QUOTA_TEST_BINARY")
	if binary == "" {
		t.Skip("requires independently verified actual Core native fixture artifact")
	}
	verifiedRefreshArtifact(t, binary, os.Getenv("CORE_QUOTA_EXPECTED_SHA256"), os.Getenv("CORE_QUOTA_EXPECTED_REVISION"))
	plugin := os.Getenv("CORE_QUOTA_PLUGIN_DIR")
	verifiedRefreshArtifact(t, filepath.Join(plugin, "opencode-go-cliproxyapi.dll"), os.Getenv("CORE_QUOTA_PLUGIN_EXPECTED_SHA256"), os.Getenv("CORE_QUOTA_PLUGIN_EXPECTED_REVISION"))
	if os.Getenv("CORE_QUOTA_PLUGIN_SDK_REVISION") != os.Getenv("CORE_QUOTA_EXPECTED_REVISION") {
		t.Fatal("plugin SDK provenance mismatch")
	}
	f := &nativeAdmissionFixture{t: t, client: &http.Client{Timeout: 15 * time.Second}, sources: map[string]cq.Source{}, completed: make(chan fr.CompletedObservation, 32)}
	t.Cleanup(func() {
		t.Logf("exact controlled counts quota[A,B]=[%d,%d] business[A,B]=[%d,%d]", f.quotaCalls[0].Load(), f.quotaCalls[1].Load(), f.sends[0].Load(), f.sends[1].Load())
	})
	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := -1
		if r.URL.Path == "/zen/go/v1/usage" && r.Header.Get("Authorization") == "Bearer NATIVE-FIXTURE-PLUGIN" {
			reset := time.Now().Add(time.Hour).Format(time.RFC3339)
			fmt.Fprintf(w, `{"usage":{"rolling":{"status":"allowed","percent":25,"resetsAt":%q},"weekly":{"status":"allowed","percent":25,"resetsAt":%q},"monthly":{"status":"allowed","percent":25,"resetsAt":%q}}}`, reset, reset, reset)
			return
		}
		switch r.Header.Get("Authorization") {
		case "Bearer NATIVE-ADMISSION-A":
			i = 0
		case "Bearer NATIVE-ADMISSION-B":
			i = 1
		}
		if i < 0 {
			t.Error("unattributed controlled upstream request")
			w.WriteHeader(403)
			return
		}
		switch r.URL.Path {
		case "/backend-api/wham/usage":
			f.quotaCalls[i].Add(1)
			if r.Header.Get("Chatgpt-Account-Id") != []string{"native-admission-account-A", "native-admission-account-B"}[i] {
				t.Error("quota credential/account mismatch")
			}
			used := 25
			if f.depleted.Load() {
				used = 100
			}
			reset := time.Now().Add(time.Hour).Unix()
			w.Header().Set("Date", time.Now().UTC().Format(http.TimeFormat))
			fmt.Fprintf(w, `{"rate_limit":{"allowed":%t,"limit_reached":%t,"primary_window":{"used_percent":%d,"reset_at":%d,"limit_window_seconds":18000},"secondary_window":{"used_percent":%d,"reset_at":%d,"limit_window_seconds":604800}}}`, used < 100, used == 100, used, reset, used, reset)
		case "/backend-api/codex/responses", "/v1/responses":
			f.sends[i].Add(1)
			io.Copy(io.Discard, r.Body)
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"native-admission-response\",\"status\":\"completed\",\"output\":[]}}\n\n")
		default:
			t.Errorf("unapproved controlled endpoint %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	seed := httptest.NewTLSServer(http.NotFoundHandler())
	cert := seed.TLS.Certificates[0]
	seed.Close()
	template, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
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
	t.Cleanup(upstream.Close)
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	auth := filepath.Join(state, "auth")
	if err = os.MkdirAll(auth, 0700); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"A", "B"} {
		body := fmt.Sprintf(`{"type":"codex","access_token":"NATIVE-ADMISSION-%s","account_id":"native-admission-account-%s","disabled":false,"priority":%d}`, name, name, 100-i*100)
		if err = os.WriteFile(filepath.Join(auth, map[string]string{"A": "codex.json", "B": "native-B.json"}[name]), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.WriteFile(filepath.Join(auth, "opencode.json"), []byte(`{"type":"claude","access_token":"DISABLED-DECOY","disabled":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	// The mandatory plugin is loaded, but its required synthetic quota credential is isolated. No
	// configured inference key is added; only A/B can receive business traffic.
	config := fmt.Sprintf("server: {port: 0}\nmanagement: {secret-key: test-password}\noauth: {auth-dir: %q}\nresource-catalog: {enabled: true}\nplugins:\n  enabled: true\n  dir: %q\n  configs:\n    opencode-go-cliproxyapi:\n      enabled: true\n      base-url: https://opencode.ai/zen/go/v1\n      api-keys: [{name: fixture-only, value: NATIVE-FIXTURE-PLUGIN}]\n", filepath.ToSlash(auth), filepath.ToSlash(plugin))
	if err = os.WriteFile(filepath.Join(state, "config.yaml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	ca := filepath.Join(dir, "ca.pem")
	if err = os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
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
	for _, k := range []string{"PATH", "SystemRoot", "WINDIR", "TEMP", "TMP"} {
		if v := os.Getenv(k); v != "" {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	cmd.Env = append(cmd.Env, "HOME="+dir, "USERPROFILE="+dir, "QUOTA_FIXTURE_LISTEN="+address, "QUOTA_FIXTURE_STATE_DIR="+state, "QUOTA_FIXTURE_UPSTREAM="+upstream.Listener.Addr().String(), "QUOTA_FIXTURE_CA="+ca, "QUOTA_FIXTURE_PLUGIN_DIR="+plugin, "GATE_FIXTURE_ROUTES=1", "GATE_FIXTURE_TLS_SERVER_NAME=chatgpt.com", "GATE_FIXTURE_MODEL=native-admission-model")
	log, err := os.Create(filepath.Join(dir, "core.log"))
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = log
	cmd.Stderr = log
	if err = cmd.Start(); err != nil {
		log.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
		log.Close()
		if t.Failed() {
			b, _ := os.ReadFile(log.Name())
			t.Log(string(b))
		}
	})
	// Readiness polling does not order admission; all query/completion ordering
	// below uses the actual coordinator's completion notification.
	deadline := time.Now().Add(20 * time.Second)
	for {
		req, _ := http.NewRequest("GET", f.base+"/v8/management/credentials", nil)
		req.Header.Set("Authorization", "Bearer test-password")
		resp, e := f.client.Do(req)
		if e == nil {
			data, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == 200 {
				var files struct {
					Files []struct {
						Name  string `json:"name"`
						Index string `json:"auth_index"`
					} `json:"files"`
				}
				if json.Unmarshal(data, &files) != nil {
					t.Fatal("invalid actual auth inventory")
				}
				for _, file := range files.Files {
					for _, name := range []string{"A", "B"} {
						if file.Name == map[string]string{"A": "codex.json", "B": "native-B.json"}[name] {
							f.sources[name] = cq.Source{AuthIndex: file.Index}
						}
					}
				}
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("actual native Core readiness unavailable")
		}
	}
	cfg := testutil.NewConfig(t)
	db := testutil.NewStore(t, cfg)
	f.db = db
	// Transparent diagnostic tap: forwards every real Core request/response
	// unchanged. It cannot manufacture a successful capability, receipt or ACK.
	coreURL, err := url.Parse(f.base)
	if err != nil {
		t.Fatal(err)
	}
	tap := httputil.NewSingleHostReverseProxy(coreURL)
	tap.ModifyResponse = func(response *http.Response) error {
		if response.Request.Method == "POST" && strings.HasSuffix(response.Request.URL.Path, "/inference-gates/operations") {
			data, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil {
				return err
			}
			response.Body = io.NopCloser(bytes.NewReader(data))
			if response.StatusCode != 200 {
				t.Logf("DIAG actual unchanged Core gate POST status=%d body=%s", response.StatusCode, data)
			}
		}
		return nil
	}
	edge := httptest.NewServer(tap)
	t.Cleanup(edge.Close)
	if err = db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: edge.URL, ManagementKey: "test-password"}}); err != nil {
		t.Fatal(err)
	}
	f.manager = New(cfg, db, collector.NewManager(cfg, db))
	f.manager.AppContext().FullRefreshService.Subscribe(func(_ context.Context, event fr.CompletedObservation) { f.completed <- event })
	var inventory struct {
		Sources []cq.Source `json:"sources"`
	}
	f.api("GET", "/usage-service/credential-quota", nil, 200, &inventory)
	for _, name := range []string{"A", "B"} {
		index := f.sources[name].AuthIndex
		found := false
		for _, src := range inventory.Sources {
			if index != "" && src.AuthIndex == index {
				f.sources[name] = src
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing real native %s source", name)
		}
	}
	return f
}

func (f *nativeAdmissionFixture) api(method, path string, body any, want int, out any) {
	f.t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		f.t.Fatal(err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+testutil.AdminKey)
	rr := httptest.NewRecorder()
	f.manager.Handler().ServeHTTP(rr, req)
	if rr.Code != want {
		f.t.Fatalf("%s %s returned %d want %d: %s", method, path, rr.Code, want, rr.Body.String())
	}
	if out != nil {
		if err = json.Unmarshal(rr.Body.Bytes(), out); err != nil {
			f.t.Fatal(err)
		}
	}
}

func (f *nativeAdmissionFixture) status(name string) cp.Status {
	f.t.Helper()
	var state cp.Status
	f.api("GET", "/usage-service/credential-policy?authIndex="+f.sources[name].AuthIndex, nil, 200, &state)
	return state
}

func (f *nativeAdmissionFixture) sendHTTP() {
	f.t.Helper()
	req, _ := http.NewRequest("POST", f.base+"/v1/responses", strings.NewReader(`{"model":"native-admission-model","input":[],"stream":false}`))
	req.Header.Set("Authorization", "Bearer synthetic-client")
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.client.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	data, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		f.t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		f.t.Fatalf("native HTTP status=%d body=%s", resp.StatusCode, data)
	}
}
