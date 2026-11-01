package credentialpolicy

import (
	"context"
	"encoding/json"
	"reflect"

	a "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy/association"
	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

type acceptedAssociation struct {
	Source   cq.Source           `json:"source"`
	Result   a.Result            `json:"result"`
	Value    ValueClassification `json:"value"`
	Decision EvidenceDecision    `json:"decision"`
}

func (s *Service) officialPrerequisite(ctx context.Context, state Status) (store.CredentialAssociationEvidence, error) {
	rows, err := s.Store.CredentialAssociationEvidenceForPolicy(ctx, state.SubjectID)
	if err != nil {
		return store.CredentialAssociationEvidence{}, err
	}
	for _, row := range rows {
		var evidence acceptedAssociation
		if json.Unmarshal([]byte(row.Document), &evidence) != nil {
			return store.CredentialAssociationEvidence{}, ErrInput
		}
		if !reflect.DeepEqual(evidence.Source, state.Source) || evidence.Decision.AssociationCaptureID != row.CaptureID || evidence.Decision.ValidUntilMS <= s.now().UnixMilli() {
			continue
		}
		capture, err := s.Store.CredentialAssociationCapture(ctx, row.CaptureID)
		if err != nil {
			return store.CredentialAssociationEvidence{}, err
		}
		if capture.PolicyID != state.SubjectID || capture.PolicyGeneration != state.Generation {
			continue
		}
		var original officialCapture
		if json.Unmarshal([]byte(capture.Document), &original) != nil {
			return store.CredentialAssociationEvidence{}, ErrInput
		}
		if original.Owner != state.Owner || original.Generation != state.Generation || !reflect.DeepEqual(original.Source, state.Source) {
			continue
		}
		// Literal profile/secret CAS, exact original job and policy document remain
		// current. Final ON acceptance repeats this guard in its write transaction.
		if s.Store.CheckCredentialAssociationCapture(ctx, capture, true) != nil {
			continue
		}
		if evidence.Decision.Action != "pause" && evidence.Decision.Action != "recover" {
			return store.CredentialAssociationEvidence{}, &UnavailableError{Reason: "native_key_value_unknown"}
		}
		return row, nil
	}
	return store.CredentialAssociationEvidence{}, &UnavailableError{Reason: "verified_selected_key_evidence_required"}
}
