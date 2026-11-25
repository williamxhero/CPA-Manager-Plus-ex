package store_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

// Start from a database that a pre-#93 build could produce: a stored close
// intent at generation 2 together with an automatic action still in flight at
// that same generation. Opening the store runs the real startup migration
// entry; the record must participate again, advance one generation, and the
// in-flight old-generation action must stay fenced by that generation alone.
func TestCredentialPolicyStartupBackfillFencesInFlightOldGenerationAction(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy-off-policy.sqlite")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	installation, err := db.CredentialPolicyInstallation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	owner := "cpa-manager.credential-policy.v1/" + installation
	subject := "legacy-off-subject"
	legacyOff := `{"subjectId":"` + subject + `","enabled":false,"generation":2,"owner":"` + owner + `","auth_index":"issued-index-1","scope":{"bootEpoch":"boot-1","revision":9},"state":"off","reason":"legacy_manual_off","applied":true,"authorityReboundTo":"rebound-subject","targets":["credential-1"]}`

	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`INSERT INTO credential_policies(id,generation,document) VALUES(?,?,?)`, subject, 2, legacyOff); err != nil {
		t.Fatalf("insert legacy close intent: %v", err)
	}
	if _, err := legacy.Exec(`INSERT INTO credential_policy_operations(id,subject_id,generation,owner,automatic,state,body,result,last_error,revision,attempted) VALUES('op-legacy',?,2,?,1,'queued','','','',1,0)`, subject, owner); err != nil {
		t.Fatalf("insert in-flight old-generation action: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = store.Open(path)
	if err != nil {
		t.Fatalf("startup migration over legacy close intent: %v", err)
	}
	defer db.Close()
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()

	record, err := db.CredentialPolicy(ctx, subject)
	if err != nil {
		t.Fatal(err)
	}
	if record.Generation != 3 {
		t.Fatalf("startup backfill generation = %d, want 3", record.Generation)
	}
	var document, legacyDocument map[string]any
	if err := json.Unmarshal([]byte(record.Document), &document); err != nil {
		t.Fatalf("backfilled document unreadable: %v", err)
	}
	var storedGeneration int64
	if err := raw.QueryRow(`SELECT json_extract(document,'$.generation') FROM credential_policies WHERE id=?`, subject).Scan(&storedGeneration); err != nil {
		t.Fatal(err)
	}
	if storedGeneration != record.Generation {
		t.Fatalf("document generation %d != generation column %d", storedGeneration, record.Generation)
	}
	if document["enabled"] != true {
		t.Fatalf("legacy close intent still not participating: %v", document["enabled"])
	}
	if err := json.Unmarshal([]byte(legacyOff), &legacyDocument); err != nil {
		t.Fatal(err)
	}
	delete(legacyDocument, "enabled")
	delete(legacyDocument, "generation")
	delete(document, "enabled")
	delete(document, "generation")
	if !reflect.DeepEqual(legacyDocument, document) {
		t.Fatalf("backfill changed other stored fields: want %v got %v", legacyDocument, document)
	}

	// Every per-credential intent now reads as participating.
	records, err := db.CredentialPolicies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("expected exactly the backfilled record, got %d", len(records))
	}
	for _, r := range records {
		var intent struct {
			Enabled *bool `json:"enabled"`
		}
		if err := json.Unmarshal([]byte(r.Document), &intent); err != nil || intent.Enabled == nil || !*intent.Enabled {
			t.Fatalf("intent for %s is not participating: %s (err=%v)", r.ID, r.Document, err)
		}
	}

	// The migration writes no operation receipt and leaves no new schedule hint.
	var operations int
	if err := raw.QueryRow(`SELECT count(*) FROM credential_policy_operations`).Scan(&operations); err != nil {
		t.Fatal(err)
	}
	if operations != 1 {
		t.Fatalf("startup backfill emitted operation receipts: %d", operations)
	}
	var hints int
	if err := raw.QueryRow(`SELECT count(*) FROM credential_policy_schedule_hints`).Scan(&hints); err != nil {
		t.Fatal(err)
	}
	if hints != 0 {
		t.Fatalf("startup backfill wrote schedule hints: %d", hints)
	}

	// The in-flight automatic action is fenced by the generation advance alone:
	// intent is now enabled, so enabled=0 cannot be the reason it is rejected.
	now := int64(1_760_000_000_000)
	lease, err := db.AcquireCredentialPolicyLease(ctx, "00000000-0000-4000-8000-000000000091", now, 30000)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CheckCredentialPolicyOperation(ctx, "op-legacy", lease, now+1); !errors.Is(err, store.ErrPolicyConflict) {
		t.Fatalf("in-flight old-generation action survived the backfill: %v", err)
	}
	if _, err := db.BeginCredentialPolicyOperation(ctx, store.CredentialPolicyOperation{ID: "00000000-0000-4000-8000-000000000092", SubjectID: subject, Generation: 2, Owner: owner, Automatic: true, Body: `{"action":"pause"}`}, lease, now+1); !errors.Is(err, store.ErrPolicyConflict) {
		t.Fatalf("stale generation replayed after the backfill: %v", err)
	}
	restored, err := db.CredentialPolicyOperation(ctx, "op-legacy")
	if err != nil || restored.Generation != 2 || restored.State != "queued" {
		t.Fatalf("in-flight action was rewritten by the backfill: %+v %v", restored, err)
	}

	// A second startup changes nothing: no further generation advance.
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	again, err := db.CredentialPolicy(ctx, subject)
	if err != nil {
		t.Fatal(err)
	}
	if again != record {
		t.Fatalf("second startup changed the backfilled record: %+v want %+v", again, record)
	}
	if err := raw.QueryRow(`SELECT count(*) FROM credential_policy_operations`).Scan(&operations); err != nil {
		t.Fatal(err)
	}
	if operations != 1 {
		t.Fatalf("second startup emitted operation receipts: %d", operations)
	}
}
