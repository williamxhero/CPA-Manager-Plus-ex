package bss

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
	"time"
)

// These deterministic strings are synthetic test fixtures, not credentials or config.
func fixtureOptions(endpoint string) Options {
	return Options{AccessKeyID: "fixture-id* /中", AccessKeySecret: "fixture-secret* /中", Endpoint: endpoint, AllowHTTP: true}
}

func mockClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewClient(fixtureOptions(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestClientDefaultsAndAbsentCredentials(t *testing.T) {
	client, err := NewClient(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if client.endpoint.String() != DefaultEndpoint || client.http.Timeout != DefaultTimeout {
		t.Fatalf("%+v", client)
	}
	if _, err := client.Balance(context.Background()); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatal(err)
	}
	if _, err := client.Bill(context.Background(), "2026-10"); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatal(err)
	}
	if _, err := client.Packages(context.Background()); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatal(err)
	}
}

func TestClientTimeoutClamping(t *testing.T) {
	for _, tc := range []struct{ input, want time.Duration }{{0, DefaultTimeout}, {-1, DefaultTimeout}, {time.Nanosecond, time.Second}, {5 * time.Second, 5 * time.Second}, {10 * time.Minute, MaxTimeout}} {
		client, err := NewClient(Options{Timeout: tc.input})
		if err != nil || client.http.Timeout != tc.want {
			t.Fatal(client, err)
		}
	}
}

func TestClientEndpointValidation(t *testing.T) {
	for _, endpoint := range []string{"http://example.com", "http://127.0.0.1.evil.example", "ftp://127.0.0.1", "https://user:pass@example.com", "https://example.com/?q=1", "https://example.com/?", "https://example.com/#fragment", "https://example.com/#", "relative", "://", "https:///"} {
		options := fixtureOptions(endpoint)
		if _, err := NewClient(options); err == nil {
			t.Fatalf("accepted %s", endpoint)
		}
	}
	options := fixtureOptions("http://127.0.0.1:1234")
	options.AllowHTTP = false
	if _, err := NewClient(options); err == nil {
		t.Fatal("HTTP accepted without explicit allowance")
	}
}

func TestClientAllowsOnlyLoopbackHTTPMocks(t *testing.T) {
	for _, endpoint := range []string{"http://127.0.0.1:1234", "http://[::1]:1234", "http://localhost:1234"} {
		if _, err := NewClient(fixtureOptions(endpoint)); err != nil {
			t.Fatal(endpoint, err)
		}
	}
}

func TestClientSignedGETURL(t *testing.T) {
	client := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Errorf("unexpected method/path %s %s", r.Method, r.URL.Path)
		}
		query := r.URL.Query()
		for key, value := range map[string]string{"Action": "QueryAccountBalance", "AccessKeyId": fixtureOptions("").AccessKeyID, "Version": "2017-12-14", "Format": "JSON", "SignatureMethod": "HMAC-SHA1", "SignatureVersion": "1.0", "RegionId": "cn-beijing"} {
			if query.Get(key) != value {
				t.Errorf("%s=%s", key, query.Get(key))
			}
		}
		if _, err := time.Parse("2006-01-02T15:04:05Z", query.Get("Timestamp")); err != nil {
			t.Error(err)
		}
		if len(query.Get("SignatureNonce")) != 32 || query.Get("SignatureNonce")[12] != '4' {
			t.Error("not UUID v4")
		}
		params := make(map[string]string)
		for key, values := range query {
			if len(values) != 1 {
				t.Error("duplicate query")
			}
			params[key] = values[0]
		}
		if query.Get("Signature") != Sign(params, fixtureOptions("").AccessKeySecret) {
			t.Error("invalid signature")
		}
		if strings.Contains(r.URL.RawQuery, "+") || !strings.Contains(r.URL.RawQuery, "%2A%20%2F%E4%B8%AD") {
			t.Error("not RFC3986 query")
		}
		_, _ = w.Write(envelope(`{"AvailableAmount":"12.5"}`))
	})
	client.options.Region = "cn-beijing"
	result, err := client.Balance(context.Background())
	if err != nil || *result.AvailableAmount != 12.5 {
		t.Fatal(result, err)
	}
}

func TestClientNoncesUnique(t *testing.T) {
	seen := make(map[string]bool)
	client := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
		nonce := r.URL.Query().Get("SignatureNonce")
		if seen[nonce] {
			t.Error("reused nonce")
		}
		seen[nonce] = true
		_, _ = w.Write(envelope(`{"AvailableAmount":1}`))
	})
	for i := 0; i < 3; i++ {
		if _, err := client.Balance(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestClientBillRequestAndParser(t *testing.T) {
	client := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
		for key, want := range map[string]string{"Action": "QueryAccountBill", "BillingCycle": "2026-10", "Granularity": "MONTHLY", "IsGroupByProduct": "true"} {
			if r.URL.Query().Get(key) != want {
				t.Error(key)
			}
		}
		_, _ = w.Write(envelope(`{"Items":{"Item":[{"PretaxAmount":2}]}}`))
	})
	result, err := client.Bill(context.Background(), "2026-10")
	if err != nil || *result.Amount != 2 {
		t.Fatal(result, err)
	}
}

