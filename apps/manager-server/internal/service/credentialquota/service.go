// Package credentialquota hosts selected credential window observations. It does
// not own inference admission, independent policy, wallet dedupe or a scheduler.
package credentialquota

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/managementquery"
	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

var ErrIdentity = errors.New("credential quota identity changed")
var ErrInput = errors.New("invalid credential quota request")
var ErrBusy = errors.New("credential quota busy")

type Binding struct {
	Scope   rc.Scope    `json:"scope"`
	Targets []rc.Target `json:"targets"`
}
type Source struct {
	Binding
	SourceVersion     string `json:"sourceVersion"`
	SourceID          string `json:"sourceId"`
	SubjectID         string `json:"subjectId"`
	QueryVersion      int64  `json:"queryVersion,omitempty"`
	AuthIndex         string `json:"auth_index,omitempty"`
	Protocol          string `json:"protocol"`
	State             string `json:"state"`
	Reason            string `json:"reason"`
	AccountProvenance string `json:"accountProvenance"`
	ManualDisabled    bool   `json:"manualDisabled"`
}
type Query struct {
	Binding
	SourceVersion string `json:"sourceVersion"`
	SourceID      string `json:"sourceId"`
	SubjectID     string `json:"subjectId"`
	Version       int64  `json:"version"`
	AuthIndex     string `json:"auth_index"`
	TimeoutMS     int64  `json:"timeoutMs,omitempty"`
}
type Window struct {
	Name              string  `json:"name"`
	Model             string  `json:"model,omitempty"`
	UsedPercent       float64 `json:"usedPercent"`
	RemainingFraction float64 `json:"remainingFraction"`
	ResetAt           string  `json:"resetAt,omitempty"`
	SourceStatus      string  `json:"sourceStatus,omitempty"`
}
type CoreObservation struct {
	Binding
	SourceVersion       string   `json:"sourceVersion"`
	ProtocolVersion     int      `json:"protocolVersion"`
	TimestampProvenance string   `json:"timestampProvenance"`
	ReceivedAtMS        int64    `json:"receivedAtMs"`
	Protocol            string   `json:"protocol"`
	QueryStatus         string   `json:"queryStatus"`
	ErrorCode           string   `json:"errorCode,omitempty"`
	ObservedAtMS        int64    `json:"observedAtMs"`
	Windows             []Window `json:"windows,omitempty"`
	Complete            bool     `json:"complete"`
	AccountProvenance   string   `json:"accountProvenance"`
	RecoveryEligible    bool     `json:"recoveryEligible"`
}
type Status struct {
	Source
	CredentialQuery rc.Capability        `json:"credentialQuery"`
	Version         int64                `json:"version"`
	Stale           bool                 `json:"stale"`
	CoreGate        bool                 `json:"coreGate"`
	Attempt         *balance.Observation `json:"attempt,omitempty"`
	LastSuccess     *balance.Observation `json:"lastSuccess,omitempty"`
}
type Job struct {
	Joined           bool    `json:"joined"`
	Status           *Status `json:"status,omitempty"`
	RequestedVersion int64   `json:"requestedVersion"`
	ID               string  `json:"id"`
	State            string  `json:"state"`
	DeadlineMS       int64   `json:"deadlineMs"`
	Query            Query   `json:"query"`
}
type Service struct {
	Store     *store.Store
	Identity  *rc.Service
	mu        sync.Mutex
	running   map[string]string
	budget    *managementquery.Budget
	completed func(Job, Status)
	starting  func(context.Context, Job) error
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
	s := &Service{Store: st, Identity: identity, running: map[string]string{}, budget: managementquery.NewBudget(st)}
	for _, option := range options {
		option(s)
	}
	return s
}

func same(a, b Binding) bool { return reflect.DeepEqual(a, b) }
func authority(s Source) string {
	s.Scope.BootEpoch = ""
	s.Scope.Revision = 0
	// QueryVersion is the durable observation CAS, not source authority.
	s.QueryVersion = 0
	s.AuthIndex = ""
	s.State = ""
	s.Reason = ""
	s.ManualDisabled = false
	s.Targets = append([]rc.Target(nil), s.Targets...)
	sort.Slice(s.Targets, func(i, j int) bool { return s.Targets[i].ResourceID < s.Targets[j].ResourceID })
	b, _ := json.Marshal(s)
	return string(b)
}
func key(b Binding) string {
	ids := []string{b.Scope.InstanceID, b.Scope.InstanceIncarnation}
	ts := append([]rc.Target(nil), b.Targets...)
	sort.Slice(ts, func(i, j int) bool { return ts[i].ResourceID < ts[j].ResourceID })
	for _, t := range ts {
		ids = append(ids, t.ResourceID, t.Incarnation)
	}
	bts, _ := json.Marshal(ids)
	h := sha256.Sum256(bts)
	return hex.EncodeToString(h[:])
}
func operation(s Source) string { b, _ := json.Marshal(s); return string(b) }

