package httpapi

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"testing"
	"time"

	fr "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fullrefresh"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

const webRefreshPath = "/usage-service/full-refresh"

// The production coordinator, authenticated API and temporary SQLite are real.
// Only scheduler time and external Core/BrowserSkill dependencies are controlled.
type webHourlyFixture struct {
	x     *webObservationFixture
	clock *refreshClock
}

func newWebHourlyFixture(t *testing.T) *webHourlyFixture {
	x := newWebObservationFixture(t)
	x.configure(t)
	clock := &refreshClock{now: time.Now(), ticks: make(chan time.Time, 8)}
	app := x.f.server.AppContext()
	app.FullRefreshService = fr.New(x.f.db, app.ResourceCapabilityService, app.OfficialBalanceService, app.CredentialQuotaService, fr.WithBudget(app.ManagementQueryBudget), fr.WithClock(clock))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := app.FullRefreshService.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	return &webHourlyFixture{x: x, clock: clock}
}

func (h *webHourlyFixture) inventory(t *testing.T) fr.Inventory {
	t.Helper()
	rr := testutil.Request(t, h.x.f.handler, "GET", webRefreshPath, "", testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	var inv fr.Inventory
	testutil.DecodeJSON(t, rr, &inv)
	return inv
}

func (h *webHourlyFixture) waitRound(t *testing.T, previous string, running bool) fr.Progress {
	t.Helper()
	until := time.Now().Add(4 * time.Second)
	for {
		inv := h.inventory(t)
		if p := inv.LatestRound; p != nil && p.Purpose == fr.Periodic && p.ID != previous && (running || p.State != "running") {
			return *p
		}
		if time.Now().After(until) {
			t.Fatalf("background web round did not progress: %+v", inv)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (h *webHourlyFixture) run(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.x.f.server.AppContext().FullRefreshService.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(4 * time.Second):
			t.Error("background scheduler did not join")
		}
	})
}

func (h *webHourlyFixture) job(t *testing.T, id string) ob.Job {
	t.Helper()
	rr := testutil.Request(t, h.x.f.handler, "GET", balancePath+"?job="+id, "", testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	var job ob.Job
	testutil.DecodeJSON(t, rr, &job)
	return job
}

func TestOfficialBalanceWebHourlyCancellationPersistsFailure(t *testing.T) {
	t.Run("cleaned", func(t *testing.T) { assertWebHourlyCancellation(t, false) })
	t.Run("cleanup-failed", func(t *testing.T) { assertWebHourlyCancellation(t, true) })
}

func assertWebHourlyCancellation(t *testing.T, cleanupFailed bool) {
	t.Helper()
	h := newWebHourlyFixture(t)
	last := h.x.result(t)["lastSuccess"]
	h.run(t)
	startup := h.waitRound(t, "", false)
	if startup.NotDue != 1 {
		t.Fatal("completed manual web capture was not credited", startup)
	}
	entered := make(chan struct{})
	var once sync.Once
	h.x.readHook.Store(func(ctx context.Context, _ int64) error {
		once.Do(func() { close(entered) })
		<-ctx.Done()
		return ctx.Err()
	})
	h.clock.advance(time.Hour)
	select {
	case <-entered:
	case <-time.After(4 * time.Second):
		t.Fatal("hourly web read did not enter")
	}
	p := h.waitRound(t, startup.ID, true)
	if len(p.Items) != 1 || p.Items[0].JobID == "" {
		t.Fatal("scheduled web job missing", p)
	}
	expectedCode := "WEB_CANCELLED"
	if cleanupFailed {
		h.x.stage.Store("stop-failed")
		expectedCode = "WEB_CLEANUP_FAILED"
	}
	rr := testutil.Request(t, h.x.f.handler, "DELETE", webRefreshPath+"?id="+p.ID, "", testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	h.x.awaitWorker(t, p.Items[0].JobID)
	job := h.job(t, p.Items[0].JobID)
	if job.State != "complete" || job.Status == nil || job.Status.Attempt == nil || job.Status.Attempt.ErrorCode != expectedCode || job.Status.Attempt.QueryStatus == "success" || !job.Status.Stale {
		t.Fatalf("cancelled scheduled web attempt was not durably failed: %+v status=%+v", job, job.Status)
	}
	raw, _ := json.Marshal(job.Status.LastSuccess)
	var saved any
	json.Unmarshal(raw, &saved)
	if !reflect.DeepEqual(last, saved) || job.Status.Attempt.AmountDecimal != "" || job.Status.Attempt.ObservedAtMS != 0 || job.Status.Attempt.WebEvidence != nil {
		t.Fatal("cancellation renewed or lost last success", job.Status)
	}
	h.x.mu.Lock()
	defer h.x.mu.Unlock()
	if !reflect.DeepEqual(h.x.calls[len(h.x.calls)-1], []string{"session", "stop", "synthetic-owned-session"}) {
		t.Fatal("cancellation did not clean its own browser session", h.x.calls)
	}
}

func TestOfficialBalanceWebHourlyTriggerAndClosedBrowser(t *testing.T) {
	h := newWebHourlyFixture(t)
	h.run(t)
	first := h.waitRound(t, "", false)
	if first.Purpose != fr.Periodic || first.Succeeded != 1 || first.State != "complete" || len(first.Items) != 1 || !first.Items[0].ManualDisabled {
		t.Fatal("startup did not acquire the configured disabled web source", first)
	}
	h.clock.advance(59 * time.Minute)
	until := time.Now().Add(3 * time.Second)
	for h.inventory(t).SchedulerCheckedAtMS != h.clock.Now().UnixMilli() {
		if time.Now().After(until) {
			t.Fatal("scheduler did not acknowledge 59m tick")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if h.inventory(t).LatestRound.ID != first.ID {
		t.Fatal("web source refreshed before 60 minutes")
	}
	h.clock.advance(time.Minute)
	hour := h.waitRound(t, first.ID, false)
	if hour.State != "complete" || hour.Succeeded != 1 || hour.Items[0].JobID == first.Items[0].JobID || hour.Items[0].LastSuccess == nil || hour.Items[0].LastSuccess.ObservedAtMS > time.Now().UnixMilli() {
		t.Fatal("hour tick did not produce a new, locally timed capture", hour)
	}
	last := *hour.Items[0].LastSuccess
	h.x.stage.Store("browser-closed")
	h.clock.advance(time.Hour)
	closed := h.waitRound(t, hour.ID, false)
	item := closed.Items[0]
	if closed.State != "partial" || closed.Failed != 1 || closed.Succeeded != 0 || item.Outcome != "unavailable" || item.Attempt == nil || item.Attempt.ErrorCode != "WEB_BROWSER_UNAVAILABLE" || !item.Stale || !reflect.DeepEqual(item.LastSuccess, &last) {
		t.Fatal("closed browser was reported successful or lost durable capture", closed)
	}
	job := h.job(t, item.JobID)
	if job.Status == nil || !job.Status.Stale || !reflect.DeepEqual(job.Status.LastSuccess, &last) || job.Status.Attempt.ObservedAtMS != 0 || job.Status.Attempt.AmountDecimal != "" {
		t.Fatal("closed browser changed the official public status", job)
	}
	// Cache reads cannot refresh the page or renew a failed observation.
	h.x.mu.Lock()
	before := len(h.x.calls)
	h.x.mu.Unlock()
	h.inventory(t)
	h.x.f.status()
	h.x.mu.Lock()
	if len(h.x.calls) != before {
		t.Error("public cache read invoked BrowserSkill")
	}
	h.x.mu.Unlock()
	h.x.stage.Store("")
	h.clock.advance(time.Hour)
	recovered := h.waitRound(t, closed.ID, false)
	if recovered.Succeeded != 1 || recovered.State != "complete" || recovered.Items[0].Stale {
		t.Fatal("next hourly round did not recover", recovered)
	}
	assertWebOwnSessionLifetimes(t, h.x, 3)
}

func (h *webHourlyFixture) addPeer(t *testing.T) fr.SourceRequest {
	t.Helper()
	var snapshot map[string]any
	json.Unmarshal([]byte(testSnapshotJSON), &snapshot)
	rows := snapshot["resources"].([]any)
	var peer map[string]any
	raw, _ := json.Marshal(rows[0])
	json.Unmarshal(raw, &peer)
	peerID := "66666666-6666-4666-8666-666666666666"
	peer["resource_id"] = peerID
	peer["incarnation"] = "77777777-7777-4777-8777-777777777777"
	snapshot["resources"] = append(rows, peer)
	raw, _ = json.Marshal(snapshot)
	h.x.f.coreSnapshot.Store(string(raw))
	body := webConfiguration(0)
	body["web"].(map[string]any)["parsingRule"] = syntheticWebRule
	target := body["targets"].([]any)[0].(map[string]any)
	target["resource_id"] = peerID
	target["incarnation"] = peer["incarnation"]
	rr := testutil.Request(t, h.x.f.handler, "PUT", balancePath, webBody(t, body), testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	var status ob.Status
	testutil.DecodeJSON(t, rr, &status)
	h.x.extraAuthority.Store(status.WebSourceAuthority)
	q := ob.Query{Binding: status.Binding, Version: status.Version}
	return fr.SourceRequest{Kind: fr.Official, Official: &q}
}

func TestOfficialBalanceWebHourlySameSiteSerializesAndJoins(t *testing.T) {
	h := newWebHourlyFixture(t)
	h.addPeer(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	var once sync.Once
	h.x.readHook.Store(func(ctx context.Context, _ int64) error {
		once.Do(func() { close(entered) })
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	h.run(t)
	select {
	case <-entered:
	case <-time.After(4 * time.Second):
		t.Fatal("no scheduled web capture")
	}
	p := h.waitRound(t, "", true)
	until := time.Now().Add(4 * time.Second)
	for len(p.Items) != 2 || p.Items[0].JobID == "" || p.Items[1].JobID == "" {
		if time.Now().After(until) {
			t.Fatal("both same-site scheduled hosts were not admitted", p)
		}
		time.Sleep(5 * time.Millisecond)
		p = h.waitRound(t, "", true)
	}
	// Manual refresh joins a scheduled exact-source host rather than reading the
	// account concurrently. It does not cancel the scheduler's other source.
	rr := testutil.Request(t, h.x.f.handler, "POST", balancePath, `{`+balanceBinding+`,"version":1}`, testutil.AdminKey)
	testutil.RequireStatus(t, rr, 202)
	var joined ob.Job
	testutil.DecodeJSON(t, rr, &joined)
	expectedJob := ""
	for _, item := range p.Items {
		if item.Targets[0].ResourceID == joined.Binding.Targets[0].ResourceID {
			expectedJob = item.JobID
		}
	}
	if expectedJob == "" || joined.ID != expectedJob {
		t.Fatal("manual refresh did not join the scheduled exact-source host", joined)
	}
	unblock()
	complete := h.waitRound(t, "", false)
	if complete.ID != p.ID || complete.Succeeded != 2 || complete.State != "complete" {
		t.Fatal("same-site sources did not finish through the existing coordinator", complete)
	}
	assertWebOwnSessionLifetimes(t, h.x, 2)
}

func TestOfficialBalanceWebHourlyQueuedCancellationLeavesOtherSession(t *testing.T) {
	h := newWebHourlyFixture(t)
	peer := h.addPeer(t)
	rr := testutil.Request(t, h.x.f.handler, "POST", balancePath, webBodyQuery(t, peer.Official), testutil.AdminKey)
	testutil.RequireStatus(t, rr, 202)
	var initial ob.Job
	testutil.DecodeJSON(t, rr, &initial)
	h.x.f.wait(initial.ID)
	h.x.awaitWorker(t, initial.ID)
	last := h.job(t, initial.ID).Status.LastSuccess
	var original fr.SourceRequest
	for _, source := range h.inventory(t).Sources {
		if source.Request.Kind == fr.Official && source.Request.Official.Targets[0].ResourceID != peer.Official.Targets[0].ResourceID {
			original = source.Request
		}
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	h.x.readHook.Store(func(ctx context.Context, _ int64) error {
		once.Do(func() { close(entered) })
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	start := func(source fr.SourceRequest) fr.Progress {
		raw, _ := json.Marshal(map[string]any{"purpose": fr.Manual, "source": source})
		rr := testutil.Request(t, h.x.f.handler, "POST", webRefreshPath, string(raw), testutil.AdminKey)
		testutil.RequireStatus(t, rr, 202)
		var p fr.Progress
		testutil.DecodeJSON(t, rr, &p)
		return p
	}
	first := start(original)
	select {
	case <-entered:
	case <-time.After(4 * time.Second):
		t.Fatal("first source did not own a session")
	}
	queued := start(peer)
	until := time.Now().Add(4 * time.Second)
	for queued.Items[0].JobID == "" {
		if time.Now().After(until) {
			t.Fatal("second source did not enter the bounded host queue")
		}
		rr = testutil.Request(t, h.x.f.handler, "GET", webRefreshPath+"?id="+queued.ID, "", testutil.AdminKey)
		testutil.RequireStatus(t, rr, 200)
		testutil.DecodeJSON(t, rr, &queued)
		time.Sleep(5 * time.Millisecond)
	}
	rr = testutil.Request(t, h.x.f.handler, "DELETE", webRefreshPath+"?id="+queued.ID, "", testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	h.x.awaitWorker(t, queued.Items[0].JobID)
	job := h.job(t, queued.Items[0].JobID)
	if job.State != "complete" || job.Status == nil || job.Status.Attempt.ErrorCode != "WEB_CANCELLED" || !job.Status.Stale || !reflect.DeepEqual(job.Status.LastSuccess, last) {
		t.Fatal("queued cancellation lost its previous capture", job)
	}
	h.x.mu.Lock()
	starts, stops := 0, 0
	for _, call := range h.x.calls {
		if len(call) >= 2 && call[0] == "session" {
			if call[1] == "start" {
				starts++
			} else if call[1] == "stop" {
				stops++
			}
		}
	}
	h.x.mu.Unlock()
	if starts != 2 || stops != 1 {
		t.Fatalf("queued cancellation touched the other task's active session: starts=%d stops=%d", starts, stops)
	}
	unblock()
	until = time.Now().Add(4 * time.Second)
	for {
		rr = testutil.Request(t, h.x.f.handler, "GET", webRefreshPath+"?id="+first.ID, "", testutil.AdminKey)
		testutil.RequireStatus(t, rr, 200)
		testutil.DecodeJSON(t, rr, &first)
		if first.State != "running" {
			break
		}
		if time.Now().After(until) {
			t.Fatal("other task did not finish after its own release")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if first.Succeeded != 1 {
		t.Fatal("queued cancellation cancelled the other source", first)
	}
	assertWebOwnSessionLifetimes(t, h.x, 2)
}

func webBodyQuery(t *testing.T, query *ob.Query) string {
	t.Helper()
	raw, err := json.Marshal(query)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func assertWebOwnSessionLifetimes(t *testing.T, x *webObservationFixture, expected int) {
	t.Helper()
	x.mu.Lock()
	defer x.mu.Unlock()
	active, starts, stops := 0, 0, 0
	for _, call := range x.calls {
		if len(call) < 2 || call[0] != "session" {
			continue
		}
		if call[1] == "start" {
			starts++
			active++
			if active != 1 {
				t.Fatal("same-site sessions overlapped", x.calls)
			}
		} else if call[1] == "stop" {
			stops++
			active--
			if !reflect.DeepEqual(call, []string{"session", "stop", "synthetic-owned-session"}) || active != 0 {
				t.Fatal("stopped a foreign or unowned session", x.calls)
			}
		}
	}
	if starts != expected || stops != expected || active != 0 {
		t.Fatalf("session lifetimes: starts=%d stops=%d active=%d want=%d", starts, stops, active, expected)
	}
}
