package officialbalance

import (
	"context"
	"errors"
	"slices"

	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
)

var (
	ErrMatchAccess = errors.New("bound access unavailable")
	ErrMatchManual = errors.New("manual source has precedence")
)

// MatchAccess is request-local. Only the binding can authorize using the
// resolved key; Configure re-verifies that binding and connection before CAS.
type MatchAccess struct {
	BaseURL    string `json:"-"`
	Credential string `json:"-"`
	binding    Binding
	inventory  *rc.Negotiated
	version    int64
}

func (s *Service) ResolveMatchAccess(ctx context.Context, b Binding) (MatchAccess, error) {
	before := s.Identity.Inventory(ctx)
	if !verifyInventory(before, b) {
		return MatchAccess{}, ErrIdentity
	}
	var rows []rc.Row
	for _, row := range before.Snapshot.Resources {
		if row.Kind == "credential" && slices.Contains(b.Targets, row.Target) {
			rows = append(rows, row)
		}
	}
	base, secret, err := s.Identity.ReadBoundAPIKeyConfig(ctx, before, rows)
	if err != nil {
		return MatchAccess{}, ErrMatchAccess
	}
	after := s.Identity.Inventory(ctx)
	if !verifyInventory(after, b) || !s.Identity.SameInventoryConnection(before, after) {
		return MatchAccess{}, ErrIdentity
	}
	record, err := s.Store.OfficialBalance(ctx, key(b))
	if err != nil {
		return MatchAccess{}, err
	}
	return MatchAccess{BaseURL: base, Credential: secret, binding: canonical(b), inventory: after, version: record.Version}, nil
}

func (s *Service) ConfigureMatch(ctx context.Context, a MatchAccess, adapter, endpoint string) error {
	if a.inventory == nil || a.Credential == "" {
		return ErrInput
	}
	_, err := s.Configure(ctx, Update{
		Config:         Config{Binding: a.binding, Version: a.version, Adapter: adapter, Endpoint: endpoint},
		Secret:         a.Credential,
		matchInventory: a.inventory,
	})
	return err
}

// Absence of provenance is manual, including every pre-existing registration.
// Keep this out of Config so the public PUT and GET shapes stay unchanged.
type storedSource struct {
	Config
	AutomaticMatch bool `json:"automaticMatch,omitempty"`
}
