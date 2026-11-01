package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	cp "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy"
	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/usage"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/worker"
)

func TestCredentialPolicyLegacyUsageCandidateCannotDisableScopedOFF(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		name := "native"
		if unavailable {
			name = "transient-capability-failure"
		}
		t.Run(name, func(t *testing.T) {
			f := newNativeAdmissionFixture(t)
			src := f.sources["B"]
			var modeFault atomic.Bool
			var modeReads, legacyCalls atomic.Int64
			core, _ := url.Parse(f.base)
			forward := httputil.NewSingleHostReverseProxy(core)
			patched := make(chan int, 4)
			forward.ModifyResponse = func(response *http.Response) error {
				if response.Request.Method == "PATCH" && response.Request.URL.Path == "/v0/management/auth-files/status" {
					patched <- response.StatusCode
				}
				return nil
			}
			edge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v8/management/resource-capabilities" {
					modeReads.Add(1)
					if modeFault.Load() {
						http.Error(w, "controlled unavailable", 503)
						return
					}
				}
				if len(r.URL.Path) >= 15 && r.URL.Path[:15] == "/v0/management/" {
					legacyCalls.Add(1)
				}
				forward.ServeHTTP(w, r)
			}))
			defer edge.Close()
			if err := f.db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: edge.URL, ManagementKey: "test-password"}}); err != nil {
				t.Fatal(err)
			}
			zero, off := int64(0), false
			var saved cp.Status
			f.api("PUT", "/usage-service/credential-policy", cp.Update{Binding: src.Binding, AuthIndex: src.AuthIndex, SourceVersion: src.SourceVersion, ExpectedGeneration: &zero, Enabled: &off}, 200, &saved)
			f.api("PATCH", "/usage-service/account-processing-policy", map[string]any{"codexQuotaCooldownEnabled": true}, 200, nil)
			cfg := collector.RuntimeConfig{CPAUpstreamURL: edge.URL, ManagementKey: "test-password"}
			legacy := worker.NewRateLimitAutoDisableWorkerWithMutationCoordinator(f.db, f.manager.AppContext().AuthFileMutationCoordinator, cfg)
			legacy.SetLegacyQuotaGuard(f.manager.AppContext().ProxyService.LegacyQuotaGuard)
			legacy.SetEnabled(f.manager.AppContext().AccountProcessingPolicyService.RuntimeSettings(context.Background()).QuotaCooldownEnabled)
			baseline := modeReads.Load()
			modeFault.Store(unavailable)
			// Actual public monitoring input, not authoritative quota evidence.
			event := usage.Event{EventHash: "controlled-legacy-429", Failed: true, FailStatusCode: 429, FailBody: `{"error":{"type":"usage_limit_reached","resets_in_seconds":60}}`, AuthFileSnapshot: "native-B.json", AuthIndex: src.AuthIndex, Provider: "codex"}
			legacy.HandleUsageEvents(context.Background(), cfg, []usage.Event{event})
			if modeReads.Load() == baseline {
				// Baseline diagnosis uses the REAL queued worker, not a manually
				// constructed candidate or private handler. Wait on actual Core ACK.
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				legacy.Start(ctx)
				select {
				case status := <-patched:
					var changed struct {
						Files []struct {
							Name     string `json:"name"`
							Disabled bool   `json:"disabled"`
						} `json:"files"`
					}
					f.coreAPI("GET", "credentials", nil, &changed)
					for _, file := range changed.Files {
						if file.Name == "native-B.json" {
							t.Fatalf("actual legacy worker bypassed scoped OFF: PATCH status=%d disabledB=%t modeReadsBeforeEnqueue=%d legacyCalls=%d", status, file.Disabled, baseline, legacyCalls.Load())
						}
					}
					t.Fatal("actual B disappeared after legacy write")
				case <-time.After(10 * time.Second):
					t.Fatalf("no mode resolution or completed legacy mutation; legacyCalls=%d", legacyCalls.Load())
				}
			}
			if legacyCalls.Load() != 0 {
				t.Fatal("legacy automatic candidate touched v0 in scoped/unknown mode")
			}
			var files struct {
				Files []struct {
					Name     string `json:"name"`
					Disabled bool   `json:"disabled"`
				} `json:"files"`
			}
			f.coreAPI("GET", "credentials", nil, &files)
			found := false
			for _, file := range files.Files {
				if file.Name == "native-B.json" {
					found = true
					if file.Disabled {
						t.Fatal("OFF B was disabled by legacy global policy")
					}
				}
			}
			if !found {
				t.Fatal("actual B missing")
			}
			var current cp.Status
			f.api("GET", "/usage-service/credential-policy?subjectId="+saved.SubjectID, nil, 200, &current)
			if current.Enabled || current.Generation != saved.Generation {
				t.Fatal("legacy event changed stored OFF intent")
			}
		})
	}
}

