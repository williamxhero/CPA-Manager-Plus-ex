package store_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

func TestCredentialQuotaCompletionSnapshotIsAtomicAndFenced(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "quota.sqlite")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	const source = `{"sourceVersion":"source-1"}`
	const success = `{"queryId":"job-1","queryStatus":"success"}`
	begin := func(job string, keep bool) {
		t.Helper()
		r := store.CredentialQuotaRecord{ID: "source", Version: 1, Source: source, Attempt: `{"queryId":"` + job + `","queryStatus":"running"}`}
		expected := int64(1)
		if job == "job-1" {
			expected = 0
		}
		if err := s.BeginCredentialQuota(ctx, r, expected, job, `{"state":"running"}`, keep); err != nil {
			t.Fatal(err)
		}
	}
	begin("job-1", false)
	first, err := s.CompleteCredentialQuotaSnapshot(ctx, "source", 1, "job-1", success, `{"state":"complete"}`, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("exact-success-snapshot", func(t *testing.T) {
		want := store.CredentialQuotaRecord{ID: "source", Version: 1, Source: source, Attempt: success, LastSuccess: success}
		if first != want {
			t.Fatalf("snapshot %+v want %+v", first, want)
		}
		doc, err := s.CredentialQuotaJob(ctx, "job-1")
		if err != nil || doc != `{"state":"complete"}` {
			t.Fatalf("job was not committed with snapshot: %s %v", doc, err)
		}
	})
	begin("job-2", true)
	const failed = `{"queryId":"job-2","queryStatus":"auth_error"}`
	t.Run("failure-retains-real-success-and-old-snapshot", func(t *testing.T) {
		r, err := s.CompleteCredentialQuotaSnapshot(ctx, "source", 1, "job-2", failed, `{"state":"complete"}`, false)
		if err != nil || r.Attempt != failed || r.LastSuccess != success || r.Source != source || r.Version != 1 || first.Attempt != success {
			t.Fatalf("bad retained/immutable snapshot: %+v old=%+v err=%v", r, first, err)
		}
	})
	begin("job-3", true)
	for _, tc := range []struct {
		name, id, job string
		version       int64
		cancelled     bool
	}{
		{"source-fence", "different-source", "job-3", 1, false},
		{"version-fence", "source", "job-3", 2, false},
		{"attempt-fence", "source", "job-2", 1, false},
		{"cancelled-before-commit", "source", "job-3", 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			acquisition, cancel := context.WithCancel(ctx)
			defer cancel()
			if tc.cancelled {
				cancel()
			}
			r, err := s.CompleteCredentialQuotaSnapshot(acquisition, tc.id, tc.version, tc.job, success, `{"state":"complete"}`, true)
			want := store.ErrQuotaConflict
			if tc.cancelled {
				want = context.Canceled
			}
			if !errors.Is(err, want) || r != (store.CredentialQuotaRecord{}) {
				t.Fatalf("uncommitted snapshot escaped: %+v %v", r, err)
			}
		})
	}
	t.Run("later-write-failure-rolls-back-snapshot", func(t *testing.T) {
		// A real SQLite failure after the source UPDATE RETURNING must not
		// publish its tentative result or leave it visible to another reader.
		raw, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		defer raw.Close()
		_, err = raw.Exec(`CREATE TRIGGER reject_quota_job BEFORE UPDATE ON credential_quota_jobs BEGIN SELECT RAISE(ABORT,'controlled job write failure'); END`)
		if err != nil {
			t.Fatal(err)
		}
		r, err := s.CompleteCredentialQuotaSnapshot(ctx, "source", 1, "job-3", success, `{"state":"complete"}`, true)
		if err == nil || r != (store.CredentialQuotaRecord{}) {
			t.Fatalf("tentative snapshot escaped: %+v %v", r, err)
		}
		current, err := s.CredentialQuota(ctx, "source")
		if err != nil || current.Attempt != `{"queryId":"job-3","queryStatus":"running"}` || current.LastSuccess != success {
			t.Fatalf("failed completion was not rolled back: %+v %v", current, err)
		}
		if _, err = raw.Exec(`DROP TRIGGER reject_quota_job`); err != nil {
			t.Fatal(err)
		}
		// Existing callers retain the error-only completion interface.
		if err = s.CompleteCredentialQuota(ctx, "source", 1, "job-3", `{"queryId":"job-3","queryStatus":"auth_error"}`, `{"state":"complete"}`, false); err != nil {
			t.Fatal(err)
		}
	})
}
