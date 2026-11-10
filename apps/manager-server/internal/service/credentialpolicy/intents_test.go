package credentialpolicy_test

import (
	"context"
	"fmt"
	"testing"

	policy "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

func TestIntentsDoNotExposeMigratedPredecessorAsCurrentSwitch(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			ctx := context.Background()
			db := testutil.NewStore(t, testutil.NewConfig(t))
			old := store.CredentialPolicyRecord{ID: "previous", Generation: 1, Document: fmt.Sprintf(`{"auth_index":"same-issued-index","enabled":%t,"owner":"same"}`, enabled)}
			if err := db.SaveCredentialPolicy(ctx, old, 0); err != nil {
				t.Fatal(err)
			}
			current := store.CredentialPolicyRecord{ID: "current", Generation: 2, Document: fmt.Sprintf(`{"auth_index":"same-issued-index","enabled":%t,"owner":"same"}`, enabled)}
			if err := db.MigrateCredentialPolicyAuthority(ctx, old, current, `{"auth_index":"same-issued-index","enabled":false,"owner":"same","authorityReboundTo":"current"}`); err != nil {
				t.Fatal(err)
			}
			got, err := policy.New(db, nil, nil).Intents(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].AuthIndex != "same-issued-index" || got[0].Enabled != enabled {
				t.Fatalf("migrated predecessor overwrites the active switch: %+v", got)
			}
		})
	}
}
