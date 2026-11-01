package credentialpolicy_test

import (
	"testing"

	policy "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy"
)

// Wire validation only. None of these synthetic responses is a gate ACK proof.
func TestGateResultRejectsTransportSuccessAndUnconfirmedRevisions(t *testing.T) {
	op := policy.GateOperation{Version: 1, OpID: "00000000-0000-4000-8000-000000000001", ExpectedGateRevision: 3}
	for _, result := range []policy.GateResult{
		{},
		{Version: 1, OpID: op.OpID, Status: "applied"},
		{Version: 1, OpID: op.OpID, Status: "pending", GateRevision: 4, DurableRevision: 4, AppliedRevision: 4, Durable: true, Applied: true},
		{Version: 1, OpID: "00000000-0000-4000-8000-000000000099", Status: "applied", GateRevision: 4, DurableRevision: 4, AppliedRevision: 4, Durable: true, Applied: true},
		{Version: 1, OpID: op.OpID, Status: "applied", GateRevision: 4, DurableRevision: 3, AppliedRevision: 4, Durable: true, Applied: true},
		{Version: 1, OpID: op.OpID, Status: "applied", GateRevision: 4, DurableRevision: 4, AppliedRevision: 3, Durable: true, Applied: true},
		{Version: 1, OpID: op.OpID, Status: "applied", GateRevision: 4, DurableRevision: 4, AppliedRevision: 4, Durable: true, Applied: false},
	} {
		if result.Confirms(op) {
			t.Fatalf("unconfirmed response treated as ACK: %+v", result)
		}
	}
}

// Scoped wire/readback model only, not a fake HTTP gate or final route proof.
func TestGateSnapshotSeparatesOrphanDiagnosticsFromLedgerIntegrity(t *testing.T) {
	source, _ := windowEvidence(t)
	op := policy.GateOperation{Version: 1, OpID: "00000000-0000-4000-8000-000000000009", ExpectedGateRevision: 3, Binding: source.Binding, Owner: "manager-b", Action: "clear"}
	result := policy.GateResult{Version: 1, OpID: op.OpID, Status: "applied", GateRevision: 4, DurableRevision: 4, AppliedRevision: 4, Durable: true, Applied: true}
	orphan := append(source.Targets[:0:0], source.Targets...)
	orphan[0].ResourceID = "00000000-0000-4000-8000-000000000099"
	ledger := policy.GateSnapshot{Version: 1, Scope: source.Scope, GateRevision: 4, Ready: true, AllSourcesReady: false, Gates: []policy.GateRestriction{{Owner: op.Owner, Targets: orphan, Applicability: "reconciliation_required"}}}
	if !ledger.Confirms(op, result) {
		t.Fatal("orphan A incorrectly vetoed distinct B own clear readback")
	}
	ledger.Ready = false
	if ledger.Confirms(op, result) {
		t.Fatal("corrupt/unpublished ledger ignored")
	}
	ledger.Ready = true
	ledger.Gates[0].Targets = append(source.Targets[:0:0], source.Targets...)
	ledger.Gates[0].Targets[0].CredentialVersion++
	if ledger.Confirms(op, result) {
		t.Fatal("same slot replacement treated as cleared/current lifecycle")
	}
}
