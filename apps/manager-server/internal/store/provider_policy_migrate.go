package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	ProviderPolicyDialectUnknown   = "unknown"
	ProviderPolicyDialectDesktopV1 = "desktop-canonical-v1"
	ProviderPolicyDialectWorkerV1  = "worker-legacy-v1"
)

type ProviderPolicyMigrateOptions struct {
	DryRun    bool
	Target    string
	BackupDir string
}

type ProviderPolicyMigrateReport struct {
	DialectBefore string `json:"dialectBefore"`
	DialectAfter  string `json:"dialectAfter"`
	BackedUp      int    `json:"backedUp"`
	Migrated      int    `json:"migrated"`
	Pending       int    `json:"pending"`
	DryRun        bool   `json:"dryRun"`
}

var (
	ErrUnsafeProviderPolicyDowngrade = errors.New("unsafe provider policy dialect downgrade refused")
	ErrProviderPolicyBackupInvalid   = errors.New("provider policy backup artifact invalid")
)

type providerPolicyMigrateDoc struct {
	SubjectID string `json:"subjectId"`
	Owner     string `json:"owner"`
	Enabled   *bool  `json:"enabled"`
	Source    string `json:"source"`
	JournalID string `json:"journalId"`
	Dialect   string `json:"dialect"`
}

func (s *Store) ensureProviderPolicyMigrationTables(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS provider_policy_schema (singleton INTEGER PRIMARY KEY CHECK(singleton=1), dialect TEXT NOT NULL, version INTEGER NOT NULL CHECK(version>0))`,
		`CREATE TABLE IF NOT EXISTS provider_policy_migration_pending (id TEXT PRIMARY KEY, generation INTEGER NOT NULL, document TEXT NOT NULL CHECK(json_valid(document)), reason TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS provider_policy_migration_backup (seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL, generation INTEGER NOT NULL, document TEXT NOT NULL CHECK(json_valid(document)), backed_up_at_ms INTEGER NOT NULL)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) providerPolicyDialect(ctx context.Context) (string, error) {
	if err := s.ensureProviderPolicyMigrationTables(ctx); err != nil {
		return "", err
	}
	var dialect string
	err := s.db.QueryRowContext(ctx, `SELECT dialect FROM provider_policy_schema WHERE singleton=1`).Scan(&dialect)
	if errors.Is(err, sql.ErrNoRows) {
		return ProviderPolicyDialectUnknown, nil
	}
	return dialect, err
}

func classifyProviderPolicyDocument(id, document string) (dialect string, pendingReason string, stampable bool) {
	var doc providerPolicyMigrateDoc
	if json.Unmarshal([]byte(document), &doc) != nil || doc.Enabled == nil {
		return ProviderPolicyDialectUnknown, "unparseable_or_missing_enabled", false
	}
	if doc.SubjectID == "" || doc.Owner == "" || doc.SubjectID != id {
		return ProviderPolicyDialectUnknown, "missing_proven_identity_or_owner", false
	}
	if strings.Contains(doc.Owner, "CredentialPolicyInstallation") || doc.JournalID != "" {
		return ProviderPolicyDialectWorkerV1, "worker_or_legacy_journal_requires_review", false
	}
	if !strings.HasPrefix(doc.Owner, "provider-balance/") {
		return ProviderPolicyDialectUnknown, "unproven_owner_namespace", false
	}
	return ProviderPolicyDialectDesktopV1, "", true
}

// MigrateProviderPolicyDialect upgrades proven Desktop rows, parks ambiguous /
// worker-legacy rows as pending, and refuses unsafe dialect downgrades. It never
// invents manualDisabled, never flips enabled, and never rewrites gate journals.
func (s *Store) MigrateProviderPolicyDialect(ctx context.Context, opts ProviderPolicyMigrateOptions) (ProviderPolicyMigrateReport, error) {
	var report ProviderPolicyMigrateReport
	report.DryRun = opts.DryRun
	if opts.Target == "" {
		opts.Target = ProviderPolicyDialectDesktopV1
	}
	before, err := s.providerPolicyDialect(ctx)
	if err != nil {
		return report, err
	}
	report.DialectBefore = before
	report.DialectAfter = before
	if before == opts.Target {
		return report, nil
	}
	if before == ProviderPolicyDialectDesktopV1 && opts.Target != ProviderPolicyDialectDesktopV1 {
		return report, ErrUnsafeProviderPolicyDowngrade
	}
	if opts.Target != ProviderPolicyDialectDesktopV1 {
		return report, fmt.Errorf("unsupported provider policy dialect target %q", opts.Target)
	}

	rows, err := s.db.QueryContext(ctx, `SELECT id,generation,document FROM provider_policies ORDER BY id`)
	if err != nil {
		return report, err
	}
	defer rows.Close()

	type row struct {
		id, document string
		generation   int64
	}
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.generation, &r.document); err != nil {
			return report, err
		}
		all = append(all, r)
	}
	if err := rows.Err(); err != nil {
		return report, err
	}
	report.BackedUp = len(all)

	type planned struct {
		row
		dialect string
		reason  string
		stamp   bool
		nextDoc string
	}
	var plan []planned
	for _, r := range all {
		dialect, reason, stamp := classifyProviderPolicyDocument(r.id, r.document)
		item := planned{row: r, dialect: dialect, reason: reason, stamp: stamp}
		if stamp {
			var body map[string]any
			if err := json.Unmarshal([]byte(r.document), &body); err != nil {
				return report, err
			}
			body["dialect"] = ProviderPolicyDialectDesktopV1
			raw, err := json.Marshal(body)
			if err != nil {
				return report, err
			}
			item.nextDoc = string(raw)
			report.Migrated++
		} else {
			report.Pending++
		}
		plan = append(plan, item)
	}

	if opts.DryRun {
		return report, nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return report, err
	}
	defer tx.Rollback()

	now := time.Now().UnixMilli()
	backupPayload := make([]map[string]any, 0, len(plan))
	for _, item := range plan {
		if _, err := tx.ExecContext(ctx, `INSERT INTO provider_policy_migration_backup(id,generation,document,backed_up_at_ms) VALUES(?,?,?,?)`, item.id, item.generation, item.document, now); err != nil {
			return report, err
		}
		backupPayload = append(backupPayload, map[string]any{
			"id": item.id, "generation": item.generation, "document": json.RawMessage(item.document),
		})
		if item.stamp {
			result, err := tx.ExecContext(ctx, `UPDATE provider_policies SET document=? WHERE id=? AND generation=? AND document=?`, item.nextDoc, item.id, item.generation, item.document)
			if err != nil {
				return report, err
			}
			n, err := result.RowsAffected()
			if err != nil {
				return report, err
			}
			if n != 1 {
				return report, ErrPolicyConflict
			}
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO provider_policy_migration_pending(id,generation,document,reason) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET generation=excluded.generation,document=excluded.document,reason=excluded.reason`, item.id, item.generation, item.document, item.reason); err != nil {
			return report, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO provider_policy_schema(singleton,dialect,version) VALUES(1,?,1) ON CONFLICT(singleton) DO UPDATE SET dialect=excluded.dialect,version=excluded.version`, opts.Target); err != nil {
		return report, err
	}
	if err := tx.Commit(); err != nil {
		return report, err
	}
	report.DialectAfter = opts.Target

	if opts.BackupDir != "" {
		if err := os.MkdirAll(opts.BackupDir, 0o755); err != nil {
			return report, err
		}
		raw, err := json.MarshalIndent(map[string]any{
			"atMs": now, "dialectBefore": before, "dialectAfter": opts.Target, "rows": backupPayload,
		}, "", "  ")
		if err != nil {
			return report, err
		}
		name := filepath.Join(opts.BackupDir, fmt.Sprintf("provider-policy-backup-%d.json", now))
		if err := os.WriteFile(name, raw, 0o600); err != nil {
			return report, err
		}
	}
	return report, nil
}

