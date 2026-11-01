package association_test

import (
	"bytes"
	"encoding/json"
	"testing"

	a "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy/association"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
)

// Pure response association matching, NOT an actual producer proof or Gate ACK.
func TestAssociationResultRequiresExactOriginalCaptureAndStrictWire(t *testing.T) {
	r, encoded, err := a.Prepare(goldenRequest(t), []byte("SYNTHETIC-ASSOCIATION-VECTOR-KEY"))
	if err != nil {
		t.Fatal(err)
	}
	result := a.Result{Version: 1, Associated: true, Reason: "selected_credential_association", Selected: r.Selected, AuthIndex: r.AuthIndex, SourceVersion: r.SourceVersion, Origin: r.Origin, OfficialBinding: ob.Binding{Scope: r.Official.Scope, Targets: r.Official.Targets}, OfficialVersion: r.Official.Version, QueryID: r.QueryID, Nonce: r.Nonce, ContractHash: encoded.ContractHash, Proof: &a.Proof{Version: 2, Kind: "selected_credential_hmac_v2", ChallengeHash: encoded.ChallengeHash}}
	raw, _ := json.Marshal(result)
	got, err := a.DecodeResult(raw)
	if err != nil || !got.Confirms(r, encoded) {
		t.Fatal("exact synthetic codec response mismatch")
	}
	for _, field := range []string{"nonce", "queryId", "officialVersion", "challengeHash", "selected", "negative_with_proof"} {
		t.Run(field, func(t *testing.T) {
			var x a.Result
			_ = json.Unmarshal(raw, &x)
			switch field {
			case "nonce":
				x.Nonce = "changed"
			case "queryId":
				x.QueryID = "changed"
			case "officialVersion":
				x.OfficialVersion++
			case "challengeHash":
				x.Proof.ChallengeHash = "changed"
			case "selected":
				x.Selected.Targets[0].CredentialVersion++
			case "negative_with_proof":
				x.Associated = false
			}
			if x.Confirms(r, encoded) {
				t.Fatal("unrelated/inconsistent proof accepted")
			}
		})
	}
	if _, err := a.DecodeResult(bytes.Replace(raw, []byte(`"associated":true`), []byte(`"associated":true,"Associated":false`), 1)); err == nil {
		t.Fatal("case-shadowed response accepted")
	}
	negative := result
	negative.Associated, negative.Proof, negative.Reason = false, nil, "credential_mismatch"
	if !negative.MatchesEnvelope(r, encoded) {
		t.Fatal("exact negative envelope lost")
	}
	negative.Nonce = "another-attempt"
	if negative.MatchesEnvelope(r, encoded) {
		t.Fatal("unrelated negative attributed to current attempt")
	}
}
