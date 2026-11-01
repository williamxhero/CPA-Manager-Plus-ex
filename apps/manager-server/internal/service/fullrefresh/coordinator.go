package fullrefresh

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	"reflect"
	"sort"
	"time"
)

func validPurpose(p Purpose) bool { return p == Manual || p == Periodic || p == Recovery }
func (s *Service) now() time.Time {
	if s.clock != nil {
		return s.clock.Now()
	}
	return time.Now()
}
func (s *Service) save(ctx context.Context, p *Progress) error {
	doc, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return s.Store.SaveFullRefresh(ctx, p.ID, p.State, string(doc))
}
func clone(p *Progress) Progress {
	b, _ := json.Marshal(p)
	var out Progress
	_ = json.Unmarshal(b, &out)
	return out
}
func (s *Service) ensureLocked(ctx context.Context) error {
	if s.closed {
		return ErrUnavailable
	}
	if s.started {
		return nil
	}
	docs, err := s.Store.InterruptedFullRefresh(ctx)
	if err != nil {
		return err
	}
	if len(docs) > 0 {
		s.recoveryNeeded = true
	}
	for _, doc := range docs {
		var p Progress
		if json.Unmarshal([]byte(doc), &p) != nil {
			return ErrUnavailable
		}
		p.State = "interrupted"
		p.FinishedAtMS = time.Now().UnixMilli()
		for i := range p.Items {
			if p.Items[i].Outcome == "queued" || p.Items[i].Outcome == "running" {
				p.Items[i].Outcome = "interrupted"
				p.Items[i].Stale = true
			}
		}
		recount(&p)
		if err = s.save(ctx, &p); err != nil {
			return err
		}
	}
	s.started = true
	go s.loop()
	return nil
}
func (s *Service) StartRound(ctx context.Context, purpose Purpose) (Progress, error) {
	if !validPurpose(purpose) {
		return Progress{}, ErrInput
	}
	// Never join a previous instance's round just because it is still in memory.
	inv, err := s.Inventory(ctx)
	if err != nil {
		return Progress{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err = s.ensureLocked(ctx); err != nil {
		return Progress{}, err
	}
	if p := s.rounds[s.activeRound]; p != nil && p.State == "running" {
		if p.Scope == inv.Scope {
			return clone(p), nil
		}
		s.cancelLocked(p, "discarded")
	}
	p := &Progress{ID: uuid.NewString(), Purpose: purpose, Scope: inv.Scope, State: "running", StartedAtMS: s.now().UnixMilli(), ResourceCount: inv.ResourceCount, Items: []Item{}}
	for _, source := range inv.Sources {
		outcome := "not_queried"
		if source.QueryCapable {
			outcome = "queued"
		}
		p.Items = append(p.Items, Item{SourceStatus: source, Outcome: outcome})
	}
	// Stable resource ordering; no one goroutine per source and no credential-label key.
	sort.SliceStable(p.Items, func(i, j int) bool { return p.Items[i].ID < p.Items[j].ID })
	recount(p)
	if err = s.save(ctx, p); err != nil {
		return Progress{}, err
	}
	s.rounds[p.ID] = p
	s.activeRound = p.ID
	return clone(p), nil
}
func requestSame(a, b SourceRequest) bool {
	if a.Kind != b.Kind {
		return false
	}
	if a.Official != nil && b.Official != nil {
		return reflect.DeepEqual(*a.Official, *b.Official)
	}
	if a.Credential != nil && b.Credential != nil {
		x, y := *a.Credential, *b.Credential
		x.Version = 0
		y.Version = 0
		x.TimeoutMS = 0
		y.TimeoutMS = 0
		return reflect.DeepEqual(x, y)
	}
	return false
}
func (s *Service) StartSingleSource(ctx context.Context, request SourceRequest, purpose Purpose) (Progress, error) {
	if !validPurpose(purpose) {
		return Progress{}, ErrInput
	}
	inv, err := s.Inventory(ctx)
	if err != nil {
		return Progress{}, err
	}
	var source *SourceStatus
	for _, row := range inv.Sources {
		if requestSame(row.Request, request) {
			r := row
			source = &r
			break
		}
	}
	if source == nil {
		return Progress{}, ErrInput
	}
	// CAS is not erased just because source identity matches.

	s.mu.Lock()
	defer s.mu.Unlock()
	if err = s.ensureLocked(ctx); err != nil {
		return Progress{}, err
	}
	for _, p := range s.rounds {
		if p.State != "running" || p.Scope != inv.Scope {
			continue
		}
		for i := range p.Items {
			if requestSame(p.Items[i].Request, request) {
				if request.Credential != nil && p.Items[i].JobID != "" {
					job, e := s.Credential.Job(ctx, p.Items[i].JobID)
					if e != nil || request.Credential.Version != job.RequestedVersion && request.Credential.Version != job.Query.Version {
						return Progress{}, ErrInput
					}
				}
				p.Items[i].Joined = true
				_ = s.save(ctx, p)
				return clone(p), nil
			}
		}
	}
	if request.Credential != nil && source.Request.Credential.Version != request.Credential.Version {
		return Progress{}, ErrInput
	}
	if len(s.rounds) >= 128 {
		return Progress{}, ErrUnavailable
	}
	p := &Progress{ID: uuid.NewString(), Purpose: purpose, Scope: inv.Scope, State: "running", StartedAtMS: s.now().UnixMilli(), ResourceCount: len(source.Targets), Items: []Item{{SourceStatus: *source, Outcome: "queued"}}}
	if !source.QueryCapable {
		p.Items[0].Outcome = "not_queried"
		p.State = "partial"
	}
	recount(p)
	if err = s.save(ctx, p); err != nil {
		return Progress{}, err
	}
	s.rounds[p.ID] = p
	return clone(p), nil
}
func (s *Service) GetProgress(ctx context.Context, id string) (Progress, error) {
	if _, err := uuid.Parse(id); err != nil {
		return Progress{}, ErrInput
	}
	doc, err := s.Store.FullRefresh(ctx, id)
	if err != nil {
		return Progress{}, ErrInput
	}
	var p Progress
	if json.Unmarshal([]byte(doc), &p) != nil {
		return Progress{}, ErrUnavailable
	}
	return p, nil
}
func recount(p *Progress) {
	p.Succeeded = 0
	p.Failed = 0
	p.NotQueried = 0
	p.Pending = 0
	for _, item := range p.Items {
		switch item.Outcome {
		case "queued", "running":
			p.Pending++
		case "success":
			p.Succeeded++
		case "not_queried":
			p.NotQueried++
		default:
			p.Failed++
		}
	}
}
func (s *Service) loop() {
	defer close(s.done)
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.sweep()
		}
	}
}
func (s *Service) sweep() {
	ctx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
	defer cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, p := range s.rounds {
		if p.State != "running" {
			delete(s.rounds, id)
			continue
		}
		admitted := 0
		for i := range p.Items {
			item := &p.Items[i]
			if item.Outcome == "queued" && time.Now().UnixMilli() < item.RetryNotBeforeMS {
				item.Outcome = "rate_limited"
				item.Reason = "source_retry_after"
				item.Stale = true
				continue
			}
			if item.Outcome == "queued" && s.now().UnixMilli()-p.StartedAtMS > 60000 {
				item.Outcome = "admission_timeout"
				item.Reason = "global_management_budget_busy"
				continue
			}
			if item.Outcome == "queued" && admitted < 4 {
				var err error
				switch item.Request.Kind {
				case Official:
					var job ob.Job
					job, err = s.Official.Start(ctx, *item.Request.Official)
					if err == nil {
						item.JobID = job.ID
					}
				case Credential:
					var job cq.Job
					job, err = s.Credential.Start(ctx, *item.Request.Credential)
					if err == nil {
						item.JobID = job.ID
						item.Request.Credential.Version = job.Query.Version
					}
				default:
					err = ErrInput
				}
				if errors.Is(err, ob.ErrBusy) || errors.Is(err, cq.ErrBusy) {
					break
				}
				if err != nil {
					item.Outcome = "identity_changed"
					item.Reason = "source_authority_changed"
					item.Stale = true
					continue
				}
				item.Outcome = "running"
				item.Queried = true
				s.purposes[item.JobID] = p.Purpose
				admitted++
			}
			if item.Outcome != "running" {
				continue
			}
			s.poll(ctx, item)
		}
		recount(p)
		if p.Pending == 0 {
			p.State = "complete"
			if p.Failed > 0 || p.NotQueried > 0 {
				p.State = "partial"
			}
			p.FinishedAtMS = s.now().UnixMilli()
			if s.activeRound == p.ID {
				s.activeRound = ""
			}
		}
		_ = s.save(ctx, p)
	}
}
func (s *Service) poll(ctx context.Context, item *Item) {
	var state string
	var err error
	switch item.Request.Kind {
	case Official:
		var job ob.Job
		job, err = s.Official.Job(ctx, item.JobID)
		state = job.State
		if err == nil && state == "complete" && job.Status != nil {
			item.Attempt = job.Status.Attempt
			item.LastSuccess = job.Status.LastSuccess
			item.Stale = job.Status.Stale
		}
	case Credential:
		var job cq.Job
		job, err = s.Credential.Job(ctx, item.JobID)
		state = job.State
		if err == nil && state == "complete" {
			var status cq.Status
			status, err = s.Credential.Status(ctx, item.Request.Credential.AuthIndex)
			if err == nil {
				item.Attempt = status.Attempt
				item.LastSuccess = status.LastSuccess
				item.Stale = status.Stale
			}
		}
	}
	if err != nil {
		item.Outcome = "identity_changed"
		item.Reason = "source_authority_changed"
		item.Stale = true
		return
	}
	if state == "running" {
		return
	}
	item.Outcome = "interrupted"
	item.Stale = true
	if state == "complete" && item.Attempt != nil && item.Attempt.QueryID == item.JobID {
		item.Outcome = item.Attempt.QueryStatus
		item.State = item.Attempt.QueryStatus
	}
}
func (s *Service) cancelLocked(p *Progress, state string) {
	for i := range p.Items {
		item := &p.Items[i]
		if item.Outcome != "queued" && item.Outcome != "running" {
			continue
		}
		if !item.Joined {
			s.budget.Cancel(item.JobID)
		}
		item.Outcome = state
		item.Stale = true
	}
	p.State = state
	p.FinishedAtMS = s.now().UnixMilli()
	recount(p)
	_ = s.save(context.Background(), p)
	if s.activeRound == p.ID {
		s.activeRound = ""
	}
}
func (s *Service) Cancel(ctx context.Context, id string) (Progress, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.rounds[id]
	if p == nil {
		return Progress{}, ErrInput
	}
	s.cancelLocked(p, "cancelled")
	return clone(p), nil
}

