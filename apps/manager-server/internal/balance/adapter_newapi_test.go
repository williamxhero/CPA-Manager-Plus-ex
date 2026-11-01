package balance_test

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
)

type newAPISecret string

func (s newAPISecret) ResolveCredential(context.Context, string) (string, error) {
	return string(s), nil
}

func TestNewAPIUnprovenMoneyConfigurationDoesNotQuery(t *testing.T) {
	var calls atomic.Int32
	target, network := newAPIUpstream(t, "/api/user/self", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":42,"quota":0}}`))
	})
	target.Adapter = balance.NewAPIUserSelfV1
	target.Currency = "USD"
	target.UnitMultiplier = "0.000002"
	got := balance.QueryNewAPI(context.Background(), target, balance.NewAPIConfig{ProtocolConfirmed: true, InstanceVersion: "fixture", Auth: balance.NewAPIUserPATWithIDV1, UserID: "42"}, newAPISecret("SYNTHETIC-PAT"), network)
	if got.QueryStatus != "needs_configuration" || calls.Load() != 0 || got.NativeQuota != nil {
		t.Fatalf("unproven money config was treated as ready: %+v calls=%d", got, calls.Load())
	}
}

func TestNewAPIUnknownProtocolIsUnsupported(t *testing.T) {
	got := balance.QueryNewAPI(context.Background(), balance.QueryTarget{Adapter: "oneapi-unconfirmed"}, balance.NewAPIConfig{ProtocolConfirmed: true, InstanceVersion: "fixture"}, nil, balance.NetworkOptions{})
	if got.QueryStatus != "unsupported" || got.CapabilityStatus != "unsupported" || got.NativeQuota != nil {
		t.Fatalf("unknown protocol advertised incorrectly: %+v", got)
	}
}

func TestNewAPIUpstreamFailuresAreSafe(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		body, want string
	}{
		{"unauthorized", 401, `SYNTHETIC-CANARY`, "auth_error"},
		{"forbidden", 403, `SYNTHETIC-CANARY`, "auth_error"},
		{"limited", 429, `SYNTHETIC-CANARY`, "rate_limited"},
		{"server-error", 500, `SYNTHETIC-CANARY`, "network_error"},
		{"200-error", 200, `{"success":false,"message":"SYNTHETIC-CANARY"}`, "parse_error"},
		{"oversize", 200, strings.Repeat("x", (2<<20)+1), "parse_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target, network := newAPIUpstream(t, "/api/user/self", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			target.Adapter = balance.NewAPIUserSelfV1
			got := balance.QueryNewAPI(context.Background(), target, balance.NewAPIConfig{ProtocolConfirmed: true, InstanceVersion: "fixture", Auth: balance.NewAPIUserPATWithIDV1, UserID: "42"}, newAPISecret("SYNTHETIC-CANARY"), network)
			raw, _ := json.Marshal(got)
			if got.QueryStatus != tc.want || got.NativeQuota != nil || got.AmountDecimal != "" || strings.Contains(string(raw), "SYNTHETIC-CANARY") {
				t.Fatalf("unsafe failed query: %+v", got)
			}
		})
	}
}

