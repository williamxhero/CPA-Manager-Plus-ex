package credentialpolicy

import (
	"context"
	"encoding/json"
	"reflect"

	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

// No acquisition here: only a completed original Job from the same76 Started
// hook can qualify ON. Failure never saves ON or invokes a private quota loop.
func (s *Service) enablePrerequisite(ctx context.Context, state Status) (store.CredentialPolicyQueryFence, string, error) {
	unavailable := func() (store.CredentialPolicyQueryFence, string, error) {
		return store.CredentialPolicyQueryFence{}, "", &UnavailableError{Reason: "fresh_authoritative_evidence_required"}
	}
	r, err := s.Store.CredentialQuota(ctx, state.SubjectID)
	if err != nil {
		return store.CredentialPolicyQueryFence{}, "", err
	}
	var status cq.Status
	status.Source = state.Source
	status.Version = r.Version
	if r.Attempt == "" || json.Unmarshal([]byte(r.Attempt), &status.Attempt) != nil || status.Attempt == nil {
		return unavailable()
	}
	raw, err := s.Store.CredentialQuotaJob(ctx, status.Attempt.QueryID)
	if err != nil {
		return unavailable()
	}
	var job cq.Job
	if json.Unmarshal([]byte(raw), &job) != nil || job.State != "complete" || job.ID != status.Attempt.QueryID || job.Query.Version != r.Version || !reflect.DeepEqual(job.Query.Binding, state.Binding) || job.Query.AuthIndex != state.AuthIndex || job.Query.SourceVersion != state.SourceVersion {
		return unavailable()
	}
	// CQ stores the original Job and observations separately; Status is a live
	// read projection, not part of the immutable stored completed Job document.
	var issued cq.Source
	if json.Unmarshal([]byte(r.Source), &issued) != nil || !reflect.DeepEqual(issued.Binding, state.Binding) || !sameAuthority(issued, state.Source) || json.Unmarshal([]byte(r.LastSuccess), &status.LastSuccess) != nil || status.Attempt.QueryID != job.ID {
		return unavailable()
	}
	status.CredentialQuery.State = "available"
	// Latest source-row attempt/success must still be the exact Job, not an old
	// successful observation retained behind a newer failed/running acquisition.
	attempt, _ := json.Marshal(status.Attempt)
	success, _ := json.Marshal(status.LastSuccess)
	if string(attempt) != r.Attempt || string(success) != r.LastSuccess {
		return unavailable()
	}
	decision := ValidateCredentialEvidence(status, s.now())
	if decision.Action != "pause" && decision.Action != "recover" {
		return unavailable()
	}
	fence, err := s.Store.CredentialPolicyQuery(ctx, job.ID)
	if err != nil {
		return unavailable()
	}
	query, _ := json.Marshal(job.Query)
	if fence.SubjectID != state.SubjectID || fence.Owner != state.Owner || fence.Generation != state.Generation || fence.Enabled != state.Enabled || fence.Source != string(query) {
		return unavailable()
	}
	return fence, raw, nil
}
