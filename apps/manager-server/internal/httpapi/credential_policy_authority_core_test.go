package httpapi

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	cp "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy"
	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	fr "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fullrefresh"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

// Public ON -> current GET -> real SQLite close/reopen. No policy/evidence rows
// are seeded; the only substituted participant is the external quota provider.
func TestCredentialPolicyActualIssuedAuthoritySurvivesONReadAndManagerRestart(t *testing.T) {
	f := newNativeAdmissionFixture(t)
	f.enableAB()
	check := func() cp.Status {
		t.Helper()
		a, b := f.status("A"), f.status("B")
		if !a.Enabled || b.Enabled || a.Generation != 1 || b.Generation != 1 || !a.BindingCurrent || a.Applied || a.Operation != nil {
			t.Fatalf("public ON/OFF intent did not survive current-source read: A=%+v B=%+v", a, b)
		}
		if a.SubjectID != cp.SubjectID(f.sources["A"]) || a.SourceID != f.sources["A"].SourceID || a.SubjectID == f.sources["A"].SubjectID {
			t.Fatal("public policy/local subject contract changed")
		}
		var wire map[string]any
		f.api("GET", "/usage-service/credential-policy?authIndex="+a.AuthIndex, nil, 200, &wire)
		if _, leaked := wire["issuedSource"]; leaked {
			t.Fatal("internal persistence envelope leaked into the public contract")
		}
		return a
	}
	before := check()
	reopenAuthorityTestManager(f)
	after := check()
	if before.Owner != after.Owner || before.SubjectID != after.SubjectID || before.Generation != after.Generation || f.quotaCalls[0].Load() != 1 || f.quotaCalls[1].Load() != 0 {
		t.Fatal("restart changed policy owner/intent or performed a private acquisition")
	}
}

func TestCredentialPolicyActualLegacyAuthorityRequiresExplicitFreshEnrollment(t *testing.T) {
	f := newNativeAdmissionFixture(t)
	f.enableAB()
	ctx := context.Background()
	a := f.status("A")
	owner := a.Owner
	record, err := f.db.CredentialPolicy(ctx, a.SubjectID)
	if err != nil {
		t.Fatal(err)
	}
	// The ONLY storage substitution is a legacy-format negative fixture, derived
	// from a real public ON. It is never accepted as new positive policy proof.
	var legacy map[string]json.RawMessage
	if err := json.Unmarshal([]byte(record.Document), &legacy); err != nil {
		t.Fatal(err)
	}
	delete(legacy, "issuedSource")
	legacy["generation"] = json.RawMessage(`2`)
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.SaveCredentialPolicy(ctx, store.CredentialPolicyRecord{ID: record.ID, Generation: 2, Document: string(raw)}, 1); err != nil {
		t.Fatal(err)
	}
	reopenAuthorityTestManager(f)
	a = f.status("A")
	if a.Enabled || a.Generation != 2 || a.Reason != "source_changed" || a.Operation != nil || a.Applied {
		t.Fatalf("incomplete legacy record regained live authority: %+v", a)
	}
	generation, enabled := a.Generation, true
	var reply any
	f.api("POST", "/usage-service/credential-policy/resume", cp.ResumeRequest{Binding: a.Binding, AuthIndex: a.AuthIndex, SourceVersion: a.SourceVersion, ExpectedGeneration: &generation, Owner: a.Owner, OpID: uuid.NewString()}, 409, &reply)
	f.api("PUT", "/usage-service/credential-policy", cp.Update{Binding: a.Binding, AuthIndex: a.AuthIndex, SourceVersion: a.SourceVersion, ExpectedGeneration: &generation, Enabled: &enabled}, 503, &reply)

	// Real depleted evidence and the production worker must not repair missing
	// authority implicitly. Typed completion, not a sleep, is the query barrier.
	f.depleted.Store(true)
	worker, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- f.manager.AppContext().FullRefreshService.Run(worker) }()
	stop := func() {
		cancel()
		shutdown, finish := context.WithTimeout(ctx, 5*time.Second)
		defer finish()
		if err := f.manager.AppContext().FullRefreshService.Shutdown(shutdown); err != nil {
			t.Error(err)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-shutdown.Done():
			t.Error("legacy lifecycle did not join")
		}
	}
	defer func() {
		if stop != nil {
			stop()
		}
	}()
	queryAuthorityTestQuota(f, f.sources["A"])
	a = f.status("A")
	if a.Enabled || a.Generation != 2 || a.Operation != nil || a.Applied {
		t.Fatal("fresh query silently reenrolled legacy ON")
	}
	f.api("PUT", "/usage-service/credential-policy", cp.Update{Binding: a.Binding, AuthIndex: a.AuthIndex, SourceVersion: a.SourceVersion, ExpectedGeneration: &generation, Enabled: &enabled}, 503, &reply)
	var gates cp.GateSnapshot
	f.coreAPI("GET", "inference-gates", nil, &gates)
	if !gates.Ready || gates.GateRevision != 0 || len(gates.Gates) != 0 {
		t.Fatal("legacy authority emitted an automatic Core action")
	}
	stop()
	stop = nil
	reopenAuthorityTestManager(f)

	// A deliberate OFF, then a new validated public acquisition, then explicit
	// ON is the recovery path. No guessed host subject or bypassed prerequisite.
	a = f.status("A")
	generation, enabled = a.Generation, false
	f.api("PUT", "/usage-service/credential-policy", cp.Update{Binding: a.Binding, AuthIndex: a.AuthIndex, SourceVersion: a.SourceVersion, ExpectedGeneration: &generation, Enabled: &enabled}, 200, &a)
	if a.Enabled || a.Generation != 3 {
		t.Fatal("legacy emergency OFF failed")
	}
	f.depleted.Store(false)
	queryAuthorityTestQuota(f, f.sources["A"])
	a = f.status("A")
	generation, enabled = a.Generation, true
	f.api("PUT", "/usage-service/credential-policy", cp.Update{Binding: a.Binding, AuthIndex: a.AuthIndex, SourceVersion: a.SourceVersion, ExpectedGeneration: &generation, Enabled: &enabled}, 200, &a)
	reopenAuthorityTestManager(f)
	a = f.status("A")
	if !a.Enabled || a.Generation != 4 || a.Owner != owner || a.Applied || a.Operation != nil {
		t.Fatalf("explicit fresh enrollment did not persist new authority: %+v", a)
	}
}

