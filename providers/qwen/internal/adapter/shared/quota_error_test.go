package shared

import (
	"encoding/json"
	"strings"
	"testing"

	"qwen-cliproxyapi/internal/errclass"
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
