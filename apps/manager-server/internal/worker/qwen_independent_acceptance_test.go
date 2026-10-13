package worker

import (
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/usage"
	"net/http"
	"testing"
	"time"
)

func TestIndependentQwenRateLimitDoesNotDisableCredential(t *testing.T) {
	now := time.Unix(1700000000, 0)
	event := usage.Event{Failed: true, FailStatusCode: http.StatusTooManyRequests, FailBody: `{"error":{"code":"rate_limit_exceeded","message":"Rate limit exceeded. Please retry later.","retry_after_seconds":60}}`, AuthFileSnapshot: "fixture-qwen.json", AuthIndex: "fixture-qwen-1", Provider: "qwen"}
	_, accepted := quotaAutoDisableCandidateFromEvent(event, "http://fixture.invalid", "synthetic-test-key", now)
	if accepted {
		t.Fatal("ordinary rate limiting was misclassified as exhausted quota; credential must not be auto-disabled")
	}
}

func TestIndependentQwenUnknownResetKeepsUnknownETA(t *testing.T) {
	now := time.Unix(1700000000, 0)
	event := usage.Event{Failed: true, FailStatusCode: http.StatusTooManyRequests, FailBody: `{"error":{"code":"quota_exhausted"}}`, AuthFileSnapshot: "fixture-qwen.json", AuthIndex: "fixture-qwen-1", Provider: "qwen"}
	candidate, accepted := quotaAutoDisableCandidateFromEvent(event, "http://fixture.invalid", "synthetic-test-key", now)
	if !accepted {
		t.Fatal("confirmed exhausted quota should be accepted")
	}
	if !candidate.ResetAt.IsZero() || candidate.RecoverAtKind != "unknown" {
		t.Fatal("unknown reset must not fabricate provider ETA")
	}
}
