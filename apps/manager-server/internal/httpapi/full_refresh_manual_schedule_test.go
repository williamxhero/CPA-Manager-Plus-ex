package httpapi

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	fr "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fullrefresh"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"modernc.org/sqlite"
)

// A real manual observation already covers this exact current source. Startup
// must query the rest of the inventory, not acquire the same source again. It
// still permits a deliberate fresh manual request and queries everything at60m.
func TestFullRefreshActualCoreManualCompletionCoversStartupCycle(t *testing.T) {
	f := newRefreshFixture(t)
	inv := f.configuredSources()
	app := f.manager.AppContext()
	clock := &refreshClock{now: time.Now(), ticks: make(chan time.Time, 8)}
	app.FullRefreshService = fr.New(f.db, app.ResourceCapabilityService, app.OfficialBalanceService, app.CredentialQuotaService, fr.WithBudget(app.ManagementQueryBudget), fr.WithClock(clock))
	events := make(chan fr.CompletedObservation, 32)
	app.FullRefreshService.Subscribe(func(_ context.Context, e fr.CompletedObservation) { events <- e })
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
	acquire := func() ob.Job {
		t.Helper()
		code, data := f.api("POST", balancePath, selected.Request.Official)
		var job ob.Job
		if code != 202 || json.Unmarshal(data, &job) != nil {
			t.Fatalf("manual%d %s", code, data)
		}
		deadline := time.After(5 * time.Second)
		for {
			select {
			case e := <-events:
				if e.AttemptID == job.ID {
					if e.Official == nil || e.Official.Attempt == nil || e.Official.Attempt.QueryStatus != "success" {
						t.Fatal("actual manual query failed")
					}
					return job
				}
			case <-deadline:
				t.Fatal("actual manual completion missing")
			}
		}
	}
	first := acquire()
	if f.calls.Load() != 1 {
		t.Fatal("unexpected initial query")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- app.FullRefreshService.Run(ctx) }()
	startup := f.waitNextRound(first.ID)
	for _, item := range startup.Items {
		if item.ID == selected.ID && (item.Outcome != "not_due" || item.Queried) {
			t.Fatalf("completed manual source queried again at startup: %+v", item)
		}
	}
	if startup.NotDue != 1 || startup.Succeeded != 8 || f.calls.Load() != 9 {
		t.Fatalf("startup coverage duplicated/lost source: %+v calls%d", startup, f.calls.Load())
	}
	second := acquire()
	if second.ID == first.ID || f.calls.Load() != 10 {
		t.Fatal("due credit cached an explicit fresh manual request")
	}
	clock.advance(59 * time.Minute)
	deadline := time.Now().Add(5 * time.Second)
	for {
		code, data := f.api("GET", "/usage-service/full-refresh", nil)
		var current fr.Inventory
		if code == 200 && json.Unmarshal(data, &current) == nil && current.SchedulerCheckedAtMS == clock.Now().UnixMilli() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("59minute scheduler acknowledgement missing")
		}
	}
	if f.calls.Load() != 10 {
		t.Fatal("refresh ran before60minutes")
	}
	clock.advance(time.Minute)
	hour := f.waitNextRound(second.ID)
	if hour.Succeeded != 9 || hour.NotDue != 0 || f.calls.Load() != 19 {
		t.Fatalf("manual credit lost exact hourly coverage: %+v calls%d", hour, f.calls.Load())
	}
	for _, item := range hour.Items {
		if item.LastSuccess != nil && item.LastSuccess.ObservedAtMS > time.Now().Add(time.Minute).UnixMilli() {
			t.Fatal("scheduler clock forged observation freshness")
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler lifecycle did not join")
	}
}

// The periodic inventory really reads an old zero due value, then a real manual
// query completes before that inventory is queued. Admission must recheck the
// durable due credit instead of turning the old snapshot into a duplicate read.
func TestFullRefreshActualCoreQueuedInventoryRechecksManualCompletion(t *testing.T) {
	queuedInventoryManualCompletion(t, false)
}

func TestFullRefreshActualCoreExplicitRecoveryOverridesQueuedDueCredit(t *testing.T) {
	queuedInventoryManualCompletion(t, true)
}