// Sources validates all producer targets against the independently negotiated
// runtime binding. Catalog-only/unbound descriptors are retained, never guessed.
func (s *Service) Sources(ctx context.Context) ([]Source, error) {
	n := s.Identity.Inventory(ctx, true)
	return sourcesFromInventory(n)
}

func sourcesFromInventory(n *rc.Negotiated) ([]Source, error) {
	if n == nil || n.Snapshot == nil || n.Identity.State != "available" || n.BindingCapability == nil || n.BindingCapability.State != "available" || n.CredentialQuery.State != "available" {
		return nil, ErrIdentity
	}
	var response struct {
		Version int      `json:"version"`
		Sources []Source `json:"sources"`
	}
	if json.Unmarshal(n.CredentialQuotaSourceProjection(), &response) != nil || response.Version != 1 || response.Sources == nil || len(response.Sources) > 10000 {
		return nil, ErrIdentity
	}
	seen := map[string]bool{}
	for _, source := range response.Sources {
		if source.Scope != n.Snapshot.Scope || len(source.Targets) == 0 || len(source.Targets) > 64 {
			return nil, ErrIdentity
		}
		if source.AuthIndex != "" && (source.SourceID == "" || source.SubjectID == "") {
			return nil, ErrIdentity
		}
		if source.AuthIndex == "" {
			if source.State != "unsupported" || source.Reason != "runtime_binding_unavailable" {
				return nil, ErrIdentity
			}
			continue
		}
		if seen[source.AuthIndex] {
			return nil, ErrIdentity
		}
		seen[source.AuthIndex] = true
		found := false
		for _, b := range n.Bindings {
			if b.AuthIndex == source.AuthIndex && b.Scope == source.Scope && reflect.DeepEqual(b.Targets, source.Targets) {
				found = true
			}
		}
		if !found {
			return nil, ErrIdentity
		}
		if source.State == "ready" && source.Protocol != "codex-oauth-wham" && source.Protocol != "opencode-go-usage" {
			return nil, ErrIdentity
		}
		for _, row := range n.Snapshot.Resources {
			if row.ResourceID == source.Targets[0].ResourceID && source.State == "ready" {
				if source.Protocol == "codex-oauth-wham" && row.Family != "codex" || source.Protocol == "opencode-go-usage" && row.Family != "opencode-go" {
					return nil, ErrIdentity
				}
			}
		}
	}
	if len(seen) != len(n.Bindings) {
		return nil, ErrIdentity
	}
	return response.Sources, nil
}
func (s *Service) source(ctx context.Context, index string) (Source, error) {
	sources, err := s.Sources(ctx)
	if err != nil {
		return Source{}, err
	}
	for _, source := range sources {
		if source.AuthIndex == index && index != "" {
			return source, nil
		}
	}
	return Source{}, ErrIdentity
}

func sameSourceIdentity(source Source, query Query) bool {
	return same(source.Binding, query.Binding) &&
		query.AuthIndex != "" && source.AuthIndex == query.AuthIndex &&
		query.SourceID != "" && source.SourceID == query.SourceID &&
		query.SubjectID != "" && source.SubjectID == query.SubjectID &&
		query.SourceVersion != "" && source.SourceVersion == query.SourceVersion
}
func (s *Service) read(ctx context.Context, source Source) (Status, error) {
	r, err := s.Store.CredentialQuota(ctx, key(source.Binding))
	if err != nil {
		return Status{}, err
	}
	return statusFromRecord(source, r), nil
}

