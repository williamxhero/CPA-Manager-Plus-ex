package credentialpolicy

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	a "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy/association"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

// Evaluation is one original Job/capture only. It is NOT persisted readiness,
// permission to save ON, reusable proof, an operation plan or applied gate ACK.
type AssociationEvaluation struct {
	CaptureID  string              `json:"captureId"`
	Associated bool                `json:"associated"`
	Reason     string              `json:"reason"`
	Value      ValueClassification `json:"value"`
}

func captureFailure(err error) string {
	if errors.Is(err, store.ErrPolicyConflict) {
		return "policy_generation_changed"
	}
	return "completed_job_or_profile_changed"
}
func (s *Service) EvaluateOfficialAssociation(ctx context.Context, id string) (AssociationEvaluation, error) {
	out := AssociationEvaluation{CaptureID: id, Reason: "credential_association_unavailable"}
	capture, err := s.Store.CredentialAssociationCapture(ctx, id)
	if err != nil {
		return out, err
	}
	if err := s.Store.CheckCredentialAssociationCapture(ctx, capture, true); err != nil {
		out.Reason = captureFailure(err)
		return out, nil
	}
	var original officialCapture
	if json.Unmarshal([]byte(capture.Document), &original) != nil || a.ValidateCapture(original.Request) != nil || original.Request.QueryID != capture.JobID || original.SubjectID != capture.PolicyID || original.Generation != capture.PolicyGeneration {
		return out, ErrInput
	}
	installation, err := s.Store.CredentialPolicyInstallation(ctx)
	if err != nil {
		return out, err
	}
	if original.Owner != OwnerNamespace+installation {
		return out, ErrIdentity
	}
	if s.coordinator == nil {
		out.Reason = "official_current_authority_unavailable"
		return out, nil
	}
	current, err := s.source(ctx, original.Source.AuthIndex)
	if err != nil {
		out.Reason = "current_source_unavailable"
		return out, nil
	}
	if !reflect.DeepEqual(current, original.Source) {
		out.Reason = "source_authority_changed"
		return out, nil
	}
	official, err := s.coordinator.Official.Status(ctx, original.Request.Official.Binding)
	if err != nil || !official.Configured || !reflect.DeepEqual(official.Config, original.Request.Official) {
		out.Reason = "official_current_authority_unavailable"
		return out, nil
	}
	secret, err := s.Store.ResolveOfficialBalanceSecret(ctx, capture.SourceID, capture.ConfigVersion, capture.SecretRef)
	if err != nil || secret == "" {
		out.Reason = "official_secret_changed"
		return out, nil
	}
	request, encoded, err := a.Prepare(original.Request, []byte(secret))
	secret = "" // never persist/echo secret or candidate MAC
	if err != nil {
		out.Reason = "association_contract_invalid"
		return out, nil
	}
	result, reason := s.core.associate(ctx, request)
	if reason != "" {
		out.Reason = strings.Replace(reason, "gate_", "association_", 1)
		return out, nil
	}
	if !result.MatchesEnvelope(request, encoded) {
		out.Reason = "association_proof_mismatch"
		return out, nil
	}
	if !result.Associated {
		out.Reason = result.Reason
		return out, nil
	}
	if !result.Confirms(request, encoded) {
		out.Reason = "association_proof_mismatch"
		return out, nil
	}
	after, err := s.source(ctx, original.Source.AuthIndex)
	if err != nil || !reflect.DeepEqual(after, current) {
		out.Reason = "source_authority_changed"
		return out, nil
	}
	official, err = s.coordinator.Official.Status(ctx, original.Request.Official.Binding)
	if err != nil || !official.Configured || !reflect.DeepEqual(official.Config, original.Request.Official) {
		out.Reason = "official_current_authority_unavailable"
		return out, nil
	}
	if err := s.Store.CheckCredentialAssociationCapture(ctx, capture, true); err != nil {
		out.Reason = captureFailure(err)
		return out, nil
	}
	raw, err := s.Store.OfficialBalanceJob(ctx, capture.JobID)
	if err != nil {
		return out, err
	}
	var job ob.Job
	if json.Unmarshal([]byte(raw), &job) != nil || job.State != "complete" || job.Status == nil || job.Status.Attempt == nil || job.Status.LastSuccess == nil || job.Status.Stale || job.Status.Attempt.QueryID != capture.JobID || job.Status.LastSuccess.QueryID != capture.JobID || !reflect.DeepEqual(job.Binding, request.Official.Binding) || job.Version != request.Official.Version || !reflect.DeepEqual(job.Status.Config, request.Official) {
		out.Reason = "completed_job_or_profile_changed"
		return out, nil
	}
	obs := job.Status.Attempt
	var binding ob.Binding
	age := s.now().UnixMilli() - obs.ObservedAtMS
	if obs.QueryStatus != "success" || obs.ErrorCode != "" || obs.Semantic != "key_remaining" || obs.ObservedAtMS <= 0 || age < 0 || age >= int64(5*time.Minute/time.Millisecond) || json.Unmarshal(obs.Binding, &binding) != nil || !reflect.DeepEqual(binding, request.Official.Binding) {
		out.Reason = "fresh_authoritative_evidence_required"
		return out, nil
	}
	out.Value = ClassifyOfficialValue(balance.NewAPITokenUsageV1, *obs)
	out.Associated = true
	out.Reason = "selected_credential_association"
	decision := EvidenceDecision{Action: "none", Reason: "native_key_value_unknown", AssociationCaptureID: id, AttemptID: capture.JobID, ObservedAtMS: obs.ObservedAtMS, ValidUntilMS: obs.ObservedAtMS + int64(5*time.Minute/time.Millisecond), OwnedReason: "quota_native_key_exhausted", EvidenceVersion: "native-key:" + original.Source.SourceVersion + ":" + strconv.FormatInt(capture.ConfigVersion, 10) + ":" + capture.JobID + ":" + encoded.ChallengeHash}
	switch out.Value.State {
	case "depleted":
		decision.Action, decision.Reason = "pause", "quota_native_key_exhausted"
	case "positive", "unlimited":
		decision.Action, decision.Reason = "recover", "quota_native_key_positive"
	}
	{
		document, err := json.Marshal(acceptedAssociation{Source: original.Source, Result: result, Value: out.Value, Decision: decision})
		if err != nil {
			return out, err
		}
		if err := s.Store.AcceptCredentialAssociationEvidence(ctx, capture, string(document)); err != nil {
			out.Associated = false
			out.Reason = captureFailure(err)
			return out, nil
		}
	}
	return out, nil
}
