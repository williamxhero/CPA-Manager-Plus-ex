package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"
	"testing"
	"time"

	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	fr "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fullrefresh"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

func nativeTerminalSource(t *testing.T, f *refreshFixture) cq.Query {
	t.Helper()
	code, data := f.api("GET", "/usage-service/credential-quota", nil)
	var response struct {
		Sources []cq.Source `json:"sources"`
	}
	if code != 200 || json.Unmarshal(data, &response) != nil {
		t.Fatalf("sources%d %s", code, data)
	}
	for _, s := range response.Sources {
		if s.State == "ready" && s.Protocol == "codex-oauth-wham" {
			return cq.Query{Binding: s.Binding, SourceVersion: s.SourceVersion, AuthIndex: s.AuthIndex}
		}
	}
	t.Fatal("actual native source missing")
	return cq.Query{}
}

func TestCredentialQuotaActualCoreManualTerminalFlightIsNotCached(t *testing.T) {
	f := newRefreshFixture(t)
	app := f.manager.AppContext()
	q := nativeTerminalSource(t, f)
	barrier := &quotaCommitBarrier{committed: make(chan context.Context, 4), release: make(chan struct{}), jobRead: make(chan struct{}, 8)}
	var releaseCommitOnce, releaseObserverOnce sync.Once
	releaseCommit := func() { releaseCommitOnce.Do(func() { close(barrier.release) }) }
	defer releaseCommit()
	db := sql.OpenDB(&quotaCompletionConnector{path: app.Config.DBPath, barrier: barrier})
	t.Cleanup(func() { db.Close() })
	app.CredentialQuotaService.Store = store.New(db)
	consumerEntered, releaseObserver := make(chan struct{}, 1), make(chan struct{})
	fresh := make(chan fr.CompletedObservation, 2)
	defer releaseObserverOnce.Do(func() { close(releaseObserver) })
	var first cq.Job
	app.FullRefreshService.Subscribe(func(_ context.Context, e fr.CompletedObservation) {
		if e.AttemptID == first.ID {
			consumerEntered <- struct{}{}
			<-releaseObserver
		} else {
			fresh <- e
		}
	})
	code, data := f.api("POST", "/usage-service/credential-quota", q)
	if code != 202 || json.Unmarshal(data, &first) != nil {
		t.Fatalf("first%d %s", code, data)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	select {
	case <-barrier.committed:
	case <-ctx.Done():
		t.Fatal("first actual commit missing")
	}
	// The next API owns the coordinator lock before reading its old host job.
	// Commit acknowledgement is still held under the host lock, so the callback
	// cannot retire that manual round before this request examines it.
	q.Version = first.Query.Version
	f.mode.Store(1)
	type reply struct {
		code int
		data []byte
	}
	responses := make(chan reply, 1)
	go func() { c, d := f.api("POST", "/usage-service/credential-quota", q); responses <- reply{c, d} }()
	select {
	case <-barrier.jobRead:
	case <-ctx.Done():
		t.Fatal("next API did not inspect terminal flight")
	}
	releaseCommit()
	var response reply
	select {
	case response = <-responses:
	case <-ctx.Done():
		t.Fatal("next API blocked after commit acknowledgement")
	}
	var second cq.Job
	if response.code != 202 || json.Unmarshal(response.data, &second) != nil || second.ID == first.ID {
		t.Fatalf("terminal native job cached: first=%s HTTP%d %s", first.ID, response.code, response.data)
	}
	select {
	case <-consumerEntered:
	case <-ctx.Done():
		t.Fatal("old completion consumer missing")
	}
	// Initial upstream entry was buffered; the second must genuinely be active.
	for i := 0; i < 2; i++ {
		select {
		case <-f.entered:
		case <-ctx.Done():
			t.Fatal("fresh second upstream missing")
		}
	}
	oldWorker := app.ManagementQueryBudget.Context(first.ID)
	releaseObserverOnce.Do(func() { close(releaseObserver) })
	select {
	case <-oldWorker.Done():
	case <-ctx.Done():
		t.Fatal("old worker did not release its own lease")
	}
	// The old worker must not delete the newer running flight. Joining it must
	// neither create a third query nor recapture a fresh trusted generation.
	code, data = f.api("POST", "/usage-service/credential-quota", q)
	var joined cq.Job
	if code != 202 || json.Unmarshal(data, &joined) != nil || joined.ID != second.ID || f.nativeCalls.Load() != 2 {
		t.Fatalf("old cleanup erased new flight: HTTP%d %s calls%d", code, data, f.nativeCalls.Load())
	}
	stale := q
	stale.Version = first.RequestedVersion
	code, data = f.api("POST", "/usage-service/credential-quota", stale)
	if code != 409 {
		t.Fatalf("native terminal/running CAS was erased: HTTP%d %s", code, data)
	}
	close(f.release)
	select {
	case e := <-fresh:
		if e.AttemptID != second.ID || e.Credential == nil || e.Credential.Attempt == nil || e.Credential.Attempt.QueryStatus != "success" {
			t.Fatalf("fresh second completion missing: %+v", e)
		}
	case <-ctx.Done():
		t.Fatal("second query did not complete")
	}
	if err := app.ManagementQueryBudget.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if f.peak.Load() > 4 || f.nativeCalls.Load() != 2 {
		t.Fatalf("duplicate query or shared budget violated calls%d peak%d", f.nativeCalls.Load(), f.peak.Load())
	}
}

func TestCredentialQuotaActualCoreSingleSourceDoesNotJoinTerminalFlight(t *testing.T) {
	f := newRefreshFixture(t)
	q := nativeTerminalSource(t, f)
	app := f.manager.AppContext()
	entered, release := make(chan struct{}, 1), make(chan struct{})
	defer close(release)
	var first cq.Job
	fresh := make(chan fr.CompletedObservation, 2)
	app.FullRefreshService.Subscribe(func(_ context.Context, e fr.CompletedObservation) {
		if e.AttemptID == first.ID {
			entered <- struct{}{}
			<-release
		} else {
			fresh <- e
		}
	})
	starts := make(chan string, 4)
	app.FullRefreshService.SubscribeStarted(func(_ context.Context, e fr.StartedObservation) error {
		if e.Source.Kind == fr.Credential {
			starts <- e.AttemptID
		}
		return nil
	})
	// Reuse the real commit barrier so the first ID is published to the test
	// before the held consumer examines it, without a data race or timing sleep.
	barrier := &quotaCommitBarrier{committed: make(chan context.Context, 4), release: make(chan struct{}), jobRead: make(chan struct{}, 8), jobReadRelease: make(chan struct{})}
	var once sync.Once
	releaseCommit := func() { once.Do(func() { close(barrier.release) }) }
	defer releaseCommit()
	var readOnce sync.Once
	releaseRead := func() { readOnce.Do(func() { close(barrier.jobReadRelease) }) }
	defer releaseRead()
	db := sql.OpenDB(&quotaCompletionConnector{path: app.Config.DBPath, barrier: barrier})
	t.Cleanup(func() { db.Close() })
	app.CredentialQuotaService.Store = store.New(db)
	f.mode.Store(1)
	code, data := f.api("POST", "/usage-service/credential-quota", q)
	if code != 202 || json.Unmarshal(data, &first) != nil {
		t.Fatalf("first%d %s", code, data)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	q.Version = first.Query.Version
	type reply struct {
		code int
		data []byte
	}
	responses := make(chan reply, 1)
	go func() {
		c, d := f.api("POST", "/usage-service/full-refresh", map[string]any{"purpose": "recovery", "source": fr.SourceRequest{Kind: fr.Credential, Credential: &q}})
		responses <- reply{c, d}
	}()
	// Inventory is finished and the new source request owns the coordinator
	// lock before its host Job read. Hold that read until the old job commits.
	select {
	case <-barrier.jobRead:
	case <-ctx.Done():
		t.Fatal("source entry did not reach host Job read")
	}
	f.mode.Store(0)
	close(f.release)
	select {
	case <-barrier.committed:
	case <-ctx.Done():
		t.Fatal("first actual commit missing")
	}
	f.mode.Store(2)
	releaseCommit()
	releaseRead()
	select {
	case response := <-responses:
		if response.code != 202 {
			t.Fatalf("single-source%d %s", response.code, response.data)
		}
	case <-ctx.Done():
		t.Fatal("source entry did not return")
	}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("first completion missing")
	}
	if id := <-starts; id != first.ID {
		t.Fatalf("wrong original start %s", id)
	}
	var nextID string
	select {
	case nextID = <-starts:
	case <-ctx.Done():
		t.Fatal("native single-source reused completed host job without new trusted start")
	}
	if nextID == first.ID {
		t.Fatal("old native start recaptured")
	}
	select {
	case e := <-fresh:
		if e.AttemptID != nextID || e.Credential == nil || e.Credential.Attempt == nil || e.Credential.Attempt.QueryStatus != "auth_error" || e.Credential.LastSuccess == nil || e.Credential.LastSuccess.QueryID != first.ID || e.Credential.Version != q.Version || f.nativeCalls.Load() != 2 {
			t.Fatalf("single-source not fresh/retained: %+v calls%d", e, f.nativeCalls.Load())
		}
	case <-ctx.Done():
		t.Fatal("fresh native completion missing")
	}
	code, data = f.api("GET", "/usage-service/credential-quota/jobs/"+nextID, nil)
	var job cq.Job
	if code != 200 || json.Unmarshal(data, &job) != nil || job.State != "complete" || job.Status == nil || job.Status.Attempt == nil || job.Status.Attempt.QueryStatus != "auth_error" {
		t.Fatalf("fresh public job%d %s", code, data)
	}
}
