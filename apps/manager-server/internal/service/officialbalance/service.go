// Package officialbalance hosts explicit, independently authorized official
// reads. Core identity is an observation precondition, never a gate ACK.
package officialbalance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

var ErrIdentity = errors.New("balance identity unavailable or changed")
var ErrInput = errors.New("invalid balance configuration")

type Binding struct {
	Scope   rc.Scope    `json:"scope"`
	Targets []rc.Target `json:"targets"`
}
type Config struct {
	Binding
	Version        int64    `json:"version"`
	Adapter        string   `json:"adapter"`
	Endpoint       string   `json:"endpoint"`
	PrivateTargets []string `json:"privateTargets,omitempty"`
	TimeoutMS      int64    `json:"timeoutMs,omitempty"`
}
type Update struct {
	Config
	Secret      string `json:"secret"`
	ClearSecret bool   `json:"clearSecret"`
}
type Status struct {
	Config
	Configured  bool                 `json:"configured"`
	State       string               `json:"state"`
	DirectQuery bool                 `json:"managerDirectQuery"`
	CoreQuery   bool                 `json:"coreQuery"`
	CoreGate    bool                 `json:"coreGate"`
	Stale       bool                 `json:"stale"`
	Attempt     *balance.Observation `json:"attempt,omitempty"`
	LastSuccess *balance.Observation `json:"lastSuccess,omitempty"`
}
type Service struct {
	running  map[string]string
	slots    chan struct{}
	Store    *store.Store
	Identity *rc.Service
	Querier  balance.Querier
	mu       sync.Mutex
}

func New(st *store.Store, identity *rc.Service) *Service {
	return &Service{Store: st, Identity: identity, Querier: balance.New(), running: map[string]string{}, slots: make(chan struct{}, 4)}
}

func canonical(b Binding) Binding {
	b.Targets = append([]rc.Target(nil), b.Targets...)
	sort.Slice(b.Targets, func(i, j int) bool { return b.Targets[i].ResourceID < b.Targets[j].ResourceID })
	return b
}

