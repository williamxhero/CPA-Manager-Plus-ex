package resourcecapability

import (
	"context"
	"time"
)

// QuotaRecheck is a scheduling hint for an exact current Core query source.
// It does not certify recovery and cannot be used to publish a gate operation.
type QuotaRecheck struct {
	Scope         Scope    `json:"scope"`
	Targets       []Target `json:"targets"`
	AuthIndex     string   `json:"auth_index"`
	SourceID      string   `json:"sourceId"`
	SubjectID     string   `json:"subjectId"`
	SourceVersion string   `json:"sourceVersion"`
	ObservedAtMS  int64    `json:"observedAtMs"`
	PublishedAtMS int64    `json:"publishedAtMs"`
	RecheckAtMS   int64    `json:"recheckAtMs"`
	ResetID       string   `json:"resetId"`
}

// QuotaRechecks reads the additive protected projection against the same
// Manager connection generation as the full inventory. A legacy or unavailable
// Core simply cannot provide an extra reset-time query; hourly reads continue.
func (s *Service) QuotaRechecks(ctx context.Context, check InventoryConnection, scope Scope) []QuotaRecheck {
	conn := check.connection
	if conn == nil || conn.owner != s {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	var view struct {
		Version       int            `json:"version"`
		Scope         Scope          `json:"scope"`
		Ready         bool           `json:"ready"`
		QuotaRechecks []QuotaRecheck `json:"quotaRechecks"`
	}
	if readCore(ctx, conn.config.CPABaseURL, conn.config.ManagementKey, "inference-gates", &view) != "" ||
		view.Version != 1 || !view.Ready || view.Scope != scope {
		return nil
	}
	current, _, _, err := s.connection.ResolveManagerConfigWithSource(ctx)
	if err != nil || current.UpdatedAtMS != conn.updatedMS || current.CPAConnection != conn.config {
		return nil
	}
	return view.QuotaRechecks
}

// InventoryConnection is an opaque request-local connection generation. A
// caller needs a fresh, checked Negotiated inventory to obtain one.
type InventoryConnection struct{ connection *inventoryConnection }

func (n *Negotiated) ConnectionGeneration() InventoryConnection {
	if n == nil {
		return InventoryConnection{}
	}
	return InventoryConnection{n.connection}
}
