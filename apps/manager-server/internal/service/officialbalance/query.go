package officialbalance

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

type Query struct {
	Binding
	Version int64 `json:"version"`
}
type Job struct {
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
	id := key(q.Binding)
	flight := id + ":" + strconv.FormatInt(q.Version, 10) + ":" + operationKey(q.Binding)
	if existing := s.running[flight]; existing != "" {
		doc, e := s.Store.OfficialBalanceJob(ctx, existing)
		var job Job
		if e == nil {
			e = json.Unmarshal([]byte(doc), &job)
		}
		return job, e
	}
	// No unbounded goroutine queue; concurrent refresh callers get a retryable failure.
	select {
	case s.slots <- struct{}{}:
	default:
		return Job{}, ErrBusy
	}
	deadline := started.Add(managementBudget(st))
	if !time.Now().Before(deadline) {
		<-s.slots
		return Job{}, context.DeadlineExceeded
	}
	job := Job{DeadlineMS: deadline.UnixMilli(), ID: uuid.NewString(), State: "running", Binding: q.Binding, Version: q.Version}
	doc, _ := json.Marshal(job)
	bindingJSON, _ := json.Marshal(job.Binding)
	initial := balance.Observation{QueryID: job.ID, QueryStatus: "running", ObservedAtMS: started.UnixMilli(), DeadlineMS: job.DeadlineMS, Binding: bindingJSON}
	attemptJSON, _ := json.Marshal(initial)
	startCtx, cancelStart := context.WithDeadline(ctx, deadline)
	defer cancelStart()
	if err := s.Store.BeginOfficialBalanceJob(startCtx, id, q.Version, job.ID, string(doc), string(attemptJSON)); err != nil {
		<-s.slots
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
	defer func() { s.mu.Lock(); delete(s.running, flight); s.mu.Unlock(); <-s.slots }()
	deadline := time.UnixMilli(job.DeadlineMS)
	budget := time.Until(deadline)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	// Reserve part of the SAME management budget for identity revalidation and
	// durable failure recording. The upstream must not consume that entire budget.
	reserve := budget / 5
	if reserve > 2*time.Second {
		reserve = 2 * time.Second
	}
	record, loadErr := s.Store.OfficialBalance(ctx, id)
	if loadErr != nil || record.Version != st.Version {
		record.SecretRef = "invalid"
	}
	obs, err := s.Querier.Query(ctx, balance.QueryTarget{CredentialRef: record.SecretRef, Adapter: st.Adapter, EndpointURL: st.Endpoint, PrivateTargets: st.PrivateTargets, Timeout: budget - reserve, NewAPI: st.NewAPI}, resolver{s.Store, id, st.Version})
	// Revalidate full Core scope/versions, even after failures; no cached authority.
	valid := s.verify(ctx, job.Binding)
	s.mu.Lock()
	defer s.mu.Unlock()
	job.State = "discarded"
	if err == nil && valid {
		obs.QueryID = job.ID
		obs.Binding, _ = json.Marshal(job.Binding)
		data, _ := json.Marshal(obs)
		if s.Store.CompleteOfficialBalance(ctx, id, st.Version, job.ID, string(data), obs.QueryStatus == "success") == nil {
			job.State = "complete"
			current, e := s.read(ctx, job.Binding)
			if e == nil {
				job.Status = &current
			}
		}
	}
	// Never extend the hard management deadline merely to persist diagnostics.
	doc, _ := json.Marshal(job)
	_ = s.Store.SaveOfficialBalanceJob(ctx, job.ID, id, string(doc))
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
