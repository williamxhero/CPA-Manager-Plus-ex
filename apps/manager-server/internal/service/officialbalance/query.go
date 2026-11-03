package officialbalance

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/queryfeedback"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

type Query struct {
	Binding
	Version int64 `json:"version"`
}
type Job struct {
	Joined     bool    `json:"joined"`
	ID         string  `json:"id"`
	DeadlineMS int64   `json:"deadlineMs"`
	State      string  `json:"state"`
	Binding    Binding `json:"binding"`
	Version    int64   `json:"version"`
	Status     *Status `json:"status,omitempty"`
}
type resolver struct {
	store   *store.Store
	id      string
	version int64
}

func (r resolver) ResolveCredential(ctx context.Context, ref string) (string, error) {
	return r.store.ResolveOfficialBalanceSecret(ctx, r.id, r.version, ref)
}
func (s *Service) Start(ctx context.Context, q Query) (Job, error) {
	started := time.Now()
	if !s.verify(ctx, q.Binding) {
		return Job{}, ErrIdentity
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.read(ctx, q.Binding)
	if err != nil {
		return Job{}, err
	}
	if st.Version != q.Version || !same(st.Binding, q.Binding) {
		return Job{}, store.ErrBalanceConflict
	}
	if q.Version == 0 {
		return Job{}, ErrInput
	}
	if st.Adapter == balance.LaysoBillingWebV1 && !st.DirectQuery {
		// Registration alone cannot authorize a browser read. Keep this source
		// out of the API querier until a verified browser/parser is available.
		return Job{}, ErrInput
	}
	id := key(q.Binding)
	flight := id + ":" + strconv.FormatInt(q.Version, 10) + ":" + operationKey(q.Binding)
	if existing := s.running[flight]; existing != "" {
		doc, e := s.Store.OfficialBalanceJob(ctx, existing)
		var job Job
		if e == nil {
			e = json.Unmarshal([]byte(doc), &job)
		}
		if e != nil {
			return Job{}, e
		}
		if job.State == "running" {
			job.Joined = true
			return job, nil
		}
		// Durable terminal publication ends provider singleflight. Completion
		// consumers may still be running; their old worker keeps its own budget
		// slot but must not turn a legitimate new query into a cached response.
		delete(s.running, flight)
	}
	if retry, err := s.budget.RetryAt(ctx, "official:"+flight); err != nil {
		return Job{}, err
	} else if s.budget.Now().UnixMilli() < retry {
		return Job{}, ErrBusy
	}
	// No unbounded goroutine queue; concurrent refresh callers get a retryable failure.
	jobID := uuid.NewString()
	if _, ok := s.budget.TryAcquire(jobID); !ok {
		return Job{}, ErrBusy
	}
	deadline := started.Add(managementBudget(st))
	if !time.Now().Before(deadline) {
		s.budget.Release(jobID)
		return Job{}, context.DeadlineExceeded
	}
	job := Job{DeadlineMS: deadline.UnixMilli(), ID: jobID, State: "running", Binding: q.Binding, Version: q.Version}
	doc, _ := json.Marshal(job)
	bindingJSON, _ := json.Marshal(job.Binding)
	initial := balance.Observation{QueryID: job.ID, QueryStatus: "running", ObservedAtMS: started.UnixMilli(), DeadlineMS: job.DeadlineMS, Binding: bindingJSON}
	if st.Adapter == balance.LaysoBillingWebV1 {
		initial.ObservedAtMS = 0
	}
	attemptJSON, _ := json.Marshal(initial)
	startCtx, cancelStart := context.WithDeadline(ctx, deadline)
	defer cancelStart()
	if err := s.Store.BeginOfficialBalanceJob(startCtx, id, q.Version, job.ID, string(doc), string(attemptJSON)); err != nil {
		s.budget.Release(jobID)
		return Job{}, err
	}
	s.running[flight] = job.ID
	go s.run(job, st, flight, id)
	return job, nil
}

var ErrBusy = errors.New("balance query busy")

func managementBudget(st Status) time.Duration {
	budget := time.Duration(st.TimeoutMS) * time.Millisecond
	if budget <= 0 {
		budget = 15 * time.Second
	}
	if budget > 60*time.Second {
		budget = 60 * time.Second
	}
	return budget
}

func (s *Service) run(job Job, st Status, flight, id string) {
	defer func() {
		s.mu.Lock()
		if s.running[flight] == job.ID {
			delete(s.running, flight)
		}
		s.mu.Unlock()
		s.budget.Release(job.ID)
	}()
	deadline := time.UnixMilli(job.DeadlineMS)
	budget := time.Until(deadline)
	ctx, cancel := context.WithDeadline(s.budget.Context(job.ID), deadline)
	defer cancel()
	ctx = queryfeedback.WithRetryReceiver(ctx, func(at time.Time) { s.budget.RecordRetry(ctx, "official:"+flight, at) })
	// Reserve part of the SAME management budget for identity revalidation and
	// durable failure recording. The upstream must not consume that entire budget.
	reserve := budget / 5
	if reserve > 2*time.Second {
		reserve = 2 * time.Second
	}

	var obs balance.Observation
	var err error
	startErr := s.queryStarted(ctx, job)
	if startErr == nil && !s.verify(ctx, job.Binding) {
		startErr = ErrIdentity
	}
	if startErr != nil {
		obs = balance.Observation{QueryStatus: "network_error", ErrorCode: "START_FENCE_UNAVAILABLE", ObservedAtMS: time.Now().UnixMilli(), DeadlineMS: job.DeadlineMS}
		if st.Adapter == balance.LaysoBillingWebV1 {
			obs.ObservedAtMS = 0
		}
	} else {
		record, loadErr := s.Store.OfficialBalance(ctx, id)
		if loadErr != nil || record.Version != st.Version {
			record.SecretRef = "invalid"
		}
		if st.Adapter == balance.LaysoBillingWebV1 {
			if loadErr != nil || record.Version != st.Version || s.WebReader == nil {
				obs = balance.Observation{QueryStatus: "unavailable", ErrorCode: "WEB_SOURCE_CHANGED"}
			} else {
				webCtx, webCancel := context.WithDeadline(ctx, deadline.Add(-reserve))
				obs = s.WebReader.Query(webCtx, webRequest(st.Config))
				webCancel()
			}
		} else {
			obs, err = s.Querier.Query(ctx, balance.QueryTarget{CredentialRef: record.SecretRef, Adapter: st.Adapter, EndpointURL: st.Endpoint, PrivateTargets: st.PrivateTargets, Timeout: budget - reserve, NewAPI: st.NewAPI, Volcengine: st.Volcengine, Aliyun: st.Aliyun}, resolver{s.Store, id, st.Version})
		}
	}
	// Revalidate full Core scope/versions, even after failures; no cached authority.
	finishCtx := ctx
	if st.Adapter == balance.LaysoBillingWebV1 {
		// Cancellation must stop acquisition without preventing its failure from
		// becoming durable. Only identity revalidation/SQLite finalization get a
		// fresh cancellation state, bounded by the SAME hard management deadline.
		var finishCancel context.CancelFunc
		finishCtx, finishCancel = context.WithDeadline(context.WithoutCancel(ctx), deadline)
		defer finishCancel()
	}
	valid := s.verify(finishCtx, job.Binding)
	if st.Adapter == balance.LaysoBillingWebV1 && errors.Is(ctx.Err(), context.Canceled) && obs.ErrorCode != "WEB_CLEANUP_FAILED" {
		obs = balance.Observation{CapabilityStatus: "supported", QueryStatus: "network_error", ErrorCode: "WEB_CANCELLED"}
	}
	s.mu.Lock()
	job.State = "discarded"
	if err == nil && valid {
		obs.QueryID = job.ID
		obs.Binding, _ = json.Marshal(job.Binding)
		data, _ := json.Marshal(obs)
		if s.Store.CompleteOfficialBalance(finishCtx, id, st.Version, job.ID, string(data), obs.QueryStatus == "success") == nil {
			job.State = "complete"
			current, e := s.read(finishCtx, job.Binding)
			if e == nil {
				job.Status = &current
			}
		}
	}
	// Never extend the hard management deadline merely to persist diagnostics.
	doc, _ := json.Marshal(job)
	saved := s.Store.SaveOfficialBalanceJob(finishCtx, job.ID, id, string(doc))
	observer := s.completed
	s.mu.Unlock()
	if saved == nil && job.State == "complete" && job.Status != nil && observer != nil {
		observer(job, *job.Status)
	}
}
func (s *Service) Job(ctx context.Context, id string) (Job, error) {
	if _, err := uuid.Parse(id); err != nil {
		return Job{}, ErrInput
	}
	s.mu.Lock()
	doc, err := s.Store.OfficialBalanceJob(ctx, id)
	if err != nil {
		s.mu.Unlock()
		return Job{}, ErrInput
	}
	var job Job
	if json.Unmarshal([]byte(doc), &job) != nil {
		s.mu.Unlock()
		return Job{}, ErrInput
	}
	active := s.running[key(job.Binding)+":"+strconv.FormatInt(job.Version, 10)+":"+operationKey(job.Binding)] == job.ID
	s.mu.Unlock()
	if !s.verify(ctx, job.Binding) {
		return Job{}, ErrIdentity
	}
	st, err := s.read(ctx, job.Binding)
	if err != nil {
		return Job{}, err
	}
	if st.Version != job.Version {
		return Job{}, store.ErrBalanceConflict
	}
	job.Status = &st
	// A persisted running job from a previous host has no active query to await.

	if job.State == "running" && !active {
		job.State = "interrupted"
		if time.Now().UnixMilli() >= job.DeadlineMS && st.Attempt != nil && st.Attempt.QueryID == job.ID {
			job.State = "complete"
			job.Status = &st
		}
	}
	return job, nil
}

func (s *Service) NextRetry(ctx context.Context, q Query) (int64, error) {
	return s.budget.RetryAt(ctx, "official:"+key(q.Binding)+":"+strconv.FormatInt(q.Version, 10)+":"+operationKey(q.Binding))
}

// TryJoin probes only the bounded local flight set and durable job, never sends
// a query. The coordinator revalidates through Job before publishing progress.
func (s *Service) TryJoin(ctx context.Context, q Query) (Job, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	flight := key(q.Binding) + ":" + strconv.FormatInt(q.Version, 10) + ":" + operationKey(q.Binding)
	id := s.running[flight]
	if id == "" {
		return Job{}, false, nil
	}
	doc, err := s.Store.OfficialBalanceJob(ctx, id)
	if err != nil {
		return Job{}, true, err
	}
	var job Job
	if err = json.Unmarshal([]byte(doc), &job); err != nil {
		return Job{}, true, err
	}
	if job.State != "running" {
		return Job{}, false, nil
	}
	job.Joined = true
	return job, true, nil
}
