package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/setting"
	automationsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/automation"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

const resourceCapabilitiesPath = "/usage-service/resource-capabilities"

func TestResourceCapabilitiesTruthfulSupport(t *testing.T) {
	cfg := testutil.NewConfig(t)
	db := testutil.NewStore(t, cfg)
	handler := New(cfg, db, collector.NewManager(cfg, db)).Handler()

	rr := testutil.Request(t, handler, http.MethodGet, resourceCapabilitiesPath, "", testutil.AdminKey)
	testutil.RequireStatus(t, rr, http.StatusOK)
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	var got map[string]any
	testutil.DecodeJSON(t, rr, &got)
	legacy := testutil.Request(t, handler, http.MethodGet, "/usage-service/account-processing-policy", "", testutil.AdminKey)
	testutil.RequireStatus(t, legacy, http.StatusOK)
	var policy map[string]any
	testutil.DecodeJSON(t, legacy, &policy)
	want := map[string]any{
		"schemaVersion":   float64(1),
		"identity":        map[string]any{"state": "unknown", "reason": "core_contract_unverified"},
		"query":           map[string]any{"state": "unknown", "reason": "query_contract_unverified"},
		"independentGate": map[string]any{"state": "unavailable", "reason": "consumer_contract_unconfigured"},
		"policy":          policy,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("support summary = %#v, want %#v", got, want)
	}
}

// Keep real persistence/auth/service wiring; inject faults only at its repository
// boundary, and observe whether policy storage was reached before authorization.
type capabilitySettingsProbe struct {
	setting.Repository
	policyReads int
	authReads   int
	policyErr   error
	authErr     error
}

func (p *capabilitySettingsProbe) LoadAutomationSettings(ctx context.Context) (store.AutomationSettings, bool, error) {
	p.policyReads++
	if p.policyErr != nil {
		return store.AutomationSettings{}, false, p.policyErr
	}
	return p.Repository.LoadAutomationSettings(ctx)
}

func (p *capabilitySettingsProbe) LoadAdminCredential(ctx context.Context) (store.AdminCredential, bool, error) {
	p.authReads++
	if p.authErr != nil {
		return store.AdminCredential{}, false, p.authErr
	}
	return p.Repository.LoadAdminCredential(ctx)
}

func TestResourceCapabilitiesAuthorizationBoundary(t *testing.T) {
	const decoy = "private-db https://decoy.invalid/path?key=synthetic-secret"
	for _, tc := range []struct {
		name          string
		uninitialized bool
		key           string
		authErr       error
		status        int
		message       string
		code          string
	}{
		{name: "no key and no CPA setup", status: 401, message: "invalid admin key", code: "invalid_admin_key"},
		{name: "wrong key", key: "synthetic-wrong-key", status: 401, message: "invalid admin key", code: "invalid_admin_key"},
		{name: "manager not initialized", uninitialized: true, status: 500, message: "authorization failed", code: "request_failed"},
		{name: "auth storage fails", key: testutil.AdminKey, authErr: errors.New(decoy), status: 500, message: "authorization failed", code: "request_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testutil.NewConfig(t)
			var db *store.Store
			if tc.uninitialized {
				var err error
				db, err = store.Open(cfg.DBPath)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = db.Close() })
			} else {
				db = testutil.NewStore(t, cfg)
			}
			probe := &capabilitySettingsProbe{Repository: db.Settings, authErr: tc.authErr}
			db.Settings = probe
			handler := New(cfg, db, collector.NewManager(cfg, db)).Handler()
			var logs bytes.Buffer
			oldOutput := log.Writer()
			log.SetOutput(&logs)
			defer log.SetOutput(oldOutput)
			rr := testutil.Request(t, handler, http.MethodGet, resourceCapabilitiesPath, "", tc.key)
			testutil.RequireStatus(t, rr, tc.status)
			var got map[string]any
			testutil.DecodeJSON(t, rr, &got)
			if !reflect.DeepEqual(got, map[string]any{"error": tc.message, "code": tc.code}) {
				t.Fatalf("auth error must be allowlisted, got %#v", got)
			}
			if rr.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("auth response must not be cached")
			}
			if probe.authReads == 0 || probe.policyReads != 0 {
				t.Fatalf("auth reads = %d, policy reads = %d; auth must precede policy", probe.authReads, probe.policyReads)
			}
			for _, private := range []string{decoy, "decoy.invalid", "synthetic-secret"} {
				if strings.Contains(rr.Body.String()+logs.String(), private) {
					t.Fatal("private auth detail leaked in response or log")
				}
			}
		})
	}
}

