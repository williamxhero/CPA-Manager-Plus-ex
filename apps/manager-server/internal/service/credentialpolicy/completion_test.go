package credentialpolicy_test

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"testing"
	"time"

	policy "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy"
	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

// Unit inventory only: never a capability or gate ACK implementation.
type fixedPolicySources []cq.Source

func (sources fixedPolicySources) Sources(context.Context) ([]cq.Source, error) { return sources, nil }

func TestCredentialPolicyRestartFreshRecoveryCandidateRetainsFullVersionFences(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewStore(t, testutil.NewConfig(t))
	status, now := windowEvidence(t)
	installation, err := db.CredentialPolicyInstallation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	id := policy.SubjectID(status.Source)
	state := policy.Status{Source: status.Source, SubjectID: id, Owner: policy.OwnerNamespace + installation, Generation: 1, Enabled: true}
	document, _ := json.Marshal(state)
	prior := store.CredentialPolicyRecord{ID: id, Generation: 1, Document: string(document)}
	if err := db.SaveCredentialPolicy(ctx, prior, 0); err != nil {
		t.Fatal(err)
	}
	// Simulated inventory/observation only. No gate capability, ON API or ACK.
	status.Scope.BootEpoch = uuid.NewString()
	status.Scope.Revision++
	var observation cq.CoreObservation
	if err := json.Unmarshal([]byte(status.Attempt.WindowsJSON), &observation); err != nil {
		t.Fatal(err)
	}
	observation.Binding = status.Binding
	for i := range observation.Windows {
		observation.Windows[i].RemainingFraction = .8
		observation.Windows[i].UsedPercent = 20
	}
	raw, _ := json.Marshal(observation)
	status.Attempt.WindowsJSON = string(raw)
	status.Attempt.Binding, _ = json.Marshal(status.Binding)
	sources := fixedPolicySources{status.Source}
	coordinator := policy.New(db, sources, nil, policy.WithClock(func() time.Time { return now }))
	q := cq.Query{Binding: status.Binding, SourceVersion: status.SourceVersion, AuthIndex: status.AuthIndex, Version: status.Version}
	if err := coordinator.CaptureCredentialQueryStart(ctx, q, status.Attempt.QueryID); err != nil {
		t.Fatal(err)
	}
	out, err := coordinator.CompleteCredentialQuery(ctx, q, status, status.Attempt.QueryID)
	if err != nil || out.Decision.Action != "recover" || out.OperationID == "" {
		t.Fatalf("fresh re-bound recovery candidate lost: %+v %v", out, err)
	}
	// A changed credential version is not certified refresh continuity.
	sources[0].Targets = append(sources[0].Targets[:0:0], sources[0].Targets...)
	sources[0].Targets[0].CredentialVersion++
	q.Binding = sources[0].Binding
	attempt := uuid.NewString()
	if err := coordinator.CaptureCredentialQueryStart(ctx, q, attempt); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(q)
	if err := db.CheckCredentialPolicyQuery(ctx, store.CredentialPolicyQueryFence{AttemptID: attempt, SubjectID: id, Generation: 1, Owner: state.Owner, Source: string(body), Enabled: true}); err == nil {
		t.Fatal("credential version guess inherited enabled policy")
	}
	state.Enabled = false
	state.Generation = 2
	document, _ = json.Marshal(state)
	if err := db.SaveCredentialPolicy(ctx, store.CredentialPolicyRecord{ID: id, Generation: 2, Document: string(document)}, 1); err != nil {
		t.Fatal(err)
	}
	scope, _ := json.Marshal(status.Scope)
	if err := db.RebindCredentialPolicy(ctx, prior, string(scope), status.AuthIndex); err == nil {
		t.Fatal("late rebind overwrote OFF")
	}
}

