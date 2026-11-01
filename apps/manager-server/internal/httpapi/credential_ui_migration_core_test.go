package httpapi

import (
	"encoding/json"
	"testing"
)

// Public API + temporary SQLite + actual immutable Core. No name-derived
// enrollment, no replay of global intent, no mutation of native disabled state.
func TestCredentialUI93UnknownDisabledHistoryStaysPending(t *testing.T) {
	f := newNativeAdmissionFixture(t, true)
	f.api("PATCH", "/usage-service/account-processing-policy", map[string]any{"codexQuotaCooldownEnabled": true}, 200, nil)
	for i := 0; i < 2; i++ {
		var status map[string]json.RawMessage
		f.api("GET", "/usage-service/credential-policy?authIndex="+f.sources["A"].AuthIndex, nil, 200, &status)
		var enabled bool
		json.Unmarshal(status["enabled"], &enabled)
		if enabled {
			t.Fatal("global ON became per-subject intent")
		}
		var migration struct {
			State  string `json:"state"`
			Reason string `json:"reason"`
		}
		json.Unmarshal(status["legacyMigration"], &migration)
		if migration.State != "pending_confirmation" || migration.Reason != "legacy_identity_owner_manual_history_unproven" {
			t.Fatalf("unknown disabled history not pending: %+v", migration)
		}
		if !f.status("A").ManualDisabled {
			t.Fatal("history review enabled native disabled credential")
		}
	}
	f.sendHTTP()
	if f.sends[0].Load() != 0 || f.sends[1].Load() != 1 {
		t.Fatal("history classification unpaused A or paused B")
	}
}
