package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/managementquery"
	mc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/managerconfig"
	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

// Only external future/old protocol envelopes, never a synthetic native mode.
// Exercise public proxy admission with real saved connection and shared budget.
func TestLegacyGuardTargetProxyRejectsFutureCapsButPreservesOld404(t *testing.T) {
	var mode, dispatched atomic.Int64
	mode.Store(200)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic" {
			t.Error("missing saved management authentication")
		}
		switch r.URL.Path {
		case "/v8/management/resource-capabilities":
			w.WriteHeader(int(mode.Load()))
			w.Write([]byte(`{"version":2}`))
		case "/v0/management/quota":
			dispatched.Add(1)
			w.Write([]byte(`{"legacy":true}`))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(400)
		}
	}))
	defer upstream.Close()
	cfg := testutil.NewConfig(t)
	db := testutil.NewStore(t, cfg)
	if err := db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: upstream.URL, ManagementKey: "synthetic"}}); err != nil {
		t.Fatal(err)
	}
	connection := mc.New(cfg, db, nil)
	service := NewWithMutationCoordinator(connection, nil, db)
	service.LegacyQuotaGuard = &managementquery.LegacyGuard{Budget: managementquery.NewBudget(db), Identity: rc.New(nil, connection)}
	request := func() *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		service.ProxyManagement(rr, httptest.NewRequest("GET", "/v0/management/quota", nil), func(w http.ResponseWriter, status int, err error) { http.Error(w, err.Error(), status) })
		return rr
	}
	rr := request()
	if rr.Code < 400 || dispatched.Load() != 0 {
		t.Fatalf("HTTP200 version2 capabilities fell back to legacy: proxyHTTP=%d legacyDispatches=%d", rr.Code, dispatched.Load())
	}
	mode.Store(404)
	rr = request()
	if rr.Code != 200 || dispatched.Load() != 1 {
		t.Fatalf("explicit old404 lost legacy functionality: proxyHTTP=%d legacyDispatches=%d body=%s", rr.Code, dispatched.Load(), rr.Body.String())
	}
}
