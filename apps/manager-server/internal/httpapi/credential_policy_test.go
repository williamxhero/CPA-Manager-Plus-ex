package httpapi

import (
	"encoding/json"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
	"testing"
)

func policyBody(t *testing.T, source cq.Source, generation int64, enabled bool) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"scope": source.Scope, "targets": source.Targets, "auth_index": source.AuthIndex, "sourceVersion": source.SourceVersion, "expectedGeneration": generation, "enabled": enabled})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCredentialPolicyRequiresPanelAuthorization(t *testing.T) {
	cfg := testutil.NewConfig(t)
	db := testutil.NewStore(t, cfg)
	testutil.EnsureAdminCredential(t, db)
	h := New(cfg, db, collector.NewManager(cfg, db)).Handler()
	for _, method := range []string{"GET", "PUT", "POST"} {
		path := "/usage-service/credential-policy"
		if method == "POST" {
			path += "/resume"
		}
		rr := testutil.Request(t, h, method, path, `{}`, "")
		testutil.RequireStatus(t, rr, 401)
		if rr.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("policy authentication response is cacheable")
		}
	}
}
