package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

// Local capture/CAS seam only. No Core association or positive ON claim.
func TestCredentialAssociationCaptureIsAtomicImmutableAndSecretCASFenced(t *testing.T) {
	ctx := context.Background()
	protector, err := security.NewProtector([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "association.sqlite")
	db, err := store.Open(path, protector)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.SaveOfficialBalance(ctx, store.OfficialBalanceRecord{ID: "official", Config: `{"version":1}`}, 0, "SYNTHETIC-SECRET-CANARY", false); err != nil {
		t.Fatal(err)
	}
	source, err := db.OfficialBalance(ctx, "official")
	if err != nil {
		t.Fatal(err)
	}
	jobID := uuid.NewString()
	job := `{"id":"` + jobID + `","state":"running","version":1}`
	if err := db.BeginOfficialBalanceJob(ctx, "official", 1, jobID, job, `{"queryId":"`+jobID+`","queryStatus":"running"}`); err != nil {
		t.Fatal(err)
	}
	capture := store.CredentialAssociationCapture{ID: uuid.NewString(), JobID: jobID, SourceID: "official", ConfigVersion: 1, Config: source.Config, SecretRef: source.SecretRef, PolicyID: "subject", Job: job, Document: `{"nonce":"SYNTHETIC-NONSECRET-NONCE"}`}
	if err := db.PutCredentialAssociationCapture(ctx, capture); err != nil {
		t.Fatal(err)
	}
	if err := db.PutCredentialAssociationCapture(ctx, capture); err != nil {
		t.Fatal(err)
	}
	changed := capture
	changed.Document = `{"nonce":"CHANGED"}`
	if err := db.PutCredentialAssociationCapture(ctx, changed); err == nil {
		t.Fatal("duplicate capture mutated immutable nonce")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(path, protector)
	if err != nil {
		t.Fatal(err)
	}
	got, err := db.CredentialAssociationCapture(ctx, capture.ID)
	if err != nil || got != capture {
		t.Fatal("capture not restart durable")
	}
	if err := db.CheckCredentialAssociationCapture(ctx, capture, false); err != nil {
		t.Fatal(err)
	}
	// Synthetic local evidence envelope only: this tests atomic acceptance/CAS,
	// never a positive Core association or gate receipt.
	if err := db.AcceptCredentialAssociationEvidence(ctx, capture, `{"syntheticLocalOnly":true}`); err == nil {
		t.Fatal("running job accepted as completed evidence")
	}
	observation := `{"queryId":"` + jobID + `","queryStatus":"success"}`
	if err := db.CompleteOfficialBalance(ctx, "official", 1, jobID, observation, true); err != nil {
		t.Fatal(err)
	}
	completed := `{"id":"` + jobID + `","state":"complete","version":1,"status":{"attempt":` + observation + `}}`
	if err := db.SaveOfficialBalanceJob(ctx, jobID, "official", completed); err != nil {
		t.Fatal(err)
	}
	if err := db.AcceptCredentialAssociationEvidence(ctx, capture, `{"syntheticLocalOnly":true}`); err != nil {
		t.Fatal(err)
	}
	if err := db.AcceptCredentialAssociationEvidence(ctx, capture, `{"syntheticLocalOnly":false}`); err == nil {
		t.Fatal("accepted immutable evidence overwritten")
	}
	if err := db.SaveOfficialBalance(ctx, store.OfficialBalanceRecord{ID: "official", Config: source.Config}, 1, "DIFFERENT-SYNTHETIC-SECRET", false); err != nil {
		t.Fatal(err)
	}
	if err := db.CheckCredentialAssociationCapture(ctx, capture, false); err == nil {
		t.Fatal("same config body with replaced secret/CAS accepted")
	}
	if err := db.AcceptCredentialAssociationEvidence(ctx, capture, `{"syntheticLocalOnly":true}`); err == nil {
		t.Fatal("accepted evidence bypassed current secret CAS")
	}
	late := capture
	late.ID = uuid.NewString()
	if err := db.PutCredentialAssociationCapture(ctx, late); err == nil {
		t.Fatal("late capture bypassed official secretCAS")
	}
}
