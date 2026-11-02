package automation

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
	sqliterepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

func TestLegacyAutomationSettingsSurviveServiceRestartWithoutActiveGlobalGate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.sqlite")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	rawDB, err := sqliterepo.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rawDB.Close() })
	const original = `{"codexQuotaCooldownEnabled":true,"futureSecret":"synthetic-private-value","authIssueQueueEnabled":false}`
	if _, err := rawDB.Exec(`INSERT INTO settings VALUES('automation_settings_v1',?,17)`, original); err != nil {
		t.Fatal(err)
	}
	svc := New(config.Config{}, st)
	if svc.RuntimeSettings(context.Background()).AccountActionsEnabled {
		t.Fatal("historical global quota ON enabled a different active policy")
	}
	if _, err := svc.Update(context.Background(), UpdateRequest{AccountActionsAutoDisable: boolPtr(true)}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if err := rawDB.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	rawDB, err = sqliterepo.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	svc = New(config.Config{}, st)
	status := mustStatus(t, svc, context.Background())
	if status.AccountActions.Enabled || !status.AccountActionsAutoDisable.Configured || status.AccountActionsAutoDisable.Enabled {
		t.Fatal("reopened service changed current policy semantics using retained legacy data")
	}
	data, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "codexQuotaCooldownEnabled") || strings.Contains(string(data), "synthetic-private-value") {
		t.Fatal("private preserved values were serialized into a public policy response")
	}
	var originalValue string
	var retained bool
	if err := rawDB.QueryRow(`SELECT json_extract(value,'$.originalValue') FROM settings WHERE key='automation_settings_legacy_preservation_v1'`).Scan(&originalValue); err != nil || originalValue != original {
		t.Fatalf("restart lost the original rollback document: %v", err)
	}
	if err := rawDB.QueryRow(`SELECT json_extract(value,'$.codexQuotaCooldownEnabled') FROM settings WHERE key='automation_settings_v1'`).Scan(&retained); err != nil || !retained {
		t.Fatalf("restart lost the retained non-operative global flag: %v", err)
	}
}
