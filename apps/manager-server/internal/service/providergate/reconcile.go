package providergate

import (
	"context"
	"encoding/json"
	"reflect"
)

// Reconcile retries only the EXACT durable immutable command. A transport
// failure before acceptance and one after acceptance have the same safe retry:
// Core deduplicates opId+body under its ledger lock. Status remains read-only.
func (s *Service) Reconcile(ctx context.Context, b Binding) (Status, error) {
	out, err := s.prepare(ctx, b)
	if err != nil {
		return Status{}, err
	}
	if out.record.Generation == 0 || out.settled {
		return out.status, nil
	}
	op := out.doc.Operation
	if !reflect.DeepEqual(op.groupBinding, coreGroup(out.status.Binding)) || op.SourceVersion != out.status.SourceVersion || op.ExpectedGateRevision != out.ledger.GateRevision {
		return out.status, ErrUnavailable
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
