package balancematch_test

import (
	"context"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/balancematch"
)

const credential = "SYNTHETIC-MATCH-KEY"

func localNetwork(s *httptest.Server) balance.NetworkOptions {
	roots := x509.NewCertPool()
	roots.AddCert(s.Certificate())
	return balance.NetworkOptions{RootCAs: roots,
		LookupIP: func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.IPv4(93, 184, 216, 34)}}, nil
		},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, s.Listener.Addr().String())
		},
	}
}

func TestFingerprintNeedsNoUpstreamRequest(t *testing.T) {
	var lookups atomic.Int32
	m := balancematch.New(nil, balancematch.Options{Network: balance.NetworkOptions{
		LookupIP: func(context.Context, string) ([]net.IPAddr, error) { lookups.Add(1); return nil, nil },
	}})
	for _, tc := range []struct{ host, adapter, path string }{
		{"API.DEEPSEEK.COM", "deepseek", "/user/balance"},
		{"api.eu.deepinfra.com", "deepinfra", "/payment/checklist"},
		{"wawapii.com", balance.OpenAICompatUsageV1, "/v1/usage"},
	} {
		out := m.Detect(context.Background(), "https://"+tc.host+"/v1", credential)
		if out.Status != "matched" || out.Adapter != tc.adapter || out.Endpoint != "https://"+strings.ToLower(tc.host)+tc.path || out.Confidence != 100 || out.Evidence == nil || out.Evidence.Source != "fingerprint" || out.Evidence.Host != strings.ToLower(tc.host) || lookups.Load() != 0 {
			t.Fatalf("wrong fingerprint for %s: %+v (lookups %d)", tc.host, out, lookups.Load())
		}
	}
	for _, host := range []string{"notdeepseek.com", "api.deepseek.com.example.com"} {
		out := m.Detect(context.Background(), "https://"+host+"/v1", "")
		if out.Status != "unmatched" || out.Reason != "credential_unavailable" || lookups.Load() != 0 {
			t.Fatalf("lookalike mistaken for known vendor: %+v", out)
		}
	}
}

func TestProbeVendorResponses(t *testing.T) {
	cases := []struct {
		name, adapter, contentType string
		shapes                     map[string]string
		wantField, reason          string
	}{
		{"DeepSeek", "deepseek", "application/json", map[string]string{"/user/balance": deepseekJSON}, "balance_infos[].currency", ""},
		{"DeepInfra", "deepinfra", "application/json", map[string]string{"/payment/checklist": deepinfraJSON}, "stripe_balance", ""},
		{"Compatible", balance.OpenAICompatUsageV1, "application/vnd.vendor+json", map[string]string{"/v1/usage": compatJSON}, "data.balance", ""},
		{"Tie", "", "application/json", map[string]string{"/user/balance": deepseekJSON, "/v1/usage": compatJSON}, "", "tied_candidates"},
		{"All fail", "", "application/json", nil, "", "no_usable_candidate"},
		{"Invalid amount", "", "application/json", map[string]string{"/v1/usage": `{"balance":"not-an-amount"}`}, "", "no_usable_candidate"},
		{"Low confidence", "", "text/plain", map[string]string{"/v1/usage": compatJSON}, "", "low_confidence"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requests := make(chan string, 16)
			s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer "+credential {
					t.Error("probe did not use GET with the bound credential")
				}
				requests <- r.URL.Path
				if body, ok := tc.shapes[r.URL.Path]; ok {
					w.Header().Set("Content-Type", tc.contentType)
					io.WriteString(w, body)
					return
				}
				w.WriteHeader(http.StatusUnauthorized)
				io.WriteString(w, credential)
			}))
			defer s.Close()
			m := balancematch.New(nil, balancematch.Options{Network: localNetwork(s), HostInterval: time.Millisecond})
			base := strings.Replace(s.URL, "127.0.0.1", "vendor.example.com", 1)
			out := m.Detect(context.Background(), base+"/v1", credential)
			if out.Adapter != tc.adapter || out.Reason != tc.reason || len(out.Candidates) != 3 {
				t.Fatalf("unexpected result: %+v", out)
			}
			wantStatus := "unmatched"
			if tc.adapter != "" {
				wantStatus = "matched"
			}
			if out.Status != wantStatus {
				t.Fatalf("status = %q, want %q", out.Status, wantStatus)
			}
			close(requests)
			var paths []string
			for p := range requests {
				paths = append(paths, p)
			}
			if got := strings.Join(paths, ","); got != "/user/balance,/payment/checklist,/v1/usage" {
				t.Fatalf("unexpected requests/retry: %s", got)
			}
			for _, c := range out.Candidates {
				probeURL := c.Evidence.URL
				if !strings.HasPrefix(probeURL, base+"/") || c.Evidence.Timestamp.IsZero() || c.Evidence.Source != "probe" || c.Evidence.Fields == nil {
					t.Fatalf("missing evidence: %+v", c.Evidence)
				}
				found := false
				for path := range tc.shapes {
					if probeURL == base+path {
						found = true
					}
				}
				wantCode := http.StatusUnauthorized
				if found {
					wantCode = http.StatusOK
				}
				if c.Evidence.Status != wantCode {
					t.Fatalf("status evidence: %+v", c.Evidence)
				}
			}
			if tc.wantField != "" && (out.Evidence == nil || !strings.Contains(strings.Join(out.Evidence.Fields, ","), tc.wantField) || out.Confidence != 100) {
				t.Fatalf("missing contract evidence: %+v", out.Evidence)
			}
		})
	}
}