func TestClientBillCycleRejectedWithoutNetwork(t *testing.T) {
	client, _ := NewClient(Options{})
	for _, value := range []string{"", "2026-13", "2026-1", "26-01", "2026-10-01"} {
		if _, err := client.Bill(context.Background(), value); err == nil || !strings.Contains(err.Error(), "YYYY-MM") {
			t.Fatal(value, err)
		}
	}
}

func TestClientPackagesRequestAndParser(t *testing.T) {
	client := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("Action") != "QueryResourcePackageInstances" {
			t.Error("wrong action")
		}
		_, _ = w.Write(envelope(`{"Instances":{"Instance":[{"ProductCode":"bailian","PackageName":"tokens","RemainingAmount":1,"TotalAmount":2}]}}`))
	})
	result, err := client.Packages(context.Background())
	if err != nil || len(result) != 1 || result[0].RemainingFraction != .5 {
		t.Fatal(result, err)
	}
}

func TestClientHTTPErrorCodeAndMessage(t *testing.T) {
	client := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"Code":"InvalidAccessKeyId.NotFound","Message":"not found","Success":false}`))
	})
	_, err := client.Balance(context.Background())
	if err == nil || !strings.Contains(err.Error(), "HTTP 403") || !strings.Contains(err.Error(), "InvalidAccessKeyId.NotFound") || !strings.Contains(err.Error(), "not found") {
		t.Fatal(err)
	}
}

func TestClientEchoedQueryRedacted(t *testing.T) {
	for _, status := range []int{200, 403} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			client := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				_ = json.NewEncoder(w).Encode(map[string]any{"Code": "Denied", "Success": false, "Message": r.URL.RawQuery})
			})
			_, err := client.Balance(context.Background())
			if err == nil || strings.Contains(err.Error(), "fixture") || strings.Contains(err.Error(), "Signature=") || strings.Contains(err.Error(), "AccessKeyId=") {
				t.Fatal(err)
			}
		})
	}
}

func TestClientSecretEchoRedactedRawAndEncoded(t *testing.T) {
	for _, encode := range []func(string) string{func(s string) string { return s }, PercentEncode, url.QueryEscape} {
		for _, field := range []string{"Message", "Code"} {
			client := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
				errorResponse := map[string]any{"Success": false, "Code": "Denied", "Message": "error"}
				errorResponse[field] = encode(fixtureOptions("").AccessKeySecret)
				_ = json.NewEncoder(w).Encode(errorResponse)
			})
			_, err := client.Balance(context.Background())
			if err == nil || strings.Contains(err.Error(), "fixture") {
				t.Fatal(err)
			}
		}
	}
}

func TestClientInvalidAndOversizedResponses(t *testing.T) {
	for _, body := range []string{"not json", strings.Repeat("x", MaxResponseBytes+1), string(envelope(`null`))} {
		client := mockClient(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) })
		if _, err := client.Balance(context.Background()); err == nil {
			t.Fatal("bad response accepted")
		}
	}
}

func TestClientRedirectNeverForwardsCredentials(t *testing.T) {
	var forwarded atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded.Add(1) }))
	defer target.Close()
	client := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.RequestURI(), http.StatusFound)
	})
	if _, err := client.Balance(context.Background()); err == nil {
		t.Fatal("redirect accepted")
	}
	if forwarded.Load() != 0 {
		t.Fatal("forwarded signed query")
	}
}

func TestClientContextCancellation(t *testing.T) {
	client := mockClient(t, func(w http.ResponseWriter, r *http.Request) { t.Error("canceled request sent") })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.Balance(ctx)
	if err == nil || !strings.Contains(err.Error(), "context canceled") || strings.Contains(err.Error(), "fixture") {
		t.Fatal(err)
	}
}

func TestClientRequestTimeout(t *testing.T) {
	client := mockClient(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	client.http.Timeout = time.Second
	_, err := client.Balance(context.Background())
	if err == nil || !strings.Contains(err.Error(), "timeout") || strings.Contains(err.Error(), "fixture") {
		t.Fatal(err)
	}
}

func TestClientContextDeadline(t *testing.T) {
	client := mockClient(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := client.Balance(ctx)
	if err == nil || !strings.Contains(err.Error(), "deadline exceeded") || strings.Contains(err.Error(), "fixture") {
		t.Fatal(err)
	}
}

func TestClientNetworkErrorDoesNotExposeURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	endpoint := server.URL
	server.Close()
	client, err := NewClient(fixtureOptions(endpoint))
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Balance(context.Background())
	if err == nil || strings.Contains(err.Error(), "fixture") || strings.Contains(err.Error(), "http://") {
		t.Fatal(err)
	}
}
