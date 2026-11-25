package providerautostate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	managerconfigsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/managerconfig"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

// A Core that accepts the connection but never answers must surface as an
// explicit timeout, never as an empty projection.
func TestReadReportsCoreTimeout(t *testing.T) {
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(1500 * time.Millisecond)
	}))
	t.Cleanup(core.Close)
	cfg := testutil.NewConfig(t)
	db := testutil.NewStore(t, cfg)
	if err := db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{
		CPABaseURL:    core.URL,
		ManagementKey: "timeout-contract-key",
	}}); err != nil {
		t.Fatal(err)
	}
	service := New(managerconfigsvc.New(cfg, db, nil))
	service.timeout = 150 * time.Millisecond
	projection, err := service.Read(context.Background())
	failure, ok := err.(*Failure)
	if !ok {
		t.Fatalf("read error = %v, want a classified failure", err)
	}
	if failure.Kind != KindUnreachable || failure.Reason != "core_timeout" {
		t.Fatalf("failure = %+v", failure)
	}
	if projection.Providers != nil || projection.Ready != nil {
		t.Fatalf("timeout fabricated a projection: %+v", projection)
	}
}
