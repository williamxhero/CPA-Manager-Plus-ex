package httpapi

import (
	"reflect"
	"testing"

	cp "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy"
)

// Actual retained native orphan A must not veto independent selected-key B's
// real ON/automatic pause/own recovery. A is an explicit management precondition,
// never invented quota evidence or a synthetic successful operation receipt.
func TestCredentialPolicyActualOrphanNativeDoesNotVetoIndependentKeyRecovery(t *testing.T) {
	f, group, endpoint := newSelectedKeyPolicyFixture(t, false, true)
	var aggregate struct {
		Ready   bool `json:"ready"`
		Applied bool `json:"applied"`
	}
	f.coreAPI("GET", "inference-gates/capabilities", nil, &aggregate)
	if aggregate.Ready || aggregate.Applied {
		t.Fatal("orphan regression requires actual negative aggregate capability diagnostics")
	}
	assertOrphan := func() {
		var ledger cp.GateSnapshot
		f.coreAPI("GET", "inference-gates", nil, &ledger)
		if !ledger.Ready || ledger.AllSourcesReady || len(ledger.Gates) != 1 {
			t.Fatal("independent recovery changed aggregate/orphan ledger integrity")
		}
		row := ledger.Gates[0]
		if row.Owner != f.orphanNative.Owner || row.EvidenceID != f.orphanNative.EvidenceID || row.Reason != f.orphanNative.Reason || row.SourceVersion != f.orphanNative.SourceVersion || row.Applicability != "reconciliation_required" || !reflect.DeepEqual(row.Targets, f.orphanNative.Targets) {
			t.Fatal("independent own clear purged or rewrote orphan evidence")
		}
	}
	assertOrphan()
	f.onEnd = assertOrphan
	runSelectedKeyPolicyFixture(t, f, group, endpoint, false, false, false)
}