func TestCredentialPolicyCompletionRequiresGenerationCapturedBeforeQuery(t *testing.T) {
	cfg := testutil.NewConfig(t)
	db := testutil.NewStore(t, cfg)
	ctx := context.Background()
	installation, err := db.CredentialPolicyInstallation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	source, _ := windowEvidence(t)
	id := policy.SubjectID(source.Source)
	owner := policy.OwnerNamespace + installation
	state := policy.Status{Source: source.Source, SubjectID: id, Owner: owner, Generation: 1, Enabled: true, State: "pending"}
	document, _ := json.Marshal(state)
	// Synthetic local intent is ONLY a coordinator unit fixture, not positive ON.
	if err := db.SaveCredentialPolicy(ctx, store.CredentialPolicyRecord{ID: id, Generation: 1, Document: string(document)}, 0); err != nil {
		t.Fatal(err)
	}
	coordinator := policy.New(db, fixedPolicySources{source.Source}, nil)
	q := cq.Query{Binding: source.Binding, SourceVersion: source.SourceVersion, AuthIndex: source.AuthIndex, Version: source.Version}
	before, err := coordinator.CompleteCredentialQuery(ctx, q, source, source.Attempt.QueryID)
	if err != nil {
		t.Fatal(err)
	}
	if before.Decision.Action != "none" || before.OperationID != "" {
		t.Fatal("completion invented a start generation")
	}
	if err := coordinator.CaptureCredentialQueryStart(ctx, q, source.Attempt.QueryID); err != nil {
		t.Fatal(err)
	}
	state.Enabled = false
	state.Generation = 2
	document, _ = json.Marshal(state)
	if err := db.SaveCredentialPolicy(ctx, store.CredentialPolicyRecord{ID: id, Generation: 2, Document: string(document)}, 1); err != nil {
		t.Fatal(err)
	}
	state.Enabled = true
	state.Generation = 3
	document, _ = json.Marshal(state)
	if err := db.SaveCredentialPolicy(ctx, store.CredentialPolicyRecord{ID: id, Generation: 3, Document: string(document)}, 2); err != nil {
		t.Fatal(err)
	}
	after, err := coordinator.CompleteCredentialQuery(ctx, q, source, source.Attempt.QueryID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Decision.Action != "none" || after.OperationID != "" || after.Decision.Reason != "policy_generation_changed" {
		t.Fatalf("late pre-OFF/ON result accepted: %+v", after)
	}
	if err := coordinator.CaptureCredentialQueryStart(ctx, q, source.Attempt.QueryID); err == nil {
		t.Fatal("old query acquired new generation through duplicate start")
	}
	pending, err := db.PendingCredentialPolicyOperations(ctx)
	if err != nil || len(pending) != 0 {
		t.Fatal("late completion queued a future gate action")
	}
}

func TestCredentialPolicyFreshFencedEvidenceQueuesDurablyWithoutClaimingApplied(t *testing.T) {
	cfg := testutil.NewConfig(t)
	db := testutil.NewStore(t, cfg)
	ctx := context.Background()
	installation, err := db.CredentialPolicyInstallation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	source, now := windowEvidence(t)
	id := policy.SubjectID(source.Source)
	owner := policy.OwnerNamespace + installation
	state := policy.Status{Source: source.Source, SubjectID: id, Owner: owner, Generation: 1, Enabled: true, State: "pending"}
	document, _ := json.Marshal(state)
	if err := db.SaveCredentialPolicy(ctx, store.CredentialPolicyRecord{ID: id, Generation: 1, Document: string(document)}, 0); err != nil {
		t.Fatal(err)
	}
	coordinator := policy.New(db, fixedPolicySources{source.Source}, nil, policy.WithClock(func() time.Time { return now }))
	q := cq.Query{Binding: source.Binding, SourceVersion: source.SourceVersion, AuthIndex: source.AuthIndex, Version: source.Version}
	if err := coordinator.CaptureCredentialQueryStart(ctx, q, source.Attempt.QueryID); err != nil {
		t.Fatal(err)
	}
	first, err := coordinator.CompleteCredentialQuery(ctx, q, source, source.Attempt.QueryID)
	if err != nil {
		t.Fatal(err)
	}
	if first.OperationID == "" || first.Decision.Action != "pause" {
		t.Fatalf("candidate not durably queued: %+v", first)
	}
	if parsed, err := uuid.Parse(first.OperationID); err != nil || parsed.Version() != 4 {
		t.Fatal("automatic Core operation requires genuine UUIDv4, not deterministic v5")
	}
	second, err := coordinator.CompleteCredentialQuery(ctx, q, source, source.Attempt.QueryID)
	if err != nil {
		t.Fatal(err)
	}
	if second.OperationID != first.OperationID {
		t.Fatal("repeated completion created a different gate operation")
	}
	pending, err := db.PendingCredentialPolicyOperations(ctx)
	if err != nil || len(pending) != 1 || pending[0].State != "queued" || pending[0].Result != "" {
		t.Fatal("pending journal was not exactly-once/local-only")
	}
	local, err := coordinator.StoredStatus(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if local.Applied || local.BindingCurrent {
		t.Fatal("local queue claimed an actual Core ACK")
	}
	if local.Operation == nil || local.Operation.OpID != first.OperationID || local.Operation.State != "queued" || local.Operation.CurrentApplied || local.Operation.ReceiptConfirmed || local.Operation.EvidenceVersion != first.Decision.EvidenceVersion {
		t.Fatalf("local pending operation metadata missing or misleading: %+v", local.Operation)
	}
	lease, err := db.AcquireCredentialPolicyLease(ctx, uuid.NewString(), now.UnixMilli(), 30000)
	if err != nil {
		t.Fatal(err)
	}
	var intent policy.PendingIntent
	if err := json.Unmarshal([]byte(pending[0].Body), &intent); err != nil {
		t.Fatal(err)
	}
	intent.Planned = true
	intent.Operation.ExpectedGateRevision = 7
	body, _ := json.Marshal(intent)
	planned, err := db.PlanCredentialPolicyOperation(ctx, first.OperationID, pending[0].Revision, string(body), lease, now.UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := db.ClaimCredentialPolicyOperation(ctx, planned.ID, planned.Revision, lease, now.UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ReleaseCredentialPolicyLease(ctx, lease); err != nil {
		t.Fatal(err)
	}
	third, err := coordinator.CompleteCredentialQuery(ctx, q, source, source.Attempt.QueryID)
	if err != nil || third.OperationID != first.OperationID {
		t.Fatalf("duplicate after immutable planning failed: %+v %v", third, err)
	}
	after, err := db.CredentialPolicyOperation(ctx, claimed.ID)
	if err != nil || after.Body != claimed.Body || after.Revision != claimed.Revision {
		t.Fatal("duplicate changed already claimed operation")
	}
	local, err = coordinator.StoredStatus(ctx, id)
	if err != nil || local.Operation == nil || !local.Operation.Planned || !local.Operation.Attempted || local.Operation.ExpectedGateRevision != 7 || local.Operation.CurrentApplied || local.Operation.ReceiptConfirmed {
		t.Fatal("planned pending metadata implied applied state")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = testutil.NewStore(t, cfg)
	coordinator = policy.New(db, fixedPolicySources{source.Source}, nil, policy.WithClock(func() time.Time { return now }))
	restarted, err := coordinator.CompleteCredentialQuery(ctx, q, source, source.Attempt.QueryID)
	if err != nil || restarted.OperationID != first.OperationID {
		t.Fatal("restart allocated another random operation UUID", err)
	}
	again, err := db.CredentialPolicyOperation(ctx, first.OperationID)
	if err != nil || again.Body != after.Body || again.Revision != after.Revision {
		t.Fatal("restart rewrote the claimed operation body")
	}
	lease, err = db.AcquireCredentialPolicyLease(ctx, uuid.NewString(), now.UnixMilli(), 30000)
	if err != nil {
		t.Fatal(err)
	}
	// A forged/local DB outcome with no actual receipt must NEVER become proof.
	if err := db.CompleteCredentialPolicyOperation(ctx, after.ID, after.Revision, "applied", "", "", lease, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err := db.ReleaseCredentialPolicyLease(ctx, lease); err != nil {
		t.Fatal(err)
	}
	local, err = coordinator.StoredStatus(ctx, id)
	if err != nil || local.Applied || local.Operation.ReceiptConfirmed || local.Operation.CurrentApplied || local.Operation.State != "error" || local.Operation.Reason != "gate_ack_unconfirmed" {
		t.Fatal("DB applied string became a confirmed receipt")
	}
}
