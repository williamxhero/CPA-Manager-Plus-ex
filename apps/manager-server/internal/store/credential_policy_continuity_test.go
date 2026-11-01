package store_test

import (
	"context"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

// Local atomic intent migration seam only; caller still needs an actual Core
// certificate. This fixture supplies neither certificate nor positive ON proof.
func TestCredentialPolicyCertifiedMigrationCASDoesNotOverrideNewIntent(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewStore(t, testutil.NewConfig(t))
	old := store.CredentialPolicyRecord{ID: "old", Generation: 1, Document: `{"enabled":false,"owner":"same"}`}
	if err := db.SaveCredentialPolicy(ctx, old, 0); err != nil {
		t.Fatal(err)
	}
	next := store.CredentialPolicyRecord{ID: "new", Generation: 2, Document: `{"enabled":false,"owner":"same","scope":{}}`}
	tomb := `{"enabled":false,"owner":"same","authorityReboundTo":"new"}`
	if err := db.MigrateCredentialPolicyAuthority(ctx, old, next, tomb); err != nil {
		t.Fatal(err)
	}
	current, err := db.CredentialPolicy(ctx, "new")
	if err != nil || current != next {
		t.Fatal("new authority intent not durably committed")
	}
	stale := next
	stale.ID = "another"
	if err := db.MigrateCredentialPolicyAuthority(ctx, old, stale, tomb); err == nil {
		t.Fatal("stale previous authority migrated twice")
	}
	competitor := store.CredentialPolicyRecord{ID: "replacement", Generation: 1, Document: `{"enabled":false,"owner":"same"}`}
	if err := db.SaveCredentialPolicy(ctx, competitor, 0); err != nil {
		t.Fatal(err)
	}
	desired := next
	desired.ID = "replacement"
	desired.Generation = 3
	if err := db.MigrateCredentialPolicyAuthority(ctx, next, desired, tomb); err == nil {
		t.Fatal("independent newer lifecycle intent overwritten")
	}
	if got, err := db.CredentialPolicy(ctx, "new"); err != nil || got != next {
		t.Fatal("conflicting migration was not atomic")
	}
}
