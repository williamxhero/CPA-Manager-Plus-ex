package httpapi

import (
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
	"testing"
)

func TestFullRefreshUnavailableInventoryIsNotEmptySuccess(t *testing.T) {
	cfg := testutil.NewConfig(t)
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	testutil.EnsureAdminCredential(t, db)
	s := New(cfg, db, collector.NewManager(cfg, db))
	h := s.Handler()
	testutil.RequireStatus(t, testutil.Request(t, h, "POST", "/usage-service/full-refresh", `{"purpose":"manual"}`, ""), 401)
	testutil.RequireStatus(t, testutil.Request(t, h, "POST", "/usage-service/full-refresh", `{"purpose":"manual"}`, testutil.AdminKey), 503)
}
