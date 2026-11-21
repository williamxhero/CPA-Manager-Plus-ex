package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	fr "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fullrefresh"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

const resetModel = "gpt-5.3-codex-spark"

func (f *refreshFixture) serveResetQuota(w http.ResponseWriter, r *http.Request) {
	slot := r.Header.Get("Chatgpt-Account-Id")
	switch {
	case slot == "reset-a" && r.Header.Get("Authorization") == "Bearer RESET-A-CANARY":
		f.resetSourceCalls.Add(1)
		switch f.resetMode.Load() {
		case 2:
			w.WriteHeader(http.StatusBadGateway)
			return
		case 3:
			select {
			case <-f.release:
			case <-r.Context().Done():
				return
			}
		}
	case slot == "reset-b" && r.Header.Get("Authorization") == "Bearer RESET-B-CANARY":
	default:
		f.t.Error("quota query used the wrong selected account")
		w.WriteHeader(http.StatusForbidden)
		return
	}
	exp := time.Now().Add(time.Hour).Unix()
	if slot == "reset-a" && f.resetMode.Load() == 1 {
		fmt.Fprintf(w, `{"rate_limit":{"allowed":true,"limit_reached":false,"primary_window":{"used_percent":25,"reset_at":%d,"limit_window_seconds":18000},"secondary_window":{"used_percent":30,"reset_at":%d,"limit_window_seconds":604800}},"additional_rate_limits":[{"limit_name":"GPT-5.3-Codex-Spark","metered_feature":"codex_bengalfox","rate_limit":{"allowed":false,"limit_reached":true,"primary_window":{"used_percent":5,"reset_at":%d,"limit_window_seconds":18000},"secondary_window":{"used_percent":100,"reset_at":%d,"limit_window_seconds":604800}}}]}`, exp, exp, exp, f.resetAt.Load())
		return
	}
	fmt.Fprintf(w, `{"rate_limit":{"allowed":true,"limit_reached":false,"primary_window":{"used_percent":25,"reset_at":%d,"limit_window_seconds":18000},"secondary_window":{"used_percent":30,"reset_at":%d,"limit_window_seconds":604800}},"additional_rate_limits":[{"limit_name":"GPT-5.3-Codex-Spark","metered_feature":"codex_bengalfox","rate_limit":{"allowed":true,"limit_reached":false,"primary_window":{"used_percent":5,"reset_at":%d,"limit_window_seconds":18000},"secondary_window":{"used_percent":10,"reset_at":%d,"limit_window_seconds":604800}}}]}`, exp, exp, exp, exp)
}

