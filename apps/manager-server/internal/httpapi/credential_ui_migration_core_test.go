package httpapi

import (
	"encoding/json"
	"testing"

	sqliterepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
)

// Public API + temporary SQLite + actual immutable Core. No name-derived
// enrollment, no replay of global intent, no mutation of native disabled state.
func TestCredentialUI93UnknownDisabledHistoryStaysPending(t *testing.T) {
	f := newNativeAdmissionFixture(t, true)
	// Seed the actual pre-upgrade settings document, not a rejected new write.
	db, err := sqliterepo.Open(f.manager.AppContext().Config.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	const legacy = `{"codexQuotaCooldownEnabled":true,"authIssueQueueEnabled":false,"updatedAtMs":17}`
	if _, err := db.Exec(`INSERT INTO settings(key,value,updated_at_ms) VALUES('automation_settings_v1',?,17)`, legacy); err != nil {
		t.Fatal(err)
	}
	f.api("PATCH", "/usage-service/account-processing-policy", map[string]any{"authIssueQueueEnabled": true}, 200, nil)
	var preserved bool
	if err := db.QueryRow(`SELECT json_extract(value,'$.codexQuotaCooldownEnabled') FROM settings WHERE key='automation_settings_v1'`).Scan(&preserved); err != nil || !preserved {
		t.Fatalf("unrelated PATCH lost stored legacy global ON: %v", err)
	}
	// The removed global quota policy is no longer an accepted write field.
	f.api("PATCH", "/usage-service/account-processing-policy", map[string]any{"codexQuotaCooldownEnabled": true}, 400, nil)
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
		b := f.status("B")
		if b.Enabled || b.ManualDisabled || b.LegacyMigration == nil || b.LegacyMigration.State != "default_off" {
			t.Fatal("stored global ON enrolled or disabled the unrelated default-OFF B")
		}
	}
	f.sendHTTP()
	if f.sends[0].Load() != 0 || f.sends[1].Load() != 1 {
		t.Fatal("history classification unpaused A or paused B")
	}
}
