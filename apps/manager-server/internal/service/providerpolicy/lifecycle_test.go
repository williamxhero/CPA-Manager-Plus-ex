package providerpolicy

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	fr "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fullrefresh"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

// Persistence/lifecycle regressions only; these do not replace authenticated
// Manager -> actual Core routing and monetary evidence tests.
func TestCompletedOnlyPersistsInboxAndCoalescesRuntimeWake(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "callback.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db, nil, nil, nil)
	ctx := context.Background()
	event := fr.CompletedObservation{AttemptID: "first-completed", Source: fr.SourceRequest{Kind: fr.Official, Official: &ob.Query{}}}
	s.completed(ctx, event)
	s.completed(ctx, event) // Same persisted attempt, not a second decision.
	event.AttemptID = "second-completed"
	s.completed(ctx, event)
	work, err := db.ProviderPolicyFrontier(ctx)
	if err != nil || len(work) != 2 {
		t.Fatalf("callback consumed durable work instead of deferring it: work=%+v err=%v", work, err)
	}
	if len(s.wake) != 1 {
		t.Fatalf("completion wakes did not coalesce: %d", len(s.wake))
	}
	for _, item := range work {
		if item.Kind != "event" || item.ID != "first-completed" && item.ID != "second-completed" {
			t.Fatalf("unexpected durable callback work: %+v", item)
		}
	}
}

func TestRuntimeRejectsConcurrentRunAndReleasesLeaseBeforeExit(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "runtime.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := New(db, nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var runErr error
	go func() { runErr = s.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("owned policy worker did not join")
		}
	})
	started := false
	for until := time.Now().Add(time.Second); time.Now().Before(until); {
		s.runMu.Lock()
		started = s.running
		s.runMu.Unlock()
		if started {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !started {
		t.Fatal("runtime did not start")
	}
	if err := s.Run(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("concurrent runtime accepted: %v", err)
	}
	cancel()
	select {
	case <-done:
		if runErr != nil {
			t.Fatal(runErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runtime did not exit on cancellation")
	}
	lease, err := db.AcquireCredentialPolicyLease(context.Background(), "00000000-0000-4000-8000-000000000041", time.Now().UnixMilli(), 30000)
	if err != nil {
		t.Fatalf("exited worker retained its lease: %v", err)
	}
	if err := db.ReleaseCredentialPolicyLease(context.Background(), lease); err != nil {
		t.Fatal(err)
	}
	stopped, stop := context.WithCancel(context.Background())
	stop()
	if err := s.Run(stopped); err != nil {
		t.Fatalf("completed runtime could not be re-entered with cancelled context: %v", err)
	}
}

func TestRuntimeMandatoryLeaseYieldCannotBeBypassedByQueuedWake(t *testing.T) {
	wake := make(chan struct{}, 1)
	wake <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if waitRuntimeCycle(ctx, wake, time.Hour, true) || ctx.Err() == nil {
		t.Fatal("queued completion bypassed the mandatory post-lease yield")
	}
	if len(wake) != 1 {
		t.Fatal("mandatory backoff lost the queued completion notification")
	}
	fast, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if !waitRuntimeCycle(fast, wake, time.Hour, false) || fast.Err() != nil {
		t.Fatal("fast successful cycle no longer wakes promptly")
	}
}
