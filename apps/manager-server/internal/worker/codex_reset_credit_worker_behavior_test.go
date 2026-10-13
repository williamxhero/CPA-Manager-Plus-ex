package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	collectorpkg "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

func TestCodexResetCreditAutoConsumeDecision(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000)
	count := int64(1)
	if !shouldAutoConsumeResetCredit(`{"reset_at":"1799999999000"}`, &count, now) {
		t.Fatal("due reset credit was not eligible for automatic consumption")
	}
	if shouldAutoConsumeResetCredit(`{"reset_at":"1800000001000"}`, &count, now) {
		t.Fatal("future reset credit was eligible for automatic consumption")
	}
	if shouldAutoConsumeResetCredit(`{"available_count":1}`, &count, now) {
		t.Fatal("reset credit without authoritative reset time was eligible")
	}
	if shouldAutoConsumeResetCredit(`{"reset_at":"1799999999000"}`, nil, now) {
		t.Fatal("reset credit without available count was eligible")
	}
}

func TestCodexResetCreditFetchFailsClosedWithoutCycle(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status_code":200,"body":{"rate_limit_reset_credits":{"available_count":1}}}`))
	}))
	defer server.Close()
	worker := NewCodexResetCreditWorker(st)
	_, err = worker.Fetch(context.Background(), collectorpkg.RuntimeConfig{CPAUpstreamURL: server.URL, ManagementKey: "test"}, CodexCredential{CredentialKey: "credential-a"})
	if err == nil {
		t.Fatal("Fetch unexpectedly accepted reset credits without a cycle")
	}
}

func TestCodexResetCreditConsumeRefreshesAroundConsume(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var mu sync.Mutex
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		var response any = map[string]any{"rate_limit_reset_credits": map[string]any{"available_count": 1, "cycle_id": "cycle-a"}}
		if r.URL.Path == "/v0/management/api-call" {
			mu.Lock()
			paths = append(paths, body["url"].(string))
			index := len(paths)
			mu.Unlock()
			if index == 3 {
				response = map[string]any{"rate_limit_reset_credits": map[string]any{"available_count": 0, "cycle_id": "cycle-a"}}
			}
		}
		encoded, _ := json.Marshal(map[string]any{"status_code": 200, "body": response})
		_, _ = w.Write(encoded)
	}))
	defer server.Close()
	worker := NewCodexResetCreditWorker(st)
	cfg := collectorpkg.RuntimeConfig{CPAUpstreamURL: server.URL, ManagementKey: "test"}
	_, err = worker.Consume(context.Background(), cfg, CodexCredential{CredentialKey: "credential-a"})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 5 || paths[0] != codexUsageURL || paths[1] != codexResetCreditsURL || paths[2] != codexResetCreditsConsumeURL || paths[3] != codexUsageURL || paths[4] != codexResetCreditsURL {
		t.Fatalf("unexpected CPA request sequence: %#v", paths)
	}
}

