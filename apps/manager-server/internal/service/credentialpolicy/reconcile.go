package credentialpolicy

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"reflect"
	"time"

	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

// The immutable operation body and its evidence/version are durable before RPC.
// Never rewrite a previously sent opId when binding or global ledger CAS changes.
type PendingIntent struct {
	Planned      bool              `json:"planned"`
	Operation    GateOperation     `json:"operation"`
	Source       cq.Source         `json:"source"`
	Evidence     EvidenceDecision  `json:"evidence"`
	ExpiresAtMS  int64             `json:"expiresAtMs"`
	Retry        int               `json:"retry,omitempty"`
	PreviousOpID string            `json:"previousOpId,omitempty"`
	OwnedAtPlan  []GateRestriction `json:"ownedAtPlan,omitempty"`
}

func (s *Service) Reconcile(ctx context.Context) error {
	ctx, cancelBudget := context.WithTimeout(ctx, 15*time.Second)
	defer cancelBudget()
	s.reconcileMu.Lock()
	defer s.reconcileMu.Unlock()
	lease, err := s.Store.AcquireCredentialPolicyLease(ctx, s.holder, s.now().UnixMilli(), 30000)
	if err != nil {
		return err
	}
	defer func() {
		release, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = s.Store.ReleaseCredentialPolicyLease(release, lease)
	}()
	pending, err := s.Store.PendingCredentialPolicyOperations(ctx)
	if err != nil {
		return err
	}
	for _, operation := range pending {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		lease, err = s.Store.AcquireCredentialPolicyLease(ctx, s.holder, s.now().UnixMilli(), 30000)
		if err != nil {
			return err
		}
		attempt, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = s.reconcileOne(attempt, operation, lease)
		cancel()
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) reconcileOne(ctx context.Context, record store.CredentialPolicyOperation, lease store.CredentialPolicyLease) error {
	current, err := s.Store.CheckCredentialPolicyOperation(ctx, record.ID, lease, s.now().UnixMilli())
	if err != nil {
		if errors.Is(err, store.ErrPolicyConflict) {
			return nil
		}
		return err
	} // stale/OFF work stays superseded, never resent
	record = current
	save := func(reason string) error {
		// Keep failure persistence inside this attempt's original budget.
		return s.Store.CompleteCredentialPolicyOperation(ctx, record.ID, record.Revision, "pending", "", reason, lease, s.now().UnixMilli())
	}
	staleAssociation := func(err error) error {
		if !errors.Is(err, store.ErrPolicyConflict) {
			return err
		}
		state, reason := "conflict", "association_evidence_changed"
		if record.Attempted {
			state, reason = "pending", "association_evidence_changed_outcome_unverified"
		}
		if err := s.Store.CompleteCredentialPolicyOperation(ctx, record.ID, record.Revision, state, "", reason, lease, s.now().UnixMilli()); errors.Is(err, store.ErrPolicyConflict) {
			return nil
		} else {
			return err
		}
	}
	var desired PendingIntent
	if json.Unmarshal([]byte(record.Body), &desired) != nil || desired.Operation.OpID != record.ID || desired.Operation.Owner != record.Owner || SubjectID(desired.Source) != record.SubjectID || desired.Operation.Model != "" {
		return save("invalid_pending_intent")
	}
	if parsed, err := uuid.Parse(record.ID); err != nil || parsed.Version() != 4 {
		return s.Store.CompleteCredentialPolicyOperation(ctx, record.ID, record.Revision, "conflict", "", "gate_operation_id_unsupported", lease, s.now().UnixMilli())
	}
	// First resolve an existing operation. A timeout is not permission to assume
	// either application or non-application and send a replacement action.
	var result GateResult
	reason := s.core.request(ctx, "GET", "operations/"+record.ID, nil, &result)
	if reason != "" && reason != "gate_operation_not_found" {
		if reason == "gate_operation_invalid" || reason == "gate_request_invalid" {
			return s.Store.CompleteCredentialPolicyOperation(ctx, record.ID, record.Revision, "conflict", "", reason, lease, s.now().UnixMilli())
		}
		return save(reason)
	}
	// A read-only operation lookup is safe for historical journals. Missing
	// persisted authority must still block both acknowledgement and any resend.
	state, err := s.StoredStatus(ctx, record.SubjectID)
	if err != nil {
		return err
	}
	if !hasSourceAuthority(state.Source) || !sameAuthority(state.Source, desired.Source) {
		return save("source_authority_changed")
	}
	if reason == "" && (!desired.Planned || !result.Confirms(desired.Operation)) {
		return save("gate_ack_unconfirmed")
	}
	if reason == "" {
		return s.confirmOperation(ctx, record, desired, result, lease)
	}
	if record.Automatic && (desired.ExpiresAtMS <= s.now().UnixMilli() || desired.Evidence.ValidUntilMS <= s.now().UnixMilli()) {
		return save("fresh_authoritative_evidence_required")
	}
	source, err := s.source(ctx, desired.Source.AuthIndex)
	if err != nil || !sameAuthority(source, desired.Source) {
		return save("current_binding_unverified")
	}
	if !reflect.DeepEqual(source.Binding, desired.Operation.Binding) {
		return save("fresh_current_binding_evidence_required")
	}
	if _, reason = s.core.capabilities(ctx, source); reason != "" {
		return save(reason)
	}
	ledger, reason := s.core.snapshot(ctx)
	if reason != "" {
		return save(reason)
	}
	if ledger.Scope != source.Scope {
		return save("gate_ledger_scope_changed")
	}
	if desired.Operation.Action == "clear" {
		owned := false
		for _, gate := range ledger.Gates {
			if gate.Owner == record.Owner && gate.Model == desired.Operation.Model && reflect.DeepEqual(gate.Targets, source.Targets) {
				if gate.Applicability != "current" || gate.Reason != desired.Operation.Reason || record.Automatic && gate.SourceVersion != source.SourceVersion {
					return save("owned_pause_evidence_mismatch")
				}
				owned = true
			}
		}
		if !owned {
			return s.Store.CompleteCredentialPolicyOperation(ctx, record.ID, record.Revision, "no_action", "", "no_owned_pause", lease, s.now().UnixMilli())
		}
	}
	if !desired.Planned {
		desired.Operation.ExpectedGateRevision = ledger.GateRevision
		desired.OwnedAtPlan = ownedSourceRows(ledger, desired.Operation)
		desired.Planned = true
		body, _ := json.Marshal(desired)
		record, err = s.Store.PlanCredentialPolicyOperation(ctx, record.ID, record.Revision, string(body), lease, s.now().UnixMilli())
		if err != nil {
			if desired.Evidence.AssociationCaptureID != "" {
				return staleAssociation(err)
			}
			return err
		}
	}
	// Claim's commit is the local automatic-action admission point. OFF never
	// waits on this network request; subsequent claims are generation-fenced.
	record, err = s.Store.ClaimCredentialPolicyOperation(ctx, record.ID, record.Revision, lease, s.now().UnixMilli())
	if err != nil {
		if desired.Evidence.AssociationCaptureID != "" {
			return staleAssociation(err)
		}
		return err
	}
	reason = s.core.request(ctx, "POST", "operations", desired.Operation, &result)
	if reason != "" {
		if reason == "gate_operation_invalid" || reason == "gate_request_invalid" {
			return s.Store.CompleteCredentialPolicyOperation(ctx, record.ID, record.Revision, "conflict", "", reason, lease, s.now().UnixMilli())
		}
		if reason == "gate_revision_conflict" && record.Automatic {
			return s.replanRevisionConflict(ctx, record, desired, lease)
		}
		if reason == "gate_conflict" {
			return s.Store.CompleteCredentialPolicyOperation(ctx, record.ID, record.Revision, "conflict", "", "gate_conflict_requery_required", lease, s.now().UnixMilli())
		}
		return save(reason)
	}
	if !result.Confirms(desired.Operation) {
		return save("gate_ack_unconfirmed")
	}
	return s.confirmOperation(ctx, record, desired, result, lease)
}

func (s *Service) replanRevisionConflict(ctx context.Context, record store.CredentialPolicyOperation, desired PendingIntent, lease store.CredentialPolicyLease) error {
	save := func(reason string) error {
		return s.Store.CompleteCredentialPolicyOperation(ctx, record.ID, record.Revision, "pending", "", reason, lease, s.now().UnixMilli())
	}
	// Only reached after a typed non-application rejection, never a timeout.
	var receipt GateResult
	reason := s.core.request(ctx, "GET", "operations/"+record.ID, nil, &receipt)
	if reason != "gate_operation_not_found" {
		return save("gate_replan_outcome_unverified")
	}
	source, err := s.source(ctx, desired.Source.AuthIndex)
	if err != nil || !reflect.DeepEqual(source, desired.Source) {
		return save("source_authority_changed")
	}
	if _, reason = s.core.capabilities(ctx, source); reason != "" {
		return save(reason)
	}
	ledger, reason := s.core.snapshot(ctx)
	if reason != "" {
		return save(reason)
	}
	next, reason := PrepareGateRevisionRetry(desired, source, ledger, s.now())
	if reason != "" {
		if desired.Retry >= 3 {
			return s.Store.CompleteCredentialPolicyOperation(ctx, record.ID, record.Revision, "conflict", "", "gate_replan_limit", lease, s.now().UnixMilli())
		}
		return save(reason)
	}
	current, err := s.source(ctx, source.AuthIndex)
	if err != nil || !reflect.DeepEqual(current, source) {
		return save("source_authority_changed")
	}
	body, err := json.Marshal(next)
	if err != nil {
		return err
	}
	return s.Store.ReplanCredentialPolicyOperation(ctx, record.ID, record.Revision, store.CredentialPolicyOperation{ID: next.Operation.OpID, SubjectID: record.SubjectID, Generation: record.Generation, Owner: record.Owner, Automatic: record.Automatic, Body: string(body)}, lease, s.now().UnixMilli())
}

func (s *Service) confirmOperation(ctx context.Context, record store.CredentialPolicyOperation, desired PendingIntent, result GateResult, lease store.CredentialPolicyLease) error {
	fail := func(reason string) error {
		return s.Store.CompleteCredentialPolicyOperation(ctx, record.ID, record.Revision, "pending", "", reason, lease, s.now().UnixMilli())
	}
	source, err := s.source(ctx, desired.Source.AuthIndex)
	if err != nil || !sameAuthority(source, desired.Source) {
		return fail("current_binding_unverified")
	}
	if _, reason := s.core.capabilities(ctx, source); reason != "" {
		return fail(reason)
	}
	ledger, reason := s.core.snapshot(ctx)
	if reason != "" {
		return fail(reason)
	}
	currentOp := desired.Operation
	currentOp.Binding = source.Binding // proven identical full lifecycle, newly issued operation scope
	if !ledger.Confirms(currentOp, result) {
		return fail("gate_current_readback_unconfirmed")
	}
	currentSource, err := s.source(ctx, source.AuthIndex)
	if err != nil || !reflect.DeepEqual(currentSource, source) {
		return fail("source_authority_changed")
	}
	encoded, _ := json.Marshal(result)
	return s.Store.CompleteCredentialPolicyOperation(ctx, record.ID, record.Revision, "applied", string(encoded), "", lease, s.now().UnixMilli())
}
