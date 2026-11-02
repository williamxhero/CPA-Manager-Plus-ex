package credentialpolicy

import (
	"encoding/json"
	"reflect"

	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
)

// Internal SQLite document only. The public Status retains its existing local
// subjectId; it cannot serialize the embedded Source's distinct host subjectId.
// Keep the full issued receipt separately; the public querySubjectId refresh
// handle does not replace this sealed persistence envelope.
type policyDocument struct {
	Status
	IssuedSource *cq.Source `json:"issuedSource,omitempty"`
}

func hasSourceAuthority(source cq.Source) bool {
	return source.SourceID != "" && source.SubjectID != "" && source.SourceVersion != "" && len(source.Targets) > 0
}

func marshalPolicyDocument(status Status) ([]byte, error) {
	document := policyDocument{Status: status}
	if hasSourceAuthority(status.Source) {
		document.IssuedSource = &status.Source
	}
	// An emergency OFF of an old incomplete record must not invent authority.
	return json.Marshal(document)
}

func unmarshalPolicyDocument(raw []byte, status *Status) error {
	var document policyDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		return err
	}
	*status = document.Status
	if document.IssuedSource == nil {
		// Legacy flattened records lost the host subject. Leave it absent: live
		// authority comparison, resume and continuity must continue to refuse it.
		return nil
	}
	source := *document.IssuedSource
	projected := status.Source
	// These three fields are shadowed by policy Status on the public projection.
	projected.SubjectID, projected.State, projected.Reason = source.SubjectID, source.State, source.Reason
	// The existing document-CAS rebind updates ONLY root scope/index. It may
	// refresh transient issuance, never persistent instance/target/source IDs or
	// any identity/credential/config/membership version of the sealed receipt.
	projected.Scope.BootEpoch, projected.Scope.Revision = source.Scope.BootEpoch, source.Scope.Revision
	projected.AuthIndex = source.AuthIndex
	if !hasSourceAuthority(source) || status.SubjectID != SubjectID(source) || !reflect.DeepEqual(projected, source) {
		return ErrIdentity
	}
	source.Scope, source.AuthIndex = status.Scope, status.AuthIndex
	status.Source = source
	return nil
}
