package providerpolicy

import (
	"context"
	"time"
)

// Run consumes only the durable completion inbox and decision journal. It never
// starts a balance query, borrows a source callback's short deadline, or changes
// manual state. The host owns cancellation and joins this worker before SQLite.
func (s *Service) Run(ctx context.Context) error {
	s.runMu.Lock()
	if s.running {
		s.runMu.Unlock()
		return ErrUnavailable
	}
	s.running = true
	s.runMu.Unlock()
	defer func() { s.runMu.Lock(); s.running = false; s.runMu.Unlock() }()
	for {
		if ctx.Err() != nil {
			return nil
		}
		// Upgrade/restart must restore protection before monetary reconcile
		// can admit new automatic traffic. Pending/unknown dialect holds.
		ready, readyErr := s.Store.ProviderPolicyMigrationReadiness(ctx)
		started := time.Now()
		var err error
		if readyErr != nil || !ready.Ready {
			err = readyErr
			if err == nil {
				err = ErrUnavailable
			}
		} else {
			// Reconcile establishes its own <=15-second, pre-lock budget and
			// rotates the durable frontier before each attempt. Errors do not
			// erase pending evidence; the next wake/restart retries it safely.
			err = s.Reconcile(ctx)
		}
		// Credential policy shares this fenced lease and polls every five
		// seconds. A tick buffered during slow I/O, or a queued completion,
		// must not immediately reacquire it after every slow/failed pass.
		if !waitRuntimeCycle(ctx, s.wake, 5*time.Second, err != nil || time.Since(started) >= 5*time.Second) {
			return nil
		}
	}
}

func waitRuntimeCycle(ctx context.Context, wake <-chan struct{}, delay time.Duration, mandatoryYield bool) bool {
	timer := time.NewTimer(delay) // Starts AFTER the preceding lease is released.
	defer timer.Stop()
	if mandatoryYield {
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return true
		}
	}
	select {
	case <-ctx.Done():
		return false
	case <-wake:
		return true
	case <-timer.C:
		return true
	}
}

func (s *Service) wakeWorker() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
