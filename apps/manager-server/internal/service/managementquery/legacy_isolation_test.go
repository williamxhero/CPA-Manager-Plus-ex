package managementquery

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	mc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/managerconfig"
	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

// External legacy/negative protocol envelopes only. Actual positive native v8
// isolation is covered through independently verified Core in httpapi tests.
func TestLegacyIsolationRequiresExplicitLegacyAndRetainsGlobalBudget(t *testing.T) {
	var code atomic.Int64
	code.Store(404)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic" {
			t.Error("missing management auth")
		}
		if r.URL.Path != "/v8/management/resource-capabilities" {
			t.Errorf("unexpected mode request %s", r.URL.Path)
		}
		w.WriteHeader(int(code.Load()))
		w.Write([]byte(`{"version":2}`))
	}))
	defer server.Close()
	cfg := testutil.NewConfig(t)
	db := testutil.NewStore(t, cfg)
	if err := db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: server.URL, ManagementKey: "synthetic"}}); err != nil {
		t.Fatal(err)
	}
	guard := &LegacyGuard{Budget: NewBudget(db), Identity: rc.New(nil, mc.New(cfg, db, nil))}
	ctx := context.Background()
	var releases []func()
	defer func() {
		for _, release := range releases {
			release()
		}
	}()
	for i := 0; i < 4; i++ {
		_, release, err := guard.Begin(ctx, server.URL, "synthetic")
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	if _, _, err := guard.Begin(ctx, server.URL, "synthetic"); !errors.Is(err, ErrBusy) {
		t.Fatalf("fifth global query admission=%v", err)
	}
	// Write mode checks are not a new provider-query queue or another4 slots.
	if err := guard.CheckTarget(ctx, server.URL, "synthetic"); err != nil {
		t.Fatal(err)
	}
	for _, status := range []int64{503, 401, 500, 200} {
		code.Store(status)
		if err := guard.CheckTarget(ctx, server.URL, "synthetic"); !errors.Is(err, ErrLegacyModeUnverified) {
			t.Fatalf("HTTP%d became legacy permission: %v", status, err)
		}
	}
	code.Store(503)
	if _, _, err := guard.Begin(ctx); !errors.Is(err, ErrLegacyModeUnverified) {
		t.Fatalf("transient inventory failure fell back to legacy Begin: %v", err)
	}
	code.Store(404)
	for _, release := range releases {
		release()
	}
	releases = nil
	_, release, err := guard.Begin(ctx, server.URL, "synthetic")
	if err != nil {
		t.Fatalf("legacy budget slot leaked: %v", err)
	}
	release()
}