// Decode the transaction's committed snapshot without another database access.
// Notification must not disappear if the acquisition expires after Commit.
func statusFromRecord(source Source, r store.CredentialQuotaRecord) Status {
	source.QueryVersion = r.Version
	out := Status{Source: source, Version: r.Version, CredentialQuery: rc.Capability{State: "available", Reason: "scoped_credential_query_v1"}}
	var old Source
	if r.Source == "" || json.Unmarshal([]byte(r.Source), &old) != nil || authority(old) != authority(source) {
		return out
	}
	if r.Attempt != "" {
		_ = json.Unmarshal([]byte(r.Attempt), &out.Attempt)
	}
	if r.LastSuccess != "" {
		_ = json.Unmarshal([]byte(r.LastSuccess), &out.LastSuccess)
	}
	if out.Attempt != nil && out.Attempt.QueryStatus == "running" && time.Now().UnixMilli() >= out.Attempt.DeadlineMS {
		out.Attempt.QueryStatus = "network_error"
		out.Attempt.ErrorCode = "MANAGEMENT_BUDGET_EXHAUSTED"
	}
	out.Stale = out.LastSuccess != nil && (out.Attempt == nil || out.Attempt.QueryStatus != "success" || time.Now().UnixMilli()-out.LastSuccess.ObservedAtMS >= 3600000 || !same(old.Binding, source.Binding))
	return out
}
func (s *Service) Status(ctx context.Context, index string) (Status, error) {
	source, err := s.source(ctx, index)
	if err != nil {
		return Status{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.read(ctx, source)
}

// InventoryStatuses validates the complete consumer contract once, then reads
// only stored observations for this one read-only inventory. No supplied source
// or status can authorize Start/worker I/O; those paths still negotiate live.
// The caller must retain the closing live identity/connection fence.
func (s *Service) InventoryStatuses(ctx context.Context, n *rc.Negotiated) ([]Status, error) {
	sources, err := sourcesFromInventory(n)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Status, 0, len(sources))
	for _, source := range sources {
		status := Status{Source: source}
		if source.AuthIndex != "" {
			status, err = s.read(ctx, source)
			if err != nil {
				return nil, err
			}
		}
		out = append(out, status)
	}
	return out, nil
}
func (s *Service) Start(ctx context.Context, q Query) (Job, error) {
	started := time.Now()
	if q.Version < 0 || q.Version >= 9007199254740991 || q.TimeoutMS < 0 || q.TimeoutMS > 60000 {
		return Job{}, ErrInput
	}
	budget := time.Duration(q.TimeoutMS) * time.Millisecond
	if budget == 0 {
		budget = 15 * time.Second
	}
	deadline := started.Add(budget)
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	source, err := s.source(ctx, q.AuthIndex)
	if err != nil || !sameSourceIdentity(source, q) {
		return Job{}, ErrIdentity
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id := key(q.Binding)
	flight := id + ":" + operation(source)
	if existing := s.running[flight]; existing != "" {
		doc, e := s.Store.CredentialQuotaJob(ctx, existing)
		var job Job
		if e == nil {
			e = json.Unmarshal([]byte(doc), &job)
		}
		if e != nil {
			return Job{}, e
		}
		if job.State == "running" {
			if q.Version != job.RequestedVersion && q.Version != job.Query.Version {
				return Job{}, store.ErrQuotaConflict
			}
			job.Joined = true
			return job, nil
		}
		// Terminal publication ends host singleflight, even while its trusted
		// completion consumer still owns the old worker's budget lease.
		delete(s.running, flight)
	}
	if retry, err := s.budget.RetryAt(ctx, "credential:"+flight); err != nil {
		return Job{}, err
	} else if s.budget.Now().UnixMilli() < retry {
		return Job{}, ErrBusy
	}
	old, err := s.Store.CredentialQuota(ctx, id)
	if err != nil {
		return Job{}, err
	}
	if old.Version != q.Version {
		return Job{}, store.ErrQuotaConflict
	}
	var previous Source
	_ = json.Unmarshal([]byte(old.Source), &previous)
	keep := old.Source != "" && authority(previous) == authority(source)
	version := old.Version
	if old.Source == "" || operation(previous) != operation(source) {
		version++
	}
	jobID := uuid.NewString()
	if _, ok := s.budget.TryAcquire(jobID); !ok {
		return Job{}, ErrBusy
	}
	requestedVersion := q.Version
	q.Version = version
	job := Job{ID: jobID, State: "running", DeadlineMS: deadline.UnixMilli(), Query: q}
	job.RequestedVersion = requestedVersion
	binding, _ := json.Marshal(q.Binding)
	initial := balance.Observation{QueryID: job.ID, QueryStatus: "running", ObservedAtMS: started.UnixMilli(), DeadlineMS: job.DeadlineMS, Binding: binding, Semantic: "quota_windows"}
	a, _ := json.Marshal(initial)
	doc, _ := json.Marshal(job)
	src, _ := json.Marshal(source)
	if err := s.Store.BeginCredentialQuota(ctx, store.CredentialQuotaRecord{ID: id, Version: version, Source: string(src), Attempt: string(a)}, old.Version, job.ID, string(doc), keep); err != nil {
		s.budget.Release(jobID)
		return Job{}, err
	}
	s.running[flight] = job.ID
	go s.run(job, source, id, flight)
	return job, nil
}
func (s *Service) run(job Job, source Source, id, flight string) {
	defer func() {
		s.mu.Lock()
		if s.running[flight] == job.ID {
			delete(s.running, flight)
		}
		s.mu.Unlock()
		s.budget.Release(job.ID)
	}()
	ctx, cancel := context.WithDeadline(s.budget.Context(job.ID), time.UnixMilli(job.DeadlineMS))
	defer cancel()
	budget := time.Until(time.UnixMilli(job.DeadlineMS))
	reserve := budget / 5
	if reserve > 2*time.Second {
		reserve = 2 * time.Second
	}
	network, ncancel := context.WithTimeout(ctx, budget-reserve)
	query := struct {
		Binding
		SourceVersion string `json:"sourceVersion"`
		AuthIndex     string `json:"auth_index"`
		TimeoutMS     int64  `json:"timeoutMs"`
	}{job.Query.Binding, job.Query.SourceVersion, job.Query.AuthIndex, (budget - reserve).Milliseconds()}
	var result CoreObservation
	startErr := s.queryStarted(network, job)
	err := startErr
	if err == nil {
		err = s.Identity.CredentialQuotaRequest(network, query, &result)
	}
	ncancel()
	current, verifyErr := s.source(ctx, job.Query.AuthIndex)
	obs := balance.Observation{QueryID: job.ID, QueryStatus: "network_error", ErrorCode: "QUOTA_QUERY_FAILED", ObservedAtMS: time.Now().UnixMilli(), DeadlineMS: job.DeadlineMS, Semantic: "quota_windows"}
	if startErr != nil {
		obs.ErrorCode = "START_FENCE_UNAVAILABLE"
	}
	obs.Binding, _ = json.Marshal(job.Query.Binding)
	job.State = "complete"
	if verifyErr != nil || operation(current) != operation(source) {
		obs.QueryStatus = "identity_changed"
		obs.ErrorCode = "IDENTITY_CHANGED"
		job.State = "discarded"
	} else if err == nil && valid(result, source) {
		obs.QueryStatus = result.QueryStatus
		obs.ErrorCode = "QUOTA_" + strings.ToUpper(result.QueryStatus)
		obs.ObservedAtMS = result.ObservedAtMS
		windows, _ := json.Marshal(result)
		obs.WindowsJSON = string(windows)
		obs.CapabilityStatus = source.State
		if result.QueryStatus == "success" {
			obs.ErrorCode = ""
		}
	} else if err == nil {
		obs.QueryStatus = "parse_error"
		obs.ErrorCode = "INVALID_CORE_QUOTA_RESPONSE"
	}
	if obs.QueryStatus == "rate_limited" {
		s.budget.RecordRetry(ctx, "credential:"+flight, time.Now().Add(time.Hour))
	}
	a, _ := json.Marshal(obs)
	doc, _ := json.Marshal(job)
	s.mu.Lock()
	committed, saved := s.Store.CompleteCredentialQuotaSnapshot(ctx, id, job.Query.Version, job.ID, string(a), string(doc), obs.QueryStatus == "success")
	observer := s.completed
	s.mu.Unlock()
	if saved == nil && job.State == "complete" && observer != nil {
		observer(job, statusFromRecord(source, committed))
	}
}
func valid(r CoreObservation, s Source) bool {
	if r.ProtocolVersion != 1 {
		return false
	}
	if !same(r.Binding, s.Binding) || r.SourceVersion != s.SourceVersion || r.Protocol != s.Protocol || r.AccountProvenance != s.AccountProvenance || len(r.Windows) > 128 {
		return false
	}
	switch r.QueryStatus {
	case "success", "auth_error", "rate_limited", "network_error", "parse_error", "needs_configuration", "unsupported":
	default:
		return false
	}
	if r.QueryStatus != "success" {
		return !r.RecoveryEligible
	}
	if r.TimestampProvenance != "upstream_http_date" || r.ReceivedAtMS < r.ObservedAtMS-60000 || r.ReceivedAtMS > time.Now().Add(time.Minute).UnixMilli() {
		return false
	}
	if r.ObservedAtMS <= 0 || time.Since(time.UnixMilli(r.ObservedAtMS)) > 5*time.Minute || time.Until(time.UnixMilli(r.ObservedAtMS)) > time.Minute || len(r.Windows) == 0 {
		return false
	}
	eligible := r.Complete && r.AccountProvenance != "unverified"
	groups := map[string]map[string]bool{}
	for _, w := range r.Windows {
		if w.Name == "" || len(w.Name) > 128 || len(w.Model) > 128 || math.IsNaN(w.UsedPercent) || math.IsInf(w.UsedPercent, 0) || w.UsedPercent < 0 || w.UsedPercent > 100 || math.IsNaN(w.RemainingFraction) || math.IsInf(w.RemainingFraction, 0) || w.RemainingFraction < 0 || w.RemainingFraction > 1 {
			return false
		}
		reset, err := time.Parse(time.RFC3339Nano, w.ResetAt)
		if err != nil {
			return false
		}
		if !reset.After(time.Now()) || w.RemainingFraction <= 0 || w.SourceStatus == "rate-limited" {
			eligible = false
		}
		if w.SourceStatus != "allowed" && w.SourceStatus != "ok" && w.SourceStatus != "rate-limited" {
			return false
		}
		if w.SourceStatus == "rate-limited" && w.RemainingFraction != 0 {
			return false
		}
		if groups[w.Model] == nil {
			groups[w.Model] = map[string]bool{}
		}
		if groups[w.Model][w.Name] {
			return false
		}
		groups[w.Model][w.Name] = true
	}
	if r.Complete {
		for _, names := range groups {
			if r.Protocol == "codex-oauth-wham" {
				if len(names) != 2 || !names["primary"] || !names["secondary"] {
					return false
				}
			} else {
				if len(names) != 3 || !names["rolling"] || !names["weekly"] || !names["monthly"] {
					return false
				}
			}
		}
	}
	if r.RecoveryEligible != eligible {
		return false
	}
	return true
}
func (s *Service) Job(ctx context.Context, id string) (Job, error) {
	if _, err := uuid.Parse(id); err != nil {
		return Job{}, ErrInput
	}
	doc, err := s.Store.CredentialQuotaJob(ctx, id)
	if err != nil {
		return Job{}, ErrInput
	}
	var job Job
	if json.Unmarshal([]byte(doc), &job) != nil {
		return Job{}, ErrInput
	}
	source, err := s.source(ctx, job.Query.AuthIndex)
	if err != nil || !sameSourceIdentity(source, job.Query) {
		return Job{}, ErrIdentity
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Completion publishes the durable attempt/job atomically under this lock.
	// Re-read after identity I/O so a stale running document cannot become interrupted.
	doc, err = s.Store.CredentialQuotaJob(ctx, id)
	if err != nil || json.Unmarshal([]byte(doc), &job) != nil {
		return Job{}, ErrInput
	}
	st, err := s.Store.CredentialQuota(ctx, key(source.Binding))
	if err != nil {
		return Job{}, err
	}
	if st.Version != job.Query.Version {
		return Job{}, store.ErrQuotaConflict
	}
	active := s.running[key(source.Binding)+":"+operation(source)] == job.ID
	if job.State == "running" && (!active || time.Now().UnixMilli() >= job.DeadlineMS) {
		job.State = "interrupted"
	}
	current, readErr := s.read(ctx, source)
	if readErr != nil {
		return Job{}, readErr
	}
	job.Status = &current
	return job, nil
}

// Core v1 does not forward HTTP Retry-After; its rate_limited observations use
// conservative hourly backoff. This is not a reset/recovery proof.
func (s *Service) NextSourceRetry(ctx context.Context, source Source) (int64, error) {
	return s.budget.RetryAt(ctx, "credential:"+key(source.Binding)+":"+operation(source))
}

// TryJoin has no network side effects. All authority is subsequently revalidated
// through Job, including full binding/sourceVersion and current persisted CAS.
func (s *Service) TryJoin(ctx context.Context, q Query) (Job, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range s.running {
		doc, err := s.Store.CredentialQuotaJob(ctx, id)
		if err != nil {
			return Job{}, true, err
		}
		var job Job
		if err = json.Unmarshal([]byte(doc), &job); err != nil {
			return Job{}, true, err
		}
		if job.State != "running" {
			continue
		}
		if sameSourceIdentity(Source{Binding: job.Query.Binding, AuthIndex: job.Query.AuthIndex, SourceID: job.Query.SourceID, SubjectID: job.Query.SubjectID, SourceVersion: job.Query.SourceVersion}, q) {
			if q.Version != job.RequestedVersion && q.Version != job.Query.Version {
				return Job{}, true, store.ErrQuotaConflict
			}
			job.Joined = true
			return job, true, nil
		}
	}
	return Job{}, false, nil
}
