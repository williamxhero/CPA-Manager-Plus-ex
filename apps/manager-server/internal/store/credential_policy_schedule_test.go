package store_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

// Local atomic scheduling/CAS semantics only. Synthetic evidence is NOT a
// qualifying Core association or positive policy/routing proof.
func TestCredentialPolicyOfficialGenerationHintIsAtomicAndOffCannotInvalidate(t *testing.T) {
	for _, discharge := range []bool{false, true} {
		t.Run(map[bool]string{false: "next_cycle", true: "current_original_started_discharge"}[discharge], func(t *testing.T) { testOfficialGenerationHint(t, discharge) })
	}
}

func testOfficialGenerationHint(t *testing.T, discharge bool) {
	ctx := context.Background()
	protector, err := security.NewProtector([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "policy.sqlite"), protector)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	installation, err := db.CredentialPolicyInstallation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SaveOfficialBalance(ctx, store.OfficialBalanceRecord{ID: "official", Config: `{"version":1}`}, 0, "SYNTHETIC-SECRET", false); err != nil {
		t.Fatal(err)
	}
	source, err := db.OfficialBalance(ctx, "official")
	if err != nil {
		t.Fatal(err)
	}
	jobID := uuid.NewString()
	running := `{"id":"` + jobID + `","state":"running","version":1}`
	if err := db.BeginOfficialBalanceJob(ctx, "official", 1, jobID, running, `{"queryId":"`+jobID+`","queryStatus":"running"}`); err != nil {
		t.Fatal(err)
	}
	c := store.CredentialAssociationCapture{ID: uuid.NewString(), JobID: jobID, SourceID: "official", ConfigVersion: 1, Config: source.Config, SecretRef: source.SecretRef, PolicyID: "selected", Job: running, Document: `{"localOnly":true}`}
	if err := db.PutCredentialAssociationCapture(ctx, c); err != nil {
		t.Fatal(err)
	}
	obs := `{"queryId":"` + jobID + `","queryStatus":"success"}`
	if err := db.CompleteOfficialBalance(ctx, "official", 1, jobID, obs, true); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveOfficialBalanceJob(ctx, jobID, "official", `{"id":"`+jobID+`","state":"complete","version":1,"status":{"attempt":`+obs+`}}`); err != nil {
		t.Fatal(err)
	}
	e := store.CredentialAssociationEvidence{CaptureID: c.ID, Document: `{"localOnly":true}`}
	if err := db.AcceptCredentialAssociationEvidence(ctx, c, e.Document); err != nil {
		t.Fatal(err)
	}
	key := "source:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	for _, k := range []string{key, "source:other", "round:scope:incarnation"} {
		if err := db.SaveFullRefreshDue(ctx, k, 9000); err != nil {
			t.Fatal(err)
		}
	}
	r := store.CredentialPolicyRecord{ID: "selected", Generation: 1, Document: `{"enabled":false}`, RefreshScheduleKey: key}
	if err := db.SaveCredentialPolicyWithAssociation(ctx, r, 0, e); err == nil {
		t.Fatal("OFF invalidated schedule hint")
	}
	if due, _ := db.FullRefreshDue(ctx, key); due != 9000 {
		t.Fatal("rejected OFF changed credit", due)
	}
	r.Document = `{"enabled":true,"owner":"cpa-manager.credential-policy.v1/` + installation + `"}`
	if err := db.SaveCredentialPolicy(ctx, r, 0); err == nil {
		t.Fatal("unguarded intent invalidated schedule hint")
	}
	if err := db.SaveCredentialPolicyWithAssociation(ctx, r, 0, e); err != nil {
		t.Fatal(err)
	}
	if due, _ := db.FullRefreshDue(ctx, key); due != 9000 {
		t.Fatal("ON invalidated current scheduler cycle credit", due)
	}
	wantDue := int64(0)
	if discharge {
		if err := db.PutCredentialAssociationCapture(ctx, c, key); err == nil {
			t.Fatal("old completed OFF capture discharged ON hint")
		}
		jobID = uuid.NewString()
		running = `{"id":"` + jobID + `","state":"running","version":1}`
		if err := db.BeginOfficialBalanceJob(ctx, "official", 1, jobID, running, `{"queryId":"`+jobID+`","queryStatus":"running"}`); err != nil {
			t.Fatal(err)
		}
		c.ID, c.JobID, c.Job = uuid.NewString(), jobID, running
		c.PolicyGeneration, c.PolicyDocument = 1, r.Document
		if err := db.PutCredentialAssociationCapture(ctx, c, key); err != nil {
			t.Fatal(err)
		}
		// Local scheduling write AFTER original Started; not quota/association proof.
		wantDue = 10000
		if err := db.SaveFullRefreshDue(ctx, key, wantDue); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.ActivateCredentialPolicyScheduleHints(ctx); err != nil {
		t.Fatal(err)
	}
	if due, _ := db.FullRefreshDue(ctx, key); due != wantDue {
		t.Fatal("next official cycle discarded new credit or retained old credit", due, wantDue)
	}
	for _, k := range []string{"source:other", "round:scope:incarnation"} {
		if due, _ := db.FullRefreshDue(ctx, k); due != 9000 {
			t.Fatal("unrelated schedule changed", k, due)
		}
	}
	r.Generation = 2
	r.Document = `{"enabled":false}`
	r.RefreshScheduleKey = ""
	if err := db.SaveFullRefreshDue(ctx, key, 10000); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveCredentialPolicy(ctx, r, 1); err != nil {
		t.Fatal(err)
	}
	if due, _ := db.FullRefreshDue(ctx, key); due != 10000 {
		t.Fatal("ordinary OFF changed scheduling", due)
	}
}

