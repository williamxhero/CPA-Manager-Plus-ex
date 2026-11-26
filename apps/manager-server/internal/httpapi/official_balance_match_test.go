package httpapi

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/balancematch"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

func TestOfficialBalanceMatchLifecycle(t *testing.T) {
	const oldKey, newKey, manualKey = "SYNTHETIC-PROBE-OLD", "SYNTHETIC-PROBE-NEW", "SYNTHETIC-MANUAL"
	const managementKey, decoyKey = "SYNTHETIC-CORE-MANAGEMENT", "SYNTHETIC-OTHER-ACCESS"
	var logs bytes.Buffer
	logOutput := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(logOutput)
	var phase atomic.Int32
	var upstreamCalls atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		want := oldKey
		if phase.Load() >= 3 {
			want = newKey
		}
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer "+want {
			t.Error("incorrect probe method or credential")
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/user/balance" && phase.Load() == 0:
			io.WriteString(w, `{"is_available":true,"balance_infos":[{"currency":"USD","total_balance":"10","granted_balance":"0","topped_up_balance":"10"}]}`)
		case r.URL.Path == "/v1/usage":
			// Deliberately try to reflect a secret in the upstream body.
			json.NewEncoder(w).Encode(map[string]string{"balance": "20.50", "echo": r.Header.Get("Authorization")})
		default:
			w.Header().Set("X-Reflected", r.Header.Get("Authorization"))
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, r.Header.Get("Authorization"))
		}
	}))
	defer upstream.Close()
	base := strings.Replace(upstream.URL, "127.0.0.1", "wallet.example.com", 1)
	updated := func(s string) string {
		return strings.Replace(s, `"credential_version":3`, `"credential_version":4`, 1)
	}
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer "+managementKey {
			t.Error("incorrect Core method or authentication")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		p := phase.Load()
		switch r.URL.Path {
		case "/v8/management/resource-capabilities":
			io.WriteString(w, testCapsJSON)
		case "/v8/management/resources":
			snapshot := testSnapshotJSON
			if p >= 3 {
				snapshot = updated(snapshot)
			}
			io.WriteString(w, snapshot)
		case "/v8/management/config/api-keys/codex":
			host, key := base, oldKey
			if p == 2 {
				host = "https://api.deepseek.com"
			}
			if p >= 3 {
				key = newKey
			}
			json.NewEncoder(w).Encode([]any{
				map[string]any{"base-url": "https://api.deepinfra.com/v1", "keys": []any{map[string]string{"resource-id": "66666666-6666-4666-8666-666666666666", "api-key": decoyKey}}},
				map[string]any{"base-url": host + "/v1", "keys": []any{map[string]string{"resource-id": "44444444-4444-4444-8444-444444444444", "api-key": key, "base-url": "https://api.deepinfra.com/v1"}}},
			})
		default:
			t.Errorf("unexpected Core read: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer core.Close()

	cfg := testutil.NewConfig(t)
	protector, _ := security.NewProtector([]byte("0123456789abcdef0123456789abcdef"))
	db, err := store.Open(cfg.DBPath, protector)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	testutil.EnsureAdminCredential(t, db)
	if err := db.SaveManagerConfig(t.Context(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: core.URL, ManagementKey: managementKey}}); err != nil {
		t.Fatal(err)
	}
	s := New(cfg, db, collector.NewManager(cfg, db))
	roots := x509.NewCertPool()
	roots.AddCert(upstream.Certificate())
	s.AppContext().BalanceMatcher = balancematch.New(s.AppContext().OfficialBalanceService, balancematch.Options{
		HostInterval: time.Millisecond,
		Network: balance.NetworkOptions{RootCAs: roots,
			LookupIP: func(context.Context, string) ([]net.IPAddr, error) {
				return []net.IPAddr{{IP: net.IPv4(93, 184, 216, 34)}}, nil
			},
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, upstream.Listener.Addr().String())
			},
		},
	})
	h := s.Handler()
	const path = balancePath + "/match"
	call := func(method, path, body, key string, code int) *httptest.ResponseRecorder {
		t.Helper()
		rr := testutil.Request(t, h, method, path, body, key)
		testutil.RequireStatus(t, rr, code)
		for _, secret := range []string{oldKey, newKey, manualKey, managementKey, decoyKey, testutil.AdminKey} {
			if strings.Contains(rr.Body.String(), secret) || strings.Contains(logs.String(), secret) {
				t.Fatal("credential appeared in response or logs")
			}
		}
		return rr
	}
	binding := "{" + balanceBinding + "}"
	match := func(bind string, apply bool) balancematch.Result {
		t.Helper()
		body := `{"binding":` + bind
		if apply {
			body += `,"apply":true`
		}
		var result balancematch.Result
		testutil.DecodeJSON(t, call("POST", path, body+"}", testutil.AdminKey, 200), &result)
		return result
	}
	read := func(bind string) officialbalance.Status {
		t.Helper()
		var status officialbalance.Status
		testutil.DecodeJSON(t, call("GET", balancePath+"?binding="+url.QueryEscape(bind), "", testutil.AdminKey, 200), &status)
		return status
	}
	call("POST", path, `{"binding":`+binding+`}`, "", 401)
	call("POST", path, `{"binding":`+binding+`}`, managementKey, 401)
	call("GET", path, "", testutil.AdminKey, 405)
	call("POST", path, `{"binding":`+binding+`,"baseURL":"https://example.com"}`, testutil.AdminKey, 400)
	if calls := upstreamCalls.Load(); calls != 0 {
		t.Fatalf("invalid requests probed upstream %d times", calls)
	}
	if out := match(binding, true); out.Applied || out.Status != "unmatched" || out.Reason != "tied_candidates" {
		t.Fatalf("tie must not write: %+v", out)
	}
	if st := read(binding); st.Version != 0 || st.Configured {
		t.Fatalf("unmatched persisted: %+v", st)
	}
	phase.Store(1)
	if out := match(binding, false); out.Applied || out.Status != "matched" || out.Adapter != balance.OpenAICompatUsageV1 {
		t.Fatalf("dry run: %+v", out)
	}
	if st := read(binding); st.Version != 0 || st.Configured {
		t.Fatalf("dry run persisted: %+v", st)
	}
	if out := match(binding, true); !out.Applied || out.Endpoint != base+"/v1/usage" {
		t.Fatalf("apply: %+v", out)
	}
	if st := read(binding); st.Version != 1 || !st.Configured || st.Endpoint != base+"/v1/usage" {
		t.Fatalf("apply not read back: %+v", st)
	}
	phase.Store(2)
	count := upstreamCalls.Load()
	if out := match(binding, false); out.Evidence.Source != "fingerprint" || out.Adapter != "deepseek" || upstreamCalls.Load() != count {
		t.Fatalf("fingerprint unexpectedly probed: %+v", out)
	}

	phase.Store(3)
	call("POST", path, `{"binding":`+binding+`,"apply":true}`, testutil.AdminKey, 409)
	currentBinding := updated(binding)
	if st := read(currentBinding); st.Version != 1 || st.Configured || st.State != "needs_configuration" {
		t.Fatalf("old source not fenced: %+v", st)
	}
	if out := match(currentBinding, true); !out.Applied || out.Status != "matched" {
		t.Fatalf("re-match current binding: %+v", out)
	}
	if st := read(currentBinding); st.Version != 2 || !st.Configured || st.Endpoint != base+"/v1/usage" {
		t.Fatalf("replacement not read back: %+v", st)
	}
	call("PUT", balancePath, strings.TrimSuffix(currentBinding, "}")+`,"version":2,"adapter":"deepseek","secret":"`+manualKey+`"}`, testutil.AdminKey, 200)
	if out := match(currentBinding, true); out.Status != "matched" || out.Applied || out.ApplyReason != "manual_configuration" {
		t.Fatalf("manual precedence: %+v", out)
	}
	if st := read(currentBinding); st.Version != 3 || st.Adapter != "deepseek" || !st.Configured {
		t.Fatalf("manual source overwritten: %+v", st)
	}
}
