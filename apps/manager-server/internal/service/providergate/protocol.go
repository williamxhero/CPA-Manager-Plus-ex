// Package providergate implements explicit administrative provider restrictions.
// It does not grant automatic monetary-policy or funding-association authority.
package providergate

import (
	"reflect"
	"sort"

	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
)

type Binding struct {
	Scope      rc.Scope    `json:"scope"`
	Targets    []rc.Target `json:"targets"`
	ProviderID string      `json:"providerId"`
}

// The Core derives provider identity from the complete issued closure. A local
// selector must never become a consumer assertion in the Core wire contract.
type groupBinding struct {
	Scope   rc.Scope    `json:"scope"`
	Targets []rc.Target `json:"targets"`
}

func coreGroup(b Binding) groupBinding { return groupBinding{b.Scope, b.Targets} }

type Update struct {
	Binding
	Version            int    `json:"version"`
	SourceVersion      string `json:"sourceVersion"`
	ExpectedGeneration *int64 `json:"expectedGeneration"`
	Action             string `json:"action"`
}

type Status struct {
	Binding
	SubjectID     string `json:"subjectId"`
	Owner         string `json:"owner"`
	Generation    int64  `json:"generation"`
	Action        string `json:"action"`
	SourceVersion string `json:"sourceVersion"`
	Ready         bool   `json:"ready"`
	OwnPaused     bool   `json:"ownPaused"`
	Applied       bool   `json:"applied"`
	State         string `json:"state"`
	Reason        string `json:"reason"`
}

type operation struct {
	groupBinding
	AuthIndex            string `json:"auth_index"`
	Version              int    `json:"version"`
	OpID                 string `json:"opId"`
	ExpectedGateRevision uint64 `json:"expectedGateRevision"`
	SourceVersion        string `json:"sourceVersion"`
	Subject              string `json:"subject"`
	Owner                string `json:"owner"`
	Action               string `json:"action"`
	Reason               string `json:"reason"`
	EvidenceID           string `json:"evidenceId"`
}

type result struct {
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

type restriction struct {
	Subject       string      `json:"subject"`
	Applicability string      `json:"applicability"`
	Targets       []rc.Target `json:"targets"`
	Owner         string      `json:"owner"`
	Model         string      `json:"model"`
	Reason        string      `json:"reason"`
	EvidenceID    string      `json:"evidenceId"`
	SourceVersion string      `json:"sourceVersion"`
	GateRevision  uint64      `json:"gateRevision"`
}

type ledger struct {
	Version      int           `json:"version"`
	Scope        rc.Scope      `json:"scope"`
	GateRevision uint64        `json:"gateRevision"`
	Ready        bool          `json:"ready"`
	Gates        []restriction `json:"gates"`
}

func canonical(b Binding) Binding {
	b.Targets = append([]rc.Target(nil), b.Targets...)
	sort.Slice(b.Targets, func(i, j int) bool {
		if b.Targets[i].ResourceID == b.ProviderID {
			return b.Targets[j].ResourceID != b.ProviderID
		}
		if b.Targets[j].ResourceID == b.ProviderID {
			return false
		}
		return b.Targets[i].ResourceID < b.Targets[j].ResourceID
	})
	return b
}

func validReceipt(op operation, r result) bool {
	return r.Version == 1 && r.OpID == op.OpID && r.Status == "applied" && r.Error == "" && r.Durable && r.Applied && op.ExpectedGateRevision < 9007199254740991 && r.GateRevision == op.ExpectedGateRevision+1 && r.DurableRevision == r.GateRevision && r.AppliedRevision == r.GateRevision
}

func sameInstance(a, b rc.Scope) bool {
	return a.InstanceID == b.InstanceID && a.InstanceIncarnation == b.InstanceIncarnation
}

func confirms(op operation, r result, l ledger) bool {
	if !validReceipt(op, r) || l.Version != 1 || !l.Ready || l.Gates == nil || !sameInstance(l.Scope, op.Scope) || l.GateRevision < r.GateRevision || l.GateRevision > 9007199254740991 {
		return false
	}
	found := false
	for _, gate := range l.Gates {
		if gate.Owner != op.Owner || len(gate.Targets) == 0 || len(op.Targets) == 0 || gate.Targets[0].ResourceID != op.Targets[0].ResourceID {
			continue
		}
		if op.Action == "clear" || gate.Subject != "provider" || gate.Model != "" || gate.Applicability != "current" || !reflect.DeepEqual(gate.Targets, op.Targets) || gate.SourceVersion != op.SourceVersion || gate.Reason != op.Reason || gate.EvidenceID != op.EvidenceID || gate.GateRevision != r.GateRevision {
			return false
		}
		found = true
	}
	return op.Action == "pause" && found || op.Action == "clear" && !found
}
