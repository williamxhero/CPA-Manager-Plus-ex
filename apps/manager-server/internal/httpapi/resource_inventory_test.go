package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

const negotiatedPath = resourceCapabilitiesPath + "?inventory=1"

func TestResourceInventoryUnavailableMode(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v8/management/resource-capabilities" || r.Header.Get("Authorization") != "Bearer synthetic-core-key" {
			t.Errorf("unexpected management request: %s %s", r.Method, r.URL.Path)
		}
		w.Write([]byte(`{"version":1,"inventory":false,"durable":false,"gate":false,"query":false,"policy_locked":true,"reason":"unsupported_mode","secret":"CANARY-SECRET"}`))
	}))
	defer upstream.Close()
	cfg := testutil.NewConfig(t)
	db := testutil.NewStore(t, cfg)
	if err := db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: upstream.URL, ManagementKey: "synthetic-core-key"}}); err != nil {
		t.Fatal(err)
	}
	handler := New(cfg, db, collector.NewManager(cfg, db)).Handler()
	rr := testutil.Request(t, handler, "GET", negotiatedPath, "", testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	var got struct {
		Negotiated struct {
			Identity     struct{ State, Reason string }
			PolicyLocked bool `json:"policyLocked"`
			Snapshot     any
		}
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Negotiated.Identity.State != "unavailable" || got.Negotiated.Identity.Reason != "unsupported_mode" || !got.Negotiated.PolicyLocked || got.Negotiated.Snapshot != nil {
		t.Fatalf("unsupported mode must remain unavailable, not an empty inventory: %s", rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "CANARY") || strings.Contains(rr.Body.String(), "synthetic-core-key") {
		t.Fatal("secret leaked")
	}
}

const testScopeJSON = `"instance_id":"11111111-1111-4111-8111-111111111111","instance_incarnation":"22222222-2222-4222-8222-222222222222","boot_epoch":"33333333-3333-4333-8333-333333333333","snapshot_revision":7`
const testTargetJSON = `"resource_id":"44444444-4444-4444-8444-444444444444","incarnation":"55555555-5555-4555-8555-555555555555","identity_version":2,"credential_version":3,"config_version":4,"membership_version":0`
const testSnapshotJSON = `{"version":1,` + testScopeJSON + `,"complete":true,"resources":[{` + testTargetJSON + `,"kind":"credential","family":"codex","manual":{"disabled":true,"excluded":false,"zero_weight":false},"identity":"valid","catalog_readiness":"ready","runtime_readiness":"unknown","capabilities":{"inventory":true,"query":false,"gate":false},"name":"CANARY-NAME","api_key":"CANARY-KEY"}]}`
const testCapsJSON = `{"version":1,"inventory":true,"durable":true,"gate":false,"query":false,"policy_locked":true,"reason":"available","scope":{` + testScopeJSON + `}}`

func TestResourceInventoryCompleteVersionedRead(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer synthetic-core-key" {
			t.Error("not authenticated read")
		}
		switch r.URL.Path {
		case "/v8/management/resource-capabilities":
			w.Write([]byte(testCapsJSON))
		case "/v8/management/resources":
			w.Write([]byte(testSnapshotJSON))
		default:
			t.Errorf("unsafe inventory path %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer upstream.Close()
	cfg := testutil.NewConfig(t)
	db := testutil.NewStore(t, cfg)
	if err := db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: upstream.URL, ManagementKey: "synthetic-core-key"}}); err != nil {
		t.Fatal(err)
	}
	handler := New(cfg, db, collector.NewManager(cfg, db)).Handler()
	rr := testutil.Request(t, handler, "GET", negotiatedPath, "", testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	var got struct {
		Negotiated struct {
			Identity struct{ State string }
			Snapshot struct {
				InstanceID string `json:"instance_id"`
				Revision   uint64 `json:"snapshot_revision"`
				Complete   bool
				Resources  []struct {
					ResourceID        string `json:"resource_id"`
					CredentialVersion uint64 `json:"credential_version"`
					Manual            struct{ Disabled bool }
				}
			}
		}
	}
	testutil.DecodeJSON(t, rr, &got)
	n := got.Negotiated
	if n.Identity.State != "available" || !n.Snapshot.Complete || n.Snapshot.InstanceID != "11111111-1111-4111-8111-111111111111" || n.Snapshot.Revision != 7 || len(n.Snapshot.Resources) != 1 {
		t.Fatalf("missing complete bound snapshot: %s", rr.Body.String())
	}
	row := n.Snapshot.Resources[0]
	if row.ResourceID != "44444444-4444-4444-8444-444444444444" || row.CredentialVersion != 3 || !row.Manual.Disabled {
		t.Fatalf("lost disabled/version identity: %s", rr.Body.String())
	}
	for _, secret := range []string{"CANARY", "synthetic-core-key", upstream.URL} {
		if strings.Contains(rr.Body.String(), secret) {
			t.Fatal("secret leaked")
		}
	}
}

func TestResourceInventoryRejectsLateSnapshot(t *testing.T) {
	var capsReads atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v8/management/resources" {
			w.Write([]byte(testSnapshotJSON))
			return
		}
		caps := testCapsJSON
		if capsReads.Add(1) > 1 {
			caps = strings.Replace(caps, `"snapshot_revision":7`, `"snapshot_revision":8`, 1)
		}
		w.Write([]byte(caps))
	}))
	defer upstream.Close()
	cfg := testutil.NewConfig(t)
	db := testutil.NewStore(t, cfg)
	if err := db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: upstream.URL, ManagementKey: "synthetic-core-key"}}); err != nil {
		t.Fatal(err)
	}
	handler := New(cfg, db, collector.NewManager(cfg, db)).Handler()
	rr := testutil.Request(t, handler, "GET", negotiatedPath, "", testutil.AdminKey)
	var got struct {
		Negotiated struct {
			Identity struct{ State, Reason string }
			Snapshot any
		}
	}
	testutil.DecodeJSON(t, rr, &got)
	if got.Negotiated.Identity.State != "unknown" || got.Negotiated.Identity.Reason != "stale_inventory" || got.Negotiated.Snapshot != nil {
		t.Fatalf("late snapshot escaped revision fence: %s", rr.Body.String())
	}
}

