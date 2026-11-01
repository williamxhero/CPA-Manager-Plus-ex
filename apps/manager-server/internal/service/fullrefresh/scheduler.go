package fullrefresh

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

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
	needed := due == 0 || now >= due
	for _, source := range inv.Sources {
		if source.QueryCapable && source.NextDueMS == 0 {
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
			// Advance the fixed periodic anchor ONLY for due periodic cycles. Manual and
			// recovery work never postpone it. Many missed hours coalesce into one round.
			next := due
			if due == 0 || now >= due {
				next = now + time.Hour.Milliseconds()
			}
			_ = s.Store.SaveFullRefreshDue(ctx, key, next)
			for _, item := range p.Items {
				if item.QueryCapable {
					_ = s.Store.SaveFullRefreshDue(ctx, sourceScheduleKey(item.SourceStatus), next)
				}
			}
		}
	}
	_ = s.Store.SaveFullRefreshDue(ctx, key+":checked", now)
}
