package officialbalance_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

func TestHostAttestationEnablesVerifiedBrowserEnrollment(t *testing.T) {
	protector, err := security.NewProtector([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "attest.sqlite"), protector)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := officialbalance.New(db, nil)
	src := balance.WebConfig{
		Site: "layso", AccountID: "h7w8", TenantID: "Personal", Role: "Personal",
		ExecutionUser: "will", Browser: balance.WebBrowserIdentity{Family: "microsoft-edge", Profile: "Profile 1", InstanceID: "synthetic-edge-instance"},
		Currency: "USD", ParsingRule: balance.LaysoDashboardModelsRuleV1,
	}
	req := balance.WebRequest{Authority: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Source: src}
	if svc.WebReader.Ready(req) {
		t.Fatal("ready before host attestation")
	}
	if err := svc.AttestWebBrowserHost(context.Background(), store.WebBrowserHostAttestation{
		ProofReference: "host-profile1-synthetic-edge-instance",
		Family:         "microsoft-edge",
		Profile:        "Profile 1",
		InstanceID:     "synthetic-edge-instance",
		ExecutionUser:  "will",
		EvidenceKind:   "verified_browser",
	}); err != nil {
		t.Fatal(err)
	}
	if !svc.WebReader.SupportsRule(balance.LaysoDashboardModelsRuleV1) {
		t.Fatal("parser rule missing")
	}
	if !svc.WebReader.Ready(req) {
		t.Fatal("host attestation must authorize Ready")
	}
	wrong := req
	wrong.Source.Browser.InstanceID = "other-instance"
	if svc.WebReader.Ready(wrong) {
		t.Fatal("mismatched instance must stay closed")
	}
	if err := svc.RevokeWebBrowserHostAttestations(context.Background()); err != nil {
		t.Fatal(err)
	}
	if svc.WebReader.Ready(req) {
		t.Fatal("revoke must fail closed")
	}
}