func TestResourceCapabilitiesReadOnlyWithSavedConnection(t *testing.T) {
	var upstreamCalls atomic.Int64
	// This deny-all fixture is the only listening socket; no app/runtime is started.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		http.Error(w, "test upstream denies all requests", http.StatusForbidden)
	}))
	t.Cleanup(upstream.Close)
	cfg := testutil.NewConfig(t)
	db := testutil.NewStore(t, cfg)
	ctx := context.Background()
	if err := db.SaveManagerConfig(ctx, store.ManagerConfig{
		CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: upstream.URL, ManagementKey: "synthetic-connection-decoy"},
	}); err != nil {
		t.Fatal(err)
	}
	enabled := true
	if _, err := db.SaveAutomationSettings(ctx, store.AutomationSettings{
		QuotaCooldownEnabled: &enabled, AccountActionsEnabled: &enabled, AccountActionsAutoDisable: &enabled,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.QuotaCooldowns.UpsertActive(ctx, store.QuotaCooldownUpsert{
		AuthFileName: "synthetic-manually-disabled.json", Provider: "xai", AuthIndex: "0",
		Owner: "cpamp_usage_429", PreDisabledState: true, DisabledAtMS: 1700000000000,
		RecoverAtMS: 1700003600000,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AccountActions.Upsert(ctx, store.AccountActionCandidateUpsert{
		AuthFileName: "synthetic-manually-disabled.json", Provider: "xai", AuthIndex: "0",
		ActionType: "review", Reason: "synthetic manual state", SeenAtMS: 1700000000000,
	}); err != nil {
		t.Fatal(err)
	}
	handler := New(cfg, db, collector.NewManager(cfg, db)).Handler()
	beforePolicy, _, err := db.LoadAutomationSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	beforeManual, err := db.QuotaCooldowns.ListActive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	beforeActions, err := db.AccountActions.List(ctx, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	beforeConnection, _, err := db.LoadManagerConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// data_version on one observer connection detects ANY committed DB write by
	// the manager, including same-value writes outside the policy repository.
	observer, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = observer.Close() })
	conn, err := observer.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	var beforeVersion int64
	if err := conn.QueryRowContext(ctx, "PRAGMA data_version").Scan(&beforeVersion); err != nil {
		t.Fatal(err)
	}

	for _, method := range []string{http.MethodGet, http.MethodOptions, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodTrace, http.MethodConnect, "PURGE"} {
		t.Run(method, func(t *testing.T) {
			body := ""
			want := http.StatusMethodNotAllowed
			switch method {
			case http.MethodGet:
				want = http.StatusOK
			case http.MethodOptions:
				want = http.StatusNoContent
			default:
				body = `{"codexQuotaCooldownEnabled":false,"authIssueQueueEnabled":false,"authIssueAutoDisableEnabled":false,"disabled":false,"excluded-models":[]}`
			}
			rr := testutil.Request(t, handler, method, resourceCapabilitiesPath, body, testutil.AdminKey)
			testutil.RequireStatus(t, rr, want)
			if rr.Header().Get("Cache-Control") != "no-store" {
				t.Error("response must not be cached")
			}
			if want == http.StatusMethodNotAllowed && rr.Header().Get("Allow") != "GET, OPTIONS" {
				t.Error("mutations must advertise only GET, OPTIONS")
			}
			if method == http.MethodOptions && rr.Body.Len() != 0 {
				t.Error("preflight must be empty")
			}
			if strings.Contains(rr.Body.String(), "synthetic-") || strings.Contains(rr.Body.String(), upstream.URL) {
				t.Error("response leaked saved connection/manual fixture details")
			}
		})
	}
	for _, key := range []string{"", "synthetic-connection-decoy"} {
		rr := testutil.Request(t, handler, http.MethodGet, resourceCapabilitiesPath, "", key)
		testutil.RequireStatus(t, rr, http.StatusUnauthorized)
		if rr.Header().Get("Cache-Control") != "no-store" {
			t.Error("unauthorized saved-connection response must not be cached")
		}
	}
	var afterVersion int64
	if err := conn.QueryRowContext(ctx, "PRAGMA data_version").Scan(&afterVersion); err != nil {
		t.Fatal(err)
	}
	if afterVersion != beforeVersion {
		t.Errorf("endpoint wrote database: data_version %d -> %d", beforeVersion, afterVersion)
	}
	afterPolicy, _, err := db.LoadAutomationSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	afterManual, err := db.QuotaCooldowns.ListActive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	afterActions, err := db.AccountActions.List(ctx, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	afterConnection, _, err := db.LoadManagerConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(beforePolicy, afterPolicy) || !reflect.DeepEqual(beforeManual, afterManual) || !reflect.DeepEqual(beforeActions, afterActions) || !reflect.DeepEqual(beforeConnection, afterConnection) {
		t.Error("read/mutation requests changed persisted policy/manual/connection data")
	}
	if calls := upstreamCalls.Load(); calls != 0 {
		t.Errorf("upstream calls = %d, want zero", calls)
	}
}

func TestResourceCapabilitiesPersistedPolicyAndEnvLocksAreNotACK(t *testing.T) {
	for _, locked := range []bool{false, true} {
		name := "persisted true"
		if locked {
			name = "environment locks override persisted true"
		}
		t.Run(name, func(t *testing.T) {
			cfg := testutil.NewConfig(t)
			db := testutil.NewStore(t, cfg)
			enabled := true
			saved, err := db.SaveAutomationSettings(context.Background(), store.AutomationSettings{
				QuotaCooldownEnabled: &enabled, AccountActionsEnabled: &enabled, AccountActionsAutoDisable: &enabled,
			})
			if err != nil {
				t.Fatal(err)
			}
			if saved.UpdatedAtMS <= 0 {
				t.Fatal("policy fixture must have a persisted timestamp")
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db = testutil.NewStore(t, cfg) // Prove real persistence, not a service cache.
			cfg.QuotaCooldownEnvSet = locked
			cfg.AccountActionsEnvSet = locked
			cfg.AccountActionsAutoEnvSet = locked
			cfg.AccountActionsAutoDisable = true
			handler := New(cfg, db, collector.NewManager(cfg, db)).Handler()
			rr := testutil.Request(t, handler, http.MethodGet, resourceCapabilitiesPath, "", testutil.AdminKey)
			testutil.RequireStatus(t, rr, http.StatusOK)
			var summary struct {
				Policy automationsvc.Status `json:"policy"`
				Gate   map[string]string    `json:"independentGate"`
			}
			testutil.DecodeJSON(t, rr, &summary)
			source := "database"
			if locked {
				source = "env"
			}
			want := automationsvc.Status{
				Source: source, UpdatedAtMS: saved.UpdatedAtMS,
				QuotaCooldown: automationsvc.Capability{
					Enabled: !locked, Configured: !locked, Source: source, Locked: locked,
					EnvKey: "USAGE_QUOTA_COOLDOWN_ENABLED", ConfigFileKey: "quotaCooldownEnabled",
				},
				AccountActions: automationsvc.Capability{
					Enabled: !locked, Configured: !locked, Source: source, Locked: locked,
					EnvKey: "USAGE_ACCOUNT_ACTIONS_ENABLED", ConfigFileKey: "accountActionsEnabled",
				},
				AccountActionsAutoDisable: automationsvc.Capability{
					Enabled: !locked, Configured: true, Source: source, Locked: locked,
					EnvKey: "USAGE_ACCOUNT_ACTIONS_AUTO_DISABLE", ConfigFileKey: "accountActionsAutoDisable", DependsOn: "authIssueQueue",
				},
			}
			if !reflect.DeepEqual(summary.Policy, want) {
				t.Fatalf("policy = %#v, want %#v", summary.Policy, want)
			}
			if !reflect.DeepEqual(summary.Gate, map[string]string{"state": "unavailable", "reason": "consumer_contract_unconfigured"}) {
				t.Fatalf("policy must never acknowledge an independent gate: %#v", summary.Gate)
			}
			var raw map[string]json.RawMessage
			testutil.DecodeJSON(t, rr, &raw)
			legacy := testutil.Request(t, handler, http.MethodGet, "/usage-service/account-processing-policy", "", testutil.AdminKey)
			testutil.RequireStatus(t, legacy, http.StatusOK)
			if !bytes.Equal(bytes.TrimSpace(raw["policy"]), bytes.TrimSpace(legacy.Body.Bytes())) {
				t.Fatal("policy must preserve the legacy automation.Status JSON verbatim")
			}
			patch := testutil.Request(t, handler, http.MethodPatch, "/usage-service/account-processing-policy", `{"codexQuotaCooldownEnabled":false}`, testutil.AdminKey)
			if locked {
				testutil.RequireStatus(t, patch, http.StatusConflict)
			} else {
				testutil.RequireStatus(t, patch, http.StatusOK)
				next := testutil.Request(t, handler, http.MethodGet, resourceCapabilitiesPath, "", testutil.AdminKey)
				testutil.RequireStatus(t, next, http.StatusOK)
				testutil.DecodeJSON(t, next, &summary)
				if summary.Policy.QuotaCooldown.Enabled || summary.Policy.QuotaCooldown.Configured {
					t.Fatal("capabilities must observe subsequent legacy policy changes")
				}
				if summary.Gate["state"] != "unavailable" {
					t.Fatal("legacy PATCH must not configure a gate")
				}
			}
		})
	}
}

func TestResourceCapabilitiesStorageFailureIsGeneric(t *testing.T) {
	const decoy = "storage at /synthetic/private.sqlite https://decoy.invalid?key=synthetic-secret"
	cfg := testutil.NewConfig(t)
	db := testutil.NewStore(t, cfg)
	probe := &capabilitySettingsProbe{Repository: db.Settings}
	db.Settings = probe
	handler := New(cfg, db, collector.NewManager(cfg, db)).Handler()
	// Warm a successful read first. A subsequent failure must not serve stale
	// policy or a partial/default summary with a misleading HTTP 200.
	rr := testutil.Request(t, handler, http.MethodGet, resourceCapabilitiesPath, "", testutil.AdminKey)
	testutil.RequireStatus(t, rr, http.StatusOK)
	probe.policyErr = errors.New(decoy)
	var logs bytes.Buffer
	oldOutput := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(oldOutput)
	rr = testutil.Request(t, handler, http.MethodGet, resourceCapabilitiesPath, "", testutil.AdminKey)
	testutil.RequireStatus(t, rr, http.StatusInternalServerError)
	var got map[string]any
	testutil.DecodeJSON(t, rr, &got)
	if !reflect.DeepEqual(got, map[string]any{"error": "resource capabilities unavailable", "code": "request_failed"}) {
		t.Fatalf("storage error must be allowlisted without partial summary, got %#v", got)
	}
	if rr.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("failure response must not be cached")
	}
	for _, private := range []string{decoy, "/synthetic/private.sqlite", "decoy.invalid", "synthetic-secret"} {
		if strings.Contains(rr.Body.String()+logs.String(), private) {
			t.Fatal("private storage detail leaked in response or log")
		}
	}
	if probe.policyReads != 2 || probe.authReads != 2 {
		t.Fatalf("reads: auth %d, policy %d", probe.authReads, probe.policyReads)
	}
}

func TestResourceCapabilitiesPreflightDoesNotReadStorage(t *testing.T) {
	cfg := testutil.NewConfig(t)
	db := testutil.NewStore(t, cfg)
	probe := &capabilitySettingsProbe{Repository: db.Settings, authErr: errors.New("auth must not be read"), policyErr: errors.New("policy must not be read")}
	db.Settings = probe
	handler := New(cfg, db, collector.NewManager(cfg, db)).Handler()
	req := httptest.NewRequest(http.MethodOptions, resourceCapabilitiesPath, nil)
	req.Header.Set("Origin", "https://panel.invalid")
	req.Header.Set("Access-Control-Request-Method", http.MethodGet)
	req.Header.Set("Access-Control-Request-Headers", "authorization")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	testutil.RequireStatus(t, rr, http.StatusNoContent)
	if rr.Header().Get("Cache-Control") != "no-store" || rr.Header().Get("Access-Control-Allow-Origin") != "*" || !strings.Contains(rr.Header().Get("Access-Control-Allow-Headers"), "Authorization") || rr.Body.Len() != 0 {
		t.Fatalf("invalid preflight: headers %#v, body %s", rr.Header(), rr.Body.String())
	}
	if probe.authReads != 0 || probe.policyReads != 0 {
		t.Fatal("preflight read storage")
	}
}
