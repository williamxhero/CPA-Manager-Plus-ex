package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

func TestAntigravityQuotaSourceAvailableOnlyForMatchingFamily(t *testing.T) {
	for _, family := range []string{"antigravity", "codex"} {
		t.Run(family, func(t *testing.T) {
			core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Error("inventory queried upstream")
					w.WriteHeader(405)
					return
				}
				switch r.URL.Path {
				case "/v8/management/resource-capabilities":
					_, _ = w.Write([]byte(sourceViewCaps("true")))
				case "/v8/management/resources":
					_, _ = w.Write([]byte(strings.Replace(testSnapshotJSON, `"family":"codex"`, `"family":"`+family+`"`, 1)))
				case "/v8/management/credential-quota/sources":
					body := strings.ReplaceAll(sourceViewEnvelope, "codex-oauth-wham", "antigravity-oauth-quota-summary")
					body = strings.ReplaceAll(body, "selected_oauth_account", "selected_oauth_project")
					_, _ = w.Write([]byte(body))
				default:
					t.Errorf("unexpected operation: %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer core.Close()
			cfg := testutil.NewConfig(t)
			db := testutil.NewStore(t, cfg)
			if err := db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: core.URL, ManagementKey: "test-key"}}); err != nil {
				t.Fatal(err)
			}
			server := New(cfg, db, collector.NewManager(cfg, db))
			response := testutil.Request(t, server.Handler(), "GET", "/usage-service/credential-quota?authIndex=0123456789abcdef", "", testutil.AdminKey)
			if family == "antigravity" {
				if response.Code != 200 || !strings.Contains(response.Body.String(), `"protocol":"antigravity-oauth-quota-summary"`) || !strings.Contains(response.Body.String(), `"state":"ready"`) {
					t.Fatalf("Antigravity source unavailable: HTTP%d %s", response.Code, response.Body.String())
				}
				refresh := testutil.Request(t, server.Handler(), "GET", "/usage-service/full-refresh", "", testutil.AdminKey)
				if refresh.Code != 200 || !strings.Contains(refresh.Body.String(), `"queryCapable":true`) {
					t.Fatalf("Antigravity common refresh unavailable: HTTP%d %s", refresh.Code, refresh.Body.String())
				}
			} else if response.Code != 409 {
				t.Fatal("Antigravity source accepted under wrong resource family")
			}
		})
	}
}