func TestNewAPIReadDeadlineIsBounded(t *testing.T) {
	target, network := newAPIUpstream(t, "/api/user/self", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	target.Adapter = balance.NewAPIUserSelfV1
	target.Timeout = 100 * time.Millisecond
	got := balance.QueryNewAPI(context.Background(), target, balance.NewAPIConfig{ProtocolConfirmed: true, InstanceVersion: "fixture", Auth: balance.NewAPIUserPATWithIDV1, UserID: "42"}, newAPISecret("SYNTHETIC-KEY"), network)
	if got.QueryStatus != "network_error" || got.NativeQuota != nil {
		t.Fatalf("deadline accepted a reading: %+v", got)
	}
}

func TestNewAPIRequiresExplicitContractBeforeNetwork(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*balance.QueryTarget, *balance.NewAPIConfig)
	}{
		{"no-attestation", func(_ *balance.QueryTarget, c *balance.NewAPIConfig) { c.ProtocolConfirmed = false }},
		{"no-version", func(_ *balance.QueryTarget, c *balance.NewAPIConfig) { c.InstanceVersion = "" }},
		{"no-account", func(q *balance.QueryTarget, _ *balance.NewAPIConfig) { q.AccountScope = "" }},
		{"no-user-id", func(_ *balance.QueryTarget, c *balance.NewAPIConfig) { c.UserID = "" }},
		{"wrong-auth-kind", func(_ *balance.QueryTarget, c *balance.NewAPIConfig) { c.Auth = balance.NewAPIInferenceKeyReadOnlyV1 }},
		{"user-id-header-injection", func(_ *balance.QueryTarget, c *balance.NewAPIConfig) { c.UserID = "42\r\nX-Foo: bad" }},
		{"http", func(q *balance.QueryTarget, _ *balance.NewAPIConfig) {
			q.EndpointURL = strings.Replace(q.EndpointURL, "https:", "http:", 1)
		}},
		{"wrong-path", func(q *balance.QueryTarget, _ *balance.NewAPIConfig) { q.EndpointURL += "/" }},
		{"escaped-path", func(q *balance.QueryTarget, _ *balance.NewAPIConfig) {
			q.EndpointURL = strings.Replace(q.EndpointURL, "/api/", "/%61pi/", 1)
		}},
		{"query", func(q *balance.QueryTarget, _ *balance.NewAPIConfig) { q.EndpointURL += "?key=SYNTHETIC-CANARY" }},
		{"empty-query", func(q *balance.QueryTarget, _ *balance.NewAPIConfig) { q.EndpointURL += "?" }},
		{"fragment", func(q *balance.QueryTarget, _ *balance.NewAPIConfig) { q.EndpointURL += "#" }},
		{"metadata-permit", func(q *balance.QueryTarget, _ *balance.NewAPIConfig) { q.PrivateTargets = []string{"169.254.169.254"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			target, network := newAPIUpstream(t, "/api/user/self", func(w http.ResponseWriter, r *http.Request) { calls.Add(1) })
			target.Adapter = balance.NewAPIUserSelfV1
			c := balance.NewAPIConfig{ProtocolConfirmed: true, InstanceVersion: "fixture", Auth: balance.NewAPIUserPATWithIDV1, UserID: "42"}
			tc.edit(&target, &c)
			got := balance.QueryNewAPI(context.Background(), target, c, newAPISecret("SYNTHETIC-CANARY"), network)
			if got.QueryStatus != "needs_configuration" || calls.Load() != 0 {
				t.Fatalf("unconfirmed request sent: %+v calls=%d", got, calls.Load())
			}
		})
	}
}

func TestNewAPIReusesSafeTransport(t *testing.T) {
	for _, name := range []string{"untrusted-tls", "private-without-permit", "mixed-dns", "metadata", "numeric-pinning", "redirect"} {
		t.Run(name, func(t *testing.T) {
			var calls, dials atomic.Int32
			target, network := newAPIUpstream(t, "/api/user/self", func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if name == "redirect" {
					w.Header().Set("Location", "https://metadata.google.internal/api/user/self")
					w.WriteHeader(302)
					return
				}
				_, _ = w.Write([]byte(`{"success":true,"data":{"id":42,"quota":7}}`))
			})
			target.Adapter = balance.NewAPIUserSelfV1
			network.DialContext = func(ctx context.Context, kind, address string) (net.Conn, error) {
				dials.Add(1)
				host, _, _ := net.SplitHostPort(address)
				if host != "127.0.0.1" {
					t.Error("dialer did not receive pinned numeric address")
					return nil, context.Canceled
				}
				return (&net.Dialer{}).DialContext(ctx, kind, address)
			}
			switch name {
			case "untrusted-tls":
				network.RootCAs = x509.NewCertPool()
			case "private-without-permit":
				target.PrivateTargets = nil
			case "mixed-dns":
				network.LookupIP = func(context.Context, string) ([]net.IPAddr, error) {
					return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}, {IP: net.ParseIP("169.254.169.254")}}, nil
				}
			case "metadata":
				network.LookupIP = func(context.Context, string) ([]net.IPAddr, error) {
					return []net.IPAddr{{IP: net.ParseIP("168.63.129.16")}}, nil
				}
			}
			got := balance.QueryNewAPI(context.Background(), target, balance.NewAPIConfig{ProtocolConfirmed: true, InstanceVersion: "fixture", Auth: balance.NewAPIUserPATWithIDV1, UserID: "42"}, newAPISecret("SYNTHETIC-CANARY"), network)
			switch name {
			case "numeric-pinning":
				if got.QueryStatus != "success" || calls.Load() != 1 || dials.Load() != 1 {
					t.Fatalf("pinned request failed: %+v", got)
				}
			case "redirect":
				if got.QueryStatus != "network_error" || calls.Load() != 1 || dials.Load() != 1 {
					t.Fatalf("redirect escaped: %+v", got)
				}
			case "untrusted-tls":
				if got.QueryStatus != "network_error" || calls.Load() != 0 {
					t.Fatalf("invalid TLS trusted: %+v", got)
				}
			default:
				if got.QueryStatus != "network_error" || calls.Load() != 0 || dials.Load() != 0 {
					t.Fatalf("unsafe destination reached dial/upstream: %+v", got)
				}
			}
			raw, _ := json.Marshal(got)
			if strings.Contains(string(raw), "SYNTHETIC-CANARY") {
				t.Fatal("transport error exposed secret")
			}
		})
	}
}

