package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/sha1"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
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
	"sync"
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

// Minimal RFC6455 client at the downstream wire boundary; no SDK dependency or
// private selector/session hooks. Every turn traverses Core's actual WS route.
func (f *nativeAdmissionFixture) reusedWSTurn() func() {
	t := f.t
	t.Helper()
	u, err := url.Parse(f.base)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", u.Host, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	keyBytes := make([]byte, 16)
	if _, err = rand.Read(keyBytes); err != nil {
		t.Fatal(err)
	}
	key := base64.StdEncoding.EncodeToString(keyBytes)
	fmt.Fprintf(conn, "GET /v1/responses HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: %s\r\nAuthorization: Bearer synthetic-client\r\n\r\n", u.Host, key)
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: "GET"})
	if err != nil {
		t.Fatal(err)
	}
	expected := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	if response.StatusCode != 101 || response.Header.Get("Sec-WebSocket-Accept") != base64.StdEncoding.EncodeToString(expected[:]) {
		t.Fatal("actual downstream WS handshake not verified")
	}
	writeFrame := func(opcode byte, data []byte) {
		t.Helper()
		if len(data) > 65535 {
			t.Fatal("bounded WS fixture frame exceeded")
		}
		frame := []byte{0x80 | opcode}
		if len(data) < 126 {
			frame = append(frame, 0x80|byte(len(data)))
		} else {
			frame = append(frame, 0x80|126, byte(len(data)>>8), byte(len(data)))
		}
		mask := make([]byte, 4)
		if _, err := rand.Read(mask); err != nil {
			t.Fatal(err)
		}
		frame = append(frame, mask...)
		for i, b := range data {
			frame = append(frame, b^mask[i%4])
		}
		if _, err := conn.Write(frame); err != nil {
			t.Fatal(err)
		}
	}
	return func() {
		t.Helper()
		if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
			t.Fatal(err)
		}
		writeFrame(1, []byte(`{"type":"response.create","model":"native-admission-model","input":[{"role":"user","content":"synthetic WS turn"}]}`))
		var message []byte
		for {
			header := make([]byte, 2)
			if _, err := io.ReadFull(reader, header); err != nil {
				t.Fatal(err)
			}
			if header[1]&0x80 != 0 {
				t.Fatal("server sent masked WS frame")
			}
			length := uint64(header[1] & 127)
			if length == 126 {
				var size [2]byte
				if _, err := io.ReadFull(reader, size[:]); err != nil {
					t.Fatal(err)
				}
				length = uint64(binary.BigEndian.Uint16(size[:]))
			} else if length == 127 {
				var size [8]byte
				if _, err := io.ReadFull(reader, size[:]); err != nil {
					t.Fatal(err)
				}
				length = binary.BigEndian.Uint64(size[:])
			}
			if length > 2<<20 {
				t.Fatal("oversized actual WS fixture response")
			}
			data := make([]byte, int(length))
			if _, err := io.ReadFull(reader, data); err != nil {
				t.Fatal(err)
			}
			switch header[0] & 15 {
			case 9:
				writeFrame(10, data)
				continue
			case 10:
				continue
			case 8:
				t.Fatal("reused WS closed before turn completion")
			case 0, 1:
				message = append(message, data...)
			default:
				t.Fatal("unexpected WS data opcode")
			}
			if header[0]&0x80 == 0 {
				continue
			}
			var event struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(message, &event) != nil {
				t.Fatal("invalid actual WS event")
			}
			switch event.Type {
			case "error", "response.failed":
				t.Fatalf("actual WS turn failed: %s", message)
			case "response.completed":
				conn.SetDeadline(time.Time{})
				return
			}
			message = nil
		}
	}
}

// This fixture only substitutes the external provider. Manager subscriptions,
// SQLite, Core inventory, quota adapter, native executor and admission stay real.
type nativeAdmissionFixture struct {
	t            *testing.T
	manager      *Server
	db           *store.Store
	base         string
	client       *http.Client
	sources      map[string]cq.Source
	completed    chan fr.CompletedObservation
	quotaCalls   [2]atomic.Int64
	sends        [2]atomic.Int64
	depleted     atomic.Bool
	drainRelease chan struct{}
	drainEntered chan struct{}
	drainOnce    sync.Once
	quotaBarrier atomic.Pointer[nativeAdmissionQuotaResponseBarrier]
}

// Optional external-response barrier for the separate ON-prerequisite slice.
// Nil in the clean five-case checkpoint; it never substitutes query evidence.
type nativeAdmissionQuotaResponseBarrier struct {
	entered chan struct{}
	release <-chan struct{}
}

func newNativeAdmissionFixture(t *testing.T, manualA ...bool) *nativeAdmissionFixture {
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
	f.drainRelease = make(chan struct{})
	f.drainEntered = make(chan struct{}, 1)
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
			if barrier := f.quotaBarrier.Load(); barrier != nil {
				select {
				case barrier.entered <- struct{}{}:
				default:
				}
				select {
				case <-barrier.release:
				case <-r.Context().Done():
					return
				}
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
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			if bytes.Contains(body, []byte("native-admission-drain")) {
				io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"content_index\":0,\"delta\":\"admitted-before-pause\"}\n\n")
				w.(http.Flusher).Flush()
				f.drainEntered <- struct{}{}
				select {
				case <-f.drainRelease:
				case <-r.Context().Done():
					return
				}
				io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"content_index\":0,\"delta\":\"drained-after-pause\"}\n\n")
			}
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
	t.Cleanup(f.releaseDrain)
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	auth := filepath.Join(state, "auth")
	if err = os.MkdirAll(auth, 0700); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"A", "B"} {
		disabled := name == "A" && len(manualA) > 0 && manualA[0]
		body := fmt.Sprintf(`{"type":"codex","access_token":"NATIVE-ADMISSION-%s","account_id":"native-admission-account-%s","disabled":%t,"priority":%d}`, name, name, disabled, 100-i*100)
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

func (f *nativeAdmissionFixture) coreAPI(method, path string, body, out any) {
	f.t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		f.t.Fatal(err)
	}
	req, _ := http.NewRequest(method, f.base+"/v8/management/"+path, bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer test-password")
	req.Header.Set("Content-Type", "application/json")
	response, err := f.client.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer response.Body.Close()
	data, err = io.ReadAll(response.Body)
	if err != nil {
		f.t.Fatal(err)
	}
	if response.StatusCode != 200 {
		f.t.Fatalf("actual Core %s %s status=%d body=%s", method, path, response.StatusCode, data)
	}
	if err = json.Unmarshal(data, out); err != nil {
		f.t.Fatal(err)
	}
}

func (f *nativeAdmissionFixture) releaseDrain() { f.drainOnce.Do(func() { close(f.drainRelease) }) }

func (f *nativeAdmissionFixture) openSSE(drain bool) *http.Response {
	f.t.Helper()
	input := "ordinary-native-sse"
	if drain {
		input = "native-admission-drain"
	}
	data, _ := json.Marshal(map[string]any{"model": "native-admission-model", "input": []any{map[string]any{"role": "user", "content": input}}, "stream": true})
	req, _ := http.NewRequest("POST", f.base+"/v1/responses", bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer synthetic-client")
	req.Header.Set("Content-Type", "application/json")
	response, err := f.client.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { response.Body.Close() })
	if response.StatusCode != 200 {
		response.Body.Close()
		f.t.Fatalf("SSE status=%d", response.StatusCode)
	}
	if !strings.Contains(response.Header.Get("Content-Type"), "text/event-stream") {
		f.t.Fatal("not actual downstream SSE")
	}
	return response
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
