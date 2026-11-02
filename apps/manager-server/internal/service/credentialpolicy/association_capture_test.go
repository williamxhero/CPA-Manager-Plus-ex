package credentialpolicy_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	p "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

// Actual SQLite/encrypted74 storage, local frozen capture only. Synthetic issued
// descriptor fixture is NOT Core association, native readiness or positive ON.
func TestOfficialStartedCapturesOriginalContractWithoutRemoteIOOrSecrets(t *testing.T) {
	ctx := context.Background()
	protector, err := security.NewProtector([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "capture.sqlite")
	db, err := store.Open(path, protector)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	status, _ := windowEvidence(t)
	src := status.Source
	src.State = "unsupported"
	src.Protocol = "unsupported"
	src.AccountProvenance = ""
	discovery := p.New(db, fixedPolicySources{src}, nil)
	if _, err := discovery.Status(ctx, src.AuthIndex); err != nil {
		t.Fatal(err)
	}
	cfg := ob.Config{Binding: ob.Binding{Scope: src.Scope, Targets: src.Targets}, Version: 1, Adapter: balance.NewAPITokenUsageV1, Endpoint: "https://synthetic.invalid/api/usage/token/", NewAPI: &balance.NewAPIConfig{ProtocolConfirmed: true, InstanceVersion: "synthetic-v1", NativeUnit: "newapi_quota", Auth: balance.NewAPIInferenceKeyReadOnlyV1}}
	raw, _ := json.Marshal(cfg)
	sourceID := p.SubjectID(src)
	if err := db.SaveOfficialBalance(ctx, store.OfficialBalanceRecord{ID: sourceID, Config: string(raw)}, 0, "SECRET-CAPTURE-CANARY", false); err != nil {
		t.Fatal(err)
	}
	jobID := uuid.NewString()
	job := ob.Job{ID: jobID, State: "running", Binding: cfg.Binding, Version: 1}
	raw, _ = json.Marshal(job)
	if err := db.BeginOfficialBalanceJob(ctx, sourceID, 1, jobID, string(raw), `{"queryId":"`+jobID+`","queryStatus":"running"}`); err != nil {
		t.Fatal(err)
	}
	svc := p.New(db, nil, nil) // NO source/Core/secret-resolution RPC possible
	q := ob.Query{Binding: cfg.Binding, Version: 1}
	if err := svc.CaptureOfficialQueryStart(ctx, q, jobID); err != nil {
		t.Fatal(err)
	}
	first, err := db.CredentialAssociationCapturesForJob(ctx, jobID)
	if err != nil || len(first) != 1 {
		t.Fatal("default OFF source not captured")
	}
	if strings.Contains(first[0].Document, "SECRET-CAPTURE-CANARY") || first[0].PolicyGeneration != 0 || first[0].PolicyDocument != "" {
		t.Fatal("secret leaked or default intent invented")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(path, protector)
	if err != nil {
		t.Fatal(err)
	}
	svc = p.New(db, nil, nil)
	if err := svc.CaptureOfficialQueryStart(ctx, q, jobID); err != nil {
		t.Fatal(err)
	}
	again, err := db.CredentialAssociationCapturesForJob(ctx, jobID)
	if err != nil || len(again) != 1 || again[0] != first[0] {
		t.Fatal("restart/duplicate recaptured nonce or intent")
	}
	if err := db.SaveCredentialPolicy(ctx, store.CredentialPolicyRecord{ID: sourceID, Generation: 1, Document: `{"enabled":false}`}, 0); err != nil {
		t.Fatal(err)
	}
	if err := db.CheckCredentialAssociationCapture(ctx, first[0], false); err == nil {
		t.Fatal("capture crossed intent enrollment generation")
	}
	evaluation, err := svc.EvaluateOfficialAssociation(ctx, first[0].ID)
	if err != nil || evaluation.Associated || evaluation.Reason != "policy_generation_changed" {
		t.Fatalf("local CAS was not rejected before any proof RPC: %+v %v", evaluation, err)
	}
}
