package providergate

import (
	"context"
	"encoding/json"
	"reflect"
	"time"
)

// Reconcile retries only the EXACT durable immutable command. A transport
// failure before acceptance and one after acceptance have the same safe retry:
// Core deduplicates opId+body under its ledger lock. Status remains read-only.
func (s *Service) Reconcile(ctx context.Context, b Binding) (Status, error) {
	return s.reconcile(ctx, b, nil)
}

// ReconcileEvidence cannot first-commit an old monetary command using a proof
// that became obsolete while prepare was performing authenticated Core reads.
func (s *Service) ReconcileEvidence(ctx context.Context, b Binding, current EvidenceGuard) (Status, error) {
	if !s.monetary || current == nil {
		return Status{}, ErrInput
	}
	return s.reconcile(ctx, b, current)
}

// AdoptMonetaryReceipt only saves a Core-verified receipt for an already
// journalled immutable command. It never POSTs, clears a restriction, or
// treats a missing operation as applied. Automatic replay remains separate.
func (s *Service) AdoptMonetaryReceipt(ctx context.Context, b Binding) (Status, error) {
	if !s.monetary {
		return Status{}, ErrInput
	}
	out, err := s.prepare(ctx, b)
	if err != nil {
		return Status{}, err
	}
	if out.record.Generation == 0 || validReceipt(out.doc.Operation, out.doc.Receipt) {
		return out.status, nil
	}
	if !sameInstance(out.doc.Operation.Scope, b.Scope) || !reflect.DeepEqual(out.doc.Operation.Targets, canonical(b).Targets) || out.doc.Operation.SourceVersion != out.status.SourceVersion || !validReceipt(out.doc.Operation, out.receipt) {
		return out.status, nil
	}
	out.doc.Receipt = out.receipt
	out.doc.UpdatedAtMS = time.Now().UnixMilli()
	raw, _ := json.Marshal(out.doc)
	if err := s.store.CompleteProviderGate(ctx, out.record, string(raw)); err != nil {
		return out.status, err
	}
	return s.Status(ctx, b)
}

func (s *Service) reconcile(ctx context.Context, b Binding, current EvidenceGuard) (Status, error) {
	out, err := s.prepare(ctx, b)
	if err != nil {
		return Status{}, err
	}
	if out.record.Generation == 0 || out.settled {
		return out.status, nil
	}
	if s.monetary && current == nil {
		return out.status, ErrInput
	}
	op := out.doc.Operation
	if !reflect.DeepEqual(op.groupBinding, coreGroup(out.status.Binding)) || op.SourceVersion != out.status.SourceVersion || op.ExpectedGateRevision != out.ledger.GateRevision {
		return out.status, ErrUnavailable
	}
	if current != nil {
		if err := current(ctx, out.inventory); err != nil {
			return out.status, err
		}
	}
	if err := out.core.request(ctx, "POST", "operations", op, &out.doc.Receipt); err != nil {
		return out.status, err
	}
	raw, _ := json.Marshal(out.doc)
	if err := s.store.CompleteProviderGate(ctx, out.record, string(raw)); err != nil {
		return out.status, err
	}
	return s.Status(ctx, b)
}
