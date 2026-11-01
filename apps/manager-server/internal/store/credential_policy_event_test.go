package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

func TestCredentialPolicyCompletionInboxSurvivesRestartWithoutReopeningDuplicates(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "policy.sqlite")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	event := store.CredentialPolicyEvent{ID: uuid.NewString(), Document: `{"decision":"pending"}`}
	if err := db.PutCredentialPolicyEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err := db.PutCredentialPolicyEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := db.PendingCredentialPolicyEvents(ctx)
	if err != nil || len(rows) != 1 || rows[0].Document != event.Document {
		t.Fatal("completion lost before operation journal dispatch")
	}
	changed := event
	changed.Document = `{"decision":"changed"}`
	if err := db.PutCredentialPolicyEvent(ctx, changed); !errors.Is(err, store.ErrPolicyOperationConflict) {
		t.Fatal("same attempt acquired different evidence")
	}
	if err := db.FinishCredentialPolicyEvent(ctx, event.ID, "policy_generation_changed"); err != nil {
		t.Fatal(err)
	}
	if err := db.PutCredentialPolicyEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	rows, err = db.PendingCredentialPolicyEvents(ctx)
	if err != nil || len(rows) != 0 {
		t.Fatal("duplicate completion reopened finished event")
	}
}
