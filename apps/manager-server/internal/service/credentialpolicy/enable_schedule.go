package credentialpolicy

import (
	"context"
	"encoding/json"

	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	fr "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fullrefresh"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

// The original qualifying job identifies a scheduling key ONLY. Its OFF fence
// remains immutable and cannot authorize actions in the new enabled generation.
// Both hosts use76's exact key normalization; no provider/transport inference.
func (s *Service) enableScheduleKey(ctx context.Context, native store.CredentialPolicyQueryFence, official store.CredentialAssociationEvidence) (string, error) {
	if native.AttemptID != "" {
		var q cq.Query
		if json.Unmarshal([]byte(native.Source), &q) != nil {
			return "", ErrIdentity
		}
		return fr.SourceScheduleKey(fr.SourceRequest{Kind: fr.Credential, Credential: &q}), nil
	}
	capture, err := s.Store.CredentialAssociationCapture(ctx, official.CaptureID)
	if err != nil {
		return "", err
	}
	var job ob.Job
	if json.Unmarshal([]byte(capture.Job), &job) != nil || job.ID != capture.JobID || job.Version != capture.ConfigVersion {
		return "", ErrIdentity
	}
	q := ob.Query{Binding: job.Binding, Version: job.Version}
	return fr.SourceScheduleKey(fr.SourceRequest{Kind: fr.Official, Official: &q}), nil
}
