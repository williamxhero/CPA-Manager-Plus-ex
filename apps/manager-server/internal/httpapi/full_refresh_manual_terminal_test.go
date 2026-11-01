package httpapi

import (
	"context"
	"encoding/json"
	fr "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fullrefresh"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	"sync"
	"testing"
	"time"
)

// The official job GET is the causal completion barrier. No scheduler Run,
// full-refresh progress polling, sleeps, config rotation or service recreation.
func TestOfficialBalanceManualTerminalRoundDoesNotCacheCompletedJob(t *testing.T) {
	f := newOfficialFixture(t, "deepinfra")
	f.configure("")
	// Hold only the public completion consumer, after the actual durable host job
	// is complete. The next API request must not depend on consumer scheduling.
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer close(release)
	f.server.AppContext().FullRefreshService.Subscribe(func(ctx context.Context, event fr.CompletedObservation) {
		once.Do(func() {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
			}
		})
	})
	f.response.Store(`{"stripe_balance":-1,"recent":2.5}`)
	firstID := f.query()
	firstJSON := f.wait(firstID)
	var first ob.Job
	if err := json.Unmarshal([]byte(firstJSON), &first); err != nil {
		t.Fatal(err)
	}
	if first.State != "complete" || first.Status == nil || first.Status.LastSuccess == nil || first.Status.LastSuccess.AmountDecimal != "-1.5" {
		t.Fatalf("first query did not complete: %s", firstJSON)
	}
	<-entered
	f.response.Store(`{"stripe_balance":-0,"recent":0.00}`)
	secondID := f.query()
	if secondID == firstID {
		t.Fatalf("standalone terminal job was cached: first=%s next=%s firstJSON=%s", firstID, secondID, firstJSON)
	}
	var second ob.Job
	result := f.wait(secondID)
	if err := json.Unmarshal([]byte(result), &second); err != nil {
		t.Fatal(err)
	}
	if second.State != "complete" || second.Version != first.Version || second.Status == nil || second.Status.LastSuccess == nil || second.Status.LastSuccess.QueryID != secondID || second.Status.LastSuccess.AmountDecimal != "0" || f.count.Load() != 2 {
		t.Fatalf("same-CAS next query was not fresh: %s calls=%d", result, f.count.Load())
	}
}

func TestOfficialBalanceManualRetryWithoutRetryAfterDoesNotInventBackoff(t *testing.T) {
	f := newOfficialFixture(t, "deepinfra")
	f.configure("")
	f.response.Store(`{"stripe_balance":-1,"recent":2.5}`)
	f.wait(f.query())
	f.response.Store("status:429")
	limitedID := f.query()
	var limited ob.Job
	if err := json.Unmarshal([]byte(f.wait(limitedID)), &limited); err != nil {
		t.Fatal(err)
	}
	if limited.Status == nil || limited.Status.Attempt == nil || limited.Status.Attempt.QueryStatus != "rate_limited" || limited.Status.LastSuccess == nil || limited.Status.LastSuccess.AmountDecimal != "-1.5" {
		t.Fatalf("429 lost real attempt/history: %+v", limited)
	}
	f.response.Store(`{"stripe_balance":-0,"recent":0.00}`)
	nextID := f.query()
	var next ob.Job
	result := f.wait(nextID)
	if err := json.Unmarshal([]byte(result), &next); err != nil {
		t.Fatal(err)
	}
	if nextID == limitedID || next.Version != limited.Version || next.Status == nil || next.Status.LastSuccess == nil || next.Status.LastSuccess.QueryID != nextID || next.Status.LastSuccess.AmountDecimal != "0" || f.count.Load() != 3 {
		t.Fatalf("absent Retry-After fabricated cached success/backoff: %s calls%d", result, f.count.Load())
	}
}

