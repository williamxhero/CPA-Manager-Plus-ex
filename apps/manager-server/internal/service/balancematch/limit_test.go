package balancematch

import (
	"context"
	"testing"
	"time"
)

func TestHostLimiterNextIsProtectedByMutex(t *testing.T) {
	ctx := context.Background()
	var limiter hostLimiter
	lease := limiter.acquire(ctx, "example.test")
	defer limiter.release(lease)

	waited := make(chan bool, 1)
	go func() {
		waited <- lease.wait(ctx, time.Minute)
	}()

	// Check the locking contract directly: the current prune/drop callers only
	// inspect idle leases, but a read under mu must also be safe during a wait.
	limiter.mu.Lock()
	observed := lease.next
	limiter.mu.Unlock()
	if !<-waited {
		t.Fatal("uncanceled lease wait did not reserve a slot")
	}
	limiter.mu.Lock()
	next := lease.next
	limiter.mu.Unlock()
	if next.IsZero() || (!observed.IsZero() && !observed.Equal(next)) {
		t.Fatalf("observed inconsistent cooldown: during wait %v, after wait %v", observed, next)
	}
}
