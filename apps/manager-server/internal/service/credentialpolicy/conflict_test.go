package credentialpolicy_test

import (
	policy "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy"
	"testing"
)

// Negative response decoding only; not a successful Core operation fixture.
func TestCredentialPolicyOnlyTypedRevisionRejectionPermitsReplan(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`{"error":"gate_conflict","conflict":"gate_revision"}`, "gate_revision_conflict"},
		{`{"error":"gate_conflict"}`, "gate_conflict"},
		{`{"error":"operation_conflict"}`, "gate_operation_conflict"},
		{`{"error":"gate_conflict","conflict":"binding"}`, "gate_conflict"},
		{`{"error":"gate_conflict","conflict":"gate_revision","extra":true}`, "gate_conflict"},
		{`{"error":"gate_conflict","conflict":"gate_revision"} {}`, "gate_conflict"},
		{`SECRET-CANARY`, "gate_conflict"},
	} {
		if got := policy.DecodeGateConflict([]byte(tc.body)); got != tc.want {
			t.Fatalf("unsafe conflict classification: %s", got)
		}
	}
}
