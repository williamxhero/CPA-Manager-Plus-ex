package balance_test

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
)

type usageSecret string

func (s usageSecret) ResolveCredential(context.Context, string) (string, error) {
	return string(s), nil
}

func usageUpstream(t *testing.T, path string, h http.HandlerFunc) (balance.QueryTarget, balance.NetworkOptions) {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	u := strings.TrimSuffix(srv.URL, "/") + path
	return balance.QueryTarget{
			Adapter:        balance.OpenAICompatUsageV1,
			EndpointURL:    u,
			PrivateTargets: []string{"127.0.0.1"},
			CredentialRef:  "synthetic-ref",
		}, balance.NetworkOptions{
			RootCAs: roots,
			LookupIP: func(ctx context.Context, host string) ([]net.IPAddr, error) {
				return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
			},
		}
}

func TestOpenAICompatUsageParsesExactWallet(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		want   string
		status string
	}{
		{"number", `{"balance":38.94283083}`, "38.94283083", "success"},
		{"string", `{"balance":"12.5"}`, "12.5", "success"},
		{"nested", `{"data":{"balance":1.25}}`, "1.25", "success"},
		{"zero", `{"balance":0}`, "0", "success"},
		{"html", `<html>SYNTHETIC-CANARY</html>`, "", "parse_error"},
		{"dup-top", `{"balance":1,"balance":2}`, "", "parse_error"},
		{"both", `{"balance":1,"data":{"balance":2}}`, "", "parse_error"},
		{"missing", `{"ok":true}`, "", "parse_error"},
		{"nan", `{"balance":"NaN"}`, "", "parse_error"},
		{"trailing", `{"balance":1} {}`, "", "parse_error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target, network := usageUpstream(t, "/v1/usage", func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v1/usage" || r.Header.Get("Authorization") != "Bearer SYNTHETIC-CANARY" {
					t.Fatalf("bad request: %s %s %q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
				}
				_, _ = w.Write([]byte(tc.body))
			})
			got, err := balance.NewWithNetwork(network).Query(context.Background(), target, usageSecret("SYNTHETIC-CANARY"))
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(got)
			if strings.Contains(string(raw), "SYNTHETIC-CANARY") {
				t.Fatal("secret leaked")
			}
			if got.QueryStatus != tc.status {
				t.Fatalf("status=%s want=%s got=%+v", got.QueryStatus, tc.status, got)
			}
			if tc.status == "success" {
				if got.AmountDecimal != tc.want || got.Currency != "USD" || got.Semantic != "account_wallet" {
					t.Fatalf("wallet mismatch: %+v want %s", got, tc.want)
				}
			} else if got.AmountDecimal != "" {
				t.Fatalf("parse failure still set amount: %+v", got)
			}
		})
	}
}

func TestOpenAICompatUsageRejectsWrongPathAndAuth(t *testing.T) {
	target, network := usageUpstream(t, "/v1/usage", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":"SYNTHETIC-CANARY"}`))
	})
	got, err := balance.NewWithNetwork(network).Query(context.Background(), target, usageSecret("SYNTHETIC-CANARY"))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(got)
	if got.QueryStatus != "auth_error" || strings.Contains(string(raw), "SYNTHETIC-CANARY") {
		t.Fatalf("auth failure mishandled: %+v raw=%s", got, raw)
	}
	if balance.ValidateAdapterSource(balance.OpenAICompatUsageV1, "https://wawapii.com/v1/models", nil) == nil {
		t.Fatal("wrong path accepted")
	}
	if balance.ValidateAdapterSource(balance.OpenAICompatUsageV1, "https://wawapii.com/v1/usage", nil) != nil {
		t.Fatal("valid usage endpoint rejected")
	}
}

func TestOpenAICompatUsageCapabilityListed(t *testing.T) {
	caps, err := balance.New().ListCapabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range caps {
		if c.Adapter == balance.OpenAICompatUsageV1 {
			found = true
			if !c.Supported || len(c.ConfigFields) == 0 {
				t.Fatalf("capability incomplete: %+v", c)
			}
		}
	}
	if !found {
		t.Fatal("openai-compat-usage-v1 missing from capabilities")
	}
}
