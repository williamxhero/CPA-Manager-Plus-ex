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
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/managementquery"
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
	Version        int64                         `json:"version"`
	Adapter        string                        `json:"adapter"`
	Endpoint       string                        `json:"endpoint"`
	PrivateTargets []string                      `json:"privateTargets,omitempty"`
	TimeoutMS      int64                         `json:"timeoutMs,omitempty"`
	NewAPI         *balance.NewAPIConfig         `json:"newAPI,omitempty"`
	BrowserSession *balance.BrowserSessionConfig `json:"browserSession,omitempty"`
	Volcengine     *balance.VolcengineConfig     `json:"volcengine,omitempty"`
	Aliyun         *balance.AliyunConfig         `json:"aliyun,omitempty"`
	Web            *balance.WebConfig            `json:"web,omitempty"`
}
type Update struct {
	Config
	Secret      string `json:"secret"`
	ClearSecret bool   `json:"clearSecret"`
}
type Status struct {
	Config
	Configured         bool                 `json:"configured"`
	State              string               `json:"state"`
	Reason             string               `json:"reason,omitempty"`
	WebSourceAuthority string               `json:"webSourceAuthority,omitempty"`
	DirectQuery        bool                 `json:"managerDirectQuery"`
	CoreQuery          bool                 `json:"coreQuery"`
	CoreGate           bool                 `json:"coreGate"`
	Stale              bool                 `json:"stale"`
	Attempt            *balance.Observation `json:"attempt,omitempty"`
	LastSuccess        *balance.Observation `json:"lastSuccess,omitempty"`
}
type Service struct {
	running   map[string]string
	budget    *managementquery.Budget
	completed func(Job, Status)
	starting  func(context.Context, Job) error
	Store     *store.Store
	Identity  *rc.Service
	Querier   balance.Querier
	WebReader balance.WebReader
	mu        sync.Mutex
}

type Option func(*Service)

func WithBudget(b *managementquery.Budget) Option {
	return func(s *Service) {
		if b != nil {
			s.budget = b
		}
	}
}