func (f *refreshFixture) serveResetInference(w http.ResponseWriter, r *http.Request) {
	switch r.Header.Get("Authorization") {
	case "Bearer RESET-A-CANARY":
		f.resetInferenceA.Add(1)
		if f.resetMode.Load() == 1 {
			reset := f.resetAt.Load()
			w.Header().Set("X-Codex-Active-Limit", "codex_bengalfox")
			w.Header().Set("X-Codex-Bengalfox-Limit-Name", "GPT-5.3-Codex-Spark")
			w.Header().Set("X-Codex-Bengalfox-Secondary-Used-Percent", "100")
			w.Header().Set("X-Codex-Bengalfox-Secondary-Window-Minutes", "10080")
			w.Header().Set("X-Codex-Bengalfox-Secondary-Reset-At", fmt.Sprint(reset))
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprintf(w, `{"error":{"type":"usage_limit_reached","limit_window_minutes":10080,"resets_at":%d}}`, reset)
			return
		}
	case "Bearer RESET-B-CANARY":
		f.resetInferenceB.Add(1)
	default:
		f.t.Error("unexpected native inference source")
		w.WriteHeader(http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"object\":\"response\",\"model\":%q,\"status\":\"completed\",\"output\":[]}}\n\n", resetModel)
}

func (f *refreshFixture) inferResetModel() {
	f.t.Helper()
	req, err := http.NewRequest(http.MethodPost, f.coreBase+"/v1/responses", strings.NewReader(`{"model":"`+resetModel+`","input":[]}`))
	if err != nil {
		f.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		f.t.Fatalf("inference HTTP %d: %s", resp.StatusCode, raw)
	}
}

func (f *refreshFixture) confirmedResetGate() bool {
	f.t.Helper()
	req, _ := http.NewRequest(http.MethodGet, f.coreBase+"/v8/management/inference-gates", nil)
	req.Header.Set("Authorization", "Bearer test-password")
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Gates []struct {
			Model         string `json:"model"`
			Applicability string `json:"applicability"`
			Quota         *struct {
				RecheckAtMS int64 `json:"recheckAtMs"`
			} `json:"quota"`
		} `json:"gates"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&out) != nil {
		f.t.Fatalf("native gate read HTTP %d", resp.StatusCode)
	}
	for _, gate := range out.Gates {
		if gate.Model == resetModel && gate.Applicability == "current" && gate.Quota != nil && gate.Quota.RecheckAtMS == f.resetAt.Load()*1000 {
			return true
		}
	}
	return false
}

func (f *refreshFixture) restartManager(t *testing.T) {
	t.Helper()
	config := f.manager.AppContext().Config
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	protector, err := security.NewProtector([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(config.DBPath, protector)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	f.db = db
	f.manager = New(config, db, collector.NewManager(config, db))
	server := httptest.NewServer(f.manager.Handler())
	t.Cleanup(server.Close)
	client := &http.Client{Timeout: 10 * time.Second}
	f.api = func(method, path string, body any) (int, []byte) {
		t.Helper()
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, server.URL+path, bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+testutil.AdminKey)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		out, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, out
	}
}

func TestFullRefreshActualCoreSchedulesConfirmedResetBeforeHourlyRound(t *testing.T) {
	testFullRefreshConfirmedReset(t, false)
}

func TestFullRefreshActualCoreFailedResetStaysPausedAcrossManagerRestart(t *testing.T) {
	testFullRefreshConfirmedReset(t, true)
}

func testFullRefreshConfirmedReset(t *testing.T, fail bool) {
	t.Helper()
	f := newRefreshFixture(t, refreshFixtureScale{providers: 1, scopedReset: true})
	clock := &refreshClock{now: time.Now(), ticks: make(chan time.Time, 8)}
	run := func() func() {
		app := f.manager.AppContext()
		service := fr.New(f.db, app.ResourceCapabilityService, app.OfficialBalanceService, app.CredentialQuotaService, fr.WithBudget(app.ManagementQueryBudget), fr.WithClock(clock))
		app.FullRefreshService = service
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- service.Run(ctx) }()
		stopped := false
		stop := func() {
			if stopped {
				return
			}
			stopped = true
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Error(err)
				}
			case <-time.After(10 * time.Second):
				t.Error("scheduler failed to stop")
			}
		}
		t.Cleanup(stop)
		return stop
	}
	stop := run()
	hourly := f.waitLatestRound()
	if hourly.Purpose != fr.Periodic || f.resetSourceCalls.Load() != 1 {
		t.Fatal("startup did not acquire the selected quota source")
	}
	reset := time.Now().Add(11 * time.Second).Truncate(time.Second)
	f.resetAt.Store(reset.Unix())
	f.resetMode.Store(1)
	f.inferResetModel()
	if a, b, gate := f.resetInferenceA.Load(), f.resetInferenceB.Load(), f.confirmedResetGate(); a != 1 || b != 1 || !gate {
		t.Fatalf("test did not establish an effective native gate and dispatch to fallback: A=%d B=%d gate=%v", a, b, gate)
	}
	// The upstream rejection can change the source's runtime scheduling key
	// when Core saves config. Let ordinary catch-up credit the *current* binding
	// before the reset, while WHAM still reports that same scoped window empty.
	clock.advance(time.Since(clock.Now()))
	f.waitSchedulerChecked(clock.Now().UnixMilli())
	f.waitNextRound(hourly.ID)
	beforeReset := f.waitLatestRound()
	initialSource := f.resetSourceCalls.Load()
	if initialSource != 2 || !f.confirmedResetGate() {
		t.Fatalf("catch-up did not acquire the current binding while the window remains empty: queries=%d", initialSource)
	}
	if fail {
		f.resetMode.Store(2)
	} else {
		f.resetMode.Store(3)
	}
	initialTotal := f.calls.Load()
	clock.advance(reset.Add(-time.Second).Sub(clock.Now()))
	f.waitSchedulerChecked(clock.Now().UnixMilli())
	if f.resetSourceCalls.Load() != initialSource || f.calls.Load() != initialTotal || !f.confirmedResetGate() {
		t.Fatal("reset was queried or gate was cleared too early")
	}
	// Query acquisition and WHAM evidence retain wall time, independent of the
	// injected scheduler ticks. Cross the actual deadline before advancing.
	time.Sleep(time.Until(reset.Add(100 * time.Millisecond)))
	clock.advance(time.Since(clock.Now()))
	f.waitSchedulerChecked(clock.Now().UnixMilli())
	limit := time.Now().Add(4 * time.Second)
	for f.resetSourceCalls.Load() == initialSource && time.Now().Before(limit) {
		time.Sleep(30 * time.Millisecond)
	}
	if f.resetSourceCalls.Load() != initialSource+1 || f.calls.Load() != initialTotal+1 {
		t.Fatalf("due reset did not acquire exactly one fresh selected query before the hourly round: queries=%d", f.resetSourceCalls.Load())
	}
	if !fail {
		// The fresh native query is held at the upstream while a public manual
		// refresh asks for the same source. It should get that same running job.
		code, data := f.api("GET", "/usage-service/full-refresh", nil)
		var inv fr.Inventory
		if code != 200 || json.Unmarshal(data, &inv) != nil || inv.LatestRound == nil ||
			inv.LatestRound.Purpose != fr.Recovery || len(inv.LatestRound.Items) != 1 {
			t.Fatalf("selected recovery round unavailable: %d %s", code, data)
		}
		picked := inv.LatestRound.Items[0]
		if picked.Request.Credential == nil || picked.JobID == "" || picked.Outcome != "running" {
			t.Fatalf("selected recovery query not running: %+v", picked)
		}
		code, data = f.api("POST", "/usage-service/credential-quota", picked.Request.Credential)
		var job struct{ ID string }
		if code != 202 || json.Unmarshal(data, &job) != nil || job.ID != picked.JobID ||
			f.resetSourceCalls.Load() != initialSource+1 || f.calls.Load() != initialTotal+1 {
			t.Fatalf("manual refresh did not join the running reset acquisition: %d %s", code, data)
		}
		close(f.release)
	}
	recheck := f.waitNextRound(beforeReset.ID)
	if recheck.Purpose != fr.Recovery || recheck.PeriodicNextDueMS != 0 || len(recheck.Items) != 1 ||
		recheck.Items[0].NextDueMS != hourly.PeriodicNextDueMS {
		t.Fatalf("reset did not target just one source while retaining its hourly deadline: %+v", recheck)
	}
	if fail {
		item := recheck.Items[0]
		if !f.confirmedResetGate() || item.Attempt == nil || item.Attempt.ErrorCode == "" ||
			item.LastSuccess == nil || item.LastSuccess.QueryID == item.Attempt.QueryID || !item.Stale {
			t.Fatal("failed query changed admission or did not retain prior success and diagnosis")
		}
		stop()
		f.restartManager(t)
		run()
		clock.advance(time.Minute)
		f.waitSchedulerChecked(clock.Now().UnixMilli())
		code, data := f.api("GET", "/usage-service/full-refresh", nil)
		var inv fr.Inventory
		if code != http.StatusOK || json.Unmarshal(data, &inv) != nil || inv.LatestRound == nil {
			t.Fatalf("latest round missing after restart: %d %s", code, data)
		}
		if inv.LatestRound.ID != recheck.ID {
			t.Fatalf("restart enqueued a new round: got %s, want %s", inv.LatestRound.ID, recheck.ID)
		}
		if f.resetSourceCalls.Load() != initialSource+1 || f.calls.Load() != initialTotal+1 || !f.confirmedResetGate() {
			t.Fatal("restart repeated a terminal reset attempt or cleared a confirmed gate")
		}
		f.inferResetModel()
		if f.resetInferenceA.Load() != 1 || f.resetInferenceB.Load() != 2 {
			t.Fatal("failed evidence did not preserve working fallback")
		}
		return
	}
	if f.confirmedResetGate() {
		t.Fatal("fresh evidence did not clear the confirmed gate")
	}
	f.inferResetModel()
	if f.resetInferenceA.Load() != 2 || f.resetInferenceB.Load() != 1 {
		t.Fatal("successful fresh evidence did not restore the preferred source")
	}
}
