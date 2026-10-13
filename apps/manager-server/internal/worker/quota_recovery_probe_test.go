package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestQuotaProbeWindowsFromQuotaFetchResponse(t *testing.T) {
	body := []byte(`{
		"groups": [
			{"buckets": [
				{"window": "five_hour", "remainingFraction": 0.4},
				{"window": "weekly", "remainingFraction": 0}
			]},
			{"buckets": [
				{"window": "monthly", "remaining_fraction": 1}
			]}
		]
	}`)
	windows := windowsFromQuotaFetchResponse(body)
	if len(windows) != 3 {
		t.Fatalf("windows = %#v, want 3", windows)
	}
	byKind := map[string]bool{}
	for _, w := range windows {
		byKind[w.WindowKind] = w.Exhausted
	}
	if byKind["five_hour"] {
		t.Fatalf("five_hour should not be exhausted: %#v", windows)
	}
	if !byKind["weekly"] {
		t.Fatalf("weekly should be exhausted: %#v", windows)
	}
	if byKind["monthly"] {
		t.Fatalf("monthly should not be exhausted: %#v", windows)
	}
}

func TestQuotaProbeIgnoresWindowsWithoutUsageEvidence(t *testing.T) {
	body := []byte(`{"groups":[{"buckets":[{"window":"weekly"}]}]}`)
	if windows := windowsFromQuotaFetchResponse(body); len(windows) != 0 {
		t.Fatalf("windows = %#v, want none without usage evidence", windows)
	}
}

func TestQuotaProbeWindowsFromProviderUsageBody(t *testing.T) {
	body := []byte(`{
		"rate_limit": {
			"primary_window": {"used_percent": 100, "limit_window_seconds": 18000},
			"secondary_window": {"used_percent": 12.5, "limit_window_seconds": 604800}
		}
	}`)
	windows := windowsFromProviderUsageBody(body)
	if len(windows) != 2 {
		t.Fatalf("windows = %#v, want 2", windows)
	}
	exhausted := map[string]bool{}
	for _, w := range windows {
		exhausted[w.WindowKind] = w.Exhausted
	}
	if !exhausted["five_hour"] {
		t.Fatalf("primary_window should map to an exhausted five_hour: %#v", windows)
	}
	if exhausted["weekly"] {
		t.Fatalf("secondary_window should map to a recovered weekly: %#v", windows)
	}
}

func TestQuotaProbeFallsBackToProviderUsageWhenQuotaFetchUnavailable(t *testing.T) {
	var apiCallAuthIndex string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case quotaFetchManagementPath:
			http.Error(w, `{"error":"no quota provider available for credential"}`, http.StatusNotImplemented)
		case apiCallManagementPath:
			var payload struct {
				AuthIndex string `json:"authIndex"`
				URL       string `json:"url"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			apiCallAuthIndex = payload.AuthIndex
			if payload.URL != codexUsageProbeURL {
				t.Errorf("api-call url = %q, want codex usage url", payload.URL)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status_code": 200,
				"body":        `{"rate_limit":{"primary_window":{"used_percent":10,"limit_window_seconds":18000}}}`,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	probe := newHTTPQuotaRecoveryProbe()
	res, err := probe.ProbeQuotaRecovery(context.Background(), server.URL, "mgmt", "codex", "auth-1", "workspace-1")
	if err != nil {
		t.Fatalf("probe error: %v", err)
	}
	if apiCallAuthIndex != "auth-1" {
		t.Fatalf("api-call auth index = %q, want auth-1", apiCallAuthIndex)
	}
	if !res.Available || len(res.Windows) != 1 || res.AnyExhausted() {
		t.Fatalf("probe result = %#v, want one available recovered window", res)
	}
}

func TestQuotaProbeUnavailableWhenNoSourceSucceeds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()

	probe := newHTTPQuotaRecoveryProbe()
	res, err := probe.ProbeQuotaRecovery(context.Background(), server.URL, "mgmt", "unknown-provider", "auth-1", "")
	if err != nil {
		t.Fatalf("probe error: %v", err)
	}
	if res.Available || len(res.Windows) != 0 {
		t.Fatalf("probe result = %#v, want unavailable", res)
	}
}

func TestQuotaProbeRequiresAuthIndex(t *testing.T) {
	probe := newHTTPQuotaRecoveryProbe()
	res, err := probe.ProbeQuotaRecovery(context.Background(), "http://cpa", "mgmt", "codex", "", "")
	if err != nil {
		t.Fatalf("probe error: %v", err)
	}
	if res.Available {
		t.Fatalf("probe result = %#v, want unavailable without an auth index", res)
	}
}