func TestCredentialPolicyScheduleHintDischargeRequiresOriginalCurrentStarted(t *testing.T) {
	for _, mode := range []string{"old_generation", "different_source", "current_exact", "restart_pending", "off_supersedes"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "policy.sqlite")
			db, err := store.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { db.Close() }()
			installation, err := db.CredentialPolicyInstallation(ctx)
			if err != nil {
				t.Fatal(err)
			}
			key := "source:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
			id, attempt, query := "issued-subject", uuid.NewString(), `{"version":1}`
			owner := "cpa-manager.credential-policy.v1/" + installation
			f := store.CredentialPolicyQueryFence{AttemptID: attempt, SubjectID: id, Owner: owner, Source: query}
			running := `{"id":"` + attempt + `","state":"running","query":` + query + `}`
			if err := db.BeginCredentialQuota(ctx, store.CredentialQuotaRecord{ID: id, Version: 1, Source: `{}`, Attempt: `{"queryId":"` + attempt + `","queryStatus":"running"}`}, 0, attempt, running, false); err != nil {
				t.Fatal(err)
			}
			if err := db.CaptureCredentialPolicyPrerequisite(ctx, f); err != nil {
				t.Fatal(err)
			}
			complete := `{"id":"` + attempt + `","state":"complete","query":` + query + `}`
			if err := db.CompleteCredentialQuota(ctx, id, 1, attempt, `{"queryId":"`+attempt+`","queryStatus":"success"}`, complete, true); err != nil {
				t.Fatal(err)
			}
			r := store.CredentialPolicyRecord{ID: id, Generation: 1, Document: `{"enabled":true,"owner":"` + owner + `"}`, RefreshScheduleKey: key}
			if err := db.SaveFullRefreshDue(ctx, key, 9000); err != nil {
				t.Fatal(err)
			}
			if err := db.SaveCredentialPolicyWithPrerequisite(ctx, r, 0, f, complete); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "off_supersedes":
				r.Generation, r.Document, r.RefreshScheduleKey = 2, `{"enabled":false,"owner":"`+owner+`"}`, ""
				if err := db.SaveCredentialPolicy(ctx, r, 1); err != nil {
					t.Fatal(err)
				}
			case "old_generation":
				if err := db.CaptureCredentialPolicyAdmittedQuery(ctx, f, key); err == nil {
					t.Fatal("old completed OFF job recaptured in ON generation")
				}
			case "restart_pending":
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
				db, err = store.Open(path)
				if err != nil {
					t.Fatal(err)
				}
			default:
				attempt = uuid.NewString()
				running = `{"id":"` + attempt + `","state":"running","query":` + query + `}`
				if err := db.BeginCredentialQuota(ctx, store.CredentialQuotaRecord{ID: id, Version: 1, Source: `{}`, Attempt: `{"queryId":"` + attempt + `","queryStatus":"running"}`}, 1, attempt, running, true); err != nil {
					t.Fatal(err)
				}
				f.AttemptID, f.Generation, f.Enabled = attempt, 1, true
				startedKey := key
				if mode == "different_source" {
					startedKey = "source:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
				}
				if err := db.CaptureCredentialPolicyAdmittedQuery(ctx, f, startedKey); err != nil {
					t.Fatal(err)
				}
				// Local scheduling write stands for later host credit, not evidence.
				if err := db.SaveFullRefreshDue(ctx, key, 10000); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.ActivateCredentialPolicyScheduleHints(ctx); err != nil {
				t.Fatal(err)
			}
			want := int64(0)
			if mode == "current_exact" {
				want = 10000
			}
			if mode == "off_supersedes" {
				want = 9000
			}
			if due, err := db.FullRefreshDue(ctx, key); err != nil || due != want {
				t.Fatalf("cycle hint/current credit mismatch: due=%d want=%d err=%v", due, want, err)
			}
		})
	}
}

func TestCredentialPolicyScheduleHintMigrationPreservesExistingIntentAndCredit(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old-policy.sqlite")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	installation, err := db.CredentialPolicyInstallation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r := store.CredentialPolicyRecord{ID: "existing-subject", Generation: 1, Document: `{"enabled":false,"owner":"cpa-manager.credential-policy.v1/` + installation + `"}`}
	if err := db.SaveCredentialPolicy(ctx, r, 0); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveFullRefreshDue(ctx, "source:existing", 9000); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	// Controlled prior-schema TEMP DB only: remove the new additive table while
	// retaining the actual old policy/installation/schedule tables and rows.
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`DROP TABLE credential_policy_schedule_hints`); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if id, err := db.CredentialPolicyInstallation(ctx); err != nil || id != installation {
		t.Fatal("migration changed owner identity", id, err)
	}
	if got, err := db.CredentialPolicy(ctx, r.ID); err != nil || got != r {
		t.Fatal("migration changed intent", got, err)
	}
	if err := db.ActivateCredentialPolicyScheduleHints(ctx); err != nil {
		t.Fatal("new hint schema unavailable", err)
	}
	if due, err := db.FullRefreshDue(ctx, "source:existing"); err != nil || due != 9000 {
		t.Fatal("migration changed old credit", due, err)
	}
}
