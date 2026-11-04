package store_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

func openEvidenceStore(t *testing.T) *store.Store {
	t.Helper()
	p, err := security.NewProtector([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "evidence.sqlite"), p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func seedConfiguredSource(t *testing.T, db *store.Store, id string) store.OfficialBalanceFence {
	t.Helper()
	ctx := context.Background()
	if err := db.SaveOfficialBalance(ctx, store.OfficialBalanceRecord{ID: id, Config: `{}`}, 0, "secret-"+id, false); err != nil {
		t.Fatal(err)
	}
	job := "job-" + id
	if err := db.BeginOfficialBalanceJob(ctx, id, 1, job, `{}`, `{"queryId":"`+job+`","queryStatus":"running"}`); err != nil {
		t.Fatal(err)
	}
	if err := db.CompleteOfficialBalance(ctx, id, 1, job, `{"queryId":"`+job+`","queryStatus":"success"}`, true); err != nil {
		t.Fatal(err)
	}
	_, fence, err := db.OfficialBalanceWithFence(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return fence
}

func TestProviderPolicyEvidenceUsesExactSQLiteSourceSnapshot(t *testing.T) {
	ctx := context.Background()
	db := openEvidenceStore(t)
	holder := "00000000-0000-4000-8000-000000000001"
	lease, err := db.AcquireCredentialPolicyLease(ctx, holder, 1000, 30000)
	if err != nil {
		t.Fatal(err)
	}
	fence := seedConfiguredSource(t, db, "source-a")
	previous := store.ProviderPolicyRecord{}
	record := store.ProviderPolicyRecord{ID: "policy-a", Generation: 1, Document: `{"enabled":true}`}
	if err := db.SaveProviderPolicy(ctx, record, previous, lease, 1000); err != nil {
		t.Fatal(err)
	}
	if err := db.ValidateProviderPolicyEvidence(ctx, record, lease, 1001, []store.OfficialBalanceFence{fence}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveOfficialBalance(ctx, store.OfficialBalanceRecord{ID: "source-a", Config: `{}`}, 1, "new-secret", false); err != nil {
		t.Fatal(err)
	}
	if err := db.ValidateProviderPolicyEvidence(ctx, record, lease, 1002, []store.OfficialBalanceFence{fence}); !errors.Is(err, store.ErrProviderPolicyEvidenceConflict) {
		t.Fatalf("stale source fence was accepted: %v", err)
	}
}

func TestProviderPolicyEvidenceRejectsEmptyDuplicateAndOversizeFences(t *testing.T) {
	ctx := context.Background()
	db := openEvidenceStore(t)
	lease, err := db.AcquireCredentialPolicyLease(ctx, "00000000-0000-4000-8000-000000000002", 1000, 30000)
	if err != nil {
		t.Fatal(err)
	}
	fence := seedConfiguredSource(t, db, "source-a")
	record := store.ProviderPolicyRecord{ID: "policy-a", Generation: 1, Document: `{"enabled":true}`}
	if err := db.SaveProviderPolicy(ctx, record, store.ProviderPolicyRecord{}, lease, 1000); err != nil {
		t.Fatal(err)
	}
	if err := db.ValidateProviderPolicyEvidence(ctx, record, lease, 1001, nil); !errors.Is(err, store.ErrProviderPolicyEvidenceConflict) {
		t.Fatalf("empty fences accepted: %v", err)
	}
	if err := db.ValidateProviderPolicyEvidence(ctx, record, lease, 1001, []store.OfficialBalanceFence{fence, fence}); !errors.Is(err, store.ErrProviderPolicyEvidenceConflict) {
		t.Fatalf("duplicate source fence accepted: %v", err)
	}
	oversize := make([]store.OfficialBalanceFence, 0, 65)
	for i := 0; i < 65; i++ {
		id := fmt.Sprintf("source-%02d", i)
		oversize = append(oversize, seedConfiguredSource(t, db, id))
	}
	if err := db.ValidateProviderPolicyEvidence(ctx, record, lease, 1001, oversize); !errors.Is(err, store.ErrProviderPolicyEvidenceConflict) {
		t.Fatalf("65-source fence accepted: %v", err)
	}
}

func TestProviderPolicyEvidenceRejectsPolicyLeaseAndSecretMismatches(t *testing.T) {
	ctx := context.Background()
	db := openEvidenceStore(t)
	lease, err := db.AcquireCredentialPolicyLease(ctx, "00000000-0000-4000-8000-000000000003", 1000, 30000)
	if err != nil {
		t.Fatal(err)
	}
	fence := seedConfiguredSource(t, db, "source-a")
	record := store.ProviderPolicyRecord{ID: "policy-a", Generation: 1, Document: `{"enabled":true}`}
	if err := db.SaveProviderPolicy(ctx, record, store.ProviderPolicyRecord{}, lease, 1000); err != nil {
		t.Fatal(err)
	}
	if err := db.ValidateProviderPolicyEvidence(ctx, store.ProviderPolicyRecord{ID: "policy-a", Generation: 2, Document: record.Document}, lease, 1001, []store.OfficialBalanceFence{fence}); !errors.Is(err, store.ErrProviderPolicyEvidenceConflict) {
		t.Fatalf("generation mismatch accepted: %v", err)
	}
	if err := db.ValidateProviderPolicyEvidence(ctx, store.ProviderPolicyRecord{ID: "policy-a", Generation: 1, Document: `{"enabled":true,"note":"x"}`}, lease, 1001, []store.OfficialBalanceFence{fence}); !errors.Is(err, store.ErrProviderPolicyEvidenceConflict) {
		t.Fatalf("document mismatch accepted: %v", err)
	}
	if err := db.ValidateProviderPolicyEvidence(ctx, record, lease, 40000, []store.OfficialBalanceFence{fence}); !errors.Is(err, store.ErrPolicyLease) {
		t.Fatalf("expired lease accepted: %v", err)
	}
	wrongLease := store.CredentialPolicyLease{Holder: "00000000-0000-4000-8000-000000000099", Epoch: lease.Epoch}
	if err := db.ValidateProviderPolicyEvidence(ctx, record, wrongLease, 1001, []store.OfficialBalanceFence{fence}); !errors.Is(err, store.ErrPolicyLease) {
		t.Fatalf("wrong lease accepted: %v", err)
	}
	disabled := store.ProviderPolicyRecord{ID: "policy-a", Generation: 2, Document: `{"enabled":false}`}
	if err := db.SaveProviderPolicy(ctx, disabled, record, lease, 1001); err != nil {
		t.Fatal(err)
	}
	if err := db.ValidateProviderPolicyEvidence(ctx, disabled, lease, 1002, []store.OfficialBalanceFence{fence}); !errors.Is(err, store.ErrProviderPolicyEvidenceConflict) {
		t.Fatalf("disabled policy accepted: %v", err)
	}
	if err := db.SaveOfficialBalance(ctx, store.OfficialBalanceRecord{ID: "source-b", Config: `{}`}, 0, "", false); err != nil {
		t.Fatal(err)
	}
	_, bare, err := db.OfficialBalanceWithFence(ctx, "source-b")
	if err != nil {
		t.Fatal(err)
	}
	enabled := store.ProviderPolicyRecord{ID: "policy-a", Generation: 3, Document: `{"enabled":true}`}
	if err := db.SaveProviderPolicy(ctx, enabled, disabled, lease, 1002); err != nil {
		t.Fatal(err)
	}
	if err := db.ValidateProviderPolicyEvidence(ctx, enabled, lease, 1003, []store.OfficialBalanceFence{bare}); !errors.Is(err, store.ErrProviderPolicyEvidenceConflict) {
		t.Fatalf("unconfigured secret accepted: %v", err)
	}
}
