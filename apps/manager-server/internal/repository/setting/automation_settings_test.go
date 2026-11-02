package setting

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	_ "modernc.org/sqlite"
)

func newAutomationSettingsDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "settings.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE settings(key TEXT PRIMARY KEY,value TEXT NOT NULL,updated_at_ms INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestAutomationSettingsPreservationAndWriteAreAtomic(t *testing.T) {
	for _, failure := range []string{"receipt", "source"} {
		t.Run(failure, func(t *testing.T) {
			db := newAutomationSettingsDB(t)
			const before = `{"codexQuotaCooldownEnabled":true,"authIssueQueueEnabled":false}`
			if _, err := db.Exec(`INSERT INTO settings VALUES('automation_settings_v1',?,17)`, before); err != nil {
				t.Fatal(err)
			}
			trigger := `CREATE TRIGGER fail_write BEFORE INSERT ON settings WHEN NEW.key='automation_settings_legacy_preservation_v1' BEGIN SELECT RAISE(ABORT,'synthetic receipt write failure'); END`
			if failure == "source" {
				trigger = `CREATE TRIGGER fail_write BEFORE UPDATE ON settings WHEN OLD.key='automation_settings_v1' BEGIN SELECT RAISE(ABORT,'synthetic source write failure'); END`
			}
			if _, err := db.Exec(trigger); err != nil {
				t.Fatal(err)
			}
			on := true
			if _, err := New(db).SaveAutomationSettings(context.Background(), model.AutomationSettings{AccountActionsEnabled: &on}); err == nil {
				t.Fatal("failed write unexpectedly succeeded")
			}
			var after string
			var updatedAt int64
			if err := db.QueryRow(`SELECT value,updated_at_ms FROM settings WHERE key='automation_settings_v1'`).Scan(&after, &updatedAt); err != nil || after != before || updatedAt != 17 {
				t.Fatalf("failed write changed original settings: %v", err)
			}
			var receipts int
			if err := db.QueryRow(`SELECT count(*) FROM settings WHERE key='automation_settings_legacy_preservation_v1'`).Scan(&receipts); err != nil || receipts != 0 {
				t.Fatalf("failed transaction left misleading completed preservation evidence: %v", err)
			}
		})
	}
}

func TestAutomationSettingsSaveKeepsLatestUnknownFieldsAndClearsNilOverrides(t *testing.T) {
	db := newAutomationSettingsDB(t)
	if _, err := db.Exec(`INSERT INTO settings VALUES('automation_settings_v1','{"codexQuotaCooldownEnabled":false,"authIssueQueueEnabled":true}',17)`); err != nil {
		t.Fatal(err)
	}
	repo := New(db)
	settings, _, err := repo.LoadAutomationSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// A future/old writer added an unknown field after this reader's load. Save
	// must merge with the current database document, not its smaller stale model.
	if _, err := db.Exec(`UPDATE settings SET value=json_set(value,'$.addedLater',json('{"precise":9007199254740993,"optional":null}')) WHERE key='automation_settings_v1'`); err != nil {
		t.Fatal(err)
	}
	settings.AccountActionsEnabled = nil
	if _, err := repo.SaveAutomationSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := db.QueryRow(`SELECT value FROM settings WHERE key='automation_settings_v1'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		t.Fatal(err)
	}
	if _, exists := fields["authIssueQueueEnabled"]; exists || string(fields["codexQuotaCooldownEnabled"]) != "false" || string(fields["addedLater"]) != `{"precise":9007199254740993,"optional":null}` {
		t.Fatalf("known override clearing or current unknown fields lost: %s", raw)
	}
}

func TestAutomationSettingsSaveRejectsNonObjectWithoutOverwriting(t *testing.T) {
	for _, raw := range []string{"null", "[]", `{"incomplete":`} {
		t.Run(raw, func(t *testing.T) {
			db := newAutomationSettingsDB(t)
			if _, err := db.Exec(`INSERT INTO settings VALUES('automation_settings_v1',?,17)`, raw); err != nil {
				t.Fatal(err)
			}
			if _, err := New(db).SaveAutomationSettings(context.Background(), model.AutomationSettings{}); err == nil {
				t.Fatal("invalid historical document was silently replaced")
			}
			var after string
			if err := db.QueryRow(`SELECT value FROM settings WHERE key='automation_settings_v1'`).Scan(&after); err != nil || after != raw {
				t.Fatalf("invalid historical document was overwritten: %v", err)
			}
		})
	}
}

func TestAutomationSettingsKnownOnlyNeedsNoLegacyReceipt(t *testing.T) {
	db := newAutomationSettingsDB(t)
	repo := New(db)
	for i := 0; i < 2; i++ {
		if _, err := repo.SaveAutomationSettings(context.Background(), model.AutomationSettings{}); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM settings WHERE key='automation_settings_legacy_preservation_v1'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("new/known-only settings created misleading legacy migration evidence: %v", err)
	}
}
