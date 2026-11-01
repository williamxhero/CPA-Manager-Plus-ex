package credentialpolicy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"reflect"
	"strconv"
	"time"

	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

type CompletionOutcome struct {
	Decision    EvidenceDecision `json:"decision"`
	OperationID string           `json:"opId,omitempty"`
}

// Unknown/unmanaged sources may still query quota. A known source's start fence
// must commit synchronously; any storage failure is returned to #76 before I/O.
func (s *Service) CaptureCredentialQueryStart(ctx context.Context, q cq.Query, attemptID string) error {
	id := SubjectID(cq.Source{Binding: q.Binding})
	r, err := s.Store.CredentialPolicy(ctx, id)
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
	current, err := s.source(ctx, q.AuthIndex)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(current.Binding, q.Binding) || current.SourceVersion != q.SourceVersion {
		return ErrIdentity
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
	return s.Store.CaptureCredentialPolicyQuery(ctx, store.CredentialPolicyQueryFence{AttemptID: attemptID, SubjectID: id, Generation: state.Generation, Owner: state.Owner, Source: string(body), Enabled: state.Enabled})
}

func (s *Service) CompleteCredentialQuery(ctx context.Context, q cq.Query, status cq.Status, attemptID string) (CompletionOutcome, error) {
	none := func(reason string) (CompletionOutcome, error) {
		return CompletionOutcome{Decision: noEvidence(reason)}, nil
	}
	id := SubjectID(cq.Source{Binding: q.Binding})
	r, err := s.Store.CredentialPolicy(ctx, id)
	if err != nil {
		return CompletionOutcome{}, err
	}
	if r.Document == "" {
		return none("policy_off")
	}
	state, err := s.StoredStatus(ctx, id)
	if err != nil {
		return CompletionOutcome{}, err
	}
	if !state.Enabled || s.lockedOff {
		return none("policy_off")
	}
	body, err := json.Marshal(q)
	if err != nil {
		return CompletionOutcome{}, ErrInput
	}
	fence := store.CredentialPolicyQueryFence{AttemptID: attemptID, SubjectID: id, Generation: state.Generation, Owner: state.Owner, Source: string(body), Enabled: true}
	if err = s.Store.CheckCredentialPolicyQuery(ctx, fence); err != nil {
		if errors.Is(err, store.ErrPolicyConflict) {
			return none("policy_generation_changed")
		}
		return CompletionOutcome{}, err
	}
	current, err := s.source(ctx, q.AuthIndex)
	if err != nil {
		return none("current_source_unavailable")
	}
	if !reflect.DeepEqual(current.Binding, q.Binding) || current.SourceVersion != q.SourceVersion || !sameAuthority(state.Source, current) {
		return none("source_authority_changed")
	}
	if !sameAuthority(state.Source, status.Source) || !reflect.DeepEqual(q.Binding, status.Binding) || q.SourceVersion != status.SourceVersion || q.Version != status.Version || status.Attempt == nil || status.Attempt.QueryID != attemptID {
		return none("source_authority_changed")
	}
	decision := ValidateCredentialEvidence(status, s.now())
	if decision.Action == "none" {
		return CompletionOutcome{Decision: decision}, nil
	}
	return s.enqueueDecision(ctx, state, status.Source, decision)
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
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(state.Owner+":"+state.SubjectID+":"+strconv.FormatInt(state.Generation, 10)+":"+decision.Action+":"+decision.EvidenceVersion)).String()
	action, reason := "pause", "quota_windows_exhausted"
	if decision.Action == "recover" {
		action = "clear"
	}
	op := GateOperation{Version: 1, OpID: id, Binding: source.Binding, AuthIndex: source.AuthIndex, SourceVersion: source.SourceVersion, Subject: "credential", Owner: state.Owner, Action: action, Reason: reason, EvidenceID: uuid.NewSHA1(uuid.NameSpaceOID, []byte("quota-evidence:"+decision.EvidenceVersion)).String()}
	desired := PendingIntent{Operation: op, Source: source, Evidence: decision, ExpiresAtMS: decision.ObservedAtMS + int64(5*time.Minute/time.Millisecond)}
	document, err := json.Marshal(desired)
	if err != nil {
		return out, err
	}
	existing, lookupErr := s.Store.CredentialPolicyOperation(ctx, id)
	if lookupErr == nil {
		var prior PendingIntent
		if json.Unmarshal([]byte(existing.Body), &prior) != nil {
			return out, store.ErrPolicyOperationConflict
		}
		// Planning alone changes the frozen revision envelope. A replayed
		// completion must match every original intent/evidence/source field.
		prior.Planned = false
		prior.Operation.ExpectedGateRevision = 0
		prior.OwnedAtPlan = nil
		if !reflect.DeepEqual(prior, desired) {
			return out, store.ErrPolicyOperationConflict
		}
		document = []byte(existing.Body)
	} else if !errors.Is(lookupErr, sql.ErrNoRows) {
		return out, lookupErr
	}
	_, err = s.Store.BeginCredentialPolicyOperation(ctx, store.CredentialPolicyOperation{ID: id, SubjectID: state.SubjectID, Generation: state.Generation, Owner: state.Owner, Automatic: true, Body: string(document)}, lease, s.now().UnixMilli())
	if err == nil {
		out.OperationID = id
	}
	return out, err
}
