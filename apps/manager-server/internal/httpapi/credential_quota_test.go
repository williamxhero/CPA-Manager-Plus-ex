package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

// Compatibility negatives intentionally simulate ONLY an external old/malformed
// producer. New query availability is accepted by the separate actual-Core test.
func TestCredentialQuotaOldAndMalformedContractsRemainUnknown(t *testing.T) {
	for _, tc := range []struct {
		name, capability, source string
		cap404                   bool
	}{
		{name: "old-core-404", cap404: true},
		{name: "legacy-inventory-no-query-capability"},
		{name: "unversioned-capability", capability: `{"sources":true,"scoped_query":true}`},
		{name: "unknown-capability-version", capability: `{"version":2,"sources":true,"scoped_query":true}`},
		{name: "new-capability-sources-404", capability: `{"version":1,"sources":true,"scoped_query":true}`},
		{name: "unversioned-sources", capability: `{"version":1,"sources":true,"scoped_query":true}`, source: `{"sources":[]}`},
		{name: "malformed-sources", capability: `{"version":1,"sources":true,"scoped_query":true}`, source: `{"version":1,"sources":[{}]}`},
		{name: "empty-sources-drop-current-credentials", capability: `{"version":1,"sources":true,"scoped_query":true}`, source: `{"version":1,"sources":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Error("read invoked query")
				}
				switch r.URL.Path {
				case "/v8/management/resource-capabilities":
					if tc.cap404 {
						w.WriteHeader(404)
						return
					}
					caps := testCapsJSON
					if tc.capability != "" {
						caps = strings.TrimSuffix(caps, "}") + `,"credential_query":` + tc.capability + `}`
					}
					w.Write([]byte(caps))
				case "/v8/management/resources":
					w.Write([]byte(testSnapshotJSON))
				case "/v8/management/resource-bindings":
					w.Write([]byte(`{"bindings":[]}`))
				case "/v8/management/credential-quota/sources":
					if tc.source == "" {
						w.WriteHeader(404)
					} else {
						w.Write([]byte(tc.source))
					}
				default:
					t.Error("unexpected/unsafe request path")
					w.WriteHeader(404)
				}
			}))
			defer upstream.Close()
			cfg := testutil.NewConfig(t)
			db := testutil.NewStore(t, cfg)
			testutil.EnsureAdminCredential(t, db)
			if err := db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: upstream.URL, ManagementKey: "synthetic-core-key"}}); err != nil {
				t.Fatal(err)
			}
			handler := New(cfg, db, collector.NewManager(cfg, db)).Handler()
			rr := testutil.Request(t, handler, "GET", negotiatedPath, "", testutil.AdminKey)
			testutil.RequireStatus(t, rr, 200)
			var response struct {
				Negotiated struct {
					CredentialQuery struct {
						State string `json:"state"`
					} `json:"credentialQuery"`
				} `json:"negotiated"`
			}
			if json.Unmarshal(rr.Body.Bytes(), &response) != nil || response.Negotiated.CredentialQuery.State != "unknown" {
				t.Fatalf("old/malformed Core claimed query available: %s", rr.Body.String())
			}
			rr = testutil.Request(t, handler, "GET", "/usage-service/credential-quota", "", testutil.AdminKey)
			if rr.Code == 200 {
				t.Fatal("credential read silently enabled unknown query contract")
			}
		})
	}
}

func TestCredentialQuotaRequiresPanelAuthorization(t *testing.T) {
	cfg := testutil.NewConfig(t)
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	testutil.EnsureAdminCredential(t, db)
	s := New(cfg, db, collector.NewManager(cfg, db))
	for _, method := range []string{"GET", "POST"} {
		req := httptest.NewRequest(method, "/usage-service/credential-quota", strings.NewReader(`{}`))
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s: got %d want 401", method, w.Code)
		}
	}
}
