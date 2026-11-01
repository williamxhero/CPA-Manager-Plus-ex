package httpapi

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	fr "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fullrefresh"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"modernc.org/sqlite"
)

// The real scheduler, SQLite and authenticated host share this seam. A cycle
// callback must run before inventory snapshots due credit, not merely before
// upstream admission; moving it after Inventory leaves the selected item not_due.
func TestFullRefreshActualCoreScheduleCycleBeforeInventoryAndFailClosed(t *testing.T) {
	f := newRefreshFixture(t)
	inv := f.configuredSources()
	app := f.manager.AppContext()
	clock := &refreshClock{now: time.Now(), ticks: make(chan time.Time, 8)}
	var cycles, followers, expectedCycle, prematureReads atomic.Int32
	db := sql.OpenDB(&cycleReadConnector{path: app.Config.DBPath, cycles: &cycles, expected: &expectedCycle, premature: &prematureReads})
	t.Cleanup(func() { db.Close() })
	svc := fr.New(store.New(db), app.ResourceCapabilityService, app.OfficialBalanceService, app.CredentialQuotaService, fr.WithBudget(app.ManagementQueryBudget), fr.WithClock(clock))
	app.FullRefreshService = svc
	var selected fr.SourceStatus
	for _, source := range inv.Sources {
		if source.Request.Kind == fr.Official {
			selected = source
			break
		}
	}
	if selected.ID == "" {
		t.Fatal("actual official source missing")
	}
	events := make(chan fr.CompletedObservation, 32)
	svc.Subscribe(func(ctx context.Context, event fr.CompletedObservation) {
		select {
		case events <- event:
		case <-ctx.Done():
		}
	})
	acquire := func() ob.Job {
		t.Helper()
		code, data := f.api("POST", balancePath, selected.Request.Official)
		var job ob.Job
		if code != 202 || json.Unmarshal(data, &job) != nil {
			t.Fatalf("manual %d %s", code, data)
		}
		deadline := time.After(5 * time.Second)
		for {
			select {
			case event := <-events:
				if event.AttemptID == job.ID {
					if event.Official == nil || event.Official.Attempt == nil || event.Official.Attempt.QueryStatus != "success" {
						t.Fatal("actual manual query failed")
					}
					return job
				}
			case <-deadline:
				t.Fatal("actual manual completion missing")
			}
		}
	}
	entered := make(chan int32, 4)
	release := make(chan struct{}, 1)
	svc.SubscribeScheduleCycle(nil)
	svc.SubscribeScheduleCycle(func(ctx context.Context) error {
		n := cycles.Add(1)
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 15*time.Second {
			return errors.New("cycle lost existing bounded context")
		}
		if n == 2 {
			// Controlled local work-hint stand-in only: no policy proof is
			// created. Actual staged hint/generation transactions are77-owned.
			if err := f.db.SaveFullRefreshDue(ctx, fr.SourceScheduleKey(selected.Request), 0); err != nil {
				return err
			}
		}
		entered <- n
		if n == 1 {
			return errors.New("controlled activation failure")
		}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	svc.SubscribeScheduleCycle(func(context.Context) error {
		followers.Add(1)
		return nil
	})
	first := acquire()
	if cycles.Load() != 0 || f.calls.Load() != 1 {
		t.Fatal("manual entry ran cycle callbacks or duplicated acquisition")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	expectedCycle.Store(1)
	go func() { done <- svc.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
			if cycles.Load() != 3 || followers.Load() != 1 || f.calls.Load() != 11 {
				t.Errorf("cancelled cycle admitted work: cycles%d followers%d calls%d", cycles.Load(), followers.Load(), f.calls.Load())
			}
		case <-time.After(10 * time.Second):
			t.Error("scheduler did not join after cancellation")
		}
	})
	waitEntry := func(want int32) {
		t.Helper()
		select {
		case got := <-entered:
			if got != want {
				t.Fatalf("cycle %d want %d", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("cycle entry missing")
		}
	}
	waitEntry(1)
	expectedCycle.Store(2)
	clock.advance(time.Minute)
	waitEntry(2) // prior failed cycle must have returned before the next starts
	if prematureReads.Load() != 0 {
		t.Fatal("scheduler read inventory due credit before cycle callback")
	}
	if f.calls.Load() != 1 || followers.Load() != 0 {
		t.Fatal("failed/blocked cycle admitted work or ran later callback")
	}
	release <- struct{}{}
	round := f.waitNextRound(first.ID)
	if round.Succeeded != 9 || round.NotDue != 0 || f.calls.Load() != 10 || followers.Load() != 1 {
		t.Fatalf("hook did not precede due snapshot: %+v calls%d followers%d", round, f.calls.Load(), followers.Load())
	}
	second := acquire()
	if second.ID == first.ID || f.calls.Load() != 11 || cycles.Load() != 2 || followers.Load() != 1 {
		t.Fatal("manual refresh cached a result or invoked cycle callback")
	}
	expectedCycle.Store(3)
	clock.advance(time.Minute)
	waitEntry(3)
	cancel() // callback context cancellation must skip Inventory/admission
}

// Observe real SQLite reads without replacing results or introducing a
// production hook. The prior manual completion has settled before a cycle is
// expected, so only scheduler inventory can read ahead of its entry callback.
type cycleReadConnector struct {
	path                        string
	cycles, expected, premature *atomic.Int32
}

func (c *cycleReadConnector) Driver() driver.Driver { return &sqlite.Driver{} }
func (c *cycleReadConnector) Connect(context.Context) (driver.Conn, error) {
	conn, err := c.Driver().Open(c.path)
	if err != nil {
		return nil, err
	}
	return &cycleReadConn{Conn: conn, observer: c}, nil
}

type cycleReadConn struct {
	driver.Conn
	observer *cycleReadConnector
}

func (c *cycleReadConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, q, args)
}
func (c *cycleReadConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	if strings.HasPrefix(q, "SELECT next_due_ms FROM full_refresh_schedule") && c.observer.cycles.Load() < c.observer.expected.Load() {
		c.observer.premature.Add(1)
	}
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, q, args)
}