// Historical legacy ownership is a SQLite precondition, not current scoped
// policy authority. Even an actually successful quota query cannot authorize a
// legacy writer to clear a current manual disable in scoped mode.
func TestCredentialPolicyLegacyRecoveryCannotClearScopedManualDisable(t *testing.T) {
	f := newNativeAdmissionFixture(t, true)
	src := f.sources["A"]
	var policy cp.Status
	zero, off := int64(0), false
	f.api("PUT", "/usage-service/credential-policy", cp.Update{Binding: src.Binding, AuthIndex: src.AuthIndex, SourceVersion: src.SourceVersion, ExpectedGeneration: &zero, Enabled: &off}, 200, &policy)
	f.api("PATCH", "/usage-service/account-processing-policy", map[string]any{"codexQuotaCooldownEnabled": true}, 200, nil)
	if policy.Enabled || !src.ManualDisabled {
		t.Fatal("missing actual scoped OFF/manual-disable precondition")
	}
	var snapshot cp.GateSnapshot
	f.coreAPI("GET", "inference-gates", nil, &snapshot)
	other := cp.GateOperation{Version: 1, OpID: uuid.NewString(), ExpectedGateRevision: snapshot.GateRevision, Binding: src.Binding, AuthIndex: src.AuthIndex, SourceVersion: src.SourceVersion, Subject: "credential", Owner: "legacy-isolation-independent-owner", Action: "pause", Reason: "independent_restriction", EvidenceID: uuid.NewString()}
	var receipt cp.GateResult
	f.coreAPI("POST", "inference-gates/operations", other, &receipt)
	f.coreAPI("GET", "inference-gates", nil, &snapshot)
	if !snapshot.Confirms(other, receipt) {
		t.Fatal("independent current-owner precondition not actually applied")
	}
	var status cq.Status
	f.api("GET", "/usage-service/credential-quota?authIndex="+src.AuthIndex, nil, 200, &status)
	var job cq.Job
	f.api("POST", "/usage-service/credential-quota", cq.Query{Binding: src.Binding, SourceVersion: src.SourceVersion, AuthIndex: src.AuthIndex, Version: status.Version}, 202, &job)
	select {
	case event := <-f.completed:
		if event.AttemptID != job.ID || event.Credential == nil || event.Credential.Attempt == nil || event.Credential.Attempt.QueryStatus != "success" {
			t.Fatal("actual same76 recovery observation missing")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("actual quota completion missing")
	}
	now := time.Now()
	_, err := f.db.UpsertQuotaCooldown(context.Background(), store.QuotaCooldownUpsert{AuthFileName: "codex.json", AuthIndex: src.AuthIndex, Provider: "codex", ReasonCode: "codex_usage_limit_reached", WindowKind: "weekly", RecoverAtMS: now.Add(-time.Minute).UnixMilli(), Owner: model.QuotaCooldownOwnerUsage429, EventHash: "historical-legacy-ownership", DisabledAtMS: now.Add(-time.Hour).UnixMilli()})
	if err != nil {
		t.Fatal(err)
	}
	upstream, _ := url.Parse(f.base)
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	var mutations atomic.Int64
	proxy.ModifyResponse = func(response *http.Response) error {
		if response.Request.Method == "PATCH" && response.Request.URL.Path == "/v0/management/auth-files/status" {
			mutations.Add(1)
			t.Logf("actual Core legacy PATCH status=%d", response.StatusCode)
		}
		return nil
	}
	edge := httptest.NewServer(proxy)
	defer edge.Close()
	legacy := worker.NewRateLimitAutoDisableWorkerWithMutationCoordinator(f.db, f.manager.AppContext().AuthFileMutationCoordinator, collector.RuntimeConfig{CPAUpstreamURL: edge.URL, ManagementKey: "test-password"})
	legacy.SetLegacyQuotaGuard(f.manager.AppContext().ProxyService.LegacyQuotaGuard)
	settings := f.manager.AppContext().AccountProcessingPolicyService.RuntimeSettings(context.Background())
	if !settings.QuotaCooldownEnabled {
		t.Fatal("legacy global ON not actually persisted")
	}
	legacy.SetEnabled(settings.QuotaCooldownEnabled)
	recovered, recoveryErr := legacy.RecoverOwnedCooldown(context.Background(), "codex.json", src.AuthIndex, "codex")
	current := f.status("A")
	var after cp.GateSnapshot
	f.coreAPI("GET", "inference-gates", nil, &after)
	t.Logf("actual legacy recovery returned=%t err=%v mutations=%d scopedEnabled=%t manualDisabled=%t", recovered, recoveryErr, mutations.Load(), current.Enabled, current.ManualDisabled)
	if recovered || mutations.Load() != 0 || current.Enabled || !current.ManualDisabled || !reflect.DeepEqual(snapshot, after) {
		t.Fatal("legacy global/old-owner recovery bypassed scoped policy OFF or cleared current manual disable")
	}
}

// A negative exact-target response is not permission once the current saved
// Manager connection has changed to a real native Core during that I/O.
func TestCredentialPolicyLegacyDelayed404CannotAuthorizeAfterNativeRebind(t *testing.T) {
	f := newNativeAdmissionFixture(t)
	blocked, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	var reads atomic.Int64
	old := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v8/management/resource-capabilities" || r.Header.Get("Authorization") != "Bearer test-password" {
			t.Errorf("unexpected old-target request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(400)
			return
		}
		if reads.Add(1) == 2 {
			close(blocked)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer old.Close()
	defer unblock()
	setConnection := func(base string) {
		f.api("PUT", "/usage-service/config", map[string]any{"config": map[string]any{
			"cpaConnection": map[string]any{"cpaBaseUrl": base, "managementKey": "test-password"},
			"collector":     map[string]any{"enabled": false},
		}}, 200, nil)
	}
	if err := f.db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: old.URL, ManagementKey: "test-password"}}); err != nil {
		t.Fatal(err)
	}
	guard := f.manager.AppContext().ProxyService.LegacyQuotaGuard
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- guard.CheckTarget(ctx, old.URL, "test-password") }()
	select {
	case <-blocked:
	case <-ctx.Done():
		t.Fatal("exact-target second request did not enter barrier")
	}
	setConnection(f.base)
	unblock()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("stale exact-target404 authorized legacy write after public Manager native connection rebind")
		}
		t.Logf("delayed legacy404 denied after actual native rebind: %v; old mode reads=%d", err, reads.Load())
	case <-ctx.Done():
		t.Fatal("legacy check did not complete after release")
	}
}
