package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

const sourceViewBinding = `{"auth_index":"0123456789abcdef","runtime_alias":"44444444-4444-4444-8444-444444444444","scope":{` + testScopeJSON + `},"targets":[{` + testTargetJSON + `}]}`
const sourceViewSource = `{"scope":{` + testScopeJSON + `},"targets":[{` + testTargetJSON + `}],"auth_index":"0123456789abcdef","sourceVersion":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","protocol":"codex-oauth-wham","state":"ready","reason":"selected_oauth","accountProvenance":"selected_oauth_account","manualDisabled":true}`
const sourceViewEnvelope = `{"version":1,"sources":[` + sourceViewSource + `],"bindings":[` + sourceViewBinding + `]}`

// These are literal frozen READ-contract cases over authenticated HTTP + real
// SQLite, not real-producer availability/gate/ON/route proof. Actual new producer
// tests remain pending its independently verified immutable environment.
func TestQuotaSourceBindingsViewNegotiatesOneReadAndPreservesOldPath(t *testing.T) {
	for _, flag := range []string{"true", "false", "absent"} {
		t.Run(flag, func(t *testing.T) {
			caps := sourceViewCaps(flag)
			var mu sync.Mutex
			var paths []string
			core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer sourceview-contract-key" {
					t.Error("not authenticated read")
				}
				mu.Lock()
				paths = append(paths, r.URL.Path)
				mu.Unlock()
				switch r.URL.Path {
				case "/v8/management/resource-capabilities":
					w.Write([]byte(caps))
				case "/v8/management/resources":
					w.Write([]byte(testSnapshotJSON))
				case "/v8/management/resource-bindings":
					w.Write([]byte(`{"bindings":[` + sourceViewBinding + `]}`))
				case "/v8/management/credential-quota/sources":
					view := sourceViewEnvelope
					if flag != "true" {
						// An unadvertised extension cannot replace the independently
						// validated old bindings read, even if it is malformed.
						view = `{"version":1,"sources":[` + sourceViewSource + `],"bindings":null}`
					}
					w.Write([]byte(view))
				default:
					t.Error("unexpected management operation")
					w.WriteHeader(404)
				}
			}))
			t.Cleanup(core.Close)
			cfg := testutil.NewConfig(t)
			db := testutil.NewStore(t, cfg)
			if err := db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: core.URL, ManagementKey: "sourceview-contract-key"}}); err != nil {
				t.Fatal(err)
			}
			server := New(cfg, db, collector.NewManager(cfg, db))
			rr := testutil.Request(t, server.Handler(), "GET", "/usage-service/credential-quota", "", testutil.AdminKey)
			if rr.Code != 200 {
				t.Fatalf("read contract HTTP%d: %s", rr.Code, rr.Body.String())
			}
			mu.Lock()
			defer mu.Unlock()
			want := []string{"/v8/management/resource-capabilities", "/v8/management/resources", "/v8/management/credential-quota/sources", "/v8/management/resource-capabilities"}
			if flag != "true" {
				want = []string{"/v8/management/resource-capabilities", "/v8/management/resources", "/v8/management/resource-bindings", "/v8/management/resource-capabilities", "/v8/management/credential-quota/sources"}
			}
			if strings.Join(paths, "|") != strings.Join(want, "|") {
				t.Fatalf("authenticated read sequence=%v want=%v", paths, want)
			}
		})
	}
}

func sourceViewCaps(flag string) string {
	optional := ""
	if flag != "absent" {
		optional = `,"source_bindings":` + flag
	}
	return strings.TrimSuffix(testCapsJSON, "}") + `,"credential_query":{"version":1,"sources":true,"scoped_query":true` + optional + `}}`
}

