package credentialpolicy

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"reflect"
	"strconv"
	"time"

	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	fr "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fullrefresh"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

type CompletionOutcome struct {
	Decision    EvidenceDecision `json:"decision"`
	OperationID string           `json:"opId,omitempty"`
}

// Unknown/unmanaged sources may still query quota. A known source's start fence
// must commit synchronously; any storage failure is returned to #76 before I/O.
func (s *Service) CaptureCredentialQueryStart(ctx context.Context, q cq.Query, attemptID string) error {
	return s.captureCredentialQueryStart(ctx, q, attemptID, false)
}

// Called ONLY by76's trusted Started hook after its host admission/Job commit.
// A raw caller's query is never enough to activate the local scope-rebind path.
func (s *Service) CaptureAdmittedCredentialQueryStart(ctx context.Context, q cq.Query, attemptID string) error {
	body, err := json.Marshal(q)
	if err != nil {
		return ErrInput
	}
	if err := s.Store.CheckCredentialPolicyAdmittedQuery(ctx, attemptID, SubjectID(cq.Source{Binding: q.Binding}), string(body)); err != nil {
		return err
	}
	return s.captureCredentialQueryStart(ctx, q, attemptID, true)
}

func (s *Service) captureCredentialQueryStart(ctx context.Context, q cq.Query, attemptID string, admitted bool) error {
	id := SubjectID(cq.Source{Binding: q.Binding})
	r, err := s.Store.CredentialPolicy(ctx, id)
	if err != nil {
		return err
	}
	if r.Document == "" {
		// An issued-ID match selects a possible predecessor, NOT an authority
		// proof. Unmanaged/unrelated B remains queryable without RPC fanout.
		records, err := s.Store.CredentialPolicies(ctx)
		if err != nil {
			return err
		}
		candidate := false
		for _, record := range records {
			var saved Status
			if json.Unmarshal([]byte(record.Document), &saved) != nil {
				return ErrInput
			}
			if saved.AuthorityReboundTo == "" && saved.SourceVersion == q.SourceVersion && refreshCandidate(saved.Binding, q.Binding) {
				candidate = true
				break
			}
		}
		if !candidate {
			// The trusted76 hook has already committed this exact running Job.
			// Capture implicit OFF without inventory RPC or invented intent. The
			// store validates the Job and installation before accepting it.
			installation, err := s.Store.CredentialPolicyInstallation(ctx)
			if err != nil {
				return err
			}
			body, err := json.Marshal(q)
			if err != nil {
				return ErrInput
			}
			return s.Store.CaptureCredentialPolicyPrerequisite(ctx, store.CredentialPolicyQueryFence{AttemptID: attemptID, SubjectID: id, Owner: OwnerNamespace + installation, Source: string(body)})
		}
	}
	if r.Document != "" {
		stored, err := s.StoredStatus(ctx, id)
		if err != nil {
			return err
		}
		bindingMatches := reflect.DeepEqual(stored.Binding, q.Binding) && stored.AuthIndex == q.AuthIndex
		if admitted {
			// Only transient issuance scope/index can differ. Every persistent
			// identity/incarnation/credential/membership version remains exact.
			bindingMatches = stored.Scope.InstanceID == q.Scope.InstanceID && stored.Scope.InstanceIncarnation == q.Scope.InstanceIncarnation && reflect.DeepEqual(stored.Targets, q.Targets)
		}
		if bindingMatches && stored.SourceVersion == q.SourceVersion {
			//76's host has just validated and committed this full issued Query.
			// An unchanged binding needs no additional inventory round trip in
			// the acquisition deadline. Changed boot/authority stays on the
			// explicit current rebind/certification path below.
			body, err := json.Marshal(q)
			if err != nil {
				return ErrInput
			}
			fence := store.CredentialPolicyQueryFence{AttemptID: attemptID, SubjectID: id, Generation: stored.Generation, Owner: stored.Owner, Source: string(body), Enabled: stored.Enabled}
			if admitted {
				return s.Store.CaptureCredentialPolicyAdmittedQuery(ctx, fence, fr.SourceScheduleKey(fr.SourceRequest{Kind: fr.Credential, Credential: &q}))
			}
			return s.Store.CaptureCredentialPolicyQuery(ctx, fence)
		}
	}
	current, err := s.source(ctx, q.AuthIndex)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(current.Binding, q.Binding) || current.SourceVersion != q.SourceVersion {
		return ErrIdentity
	}
	r, err = s.certifiedIntent(ctx, current, r)
	if err != nil {
		return err
	}
	if r.Document == "" {
		return nil
	}
	state, err := s.StoredStatus(ctx, id)
	if err != nil {
		return err
	}
	if !sameAuthority(state.Source, current) {
		return nil
	} // changed authority is unowned, not query-blocked
	if !reflect.DeepEqual(state.Binding, current.Binding) || state.AuthIndex != current.AuthIndex {
		scope, err := json.Marshal(current.Scope)
		if err != nil {
			return ErrInput
		}
		if err := s.Store.RebindCredentialPolicy(ctx, r, string(scope), current.AuthIndex); err != nil {
			return err
		}
	}
	body, err := json.Marshal(q)
	if err != nil {
		return ErrInput
	}
	fence := store.CredentialPolicyQueryFence{AttemptID: attemptID, SubjectID: id, Generation: state.Generation, Owner: state.Owner, Source: string(body), Enabled: state.Enabled}
	if admitted {
		return s.Store.CaptureCredentialPolicyAdmittedQuery(ctx, fence, fr.SourceScheduleKey(fr.SourceRequest{Kind: fr.Credential, Credential: &q}))
	}
	return s.Store.CaptureCredentialPolicyQuery(ctx, fence)
}