func queryAuthorityTestQuota(f *nativeAdmissionFixture, source cq.Source) {
	f.t.Helper()
	var status cq.Status
	f.api("GET", "/usage-service/credential-quota?authIndex="+source.AuthIndex, nil, 200, &status)
	var job cq.Job
	f.api("POST", "/usage-service/credential-quota", cq.Query{Binding: status.Binding, AuthIndex: status.AuthIndex, SourceID: status.SourceID, SubjectID: status.SubjectID, SourceVersion: status.SourceVersion, Version: status.Version}, 202, &job)
	deadline := time.After(15 * time.Second)
	for {
		select {
		case event := <-f.completed:
			if event.AttemptID != job.ID {
				continue
			}
			if event.Credential == nil || event.Credential.Stale || event.Credential.Attempt == nil || event.Credential.Attempt.QueryStatus != "success" || event.Credential.Attempt.QueryID != job.ID {
				f.t.Fatal("fresh authority enrollment query failed")
			}
			return
		case <-deadline:
			f.t.Fatal("fresh authority enrollment completion missing")
		}
	}
}

// Only used while the fixture's lifecycle is stopped; join all old host workers
// before closing its real temporary database and constructing a fresh Manager.
func reopenAuthorityTestManager(f *nativeAdmissionFixture) {
	f.t.Helper()
	cfg := f.manager.AppContext().Config
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := f.manager.AppContext().FullRefreshService.Shutdown(ctx); err != nil {
		f.t.Fatal(err)
	}
	if err := f.db.Close(); err != nil {
		f.t.Fatal(err)
	}
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { db.Close() })
	f.db = db
	f.manager = New(cfg, db, collector.NewManager(cfg, db))
	f.manager.AppContext().FullRefreshService.Subscribe(func(_ context.Context, event fr.CompletedObservation) { f.completed <- event })
}