func TestQuotaSourceBindingsAdvertisedBadViewCannotRepairWithLegacyRead(t *testing.T) {
	for _, tc := range []struct{ name, view string }{
		{"bindings-missing", `{"version":1,"sources":[` + sourceViewSource + `]}`},
		{"bindings-null", `{"version":1,"sources":[` + sourceViewSource + `],"bindings":null}`},
		{"bindings-malformed", `{"version":1,"sources":[` + sourceViewSource + `],"bindings":{}}`},
		{"sources-null", `{"version":1,"sources":null,"bindings":[` + sourceViewBinding + `]}`},
		{"catalog-closure", `{"version":1,"sources":[],"bindings":[]}`},
		{"binding-version", strings.Replace(sourceViewEnvelope, sourceViewBinding, strings.Replace(sourceViewBinding, `"credential_version":3`, `"credential_version":4`, 1), 1)},
		{"binding-scope", strings.Replace(sourceViewEnvelope, sourceViewBinding, strings.Replace(sourceViewBinding, `"snapshot_revision":7`, `"snapshot_revision":8`, 1), 1)},
		{"binding-alias", strings.Replace(sourceViewEnvelope, `"runtime_alias":"44444444-4444-4444-8444-444444444444"`, `"runtime_alias":"66666666-6666-4666-8666-666666666666"`, 1)},
		{"binding-source-mismatch", strings.Replace(sourceViewEnvelope, sourceViewBinding, strings.Replace(sourceViewBinding, "0123456789abcdef", "fedcba9876543210", 1), 1)},
		{"binding-count-closure", `{"version":1,"sources":[` + sourceViewSource + `],"bindings":[]}`},
		{"source-headers", strings.Replace(sourceViewEnvelope, `"sourceVersion":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`, `"sourceVersion":"bad"`, 1)},
		{"source-ready-protocol", strings.Replace(sourceViewEnvelope, "codex-oauth-wham", "unverified-future", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, paths := sourceViewContractServer(t, sourceViewContractCase{view: func(int) string { return tc.view }})
			rr := testutil.Request(t, server.Handler(), "GET", "/usage-service/credential-quota", "", testutil.AdminKey)
			var rejected struct {
				Error string `json:"error"`
			}
			if rr.Code != 409 || json.Unmarshal(rr.Body.Bytes(), &rejected) != nil || rejected.Error != "identity_changed" {
				t.Fatalf("bad advertised view not fenced: HTTP%d %s", rr.Code, rr.Body.String())
			}
			for _, path := range paths() {
				if path == "/v8/management/resource-bindings" {
					t.Fatal("bad advertised view repaired by legacy read")
				}
			}
		})
	}
}

func TestQuotaSourceBindingsViewAdvertisementRequiresExactQueryContract(t *testing.T) {
	for _, malformed := range []string{
		strings.Replace(sourceViewCaps("true"), `"credential_query":{"version":1`, `"credential_query":{"version":2`, 1),
		strings.Replace(sourceViewCaps("true"), `"sources":true`, `"sources":false`, 1),
		strings.Replace(sourceViewCaps("true"), `"scoped_query":true`, `"scoped_query":false`, 1),
	} {
		server, paths := sourceViewContractServer(t, sourceViewContractCase{caps: func(int) string { return malformed }})
		observed := server.AppContext().ResourceCapabilityService.Inventory(context.Background(), true)
		if observed.CredentialQuery.State == "available" || len(observed.CredentialQuotaSourceProjection()) != 0 {
			t.Fatal("contradictory advertisement acquired source authority")
		}
		for _, path := range paths() {
			if path == "/v8/management/resource-bindings" || path == "/v8/management/credential-quota/sources" {
				t.Fatal("invalid advertised contract attempted repair/query")
			}
		}
	}
}

func TestQuotaSourceBindingsViewClosingCapabilityAndConnectionFences(t *testing.T) {
	for _, kind := range []string{"capability-flag", "capability-version", "capability-scope", "connection-CAS"} {
		t.Run(kind, func(t *testing.T) {
			options := sourceViewContractCase{caps: func(n int) string {
				caps := sourceViewCaps("true")
				if n == 2 {
					switch kind {
					case "capability-flag":
						caps = sourceViewCaps("false")
					case "capability-version":
						caps = strings.Replace(caps, `"credential_query":{"version":1`, `"credential_query":{"version":2`, 1)
					case "capability-scope":
						caps = strings.Replace(caps, `"snapshot_revision":7`, `"snapshot_revision":8`, 1)
					}
				}
				return caps
			}}
			if kind == "connection-CAS" {
				options.onView = func(db *store.Store) {
					if err := db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: "http://127.0.0.1:1", ManagementKey: "different-contract-key"}}); err != nil {
						t.Error(err)
					}
				}
			}
			server, paths := sourceViewContractServer(t, options)
			observed := server.AppContext().ResourceCapabilityService.Inventory(context.Background(), true)
			if observed.CredentialQuery.State == "available" || observed.Identity.State == "available" || len(observed.CredentialQuotaSourceProjection()) != 0 {
				t.Fatalf("late view survived changed authority: %+v", observed)
			}
			want := "/v8/management/resource-capabilities|/v8/management/resources|/v8/management/credential-quota/sources|/v8/management/resource-capabilities"
			if strings.Join(paths(), "|") != want {
				t.Fatalf("closing check/repair behavior changed: %v", paths())
			}
		})
	}
}