func TestFullRefreshActualCoreCompletedOfficialJoinsOnlyWhileOtherItemsActive(t *testing.T) {
	f := newRefreshFixture(t)
	f.configuredSources(60000) // deliberate held work within the existing hard bound
	f.mode.Store(7)
	code, data := f.api("POST", "/usage-service/full-refresh", map[string]any{"purpose": "periodic"})
	if code != 202 {
		t.Fatalf("start%d %s", code, data)
	}
	var p fr.Progress
	json.Unmarshal(data, &p)
	var finished fr.Item
	until := time.Now().Add(10 * time.Second)
	for {
		_, data = f.api("GET", "/usage-service/full-refresh?id="+p.ID, nil)
		json.Unmarshal(data, &p)
		for _, item := range p.Items {
			if item.Request.Kind == fr.Official && item.Outcome == "success" {
				finished = item
				break
			}
		}
		if finished.JobID != "" {
			break
		}
		if time.Now().After(until) {
			t.Fatal("controlled official source did not complete")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if p.State != "running" || p.Pending == 0 {
		t.Fatalf("other inventory items must still be active: %+v", p)
	}
	code, data = f.api("POST", balancePath, finished.Request.Official)
	var joined ob.Job
	json.Unmarshal(data, &joined)
	if code != 202 || joined.ID != finished.JobID {
		t.Fatalf("completed source lost full-round singleflight%d %s", code, data)
	}
	close(f.release)
	done := f.waitRound(p.ID)
	if done.Succeeded != 9 || done.Failed != 0 {
		t.Fatalf("full round lost actual observations: %+v", done)
	}
	code, data = f.api("POST", balancePath, finished.Request.Official)
	var next ob.Job
	json.Unmarshal(data, &next)
	if code != 202 || next.ID == finished.JobID || next.Version != finished.Request.Official.Version {
		t.Fatalf("drained full round cached old job%d %s", code, data)
	}
	until = time.Now().Add(10 * time.Second)
	for {
		code, data = f.api("GET", balancePath+"?job="+next.ID, nil)
		json.Unmarshal(data, &next)
		if code != 200 {
			t.Fatalf("job%d %s", code, data)
		}
		if next.State == "complete" {
			break
		}
		if time.Now().After(until) {
			t.Fatalf("next job did not complete %s", data)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if next.Status == nil || next.Status.LastSuccess == nil || next.Status.LastSuccess.QueryID != next.ID || f.calls.Load() != 10 || f.peak.Load() > 4 {
		t.Fatalf("not exactly one fresh post-round query: %s calls%d peak%d", data, f.calls.Load(), f.peak.Load())
	}
}

func TestOfficialBalanceSingleSourceEntryDoesNotJoinTerminalHostFlight(t *testing.T) {
	f := newRefreshFixture(t)
	inv := f.configuredSources()
	var source fr.SourceRequest
	for _, src := range inv.Sources {
		if src.Request.Kind == fr.Official {
			source = src.Request
			break
		}
	}
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	var once sync.Once
	f.manager.AppContext().FullRefreshService.Subscribe(func(ctx context.Context, e fr.CompletedObservation) { once.Do(func() { close(entered); <-release }) })
	starts := make(chan string, 4)
	f.manager.AppContext().FullRefreshService.SubscribeStarted(func(ctx context.Context, e fr.StartedObservation) error { starts <- e.AttemptID; return nil })
	wait := func(id string) ob.Job {
		t.Helper()
		until := time.Now().Add(10 * time.Second)
		for {
			code, data := f.api("GET", balancePath+"?job="+id, nil)
			var job ob.Job
			json.Unmarshal(data, &job)
			if code != 200 {
				t.Fatalf("job%d %s", code, data)
			}
			if job.State == "complete" {
				return job
			}
			if time.Now().After(until) {
				t.Fatalf("job did not complete %s", data)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	code, data := f.api("POST", balancePath, source.Official)
	var first ob.Job
	json.Unmarshal(data, &first)
	if code != 202 {
		t.Fatalf("first%d %s", code, data)
	}
	first = wait(first.ID)
	<-entered
	if first.Status == nil || first.Status.LastSuccess == nil {
		t.Fatalf("first success missing %+v", first)
	}
	if started := <-starts; started != first.ID {
		t.Fatalf("start attempt %s != %s", started, first.ID)
	}
	f.mode.Store(2) // next actual provider read returns401, not cached success
	code, data = f.api("POST", "/usage-service/full-refresh", map[string]any{"purpose": "manual", "source": source})
	if code != 202 {
		t.Fatalf("source%d %s", code, data)
	}
	var nextID string
	select {
	case nextID = <-starts:
	case <-time.After(5 * time.Second):
		t.Fatal("new source entry reused a terminal job without a new trusted start")
	}
	if nextID == first.ID {
		t.Fatal("terminal query recaptured as a new start")
	}
	next := wait(nextID)
	if next.Status == nil || next.Status.Attempt == nil || next.Status.Attempt.QueryID != nextID || next.Status.Attempt.QueryStatus != "auth_error" || next.Status.LastSuccess == nil || next.Status.LastSuccess.QueryID != first.ID || f.calls.Load() != 2 {
		t.Fatalf("source entry did not query fresh same-CAS response: %+v calls%d", next, f.calls.Load())
	}
}