func TestResourceInventoryConnectionChangeFencesCompletion(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v8/management/resources" {
			close(entered)
			<-release
			w.Write([]byte(testSnapshotJSON))
			return
		}
		w.Write([]byte(testCapsJSON))
	}))
	defer upstream.Close()
	next := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"usage-statistics-enabled":false}`)) }))
	defer next.Close()
	cfg := testutil.NewConfig(t)
	db := testutil.NewStore(t, cfg)
	if err := db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: upstream.URL, ManagementKey: "synthetic-core-key"}}); err != nil {
		t.Fatal(err)
	}
	handler := New(cfg, db, collector.NewManager(cfg, db)).Handler()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- testutil.Request(t, handler, "GET", negotiatedPath, "", testutil.AdminKey) }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		close(release)
		t.Fatal("inventory not entered")
	}
	body := `{"config":{"cpaConnection":{"cpaBaseUrl":"` + next.URL + `","managementKey":"synthetic-next-key"},"collector":{"enabled":false}}}`
	saved := testutil.Request(t, handler, "PUT", "/usage-service/config", body, testutil.AdminKey)
	close(release)
	testutil.RequireStatus(t, saved, 200)
	rr := <-done
	var got struct {
		Negotiated struct {
			Identity struct{ State, Reason string }
			Snapshot any
		}
	}
	testutil.DecodeJSON(t, rr, &got)
	if got.Negotiated.Identity.Reason != "connection_changed" || got.Negotiated.Snapshot != nil {
		t.Fatalf("old connection escaped: %s", rr.Body.String())
	}
}

func TestResourceInventoryRejectsUnsafeProjection(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"unproven row binding secret", strings.Replace(strings.Replace(strings.Replace(testSnapshotJSON, testTargetJSON, `"resource_id":"","incarnation":"","identity_version":0,"credential_version":0,"config_version":0,"membership_version":0`, 1), `"identity":"valid"`, `"identity":"ambiguous","source_bindings":[{"resource_id":"CANARY-KEY"}]`, 1), `"catalog_readiness":"ready"`, `"catalog_readiness":"not_ready"`, 1)},
		{"missing parent", strings.Replace(testSnapshotJSON, `"kind":"credential"`, `"parent_id":"66666666-6666-4666-8666-666666666666","kind":"credential"`, 1)},
		{"manual disabled omitted", strings.Replace(testSnapshotJSON, `"disabled":true,`, "", 1)},
		{"manual state null", strings.Replace(testSnapshotJSON, `"manual":{"disabled":true,"excluded":false,"zero_weight":false}`, `"manual":null`, 1)},
		{"opaque identity contains secret", strings.Replace(testSnapshotJSON, "44444444-4444-4444-8444-444444444444", "CANARY-KEY", 1)},
		{"family contains secret", strings.Replace(testSnapshotJSON, `"family":"codex"`, `"family":"CANARY-KEY"`, 1)},
		{"readiness contains secret", strings.Replace(testSnapshotJSON, `"catalog_readiness":"ready"`, `"catalog_readiness":"CANARY-KEY"`, 1)},
		{"missing version precondition", strings.Replace(testSnapshotJSON, `"credential_version":3`, `"credential_version":0`, 1)},
		{"unsafe numeric precision", strings.Replace(testSnapshotJSON, `"credential_version":3`, `"credential_version":9007199254740992`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v8/management/resources" {
					w.Write([]byte(tc.body))
				} else {
					w.Write([]byte(testCapsJSON))
				}
			}))
			defer upstream.Close()
			cfg := testutil.NewConfig(t)
			db := testutil.NewStore(t, cfg)
			if err := db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: upstream.URL, ManagementKey: "synthetic-core-key"}}); err != nil {
				t.Fatal(err)
			}
			handler := New(cfg, db, collector.NewManager(cfg, db)).Handler()
			rr := testutil.Request(t, handler, "GET", negotiatedPath, "", testutil.AdminKey)
			var got struct {
				Negotiated struct {
					Identity struct{ State, Reason string }
					Snapshot any
				}
			}
			testutil.DecodeJSON(t, rr, &got)
			if got.Negotiated.Identity.Reason != "invalid_contract" || got.Negotiated.Snapshot != nil || strings.Contains(rr.Body.String(), "CANARY") {
				t.Fatalf("unsafe projection escaped allowlist: %s", rr.Body.String())
			}
		})
	}
}

func TestResourceInventoryPreservesPluginSourceBindings(t *testing.T) {
	target := `"resource_id":"66666666-6666-4666-8666-666666666666","incarnation":"77777777-7777-4777-8777-777777777777","identity_version":2,"credential_version":3,"config_version":4,"membership_version":0`
	var snapshot map[string]any
	if err := json.Unmarshal([]byte(testSnapshotJSON), &snapshot); err != nil {
		t.Fatal(err)
	}
	rows := snapshot["resources"].([]any)
	first := rows[0].(map[string]any)
	first["family"] = "opencode-go"
	first["binding_state"] = "verified_projection"
	var binding map[string]any
	if err := json.Unmarshal([]byte("{"+target+"}"), &binding); err != nil {
		t.Fatal(err)
	}
	first["source_bindings"] = []any{binding}
	second := map[string]any{}
	for key, val := range first {
		second[key] = val
	}
	for key, val := range binding {
		second[key] = val
	}
	delete(second, "source_bindings")
	snapshot["resources"] = []any{first, second}
	body, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v8/management/resources" {
			w.Write(body)
		} else {
			w.Write([]byte(testCapsJSON))
		}
	}))
	defer upstream.Close()
	cfg := testutil.NewConfig(t)
	db := testutil.NewStore(t, cfg)
	if err := db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: upstream.URL, ManagementKey: "synthetic-core-key"}}); err != nil {
		t.Fatal(err)
	}
	rr := testutil.Request(t, New(cfg, db, collector.NewManager(cfg, db)).Handler(), "GET", negotiatedPath, "", testutil.AdminKey)
	var got struct {
		Negotiated struct {
			Identity struct{ State string }
			Snapshot struct {
				Resources []struct {
					Family       string
					BindingState string `json:"binding_state"`
					Bindings     []struct {
						ResourceID        string `json:"resource_id"`
						CredentialVersion uint64 `json:"credential_version"`
					} `json:"source_bindings"`
				}
			}
		}
	}
	testutil.DecodeJSON(t, rr, &got)
	if got.Negotiated.Identity.State != "available" || len(got.Negotiated.Snapshot.Resources) != 2 {
		t.Fatalf("plugin inventory dropped: %s", rr.Body.String())
	}
	row := got.Negotiated.Snapshot.Resources[0]
	if row.Family != "opencode-go" || row.BindingState != "verified_projection" || len(row.Bindings) != 1 || row.Bindings[0].ResourceID != "66666666-6666-4666-8666-666666666666" || row.Bindings[0].CredentialVersion != 3 {
		t.Fatalf("plugin binding lost: %s", rr.Body.String())
	}
}

func TestResourceInventoryNegotiationAuthorizationAndMethods(t *testing.T) {
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.Write([]byte(testCapsJSON)) }))
	defer upstream.Close()
	cfg := testutil.NewConfig(t)
	db := testutil.NewStore(t, cfg)
	if err := db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: upstream.URL, ManagementKey: "synthetic-core-key"}}); err != nil {
		t.Fatal(err)
	}
	handler := New(cfg, db, collector.NewManager(cfg, db)).Handler()
	for _, tc := range []struct {
		method, key string
		want        int
	}{{"GET", "", 401}, {"GET", "wrong-key", 401}, {"OPTIONS", "", 204}, {"POST", testutil.AdminKey, 405}, {"PUT", testutil.AdminKey, 405}, {"PATCH", testutil.AdminKey, 405}, {"DELETE", testutil.AdminKey, 405}, {"HEAD", testutil.AdminKey, 405}} {
		rr := testutil.Request(t, handler, tc.method, negotiatedPath, `{"enabled":true}`, tc.key)
		testutil.RequireStatus(t, rr, tc.want)
		if rr.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("cacheable capability")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("unauthorized/method/preflight read reached Core")
	}
}

func TestResourceInventoryTransportFailuresRemainSecretFree(t *testing.T) {
	var redirected atomic.Int64
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected.Add(1)
		t.Error("redirect followed with management key")
	}))
	defer sink.Close()
	for _, tc := range []struct {
		name         string
		status       int
		body, reason string
	}{
		{"unauthorized", 401, "CANARY-SECRET", "core_auth_error"},
		{"forbidden", 403, "CANARY-SECRET", "core_auth_error"},
		{"legacy", 404, "CANARY-SECRET", "unsupported_contract"},
		{"failed", 503, "CANARY-SECRET", "core_unavailable"},
		{"redirect", 302, "CANARY-SECRET", "core_unavailable"},
		{"bad JSON", 200, "CANARY-SECRET", "invalid_contract"},
		{"trailing JSON", 200, testCapsJSON + `{"key":"CANARY-SECRET"}`, "invalid_contract"},
		{"oversized", 200, testCapsJSON + strings.Repeat(" ", 2<<20), "invalid_contract"},
		{"future", 200, strings.Replace(testCapsJSON, `"version":1`, `"version":2`, 1), "unsupported_contract"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", sink.URL)
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}))
			defer upstream.Close()
			cfg := testutil.NewConfig(t)
			db := testutil.NewStore(t, cfg)
			if err := db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: upstream.URL, ManagementKey: "synthetic-core-key"}}); err != nil {
				t.Fatal(err)
			}
			rr := testutil.Request(t, New(cfg, db, collector.NewManager(cfg, db)).Handler(), "GET", negotiatedPath, "", testutil.AdminKey)
			testutil.RequireStatus(t, rr, 200)
			var got struct {
				Negotiated struct {
					Identity struct{ State, Reason string }
					Snapshot any
				}
			}
			testutil.DecodeJSON(t, rr, &got)
			if got.Negotiated.Identity.Reason != tc.reason || got.Negotiated.Identity.State != "unknown" || got.Negotiated.Snapshot != nil {
				t.Fatalf("unexpected support result: %s", rr.Body.String())
			}
			if strings.Contains(rr.Body.String(), "CANARY") || strings.Contains(rr.Body.String(), "synthetic-core-key") || strings.Contains(rr.Body.String(), upstream.URL) {
				t.Fatal("secret leaked")
			}
		})
	}
	if redirected.Load() != 0 {
		t.Fatal("redirect reached sink")
	}
}

func TestResourceInventoryLifecycleProjectionHasNoLocalIdentityCache(t *testing.T) {
	current := testSnapshotJSON
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v8/management/resources" {
			w.Write([]byte(current))
			return
		}
		var snap map[string]any
		if err := json.Unmarshal([]byte(current), &snap); err != nil {
			t.Error(err)
			return
		}
		scope := map[string]any{}
		for _, key := range []string{"instance_id", "instance_incarnation", "boot_epoch", "snapshot_revision"} {
			scope[key] = snap[key]
		}
		json.NewEncoder(w).Encode(map[string]any{"version": 1, "inventory": true, "durable": true, "query": false, "gate": false, "policy_locked": true, "reason": "available", "scope": scope})
	}))
	defer upstream.Close()
	cfg := testutil.NewConfig(t)
	db := testutil.NewStore(t, cfg)
	if err := db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: upstream.URL, ManagementKey: "synthetic-core-key"}}); err != nil {
		t.Fatal(err)
	}
	handler := New(cfg, db, collector.NewManager(cfg, db)).Handler()
	for _, tc := range []struct {
		name, body, instance, id string
		credential               uint64
	}{
		{"original", testSnapshotJSON, "11111111-1111-4111-8111-111111111111", "44444444-4444-4444-8444-444444444444", 3},
		{"rename", strings.Replace(testSnapshotJSON, "CANARY-NAME", "RENAMED", 1), "11111111-1111-4111-8111-111111111111", "44444444-4444-4444-8444-444444444444", 3},
		{"key change", strings.Replace(testSnapshotJSON, `"credential_version":3`, `"credential_version":4`, 1), "11111111-1111-4111-8111-111111111111", "44444444-4444-4444-8444-444444444444", 4},
		{"delete recreate", strings.ReplaceAll(testSnapshotJSON, "44444444-4444-4444-8444-444444444444", "66666666-6666-4666-8666-666666666666"), "11111111-1111-4111-8111-111111111111", "66666666-6666-4666-8666-666666666666", 3},
		{"another instance", strings.ReplaceAll(testSnapshotJSON, "11111111-1111-4111-8111-111111111111", "77777777-7777-4777-8777-777777777777"), "77777777-7777-4777-8777-777777777777", "44444444-4444-4444-8444-444444444444", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current = tc.body
			rr := testutil.Request(t, handler, "GET", negotiatedPath, "", testutil.AdminKey)
			var got struct {
				Negotiated struct {
					Snapshot struct {
						Instance  string `json:"instance_id"`
						Resources []struct {
							ID      string `json:"resource_id"`
							Version uint64 `json:"credential_version"`
						}
					}
				}
			}
			testutil.DecodeJSON(t, rr, &got)
			if got.Negotiated.Snapshot.Instance != tc.instance || len(got.Negotiated.Snapshot.Resources) != 1 || got.Negotiated.Snapshot.Resources[0].ID != tc.id || got.Negotiated.Snapshot.Resources[0].Version != tc.credential {
				t.Fatalf("identity contaminated: %s", rr.Body.String())
			}
		})
	}
}

func TestResourceInventoryReorderAndAmbiguousRowsDoNotAcquireEligibility(t *testing.T) {
	var snapshot map[string]any
	if err := json.Unmarshal([]byte(testSnapshotJSON), &snapshot); err != nil {
		t.Fatal(err)
	}
	first := snapshot["resources"].([]any)[0].(map[string]any)
	second := map[string]any{}
	for key, val := range first {
		second[key] = val
	}
	second["resource_id"] = "66666666-6666-4666-8666-666666666666"
	second["incarnation"] = "77777777-7777-4777-8777-777777777777"
	second["identity"] = "ambiguous"
	second["catalog_readiness"] = "not_ready"
	var ordered atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v8/management/resources" {
			w.Write([]byte(testCapsJSON))
			return
		}
		body := map[string]any{}
		for key, val := range snapshot {
			body[key] = val
		}
		if ordered.Load() {
			body["resources"] = []any{second, first}
		} else {
			body["resources"] = []any{first, second}
		}
		json.NewEncoder(w).Encode(body)
	}))
	defer upstream.Close()
	cfg := testutil.NewConfig(t)
	db := testutil.NewStore(t, cfg)
	if err := db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: upstream.URL, ManagementKey: "synthetic-core-key"}}); err != nil {
		t.Fatal(err)
	}
	handler := New(cfg, db, collector.NewManager(cfg, db)).Handler()
	for _, reverse := range []bool{false, true} {
		ordered.Store(reverse)
		rr := testutil.Request(t, handler, "GET", negotiatedPath, "", testutil.AdminKey)
		var got struct {
			Negotiated struct {
				IndependentGate struct{ State string }
				Snapshot        struct {
					Resources []struct {
						ID                string `json:"resource_id"`
						Identity          string
						Readiness         string `json:"catalog_readiness"`
						CredentialVersion uint64 `json:"credential_version"`
						Capabilities      struct{ Gate bool }
					}
				}
			}
		}
		testutil.DecodeJSON(t, rr, &got)
		if len(got.Negotiated.Snapshot.Resources) != 2 || got.Negotiated.IndependentGate.State != "unavailable" {
			t.Fatalf("missing conservative inventory: %s", rr.Body.String())
		}
		for _, row := range got.Negotiated.Snapshot.Resources {
			if row.ID == "44444444-4444-4444-8444-444444444444" && (row.Identity != "valid" || row.CredentialVersion != 3) {
				t.Fatal("order changed identity")
			}
			if row.ID == "66666666-6666-4666-8666-666666666666" && (row.Identity != "ambiguous" || row.Readiness != "not_ready" || row.Capabilities.Gate) {
				t.Fatal("ambiguous row promoted")
			}
		}
	}
}

func TestResourceInventoryConsumesHostIssuedRuntimeBinding(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v8/management/resource-capabilities":
			w.Write([]byte(testCapsJSON))
		case "/v8/management/resources":
			w.Write([]byte(testSnapshotJSON))
		case "/v8/management/resource-bindings":
			w.Write([]byte(`{"bindings":[{"auth_index":"0123456789abcdef","runtime_alias":"44444444-4444-4444-8444-444444444444","scope":{` + testScopeJSON + `},"targets":[{` + testTargetJSON + `}],"name":"CANARY-SECRET"}]}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer upstream.Close()
	cfg := testutil.NewConfig(t)
	db := testutil.NewStore(t, cfg)
	if err := db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: upstream.URL, ManagementKey: "synthetic-core-key"}}); err != nil {
		t.Fatal(err)
	}
	rr := testutil.Request(t, New(cfg, db, collector.NewManager(cfg, db)).Handler(), "GET", negotiatedPath+"&bindings=1", "", testutil.AdminKey)
	var got struct {
		Negotiated struct {
			BindingCapability struct{ State string }
			Bindings          []struct {
				Index   string `json:"auth_index"`
				Alias   string `json:"runtime_alias"`
				Targets []struct {
					ID      string `json:"resource_id"`
					Version uint64 `json:"credential_version"`
				}
			}
		}
	}
	testutil.DecodeJSON(t, rr, &got)
	if got.Negotiated.BindingCapability.State != "available" || len(got.Negotiated.Bindings) != 1 {
		t.Fatalf("host binding missing: %s", rr.Body.String())
	}
	binding := got.Negotiated.Bindings[0]
	if binding.Index != "0123456789abcdef" || binding.Alias != "44444444-4444-4444-8444-444444444444" || len(binding.Targets) != 1 || binding.Targets[0].ID != binding.Alias || binding.Targets[0].Version != 3 || strings.Contains(rr.Body.String(), "CANARY") {
		t.Fatalf("wrong/unsafe host association: %s", rr.Body.String())
	}
}

