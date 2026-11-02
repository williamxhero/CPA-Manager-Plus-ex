package httpapi

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	fr "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fullrefresh"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"modernc.org/sqlite"
)

// The adapter below changes only SQLite's acknowledgement timing: the real
// completion transaction commits first, then the acquisition is cancelled before
// Commit returns. No production hook, forged row, query retry or timing sleep.
func TestCredentialQuotaActualCoreCommittedCompletionSurvivesCancellation(t *testing.T) {
	credentialQuotaCompletionCancellation(t, false)
}

func TestCredentialQuotaActualCoreCancelledCommitDoesNotNotify(t *testing.T) {
	credentialQuotaCompletionCancellation(t, true)
}

func credentialQuotaCompletionCancellation(t *testing.T, beforeCommit bool) {
	t.Helper()
	f := newRefreshFixture(t)
	app := f.manager.AppContext()
	barrier := &quotaCommitBarrier{committed: make(chan context.Context, 1), release: make(chan struct{}), beforeCommit: beforeCommit}
	var release sync.Once
	releaseCommit := func() { release.Do(func() { close(barrier.release) }) }
	defer releaseCommit()
	db := sql.OpenDB(&quotaCompletionConnector{path: app.Config.DBPath, barrier: barrier})
	t.Cleanup(func() { db.Close() })
	// Same temporary SQLite and actual host; replace only the public database
	// adapter before any acquisition. Identity, coordinator and global budget stay.
	app.CredentialQuotaService.Store = store.New(db)
	completed := make(chan fr.CompletedObservation, 1)
	app.FullRefreshService.Subscribe(func(_ context.Context, event fr.CompletedObservation) {
		if event.Source.Kind == fr.Credential {
			completed <- event
		}
	})
	code, data := f.api("GET", "/usage-service/credential-quota", nil)
	if code != 200 {
		t.Fatalf("sources HTTP%d: %s", code, data)
	}
	var sources struct {
		Sources []cq.Source `json:"sources"`
	}
	if err := json.Unmarshal(data, &sources); err != nil {
		t.Fatal(err)
	}
	var source cq.Source
	for _, candidate := range sources.Sources {
		if candidate.State == "ready" && candidate.Protocol == "codex-oauth-wham" {
			source = candidate
			break
		}
	}
	if source.AuthIndex == "" {
		t.Fatalf("actual native source missing: %s", data)
	}
	code, data = f.api("POST", "/usage-service/credential-quota", cq.Query{Binding: source.Binding, AuthIndex: source.AuthIndex, SourceID: source.SourceID, SubjectID: source.SubjectID, SourceVersion: source.SourceVersion})
	if code != 202 {
		t.Fatalf("start HTTP%d: %s", code, data)
	}
	var job cq.Job
	if err := json.Unmarshal(data, &job); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	select {
	case acquisition := <-barrier.committed:
		if acquisition.Err() != nil {
			t.Fatal("acquisition expired before real commit")
		}
		app.ManagementQueryBudget.Cancel(job.ID)
		<-acquisition.Done()
		if _, err := app.CredentialQuotaService.Store.CredentialQuota(acquisition, "cancelled-read"); !errors.Is(err, context.Canceled) {
			t.Fatalf("postcommit read did not fail with cancellation: %v", err)
		}
		releaseCommit()
	case <-ctx.Done():
		t.Fatal("real completion transaction did not commit")
	}
	// Joining the worker is the causal notification barrier, not progress/Job
	// polling or a larger query budget. The database read is now provably cancelled.
	if err := app.ManagementQueryBudget.Close(ctx); err != nil {
		t.Fatal(err)
	}
	code, data = f.api("GET", "/usage-service/credential-quota/jobs/"+job.ID, nil)
	if code != 200 {
		t.Fatalf("job HTTP%d: %s", code, data)
	}
	if err := json.Unmarshal(data, &job); err != nil {
		t.Fatal(err)
	}
	if beforeCommit {
		if job.State != "interrupted" || job.Status == nil || job.Status.LastSuccess != nil || job.Status.Attempt == nil || job.Status.Attempt.QueryStatus == "success" || f.nativeCalls.Load() != 1 {
			t.Fatalf("cancelled commit published success: %s nativeCalls=%d", data, f.nativeCalls.Load())
		}
	} else if job.State != "complete" || job.Status == nil || job.Status.Attempt == nil || job.Status.Attempt.QueryStatus != "success" || f.nativeCalls.Load() != 1 {
		t.Fatalf("committed job/call count changed: %s nativeCalls=%d", data, f.nativeCalls.Load())
	}
	select {
	case event := <-completed:
		if beforeCommit {
			t.Fatalf("uncommitted native job forged a typed completion: %+v", event)
		}
		if event.AttemptID != job.ID || event.Credential == nil || event.Credential.Attempt == nil || event.Credential.LastSuccess == nil || event.Credential.Attempt.QueryID != job.ID || event.Credential.Attempt.QueryStatus != "success" || event.Credential.LastSuccess.QueryID != job.ID {
			t.Fatalf("notification is not the committed observation: %+v", event)
		}
	default:
		if !beforeCommit {
			t.Fatal("durable successful native job lost its typed completion after postcommit cancellation")
		}
	}
}

type quotaCommitBarrier struct {
	committed      chan context.Context
	release        chan struct{}
	beforeCommit   bool
	jobRead        chan struct{}
	jobReadRelease chan struct{}
}
type quotaCompletionConnector struct {
	path    string
	barrier *quotaCommitBarrier
}

func (c *quotaCompletionConnector) Driver() driver.Driver { return &sqlite.Driver{} }
func (c *quotaCompletionConnector) Connect(context.Context) (driver.Conn, error) {
	conn, err := c.Driver().Open(c.path)
	if err != nil {
		return nil, err
	}
	return &quotaCompletionConn{Conn: conn, barrier: c.barrier}, nil
}

type quotaCompletionConn struct {
	driver.Conn
	barrier *quotaCommitBarrier
	tx      *quotaCompletionTx
}

func (c *quotaCompletionConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	tx, err := c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	c.tx = &quotaCompletionTx{Tx: tx, ctx: ctx, barrier: c.barrier}
	return c.tx, nil
}
func (c *quotaCompletionConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if c.tx != nil && strings.HasPrefix(query, "UPDATE credential_quota_sources SET attempt=") {
		c.tx.completion = true
	}
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}
func (c *quotaCompletionConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if c.barrier.jobRead != nil && strings.HasPrefix(query, "SELECT document FROM credential_quota_jobs") {
		select {
		case c.barrier.jobRead <- struct{}{}:
		default:
		}
		if c.barrier.jobReadRelease != nil {
			select {
			case <-c.barrier.jobReadRelease:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
	if c.tx != nil && strings.HasPrefix(query, "UPDATE credential_quota_sources SET attempt=") {
		c.tx.completion = true
	}
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

type quotaCompletionTx struct {
	driver.Tx
	ctx        context.Context
	barrier    *quotaCommitBarrier
	completion bool
}

func (tx *quotaCompletionTx) Commit() error {
	if tx.completion && tx.barrier.beforeCommit {
		tx.barrier.committed <- tx.ctx
		<-tx.barrier.release
		if err := tx.ctx.Err(); err != nil {
			_ = tx.Tx.Rollback()
			return err
		}
	}
	err := tx.Tx.Commit()
	if err == nil && tx.completion && !tx.barrier.beforeCommit {
		tx.barrier.committed <- tx.ctx
		<-tx.barrier.release
	}
	return err
}
