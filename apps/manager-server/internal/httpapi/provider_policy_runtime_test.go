package httpapi

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	fr "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fullrefresh"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

// Manual-query fixtures run ONLY the actual monetary worker, not the scheduler:
// starting FullRefreshService.Run here would acquire additional money samples.
// The caller must stop/join before shutting down its coordinator or closing DB.
func startProviderPolicyRuntime(t *testing.T, manager *Server) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	policy := manager.AppContext().ProviderPolicyService
	go func() { done <- policy.Run(ctx) }()
	var once sync.Once
	return func() {
		t.Helper()
		once.Do(func() {
			cancel()
			var err error
			select {
			case err = <-done:
			case <-time.After(20 * time.Second):
				t.Error("actual provider policy runtime did not cancel within its lifecycle budget")
				// Do not let failure cleanup close SQLite under an owned worker.
				err = <-done
			}
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("actual provider policy runtime exited: %v", err)
			}
		})
	}
}

func shutdownProviderPolicyManager(t *testing.T, manager *Server, stop func()) {
	t.Helper()
	if stop != nil {
		stop()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := manager.AppContext().FullRefreshService.Shutdown(ctx); err != nil {
		t.Errorf("owned source/coordinator shutdown: %v", err)
		// Also join source callbacks on the failure path before DB cleanup.
		if err := manager.AppContext().FullRefreshService.Shutdown(context.Background()); err != nil {
			t.Errorf("owned source/coordinator join: %v", err)
		}
	}
}

// Read only the durable inbox. A completion notification means persistence,
// NOT that an asynchronous monetary consumer has checked this exact event.
// This barrier preserves blocking-source/generation ordering without replaying
// acquisition, running a private evaluator, or repairing any database record.
func awaitProviderPolicyEvent(t *testing.T, db *store.Store, id string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		// Inspect missing-notification captures FIRST. Reading only done=0
		// events could mistake publication-before-notify for already consumed.
		unfinished, err := db.ProviderPolicyUnfinishedCaptures(ctx)
		if err != nil {
			t.Fatalf("read actual unfinished source captures: %v", err)
		}
		events, err := db.ProviderPolicyEvents(ctx)
		if err != nil {
			t.Fatalf("read actual policy completion inbox: %v", err)
		}
		pending := false
		for _, capture := range unfinished {
			pending = pending || capture.AttemptID == id
		}
		for _, event := range events {
			pending = pending || event.ID == id
		}
		if !pending {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("actual runtime did not consume persisted completion %s: %v", id, ctx.Err())
		case <-ticker.C:
		}
	}
}

func awaitProviderPolicyCondition(t *testing.T, description string, check func() bool) {
	t.Helper()
	timer := time.NewTimer(20 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if check() {
			return
		}
		select {
		case <-timer.C:
			t.Fatal(description)
		case <-ticker.C:
		}
	}
}

type policyHeldOperation struct {
	ctx     context.Context
	started time.Time
}

type policyOperationBarrier struct {
	arrived chan policyHeldOperation
	release chan struct{}
	once    sync.Once
}

func (b *policyOperationBarrier) unblock() { b.once.Do(func() { close(b.release) }) }

// The first real immutable operations POST is held BEFORE forwarding beyond
// both the shared completion callback's deadline and a whole five-second item
// budget. Only the independently owned real Run may finish it. No manual
// Reconcile, resave, extra query, fabricated proof or ACK can rescue this test.
func TestProviderPolicyActualCoreCompletionEscapesCallbackBudget(t *testing.T) {
	f := newPolicyUnsentFixture(t)
	original := f.status()
	before := f.revision()
	barrier := &policyOperationBarrier{arrived: make(chan policyHeldOperation, 1), release: make(chan struct{})}
	defer barrier.unblock() // Release before fixture cleanup cancels/joins Run.
	f.wire.holdOperation.Store(barrier)
	type callbackObservation struct {
		ctx   context.Context
		event fr.CompletedObservation
		err   error
	}
	callbacks := make(chan callbackObservation, 2)
	f.manager.AppContext().FullRefreshService.Subscribe(func(ctx context.Context, event fr.CompletedObservation) {
		callbacks <- callbackObservation{ctx: ctx, event: event, err: ctx.Err()}
	})
	low := f.query("0.05") // Returns after real source persistence/notification only.
	var callback callbackObservation
	select {
	case callback = <-callbacks:
	case <-time.After(5 * time.Second):
		t.Fatal("monetary work blocked the actual completion observer")
	}
	deadline, bounded := callback.ctx.Deadline()
	if !bounded || callback.err != nil || callback.event.AttemptID != low.ID || callback.event.Source.Official == nil || !reflect.DeepEqual(*callback.event.Source.Official, ob.Query{Binding: low.Binding, Version: low.Version}) {
		t.Fatalf("completion callback lost its live shared budget/exact source: %+v err=%v", callback.event, callback.err)
	}
	var held policyHeldOperation
	select {
	case held = <-barrier.arrived:
	case <-time.After(10 * time.Second):
		t.Fatal("persisted completion never reached a real immutable Core POST via Run")
	}
	// Do not use callback.ctx.Done alone: notify cancels it on return, usually
	// before its deadline. Cross the actual deadline AND five seconds of this
	// very HTTP exchange; cancellation/retry of the held attempt is a failure.
	releaseAt := held.started.Add(5*time.Second + 100*time.Millisecond)
	if afterDeadline := deadline.Add(100 * time.Millisecond); afterDeadline.After(releaseAt) {
		releaseAt = afterDeadline
	}
	timer := time.NewTimer(time.Until(releaseAt))
	defer timer.Stop()
	select {
	case <-held.ctx.Done():
		t.Fatalf("real Core POST inherited callback/item budget before release: %v", held.ctx.Err())
	case <-timer.C:
	}
	if held.ctx.Err() != nil || callback.ctx.Err() == nil || !time.Now().After(deadline) {
		t.Fatal("did not prove an active Core exchange beyond the cancelled/expired callback budget")
	}
	attempted, forwarded, accepted := f.wire.snapshot()
	if len(attempted) != 1 || len(forwarded) != 0 || len(accepted) != 0 || f.revision() != before || f.queries.Load() != 1 {
		t.Fatal("held operation was forwarded, retried, accepted, or reacquired before release")
	}
	barrier.unblock()
	f.awaitApplied("callback-budget-pause", low.ID, true, before+1, original)
	f.assertNoPending()
	high := f.query("1.00")
	if high.ID == low.ID {
		t.Fatal("fresh recovery reused the callback-budget low attempt")
	}
	f.awaitApplied("callback-budget-fresh-clear", high.ID, false, before+2, original)
	f.assertNoPending()
	f.assertExactReceipts(before, "pause", "clear")
	attempted, forwarded, _ = f.wire.snapshot()
	if len(attempted) != 2 || len(forwarded) != 2 || f.queries.Load() != 2 || f.revision() != before+2 {
		t.Fatal("callback escape/recovery requires exactly two queries, unique operations and revision increments")
	}
	f.t.Logf("actual Core revision=%s sha256=%s; completionDeadline=%s heldPOST=%s low=%s high=%s; independent Run escaped callback budget without scheduler, rescue Reconcile or duplicate operation", f.core.commit, f.core.digest, deadline.Format(time.RFC3339Nano), time.Since(held.started), low.ID, high.ID)
}
