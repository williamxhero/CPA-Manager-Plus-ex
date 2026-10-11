package worker

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	collectorpkg "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

// A provider timestamp schedules a recheck, not immediate recovery. The grace
// gate must also apply to cooldowns persisted before the worker restarted.
func TestProviderResetWaitsForGraceBeforeQuotaProbeAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.sqlite")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Millisecond)
	seeded := seedProviderCooldown(t, st, now.Add(time.Minute))
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	probeCalls := 0
	server, patchCalls, _ := failClosedRecoveryServer(t, func(w http.ResponseWriter, r *http.Request) {
		probeCalls++
		authoritativeRecoveredQuota(w, "weekly")
	})
	defer server.Close()
	worker := NewRateLimitAutoDisableWorker(st, collectorpkg.RuntimeConfig{CPAUpstreamURL: server.URL, ManagementKey: "mgmt"})
	ctx := context.Background()

	worker.enableDue(ctx, now)
	if probeCalls != 0 || *patchCalls != 0 {
		t.Fatalf("at reset: probes=%d enables=%d, want no action before grace", probeCalls, *patchCalls)
	}
	active, err := st.QuotaCooldowns.ListActive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].RecoverAtMS != seeded.RecoverAtMS || active[0].NextCheckAtMS != now.Add(30*time.Second).UnixMilli() {
		t.Fatalf("grace must preserve provider ETA and persist check at reset+30s: %#v", active)
	}

	worker.enableDue(ctx, now.Add(30*time.Second-time.Millisecond))
	if probeCalls != 0 || *patchCalls != 0 {
		t.Fatal("action occurred before grace boundary")
	}
	worker.enableDue(ctx, now.Add(30*time.Second))
	worker.enableDue(ctx, now.Add(time.Minute))
	if probeCalls != 1 || *patchCalls != 1 {
		t.Fatalf("after grace: probes=%d enables=%d, want one confirmed recovery", probeCalls, *patchCalls)
	}
}
