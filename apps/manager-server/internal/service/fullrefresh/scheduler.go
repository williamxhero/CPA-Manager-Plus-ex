package fullrefresh

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

// SourceScheduleKey returns the existing exact-source scheduling identifier.
// It conveys neither query authority nor observation or policy-generation proof.
func SourceScheduleKey(request SourceRequest) string {
	return sourceScheduleKey(SourceStatus{Request: request})
}

func sourceScheduleKey(src SourceStatus) string {
	request := src.Request
	if request.Credential != nil {
		q := *request.Credential
		q.Version = 0
		q.TimeoutMS = 0
		request.Credential = &q
	}
	raw, _ := json.Marshal(request)
	sum := sha256.Sum256(raw)
	return "source:" + hex.EncodeToString(sum[:])
}
func roundScheduleKey(inv Inventory) string {
	return "round:" + inv.Scope.InstanceID + ":" + inv.Scope.InstanceIncarnation
}

// SubscribeScheduleCycle registers bounded local scheduling work before each
// scheduler inventory snapshot. Callbacks run serially outside the coordinator
// lock, with the existing cycle context. Errors skip that cycle. This is not an
// acquisition event or authority/proof; manual/source/round starts never call it.
// Register subscribers during startup before Run.
func (s *Service) SubscribeScheduleCycle(observer func(context.Context) error) {
	if observer == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scheduleCycleObservers = append(s.scheduleCycleObservers, observer)
}

// Run is independent of collector/browser and every policy ON/OFF setting.
// Ticks are scheduler time only. Upstream timestamps retain real-time provenance.
func (s *Service) Run(ctx context.Context) error {
	s.mu.Lock()
	if s.schedulerRunning || s.closed {
		s.mu.Unlock()
		return ErrUnavailable
	}
	if err := s.ensureLocked(ctx); err != nil {
		s.mu.Unlock()
		return err
	}
	s.schedulerRunning = true
	s.schedulerAnchor = s.now()
	s.mu.Unlock()
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = s.Shutdown(shutdown)
	}()
	var ticks <-chan time.Time
	var ticker *time.Ticker
	if s.clock != nil {
		ticks = s.clock.Ticks()
	} else {
		ticker = time.NewTicker(time.Minute)
		defer ticker.Stop()
		ticks = ticker.C
	}
	for {
		s.schedule(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-s.ctx.Done():
			return nil
		case _, ok := <-ticks:
			if !ok {
				return nil
			}
		}
	}
}
func (s *Service) schedule(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	s.mu.Lock()
	observers := append([]func(context.Context) error(nil), s.scheduleCycleObservers...)
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return
	}
	for _, observer := range observers {
		if ctx.Err() != nil {
			return
		}
		if err := observer(ctx); err != nil {
			return
		}
	}
	if ctx.Err() != nil {
		return
	}
	inv, err := s.Inventory(ctx)
	if err != nil {
		return
	}
	now := s.now().UnixMilli()
	key := roundScheduleKey(inv)
	due, err := s.Store.FullRefreshDue(ctx, key)
	if err != nil {
		return
	}
	needed := due == 0 || now >= due || inv.LatestRound != nil && inv.LatestRound.State == "interrupted"
	for _, source := range inv.Sources {
		if source.QueryCapable && (source.NextDueMS == 0 || source.NextDueMS <= now) {
			needed = true
		}
	}
	s.mu.Lock()
	needed = needed || s.recoveryNeeded
	s.recoveryNeeded = false
	s.mu.Unlock()
	if needed {
		p, err := s.StartRound(ctx, Periodic)
		if err == nil {
			_ = s.Store.SaveFullRefreshDue(ctx, key, p.PeriodicNextDueMS)
		}
	}
	_ = s.Store.SaveFullRefreshDue(ctx, key+":checked", now)
}

// A joined periodic round retains its original cycle anchor.
func (s *Service) enrollPeriodic(ctx context.Context, p *Progress, inv Inventory) {
	if p.PeriodicNextDueMS != 0 {
		return
	}
	due, err := s.Store.FullRefreshDue(ctx, roundScheduleKey(inv))
	if err != nil {
		return
	}
	now := s.now().UnixMilli()
	if due == 0 {
		anchor := s.schedulerAnchor
		if anchor.IsZero() {
			anchor = s.now()
		}
		due = anchor.Add(time.Hour).UnixMilli()
	}
	if due <= now {
		due += ((now-due)/time.Hour.Milliseconds() + 1) * time.Hour.Milliseconds()
	}
	p.PeriodicNextDueMS = due
	for i := range p.Items {
		if p.Items[i].Queried || p.Items[i].Outcome == "shared_wallet" {
			s.markAdmitted(ctx, p, &p.Items[i])
		}
	}
}
func (s *Service) markAdmitted(ctx context.Context, p *Progress, item *Item) {
	if p.PeriodicNextDueMS == 0 {
		return
	}
	next := p.PeriodicNextDueMS
	if next <= s.now().UnixMilli() {
		next = s.now().Add(time.Hour).UnixMilli()
	}
	if err := s.Store.SaveFullRefreshDue(ctx, sourceScheduleKey(item.SourceStatus), next); err == nil {
		item.NextDueMS = next
	}
}

// A real terminal host attempt covers this exact current source in the current
// cycle, even when acquired manually before the scheduler starts. Keep an
// existing future cycle anchor; a manual request must not postpone it. This is
// scheduling credit only, never a new observation, authentication or callback.
func (s *Service) creditCompletedSource(source SourceRequest) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()
	row := SourceStatus{Request: source}
	key := sourceScheduleKey(row)
	now := s.now()
	due, err := s.Store.FullRefreshDue(ctx, key)
	if err != nil || due > now.UnixMilli() {
		return
	}
	var inv Inventory
	if source.Official != nil {
		inv.Scope = source.Official.Scope
	} else if source.Credential != nil {
		inv.Scope = source.Credential.Scope
	} else {
		return
	}
	next, err := s.Store.FullRefreshDue(ctx, roundScheduleKey(inv))
	if err != nil {
		return
	}
	if next <= now.UnixMilli() {
		next = now.Add(time.Hour).UnixMilli()
	}
	_ = s.Store.SaveFullRefreshDue(ctx, key, next)
}
