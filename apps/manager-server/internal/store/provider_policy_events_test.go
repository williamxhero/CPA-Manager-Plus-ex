package store_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

func TestProviderPolicyFrontierRotatesAcrossDecisionAndEventKinds(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mixed-frontier.sqlite")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if db != nil {
			_ = db.Close()
		}
	}()
	lease, err := db.AcquireCredentialPolicyLease(ctx, "00000000-0000-4000-8000-000000000003", 1000, 30000)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 64; i++ {
		id := fmt.Sprintf("slow-decision-%02d", i)
		next := store.ProviderPolicyRecord{ID: id, Generation: 1, Document: `{"enabled":false,"pending":{"action":"pause"}}`}
		if err := db.SaveProviderPolicy(ctx, next, store.ProviderPolicyRecord{ID: id}, lease, 1001); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.PutProviderPolicyEvent(ctx, "independent-success", `{}`); err != nil {
		t.Fatal(err)
	}
	first, err := db.ProviderPolicyFrontier(ctx)
	if err != nil || len(first) != 64 || first[0].Kind != "event" {
		t.Fatalf("new completion hidden behind the decision-only queue: %+v %v", first, err)
	}
	// Model a bounded pass that exhausts its budget after only a few failures.
	// Next selection must rotate ACROSS classes, not restart all decisions first.
	for _, item := range first[:3] {
		if err := db.MarkProviderPolicyAttempt(ctx, item.Kind+"/"+item.ID, lease, 1002); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	next, err := db.ProviderPolicyFrontier(ctx)
	if err != nil || len(next) != 64 || next[0].Kind != "policy" || next[0].ID != "slow-decision-02" {
		t.Fatalf("mixed retry order lost on SQLite reopen: %+v %v", next, err)
	}
	if err := db.PutProviderPolicyEvent(ctx, "new-independent-success", `{}`); err != nil {
		t.Fatal(err)
	}
	next, err = db.ProviderPolicyFrontier(ctx)
	if err != nil || next[0].Kind != "event" || next[0].ID != "new-independent-success" {
		t.Fatalf("previous slow decisions can starve a new event across repeated budgets: %+v %v", next, err)
	}
	if err := db.CompleteProviderPolicyEvent(ctx, next[0].ID); err != nil {
		t.Fatal(err)
	}
	next, err = db.ProviderPolicyFrontier(ctx)
	if err != nil || next[0].ID != "slow-decision-02" {
		t.Fatal("completion remained active or unattempted decisions were dropped")
	}
}

// Persistence-only regression: deliberately seeded crash records are not
// fabricated successful monetary observations or actual Core acknowledgements.
func TestProviderPolicyRecoverySkipsAbandonedRunningHead(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "provider-policy.sqlite")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	lease, err := db.AcquireCredentialPolicyLease(ctx, "00000000-0000-4000-8000-000000000001", 1000, 30000)
	if err != nil {
		t.Fatal(err)
	}
	policy := store.ProviderPolicyRecord{ID: "controlled-policy", Generation: 1, Document: `{"enabled":true}`}
	if err := db.SaveProviderPolicy(ctx, policy, store.ProviderPolicyRecord{ID: policy.ID}, lease, 1001); err != nil {
		t.Fatal(err)
	}
	terminal := ""
	for i := 0; i < 65; i++ {
		id := fmt.Sprintf("controlled-crash-attempt-%02d", i)
		state := "running"
		if i == 64 {
			state = "complete"
			terminal = id
		}
		if err := db.SaveOfficialBalanceJob(ctx, id, "controlled-source", fmt.Sprintf(`{"id":%q,"state":%q}`, id, state)); err != nil {
			t.Fatal(err)
		}
		if err := db.CaptureProviderPolicy(ctx, store.ProviderPolicyCapture{SubjectID: policy.ID, AttemptID: id, Generation: 1, Document: `{"query":{"version":1}}`}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	captures, err := db.ProviderPolicyUnfinishedCaptures(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(captures) != 1 || captures[0].AttemptID != terminal {
		t.Fatalf("terminal65 starved behind interrupted jobs: %+v", captures)
	}
	if err := db.PutProviderPolicyEvent(ctx, terminal, `{"completion":"original"}`); err != nil {
		t.Fatal(err)
	}
	if err := db.PutProviderPolicyEvent(ctx, terminal, `{"completion":"duplicate"}`); err != nil {
		t.Fatal(err)
	}
	events, err := db.ProviderPolicyEvents(ctx)
	if err != nil || len(events) != 1 || events[0].Document != `{"completion":"original"}` {
		t.Fatalf("completion not durable/deduplicated: %+v %v", events, err)
	}
	if err := db.CompleteProviderPolicyEvent(ctx, terminal); err != nil {
		t.Fatal(err)
	}
	if err := db.PutProviderPolicyEvent(ctx, terminal, `{"completion":"late"}`); err != nil {
		t.Fatal(err)
	}
	events, err = db.ProviderPolicyEvents(ctx)
	if err != nil || len(events) != 0 {
		t.Fatal("late duplicate reopened a consumed event")
	}
	captures, err = db.ProviderPolicyUnfinishedCaptures(ctx)
	if err != nil || len(captures) != 0 {
		t.Fatal("completed event was recovered a second time")
	}
}

func TestProviderPolicyDecisionJournalAndFairRetryAreLeaseFenced(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "provider-history.sqlite")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	lease, err := db.AcquireCredentialPolicyLease(ctx, "00000000-0000-4000-8000-000000000001", 1000, 30000)
	if err != nil {
		t.Fatal(err)
	}
	previous := store.ProviderPolicyRecord{ID: "controlled-subject", Generation: 1, Document: `{"enabled":true}`}
	if err := db.SaveProviderPolicy(ctx, previous, store.ProviderPolicyRecord{ID: previous.ID}, lease, 1001); err != nil {
		t.Fatal(err)
	}
	evidence := strings.Repeat("a", 64)
	pending := fmt.Sprintf(`{"enabled":true,"pending":{"evidence":%q,"action":"pause","attemptId":"controlled"}}`, evidence)
	if err := db.ReviseProviderPolicy(ctx, previous, pending, lease, 1002); err != nil {
		t.Fatal(err)
	}
	previous.Document = pending
	inspection, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer inspection.Close()
	var state, snapshot string
	if err := inspection.QueryRow(`SELECT outcome,document FROM provider_policy_decisions WHERE id=?`, evidence).Scan(&state, &snapshot); err != nil || state != "pending" {
		t.Fatalf("missing durable decision: %s %v", state, err)
	}
	if err := db.ReviseProviderPolicy(ctx, previous, `{"enabled":true}`, lease, 31000, "retired_unattempted"); !errors.Is(err, store.ErrProviderPolicyConflict) {
		t.Fatalf("expired authority changed decision: %v", err)
	}
	if err := db.ReviseProviderPolicy(ctx, previous, `{"enabled":true}`, lease, 1003, "retired_unattempted"); err != nil {
		t.Fatal(err)
	}
	var retained string
	if err := inspection.QueryRow(`SELECT outcome,document FROM provider_policy_decisions WHERE id=?`, evidence).Scan(&state, &retained); err != nil || state != "retired_unattempted" || retained != snapshot {
		t.Fatal("retirement deleted or rewrote the original decision evidence")
	}
	for i := 0; i < 65; i++ {
		if err := db.PutProviderPolicyEvent(ctx, fmt.Sprintf("controlled-fair-%02d", i), `{}`); err != nil {
			t.Fatal(err)
		}
	}
	first, err := db.ProviderPolicyEvents(ctx)
	if err != nil || len(first) != 64 {
		t.Fatal("expected bounded first page")
	}
	for _, event := range first {
		if err := db.MarkProviderPolicyAttempt(ctx, "event/"+event.ID, lease, 1004); err != nil {
			t.Fatal(err)
		}
	}
	next, err := db.ProviderPolicyEvents(ctx)
	if err != nil || len(next) == 0 || next[0].ID != "controlled-fair-64" {
		t.Fatal("failed head permanently starved the 65th independent event")
	}
	if err := db.MarkProviderPolicyAttempt(ctx, "event/"+next[0].ID, lease, 31000); !errors.Is(err, store.ErrPolicyLease) {
		t.Fatal("expired lease modified retry ordering")
	}
}
