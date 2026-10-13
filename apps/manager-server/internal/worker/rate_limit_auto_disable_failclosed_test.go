package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	collectorpkg "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpaauthfiles"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

// authoritativeRecoveredQuota writes the normalized quota envelope that
// confirms every applicable window has recovered. Recovery tests whose fake CPA
// serves this answer the SPEC's "authoritative positive confirmation" gate.
func authoritativeRecoveredQuota(w http.ResponseWriter, window string) {
	if window == "" {
		window = "weekly"
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"groups": []map[string]any{{
			"buckets": []map[string]any{{"window": window, "remainingFraction": 1.0}},
		}},
	})
}

// failClosedTarget builds the resolved mutation target the recovery gate
// receives right before it decides between enable and defer.
func failClosedTarget() cpaauthfiles.StatusMutationTarget {
	return cpaauthfiles.StatusMutationTarget{
		Selector: "codex-auth.json",
		File: cpaauthfiles.File{
			ID:              "runtime-codex",
			Name:            "codex-auth.json",
			AuthIndex:       "auth-1",
			Provider:        "codex",
			AccountSnapshot: "alice@example.com",
			AccountID:       "workspace-1",
			Disabled:        true,
		},
	}
}

// failClosedRecoveryServer serves the minimum CPA management surface needed to
// reach the recovery gate. probeHandler answers the quota probe calls so each
// test can decide whether the probe errors out or reports "no authoritative
// source".
func failClosedRecoveryServer(t *testing.T, probeHandler http.HandlerFunc) (*httptest.Server, *int, *[]bool) {
	t.Helper()
	var mu sync.Mutex
	patchCalls := 0
	patchedDisabled := []bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v0/management/auth-files":
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"id":         "runtime-codex",
				"name":       "codex-auth.json",
				"auth_index": "auth-1",
				"provider":   "codex",
				"account":    "alice@example.com",
				"account_id": "workspace-1",
				"disabled":   true,
			}})
		case r.Method == http.MethodPatch && r.URL.Path == "/v0/management/auth-files/status":
			var payload struct {
				Disabled bool `json:"disabled"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			mu.Lock()
			patchCalls++
			patchedDisabled = append(patchedDisabled, payload.Disabled)
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		case r.Method == http.MethodPost && r.URL.Path == "/v0/management/quota/fetch":
			probeHandler(w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/v0/management/api-call":
			probeHandler(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	return server, &patchCalls, &patchedDisabled
}

func seedProviderCooldown(t *testing.T, st *store.Store, now time.Time) store.QuotaCooldown {
	t.Helper()
	seeded, err := st.UpsertQuotaCooldown(context.Background(), store.QuotaCooldownUpsert{
		AuthFileName:     "codex-auth.json",
		AuthIndex:        "auth-1",
		Provider:         "codex",
		RecoverAtMS:      now.Add(-time.Minute).UnixMilli(),
		Owner:            model.QuotaCooldownOwnerUsage429,
		PreDisabledState: false,
		DisabledAtMS:     now.Add(-time.Hour).UnixMilli(),
	})
	if err != nil {
		t.Fatalf("seed cooldown: %v", err)
	}
	if seeded.RecoverAtKind != model.QuotaCooldownRecoverKindProvider {
		t.Fatalf("seeded recover kind = %q, want provider", seeded.RecoverAtKind)
	}
	return seeded
}

func assertNoEnable(t *testing.T, st *store.Store, patchCalls *int, wantReasonFragment string) {
	t.Helper()
	if *patchCalls != 0 {
		t.Fatalf("patch calls = %d, want no enable while the probe cannot confirm recovery", *patchCalls)
	}
	active, err := st.QuotaCooldowns.ListActive(context.Background())
	if err != nil {
		t.Fatalf("list active cooldowns: %v", err)
	}
	if len(active) != 1 {
		t.Fatalf("active cooldowns = %#v, want exactly one retained cooldown", active)
	}
	if !strings.Contains(active[0].LastError, wantReasonFragment) {
		t.Fatalf("last error = %q, want reason containing %q", active[0].LastError, wantReasonFragment)
	}
}

// A provider-reset cooldown must stay disabled when the recovery probe itself
// fails (management endpoint unreachable / connection dropped).
func TestProviderResetRecoveryFailsClosedOnProbeError(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	server, patchCalls, _ := failClosedRecoveryServer(t, func(w http.ResponseWriter, r *http.Request) {
		// Drop the connection so the probe returns a transport error.
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "no hijack", http.StatusInternalServerError)
			return
		}
		conn, _, hijackErr := hj.Hijack()
		if hijackErr != nil {
			http.Error(w, hijackErr.Error(), http.StatusInternalServerError)
			return
		}
		_ = conn.Close()
	})
	defer server.Close()

	ctx := context.Background()
	now := time.Now()
	seeded := seedProviderCooldown(t, st, now)

	worker := NewRateLimitAutoDisableWorker(st, collectorpkg.RuntimeConfig{CPAUpstreamURL: server.URL, ManagementKey: "mgmt"})
	worker.enableDue(ctx, now)

	assertNoEnable(t, st, patchCalls, "not confirmed")
	if seeded.RecoverAtMS == 0 {
		t.Fatal("seeded recoverAtMs = 0, want a provider reset timestamp")
	}
}

// A provider-reset cooldown must stay disabled when no authoritative quota
// source can be read (quota/fetch unavailable and provider usage fallback
// yields nothing).
func TestProviderResetRecoveryFailsClosedWithoutAuthoritativeQuotaSource(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	server, patchCalls, _ := failClosedRecoveryServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/management/quota/fetch":
			http.Error(w, `{"error":"no quota provider available for credential"}`, http.StatusNotImplemented)
		case "/v0/management/api-call":
			http.Error(w, "not found", http.StatusNotFound)
		default:
			http.NotFound(w, r)
		}
	})
	defer server.Close()

	ctx := context.Background()
	now := time.Now()
	seedProviderCooldown(t, st, now)

	worker := NewRateLimitAutoDisableWorker(st, collectorpkg.RuntimeConfig{CPAUpstreamURL: server.URL, ManagementKey: "mgmt"})
	worker.enableDue(ctx, now)

	assertNoEnable(t, st, patchCalls, "not confirmed")
}

// Without a probe at all there is no authoritative confirmation, so a
// provider-reset cooldown must not be enabled either.
func TestProviderResetRecoveryFailsClosedWithoutProbe(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	server, patchCalls, _ := failClosedRecoveryServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "probe should not be consulted", http.StatusInternalServerError)
	})
	defer server.Close()

	ctx := context.Background()
	now := time.Now()
	seedProviderCooldown(t, st, now)

	worker := NewRateLimitAutoDisableWorker(st, collectorpkg.RuntimeConfig{CPAUpstreamURL: server.URL, ManagementKey: "mgmt"})
	worker.quotaProbe = nil
	worker.enableDue(ctx, now)

	assertNoEnable(t, st, patchCalls, "not confirmed")
}

// A probe that answers "available" but without any usable window is not a
// positive confirmation: no window data means we cannot prove recovery.
func TestProviderResetRecoveryFailsClosedWhenProbeHasNoWindows(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	server, _, _ := failClosedRecoveryServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unexpected probe call", http.StatusInternalServerError)
	})
	defer server.Close()

	worker := NewRateLimitAutoDisableWorker(st, collectorpkg.RuntimeConfig{CPAUpstreamURL: server.URL, ManagementKey: "mgmt"})
	worker.quotaProbe = staticQuotaProbe{result: quotaProbeResult{Available: true}}

	item := store.QuotaCooldown{
		ID:              7,
		AuthFileName:    "codex-auth.json",
		AuthIndex:       "auth-1",
		Provider:        "codex",
		RecoverAtKind:   model.QuotaCooldownRecoverKindProvider,
		RecoverAtMS:     time.Now().Add(-time.Minute).UnixMilli(),
		NextCheckAtMS:   time.Now().Add(-time.Minute).UnixMilli(),
		Owner:           model.QuotaCooldownOwnerUsage429,
		AccountSnapshot: "alice@example.com",
	}
	reason, deferred := worker.recoveryDeferred(context.Background(), server.URL, "mgmt", failClosedTarget(), item)
	if !deferred {
		t.Fatalf("recoveryDeferred = (%q, false), want deferred when no window data is available", reason)
	}
	if !strings.Contains(reason, "not confirmed") {
		t.Fatalf("defer reason = %q, want a not-confirmed reason", reason)
	}
}

// staticQuotaProbe is a deterministic probe used by the fail-closed tests.
type staticQuotaProbe struct {
	result quotaProbeResult
	err    error
}

func (p staticQuotaProbe) ProbeQuotaRecovery(_ context.Context, _, _, _, _, _ string) (quotaProbeResult, error) {
	return p.result, p.err
}
