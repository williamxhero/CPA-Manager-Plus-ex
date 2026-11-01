package httpapi

import (
	"testing"
	"time"

	cp "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy"
	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
)

// Real A association exists first. A later literal profile/secret CAS invalidates
// it even though independent billing remains authorized and returns finite data.
// The new billing secret is NOT either selected inference key; Core must not
// associate by provider label/group/domain or carry the old successful proof.
func TestCredentialPolicyActualOfficialProfileSecretCASPreservesBillingNotOldProof(t *testing.T) {
	runSelectedKeyPolicyHTTP(t, false, false, false, func(f *selectedKeyPolicyFixture) {
		f.onPrerequisite = func(original ob.Status, selected, other cq.Source) {
			f.billingOnlyKey.Store(true)
			changed := original.Config
			protocol := *changed.NewAPI
			protocol.InstanceVersion = "synthetic-selected-key-revised-profile-v2"
			changed.NewAPI = &protocol // literal profile changes, same query budget
			var configured ob.Status
			f.api("PUT", balancePath, ob.Update{Config: changed, Secret: "BILLING-ONLY-KEY"}, 200, &configured)
			if configured.Version != original.Version+1 {
				t.Fatal("actual profile/secret CAS did not advance")
			}
			assertNoON := func(src cq.Source) {
				generation, enabled := int64(0), true
				var response struct {
					Error string `json:"error"`
				}
				f.api("PUT", "/usage-service/credential-policy", cp.Update{Binding: src.Binding, AuthIndex: src.AuthIndex, SourceVersion: src.SourceVersion, ExpectedGeneration: &generation, Enabled: &enabled}, 503, &response)
				if response.Error != "verified_selected_key_evidence_required" {
					t.Fatalf("wrong precise changed-proof reason: %s", response.Error)
				}
				label := "B"
				if src.AuthIndex == selected.AuthIndex {
					label = "A"
				}
				state := f.status(label)
				if state.Enabled || state.Generation != 0 || state.Applied || state.Operation != nil {
					t.Fatal("changed/foreign billing proof persisted policy intent or gate")
				}
			}
			assertNoON(selected) // old REAL positive A proof must fail local CAS
			var job ob.Job
			f.api("POST", balancePath, ob.Query{Binding: configured.Binding, Version: configured.Version}, 202, &job)
			if job.Version != configured.Version {
				t.Fatal("new profile joined a different-version completed job")
			}
			deadline := time.After(5 * time.Second)
			for {
				select {
				case event := <-f.completed:
					if event.AttemptID != job.ID {
						continue
					}
					if event.Official == nil || event.Official.Attempt == nil || event.Official.Attempt.QueryStatus != "success" || event.Official.Attempt.Semantic != "key_remaining" {
						t.Fatal("association mismatch broke independently authorized billing")
					}
					assertNoON(selected)
					assertNoON(other)
					if f.quotaCalls[0].Load() != 2 || f.sends[0].Load() != 0 || f.sends[1].Load() != 0 {
						t.Fatal("CAS negative duplicated billing or sent inference")
					}
					return
				case <-deadline:
					t.Fatal("same76 changed-profile billing completion missing")
				}
			}
		}
	})
}