func TestProbeLimitsAcrossCalls(t *testing.T) {
	const interval = 100 * time.Millisecond
	type arrival struct {
		host string
		time time.Time
	}
	var mu sync.Mutex
	var arrivals []arrival
	var active, peak atomic.Int32
	started := make(chan struct{}, 16)
	release := make(chan struct{})
	var unblock sync.Once
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		mu.Lock()
		arrivals = append(arrivals, arrival{r.Host, time.Now()})
		mu.Unlock()
		started <- struct{}{}
		<-release
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer s.Close()
	defer unblock.Do(func() { close(release) })
	m := balancematch.New(nil, balancematch.Options{Network: localNetwork(s), HostInterval: interval, MaxConcurrent: 2})
	first := strings.Replace(s.URL, "127.0.0.1", "first.example.com", 1)
	second := strings.Replace(s.URL, "127.0.0.1", "second.example.com", 1)
	done := make(chan balancematch.Result, 2)
	go func() { done <- m.Detect(t.Context(), first, credential) }()
	go func() { done <- m.Detect(t.Context(), second, credential) }()
	for range 2 {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("two different hosts did not probe concurrently")
		}
	}
	if out := m.Detect(t.Context(), "https://third.example.com", credential); out.Reason != "match_busy" {
		t.Fatalf("over-limit call was not rejected: %+v", out)
	}
	unblock.Do(func() { close(release) })
	for range 2 {
		if out := <-done; out.Status != "unmatched" || len(out.Candidates) != 3 {
			t.Fatalf("expected three 429s without retries: %+v", out)
		}
	}
	// Same normalized hostname, different port, must share the cooldown
	// even after the previous call returned. The test dialer stays local.
	alias := "https://FIRST.example.com.:12345"
	if out := m.Detect(t.Context(), alias, credential); len(out.Candidates) != 3 {
		t.Fatalf("follow-up probe: %+v", out)
	}
	if peak.Load() != 2 || len(arrivals) != 9 {
		t.Fatalf("peak concurrency %d, request count %d", peak.Load(), len(arrivals))
	}
	last := make(map[string]time.Time)
	for _, a := range arrivals {
		host, _, err := net.SplitHostPort(a.host)
		if err != nil {
			t.Fatal(err)
		}
		// Arrival times include variable TLS latency; leave a small margin.
		if prev := last[host]; !prev.IsZero() && a.time.Sub(prev) < interval-40*time.Millisecond {
			t.Fatalf("host %s probed too fast: %v", host, a.time.Sub(prev))
		}
		last[host] = a.time
	}
}

func TestProbeDeadlineDoesNotSelectIncompleteWinner(t *testing.T) {
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/user/balance" {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, deepseekJSON)
			return
		}
		<-r.Context().Done()
	}))
	defer s.Close()
	m := balancematch.New(nil, balancematch.Options{Network: localNetwork(s), HostInterval: time.Millisecond, Deadline: 250 * time.Millisecond})
	start := time.Now()
	out := m.Detect(t.Context(), s.URL, credential)
	if out.Status != "unmatched" || out.Reason != "probe_budget_exhausted" || len(out.Candidates) != 3 || time.Since(start) > 2*time.Second {
		t.Fatalf("incomplete comparison cannot match: %+v", out)
	}
}

func TestProbeRejectsPrivateTargetAndRedirects(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path == "/redirected" {
			t.Error("redirect was followed")
		}
		http.Redirect(w, r, "/redirected", http.StatusTemporaryRedirect)
	}))
	defer s.Close()
	m := balancematch.New(nil, balancematch.Options{HostInterval: time.Millisecond})
	if out := m.Detect(t.Context(), s.URL, credential); out.Status != "unmatched" || calls.Load() != 0 {
		t.Fatalf("private target reached: %+v", out)
	}
	m = balancematch.New(nil, balancematch.Options{Network: localNetwork(s), HostInterval: time.Millisecond})
	out := m.Detect(t.Context(), s.URL, credential)
	if out.Status != "unmatched" || len(out.Candidates) != 3 || calls.Load() != 3 {
		t.Fatalf("redirect not recorded as a single unsuccessful probe: %+v", out)
	}
	for _, c := range out.Candidates {
		if c.Evidence.Status != http.StatusTemporaryRedirect {
			t.Fatalf("missing redirect HTTP status: %+v", c)
		}
	}
}

const deepseekJSON = `{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"10.00","granted_balance":"1.00","topped_up_balance":"9.00"}]}`
const deepinfraJSON = `{"stripe_balance":-8.25,"recent":1.00}`
const compatJSON = `{"data":{"balance":"42.00"}}`