func TestNewAPISelfRejectsAmbiguousQuotaJSON(t *testing.T) {
	for _, body := range []string{
		`{"success":true,"success":false,"data":{"id":42,"quota":1}}`,
		`{"success":true,"data":{"id":42,"quota":1,"quota":2}}`,
		`{"success":true,"data":{"id":42,"quota":1,"Quota":2}}`,
		`{"success":true,"data":{"id":42,"quota":null}}`,
		`{"success":true,"data":{"id":42}}`,
		`{"success":true,"data":{"id":41,"quota":1}}`,
		`{"success":true,"data":{"id":42,"quota":1e2}}`,
		`{"success":true,"data":{"id":42,"quota":"NaN"}}`,
		`{"success":true,"data":{"id":42,"quota":"Infinity"}}`,
		`{"success":true,"data":{"id":42,"quota":1.1}}`,
		`{"success":true,"data":{"id":42,"quota":"` + strings.Repeat("9", 121) + `"}}`,
		`{"success":true,"error":{"message":"SYNTHETIC-CANARY"},"data":{"id":42,"quota":1}}`,
		`{"success":false,"message":"SYNTHETIC-CANARY","data":{"id":42,"quota":1}}`,
		`<html>SYNTHETIC-CANARY</html>`,
		`{"success":true,"data":{"id":42,"quota":1}} {}`,
	} {
		t.Run("invalid-"+string(rune('A'+len(body)%26)), func(t *testing.T) {
			target, network := newAPIUpstream(t, "/api/user/self", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) })
			target.Adapter = balance.NewAPIUserSelfV1
			got := balance.QueryNewAPI(context.Background(), target, balance.NewAPIConfig{ProtocolConfirmed: true, InstanceVersion: "fixture", Auth: balance.NewAPIUserPATWithIDV1, UserID: "42"}, newAPISecret("SYNTHETIC-CANARY"), network)
			raw, _ := json.Marshal(got)
			if got.QueryStatus != "parse_error" || got.NativeQuota != nil || got.AmountDecimal != "" || strings.Contains(string(raw), "SYNTHETIC-CANARY") {
				t.Fatalf("ambiguous/invalid response accepted or leaked: %+v", got)
			}
		})
	}
}

func newAPIUpstream(t *testing.T, path string, handler http.HandlerFunc) (balance.QueryTarget, balance.NetworkOptions) {
	t.Helper()
	s := httptest.NewTLSServer(handler)
	t.Cleanup(s.Close)
	pool := x509.NewCertPool()
	pool.AddCert(s.Certificate())
	return balance.QueryTarget{EndpointURL: s.URL + path, PrivateTargets: []string{"127.0.0.1"}, CredentialRef: "synthetic-ref", AccountScope: "confirmed-account-A"}, balance.NetworkOptions{
		RootCAs: pool,
		LookupIP: func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
		},
	}
}

func TestNewAPIConfirmedSelfPreservesNativeDebt(t *testing.T) {
	target, network := newAPIUpstream(t, "/api/user/self", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/user/self" || r.Header.Get("Authorization") != "Bearer SYNTHETIC-PAT" || r.Header.Get("New-Api-User") != "42" || r.Header.Get("Cookie") != "" {
			t.Error("wrong explicit read-only request")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":42,"quota":-9007199254740993,"used_quota":123}}`))
	})
	target.Adapter = balance.NewAPIUserSelfV1
	config := balance.NewAPIConfig{ProtocolConfirmed: true, InstanceVersion: "operator-confirmed-fixture-revision", Auth: balance.NewAPIUserPATWithIDV1, UserID: "42"}
	got := balance.QueryNewAPI(context.Background(), target, config, newAPISecret("SYNTHETIC-PAT"), network)
	if got.QueryStatus != "success" || got.Semantic != "account_wallet" || got.NativeQuota == nil || got.NativeQuota.Remaining == nil || *got.NativeQuota.Remaining != "-9007199254740993" || got.NativeQuota.State != "finite" {
		t.Fatalf("native account debt lost: %+v", got)
	}
	if got.Currency != "" || got.AmountDecimal != "" || len(got.Balances) != 0 {
		t.Fatal("native quota must not invent money or currency")
	}
	raw, _ := json.Marshal(got)
	if !json.Valid(raw) {
		t.Fatal("invalid result JSON")
	}
}

