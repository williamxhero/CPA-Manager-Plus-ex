package credentialpolicy

import (
	"context"
	"sync"
	"time"

	fr "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fullrefresh"
)

// CoordinatedService preserves76's entire Source/Start/Job implementation. Only
// runtime lifecycle is extended; policy does not own another quota scheduler.
type CoordinatedService struct {
	*fr.Service
	Policy *Service
	// Background runs additional durable policy workers under this same owned
	// lifecycle. Register once before Run; none owns another quota scheduler.
	Background []func(context.Context) error
	mu         sync.Mutex
	stop       context.CancelFunc
	done       chan struct{}
	closed     bool
}

func (s *CoordinatedService) Run(ctx context.Context) error {
	s.mu.Lock()
	if s.closed || s.done != nil {
		s.mu.Unlock()
		return ErrUnavailable
	}
	worker, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	s.stop, s.done = cancel, done
	s.mu.Unlock()
	// One startup catalog/source discovery, before76 admits any scheduled job.
	// This is not a quota acquisition or another background query loop. Missing
	// discovery stays unavailable; Started never waits on a discovery/proof RPC.
	discovery, stopDiscovery := context.WithTimeout(worker, 5*time.Second)
	_, _ = s.Policy.source(discovery, "") // warms all descriptors; selects no credential
	stopDiscovery()
	go func() {
		defer close(done)
		var workers sync.WaitGroup
		run := func(task func(context.Context) error) {
			if task == nil {
				return
			}
			workers.Add(1)
			go func() { defer workers.Done(); _ = task(worker) }()
		}
		run(s.Policy.Run)
		for _, task := range s.Background {
			run(task)
		}
		workers.Wait()
	}()
	defer func() { cancel(); <-done }()
	return s.Service.Run(ctx)
}

func (s *CoordinatedService) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	stop, done := s.stop, s.done
	s.mu.Unlock()
	if stop != nil {
		stop()
	}
	err := s.Service.Shutdown(ctx)
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

// Run dispatches only durable completions and the operation journal. Both actual
// send claims and enqueue transactions use the shared per-DB fenced lease.
// A periodic wake reconciles lost ACKs/restart without relying on a UI resave.
func (s *Service) Run(ctx context.Context) error {
	s.runMu.Lock()
	if s.running {
		s.runMu.Unlock()
		return ErrUnavailable
	}
	s.running = true
	s.runMu.Unlock()
	defer func() { s.runMu.Lock(); s.running = false; s.runMu.Unlock() }()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return nil
		}
		attempt, cancel := context.WithTimeout(ctx, 15*time.Second)
		_ = s.ProcessCompletions(attempt)
		_ = s.Reconcile(attempt)
		cancel()
		select {
		case <-ctx.Done():
			return nil
		case <-s.wake:
		case <-ticker.C:
		}
	}
}

func (s *Service) wakeWorker() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
