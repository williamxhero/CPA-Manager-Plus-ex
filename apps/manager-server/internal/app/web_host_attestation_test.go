package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

func TestLoadHostWebBrowserAttestationFromEnvFile(t *testing.T) {
	protector, err := security.NewProtector([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "attest-env.sqlite"), protector)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := officialbalance.New(db, nil)

	path := filepath.Join(t.TempDir(), "host-attestation.json")
	body := `{
  "proofReference": "host-edge-profile1-synthetic-env",
  "family": "microsoft-edge",
  "profile": "Profile 1",
  "instanceId": "synthetic-edge-instance",
  "executionUser": "will",
  "evidenceKind": "verified_browser"
}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CPAMP_WEB_BROWSER_ATTESTATION", path)
	if err := loadHostWebBrowserAttestation(context.Background(), svc); err != nil {
		t.Fatal(err)
	}
	req := balance.WebRequest{
		Authority: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		Source: balance.WebConfig{
			Site: "layso", AccountID: "h7w8", TenantID: "Personal", Role: "Personal",
			ExecutionUser: "will",
			Browser:       balance.WebBrowserIdentity{Family: "microsoft-edge", Profile: "Profile 1", InstanceID: "synthetic-edge-instance"},
			Currency:      "USD", ParsingRule: balance.LaysoDashboardModelsRuleV1,
		},
	}
	if !svc.WebReader.Ready(req) {
		t.Fatal("env attestation file must authorize Ready")
	}
}