// SetCompletionObserver is startup wiring, not a second query path.
// SetStartedObserver is coordinator startup wiring. Called after durable job
// creation, outside locks, before upstream I/O; failure prevents the query.
func (s *Service) SetStartedObserver(fn func(context.Context, Job) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.starting = fn
}
func (s *Service) queryStarted(ctx context.Context, job Job) error {
	s.mu.Lock()
	observer := s.starting
	s.mu.Unlock()
	if observer != nil {
		return observer(ctx, job)
	}
	return ctx.Err()
}
func (s *Service) SetCompletionObserver(fn func(Job, Status)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.completed = fn
}
func New(st *store.Store, identity *rc.Service, options ...Option) *Service {
	s := &Service{Store: st, Identity: identity, Querier: balance.New(), WebReader: balance.NewBrowserSkill(balance.BrowserSkillOptions{
		Rules: map[string]func([]byte) (balance.WebPage, error){
			balance.LaysoDashboardModelsRuleV1: balance.ParseLaysoDashboardModelsHTML,
		},
		// Enrollment stays fail-closed until the host supplies a verified
		// Profile 1 ↔ instance proof. Rules alone never authorize collection.
	}), running: map[string]string{}, budget: managementquery.NewBudget(st)}
	for _, option := range options {
		option(s)
	}
	return s
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

// Every persisted source-contract field is authority, not only URL/adapter.
// Empty secret preserves ONLY an unchanged contract; an explicit new secret
// can establish a changed account/auth/version/unit authority via the same CAS.
func sameSourceAuthority(a, b Config) bool {
	if !sameAuthority(a.Binding, b.Binding) {
		return false
	}
	a.Binding = Binding{}
	b.Binding = Binding{}
	a.Version = 0
	b.Version = 0
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
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
	return verifyInventory(n, b)
}

func verifyInventory(n *rc.Negotiated, b Binding) bool {
	if n == nil || n.Snapshot == nil || n.Identity.State != "available" || n.Snapshot.Scope != b.Scope || len(b.Targets) == 0 || len(b.Targets) > 64 {
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
	if u.Adapter != "deepseek" && u.Adapter != "deepinfra" && u.Adapter != balance.OpenAICompatUsageV1 && u.Adapter != balance.NewAPIUserSelfV1 && u.Adapter != balance.NewAPITokenUsageV1 && u.Adapter != balance.NewAPIBrowserSessionV1 && u.Adapter != balance.VolcengineAccountBalanceV20220101 && u.Adapter != balance.AliyunPrepaidCashV20171214 && u.Adapter != balance.LaysoBillingWebV1 {
		return Status{}, ErrInput
	}
	if u.Adapter == balance.LaysoBillingWebV1 {
		if balance.ValidateWebContract(u.Web) != nil || u.Secret != "" || len(u.PrivateTargets) != 0 || u.Endpoint != "" && u.Endpoint != balance.LaysoBillingPage {
			return Status{}, ErrInput
		}
		if u.Web.ParsingRule != "unverified" && (s.WebReader == nil || !s.WebReader.SupportsRule(u.Web.ParsingRule)) {
			return Status{}, ErrInput
		}
		u.Endpoint = balance.LaysoBillingPage
		// Web sources never inherit an API credential, including API -> web.
		u.ClearSecret = true
	} else if u.Web != nil {
		return Status{}, ErrInput
	}
	if u.Adapter == balance.AliyunPrepaidCashV20171214 {
		if balance.ValidateAliyunContract(u.Aliyun) != nil || strings.TrimSpace(u.Secret) != "" && balance.ValidateAliyunSecret(u.Secret) != nil {
			return Status{}, ErrInput
		}
		if u.Endpoint == "" {
			u.Endpoint = balance.AliyunBalanceEndpoint
		}
	} else if u.Aliyun != nil {
		return Status{}, ErrInput
	}
	if u.Adapter == balance.VolcengineAccountBalanceV20220101 {
		if balance.ValidateVolcengineContract(u.Volcengine) != nil || strings.TrimSpace(u.Secret) != "" && balance.ValidateVolcengineSecret(u.Secret) != nil {
			return Status{}, ErrInput
		}
		if u.Endpoint == "" {
			u.Endpoint = balance.VolcengineBalanceEndpoint
		}
	} else if u.Volcengine != nil {
		return Status{}, ErrInput
	}
	if u.Adapter == balance.NewAPIBrowserSessionV1 {
		if balance.ValidateBrowserSessionContract(u.BrowserSession, u.NewAPI) != nil || strings.TrimSpace(u.Secret) != "" {
			return Status{}, ErrInput
		}
		u.Endpoint = u.BrowserSession.Origin + balance.NewAPIBrowserSessionPath
		u.ClearSecret = true
	} else if u.BrowserSession != nil {
		return Status{}, ErrInput
	}
	if u.Adapter == balance.NewAPIUserSelfV1 || u.Adapter == balance.NewAPITokenUsageV1 {
		if balance.ValidateNewAPIContract(u.Adapter, u.NewAPI) != nil {
			return Status{}, ErrInput
		}
	} else if u.Adapter != balance.NewAPIBrowserSessionV1 && u.NewAPI != nil {
		return Status{}, ErrInput
	}
	if u.Endpoint == "" && u.Adapter == "deepseek" {
		u.Endpoint = "https://api.deepseek.com/user/balance"
	}
	if u.Endpoint == "" && u.Adapter == "deepinfra" {
		u.Endpoint = "https://api.deepinfra.com/payment/checklist"
	}
	if u.Adapter != balance.LaysoBillingWebV1 {
		if err := balance.ValidateAdapterSource(u.Adapter, u.Endpoint, u.PrivateTargets); err != nil {
			return Status{}, ErrInput
		}
	}
	inventory := s.Identity.Inventory(ctx)
	if !verifyInventory(inventory, u.Binding) {
		return Status{}, ErrIdentity
	}
	if u.Adapter == balance.VolcengineAccountBalanceV20220101 || u.Adapter == balance.AliyunPrepaidCashV20171214 {
		// CPA entry, not hostname/provider branding, selects prepaid semantics.
		// The complete host-issued group must contain an actual AI provider.
		providerEntry := false
		for _, row := range inventory.Snapshot.Resources {
			for _, target := range u.Targets {
				if row.Kind == "provider" && row.Target == target {
					providerEntry = true
				}
			}
		}
		if !providerEntry {
			return Status{}, ErrInput
		}
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
	if old.Config != "" && !sameSourceAuthority(previous, u.Config) {
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
	return s.readRecord(ctx, b, r)
}

func (s *Service) InventoryStatusEvidence(ctx context.Context, b Binding, n *rc.Negotiated) (Status, store.OfficialBalanceFence, error) {
	if !verifyInventory(n, b) {
		return Status{}, store.OfficialBalanceFence{}, ErrIdentity
	}
	key := key(b)
	r, fence, err := s.Store.OfficialBalanceWithFence(ctx, key)
	if err != nil {
		return Status{}, store.OfficialBalanceFence{}, err
	}
	status, err := s.readRecord(ctx, b, r)
	return status, fence, err
}

func (s *Service) readRecord(ctx context.Context, b Binding, r store.OfficialBalanceRecord) (Status, error) {
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
	if out.Adapter == balance.LaysoBillingWebV1 {
		// The complete typed contract is durable configuration. A fake secret
		// must never be needed to make a web source appear configured.
		out.Configured = balance.ValidateWebContract(out.Web) == nil && out.Endpoint == balance.LaysoBillingPage && len(out.PrivateTargets) == 0 && out.NewAPI == nil && out.Volcengine == nil && out.Aliyun == nil
		request := webRequest(out.Config)
		out.WebSourceAuthority = request.Authority
		out.DirectQuery = out.Configured && s.WebReader != nil && s.WebReader.Ready(request)
		if out.Configured && !out.DirectQuery {
			out.State = "unavailable"
			out.Reason = "web_source_verification_required"
		} else if out.DirectQuery {
			out.State = "ready"
		}
	} else {
		out.DirectQuery = out.Adapter == "deepseek" || out.Adapter == "deepinfra" || out.Adapter == balance.OpenAICompatUsageV1 || out.Adapter == balance.VolcengineAccountBalanceV20220101 && balance.ValidateVolcengineContract(out.Volcengine) == nil || out.Adapter == balance.AliyunPrepaidCashV20171214 && balance.ValidateAliyunContract(out.Aliyun) == nil || ((out.Adapter == balance.NewAPIUserSelfV1 || out.Adapter == balance.NewAPITokenUsageV1) && balance.ValidateNewAPIContract(out.Adapter, out.NewAPI) == nil) || (out.Adapter == balance.NewAPIBrowserSessionV1 && balance.ValidateBrowserSessionContract(out.BrowserSession, out.NewAPI) == nil)
		if !out.DirectQuery {
			out.State = "unsupported"
		} else if r.Configured {
			out.State = "ready"
		}
	}
	if r.Attempt != "" {
		json.Unmarshal([]byte(r.Attempt), &out.Attempt)
		if out.Attempt != nil {
			if out.Attempt.QueryStatus == "running" && out.Attempt.DeadlineMS > 0 && time.Now().UnixMilli() >= out.Attempt.DeadlineMS {
				out.Attempt.QueryStatus = "network_error"
				out.Attempt.ErrorCode = "MANAGEMENT_BUDGET_EXHAUSTED"
			}
			if out.Adapter != balance.LaysoBillingWebV1 || out.DirectQuery {
				out.State = out.Attempt.QueryStatus
			}
		}
	}
	if r.LastSuccess != "" {
		json.Unmarshal([]byte(r.LastSuccess), &out.LastSuccess)
	}
	if out.Adapter == balance.LaysoBillingWebV1 && out.LastSuccess != nil && (out.LastSuccess.WebEvidence == nil || out.LastSuccess.WebEvidence.Authority != out.WebSourceAuthority) {
		out.LastSuccess = nil
	}
	out.Stale = out.LastSuccess != nil && (!out.DirectQuery || out.Attempt == nil || out.Attempt.QueryStatus != "success" || time.Now().UnixMilli()-out.LastSuccess.ObservedAtMS >= 3600000)
	if out.LastSuccess != nil {
		var observed Binding
		if json.Unmarshal(out.LastSuccess.Binding, &observed) != nil || !same(observed, b) {
			out.Stale = true
		}
	}
	return out, nil
}

func webRequest(c Config) balance.WebRequest {
	// The complete group is authority; the caller's ordering is not.
	c.Binding = canonical(c.Binding)
	raw, _ := json.Marshal(c)
	h := sha256.Sum256(raw)
	q := balance.WebRequest{Authority: hex.EncodeToString(h[:])}
	if c.Web != nil {
		q.Source = *c.Web
	}
	return q
}
func (s *Service) Status(ctx context.Context, b Binding) (Status, error) {
	if !s.verify(ctx, b) {
		return Status{}, ErrIdentity
	}
	return s.read(ctx, b)
}

// InventoryStatus reads stored observations against the enclosing read-only
// inventory's authenticated evidence. The caller must close that inventory with
// a live identity/connection check. Configure, Start and workers always verify
// live; this evidence is never installed in a context or retained by the service.
func (s *Service) InventoryStatus(ctx context.Context, b Binding, n *rc.Negotiated) (Status, error) {
	if !verifyInventory(n, b) {
		return Status{}, ErrIdentity
	}
	return s.read(ctx, b)
}
