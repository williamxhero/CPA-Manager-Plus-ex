package credentialpolicy

import (
	"testing"

	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
)

func TestSameGateResourceIDsMatchesPrimaryCredentialSlot(t *testing.T) {
	parent := rc.Target{ResourceID: "2077df6c-dbcf-476b-930e-9d6fd99d7bfa"}
	child := rc.Target{ResourceID: "d518f5b7-0000-4000-8000-000000000001"}
	if !sameGateResourceIDs([]rc.Target{parent, child}, []rc.Target{parent}) {
		t.Fatal("projection membership change must still own the parent slot")
	}
	if sameGateResourceIDs([]rc.Target{parent}, []rc.Target{child}) {
		t.Fatal("distinct credential slots must not match")
	}
}

func TestGateSnapshotConfirmsClearWhenDriftedRowRemoved(t *testing.T) {
	parent := rc.Target{ResourceID: "2077df6c-dbcf-476b-930e-9d6fd99d7bfa"}
	op := GateOperation{
		Version:              1,
		OpID:                 "00000000-0000-4000-8000-0000000000aa",
		ExpectedGateRevision: 84,
		Binding:              cq.Binding{Targets: []rc.Target{parent}},
		Owner:                "owner-a",
		Action:               "clear",
	}
	result := GateResult{
		Version: 1, OpID: op.OpID, Status: "applied",
		GateRevision: 85, DurableRevision: 85, AppliedRevision: 85,
		Durable: true, Applied: true,
	}
	snap := GateSnapshot{Version: 1, Ready: true, GateRevision: 85, Gates: []GateRestriction{}}
	if !snap.Confirms(op, result) {
		t.Fatal("clear must confirm when drifted gate is gone")
	}
	snap.Gates = []GateRestriction{{
		Owner: "owner-a", Applicability: "reconciliation_required",
		Targets: []rc.Target{parent, {ResourceID: "dead"}},
	}}
	if snap.Confirms(op, result) {
		t.Fatal("clear must not confirm while same-slot drifted gate remains")
	}
}