func queuedInventoryManualCompletion(t *testing.T, explicitRecovery bool) {
	t.Helper()
	f := newRefreshFixture(t)
	inv := f.configuredSources()
	var selected fr.SourceStatus
	for _, source := range inv.Sources {
		if source.Request.Kind == fr.Official {
			selected = source
			break
		}
	}
	if selected.ID == "" {
		t.Fatal("actual source missing")
	}
	app := f.manager.AppContext()
	barrier := &scheduleDueBarrier{entered: make(chan struct{}, 1), release: make(chan struct{})}
	barrier.armed.Store(true)
	var once sync.Once
	release := func() { once.Do(func() { close(barrier.release) }) }
	defer release()
	db := sql.OpenDB(&scheduleDueConnector{path: app.Config.DBPath, barrier: barrier})
	t.Cleanup(func() { db.Close() })
	app.FullRefreshService = fr.New(store.New(db), app.ResourceCapabilityService, app.OfficialBalanceService, app.CredentialQuotaService, fr.WithBudget(app.ManagementQueryBudget))
	events := make(chan fr.CompletedObservation, 32)
	app.FullRefreshService.Subscribe(func(_ context.Context, e fr.CompletedObservation) { events <- e })
	type reply struct {
		code int
		data []byte
	}
	responses := make(chan reply, 1)
	go func() {
		code, data := f.api("POST", "/usage-service/full-refresh", map[string]any{"purpose": "periodic"})
		responses <- reply{code, data}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	select {
	case <-barrier.entered:
	case <-ctx.Done():
		t.Fatal("old source due read missing")
	}
	code, data := f.api("POST", balancePath, selected.Request.Official)
	var job ob.Job
	if code != 202 || json.Unmarshal(data, &job) != nil {
		t.Fatalf("manual%d %s", code, data)
	}
	for {
		select {
		case e := <-events:
			if e.AttemptID == job.ID {
				if e.Official == nil || e.Official.Attempt == nil || e.Official.Attempt.QueryStatus != "success" {
					t.Fatal("actual manual attempt failed")
				}
				goto completed
			}
		case <-ctx.Done():
			t.Fatal("manual completion missing")
		}
	}
completed:
	if explicitRecovery {
		f.mode.Store(1)
	}
	release()
	var response reply
	select {
	case response = <-responses:
	case <-ctx.Done():
		t.Fatal("periodic inventory did not return")
	}
	var round fr.Progress
	if response.code != 202 || json.Unmarshal(response.data, &round) != nil || round.Purpose != fr.Periodic {
		t.Fatalf("round%d %s", response.code, response.data)
	}
	if explicitRecovery {
		for i := 0; i < 2; i++ {
			select {
			case <-f.entered:
			case <-ctx.Done():
				t.Fatal("periodic admission barrier missing")
			}
		}
		code, data = f.api("POST", "/usage-service/full-refresh", map[string]any{"purpose": "recovery"})
		var joined fr.Progress
		if code != 202 || json.Unmarshal(data, &joined) != nil || joined.ID != round.ID {
			t.Fatalf("explicit recovery lost whole-round singleflight%d %s", code, data)
		}
		close(f.release)
	}
	done := f.waitRound(round.ID)
	if explicitRecovery {
		for _, item := range done.Items {
			if item.ID == selected.ID && (item.Outcome != "success" || !item.Queried || item.JobID == job.ID || item.Reason == "source_queried_since_inventory") {
				t.Fatalf("due credit suppressed explicit fresh recovery: %+v", item)
			}
		}
		if done.NotDue != 0 || done.Succeeded != 9 || f.calls.Load() != 10 || f.peak.Load() > 4 {
			t.Fatalf("explicit recovery omitted/duplicated actual query: %+v calls%d peak%d", done, f.calls.Load(), f.peak.Load())
		}
		return
	}
	for _, item := range done.Items {
		if item.ID == selected.ID && (item.Outcome != "not_due" || item.Queried || item.JobID != "") {
			t.Fatalf("old queued inventory re-acquired completed manual source: %+v", item)
		}
	}
	if done.NotDue != 1 || done.Succeeded != 8 || f.calls.Load() != 9 || f.peak.Load() > 4 {
		t.Fatalf("queued admission lost/duplicated current source: %+v calls%d peak%d", done, f.calls.Load(), f.peak.Load())
	}
}

type scheduleDueBarrier struct {
	armed   atomic.Bool
	entered chan struct{}
	release chan struct{}
}
type scheduleDueConnector struct {
	path    string
	barrier *scheduleDueBarrier
}

func (c *scheduleDueConnector) Driver() driver.Driver { return &sqlite.Driver{} }
func (c *scheduleDueConnector) Connect(context.Context) (driver.Conn, error) {
	conn, err := c.Driver().Open(c.path)
	if err != nil {
		return nil, err
	}
	return &scheduleDueConn{Conn: conn, barrier: c.barrier}, nil
}

type scheduleDueConn struct {
	driver.Conn
	barrier *scheduleDueBarrier
}

func (c *scheduleDueConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, q, args)
}
func (c *scheduleDueConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	rows, err := c.Conn.(driver.QueryerContext).QueryContext(ctx, q, args)
	if err == nil && strings.HasPrefix(q, "SELECT next_due_ms FROM full_refresh_schedule") && len(args) == 1 {
		if key, ok := args[0].Value.(string); ok && strings.HasPrefix(key, "source:") && c.barrier.armed.CompareAndSwap(true, false) {
			return &scheduleDueRows{Rows: rows, ctx: ctx, barrier: c.barrier}, nil
		}
	}
	return rows, err
}

type scheduleDueRows struct {
	driver.Rows
	ctx     context.Context
	barrier *scheduleDueBarrier
	closed  bool
}

func (r *scheduleDueRows) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	return r.Rows.Close()
}
func (r *scheduleDueRows) Next(values []driver.Value) error {
	err := r.Rows.Next(values)
	// Preserve the actual read result while freeing SQLite's reader before the
	// real manual completion writes its credit. No row or success is invented.
	if closeErr := r.Close(); closeErr != nil {
		return closeErr
	}
	r.barrier.entered <- struct{}{}
	select {
	case <-r.barrier.release:
		return err
	case <-r.ctx.Done():
		return r.ctx.Err()
	}
}
