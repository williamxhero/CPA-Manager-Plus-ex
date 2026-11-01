package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	cp "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy"
	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	fr "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fullrefresh"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

// One independent negative slice. Do not include this intentionally RED case in
// the already delivered clean five-case native admission checkpoint.
func TestCredentialPolicyONCannotSaveBeforeQualifyingSame76Query(t *testing.T) {
	f := newNativeAdmissionFixture(t)
	src := f.sources["A"]
	initial := f.status("A")
	if initial.Enabled || f.quotaCalls[0].Load() != 0 || f.quotaCalls[1].Load() != 0 {
		t.Fatal("ON prerequisite probe did not start with unqueried OFF credentials")
	}
	entered, release := make(chan struct{}, 4), make(chan struct{})
	var once sync.Once
	allowResponse := func() { once.Do(func() { close(release) }) }
	t.Cleanup(allowResponse)
	f.quotaBarrier.Store(&nativeAdmissionQuotaResponseBarrier{entered: entered, release: release})
	started := make(chan fr.StartedObservation, 8)
	f.manager.AppContext().FullRefreshService.SubscribeStarted(func(_ context.Context, event fr.StartedObservation) error { started <- event; return nil })
	generation, enabled := initial.Generation, true
	body, err := json.Marshal(cp.Update{Binding: src.Binding, AuthIndex: src.AuthIndex, SourceVersion: src.SourceVersion, ExpectedGeneration: &generation, Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	type reply struct {
		code int
		body []byte
	}
	returned := make(chan reply, 1)
	go func() {
		req := httptest.NewRequest("PUT", "/usage-service/credential-policy", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+testutil.AdminKey)
		rr := httptest.NewRecorder()
		f.manager.Handler().ServeHTTP(rr, req)
		returned <- reply{code: rr.Code, body: bytes.Clone(rr.Body.Bytes())}
	}()
	var result reply
	released := false
	select {
	case result = <-returned:
		// Current production takes this branch with200/enabled=true and ZERO
		// quota queries. Never run a background query first to hide that gap.
	case <-entered:
		// If ON acquires a real76 query, exercise the production lifecycle while
		// its external response is held. No private Consume/coordinator call.
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- f.manager.AppContext().FullRefreshService.Run(ctx) }()
		t.Cleanup(func() {
			allowResponse()
			cancel()
			shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			if err := f.manager.AppContext().FullRefreshService.Shutdown(shutdown); err != nil {
				t.Error(err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Error(err)
				}
			case <-shutdown.Done():
				t.Error("production76 lifecycle did not join")
			}
		})
		blocked := f.status("A")
		if blocked.Enabled {
			t.Fatal("ON persisted enabled=true while actual native quota response was still held")
		}
		allowResponse()
		released = true
		select {
		case result = <-returned:
		case <-time.After(15 * time.Second):
			t.Fatal("ON neither returned explicit unavailability/pending nor awaited actual query successfully")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("ON did not return or start an actual76 quota query")
	}
	var response struct {
		Enabled bool   `json:"enabled"`
		Error   string `json:"error"`
		Reason  string `json:"reason"`
		State   string `json:"state"`
	}
	if json.Unmarshal(result.body, &response) != nil {
		t.Fatalf("ON returned non-JSON HTTP%d", result.code)
	}
	current := f.status("A")
	if current.Enabled || response.Enabled {
		if !released {
			t.Fatalf("ON saved enabled=true before qualifying same76 query: HTTP%d quota[A,B]=[%d,%d] responseHeld=true; no qualifying success was supplied", result.code, f.quotaCalls[0].Load(), f.quotaCalls[1].Load())
		}
		// A future query-awaiting implementation may choose200 or another public
		// success style. Require the real current successful same-source result.
		var status cq.Status
		f.api("GET", "/usage-service/credential-quota?authIndex="+src.AuthIndex, nil, 200, &status)
		if status.Stale || status.Attempt == nil || status.LastSuccess == nil || status.Attempt.QueryStatus != "success" || status.Attempt.QueryID != status.LastSuccess.QueryID || status.SourceVersion != src.SourceVersion {
			t.Fatal("ON enabled without current qualifying native quota result")
		}
		select {
		case event := <-started:
			if event.Source.Credential == nil || event.Source.Credential.AuthIndex != src.AuthIndex || event.AttemptID != status.Attempt.QueryID {
				t.Fatal("ON query did not traverse the original same76 start")
			}
		default:
			t.Fatal("ON query bypassed actual76 lifecycle")
		}
		return
	}
	// No invented exact503/202 contract. Explicit unavailable/pending is valid
	// provided the public read never reports the unqualified intent as enabled.
	if result.code == 401 || result.code == 403 || response.Error == "" && response.Reason == "" && response.State != "pending" && response.State != "unavailable" {
		t.Fatalf("ON did not expose explicit unavailable/pending: HTTP%d %s", result.code, result.body)
	}
}
