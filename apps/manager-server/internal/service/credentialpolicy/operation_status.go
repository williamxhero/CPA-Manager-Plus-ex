package credentialpolicy

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// Receipt revisions are historical durable facts; CurrentApplied requires fresh
// receipt + exact current ledger/source readback, never a saved DB boolean.
type OperationStatus struct {
	OpID                  string `json:"opId"`
	PolicyGeneration      int64  `json:"policyGeneration"`
	State                 string `json:"state"`
	Action                string `json:"action"`
	Revision              int64  `json:"revision"`
	Attempted             bool   `json:"attempted"`
	Planned               bool   `json:"planned"`
	ExpectedGateRevision  uint64 `json:"expectedGateRevision"`
	EvidenceVersion       string `json:"evidenceVersion"`
	GateRevision          uint64 `json:"gateRevision"`
	DurableRevision       uint64 `json:"durableRevision"`
	AppliedRevision       uint64 `json:"appliedRevision"`
	ReceiptConfirmed      bool   `json:"receiptConfirmed"`
	CurrentApplied        bool   `json:"currentApplied"`
	Reason                string `json:"reason"`
	ModelAutomationReason string `json:"modelAutomationReason,omitempty"`
	PreviousOpID          string `json:"previousOpId,omitempty"`
	Retry                 int    `json:"retry"`
}

func validEvidenceReference(intent PendingIntent) bool {
	if intent.Evidence.Action == "manual_resume" {
		parsed, err := uuid.Parse(intent.Operation.OpID)
		return err == nil && parsed.String() == intent.Operation.OpID && intent.Evidence.EvidenceVersion == "management:"+intent.Operation.OpID
	}
	parts := strings.Split(intent.Evidence.EvidenceVersion, ":")
	if len(parts) != 3 || parts[0] != intent.Source.SourceVersion || len(parts[0]) != 64 {
		return false
	}
	if _, err := hex.DecodeString(parts[0]); err != nil {
		return false
	}
	v, err := strconv.ParseInt(parts[1], 10, 64)
	_, idErr := uuid.Parse(parts[2])
	return err == nil && v > 0 && v <= 9007199254740991 && idErr == nil && parts[2] == intent.Evidence.AttemptID
}

func (s *Service) attachOperation(ctx context.Context, status *Status, current bool) error {
	status.Operation, status.Applied = nil, false
	record, err := s.Store.LatestCredentialPolicyOperation(ctx, status.SubjectID)
	if err != nil {
		return err
	}
	if record.ID == "" {
		return nil
	}
	var intent PendingIntent
	if json.Unmarshal([]byte(record.Body), &intent) != nil || intent.Operation.OpID != record.ID || intent.Operation.Owner != status.Owner || record.Owner != status.Owner || record.SubjectID != SubjectID(intent.Source) || !validEvidenceReference(intent) || intent.Operation.Action != "pause" && intent.Operation.Action != "clear" {
		status.Operation = &OperationStatus{State: "invalid", Reason: "invalid_pending_intent"}
		return nil
	}
	op := &OperationStatus{OpID: record.ID, PolicyGeneration: record.Generation, State: record.State, Action: intent.Operation.Action, Revision: record.Revision, Attempted: record.Attempted, Planned: intent.Planned, ExpectedGateRevision: intent.Operation.ExpectedGateRevision, EvidenceVersion: intent.Evidence.EvidenceVersion, Reason: "current_gate_unverified"}
	status.Operation = op
	op.Retry, op.PreviousOpID = intent.Retry, intent.PreviousOpID
	if intent.Evidence.ModelAutomationReason == "model_scope_unsupported" {
		op.ModelAutomationReason = intent.Evidence.ModelAutomationReason
	}
	switch record.LastError {
	case "outcome_unverified", "policy_generation_changed", "gate_cas_replanned", "gate_replan_limit", "gate_conflict_requery_required", "fresh_authoritative_evidence_required", "current_binding_unverified", "source_authority_changed", "gate_core_unavailable", "gate_auth_error", "gate_current_readback_unconfirmed", "gate_ack_unconfirmed", "gate_replan_outcome_unverified", "no_owned_pause", "owned_pause_evidence_mismatch", "gate_connection_unavailable", "gate_contract_unavailable":
		op.Reason = record.LastError
	}
	var receipt GateResult
	if intent.Planned && record.State == "applied" && json.Unmarshal([]byte(record.Result), &receipt) == nil && receipt.Confirms(intent.Operation) {
		op.ReceiptConfirmed = true
		op.GateRevision, op.DurableRevision, op.AppliedRevision = receipt.GateRevision, receipt.DurableRevision, receipt.AppliedRevision
	}
	if record.State == "applied" && !op.ReceiptConfirmed {
		op.State, op.Reason = "error", "gate_ack_unconfirmed"
	}
	if !current || !op.ReceiptConfirmed || status.Availability.State != "available" || !sameAuthority(status.Source, intent.Source) {
		return nil
	}
	var live GateResult
	if reason := s.core.request(ctx, "GET", "operations/"+record.ID, nil, &live); reason != "" {
		op.Reason = reason
		return nil
	}
	if !reflect.DeepEqual(live, receipt) {
		op.Reason = "gate_receipt_changed"
		return nil
	}
	ledger, reason := s.core.snapshot(ctx)
	if reason != "" {
		op.Reason = reason
		return nil
	}
	currentOp := intent.Operation
	currentOp.Binding = status.Binding
	if !ledger.Confirms(currentOp, live) {
		op.Reason = "gate_current_readback_unconfirmed"
		return nil
	}
	op.CurrentApplied, status.Applied, op.Reason = true, true, "current_gate_confirmed"
	return nil
}
