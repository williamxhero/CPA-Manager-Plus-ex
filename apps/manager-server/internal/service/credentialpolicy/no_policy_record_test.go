package credentialpolicy_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	policy "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy"
	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

// "No policy record yet" and "stored, but not participating" must not share a
// word: the first names the absence, the second is refused by the query fence.
func TestCredentialPolicyCompletionDistinguishesNoRecordFromNonParticipation(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewStore(t, testutil.NewConfig(t))
	source, now := windowEvidence(t)
	coordinator := policy.New(db, fixedPolicySources{source.Source}, nil, policy.WithClock(func() time.Time { return now }))
	q := cq.Query{Binding: source.Binding, SourceVersion: source.SourceVersion, AuthIndex: source.AuthIndex, Version: source.Version}
	out, err := coordinator.CompleteCredentialQuery(ctx, q, source, uuid.NewString())
	if err != nil || out.Decision.Action != "none" || out.Decision.Reason != "no_policy_record" {
		t.Fatalf("absent policy record was not named as such: %+v %v", out, err)
	}
	installation, err := db.CredentialPolicyInstallation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	id := policy.SubjectID(source.Source)
	state := policy.Status{Source: source.Source, SubjectID: id, Owner: policy.OwnerNamespace + installation, Generation: 1, Enabled: false, State: "off"}
	document, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	// A historical disabled record only proves lifetime of the intent; it is
	// never a close-intent reason and never yields evidence.
	if err := db.SaveCredentialPolicy(ctx, store.CredentialPolicyRecord{ID: id, Generation: 1, Document: string(document)}, 0); err != nil {
		t.Fatal(err)
	}
	out, err = coordinator.CompleteCredentialQuery(ctx, q, source, uuid.NewString())
	if err != nil || out.Decision.Action != "none" || out.Decision.Reason != "policy_generation_changed" {
		t.Fatalf("non-participating record was not refused by the query fence: %+v %v", out, err)
	}
	if out.Decision.Reason == "no_policy_record" || out.Decision.Reason == "policy_off" {
		t.Fatalf("non-participation reused an absence/close-intent reason: %+v", out)
	}
}