// RestoreProviderPolicyBackup replaces current provider_policies from a backup
// artifact written by MigrateProviderPolicyDialect. It refuses when the live
// dialect is Desktop v1 and the backup predates that protection (unsafe
// downgrade), and never touches credential secrets or gate journals.
func (s *Store) RestoreProviderPolicyBackup(ctx context.Context, backupPath string) error {
	raw, err := os.ReadFile(backupPath)
	if err != nil {
		return err
	}
	var artifact struct {
		DialectBefore string `json:"dialectBefore"`
		DialectAfter  string `json:"dialectAfter"`
		Rows          []struct {
			ID         string          `json:"id"`
			Generation int64           `json:"generation"`
			Document   json.RawMessage `json:"document"`
		} `json:"rows"`
	}
	if json.Unmarshal(raw, &artifact) != nil || len(artifact.Rows) == 0 {
		return ErrProviderPolicyBackupInvalid
	}
	for _, row := range artifact.Rows {
		if row.ID == "" || row.Generation <= 0 || !json.Valid(row.Document) {
			return ErrProviderPolicyBackupInvalid
		}
	}
	current, err := s.providerPolicyDialect(ctx)
	if err != nil {
		return err
	}
	if current == ProviderPolicyDialectDesktopV1 && artifact.DialectBefore != ProviderPolicyDialectDesktopV1 {
		return ErrUnsafeProviderPolicyDowngrade
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM provider_policies`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM provider_policy_migration_pending`); err != nil {
		return err
	}
	for _, row := range artifact.Rows {
		if _, err := tx.ExecContext(ctx, `INSERT INTO provider_policies(id,generation,document) VALUES(?,?,?)`, row.ID, row.Generation, string(row.Document)); err != nil {
			return err
		}
	}
	dialect := artifact.DialectBefore
	if dialect == "" {
		dialect = ProviderPolicyDialectUnknown
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO provider_policy_schema(singleton,dialect,version) VALUES(1,?,1) ON CONFLICT(singleton) DO UPDATE SET dialect=excluded.dialect,version=excluded.version`, dialect); err != nil {
		return err
	}
	return tx.Commit()
}