func TestCodexResetCreditStartConsumesDueCreditAndRefreshes(t *testing.T) {
	fake := newFakeCodexCPA(t, fakeCodexCPAConfig{resetAt: "1799999999000"})
	defer fake.Close()
	st := openCodexResetCreditTestStore(t)
	worker := NewCodexResetCreditWorker(st)
	worker.SetClock(func() time.Time { return time.UnixMilli(1_800_000_000_000) })
	worker.SetInterval(time.Hour)
	worker.UpdateRuntimeConfig(context.Background(), collectorpkg.RuntimeConfig{CPAUpstreamURL: fake.URL(), ManagementKey: "test"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker.Start(ctx)
	fake.waitForConsume(t, true)
	fake.waitForCalls(t, 9)

	calls := fake.Calls()
	consumeAt := -1
	for i, target := range calls {
		if target == codexResetCreditsConsumeURL {
			consumeAt = i
			break
		}
	}
	if consumeAt < 0 || len(calls) <= consumeAt+2 || calls[consumeAt+1] != codexUsageURL || calls[consumeAt+2] != codexResetCreditsURL {
		t.Fatalf("successful consume was not followed by a fresh usage/reset refresh: %#v", calls)
	}
	if fake.ConsumeCalls() != 1 {
		t.Fatalf("consume calls = %d, want 1", fake.ConsumeCalls())
	}
}

func TestCodexResetCreditStartWakesWhenRuntimeConfigArrives(t *testing.T) {
	fake := newFakeCodexCPA(t, fakeCodexCPAConfig{resetAt: "1799999999000"})
	defer fake.Close()
	st := openCodexResetCreditTestStore(t)
	worker := NewCodexResetCreditWorker(st)
	worker.SetClock(func() time.Time { return time.UnixMilli(1_800_000_000_000) })
	worker.SetInterval(time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker.Start(ctx)
	time.Sleep(100 * time.Millisecond)
	worker.UpdateRuntimeConfig(context.Background(), collectorpkg.RuntimeConfig{CPAUpstreamURL: fake.URL(), ManagementKey: "test"})
	fake.waitForConsume(t, true)
	if fake.ConsumeCalls() != 1 {
		t.Fatalf("runtime config wake consumed %d reset credits, want 1", fake.ConsumeCalls())
	}
}

func TestCodexResetCreditStartDisabledDoesNotConsume(t *testing.T) {
	fake := newFakeCodexCPA(t, fakeCodexCPAConfig{resetAt: "1799999999000"})
	defer fake.Close()
	st := openCodexResetCreditTestStore(t)
	worker := NewCodexResetCreditWorker(st)
	worker.SetEnabled(false)
	worker.SetInterval(time.Hour)
	worker.UpdateRuntimeConfig(context.Background(), collectorpkg.RuntimeConfig{CPAUpstreamURL: fake.URL(), ManagementKey: "test"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker.Start(ctx)
	time.Sleep(100 * time.Millisecond)
	if fake.ConsumeCalls() != 0 {
		t.Fatalf("disabled worker consumed %d reset credits", fake.ConsumeCalls())
	}
	if len(fake.Calls()) != 0 {
		t.Fatalf("disabled worker contacted CPA: %#v", fake.Calls())
	}
}

func TestCodexResetCreditStartDoesNotConsumeFutureOrUnknownEvidence(t *testing.T) {
	tests := []struct {
		name    string
		resetAt string
	}{
		{name: "future", resetAt: "1800000001000"},
		{name: "unknown", resetAt: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeCodexCPA(t, fakeCodexCPAConfig{resetAt: tt.resetAt})
			defer fake.Close()
			st := openCodexResetCreditTestStore(t)
			worker := NewCodexResetCreditWorker(st)
			worker.SetClock(func() time.Time { return time.UnixMilli(1_800_000_000_000) })
			worker.SetInterval(time.Hour)
			worker.UpdateRuntimeConfig(context.Background(), collectorpkg.RuntimeConfig{CPAUpstreamURL: fake.URL(), ManagementKey: "test"})

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			worker.Start(ctx)
			fake.waitForCalls(t, 4)
			time.Sleep(50 * time.Millisecond)
			if fake.ConsumeCalls() != 0 {
				t.Fatalf("%s evidence caused %d consume calls", tt.name, fake.ConsumeCalls())
			}
		})
	}
}

func TestCodexResetCreditStartDoesNotRetryFailedConsumeInSameTick(t *testing.T) {
	fake := newFakeCodexCPA(t, fakeCodexCPAConfig{resetAt: "1799999999000", failConsume: true})
	defer fake.Close()
	st := openCodexResetCreditTestStore(t)
	worker := NewCodexResetCreditWorker(st)
	worker.SetClock(func() time.Time { return time.UnixMilli(1_800_000_000_000) })
	worker.SetInterval(time.Hour)
	worker.UpdateRuntimeConfig(context.Background(), collectorpkg.RuntimeConfig{CPAUpstreamURL: fake.URL(), ManagementKey: "test"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker.Start(ctx)
	fake.waitForConsume(t, false)
	time.Sleep(100 * time.Millisecond)
	if fake.ConsumeCalls() != 1 {
		t.Fatalf("failed consume was retried in the same tick: %d calls", fake.ConsumeCalls())
	}
}

type fakeCodexCPAConfig struct {
	resetAt     string
	failConsume bool
}

type fakeCodexCPA struct {
	server       *httptest.Server
	mu           sync.Mutex
	calls        []string
	consumeCalls int
}

func newFakeCodexCPA(t *testing.T, config fakeCodexCPAConfig) *fakeCodexCPA {
	t.Helper()
	fake := &fakeCodexCPA{}
	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v0/management/auth-files" {
			_ = json.NewEncoder(w).Encode(map[string]any{"files": []map[string]any{{
				"name": "codex-auth.json", "auth_index": "auth-1", "provider": "codex", "account_id": "account-1",
			}}})
			return
		}
		if r.URL.Path != "/v0/management/api-call" {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode api-call request: %v", err)
		}
		target, _ := body["url"].(string)
		fake.mu.Lock()
		fake.calls = append(fake.calls, target)
		if target == codexResetCreditsConsumeURL {
			fake.consumeCalls++
		}
		consumeCalls := fake.consumeCalls
		fake.mu.Unlock()

		if target == codexResetCreditsConsumeURL && config.failConsume {
			_ = json.NewEncoder(w).Encode(map[string]any{"status_code": 500, "body": map[string]any{}})
			return
		}
		response := map[string]any{}
		if target == codexResetCreditsURL {
			available := int64(1)
			if consumeCalls > 0 && !config.failConsume {
				available = 0
			}
			credits := map[string]any{"available_count": available, "cycle_id": "cycle-a"}
			if config.resetAt != "" {
				credits["reset_at"] = config.resetAt
			}
			response = map[string]any{"rate_limit_reset_credits": credits}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status_code": 200, "body": response})
	}))
	return fake
}

func (f *fakeCodexCPA) URL() string { return f.server.URL }

func (f *fakeCodexCPA) Close() { f.server.Close() }

func (f *fakeCodexCPA) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeCodexCPA) ConsumeCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.consumeCalls
}

func (f *fakeCodexCPA) waitForCalls(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(f.Calls()) >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d CPA api calls; got %#v", want, f.Calls())
}

func (f *fakeCodexCPA) waitForConsume(t *testing.T, success bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if f.ConsumeCalls() > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	if success {
		t.Fatalf("timed out waiting for successful consume; calls=%#v", f.Calls())
	}
	t.Fatalf("timed out waiting for failed consume; calls=%#v", f.Calls())
}

func openCodexResetCreditTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}