func TestResourceInventoryRejectsStaleOrUnprovenRuntimeBindings(t *testing.T) {
	valid := `{"bindings":[{"auth_index":"0123456789abcdef","runtime_alias":"44444444-4444-4444-8444-444444444444","scope":{` + testScopeJSON + `},"targets":[{` + testTargetJSON + `}]}]}`
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"stale boot", strings.Replace(valid, "33333333-3333-4333-8333-333333333333", "77777777-7777-4777-8777-777777777777", 1), 200},
		{"stale credential version", strings.Replace(valid, `"credential_version":3`, `"credential_version":4`, 1), 200},
		{"unproven alias", strings.Replace(valid, `"runtime_alias":"44444444-4444-4444-8444-444444444444"`, `"runtime_alias":"66666666-6666-4666-8666-666666666666"`, 1), 200},
		{"secret selector", strings.Replace(valid, "0123456789abcdef", "CANARY-SECRET", 1), 200},
		{"missing targets", strings.Replace(valid, `[{`+testTargetJSON+`}]`, `[]`, 1), 200},
		{"bad contract", "CANARY-SECRET", 200},
		{"old producer", "CANARY-SECRET", 404},
		{"not ready", "CANARY-SECRET", 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v8/management/resource-capabilities":
					w.Write([]byte(testCapsJSON))
				case "/v8/management/resources":
					w.Write([]byte(testSnapshotJSON))
				case "/v8/management/resource-bindings":
					w.WriteHeader(tc.status)
					w.Write([]byte(tc.body))
				default:
					t.Error("unsafe endpoint")
				}
			}))
			defer upstream.Close()
			cfg := testutil.NewConfig(t)
			db := testutil.NewStore(t, cfg)
			if err := db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: upstream.URL, ManagementKey: "synthetic-core-key"}}); err != nil {
				t.Fatal(err)
			}
			rr := testutil.Request(t, New(cfg, db, collector.NewManager(cfg, db)).Handler(), "GET", negotiatedPath+"&bindings=1", "", testutil.AdminKey)
			var got struct {
				Negotiated struct {
					BindingCapability struct{ State string }
					Bindings          []any
				}
			}
			testutil.DecodeJSON(t, rr, &got)
			if got.Negotiated.BindingCapability.State != "unavailable" || len(got.Negotiated.Bindings) != 0 || strings.Contains(rr.Body.String(), "CANARY") {
				t.Fatalf("unproven binding escaped: %s", rr.Body.String())
			}
		})
	}
}
