package codexreset_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/codexreset"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

func TestClaimConsumptionIsUniquePerCredentialCycleRequest(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	repo := st.CodexResetCredits
	ctx := context.Background()
	claimed, err := repo.ClaimConsumption(ctx, "credential-a", "cycle-a", "request-a")
	if err != nil || !claimed {
		t.Fatalf("first claim = %t, %v", claimed, err)
	}
	claimed, err = repo.ClaimConsumption(ctx, "credential-a", "cycle-a", "request-a")
	if err != nil {
		t.Fatal(err)
	}
	if claimed {
		t.Fatal("duplicate claim succeeded")
	}
	claimed, err = repo.ClaimConsumption(ctx, "credential-a", "cycle-b", "request-a")
	if err != nil || !claimed {
		t.Fatalf("new cycle claim = %t, %v", claimed, err)
	}
}

func TestRecordObservationAndMarkConsumed(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	count := int64(2)
	if err := st.CodexResetCredits.RecordObservation(ctx, codexreset.LedgerEntry{CredentialKey: "credential-a", CycleKey: "cycle-a", RedeemRequestID: "request-a", Status: "observed", AvailableCount: &count, DetailJSON: `{"available_count":2}`}); err != nil {
		t.Fatal(err)
	}
	claimed, err := st.CodexResetCredits.ClaimConsumption(ctx, "credential-a", "cycle-a", "request-a")
	if err != nil {
		t.Fatal(err)
	}
	if claimed {
		t.Fatal("observation row should fence duplicate request")
	}
}
