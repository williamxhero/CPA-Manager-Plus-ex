package credentialpolicy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/google/uuid"
	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

type ResumeRequest struct {
	cq.Binding
	AuthIndex          string `json:"auth_index"`
	SourceVersion      string `json:"sourceVersion"`
	ExpectedGeneration *int64 `json:"expectedGeneration"`
	Owner              string `json:"owner"`
	OpID               string `json:"opId"`
}

// Resume is a distinct authenticated management intent, never a policy-OFF
// side effect. The controller authenticates before this method is called.
func (s *Service) Resume(ctx context.Context, q ResumeRequest) (Status, error) {
	id, err := uuid.Parse(q.OpID)
	if err != nil || id.String() != q.OpID || q.ExpectedGeneration == nil || *q.ExpectedGeneration <= 0 {
		return Status{}, ErrInput
	}
	source, err := s.source(ctx, q.AuthIndex)
	if err != nil || !reflect.DeepEqual(source.Binding, q.Binding) || source.SourceVersion != q.SourceVersion {
		return Status{}, ErrIdentity
	}
	state, err := s.StoredStatus(ctx, SubjectID(source))
	if err != nil {
		return Status{}, err
	}
	if state.Generation != *q.ExpectedGeneration {
		return Status{}, store.ErrPolicyConflict
	}
	if q.Owner != state.Owner || !sameAuthority(state.Source, source) {
		return Status{}, ErrIdentity
	}
	if _, reason := s.core.capabilities(ctx, source); reason != "" {
		return Status{}, &UnavailableError{Reason: reason}
	}
	ledger, reason := s.core.snapshot(ctx)
	if reason != "" {
		return Status{}, &UnavailableError{Reason: reason}
	}
	if ledger.Scope != source.Scope {
		return Status{}, ErrIdentity
	}
	s.reconcileMu.Lock()
	defer s.reconcileMu.Unlock()
	lease, err := s.Store.AcquireCredentialPolicyLease(ctx, s.holder, s.now().UnixMilli(), 30000)
	if err != nil {
		return Status{}, err
	}
	defer func() {
		release, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = s.Store.ReleaseCredentialPolicyLease(release, lease)
	}()
	record, err := s.Store.CredentialPolicyOperation(ctx, q.OpID)
	if err == nil {
		var prior PendingIntent
		if record.Automatic || record.SubjectID != state.SubjectID || record.Generation != state.Generation || record.Owner != state.Owner || json.Unmarshal([]byte(record.Body), &prior) != nil || prior.Operation.Action != "clear" || prior.Evidence.Action != "manual_resume" || !sameAuthority(prior.Source, source) {
			return Status{}, store.ErrPolicyOperationConflict
		}
	} else if errors.Is(err, sql.ErrNoRows) {
		owned := false
		pauseReason := "authenticated_management_resume"
		for _, row := range ledger.Gates {
			if row.Owner == state.Owner && row.Model == "" && reflect.DeepEqual(row.Targets, source.Targets) {
				if row.Applicability != "current" {
					return Status{}, ErrIdentity
				}
				owned = true
				pauseReason = row.Reason
			}
		}
		op := GateOperation{Version: 1, OpID: q.OpID, ExpectedGateRevision: ledger.GateRevision, Binding: source.Binding, AuthIndex: source.AuthIndex, SourceVersion: source.SourceVersion, Subject: "credential", Owner: state.Owner, Action: "clear", Reason: pauseReason, EvidenceID: uuid.NewSHA1(uuid.NameSpaceOID, []byte("authenticated-resume:"+q.OpID)).String()}
		intent := PendingIntent{Planned: true, Operation: op, Source: source, Evidence: EvidenceDecision{Action: "manual_resume", Reason: "authenticated_management_resume", EvidenceVersion: "management:" + q.OpID, ObservedAtMS: s.now().UnixMilli()}, OwnedAtPlan: ownedSourceRows(ledger, op)}
		body, _ := json.Marshal(intent)
		record, err = s.Store.BeginCredentialPolicyOperation(ctx, store.CredentialPolicyOperation{ID: q.OpID, SubjectID: state.SubjectID, Generation: state.Generation, Owner: state.Owner, Automatic: false, Body: string(body)}, lease, s.now().UnixMilli())
		if err != nil {
			return Status{}, err
		}
		if !owned {
			err = s.Store.CompleteCredentialPolicyOperation(ctx, record.ID, record.Revision, "no_action", "", "no_owned_pause", lease, s.now().UnixMilli())
			if err != nil {
				return Status{}, err
			}
		}
	} else {
		return Status{}, err
	}
	if err := s.reconcileOne(ctx, record, lease); err != nil {
		return Status{}, err
	}
	// Query support is not an authorization requirement for explicit own resume.
	// Applied still requires actual receipt + current ledger under current source.
	state.Source = source
	state.BindingCurrent = true
	state.Availability = rc.Capability{State: "available", Reason: "current_source_gate_ready"}
	state.QueryAvailability = rc.Capability{State: source.State, Reason: source.Reason}
	if err := s.attachOperation(ctx, &state, true); err != nil {
		return Status{}, err
	}
	current, err := s.source(ctx, source.AuthIndex)
	if err != nil || !reflect.DeepEqual(current, source) {
		return Status{}, ErrIdentity
	}
	return state, nil
}
