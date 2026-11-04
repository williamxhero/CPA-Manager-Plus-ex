package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestProviderPolicyMigrationDialectBackupDryRunAndAmbiguousPending(t *testing.T) {
	ctx := context.Background()
	db := openPolicyMigrateFixture(t)
	defer db.Close()

	proven := `{"subjectId":"subj-a","owner":"provider-balance/install-1","enabled":false,"source":"explicit"}`
	ambiguous := `{"enabled":true}` // missing owner/subject — must not auto-enable
	legacyWorker := `{"subjectId":"subj-b","owner":"CredentialPolicyInstallation/worker","enabled":false,"source":"explicit","journalId":"worker-legacy"}`
	if _, err := db.db.ExecContext(ctx, `INSERT INTO provider_policies(id,generation,document) VALUES(?,?,?),(?,?,?),(?,?,?)`,
		"subj-a", 1, proven,
		"amb-1", 1, ambiguous,
		"subj-b", 2, legacyWorker,
	); err != nil {
		t.Fatal(err)
	}

	backupDir := t.TempDir()
	report, err := db.MigrateProviderPolicyDialect(ctx, ProviderPolicyMigrateOptions{
		DryRun:    true,
		Target:    ProviderPolicyDialectDesktopV1,
		BackupDir: backupDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.DialectBefore != ProviderPolicyDialectUnknown || report.DialectAfter != ProviderPolicyDialectUnknown {
		t.Fatalf("dry-run must not advance dialect: %+v", report)
	}
	if report.Migrated != 1 || report.Pending != 2 || report.BackedUp != 3 {
		t.Fatalf("dry-run counts: %+v", report)
	}
	var count int
	if err := db.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM provider_policies`).Scan(&count); err != nil || count != 3 {
		t.Fatalf("dry-run mutated rows: count=%d err=%v", count, err)
	}
	if entries, _ := filepath.Glob(filepath.Join(backupDir, "*")); len(entries) != 0 {
		t.Fatalf("dry-run wrote backup files: %v", entries)
	}

	report, err = db.MigrateProviderPolicyDialect(ctx, ProviderPolicyMigrateOptions{
		DryRun:    false,
		Target:    ProviderPolicyDialectDesktopV1,
		BackupDir: backupDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.DialectAfter != ProviderPolicyDialectDesktopV1 || report.Migrated != 1 || report.Pending != 2 {
		t.Fatalf("apply report: %+v", report)
	}
	if entries, _ := filepath.Glob(filepath.Join(backupDir, "*.json")); len(entries) == 0 {
		t.Fatal("expected backup artifact")
	}

	var doc string
	if err := db.db.QueryRowContext(ctx, `SELECT document FROM provider_policies WHERE id=?`, "subj-a").Scan(&doc); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if json.Unmarshal([]byte(doc), &body) != nil || body["dialect"] != ProviderPolicyDialectDesktopV1 {
		t.Fatalf("proven row missing dialect stamp: %s", doc)
	}
	if body["enabled"] != false {
		t.Fatal("migration must not flip enabled")
	}
	if _, ok := body["manualDisabled"]; ok {
		t.Fatal("must not invent manualDisabled as a gate substitute")
	}

	var pendingReason string
	if err := db.db.QueryRowContext(ctx, `SELECT reason FROM provider_policy_migration_pending WHERE id=?`, "amb-1").Scan(&pendingReason); err != nil {
		t.Fatal(err)
	}
	if pendingReason == "" {
		t.Fatal("ambiguous row needs pending reason")
	}
	if err := db.db.QueryRowContext(ctx, `SELECT document FROM provider_policies WHERE id=?`, "amb-1").Scan(&doc); err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal([]byte(doc), &body) != nil || body["enabled"] != true {
		t.Fatalf("ambiguous source row must remain untouched pending review: %s", doc)
	}

	again, err := db.MigrateProviderPolicyDialect(ctx, ProviderPolicyMigrateOptions{
		DryRun:    false,
		Target:    ProviderPolicyDialectDesktopV1,
		BackupDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if again.Migrated != 0 || again.Pending != 0 || again.DialectAfter != ProviderPolicyDialectDesktopV1 {
		t.Fatalf("idempotent re-run must no-op: %+v", again)
	}
}

func TestProviderPolicyMigrationReadinessHoldsTrafficUntilResolved(t *testing.T) {
	ctx := context.Background()
	db := openPolicyMigrateFixture(t)
	defer db.Close()

	ready, err := db.ProviderPolicyMigrationReadiness(ctx)
	if err != nil || !ready.Ready || ready.ProtectionHold {
		t.Fatalf("empty store should be ready: %+v err=%v", ready, err)
	}
	if _, err := db.db.ExecContext(ctx, `INSERT INTO provider_policies(id,generation,document) VALUES(?,?,?)`,
		"amb-1", 1, `{"enabled":true}`); err != nil {
		t.Fatal(err)
	}
	ready, err = db.ProviderPolicyMigrationReadiness(ctx)
	if err != nil || ready.Ready || !ready.ProtectionHold || ready.Reason != "dialect_unknown_with_policies" {
		t.Fatalf("unknown dialect with policies must hold: %+v err=%v", ready, err)
	}
	if _, err := db.MigrateProviderPolicyDialect(ctx, ProviderPolicyMigrateOptions{Target: ProviderPolicyDialectDesktopV1}); err != nil {
		t.Fatal(err)
	}
	ready, err = db.ProviderPolicyMigrationReadiness(ctx)
	if err != nil || ready.Ready || !ready.ProtectionHold || ready.PendingCount != 1 {
		t.Fatalf("pending rows must keep protection hold: %+v err=%v", ready, err)
	}
}

func TestProviderPolicyMigrationRefusesUnsafeDowngrade(t *testing.T) {
	ctx := context.Background()
	db := openPolicyMigrateFixture(t)
	defer db.Close()
	if _, err := db.MigrateProviderPolicyDialect(ctx, ProviderPolicyMigrateOptions{
		Target: ProviderPolicyDialectDesktopV1,
	}); err != nil {
		t.Fatal(err)
	}
	_, err := db.MigrateProviderPolicyDialect(ctx, ProviderPolicyMigrateOptions{
		Target: ProviderPolicyDialectUnknown,
	})
	if err == nil {
		t.Fatal("expected unsafe downgrade refusal")
	}
}

func openPolicyMigrateFixture(t *testing.T) *Store {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "migrate.sqlite"), newTestProtector(t))
	if err != nil {
		t.Fatal(err)
	}
	return db
}