// Shutdown cancels ALL hosts, even manual requests through the old source APIs.
func (s *Service) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	s.cancel()
	for _, p := range s.rounds {
		if p.State == "running" {
			s.cancelLocked(p, "interrupted")
		}
	}
	started := s.started
	s.mu.Unlock()
	err := s.budget.Close(ctx)
	if started {
		select {
		case <-s.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}
func (s *Service) Subscribe(observer Observer) {
	if observer == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.observers = append(s.observers, observer)
}
func (s *Service) notify(event CompletedObservation) {
	s.mu.Lock()
	event.Purpose = s.purposes[event.AttemptID]
	if event.Purpose == "" {
		event.Purpose = Manual
	}
	delete(s.purposes, event.AttemptID)
	observers := append([]Observer(nil), s.observers...)
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()
	for _, observer := range observers {
		observer(ctx, event)
	}
}
func (s *Service) completedOfficial(job ob.Job, status ob.Status) {
	if job.State != "complete" || status.Attempt == nil || status.Attempt.QueryID != job.ID {
		return
	}
	q := ob.Query{Binding: job.Binding, Version: job.Version}
	s.notify(CompletedObservation{Source: SourceRequest{Kind: Official, Official: &q}, AttemptID: job.ID, Official: &status})
}
func (s *Service) completedCredential(job cq.Job, status cq.Status) {
	if job.State != "complete" || status.Attempt == nil || status.Attempt.QueryID != job.ID {
		return
	}
	q := job.Query
	s.notify(CompletedObservation{Source: SourceRequest{Kind: Credential, Credential: &q}, AttemptID: job.ID, Credential: &status})
}
