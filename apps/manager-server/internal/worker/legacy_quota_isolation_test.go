package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	collectorpkg "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/managementquery"
	mc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/managerconfig"
	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/usage"
)

// This is an external OLD protocol fixture (404 for v8), not a synthetic native
// mode. Native acceptance uses the independently compiled Core in httpapi.
func TestLegacyQuotaIsolationPreservesOldWorkerAndRejectsLaterProbeFailure(t *testing.T) {
	var disabled atomic.Bool
	var mode atomic.Int64
	mode.Store(404)
	var patches atomic.Int64
	firstPatch := make(chan struct{}, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic" {
			t.Error("missing management auth")
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /v8/management/resource-capabilities":
			w.WriteHeader(int(mode.Load()))
		case "GET /v0/management/auth-files":
			fmt.Fprintf(w, `[{"id":"legacy","name":"legacy.json","auth_index":"legacy-index","provider":"codex","disabled":%t}]`, disabled.Load())
		case "PATCH /v0/management/auth-files/status":
			var body struct {
				Disabled bool `json:"disabled"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("invalid mutation")
			}
			disabled.Store(body.Disabled)
			patches.Add(1)
			firstPatch <- struct{}{}
			w.Write([]byte(`{"ok":true}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	cfg := testutil.NewConfig(t)
	db := testutil.NewStore(t, cfg)
	if err := db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: server.URL, ManagementKey: "synthetic"}}); err != nil {
		t.Fatal(err)
	}
	runtime := collectorpkg.RuntimeConfig{CPAUpstreamURL: server.URL, ManagementKey: "synthetic"}
	w := NewRateLimitAutoDisableWorker(db, runtime)
	w.SetLegacyQuotaGuard(&managementquery.LegacyGuard{Budget: managementquery.NewBudget(db), Identity: rc.New(nil, mc.New(cfg, db, nil))})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)
	w.HandleUsageEvents(ctx, runtime, []usage.Event{{Failed: true, FailStatusCode: 429, FailBody: `{"error":{"type":"usage_limit_reached","resets_in_seconds":60}}`, AuthFileSnapshot: "legacy.json", AuthIndex: "legacy-index", Provider: "codex", EventHash: "legacy-live-candidate"}})
	select {
	case <-firstPatch:
	case <-time.After(5 * time.Second):
		t.Fatal("legacy worker did not disable")
	}
	mode.Store(503)
	recovered, err := w.RecoverOwnedCooldown(ctx, "legacy.json", "legacy-index", "codex")
	if err == nil || recovered || !disabled.Load() || patches.Load() != 1 {
		t.Fatal("transient mode probe failure fell back and recovered")
	}
	mode.Store(404)
	recovered, err = w.RecoverOwnedCooldown(ctx, "legacy.json", "legacy-index", "codex")
	if err != nil || !recovered || disabled.Load() || patches.Load() != 2 {
		t.Fatalf("old legacy recovery changed: recovered=%t err=%v patches=%d", recovered, err, patches.Load())
	}
}

// Component queue/barrier proof supplements the actual Core/API test. It does
// not invent a successful v8 capability or replace native acceptance.
func TestLegacyQuotaIsolationRejectsUnverifiedQueuedAndIncomingCandidates(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	cfg := testutil.NewConfig(t)
	db := testutil.NewStore(t, cfg)
	runtime := collectorpkg.RuntimeConfig{CPAUpstreamURL: server.URL, ManagementKey: "synthetic"}
	w := NewRateLimitAutoDisableWorker(db, runtime)
	event := usage.Event{Failed: true, FailStatusCode: 429, FailBody: `{"error":{"type":"usage_limit_reached","resets_in_seconds":60}}`, AuthFileSnapshot: "native-B.json", AuthIndex: "synthetic-index", Provider: "codex", EventHash: "queued-before-mode-change"}
	w.HandleUsageEvents(context.Background(), runtime, []usage.Event{event})
	if len(w.jobs) != 1 {
		t.Fatal("strict legacy candidate was not parsed and queued")
	}
	queued := <-w.jobs
	w.SetLegacyQuotaGuard(&managementquery.LegacyGuard{})
	w.handleCandidate(context.Background(), queued)
	w.HandleUsageEvents(context.Background(), runtime, []usage.Event{event})
	if calls.Load() != 0 || len(w.jobs) != 0 {
		t.Fatal("unknown mode dispatched queued work or admitted new work")
	}
	// A bare429 remains a non-candidate; no capability/probe loop is added.
	event.FailBody = `{"error":{"message":"rate limited"}}`
	w.HandleUsageEvents(context.Background(), runtime, []usage.Event{event})
	if calls.Load() != 0 || len(w.jobs) != 0 {
		t.Fatal("bare429 became an automatic action")
	}
}
