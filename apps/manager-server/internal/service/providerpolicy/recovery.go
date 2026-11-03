package providerpolicy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"time"

	pg "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/providergate"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

type Recovery struct {
	Binding
	ExpectedGeneration     *int64 `json:"expectedGeneration"`
	ExpectedGateGeneration *int64 `json:"expectedGateGeneration"`
	SourceVersion          string `json:"sourceVersion"`
}

// Recover is an explicit administrator override, not an automatic high-balance
// decision. It preserves OFF/manual state and can only clear this installation's
// monetary owner. Existing immutable journal/CAS prevents replay after lost ACK.
func (s *Service) Recover(ctx context.Context, q Recovery) (Status, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	version, err := hex.DecodeString(q.SourceVersion)
	if q.ExpectedGeneration == nil || q.ExpectedGateGeneration == nil || *q.ExpectedGeneration <= 0 || *q.ExpectedGeneration >= 9007199254740991 || *q.ExpectedGateGeneration <= 0 || *q.ExpectedGateGeneration >= 9007199254740991 || err != nil || len(version) != 32 || hex.EncodeToString(version) != q.SourceVersion {
		return Status{}, ErrInput
	}
	if s.Identity == nil || s.monetary == nil {
		return Status{}, ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lease, err := s.Store.AcquireCredentialPolicyLease(ctx, s.holder, time.Now().UnixMilli(), 30000)
	if err != nil {
		return Status{}, err
	}
	defer func() {
		release, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = s.Store.ReleaseCredentialPolicyLease(release, lease)
	}()
	opening := s.Identity.Inventory(ctx, true)
	b, _, err := bindingInventory(opening, q.ProviderID)
	if err != nil || !reflect.DeepEqual(canonical(q.Binding), b) {
		return Status{}, ErrIdentity
	}
	row, err := s.Store.ProviderPolicy(ctx, subject(b))
	if err != nil {
		return Status{}, err
	}
	if row.Generation != *q.ExpectedGeneration {
		return Status{}, store.ErrProviderPolicyConflict
	}
	var saved intent
	installation, err := s.Store.CredentialPolicyInstallation(ctx)
	if err != nil {
		return Status{}, err
	}
	if json.Unmarshal([]byte(row.Document), &saved) != nil || saved.Enabled || saved.Owner != OwnerNamespace+installation || !sameAuthority(saved.Binding, b) {
		return Status{}, ErrUnavailable
	}
	authorize := func(ctx context.Context) error {
		// Explicit recovery fences the exact OFF intent/lease, independently of
		// automatic evaluation's mandatory fresh source fences.
		if err := s.Store.ValidateProviderRecovery(ctx, row, lease, time.Now().UnixMilli()); err != nil {
			return err
		}
		closing := s.Identity.Inventory(ctx, true)
		current, _, err := bindingInventory(closing, b.ProviderID)
		if err != nil || !reflect.DeepEqual(b, current) || !s.Identity.SameInventoryConnection(opening, closing) {
			return ErrIdentity
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return s.Store.ValidateProviderRecovery(ctx, row, lease, time.Now().UnixMilli())
	}
	raw, _ := json.Marshal(struct {
		Request Recovery
		Intent  string
	}{q, row.Document})
	evidence := sha256.Sum256(raw)
	_, err = s.monetary.RecoverMonetary(ctx, pg.Binding{Scope: b.Scope, Targets: b.Targets, ProviderID: b.ProviderID}, *q.ExpectedGateGeneration, q.SourceVersion, hex.EncodeToString(evidence[:]), authorize)
	if err != nil {
		return Status{}, err
	}
	return s.Status(ctx, q.ProviderID)
}
