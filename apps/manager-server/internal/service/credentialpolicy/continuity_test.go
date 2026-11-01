package credentialpolicy_test

import (
	"encoding/json"
	"testing"

	p "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy"
)

// Receipt shape/fence test only, not an actual Core continuity certificate.
func TestRefreshContinuityRequiresExactCertifiedTargetsNotHistoricalBootEquality(t *testing.T) {
	status, _ := windowEvidence(t)
	current := status.Source
	previous := current.Binding
	previous.Scope.BootEpoch = "00000000-0000-4000-8000-000000000099"
	previous.Scope.Revision = 1
	// Separate backing storage: the historic target must not mutate current.
	raw, _ := json.Marshal(previous)
	_ = json.Unmarshal(raw, &previous)
	previous.Targets[0].CredentialVersion++
	result := p.ContinuityReceipt{Version: 1, Proven: true, Reason: "certified_native_refresh_chain", Kind: "native_oauth_refresh", Binding: current.Binding, PreviousTargets: previous.Targets, AuthIndex: current.AuthIndex, SourceVersion: current.SourceVersion}
	if !result.Confirms(previous, current) {
		t.Fatal("historical boot/revision incorrectly treated as issuance certificate")
	}
	raw, _ = json.Marshal(result)
	for _, name := range []string{"unproven", "kind", "reason", "source", "previous_target", "current_target", "boot", "persistent_instance"} {
		t.Run(name, func(t *testing.T) {
			var changed p.ContinuityReceipt
			_ = json.Unmarshal(raw, &changed)
			switch name {
			case "unproven":
				changed.Proven = false
			case "kind":
				changed.Kind = "same_account_label"
			case "reason":
				changed.Reason = "claimed"
			case "source":
				changed.SourceVersion = "changed"
			case "previous_target":
				changed.PreviousTargets[0].IdentityVersion++
			case "current_target":
				changed.Targets[0].MembershipVersion++
			case "boot":
				changed.Scope.BootEpoch = previous.Scope.BootEpoch
			case "persistent_instance":
				changed.Scope.InstanceIncarnation = previous.Scope.BootEpoch
			}
			if changed.Confirms(previous, current) {
				t.Fatal("unrelated or untrusted continuity accepted")
			}
		})
	}
}
