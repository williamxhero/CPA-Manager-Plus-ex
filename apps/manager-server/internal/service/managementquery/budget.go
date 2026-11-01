// Package managementquery bounds all management observation hosts together.
// It is deliberately unrelated to inference transport budgets.
package managementquery

import (
	"context"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"sync"
	"time"
)

type Budget struct {
	mu       sync.Mutex
	jobs     map[string]context.CancelFunc
	contexts map[string]context.Context
	wg       sync.WaitGroup
	closed   bool
	store    *store.Store
	retry    map[string]int64
}

func NewBudget(stores ...*store.Store) *Budget {
	b := &Budget{jobs: map[string]context.CancelFunc{}, contexts: map[string]context.Context{}, retry: map[string]int64{}}
	if len(stores) > 0 {
		b.store = stores[0]
	}
	return b
}
func (b *Budget) RetryAt(ctx context.Context, key string) (int64, error) {
	if b.store != nil {
		return b.store.ManagementQueryRetryAt(ctx, key)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.retry[key], nil
}
func (b *Budget) RecordRetry(ctx context.Context, key string, at time.Time) {
	if b.store != nil {
		_ = b.store.SaveManagementQueryRetryAt(ctx, key, at.UnixMilli())
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if at.UnixMilli() > b.retry[key] {
		b.retry[key] = at.UnixMilli()
	}
}

// TryAcquire never creates a waiting goroutine; the shared bound is exactly4.
func (b *Budget) TryAcquire(id string) (context.Context, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || len(b.jobs) >= 4 {
		return nil, false
	}
	if _, exists := b.jobs[id]; exists {
		return nil, false
	}
	ctx, cancel := context.WithCancel(context.Background())
	b.jobs[id] = cancel
	b.contexts[id] = ctx
	b.wg.Add(1)
	return ctx, true
}
func (b *Budget) Context(id string) context.Context {
	b.mu.Lock()
	defer b.mu.Unlock()
	if ctx := b.contexts[id]; ctx != nil {
		return ctx
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}
func (b *Budget) Release(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if c := b.jobs[id]; c != nil {
		c()
		delete(b.jobs, id)
		delete(b.contexts, id)
		b.wg.Done()
	}
}
func (b *Budget) Cancel(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if c := b.jobs[id]; c != nil {
		c()
	}
}
func (b *Budget) Close(ctx context.Context) error {
	b.mu.Lock()
	b.closed = true
	for _, cancel := range b.jobs {
		cancel()
	}
	b.mu.Unlock()
	done := make(chan struct{})
	go func() { b.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
