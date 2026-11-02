package httpapi

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	cp "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy"
	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	fr "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fullrefresh"
)

// Reuses the sidecar's external-only fixture without modifying its owned files.
// This slice proves the authenticated ON prerequisite, not automated admission.
func TestCredentialPolicyActualFreshSharedQueryRequiredForEveryONGeneration(t *testing.T) {
	runCredentialPolicyONPrerequisite(t, 300)
}

// The existing coordinator default budget is a separate functional slice; the
// unchanged300ms case above remains required and must not be relabelled green.
func TestCredentialPolicyActualFreshSharedQueryDefaultBudgetONPrerequisite(t *testing.T) {
	runCredentialPolicyONPrerequisite(t, 0)
}

func runCredentialPolicyONPrerequisite(t *testing.T, timeout int64) {
	f := newNativeAdmissionFixture(t)
	var remainingAfterPolicyMS atomic.Int64
	f.manager.AppContext().FullRefreshService.SubscribeStarted(func(_ context.Context, event fr.StartedObservation) error {
		if event.Source.Kind == fr.Credential {
			remainingAfterPolicyMS.Store(time.Until(time.UnixMilli(event.DeadlineMS)).Milliseconds())
		}
		return nil // observation only: does not replace any original trusted fence
	})
	src := f.sources["A"]
	generation := int64(0)
	enabled := true
	update := func(want int, out any) {
		f.api("PUT", "/usage-service/credential-policy", cp.Update{Binding: src.Binding, AuthIndex: src.AuthIndex, SourceVersion: src.SourceVersion, ExpectedGeneration: &generation, Enabled: &enabled}, want, out)
	}
	var rejected struct {
		Error string `json:"error"`
	}
	update(503, &rejected)
	if rejected.Error != "fresh_authoritative_evidence_required" {
		t.Fatalf("wrong prerequisite rejection: %+v", rejected)
	}
	query := func() {
		var status cq.Status
		f.api("GET", "/usage-service/credential-quota?authIndex="+src.AuthIndex, nil, 200, &status)
		q := cq.Query{Binding: status.Binding, AuthIndex: status.AuthIndex, SourceID: status.SourceID, SubjectID: status.SubjectID, SourceVersion: status.SourceVersion, Version: status.Version, TimeoutMS: timeout}
		var job cq.Job
		f.api("POST", "/usage-service/credential-quota", q, 202, &job)
		// Wait on the real typed completion; aggressive Job polling itself
		// performs current-source Core reads and competes with this300ms job.
		select {
		case event := <-f.completed:
			if event.AttemptID != job.ID {
				t.Fatal("unrelated completion")
			}
		case <-time.After(5 * time.Second):
			// Failure-only public read AFTER the fixed deadline; never poll Core
			// concurrently with the short query or extend its execution budget.
			var failed cq.Job
			f.api("GET", "/usage-service/credential-quota/jobs/"+job.ID, nil, 200, &failed)
			t.Logf("actual short-budget diagnostic job=%s state=%s deadlineMs=%d quotaA=%d quotaB=%d", failed.ID, failed.State, failed.DeadlineMS, f.quotaCalls[0].Load(), f.quotaCalls[1].Load())
			if failed.Status != nil && failed.Status.Attempt != nil {
				t.Logf("actual short-budget diagnostic status=%s error=%s", failed.Status.Attempt.QueryStatus, failed.Status.Attempt.ErrorCode)
			}
			t.Logf("actual short-budget diagnostic remainingAfterPolicyStartedMs=%d", remainingAfterPolicyMS.Load())
			t.Fatalf("same76 trusted completion missing; requested timeoutMs=%d", timeout)
		}
		f.api("GET", "/usage-service/credential-quota/jobs/"+job.ID, nil, 200, &job)
		if job.Status == nil || job.Status.Attempt == nil || job.Status.Attempt.QueryStatus != "success" {
			t.Fatalf("actual prerequisite query failed: %+v", job)
		}
	}
	query()
	var saved cp.Status
	update(200, &saved)
	if !saved.Enabled || saved.Generation != 1 || saved.Applied || saved.Operation != nil {
		t.Fatalf("ON not truthful: %+v", saved)
	}
	// The pre-ON completion captured OFF. It cannot be recaptured as ON or
	// authorize a later action, even though the upstream quota was authoritative.
	generation = 1
	enabled = false
	update(200, &saved)
	generation = 2
	enabled = true
	update(503, &rejected)
	if rejected.Error != "fresh_authoritative_evidence_required" {
		t.Fatal("OFF/ON reused an earlier generation")
	}
	query()
	update(200, &saved)
	if !saved.Enabled || saved.Generation != 3 || saved.Applied || saved.Operation != nil {
		t.Fatalf("fresh generation not armed independently: %+v", saved)
	}
	if f.quotaCalls[0].Load() != 2 || f.quotaCalls[1].Load() != 0 || f.sends[0].Load() != 0 || f.sends[1].Load() != 0 {
		t.Fatal("prerequisite created duplicate/private acquisitions or business traffic")
	}
}
