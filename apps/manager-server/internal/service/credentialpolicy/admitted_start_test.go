package credentialpolicy_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	p "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy"
	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

// Local SQLite/trusted admitted-Job seam ONLY: synthetic source descriptors,
// existing OFF record, no positive ON, no Core ACK or routing claim.
func TestAdmittedKnownOFFCapturesOriginalGenerationAcrossIssuedScopeWithoutIO(t *testing.T) {
	ctx := context.Background()
	cfg := testutil.NewConfig(t)
	db := testutil.NewStore(t, cfg)
	installation, err := db.CredentialPolicyInstallation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	observed, _ := windowEvidence(t)
	old := observed.Source
	id := p.SubjectID(old)
	state := p.Status{Source: old, SubjectID: id, Owner: p.OwnerNamespace + installation, Generation: 2, Enabled: false}
	raw, _ := json.Marshal(state)
	if err := db.SaveCredentialPolicy(ctx, store.CredentialPolicyRecord{ID: id, Generation: 2, Document: string(raw)}, 0); err != nil {
		t.Fatal(err)
	}
	current := old
	current.Scope.BootEpoch = uuid.NewString()
	current.Scope.Revision++
	current.AuthIndex = "current-runtime-selector"
	q := cq.Query{Binding: current.Binding, AuthIndex: current.AuthIndex, SourceVersion: current.SourceVersion, Version: 1}
	job := cq.Job{ID: uuid.NewString(), State: "running", Query: q}
	jobDoc, _ := json.Marshal(job)
	sourceDoc, _ := json.Marshal(current)
	attemptDoc, _ := json.Marshal(map[string]any{"queryId": job.ID, "queryStatus": "running"})
	if err := db.BeginCredentialQuota(ctx, store.CredentialQuotaRecord{ID: id, Version: 1, Source: string(sourceDoc), Attempt: string(attemptDoc)}, 0, job.ID, string(jobDoc), false); err != nil {
		t.Fatal(err)
	}
	svc := p.New(db, nil, nil, false) // any remote fallback is unavailable
	if err := svc.CaptureAdmittedCredentialQueryStart(ctx, q, job.ID); err != nil {
		t.Fatal(err)
	}
	fence, err := db.CredentialPolicyQuery(ctx, job.ID)
	want, _ := json.Marshal(q)
	if err != nil || fence.Generation != 2 || fence.Enabled || fence.Owner != state.Owner || fence.Source != string(want) {
		t.Fatal("original OFF/gen/full admitted query not durably captured", err)
	}
	if err := svc.CaptureAdmittedCredentialQueryStart(ctx, q, job.ID); err != nil {
		t.Fatal("exact duplicate changed capture", err)
	}
	changed := q
	changed.AuthIndex = "invented-selector"
	if err := svc.CaptureAdmittedCredentialQueryStart(ctx, changed, job.ID); err == nil {
		t.Fatal("raw altered query trusted instead of durable Job")
	}
	if err := svc.CaptureAdmittedCredentialQueryStart(ctx, q, uuid.NewString()); err == nil {
		t.Fatal("unadmitted invented Job accepted")
	}
	state.Generation = 3
	raw, _ = json.Marshal(state)
	if err := db.SaveCredentialPolicy(ctx, store.CredentialPolicyRecord{ID: id, Generation: 3, Document: string(raw)}, 2); err != nil {
		t.Fatal(err)
	}
	if err := svc.CaptureAdmittedCredentialQueryStart(ctx, q, job.ID); err == nil {
		t.Fatal("old duplicate recaptured new generation")
	}
	// A genuinely different full target is NOT a transient scope rebind, even
	// if an untrusted test fixture reuses the same SourceVersion string.
	changed = q
	changed.Targets = append(changed.Targets[:0:0], q.Targets...)
	changed.Targets[0].CredentialVersion++
	changed.Version = 2
	newJob := cq.Job{ID: uuid.NewString(), State: "running", Query: changed}
	jobDoc, _ = json.Marshal(newJob)
	current.Binding = changed.Binding
	sourceDoc, _ = json.Marshal(current)
	attemptDoc, _ = json.Marshal(map[string]any{"queryId": newJob.ID, "queryStatus": "running"})
	if err := db.BeginCredentialQuota(ctx, store.CredentialQuotaRecord{ID: id, Version: 2, Source: string(sourceDoc), Attempt: string(attemptDoc)}, 1, newJob.ID, string(jobDoc), false); err != nil {
		t.Fatal(err)
	}
	if err := svc.CaptureAdmittedCredentialQueryStart(ctx, changed, newJob.ID); err == nil {
		t.Fatal("changed credential version inherited authority without actual certificate")
	}
}
