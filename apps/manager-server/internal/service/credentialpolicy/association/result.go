package association

import (
	"reflect"

	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
)

type Proof struct {
	Version       int    `json:"version"`
	Kind          string `json:"kind"`
	ChallengeHash string `json:"challengeHash"`
}
type Result struct {
	Version         int        `json:"version"`
	Associated      bool       `json:"associated"`
	Reason          string     `json:"reason"`
	Selected        cq.Binding `json:"selected"`
	AuthIndex       string     `json:"auth_index"`
	SourceVersion   string     `json:"sourceVersion"`
	Origin          string     `json:"origin"`
	OfficialBinding ob.Binding `json:"officialBinding"`
	OfficialVersion int64      `json:"officialVersion"`
	QueryID         string     `json:"queryId"`
	Nonce           string     `json:"nonce"`
	ContractHash    string     `json:"contractHash"`
	Proof           *Proof     `json:"proof,omitempty"`
}

func DecodeResult(raw []byte) (Result, error) {
	var r Result
	if decodeStrict(raw, &r) != nil || r.Version != 1 || r.Associated != (r.Proof != nil) {
		return Result{}, ErrContract
	}
	if r.Associated {
		if r.Reason != "selected_credential_association" || r.Proof.Version != 2 || r.Proof.Kind != "selected_credential_hmac_v2" || !descriptor.MatchString(r.Proof.ChallengeHash) {
			return Result{}, ErrContract
		}
	} else {
		switch r.Reason {
		case "credential_mismatch", "origin_mismatch", "association_unsupported", "source_group_mismatch":
		default:
			return Result{}, ErrContract
		}
	}
	return r, nil
}

// Confirms matches only the original frozen envelope, never local job/config/
// secret/intent truth. Those predicates must be committed by Manager separately.
func (r Result) Confirms(q Request, e Encoded) bool {
	return r.Associated && r.Reason == "selected_credential_association" && r.Proof != nil && r.Proof.Version == 2 && r.Proof.Kind == "selected_credential_hmac_v2" && r.Proof.ChallengeHash == e.ChallengeHash && r.MatchesEnvelope(q, e)
}

// Negative results also belong to one original completed Job/nonce. They must
// not silently label an unrelated profile or selected credential as mismatched.
func (r Result) MatchesEnvelope(q Request, e Encoded) bool {
	return r.Version == 1 && r.ContractHash == e.ContractHash && reflect.DeepEqual(r.Selected, q.Selected) && reflect.DeepEqual(r.OfficialBinding, ob.Binding{Scope: q.Official.Scope, Targets: q.Official.Targets}) && r.OfficialVersion == q.Official.Version && r.AuthIndex == q.AuthIndex && r.SourceVersion == q.SourceVersion && r.Origin == q.Origin && r.QueryID == q.QueryID && r.Nonce == q.Nonce
}
