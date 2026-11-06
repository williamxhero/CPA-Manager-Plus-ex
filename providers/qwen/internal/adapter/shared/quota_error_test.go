package shared

import (
	"encoding/json"
	"strings"
	"testing"

	"cpa-credential-qwen/internal/errclass"
)

func TestUpstreamQuotaErrorPreservesSafeMetadata(t *testing.T) {
	body := []byte(`{"error":{"code":"insufficient_quota","type":"insufficient_quota","message":"Quota exhausted selected-key config-key token-secret cookie-secret","token":"token-secret"},"reset_at":"2030-01-02T03:04:05Z","cookie":"cookie-secret","unknown":"config-key"}`)
	wire := errclass.ToEnvelopeError(UpstreamStatusError(429, body))
	var metadata struct {
		Error struct {
			Code string `json:"code"`
			Type string `json:"type"`
		} `json:"error"`
		ResetAt string `json:"reset_at"`
	}
	if err := json.Unmarshal([]byte(wire.Message), &metadata); err != nil {
		t.Fatalf("ABI message lost structured quota evidence: %q (%v)", wire.Message, err)
	}
	if metadata.Error.Code != "insufficient_quota" || metadata.Error.Type != "insufficient_quota" || metadata.ResetAt != "2030-01-02T03:04:05Z" || wire.HTTPStatus != 429 {
		t.Fatalf("quota metadata = %+v; status=%d", metadata, wire.HTTPStatus)
	}
	for _, secret := range []string{"selected-key", "config-key", "token-secret", "cookie-secret", "unknown"} {
		if strings.Contains(wire.Message, secret) {
			t.Fatalf("upstream data leaked: %q", wire.Message)
		}
	}
}

func TestUpstreamQuotaErrorFixedFieldProjection(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		status           int
	}{
		{"code only 402", `{"error":{"code":"quota_exceeded","type":"synthetic-secret","message":"synthetic-secret"},"reset_at":"2030-01-02T11:04:05.123+08:00","extra":"synthetic-secret"}`, `{"error":{"code":"quota_exceeded","type":""},"reset_at":"2030-01-02T03:04:05.123Z"}`, 402},
		{"type only", `{"error":{"code":"synthetic-secret","type":"quota_exhausted"},"reset_at":"invalid-synthetic-secret"}`, `{"error":{"code":"","type":"quota_exhausted"}}`, 429},
		{"invalid reset omitted", `{"error":{"code":"insufficient_quota"},"reset_at":"tomorrow","token":"synthetic-secret"}`, `{"error":{"code":"insufficient_quota","type":""}}`, 429},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wire := errclass.ToEnvelopeError(UpstreamStatusError(tc.status, []byte(tc.body)))
			if wire.Message != tc.want || wire.HTTPStatus != tc.status || wire.Code != "quota_exhaustion" {
				t.Fatalf("unsafe or inaccurate projection: %+v", wire)
			}
		})
	}
}

func TestUpstreamQuotaErrorDoesNotInventStructuredEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"unknown code", `{"error":{"code":"unknown","message":"rate limit"},"reset_at":"2030-01-02T03:04:05Z"}`, 429},
		{"malformed", `{"error":{"code":"insufficient_quota"}`, 429},
		{"wrong status", `{"error":{"code":"insufficient_quota","message":"unavailable"}}`, 503},
		{"bounded body", `{"padding":"` + strings.Repeat("x", 5000) + `","error":{"code":"insufficient_quota"}}`, 429},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wire := errclass.ToEnvelopeError(UpstreamStatusError(tc.status, []byte(tc.body)))
			var metadata map[string]any
			if json.Unmarshal([]byte(wire.Message), &metadata) == nil {
				t.Fatalf("invented quota metadata: %s", wire.Message)
			}
			if wire.HTTPStatus != tc.status {
				t.Fatal("status changed")
			}
		})
	}
}
