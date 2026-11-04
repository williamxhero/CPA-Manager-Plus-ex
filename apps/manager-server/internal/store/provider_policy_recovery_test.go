package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

func TestValidateProviderRecoveryRequiresExactOffIntentAndLease(t *testing.T) {
	ctx := context.Background()
	db := openEvidenceStore(t)
	lease, err := db.AcquireCredentialPolicyLease(ctx, "00000000-0000-4000-8000-000000000010", 1000, 30000)
	if err != nil {
		t.Fatal(err)
	}
	off := store.ProviderPolicyRecord{ID: "policy-off", Generation: 1, Document: `{"enabled":false,"owner":"x"}`}
	if err := db.SaveProviderPolicy(ctx, off, store.ProviderPolicyRecord{}, lease, 1000); err != nil {
		t.Fatal(err)
	}
	if err := db.ValidateProviderRecovery(ctx, off, lease, 1001); err != nil {
		t.Fatal(err)
	}
	on := store.ProviderPolicyRecord{ID: "policy-off", Generation: 2, Document: `{"enabled":true,"owner":"x"}`}
	if err := db.SaveProviderPolicy(ctx, on, off, lease, 1001); err != nil {
		t.Fatal(err)
	}
	if err := db.ValidateProviderRecovery(ctx, on, lease, 1002); !errors.Is(err, store.ErrPolicyLease) {
		t.Fatalf("enabled policy accepted for recovery: %v", err)
	}
}
