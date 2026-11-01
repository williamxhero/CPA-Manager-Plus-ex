package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

// SQLite public persistence seam only. This is not a positive Core gate test.
func TestCredentialPolicyLeaseFencesConcurrentAndRestartedCoordinators(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "policy.sqlite")
	first, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	a := "00000000-0000-4000-8000-000000000001"
	b := "00000000-0000-4000-8000-000000000002"
	lease, err := first.AcquireCredentialPolicyLease(ctx, a, 1000, 30000)
	if err != nil {
		t.Fatal(err)
	}
	if lease.Epoch != 1 || lease.ExpiresMS != 31000 {
		t.Fatalf("bad initial lease %+v", lease)
	}
	if _, err := second.AcquireCredentialPolicyLease(ctx, b, 1001, 30000); !errors.Is(err, store.ErrPolicyLease) {
		t.Fatalf("second coordinator admitted: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if _, err := restarted.AcquireCredentialPolicyLease(ctx, b, 2000, 30000); !errors.Is(err, store.ErrPolicyLease) {
		t.Fatalf("restart silently stole unexpired lease: %v", err)
	}
	next, err := restarted.AcquireCredentialPolicyLease(ctx, b, 31000, 30000)
	if err != nil {
		t.Fatal(err)
	}
	if next.Epoch != 2 {
		t.Fatal("lease epoch not durable")
	}
	if err := second.CheckCredentialPolicyLease(ctx, lease, 31001); !errors.Is(err, store.ErrPolicyLease) {
		t.Fatal("expired holder could still write")
	}
	if err := second.ReleaseCredentialPolicyLease(ctx, lease); !errors.Is(err, store.ErrPolicyLease) {
		t.Fatal("old holder released new lease")
	}
	if err := restarted.CheckCredentialPolicyLease(ctx, next, 31001); err != nil {
		t.Fatal(err)
	}
	if err := restarted.ReleaseCredentialPolicyLease(ctx, next); err != nil {
		t.Fatal(err)
	}
	third, err := second.AcquireCredentialPolicyLease(ctx, a, 31002, 30000)
	if err != nil {
		t.Fatal(err)
	}
	if third.Epoch != 3 {
		t.Fatal("release reset epoch and made an old lease reusable")
	}
}

