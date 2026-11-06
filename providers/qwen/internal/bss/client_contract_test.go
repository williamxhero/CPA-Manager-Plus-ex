package bss_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"cpa-credential-qwen/internal/bss"
)

func fixtureClient(t *testing.T, handler http.HandlerFunc) *bss.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := bss.NewClient(optionsFor(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestClientBalanceRequiredWithoutImplicitOptionalQueries(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"zero-balance", string(success(`{"AvailableAmount":0}`)), true},
		{"missing-balance", string(success(`{}`)), false},
		{"null-balance", string(success(`{"AvailableAmount":null}`)), false},
		{"denied-balance", `{"Code":"Denied","Success":false,"Message":"not allowed"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			client := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Query().Get("Action") != "QueryAccountBalance" {
					t.Error("balance triggered an implicit optional operation")
				}
				_, _ = w.Write([]byte(tc.body))
			})
			got, err := client.Balance(context.Background())
			if (err == nil) != tc.valid || calls.Load() != 1 {
				t.Fatalf("required balance mismatch: calls=%d err=%v", calls.Load(), err)
			}
			if tc.valid && (got.AvailableAmount == nil || *got.AvailableAmount != 0) {
				t.Fatal("real zero balance lost")
			}
		})
	}
}

func TestOptionalBillAndPackagesFailuresDoNotPoisonBalance(t *testing.T) {
	client := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("Action") == "QueryAccountBalance" {
			_, _ = w.Write(success(`{"AvailableAmount":12.5,"Currency":"CNY"}`))
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"Code":"Denied","Success":false,"Message":"optional scope unavailable"}`))
	})
	before, err := client.Balance(context.Background())
	if err != nil || before.AvailableAmount == nil || *before.AvailableAmount != 12.5 {
		t.Fatalf("balance unavailable: %+v, %v", before, err)
	}
	if bill, err := client.Bill(context.Background(), "2026-11"); err == nil || bill.Amount != nil {
		t.Fatalf("optional bill failure became zero/success: %+v, %v", bill, err)
	}
	if packages, err := client.Packages(context.Background()); err == nil || len(packages) != 0 {
		t.Fatalf("optional package failure became success: %+v, %v", packages, err)
	}
	after, err := client.Balance(context.Background())
	if err != nil || after.AvailableAmount == nil || *after.AvailableAmount != 12.5 || *before.AvailableAmount != 12.5 {
		t.Fatalf("optional errors poisoned required balance: %+v, %v", after, err)
	}
}

func TestClientEveryRedirectStatusStopsSignedRequests(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308} {
		for _, sameOrigin := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/same-origin=%t", status, sameOrigin), func(t *testing.T) {
				var forwarded, calls atomic.Int32
				target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { forwarded.Add(1) }))
				defer target.Close()
				client := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					location := target.URL + r.URL.RequestURI()
					if sameOrigin {
						location = "/redirected?" + r.URL.RawQuery
					}
					w.Header().Set("Location", location)
					w.WriteHeader(status)
				})
				if _, err := client.Balance(context.Background()); err == nil {
					t.Fatal("redirect accepted")
				}
				if forwarded.Load() != 0 || calls.Load() != 1 {
					t.Fatal("signed request followed a redirect")
				}
			})
		}
	}
}

func TestClientResponseLimitAllOperations(t *testing.T) {
	for _, operation := range []struct {
		name, data string
		call       func(*bss.Client) error
	}{
		{"balance", `{"AvailableAmount":0}`, func(c *bss.Client) error { _, err := c.Balance(context.Background()); return err }},
		{"bill", `{"Items":[]}`, func(c *bss.Client) error { _, err := c.Bill(context.Background(), "2026-11"); return err }},
		{"packages", `{"Instances":[]}`, func(c *bss.Client) error { _, err := c.Packages(context.Background()); return err }},
	} {
		for _, delta := range []int{-1, 0, 1} {
			t.Run(fmt.Sprintf("%s/limit%+d", operation.name, delta), func(t *testing.T) {
				body := string(success(operation.data))
				body += strings.Repeat(" ", bss.MaxResponseBytes+delta-len(body))
				client := fixtureClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) })
				err := operation.call(client)
				if delta <= 0 && err != nil || delta > 0 && (err == nil || !strings.Contains(err.Error(), "size limit")) {
					t.Fatalf("response limit mismatch: %v", err)
				}
			})
		}
	}
}

func TestClientErrorsRedactCredentialsWithAlternateEncodings(t *testing.T) {
	encoders := []struct {
		name string
		fn   func(string) string
	}{
		{"raw", func(s string) string { return s }},
		{"percent", bss.PercentEncode},
		{"form", url.QueryEscape},
		{"lower-percent-hex", func(s string) string {
			s = bss.PercentEncode(s)
			for i := 0; i+2 < len(s); i++ {
				if s[i] == '%' {
					s = s[:i+1] + strings.ToLower(s[i+1:i+3]) + s[i+3:]
					i += 2
				}
			}
			return s
		}},
		{"double-percent", func(s string) string { return bss.PercentEncode(bss.PercentEncode(s)) }},
	}
	for _, encoding := range encoders {
		for _, status := range []int{200, 403} {
			for _, value := range []string{optionsFor("").AccessKeyID, optionsFor("").AccessKeySecret} {
				t.Run(fmt.Sprintf("%s/%d/%s", encoding.name, status, strings.Split(value, "*")[0]), func(t *testing.T) {
					client := fixtureClient(t, func(w http.ResponseWriter, _ *http.Request) {
						w.WriteHeader(status)
						_ = json.NewEncoder(w).Encode(map[string]any{"Success": false, "Code": "Denied", "Message": encoding.fn(value)})
					})
					for _, call := range []func() error{
						func() error { _, err := client.Balance(context.Background()); return err },
						func() error { _, err := client.Bill(context.Background(), "2026-11"); return err },
						func() error { _, err := client.Packages(context.Background()); return err },
					} {
						if err := call(); err == nil || strings.Contains(err.Error(), "fixture") {
							t.Fatalf("credential echo escaped redaction: %v", err)
						}
					}
				})
			}
		}
	}
}
