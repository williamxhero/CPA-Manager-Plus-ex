package setting

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestRuntimeControlsRoundTrip(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "controls.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err = db.Exec("create table settings(key text primary key,value text not null,updated_at_ms integer not null)"); err != nil {
		t.Fatal(err)
	}
	missing, err := LoadRuntimeControls(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if missing.QuotaRefreshIntervalMs != 3_600_000 || missing.AutoStartStopEnabled {
		t.Fatalf("default = %+v", missing)
	}
	next := RuntimeControls{
		QuotaRefreshIntervalMs: 900_000,
		QuotaFetchedAtMs:       10,
		AutoStartStopEnabled:   true,
		AutoStartStopIDs: []string{
			"openai:068a7783-00cf-40e3-a900-ced04834afbe",
			"claude:sk-secret",
		},
		BalanceTexts: map[string]string{
			"openai:068a7783-00cf-40e3-a900-ced04834afbe": "USD 0",
			"claude:sk-secret": "USD 1",
		},
	}
	if err = SaveRuntimeControls(context.Background(), db, next); err != nil {
		t.Fatal(err)
	}
	got, err := LoadRuntimeControls(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if got.QuotaRefreshIntervalMs != 900_000 || !got.AutoStartStopEnabled || got.BalanceTexts["openai:068a7783-00cf-40e3-a900-ced04834afbe"] != "USD 0" || len(got.BalanceTexts) != 1 || len(got.AutoStartStopIDs) != 1 || got.AutoStartStopIDs[0] != "openai:068a7783-00cf-40e3-a900-ced04834afbe" {
		t.Fatalf("saved = %+v", got)
	}
}
