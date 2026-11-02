package credentialpolicy_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	policy "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy"
	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	fr "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fullrefresh"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

// Local synthetic intent/inventory evidence seam, no positive ON or Core ACK.
func TestCredentialPolicyCompletedCallbackDurableAndFencedAcrossOFFON(t *testing.T) {
	ctx := context.Background()
	cfg := testutil.NewConfig(t)
	db := testutil.NewStore(t, cfg)
	status, now := windowEvidence(t)
	installation, err := db.CredentialPolicyInstallation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	state := policy.Status{Source: status.Source, SubjectID: policy.SubjectID(status.Source), Generation: 1, Owner: policy.OwnerNamespace + installation, Enabled: true}
	doc, _ := json.Marshal(state)
	if err := db.SaveCredentialPolicy(ctx, store.CredentialPolicyRecord{ID: state.SubjectID, Generation: 1, Document: string(doc)}, 0); err != nil {
		t.Fatal(err)
	}
	svc := policy.New(db, fixedPolicySources{status.Source}, nil, policy.WithClock(func() time.Time { return now }))
	q := cq.Query{Binding: status.Binding, AuthIndex: status.AuthIndex, SourceVersion: status.SourceVersion, Version: status.Version}
	if err := svc.CaptureCredentialQueryStart(ctx, q, status.Attempt.QueryID); err != nil {
		t.Fatal(err)
	}
	event := fr.CompletedObservation{Source: fr.SourceRequest{Kind: fr.Credential, Credential: &q}, AttemptID: status.Attempt.QueryID, Credential: &status}
	if err := svc.ObserveCompleted(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err := svc.ObserveCompleted(ctx, event); err != nil {
		t.Fatal(err)
	}
	queued, err := db.PendingCredentialPolicyEvents(ctx)
	if err != nil || len(queued) != 1 {
		t.Fatal("completion not durable/idempotent before dispatch")
	}
	ops, err := db.PendingCredentialPolicyOperations(ctx)
	if err != nil || len(ops) != 0 {
		t.Fatal("completion callback entered gate dispatch/journal work")
	}
	state.Enabled = false
	state.Generation = 2
	doc, _ = json.Marshal(state)
	if err := db.SaveCredentialPolicy(ctx, store.CredentialPolicyRecord{ID: state.SubjectID, Generation: 2, Document: string(doc)}, 1); err != nil {
		t.Fatal(err)
	}
	state.Enabled = true
	state.Generation = 3
	doc, _ = json.Marshal(state)
	if err := db.SaveCredentialPolicy(ctx, store.CredentialPolicyRecord{ID: state.SubjectID, Generation: 3, Document: string(doc)}, 2); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = testutil.NewStore(t, cfg)
	svc = policy.New(db, fixedPolicySources{status.Source}, nil, policy.WithClock(func() time.Time { return now }))
	if err := svc.ProcessCompletions(ctx); err != nil {
		t.Fatal(err)
	}
	ops, err = db.PendingCredentialPolicyOperations(ctx)
	if err != nil || len(ops) != 0 {
		t.Fatal("persisted pre-OFF/ON evidence crossed generation fence after restart")
	}
	queued, err = db.PendingCredentialPolicyEvents(ctx)
	if err != nil || len(queued) != 0 {
		t.Fatal("stale event did not settle safely")
	}
}

// Lifecycle proof is offline/negative: a known OFF completion is settled without
// any gate or quota request. Runtime cancellation must join the worker.
func TestCredentialPolicyRunDrainsDurableInboxAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db := testutil.NewStore(t, testutil.NewConfig(t))
	status, now := windowEvidence(t)
	installation, err := db.CredentialPolicyInstallation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	state := policy.Status{Source: status.Source, SubjectID: policy.SubjectID(status.Source), Generation: 1, Owner: policy.OwnerNamespace + installation}
	doc, _ := json.Marshal(state)
	if err := db.SaveCredentialPolicy(ctx, store.CredentialPolicyRecord{ID: state.SubjectID, Generation: 1, Document: string(doc)}, 0); err != nil {
		t.Fatal(err)
	}
	svc := policy.New(db, nil, nil, policy.WithClock(func() time.Time { return now }))
	q := cq.Query{Binding: status.Binding, AuthIndex: status.AuthIndex, SourceVersion: status.SourceVersion, Version: status.Version}
	if err := svc.ObserveCompleted(ctx, fr.CompletedObservation{Source: fr.SourceRequest{Kind: fr.Credential, Credential: &q}, AttemptID: status.Attempt.QueryID, Credential: &status}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx) }()
	deadline := time.After(3 * time.Second)
	for {
		event, err := db.CredentialPolicyEvent(ctx, status.Attempt.QueryID)
		if err != nil {
			t.Fatal(err)
		}
		if event.State == "complete" {
			break
		}
		select {
		case <-deadline:
			t.Fatal("runtime did not settle persisted OFF completion")
		case <-time.After(time.Millisecond):
		}
	}
	if err := svc.Run(ctx); !errors.Is(err, policy.ErrUnavailable) {
		t.Fatalf("duplicate worker admitted: %v", err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker outlived runtime cancellation")
	}
}

