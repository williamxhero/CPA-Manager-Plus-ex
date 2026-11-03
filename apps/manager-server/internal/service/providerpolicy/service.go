// Package providerpolicy owns provider monetary intent. Configuration is never
// a routing ACK, funding certificate, or permission to change manual state.
package providerpolicy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	pg "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/providergate"
	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

var ErrInput = errors.New("invalid provider policy request")
var ErrIdentity = errors.New("provider policy identity unavailable")
var ErrUnavailable = errors.New("provider policy authority unavailable")

const OwnerNamespace = "cpa-manager.provider-balance.v1/"

type Binding struct {
	Scope      rc.Scope    `json:"scope"`
	Targets    []rc.Target `json:"targets"`
	ProviderID string      `json:"providerId"`
}
type Update struct {
	Binding
	ExpectedGeneration *int64 `json:"expectedGeneration"`
	Enabled            *bool  `json:"enabled"`
}
type intent struct {
	Binding
	Enabled     bool   `json:"enabled"`
	Owner       string `json:"owner"`
	UpdatedAtMS int64  `json:"updatedAtMs"`
}
type Status struct {
	Binding
	SubjectID      string `json:"subjectId"`
	Enabled        bool   `json:"enabled"`
	Effective      bool   `json:"effective"`
	Generation     int64  `json:"generation"`
	Owner          string `json:"owner"`
	Source         string `json:"source"`
	UpdatedAtMS    int64  `json:"updatedAtMs"`
	BindingCurrent bool   `json:"bindingCurrent"`
	Locked         bool   `json:"locked"`
	ManualDisabled bool   `json:"manualDisabled"`
	OwnPaused      bool   `json:"ownPaused"`
	Applied        bool   `json:"applied"`
	State          string `json:"state"`
	Reason         string `json:"reason"`
}

type Service struct {
	Store    *store.Store
	Identity *rc.Service
	Balance  *ob.Service
	Gate     *pg.Service
	// Clock is a startup dependency. Cached reads never renew source freshness.
	Clock  func() time.Time
	mu     sync.Mutex
	holder string
}

func New(st *store.Store, identity *rc.Service, balance *ob.Service, gate *pg.Service) *Service {
	return &Service{Store: st, Identity: identity, Balance: balance, Gate: gate, Clock: time.Now, holder: uuid.NewString()}
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

func sameAuthority(a, b Binding) bool {
	a.Scope.BootEpoch = ""
	a.Scope.Revision = 0
	b.Scope.BootEpoch = ""
	b.Scope.Revision = 0
	return reflect.DeepEqual(canonical(a), canonical(b))
}

func subject(b Binding) string {
	raw, _ := json.Marshal([]string{OwnerNamespace, b.Scope.InstanceID, b.Scope.InstanceIncarnation, b.ProviderID, b.Targets[0].Incarnation})
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

func (s *Service) binding(ctx context.Context, id string) (Binding, bool, error) {
	if s.Identity == nil || uuid.Validate(id) != nil {
		return Binding{}, false, ErrIdentity
	}
	n := s.Identity.Inventory(ctx, true)
	return bindingInventory(n, id)
}

func bindingInventory(n *rc.Negotiated, id string) (Binding, bool, error) {
	if n == nil || n.Snapshot == nil || !n.Snapshot.Complete || n.Identity.State != "available" {
		return Binding{}, false, ErrIdentity
	}
	rows := map[string]rc.Row{}
	for _, row := range n.Snapshot.Resources {
		rows[row.ResourceID] = row
	}
	parent, ok := rows[id]
	if !ok || parent.Kind != "provider" || parent.Family != "openai-compatibility" || parent.Identity != "valid" || parent.Readiness != "ready" || len(parent.Members) == 0 || len(parent.Members) > 63 {
		return Binding{}, false, ErrIdentity
	}
	b := Binding{Scope: n.Snapshot.Scope, ProviderID: id, Targets: []rc.Target{parent.Target}}
	disabled := parent.Manual.Disabled
	seen := map[string]bool{id: true}
	for _, member := range parent.Members {
		r, ok := rows[member]
		if !ok || seen[member] || r.Kind != "credential" || r.ParentID != id || r.Identity != "valid" || r.Readiness != "ready" || len(r.SourceBindings) != 0 {
			return Binding{}, false, ErrIdentity
		}
		seen[member] = true
		b.Targets = append(b.Targets, r.Target)
		disabled = disabled || r.Manual.Disabled
	}
	return canonical(b), disabled, nil
}

func (s *Service) Status(ctx context.Context, id string) (Status, error) {
	b, disabled, err := s.binding(ctx, id)
	if err != nil {
		return Status{}, err
	}
	installation, err := s.Store.CredentialPolicyInstallation(ctx)
	if err != nil {
		return Status{}, err
	}
	out := Status{Binding: b, SubjectID: subject(b), Owner: OwnerNamespace + installation, Source: "default_off", BindingCurrent: true, Locked: true, ManualDisabled: disabled, State: "off", Reason: "funding_coverage_unverified"}
	r, err := s.Store.ProviderPolicy(ctx, out.SubjectID)
	if err != nil {
		return Status{}, err
	}
	out.Generation = r.Generation
	if r.Generation > 0 {
		var saved intent
		if json.Unmarshal([]byte(r.Document), &saved) != nil || saved.Owner != out.Owner {
			return Status{}, ErrUnavailable
		}
		out.Enabled = saved.Enabled
		out.UpdatedAtMS = saved.UpdatedAtMS
		out.Source = "explicit"
		out.BindingCurrent = sameAuthority(saved.Binding, b)
		if out.Enabled {
			out.State = "unavailable"
		}
		if !out.BindingCurrent {
			out.Reason = "provider_authority_changed"
		}
	}
	return out, nil
}

func (s *Service) Update(ctx context.Context, q Update) (Status, error) {
	if q.Enabled == nil || q.ExpectedGeneration == nil || *q.ExpectedGeneration < 0 || *q.ExpectedGeneration >= 9007199254740991 {
		return Status{}, ErrInput
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
	state, err := s.Status(ctx, q.ProviderID)
	if err != nil {
		return Status{}, err
	}
	if !reflect.DeepEqual(canonical(q.Binding), state.Binding) {
		return Status{}, ErrIdentity
	}
	if state.Generation != *q.ExpectedGeneration {
		return Status{}, store.ErrProviderPolicyConflict
	}
	// ON requires a current actual gate plus independently proven funding
	// coverage. Never silently turn a configured label into monetary authority.
	if *q.Enabled {
		return Status{}, ErrUnavailable
	}
	previous, err := s.Store.ProviderPolicy(ctx, state.SubjectID)
	if err != nil {
		return Status{}, err
	}
	doc, _ := json.Marshal(intent{Binding: state.Binding, Enabled: *q.Enabled, Owner: state.Owner, UpdatedAtMS: time.Now().UnixMilli()})
	next := store.ProviderPolicyRecord{ID: state.SubjectID, Generation: state.Generation + 1, Document: string(doc)}
	if err := s.Store.SaveProviderPolicy(ctx, next, previous, lease, time.Now().UnixMilli()); err != nil {
		return Status{}, err
	}
	return s.Status(ctx, q.ProviderID)
}
