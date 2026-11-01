package credentialpolicy_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	p "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy"
	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

func TestMalformedGateOperationIsNotTransientUnavailability(t *testing.T) {
	if got := p.DecodeGateBadRequest([]byte(` { "error" : "invalid_operation" } `)); got != "gate_operation_invalid" {
		t.Fatal(got)
	}
	for _, raw := range []string{`{}`, `{"Error":"invalid_operation"}`, `{"error":"invalid_operation","error":"invalid_operation"}`, `{"error":"invalid_operation","extra":1}`, `{"error":"invalid_operation"}{}`} {
		if got := p.DecodeGateBadRequest([]byte(raw)); got != "gate_request_invalid" {
			t.Fatal("malformed rejection accepted", got)
		}
	}
}

// Local unsupported historical journal fixture; no Core ACK or applied pause.
func TestRejectedLegacyV5OperationSettlesWithoutBodyMutationOrReplacement(t *testing.T) {
	ctx := context.Background()
	cfg := testutil.NewConfig(t)
	db := testutil.NewStore(t, cfg)
	observed, now := windowEvidence(t)
	installation, err := db.CredentialPolicyInstallation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	state := p.Status{Source: observed.Source, SubjectID: p.SubjectID(observed.Source), Owner: p.OwnerNamespace + installation, Generation: 1, Enabled: true}
	doc, _ := json.Marshal(state)
	if err := db.SaveCredentialPolicy(ctx, store.CredentialPolicyRecord{ID: state.SubjectID, Generation: 1, Document: string(doc)}, 0); err != nil {
		t.Fatal(err)
	}
	svc := p.New(db, fixedPolicySources{observed.Source}, nil, false, p.WithClock(func() time.Time { return now }))
	q := cq.Query{Binding: observed.Binding, AuthIndex: observed.AuthIndex, SourceVersion: observed.SourceVersion, Version: observed.Version}
	if err := svc.CaptureCredentialQueryStart(ctx, q, observed.Attempt.QueryID); err != nil {
		t.Fatal(err)
	}
	decision := p.ValidateCredentialEvidence(observed, now)
	oldID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(state.Owner+":"+state.SubjectID+":1:pause:"+decision.EvidenceVersion)).String()
	intent := p.PendingIntent{Planned: true, Operation: p.GateOperation{Version: 1, OpID: oldID, Binding: observed.Binding, AuthIndex: observed.AuthIndex, SourceVersion: observed.SourceVersion, Subject: "credential", Owner: state.Owner, Action: "pause", Reason: "quota_windows_exhausted", EvidenceID: uuid.NewSHA1(uuid.NameSpaceOID, []byte("quota-evidence:"+decision.EvidenceVersion)).String()}, Source: observed.Source, Evidence: decision, ExpiresAtMS: decision.ValidUntilMS}
	body, _ := json.Marshal(intent)
	lease, err := db.AcquireCredentialPolicyLease(ctx, uuid.NewString(), now.UnixMilli(), 30000)
	if err != nil {
		t.Fatal(err)
	}
	old, err := db.BeginCredentialPolicyOperation(ctx, store.CredentialPolicyOperation{ID: oldID, SubjectID: state.SubjectID, Generation: 1, Owner: state.Owner, Automatic: true, Body: string(body)}, lease, now.UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	old, err = db.ClaimCredentialPolicyOperation(ctx, oldID, old.Revision, lease, now.UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ReleaseCredentialPolicyLease(ctx, lease); err != nil {
		t.Fatal(err)
	}
	duplicate, err := svc.CompleteCredentialQuery(ctx, q, observed, observed.Attempt.QueryID)
	if err != nil || duplicate.OperationID != oldID {
		t.Fatal("legacy completion replaced old operation", err)
	}
	if err := svc.Reconcile(ctx); err != nil {
		t.Fatal(err)
	} // no connection: must settle before RPC
	failed, err := db.CredentialPolicyOperation(ctx, oldID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Body != old.Body || failed.ID != oldID || failed.State != "conflict" || failed.LastError != "gate_operation_id_unsupported" || !failed.Attempted || failed.Result != "" {
		t.Fatal("legacy journal mutated, falsely acknowledged, or left retrying")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = testutil.NewStore(t, cfg)
	svc = p.New(db, fixedPolicySources{observed.Source}, nil, false, p.WithClock(func() time.Time { return now }))
	again, err := svc.CompleteCredentialQuery(ctx, q, observed, observed.Attempt.QueryID)
	if err != nil || again.OperationID != oldID {
		t.Fatal("restart replaced rejected legacy ID", err)
	}
	pending, err := db.PendingCredentialPolicyOperations(ctx)
	if err != nil || len(pending) != 0 {
		t.Fatal("legacy rejection generated replacement work")
	}
}
