// Package fullrefresh coordinates management observations, never routing policy.
package fullrefresh

import (
	"context"
	"encoding/json"
	"errors"
	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"sort"
	"sync"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/managementquery"
)

var ErrUnavailable = errors.New("refresh inventory unavailable")
var ErrInput = errors.New("invalid refresh request")

type Service struct {
	Identity         *rc.Service
	Official         *ob.Service
	Credential       *cq.Service
	Store            *store.Store
	mu               sync.Mutex
	budget           *managementquery.Budget
	ctx              context.Context
	cancel           context.CancelFunc
	done             chan struct{}
	started          bool
	closed           bool
	activeRound      string
	rounds           map[string]*Progress
	purposes         map[string]Purpose
	observers        []Observer
	clock            Clock
	recoveryNeeded   bool
	schedulerAnchor  time.Time
	schedulerRunning bool
	startedObservers []StartedObserver

	scheduleCycleObservers []func(context.Context) error
}

type Option func(*Service)

func WithBudget(b *managementquery.Budget) Option {
	return func(s *Service) {
		if b != nil {
			s.budget = b
		}
	}
}
func WithClock(c Clock) Option { return func(s *Service) { s.clock = c } }
func New(st *store.Store, identity *rc.Service, official *ob.Service, credential *cq.Service, options ...Option) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{Store: st, Identity: identity, Official: official, Credential: credential, budget: managementquery.NewBudget(st), ctx: ctx, cancel: cancel, done: make(chan struct{}), rounds: map[string]*Progress{}, purposes: map[string]Purpose{}}
	for _, option := range options {
		option(s)
	}
	official.SetCompletionObserver(s.completedOfficial)
	official.SetStartedObserver(s.startedOfficial)
	credential.SetCompletionObserver(s.completedCredential)
	credential.SetStartedObserver(s.startedCredential)
	return s
}