func TestQuotaSourceBindingsViewRequiresSameSourceBindingJoin(t *testing.T) {
	for _, view := range []string{
		strings.Replace(sourceViewEnvelope, sourceViewBinding, strings.Replace(sourceViewBinding, "0123456789abcdef", "fedcba9876543210", 1), 1),
		`{"version":1,"sources":[` + sourceViewSource + `],"bindings":[]}`,
	} {
		server, _ := sourceViewContractServer(t, sourceViewContractCase{view: func(int) string { return view }})
		observed := server.AppContext().ResourceCapabilityService.Inventory(context.Background(), true)
		if observed.CredentialQuery.State == "available" || len(observed.CredentialQuotaSourceProjection()) != 0 {
			t.Fatal("individually valid but unrelated source/binding projections advertised as one coherent view")
		}
	}
}

func TestQuotaSourceBindingsViewProjectionIsPrivateCopyAndNotCached(t *testing.T) {
	server, paths := sourceViewContractServer(t, sourceViewContractCase{view: func(n int) string {
		if n == 1 {
			return strings.TrimSuffix(sourceViewEnvelope, "}") + `,"secret":"SOURCEVIEW-CANARY"}`
		}
		return `{"version":1,"sources":[` + sourceViewSource + `]}`
	}})
	identity := server.AppContext().ResourceCapabilityService
	first := identity.Inventory(context.Background(), true)
	if first.CredentialQuery.State != "available" || first.BindingCapability == nil || first.BindingCapability.State != "available" {
		t.Fatal("literal read contract unavailable")
	}
	original := first.CredentialQuotaSourceProjection()
	copy := first.CredentialQuotaSourceProjection()
	copy[0] = '!'
	if !bytes.Equal(original, first.CredentialQuotaSourceProjection()) {
		t.Fatal("copy changed retained request evidence")
	}
	serialized, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"CANARY", "sourceview-contract-key", `"sourceVersion"`} {
		if bytes.Contains(serialized, []byte(secret)) {
			t.Fatal("raw private envelope leaked into public inventory")
		}
	}
	second := identity.Inventory(context.Background(), false)
	if second.CredentialQuery.State == "available" || len(second.CredentialQuotaSourceProjection()) != 0 || !bytes.Equal(original, first.CredentialQuotaSourceProjection()) {
		t.Fatal("bad next request reused/mutated old evidence")
	}
	for _, path := range paths() {
		if path == "/v8/management/resource-bindings" {
			t.Fatal("unexpected legacy repair/cache path")
		}
	}
}

type sourceViewContractCase struct {
	caps   func(int) string
	view   func(int) string
	onView func(*store.Store)
}

func sourceViewContractServer(t *testing.T, options sourceViewContractCase) (*Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var paths []string
	capCalls, viewCalls := 0, 0
	var db *store.Store
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer sourceview-contract-key" {
			t.Error("not authenticated read")
		}
		mu.Lock()
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/v8/management/resource-capabilities" {
			capCalls++
		}
		if r.URL.Path == "/v8/management/credential-quota/sources" {
			viewCalls++
		}
		capN, viewN := capCalls, viewCalls
		mu.Unlock()
		switch r.URL.Path {
		case "/v8/management/resource-capabilities":
			body := sourceViewCaps("true")
			if options.caps != nil {
				body = options.caps(capN)
			}
			w.Write([]byte(body))
		case "/v8/management/resources":
			w.Write([]byte(testSnapshotJSON))
		case "/v8/management/credential-quota/sources":
			if options.onView != nil {
				options.onView(db)
			}
			body := sourceViewEnvelope
			if options.view != nil {
				body = options.view(viewN)
			}
			w.Write([]byte(body))
		case "/v8/management/resource-bindings":
			w.Write([]byte(`{"bindings":[` + sourceViewBinding + `]}`))
		default:
			t.Error("unsafe management operation")
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(core.Close)
	cfg := testutil.NewConfig(t)
	db = testutil.NewStore(t, cfg)
	if err := db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: core.URL, ManagementKey: "sourceview-contract-key"}}); err != nil {
		t.Fatal(err)
	}
	return New(cfg, db, collector.NewManager(cfg, db)), func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), paths...) }
}
