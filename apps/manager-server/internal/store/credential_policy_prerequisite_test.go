package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

// Real SQLite/local CAS seam only, not qualifying upstream evidence or ON proof.
func TestCredentialPolicyPrerequisiteCapturesDefaultOffButNeverAuthorizesActions(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "policy.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	installation, err := db.CredentialPolicyInstallation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	id, attempt := "issued-subject", uuid.NewString()
	q := `{"version":1}`
	f := store.CredentialPolicyQueryFence{AttemptID: attempt, SubjectID: id, Owner: "cpa-manager.credential-policy.v1/" + installation, Source: q}
	if err := db.CaptureCredentialPolicyPrerequisite(ctx, f); !errors.Is(err, store.ErrPolicyConflict) {
		t.Fatalf("invented job accepted: %v", err)
	}
	running := `{"id":"` + attempt + `","state":"running","query":` + q + `}`
	if err := db.BeginCredentialQuota(ctx, store.CredentialQuotaRecord{ID: id, Version: 1, Source: `{}`, Attempt: `{"queryId":"` + attempt + `"}`}, 0, attempt, running, false); err != nil {
		t.Fatal(err)
	}
	if err := db.CaptureCredentialPolicyPrerequisite(ctx, f); err != nil {
		t.Fatal(err)
	}
	if err := db.CaptureCredentialPolicyPrerequisite(ctx, f); err != nil {
		t.Fatal("duplicate", err)
	}
	if err := db.CheckCredentialPolicyQuery(ctx, f); !errors.Is(err, store.ErrPolicyConflict) {
		t.Fatal("OFF prerequisite became automatic action authority", err)
	}
	record, _ := json.Marshal(map[string]any{"generation": 1, "enabled": false, "owner": f.Owner})
	if err := db.SaveCredentialPolicy(ctx, store.CredentialPolicyRecord{ID: id, Generation: 1, Document: string(record)}, 0); err != nil {
		t.Fatal(err)
	}
	if err := db.CaptureCredentialPolicyPrerequisite(ctx, f); !errors.Is(err, store.ErrPolicyConflict) {
		t.Fatal("old job recaptured after intent changed", err)
	}
}

func TestCredentialPolicyEnableCommitFencesOriginalCompletedJobAndLatestAttempt(t *testing.T) {
	for _, newer := range []bool{false, true} {
		t.Run(map[bool]string{false: "exact_local_CAS", true: "new_attempt_invalidates"}[newer], func(t *testing.T) {
			ctx := context.Background()
			db, err := store.Open(filepath.Join(t.TempDir(), "policy.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			installation, err := db.CredentialPolicyInstallation(ctx)
			if err != nil {
				t.Fatal(err)
			}
			id, attempt := "issued-subject", uuid.NewString()
			q := `{"version":1}`
			f := store.CredentialPolicyQueryFence{AttemptID: attempt, SubjectID: id, Owner: "cpa-manager.credential-policy.v1/" + installation, Source: q}
			running := `{"id":"` + attempt + `","state":"running","query":` + q + `}`
			if err := db.BeginCredentialQuota(ctx, store.CredentialQuotaRecord{ID: id, Version: 1, Source: `{}`, Attempt: `{"queryId":"` + attempt + `"}`}, 0, attempt, running, false); err != nil {
				t.Fatal(err)
			}
			if err := db.CaptureCredentialPolicyPrerequisite(ctx, f); err != nil {
				t.Fatal(err)
			}
			complete := `{"id":"` + attempt + `","state":"complete","query":` + q + `}`
			observation := `{"queryId":"` + attempt + `","queryStatus":"success"}`
			if err := db.CompleteCredentialQuota(ctx, id, 1, attempt, observation, complete, true); err != nil {
				t.Fatal(err)
			}
			doc, _ := json.Marshal(map[string]any{"enabled": true, "owner": f.Owner, "generation": 1})
			record := store.CredentialPolicyRecord{ID: id, Generation: 1, Document: string(doc)}
			if err := db.SaveCredentialPolicyWithPrerequisite(ctx, record, 0, f, running); !errors.Is(err, store.ErrPolicyConflict) {
				t.Fatal("different job document accepted", err)
			}
			if newer {
				next := uuid.NewString()
				if err := db.BeginCredentialQuota(ctx, store.CredentialQuotaRecord{ID: id, Version: 1, Source: `{}`, Attempt: `{"queryId":"` + next + `","queryStatus":"running"}`}, 1, next, `{}`, true); err != nil {
					t.Fatal(err)
				}
			}
			err = db.SaveCredentialPolicyWithPrerequisite(ctx, record, 0, f, complete)
			if newer {
				if !errors.Is(err, store.ErrPolicyConflict) {
					t.Fatal("new attempt did not fence ON", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := db.CheckCredentialPolicyQuery(ctx, f); !errors.Is(err, store.ErrPolicyConflict) {
				t.Fatal("pre-ON OFF fence became automatic authority", err)
			}
			if err := db.SaveCredentialPolicyWithPrerequisite(ctx, record, 0, f, complete); !errors.Is(err, store.ErrPolicyConflict) {
				t.Fatal("completed query crossed intent generation", err)
			}
		})
	}
}