func (s *Service) Inventory(ctx context.Context) (Inventory, error) {
	n := s.Identity.Inventory(ctx, true)
	if n.Snapshot == nil || n.Identity.State != "available" || n.BindingCapability == nil || n.BindingCapability.State != "available" {
		return Inventory{}, ErrUnavailable
	}
	out := Inventory{Scope: n.Snapshot.Scope, ResourceCount: len(n.Snapshot.Resources), Sources: []SourceStatus{}}
	rows := map[string]rc.Row{}
	for _, r := range n.Snapshot.Resources {
		if r.ResourceID != "" {
			rows[r.ResourceID] = r
		} else {
			out.UnidentifiedCount++
		}
	}
	covered := map[string]bool{}
	// Official groups include provider members and ONLY verified projection edges.
	seen := map[string]bool{}
	for _, row := range n.Snapshot.Resources {
		if row.ResourceID == "" || seen[row.ResourceID] {
			continue
		}
		seed := row
		if row.ParentID != "" {
			seed = rows[row.ParentID]
		}
		queue := []string{seed.ResourceID}
		group := map[string]bool{}
		targets := []rc.Target{}
		disabled := false
		valid := true
		for len(queue) > 0 {
			id := queue[0]
			queue = queue[1:]
			if group[id] {
				continue
			}
			r, ok := rows[id]
			if !ok {
				valid = false
				break
			}
			group[id] = true
			seen[id] = true
			targets = append(targets, r.Target)
			disabled = disabled || r.Manual.Disabled
			if r.Identity != "valid" || r.Readiness != "ready" {
				valid = false
			}
			queue = append(queue, r.Members...)
			for _, b := range r.SourceBindings {
				queue = append(queue, b.ResourceID)
			}
		}
		if !valid || len(targets) == 0 || len(targets) > 64 {
			continue
		}
		sort.Slice(targets, func(i, j int) bool { return targets[i].ResourceID < targets[j].ResourceID })
		b := ob.Binding{Scope: out.Scope, Targets: targets}
		status, err := s.Official.InventoryStatus(ctx, b, n)
		if err != nil {
			return Inventory{}, ErrUnavailable
		}
		if !status.Configured && seed.Kind != "provider" {
			continue
		}
		state, reason := status.State, ""
		if !status.Configured {
			state = "needs_configuration"
			reason = "explicit_official_query_authorization_required"
		}
		if status.Configured {
			for id := range group {
				covered[id] = true
			}
		}
		retry, err := s.Official.NextRetry(ctx, ob.Query{Binding: b, Version: status.Version})
		if err != nil {
			return Inventory{}, err
		}
		out.Sources = append(out.Sources, SourceStatus{WalletProof: verifiedWallet(status), RetryNotBeforeMS: retry, QueryCapable: status.Configured && status.DirectQuery, ID: sourceID(Official, out.Scope, targets), Request: SourceRequest{Kind: Official, Official: &ob.Query{Binding: b, Version: status.Version}}, Targets: targets, State: state, Reason: reason, ManualDisabled: disabled, Attempt: status.Attempt, LastSuccess: status.LastSuccess, Stale: status.Stale})
	}
	// Explicit per-member monetary sources are independent authorized queries.
	// A group's query credential cannot stand in for its other runtime keys.
	for _, row := range n.Snapshot.Resources {
		if row.Kind != "credential" || row.ParentID == "" || row.Identity != "valid" || row.Readiness != "ready" || len(row.SourceBindings) != 0 {
			continue
		}
		b := ob.Binding{Scope: out.Scope, Targets: []rc.Target{row.Target}}
		status, err := s.Official.InventoryStatus(ctx, b, n)
		if err != nil {
			return Inventory{}, ErrUnavailable
		}
		if !status.Configured {
			continue
		}
		q := ob.Query{Binding: b, Version: status.Version}
		retry, err := s.Official.NextRetry(ctx, q)
		if err != nil {
			return Inventory{}, err
		}
		out.Sources = append(out.Sources, SourceStatus{WalletProof: verifiedWallet(status), RetryNotBeforeMS: retry, QueryCapable: status.DirectQuery, ID: sourceID(Official, out.Scope, b.Targets), Request: SourceRequest{Kind: Official, Official: &q}, Targets: b.Targets, State: status.State, ManualDisabled: row.Manual.Disabled, Attempt: status.Attempt, LastSuccess: status.LastSuccess, Stale: status.Stale})
		covered[row.ResourceID] = true
	}
	if n.CredentialQuery.State == "available" {
		statuses, err := s.Credential.InventoryStatuses(ctx, n)
		if err != nil {
			return Inventory{}, ErrUnavailable
		}
		for _, status := range statuses {
			source := status.Source
			allCovered := true
			for _, t := range source.Targets {
				if !covered[t.ResourceID] {
					allCovered = false
				}
			}
			if source.State != "ready" && allCovered {
				continue
			}
			q := &cq.Query{Binding: source.Binding, SourceVersion: source.SourceVersion, AuthIndex: source.AuthIndex, Version: status.Version}
			id := ""
			if len(source.Targets) > 0 && source.Targets[0].ResourceID != "" {
				id = sourceID(Credential, out.Scope, source.Targets)
			}
			retry, err := s.Credential.NextSourceRetry(ctx, source)
			if err != nil {
				return Inventory{}, err
			}
			q.SourceID = source.SourceID
			q.SubjectID = source.SubjectID
			q.Version = status.Version
			out.Sources = append(out.Sources, SourceStatus{RetryNotBeforeMS: retry, QueryCapable: source.State == "ready", ID: id, Request: SourceRequest{Kind: Credential, Credential: q}, Targets: source.Targets, AuthIndex: source.AuthIndex, SourceID: source.SourceID, SubjectID: source.SubjectID, QueryVersion: status.Version, SourceVersion: source.SourceVersion, State: source.State, Reason: source.Reason, ManualDisabled: source.ManualDisabled, Attempt: status.Attempt, LastSuccess: status.LastSuccess, Stale: status.Stale})
		}
	}
	// A source roster never silently drops catalog-only descriptors.
	accounted := map[string]bool{}
	for _, src := range out.Sources {
		for _, t := range src.Targets {
			accounted[t.ResourceID] = true
		}
	}
	for _, r := range n.Snapshot.Resources {
		if r.ResourceID != "" && accounted[r.ResourceID] {
			continue
		}
		out.Sources = append(out.Sources, SourceStatus{Request: SourceRequest{Kind: Unavailable}, Targets: []rc.Target{r.Target}, State: "unavailable", Reason: "runtime_binding_or_source_authority_unavailable", ManualDisabled: r.Manual.Disabled})
	}
	for i := range out.Sources {
		due, err := s.Store.FullRefreshDue(ctx, sourceScheduleKey(out.Sources[i]))
		if err != nil {
			return Inventory{}, err
		}
		out.Sources[i].NextDueMS = due
	}
	out.NextDueMS, _ = s.Store.FullRefreshDue(ctx, roundScheduleKey(out))
	out.SchedulerCheckedAtMS, _ = s.Store.FullRefreshDue(ctx, roundScheduleKey(out)+":checked")
	if doc, err := s.Store.LatestFullRefresh(ctx); err == nil && doc != "" {
		var latest Progress
		if json.Unmarshal([]byte(doc), &latest) == nil && latest.Scope.InstanceID == out.Scope.InstanceID && latest.Scope.InstanceIncarnation == out.Scope.InstanceIncarnation {
			out.LatestRound = &latest
			if latest.Scope == out.Scope {
				for i := range out.Sources {
					source := &out.Sources[i]
					if source.WalletProof == nil {
						continue
					}
					for _, item := range latest.Items {
						if item.SharedWallet != nil && requestSame(source.Request, item.Request) && sameMembership(*source.WalletProof, item.SharedWallet.Membership) {
							copy := *item.SharedWallet
							source.SharedWallet = &copy
						}
					}
				}
			}
		}
	}
	// The shared evidence is local to this read only. Always close with a fresh,
	// fully validated live inventory AND the same Manager connection generation;
	// never carry it into Configure/Start, workers, or inference admission.
	current := s.Identity.Inventory(ctx, true)
	if current.Snapshot == nil || current.Identity.State != "available" || current.BindingCapability == nil || current.BindingCapability.State != "available" || current.CredentialQuery != n.CredentialQuery || current.Snapshot.Scope != out.Scope || !s.Identity.SameInventoryConnection(n, current) {
		return Inventory{}, ErrUnavailable
	}
	return out, nil
}