// This proves local journal planning only, NOT a positive ON or gate ACK.
func TestCredentialPolicyRestartedCompletionPlansOnlyFreshEvidence(t *testing.T) {
	for _, expired := range []bool{false, true} {
		name := "fresh"
		if expired {
			name = "expired"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			cfg := testutil.NewConfig(t)
			db := testutil.NewStore(t, cfg)
			status, now := windowEvidence(t)
			installation, err := db.CredentialPolicyInstallation(ctx)
			if err != nil {
				t.Fatal(err)
			}
			state := policy.Status{Source: status.Source, SubjectID: policy.SubjectID(status.Source), Generation: 1, Owner: policy.OwnerNamespace + installation, Enabled: true}
			doc, _ := json.Marshal(state)
			if err := db.SaveCredentialPolicy(ctx, store.CredentialPolicyRecord{ID: state.SubjectID, Generation: 1, Document: string(doc)}, 0); err != nil {
				t.Fatal(err)
			}
			svc := policy.New(db, fixedPolicySources{status.Source}, nil, policy.WithClock(func() time.Time { return now }))
			q := cq.Query{Binding: status.Binding, AuthIndex: status.AuthIndex, SourceVersion: status.SourceVersion, Version: status.Version}
			if err := svc.CaptureCredentialQueryStart(ctx, q, status.Attempt.QueryID); err != nil {
				t.Fatal(err)
			}
			event := fr.CompletedObservation{Source: fr.SourceRequest{Kind: fr.Credential, Credential: &q}, AttemptID: status.Attempt.QueryID, Credential: &status}
			if err := svc.ObserveCompleted(ctx, event); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db = testutil.NewStore(t, cfg)
			if expired {
				now = now.Add(6 * time.Minute)
			}
			svc = policy.New(db, fixedPolicySources{status.Source}, nil, policy.WithClock(func() time.Time { return now }))
			if err := svc.ProcessCompletions(ctx); err != nil {
				t.Fatal(err)
			}
			ops, err := db.PendingCredentialPolicyOperations(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if expired {
				if len(ops) != 0 {
					t.Fatal("expired evidence planned an action")
				}
			} else {
				if len(ops) != 1 || ops[0].Attempted {
					t.Fatal("fresh evidence did not produce one unsent durable operation")
				}
				if err := svc.ObserveCompleted(ctx, event); err != nil {
					t.Fatal(err)
				}
				if err := svc.ProcessCompletions(ctx); err != nil {
					t.Fatal(err)
				}
				again, err := db.PendingCredentialPolicyOperations(ctx)
				if err != nil || len(again) != 1 || again[0].ID != ops[0].ID || again[0].Body != ops[0].Body {
					t.Fatal("duplicate completion rewrote operation")
				}
			}
		})
	}
}
