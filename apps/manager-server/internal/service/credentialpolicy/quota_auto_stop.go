package credentialpolicy

import (
	"context"
	"reflect"
	"strings"

	"github.com/google/uuid"
	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
)

type quotaAutoStop struct {
	Version int `json:"version"`
	cq.Binding
	AuthIndex     string `json:"auth_index"`
	SourceVersion string `json:"sourceVersion"`
	Subject       string `json:"subject"`
	Owner         string `json:"owner"`
	Generation    int64  `json:"generation"`
	Enabled       bool   `json:"enabled"`
}

// Sync intent ahead of traffic. The actual 429 stop is synchronous inside Core;
// this worker repairs lost acknowledgements and boot-scope changes only.
func (s *Service) syncQuotaAutoStop(ctx context.Context, state Status) string {
	p := quotaAutoStop{Version: 1, Binding: state.Binding, AuthIndex: state.AuthIndex, SourceVersion: state.SourceVersion, Subject: "credential", Owner: state.Owner, Generation: state.Generation, Enabled: state.Enabled}
	var ack quotaAutoStop
	if reason := s.core.request(ctx, "POST", "quota-auto-stop", p, &ack); reason != "" {
		return reason
	}
	if !reflect.DeepEqual(p, ack) {
		return "gate_contract_invalid"
	}
	return ""
}

func (s *Service) quotaAutoStopReady(ctx context.Context, state Status) bool {
	p := quotaAutoStop{Version: 1, Binding: state.Binding, AuthIndex: state.AuthIndex, SourceVersion: state.SourceVersion, Subject: "credential", Owner: state.Owner, Generation: state.Generation, Enabled: state.Enabled}
	var ack struct {
		Version int  `json:"version"`
		Synced  bool `json:"synced"`
	}
	return s.core.request(ctx, "POST", "quota-auto-stop/status", p, &ack) == "" && ack.Version == 1 && ack.Synced
}

// SyncQuotaAutoStops restores already-saved intent without acquiring quota.
// Certified native rotation uses the same ancestry proof as normal policy work.
func (s *Service) SyncQuotaAutoStops(ctx context.Context) error {
	records, err := s.Store.CredentialPolicies(ctx)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		return nil
	}
	if s.Quota == nil {
		return ErrIdentity
	}
	sources, err := s.Quota.Sources(ctx)
	if err != nil {
		return err
	}
	for _, record := range records {
		var saved Status
		if unmarshalPolicyDocument([]byte(record.Document), &saved) != nil || saved.AuthorityReboundTo != "" {
			continue
		}
		if !saved.Enabled {
			_ = s.syncQuotaAutoStop(ctx, saved)
			continue
		}
		for _, src := range sources {
			if src.AuthIndex != saved.AuthIndex {
				continue
			}
			src, err = s.source(ctx, src.AuthIndex)
			if err != nil {
				return err
			}
			currentRecord, err := s.Store.CredentialPolicy(ctx, SubjectID(src))
			if err != nil {
				return err
			}
			currentRecord, err = s.certifiedIntent(ctx, src, currentRecord)
			if err != nil {
				return err
			}
			var state Status
			if unmarshalPolicyDocument([]byte(currentRecord.Document), &state) != nil || !sameAuthority(state.Source, src) {
				break
			}
			state.Source = src
			_ = s.syncQuotaAutoStop(ctx, state)
			break
		}
	}
	return nil
}

// A Core-observed exhaustion has no Manager query-operation journal. Read its
// real receipt and current owner gate so it remains visible and resumable.
func (s *Service) attachUpstreamQuotaPause(ctx context.Context, status *Status) {
	ledger, reason := s.core.snapshot(ctx)
	if reason != "" || ledger.Scope != status.Scope {
		return
	}
	for _, gate := range ledger.Gates {
		if gate.Owner != status.Owner || gate.Model != "" || gate.Reason != "quota_windows_exhausted" || gate.Applicability != "current" || !reflect.DeepEqual(gate.Targets, status.Targets) || !strings.HasPrefix(gate.EvidenceID, "upstream-quota:") {
			continue
		}
		id := strings.TrimPrefix(gate.EvidenceID, "upstream-quota:")
		if uuid.Validate(id) != nil {
			continue
		}
		var receipt GateResult
		if s.core.request(ctx, "GET", "operations/"+id, nil, &receipt) != "" || receipt.Version != 1 || receipt.OpID != id || receipt.Status != "applied" || receipt.Error != "" || !receipt.Durable || !receipt.Applied || receipt.GateRevision == 0 || receipt.GateRevision > gate.GateRevision || gate.GateRevision > ledger.GateRevision || receipt.DurableRevision != receipt.GateRevision || receipt.AppliedRevision != receipt.GateRevision {
			continue
		}
		status.Applied = true
		status.Operation = &OperationStatus{OpID: id, PolicyGeneration: status.Generation, State: "applied", Action: "pause", Attempted: true, Planned: true, EvidenceVersion: gate.EvidenceID, GateRevision: receipt.GateRevision, DurableRevision: receipt.DurableRevision, AppliedRevision: receipt.AppliedRevision, ReceiptConfirmed: true, CurrentApplied: true, Reason: "current_gate_confirmed"}
		return
	}
}
