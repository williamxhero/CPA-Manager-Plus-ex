package credentialpolicy

import (
	"reflect"

	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
)

// Exact producer v1 wire. EvidenceVersion/policyGeneration belong to the local
// journal, not speculative additive fields sent to the Core operation decoder.
type GateOperation struct {
	Version              int    `json:"version"`
	OpID                 string `json:"opId"`
	ExpectedGateRevision uint64 `json:"expectedGateRevision"`
	cq.Binding
	AuthIndex     string `json:"auth_index"`
	SourceVersion string `json:"sourceVersion"`
	Subject       string `json:"subject"`
	Model         string `json:"model"`
	Owner         string `json:"owner"`
	Action        string `json:"action"`
	Reason        string `json:"reason"`
	EvidenceID    string `json:"evidenceId"`
}
type GateResult struct {
	Version         int    `json:"version"`
	OpID            string `json:"opId"`
	Status          string `json:"status"`
	GateRevision    uint64 `json:"gateRevision"`
	DurableRevision uint64 `json:"durableRevision"`
	AppliedRevision uint64 `json:"appliedRevision"`
	Durable         bool   `json:"durable"`
	Applied         bool   `json:"applied"`
	Error           string `json:"error"`
}

func (r GateResult) Confirms(op GateOperation) bool {
	return op.Version == 1 && r.Version == 1 && r.OpID == op.OpID && r.Status == "applied" && r.Error == "" && r.Durable && r.Applied && op.ExpectedGateRevision < 9007199254740991 && r.GateRevision == op.ExpectedGateRevision+1 && r.DurableRevision == r.GateRevision && r.AppliedRevision == r.GateRevision
}

type GateRestriction struct {
	Applicability string      `json:"applicability"`
	Targets       []rc.Target `json:"targets"`
	Owner         string      `json:"owner"`
	Model         string      `json:"model"`
	Reason        string      `json:"reason"`
	EvidenceID    string      `json:"evidenceId"`
	SourceVersion string      `json:"sourceVersion"`
	GateRevision  uint64      `json:"gateRevision"`
}
type GateSnapshot struct {
	AllSourcesReady bool              `json:"allSourcesReady"`
	Version         int               `json:"version"`
	Scope           rc.Scope          `json:"scope"`
	GateRevision    uint64            `json:"gateRevision"`
	Ready           bool              `json:"ready"`
	Gates           []GateRestriction `json:"gates"`
}

// A historical operation ACK is not current protection. Match the present full
// lifecycle/owner/reason/evidence under a current ledger scope after every write.
func (g GateSnapshot) Confirms(op GateOperation, result GateResult) bool {
	if !result.Confirms(op) || g.Version != 1 || !g.Ready || g.Gates == nil || g.Scope != op.Scope || g.GateRevision < result.GateRevision || g.GateRevision > 9007199254740991 {
		return false
	}
	found := false
	for _, gate := range g.Gates {
		if gate.Owner != op.Owner || gate.Model != op.Model {
			continue
		}
		if !reflect.DeepEqual(gate.Targets, op.Targets) {
			for _, a := range gate.Targets {
				for _, b := range op.Targets {
					if a.ResourceID == b.ResourceID {
						return false
					}
				}
			}
			continue
		}
		if op.Action == "clear" {
			return false
		}
		if gate.Applicability != "current" || gate.SourceVersion != op.SourceVersion || gate.Reason != op.Reason || gate.EvidenceID != op.EvidenceID || gate.GateRevision != result.GateRevision {
			return false
		}
		found = true
	}
	return op.Action == "pause" && found || op.Action == "clear" && !found
}
