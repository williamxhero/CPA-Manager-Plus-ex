package httpapi

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	sqliterepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

// The public PATCH seam must preserve pre-upgrade data in actual SQLite, even
// when the current runtime no longer interprets those fields.
func TestAccountProcessingPolicyPatchPreservesLegacySQLiteDocument(t *testing.T) {
	cfg := testutil.NewConfig(t)
	handler, _ := newCompatHandler(t, cfg, nil)
	db, err := sqliterepo.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	const before = `{"codexQuotaCooldownEnabled":true,"futurePolicy":{"limit":9007199254740993,"flags":[false,null]},"authIssueQueueEnabled":false,"updatedAtMs":17}`
	if _, err := db.Exec(`INSERT INTO settings(key,value,updated_at_ms) VALUES('automation_settings_v1',?,17)`, before); err != nil {
		t.Fatal(err)
	}

	rr := testutil.Request(t, handler, http.MethodPatch, "/usage-service/account-processing-policy", `{"authIssueQueueEnabled":true}`, testutil.AdminKey)
	testutil.RequireStatus(t, rr, http.StatusOK)
	var raw string
	if err := db.QueryRow(`SELECT value FROM settings WHERE key='automation_settings_v1'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var saved map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &saved); err != nil {
		t.Fatal(err)
	}
	if string(saved["codexQuotaCooldownEnabled"]) != "true" {
		t.Fatalf("unrelated PATCH erased legacy global intent: %s", raw)
	}
	if string(saved["futurePolicy"]) != `{"limit":9007199254740993,"flags":[false,null]}` {
		t.Fatalf("unrelated PATCH changed unknown field or numeric precision: %s", raw)
	}
	if string(saved["authIssueQueueEnabled"]) != "true" {
		t.Fatalf("PATCH did not persist its requested field: %s", raw)
	}
}

func TestAccountProcessingPolicyPatchAuditsPreservationAndAllowsExactRollback(t *testing.T) {
	cfg := testutil.NewConfig(t)
	handler, _ := newCompatHandler(t, cfg, nil)
	db, err := sqliterepo.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	// Keep original whitespace and both timestamps: rollback must be exact, not
	// a reconstruction from the smaller current runtime model.
	const before = `{ "codexQuotaCooldownEnabled": true, "futurePolicy": {"limit":9007199254740993}, "authIssueQueueEnabled": false, "updatedAtMs": 11 }`
	if _, err := db.Exec(`INSERT INTO settings(key,value,updated_at_ms) VALUES('automation_settings_v1',?,17)`, before); err != nil {
		t.Fatal(err)
	}
	patch := func(body string) {
		t.Helper()
		rr := testutil.Request(t, handler, http.MethodPatch, "/usage-service/account-processing-policy", body, testutil.AdminKey)
		testutil.RequireStatus(t, rr, http.StatusOK)
		if strings.Contains(rr.Body.String(), "codexQuotaCooldownEnabled") || strings.Contains(rr.Body.String(), "futurePolicy") {
			t.Fatal("persistence-only legacy fields leaked into the active policy API")
		}
	}
	patch(`{"authIssueQueueEnabled":true}`)
	const auditKey = "automation_settings_legacy_preservation_v1"
	var auditJSON string
	if err := db.QueryRow(`SELECT value FROM settings WHERE key=?`, auditKey).Scan(&auditJSON); err != nil {
		t.Fatalf("PATCH did not durably record its explicit legacy preservation decision: %v", err)
	}
	var audit struct {
		Version             int               `json:"version"`
		SourceKey           string            `json:"sourceKey"`
		Strategy            string            `json:"strategy"`
		PerSubjectMapping   string            `json:"perSubjectMapping"`
		Fields              map[string]string `json:"fields"`
		OriginalValue       string            `json:"originalValue"`
		OriginalUpdatedAtMS int64             `json:"originalUpdatedAtMs"`
		PreservedAtMS       int64             `json:"preservedAtMs"`
	}
	if err := json.Unmarshal([]byte(auditJSON), &audit); err != nil {
		t.Fatal(err)
	}
	if audit.Version != 1 || audit.SourceKey != "automation_settings_v1" || audit.Strategy != "preserve_only" || audit.PerSubjectMapping != "none" || audit.PreservedAtMS <= 0 {
		t.Fatalf("missing explicit non-operative, non-enrolling preservation decision: %+v", audit)
	}
	wantFields := map[string]string{"codexQuotaCooldownEnabled": "preserved_unmapped_requires_confirmation", "futurePolicy": "preserved_uninterpreted"}
	if !reflect.DeepEqual(audit.Fields, wantFields) || audit.OriginalValue != before || audit.OriginalUpdatedAtMS != 17 {
		t.Fatalf("preservation manifest lost original data or field disposition: %+v", audit)
	}
	patch(`{"authIssueAutoDisableEnabled":true}`)
	var again string
	if err := db.QueryRow(`SELECT value FROM settings WHERE key=?`, auditKey).Scan(&again); err != nil || again != auditJSON {
		t.Fatalf("later PATCH replaced the immutable pre-migration snapshot: %v", err)
	}
	// The recovery procedure consumes only the durable receipt, just as an
	// offline operator would. It does not touch credential policies or Core.
	if _, err := db.Exec(`UPDATE settings SET value=json_extract((SELECT value FROM settings WHERE key=?),'$.originalValue'), updated_at_ms=json_extract((SELECT value FROM settings WHERE key=?),'$.originalUpdatedAtMs') WHERE key='automation_settings_v1'`, auditKey, auditKey); err != nil {
		t.Fatal(err)
	}
	var restored string
	var restoredAt int64
	if err := db.QueryRow(`SELECT value,updated_at_ms FROM settings WHERE key='automation_settings_v1'`).Scan(&restored, &restoredAt); err != nil || restored != before || restoredAt != 17 {
		t.Fatalf("rollback did not restore the exact pre-migration document and row timestamp: %v", err)
	}
	var subjects int
	if err := db.QueryRow(`SELECT count(*) FROM credential_policies`).Scan(&subjects); err != nil || subjects != 0 {
		t.Fatalf("global legacy ON was mapped to per-subject policies: count=%d err=%v", subjects, err)
	}
}