// Persistent credential authority excludes ephemeral catalog operation scope.
// Targets retain EVERY version, and both durable instance identifiers remain.
func sameAuthority(a, b Binding) bool {
	a.Scope.BootEpoch = ""
	b.Scope.BootEpoch = ""
	a.Scope.Revision = 0
	b.Scope.Revision = 0
	return same(a, b)
}
func operationKey(b Binding) string {
	raw, _ := json.Marshal(canonical(b))
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

func same(a, b Binding) bool {
	x, _ := json.Marshal(canonical(a))
	y, _ := json.Marshal(canonical(b))
	return string(x) == string(y)
}

// Storage identity deliberately excludes versions: a changed tuple can replace
// its old configuration via CAS, but cannot read/reuse its secrets or results.
func key(b Binding) string {
	ids := []string{b.Scope.InstanceID, b.Scope.InstanceIncarnation}
	for _, t := range canonical(b).Targets {
		ids = append(ids, t.ResourceID, t.Incarnation)
	}
	raw, _ := json.Marshal(ids)
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}
func (s *Service) verify(ctx context.Context, b Binding) bool {
	n := s.Identity.Inventory(ctx)
	if n.Snapshot == nil || n.Identity.State != "available" || n.Snapshot.Scope != b.Scope || len(b.Targets) == 0 || len(b.Targets) > 64 {
		return false
	}
	seen := map[string]bool{}
	for _, t := range b.Targets {
		if seen[t.ResourceID] {
			return false
		}
		seen[t.ResourceID] = true
		found := false
		for _, r := range n.Snapshot.Resources {
			if r.Target == t && r.Identity == "valid" && r.Readiness == "ready" {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	// Bind the complete proven source group, not an arbitrary set/subset.
	rows := map[string]rc.Row{}
	for _, r := range n.Snapshot.Resources {
		rows[r.ResourceID] = r
	}
	seed := b.Targets[0].ResourceID
	providers := 0
	for _, t := range b.Targets {
		if rows[t.ResourceID].Kind == "provider" {
			seed = t.ResourceID
			providers++
		}
	}
	if providers > 1 {
		return false
	}
	required := map[string]bool{}
	queue := []string{seed}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if required[id] {
			continue
		}
		required[id] = true
		row, ok := rows[id]
		if !ok || !seen[id] {
			return false
		}
		for _, target := range row.SourceBindings {
			queue = append(queue, target.ResourceID)
		}
		if row.Kind == "provider" {
			queue = append(queue, row.Members...)
		}
	}
	return len(required) == len(seen)
}
func (s *Service) Configure(ctx context.Context, u Update) (Status, error) {
	if u.Version < 0 || u.Version >= 9007199254740991 || len(u.Secret) > 8192 || u.ClearSecret && strings.TrimSpace(u.Secret) != "" || u.TimeoutMS < 0 || u.TimeoutMS > 60000 || len(u.PrivateTargets) > 16 {
		return Status{}, ErrInput
	}
	if u.Adapter != "deepseek" && u.Adapter != "deepinfra" {
		return Status{}, ErrInput
	}
	if u.Endpoint == "" && u.Adapter == "deepseek" {
		u.Endpoint = "https://api.deepseek.com/user/balance"
	}
	if err := balance.ValidateSource(u.Endpoint, u.PrivateTargets); err != nil {
		return Status{}, ErrInput
	}
	if !s.verify(ctx, u.Binding) {
		return Status{}, ErrIdentity
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id := key(u.Binding)
	old, err := s.Store.OfficialBalance(ctx, id)
	if err != nil {
		return Status{}, err
	}
	if old.Version != u.Version {
		return Status{}, store.ErrBalanceConflict
	}
	var previous Config
	if old.Config != "" && json.Unmarshal([]byte(old.Config), &previous) != nil {
		return Status{}, ErrInput
	}
	// A changed authority never silently carries a credential to a different target.
	if old.Config != "" && (!sameAuthority(previous.Binding, u.Binding) || previous.Endpoint != u.Endpoint || previous.Adapter != u.Adapter) {
		u.ClearSecret = true
	}
	if strings.TrimSpace(u.Secret) != "" {
		u.ClearSecret = false
	}
	u.Binding = canonical(u.Binding)
	u.Version++
	data, _ := json.Marshal(u.Config)
	if err := s.Store.SaveOfficialBalance(ctx, store.OfficialBalanceRecord{ID: id, Config: string(data)}, u.Version-1, strings.TrimSpace(u.Secret), u.ClearSecret); err != nil {
		return Status{}, err
	}
	return s.read(ctx, u.Binding)
}
func (s *Service) read(ctx context.Context, b Binding) (Status, error) {
	r, err := s.Store.OfficialBalance(ctx, key(b))
	if err != nil {
		return Status{}, err
	}
	out := Status{Config: Config{Binding: b, Version: r.Version}, State: "needs_configuration"}
	if r.Config == "" {
		return out, nil
	}
	// Decode into fresh storage: json.Unmarshal reuses slice backing arrays.
	// Decoding into a Config pre-seeded with b would mutate the caller targets
	// and erase the very version mismatch we must reject.
	var stored Config
	if json.Unmarshal([]byte(r.Config), &stored) != nil {
		return Status{}, ErrInput
	}
	out.Config = stored
	if !sameAuthority(out.Binding, b) {
		return Status{Config: Config{Binding: b, Version: r.Version}, State: "needs_configuration"}, nil
	}
	out.Binding = b
	out.Configured = r.Configured
	out.DirectQuery = out.Adapter == "deepseek"
	if !out.DirectQuery {
		out.State = "unsupported"
	} else if r.Configured {
		out.State = "ready"
	}
	if r.Attempt != "" {
		json.Unmarshal([]byte(r.Attempt), &out.Attempt)
		if out.Attempt != nil {
			if out.Attempt.QueryStatus == "running" && out.Attempt.DeadlineMS > 0 && time.Now().UnixMilli() >= out.Attempt.DeadlineMS {
				out.Attempt.QueryStatus = "network_error"
				out.Attempt.ErrorCode = "MANAGEMENT_BUDGET_EXHAUSTED"
			}
			out.State = out.Attempt.QueryStatus
		}
	}
	if r.LastSuccess != "" {
		json.Unmarshal([]byte(r.LastSuccess), &out.LastSuccess)
	}
	out.Stale = out.LastSuccess != nil && (out.Attempt == nil || out.Attempt.QueryStatus != "success" || time.Now().UnixMilli()-out.LastSuccess.ObservedAtMS >= 3600000)
	if out.LastSuccess != nil {
		var observed Binding
		if json.Unmarshal(out.LastSuccess.Binding, &observed) != nil || !same(observed, b) {
			out.Stale = true
		}
	}
	return out, nil
}
func (s *Service) Status(ctx context.Context, b Binding) (Status, error) {
	if !s.verify(ctx, b) {
		return Status{}, ErrIdentity
	}
	return s.read(ctx, b)
}
