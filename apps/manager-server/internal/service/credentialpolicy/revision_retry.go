package credentialpolicy

import (
	"reflect"
	"sort"
	"time"

	"github.com/google/uuid"
	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
)

func ownedSourceRows(ledger GateSnapshot, op GateOperation) []GateRestriction {
	var rows []GateRestriction
	for _, row := range ledger.Gates {
		if row.Owner != op.Owner || row.Model != op.Model {
			continue
		}
		overlaps := false
		for _, a := range row.Targets {
			for _, b := range op.Targets {
				if a.ResourceID == b.ResourceID {
					overlaps = true
				}
			}
		}
		if overlaps {
			rows = append(rows, row)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].GateRevision < rows[j].GateRevision })
	return rows
}

// Pure successor planning, NOT permission to retry. The dispatcher separately
// requires a typed revision-only non-application rejection and real op404,
// current eligibility/source verification and transactional lease/generation CAS.
func PrepareGateRevisionRetry(previous PendingIntent, current cq.Source, ledger GateSnapshot, now time.Time) (PendingIntent, string) {
	if previous.Evidence.Action != "pause" && previous.Evidence.Action != "recover" {
		return PendingIntent{}, "fresh_authoritative_evidence_required"
	}
	if !previous.Planned || previous.Retry < 0 || previous.Retry >= 3 || previous.ExpiresAtMS <= now.UnixMilli() || previous.Evidence.ObservedAtMS <= 0 || previous.Evidence.ObservedAtMS > now.Add(time.Minute).UnixMilli() || now.UnixMilli()-previous.Evidence.ObservedAtMS > int64(5*time.Minute/time.Millisecond) || !validEvidenceReference(previous) {
		return PendingIntent{}, "fresh_authoritative_evidence_required"
	}
	if !reflect.DeepEqual(current, previous.Source) || !reflect.DeepEqual(current.Binding, previous.Operation.Binding) || current.SourceVersion != previous.Operation.SourceVersion || current.AuthIndex != previous.Operation.AuthIndex {
		return PendingIntent{}, "source_authority_changed"
	}
	if ledger.Version != 1 || !ledger.Ready || ledger.Gates == nil || ledger.Scope != current.Scope || ledger.GateRevision <= previous.Operation.ExpectedGateRevision || ledger.GateRevision >= 9007199254740991 {
		return PendingIntent{}, "gate_revision_replan_unverified"
	}
	if !reflect.DeepEqual(ownedSourceRows(ledger, previous.Operation), previous.OwnedAtPlan) {
		return PendingIntent{}, "owned_pause_changed"
	}
	next := previous
	next.PreviousOpID = previous.Operation.OpID
	next.Operation.OpID = uuid.NewString()
	next.Operation.ExpectedGateRevision = ledger.GateRevision
	next.Retry++
	return next, ""
}