func (s *Service) CompleteCredentialQuery(ctx context.Context, q cq.Query, status cq.Status, attemptID string) (CompletionOutcome, error) {
	state, decision, err := s.classifyCompletion(ctx, q, status, attemptID)
	if err != nil || decision.Action == "none" {
		return CompletionOutcome{Decision: decision}, err
	}
	current, err := s.source(ctx, q.AuthIndex)
	if err != nil {
		return CompletionOutcome{Decision: noEvidence("current_source_unavailable")}, nil
	}
	if !reflect.DeepEqual(current.Binding, q.Binding) || current.SourceVersion != q.SourceVersion || !sameAuthority(state.Source, current) {
		return CompletionOutcome{Decision: noEvidence("source_authority_changed")}, nil
	}
	return s.enqueueDecision(ctx, state, status.Source, decision)
}

// Pure evidence + local DB fences only; no Core inventory or gate I/O in the
// synchronous completion callback. The worker independently checks current Core.
func (s *Service) classifyCompletion(ctx context.Context, q cq.Query, status cq.Status, attemptID string) (Status, EvidenceDecision, error) {
	id := SubjectID(cq.Source{Binding: q.Binding})
	r, err := s.Store.CredentialPolicy(ctx, id)
	if err != nil {
		return Status{}, EvidenceDecision{}, err
	}
	if r.Document == "" {
		return Status{}, noEvidence("policy_off"), nil
	}
	state, err := s.StoredStatus(ctx, id)
	if err != nil {
		return Status{}, EvidenceDecision{}, err
	}
	if !state.Enabled {
		return state, noEvidence("policy_off"), nil
	}
	body, err := json.Marshal(q)
	if err != nil {
		return state, EvidenceDecision{}, ErrInput
	}
	fence := store.CredentialPolicyQueryFence{AttemptID: attemptID, SubjectID: id, Generation: state.Generation, Owner: state.Owner, Source: string(body), Enabled: true}
	if err = s.Store.CheckCredentialPolicyQuery(ctx, fence); err != nil {
		if errors.Is(err, store.ErrPolicyConflict) {
			return state, noEvidence("policy_generation_changed"), nil
		}
		return state, EvidenceDecision{}, err
	}
	if !sameAuthority(state.Source, status.Source) || !reflect.DeepEqual(q.Binding, status.Binding) || q.SourceVersion != status.SourceVersion || q.Version != status.Version || status.Attempt == nil || status.Attempt.QueryID != attemptID {
		return state, noEvidence("source_authority_changed"), nil
	}
	decision := ValidateCredentialEvidence(status, s.now())
	return state, decision, nil
}

func (s *Service) enqueueDecision(ctx context.Context, state Status, source cq.Source, decision EvidenceDecision) (CompletionOutcome, error) {
	out := CompletionOutcome{Decision: decision}
	s.reconcileMu.Lock()
	defer s.reconcileMu.Unlock()
	lease, err := s.Store.AcquireCredentialPolicyLease(ctx, s.holder, s.now().UnixMilli(), 30000)
	if err != nil {
		return out, err
	}
	defer func() {
		release, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = s.Store.ReleaseCredentialPolicyLease(release, lease)
	}()
	// Stable decision key is NOT the protocol operation ID. The store allocates
	// one cryptographic v4 in the same CAS transaction as its immutable body.
	key := uuid.NewSHA1(uuid.NameSpaceOID, []byte(state.Owner+":"+state.SubjectID+":"+strconv.FormatInt(state.Generation, 10)+":"+decision.Action+":"+decision.EvidenceVersion)).String()
	action, reason := "pause", "quota_windows_exhausted"
	if decision.OwnedReason != "" {
		reason = decision.OwnedReason
	}
	if decision.Action == "recover" {
		action = "clear"
	}
	op := GateOperation{Version: 1, Binding: source.Binding, AuthIndex: source.AuthIndex, SourceVersion: source.SourceVersion, Subject: "credential", Owner: state.Owner, Action: action, Reason: reason, EvidenceID: uuid.NewSHA1(uuid.NameSpaceOID, []byte("quota-evidence:"+decision.EvidenceVersion)).String()}
	desired := PendingIntent{Operation: op, Source: source, Evidence: decision, ExpiresAtMS: decision.ObservedAtMS + int64(5*time.Minute/time.Millisecond)}
	if decision.ValidUntilMS > 0 && decision.ValidUntilMS < desired.ExpiresAtMS {
		desired.ExpiresAtMS = decision.ValidUntilMS
	}
	build := func(id, existing string) (string, error) {
		desired.Operation.OpID = id
		if existing == "" {
			document, err := json.Marshal(desired)
			return string(document), err
		}
		var prior PendingIntent
		if json.Unmarshal([]byte(existing), &prior) != nil {
			return "", store.ErrPolicyOperationConflict
		}
		// Planning alone changes the frozen revision envelope. A replayed
		// completion must match every original intent/evidence/source field.
		prior.Planned = false
		prior.Operation.ExpectedGateRevision = 0
		prior.OwnedAtPlan = nil
		if !reflect.DeepEqual(prior, desired) {
			return "", store.ErrPolicyOperationConflict
		}
		return existing, nil
	}
	record, err := s.Store.BeginCredentialPolicyDecision(ctx, key, key, store.CredentialPolicyOperation{SubjectID: state.SubjectID, Generation: state.Generation, Owner: state.Owner, Automatic: true}, lease, s.now().UnixMilli(), build)
	if err == nil {
		out.OperationID = record.ID
	}
	return out, err
}
