package worker

import (
	"net/http"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/usage"
)

func TestQwenQuotaEvidenceRequiresKnownCodeOrType(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, body string
		status     int
		accept     bool
		kind       string
	}{
		{"normal upstream", `{"error":{"code":"insufficient_quota","type":"insufficient_quota","message":"Quota exhausted"},"reset_at":"2030-01-02T03:04:05Z"}`, 429, true, model.QuotaCooldownRecoverKindProvider},
		{"type only", `{"error":{"type":"insufficient_quota"}}`, 429, true, model.QuotaCooldownRecoverKindUnknown},
		{"invalid reset", `{"error":{"code":"insufficient_quota"},"reset_at":"invalid"}`, 429, true, model.QuotaCooldownRecoverKindUnknown},
		{"unrelated nested reset", `{"error":{"code":"insufficient_quota"},"debug":{"reset_at":"2030-01-03T03:04:05Z"}}`, 429, true, model.QuotaCooldownRecoverKindUnknown},
		{"ordinary 429", `{"error":{"code":"rate_limit_exceeded","message":"Quota exhausted, retry later"}}`, 429, false, ""},
		{"unknown code", `{"error":{"code":"unknown_insufficient_quota"}}`, 429, false, ""},
		{"message only", `{"error":{"message":"quota exhausted"}}`, 429, false, ""},
		{"unrelated metadata", `{"debug":{"code":"insufficient_quota"}}`, 429, false, ""},
		{"unauthorized", `{"error":{"code":"insufficient_quota"}}`, http.StatusUnauthorized, false, ""},
		{"forbidden", `{"error":{"code":"insufficient_quota"}}`, http.StatusForbidden, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate, ok := quotaAutoDisableCandidateFromEvent(usage.Event{Failed: true, FailStatusCode: tc.status, FailBody: tc.body, Provider: "qwen", AuthFileSnapshot: "qwen.json", AuthIndex: "current"}, "http://isolated", "synthetic-management-key", now)
			if ok != tc.accept {
				t.Fatalf("accepted=%t want=%t", ok, tc.accept)
			}
			if ok && candidate.RecoverAtKind != tc.kind {
				t.Fatalf("schedule kind=%q want=%q", candidate.RecoverAtKind, tc.kind)
			}
			if ok && tc.kind == model.QuotaCooldownRecoverKindUnknown && !candidate.ResetAt.IsZero() {
				t.Fatal("invented reset time")
			}
			if ok && tc.kind == model.QuotaCooldownRecoverKindProvider && candidate.ResetAt.Format(time.RFC3339) != "2030-01-02T03:04:05Z" {
				t.Fatal("normal reset lost")
			}
		})
	}
}

func TestQwenQuotaEvidenceReadsABIMessageFromRawUsageEnvelope(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	event := usage.Event{
		Failed:           true,
		FailStatusCode:   http.StatusTooManyRequests,
		RawJSON:          `{"failed":{"body":"{\"error\":{\"code\":\"insufficient_quota\"},\"reset_at\":\"2030-01-02T03:04:05Z\"}"}}`,
		Provider:         "qwen",
		AuthFileSnapshot: "qwen.json",
		AuthIndex:        "current",
	}
	candidate, ok := quotaAutoDisableCandidateFromEvent(event, "http://isolated", "synthetic-management-key", now)
	if !ok || candidate.RecoverAtKind != model.QuotaCooldownRecoverKindProvider || candidate.ResetAt.Format(time.RFC3339) != "2030-01-02T03:04:05Z" {
		t.Fatalf("raw usage envelope candidate = %#v, ok=%t", candidate, ok)
	}
}