func TestNewAPIConfirmedPATOnlyVariantStillChecksAccount(t *testing.T) {
	target, network := newAPIUpstream(t, "/api/user/self", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("New-Api-User") != "" || r.Header.Get("Authorization") != "Bearer SYNTHETIC-PAT" {
			t.Error("PAT-only variant used a different auth contract")
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":"42","quota":"12"}}`))
	})
	target.Adapter = balance.NewAPIUserSelfV1
	got := balance.QueryNewAPI(context.Background(), target, balance.NewAPIConfig{ProtocolConfirmed: true, InstanceVersion: "operator-confirmed-pat-only-fixture", Auth: "user-pat-v1", UserID: "42"}, newAPISecret("SYNTHETIC-PAT"), network)
	if got.QueryStatus != "success" || got.NativeQuota == nil || got.NativeQuota.Remaining == nil || *got.NativeQuota.Remaining != "12" {
		t.Fatalf("explicit PAT-only instance failed: %+v", got)
	}
}

func TestNewAPITokenQuotaIsNotAccountWallet(t *testing.T) {
	target, network := newAPIUpstream(t, "/api/usage/token/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/usage/token/" || r.Header.Get("Authorization") != "Bearer SYNTHETIC-KEY" || r.Header.Get("New-Api-User") != "" {
			t.Error("wrong token read-only request")
		}
		_, _ = w.Write([]byte(`{"code":true,"data":{"object":"token_usage","total_available":"9007199254740993123456789","total_used":1,"total_granted":"9007199254740993123456790","unlimited_quota":false,"user_quota":{"total_available":123456}}}`))
	})
	target.Adapter = "newapi-token-usage-v1"
	target.AccountScope = ""
	c := balance.NewAPIConfig{ProtocolConfirmed: true, InstanceVersion: "fixture-pinned", Auth: "inference-key-readonly-v1"}
	got := balance.QueryNewAPI(context.Background(), target, c, newAPISecret("SYNTHETIC-KEY"), network)
	if got.QueryStatus != "success" || got.Semantic != "key_remaining" || got.NativeQuota == nil || got.NativeQuota.Remaining == nil || *got.NativeQuota.Remaining != "9007199254740993123456789" || got.NativeQuota.Unlimited == nil || *got.NativeQuota.Unlimited {
		t.Fatalf("wrong key quota: %+v", got)
	}
	if got.AmountDecimal != "" || got.Currency != "" || len(got.Balances) != 0 {
		t.Fatal("key or optional user_quota invented a money wallet")
	}
}

func TestNewAPITokenNonfiniteAllowanceNeverBecomesZero(t *testing.T) {
	for _, tc := range []struct{ name, fields, state string }{
		{"unlimited-placeholder", `"unlimited_quota":true,"total_available":100000000`, "unlimited"},
		{"unlimited-no-counter", `"unlimited_quota":true`, "unlimited"},
		{"missing-unlimited", `"total_available":0`, "unknown"},
		{"null-unlimited", `"unlimited_quota":null,"total_available":0`, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target, network := newAPIUpstream(t, "/api/usage/token/", func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"code":true,"data":{"object":"token_usage",` + tc.fields + `}}`))
			})
			target.Adapter = balance.NewAPITokenUsageV1
			got := balance.QueryNewAPI(context.Background(), target, balance.NewAPIConfig{ProtocolConfirmed: true, InstanceVersion: "fixture", Auth: balance.NewAPIInferenceKeyReadOnlyV1}, newAPISecret("SYNTHETIC-KEY"), network)
			if got.QueryStatus != "success" || got.NativeQuota == nil || got.NativeQuota.State != tc.state || got.NativeQuota.Remaining != nil || got.AmountDecimal != "" {
				t.Fatalf("unknown/unlimited became finite: %+v", got)
			}
		})
	}
}