func TestCredentialPolicyJournalPersistsAndOFFFencesPendingAction(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "policy.sqlite")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	id, err := db.CredentialPolicyInstallation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	owner := "cpa-manager.credential-policy.v1/" + id
	// Local synthetic intent at the PUBLIC store seam only; not an ON API/gate ACK.
	subject := strings.Repeat("a", 64)
	doc := `{"enabled":true,"owner":"` + owner + `"}`
	if err := db.SaveCredentialPolicy(ctx, store.CredentialPolicyRecord{ID: subject, Generation: 1, Document: doc}, 0); err != nil {
		t.Fatal(err)
	}
	lease, err := db.AcquireCredentialPolicyLease(ctx, "00000000-0000-4000-8000-000000000001", 1000, 30000)
	if err != nil {
		t.Fatal(err)
	}
	operation := store.CredentialPolicyOperation{ID: "00000000-0000-4000-8000-000000000003", SubjectID: subject, Generation: 1, Owner: owner, Automatic: true, Body: `{"action":"pause"}`}
	saved, err := db.BeginCredentialPolicyOperation(ctx, operation, lease, 1001)
	if err != nil {
		t.Fatal(err)
	}
	if saved.State != "queued" || saved.Revision != 1 {
		t.Fatalf("intent was not durable before RPC: %+v", saved)
	}
	duplicate, err := db.BeginCredentialPolicyOperation(ctx, operation, lease, 1002)
	if err != nil || duplicate.ID != saved.ID || duplicate.Revision != 1 {
		t.Fatal("idempotent journal retry changed operation")
	}
	operation.Body = `{"action":"clear"}`
	if _, err := db.BeginCredentialPolicyOperation(ctx, operation, lease, 1003); !errors.Is(err, store.ErrPolicyOperationConflict) {
		t.Fatal("same opId with different body accepted")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := db.CredentialPolicyOperation(ctx, saved.ID)
	if err != nil || restored.Body != saved.Body || restored.State != "queued" {
		t.Fatal("journal lost on restart")
	}
	if err := db.SaveCredentialPolicy(ctx, store.CredentialPolicyRecord{ID: subject, Generation: 2, Document: `{"enabled":false,"owner":"` + owner + `"}`}, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CheckCredentialPolicyOperation(ctx, saved.ID, lease, 1004); !errors.Is(err, store.ErrPolicyConflict) {
		t.Fatalf("OFF did not fence delayed automatic send: %v", err)
	}
	restored, err = db.CredentialPolicyOperation(ctx, saved.ID)
	if err != nil || restored.State != "superseded" {
		t.Fatalf("pending operation not visibly superseded: %+v %v", restored, err)
	}
	// A late RPC completion cannot publish an applied receipt into this new OFF generation.
	if err := db.CompleteCredentialPolicyOperation(ctx, saved.ID, saved.Revision, "pending", "", "gate_core_unavailable", lease, 1004); !errors.Is(err, store.ErrPolicyConflict) {
		t.Fatalf("late result overwrote OFF fence: %v", err)
	}
}

func TestCredentialPolicyStartedFenceCannotCarryRecoveryAcrossOFFON(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "policy.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	installation, err := db.CredentialPolicyInstallation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	owner := "cpa-manager.credential-policy.v1/" + installation
	subject := strings.Repeat("b", 64)
	on := `{"enabled":true,"owner":"` + owner + `"}`
	off := `{"enabled":false,"owner":"` + owner + `"}`
	if err := db.SaveCredentialPolicy(ctx, store.CredentialPolicyRecord{ID: subject, Generation: 1, Document: on}, 0); err != nil {
		t.Fatal(err)
	}
	fence := store.CredentialPolicyQueryFence{AttemptID: "00000000-0000-4000-8000-000000000009", SubjectID: subject, Generation: 1, Owner: owner, Source: `{"version":3}`, Enabled: true}
	if err := db.CaptureCredentialPolicyQuery(ctx, fence); err != nil {
		t.Fatal(err)
	}
	if err := db.CheckCredentialPolicyQuery(ctx, fence); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveCredentialPolicy(ctx, store.CredentialPolicyRecord{ID: subject, Generation: 2, Document: off}, 1); err != nil {
		t.Fatal(err)
	}
	if err := db.CheckCredentialPolicyQuery(ctx, fence); !errors.Is(err, store.ErrPolicyConflict) {
		t.Fatal("OFF did not invalidate in-flight recovery fence")
	}
	if err := db.SaveCredentialPolicy(ctx, store.CredentialPolicyRecord{ID: subject, Generation: 3, Document: on}, 2); err != nil {
		t.Fatal(err)
	}
	if err := db.CheckCredentialPolicyQuery(ctx, fence); !errors.Is(err, store.ErrPolicyConflict) {
		t.Fatal("old generation accepted after OFF/ON")
	}
	fence.Generation = 3
	if err := db.CaptureCredentialPolicyQuery(ctx, fence); !errors.Is(err, store.ErrPolicyConflict) {
		t.Fatal("singleflight/replayed start acquired a newer generation")
	}
	fence.AttemptID = "00000000-0000-4000-8000-000000000010"
	if err := db.CaptureCredentialPolicyQuery(ctx, fence); err != nil {
		t.Fatal(err)
	}
	if err := db.CheckCredentialPolicyQuery(ctx, fence); err != nil {
		t.Fatal(err)
	}
	fence.Source = `{"version":4}`
	if err := db.CheckCredentialPolicyQuery(ctx, fence); !errors.Is(err, store.ErrPolicyConflict) {
		t.Fatal("source version changed underneath captured evidence")
	}
}

func TestCredentialPolicyDispatchPersistsUnknownOutcomeBeforeRPCAndFreezesBody(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "policy.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	installation, err := db.CredentialPolicyInstallation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	owner := "cpa-manager.credential-policy.v1/" + installation
	subject := strings.Repeat("c", 64)
	if err := db.SaveCredentialPolicy(ctx, store.CredentialPolicyRecord{ID: subject, Generation: 1, Document: `{"enabled":true,"owner":"` + owner + `"}`}, 0); err != nil {
		t.Fatal(err)
	}
	lease, err := db.AcquireCredentialPolicyLease(ctx, "00000000-0000-4000-8000-000000000001", 1000, 30000)
	if err != nil {
		t.Fatal(err)
	}
	op, err := db.BeginCredentialPolicyOperation(ctx, store.CredentialPolicyOperation{ID: "00000000-0000-4000-8000-000000000002", SubjectID: subject, Generation: 1, Owner: owner, Automatic: true, Body: `{"planned":false}`}, lease, 1001)
	if err != nil {
		t.Fatal(err)
	}
	planned, err := db.PlanCredentialPolicyOperation(ctx, op.ID, op.Revision, `{"planned":true,"expectedGateRevision":7}`, lease, 1002)
	if err != nil {
		t.Fatal(err)
	}
	sending, err := db.ClaimCredentialPolicyOperation(ctx, planned.ID, planned.Revision, lease, 1003)
	if err != nil {
		t.Fatal(err)
	}
	durable, err := db.CredentialPolicyOperation(ctx, op.ID)
	if err != nil || !durable.Attempted || durable.State != "pending" || durable.LastError != "outcome_unverified" {
		t.Fatal("RPC could begin before durable pending/unknown outcome")
	}
	if _, err := db.PlanCredentialPolicyOperation(ctx, op.ID, sending.Revision, `{"changed":true}`, lease, 1004); !errors.Is(err, store.ErrPolicyOperationConflict) {
		t.Fatal("sent opId body could be changed")
	}
	if _, err := db.ClaimCredentialPolicyOperation(ctx, op.ID, planned.Revision, lease, 1005); !errors.Is(err, store.ErrPolicyConflict) {
		t.Fatal("stale dispatch claim reused")
	}
}
