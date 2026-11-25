package sqlite

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
)

func TestMigrateBackfillsLegacyDisabledCredentialPolicyIntent(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "credential-policy-intent.sqlite"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	const owner = "cpa-manager.credential-policy.v1/5f6a1c1e-0000-4000-8000-000000000001"
	legacyOff := `{"subjectId":"legacy-off","enabled":false,"generation":4,"owner":"` + owner + `","auth_index":"issued-index-1","scope":{"bootEpoch":"boot-1","revision":7},"state":"off","reason":"legacy_manual_off","applied":true,"authorityReboundTo":"rebound-subject","targets":["credential-1"],"availability":{"state":"available"}}`
	alreadyOn := `{"subjectId":"already-on","enabled":true,"generation":3,"owner":"` + owner + `","state":"pending","reason":"quota_native_key_exhausted"}`
	brokenJSON := `{"subjectId":"broken-json","enabled":false,"generation":2,`
	noEnabled := `{"subjectId":"no-enabled","generation":1,"owner":"` + owner + `","state":"pending"}`
	generationDrift := `{"subjectId":"generation-drift","enabled":false,"generation":9,"owner":"` + owner + `","state":"off"}`

	insertPolicy := func(id string, generation int64, document string) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO credential_policies(id,generation,document) VALUES(?,?,?)`, id, generation, document); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}
	insertPolicy("legacy-off", 4, legacyOff)
	insertPolicy("already-on", 3, alreadyOn)
	insertPolicy("broken-json", 2, brokenJSON)
	insertPolicy("no-enabled", 1, noEnabled)
	insertPolicy("generation-drift", 6, generationDrift)
	if _, err := db.Exec(`INSERT INTO credential_policy_operations(id,subject_id,generation,owner,automatic,state,body,result,last_error,revision,attempted) VALUES('op-legacy','legacy-off',4,?,1,'queued','','','',1,0)`, owner); err != nil {
		t.Fatalf("insert in-flight operation: %v", err)
	}

	if err := Migrate(db); err != nil {
		t.Fatalf("migrate legacy credential policy intent: %v", err)
	}

	generation, document := readCredentialPolicyRow(t, db, "legacy-off")
	if generation != 5 {
		t.Fatalf("backfill did not advance the stored generation column: %d", generation)
	}
	backfilled := decodeCredentialPolicyDocument(t, document)
	if backfilled["enabled"] != true {
		t.Fatalf("legacy close intent was not backfilled to participating: %v", backfilled["enabled"])
	}
	if backfilled["generation"] != float64(5) {
		t.Fatalf("document generation and column generation diverged: %v", backfilled["generation"])
	}
	original := decodeCredentialPolicyDocument(t, legacyOff)
	delete(original, "enabled")
	delete(original, "generation")
	delete(backfilled, "enabled")
	delete(backfilled, "generation")
	if !reflect.DeepEqual(original, backfilled) {
		t.Fatalf("backfill changed stored fields: want %v got %v", original, backfilled)
	}

	// Rows that were already participating, unparsable, or had no explicit
	// intent are byte-for-byte untouched.
	for _, want := range []struct {
		id         string
		generation int64
		document   string
	}{
		{id: "already-on", generation: 3, document: alreadyOn},
		{id: "broken-json", generation: 2, document: brokenJSON},
		{id: "no-enabled", generation: 1, document: noEnabled},
		{id: "generation-drift", generation: 6, document: generationDrift},
	} {
		generation, document := readCredentialPolicyRow(t, db, want.id)
		if generation != want.generation || document != want.document {
			t.Fatalf("%s was rewritten by the backfill: (%d,%s)", want.id, generation, document)
		}
	}

	var operations int
	if err := db.QueryRow(`SELECT count(*) FROM credential_policy_operations`).Scan(&operations); err != nil {
		t.Fatal(err)
	}
	if operations != 1 {
		t.Fatalf("backfill emitted operation receipts: %d", operations)
	}
	var operationGeneration int64
	var operationState string
	if err := db.QueryRow(`SELECT generation,state FROM credential_policy_operations WHERE id='op-legacy'`).Scan(&operationGeneration, &operationState); err != nil {
		t.Fatal(err)
	}
	if operationGeneration != 4 || operationState != "queued" {
		t.Fatalf("backfill rewrote the in-flight operation: (%d,%s)", operationGeneration, operationState)
	}

	before := credentialPolicyRows(t, db)
	if err := Migrate(db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if after := credentialPolicyRows(t, db); !reflect.DeepEqual(before, after) {
		t.Fatalf("second startup changed credential policy rows: want %v got %v", before, after)
	}
}

func readCredentialPolicyRow(t *testing.T, db *sql.DB, id string) (int64, string) {
	t.Helper()
	var generation int64
	var document string
	if err := db.QueryRow(`SELECT generation,document FROM credential_policies WHERE id=?`, id).Scan(&generation, &document); err != nil {
		t.Fatalf("read credential policy %s: %v", id, err)
	}
	return generation, document
}

func credentialPolicyRows(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT id,generation,document FROM credential_policies ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id, document string
		var generation int64
		if err := rows.Scan(&id, &generation, &document); err != nil {
			t.Fatal(err)
		}
		out = append(out, id+"|"+strconv.FormatInt(generation, 10)+"|"+document)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func decodeCredentialPolicyDocument(t *testing.T, document string) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(document), &decoded); err != nil {
		t.Fatalf("decode credential policy document: %v", err)
	}
	return decoded
}
