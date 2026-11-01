package store

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
)

// Compact classified evidence/issued references only, not credentials/raw HTTP.
// Durable inbox decouples host completion and query budgets from gate RPC waits.
type CredentialPolicyEvent struct {
	ID       string
	Document string
	State    string
	Reason   string
}

func (s *Store) CredentialPolicyEvent(ctx context.Context, id string) (CredentialPolicyEvent, error) {
	var event CredentialPolicyEvent
	err := s.db.QueryRowContext(ctx, `SELECT id,document,state,reason FROM credential_policy_events WHERE id=?`, id).Scan(&event.ID, &event.Document, &event.State, &event.Reason)
	return event, err
}

func (s *Store) PutCredentialPolicyEvent(ctx context.Context, event CredentialPolicyEvent) error {
	id, err := uuid.Parse(event.ID)
	if err != nil || id.String() != event.ID || len(event.Document) > 128<<10 || !json.Valid([]byte(event.Document)) {
		return ErrPolicyOperationConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO credential_policy_events(id,document,state) VALUES(?,?,'pending') ON CONFLICT(id) DO NOTHING`, event.ID, event.Document); err != nil {
		return err
	}
	var previous string
	if err = tx.QueryRowContext(ctx, `SELECT document FROM credential_policy_events WHERE id=?`, event.ID).Scan(&previous); err != nil {
		return err
	}
	if previous != event.Document {
		return ErrPolicyOperationConflict
	}
	return tx.Commit()
}

func (s *Store) PendingCredentialPolicyEvents(ctx context.Context) ([]CredentialPolicyEvent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,document,state,reason FROM credential_policy_events WHERE state='pending' ORDER BY rowid LIMIT 64`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []CredentialPolicyEvent{}
	for rows.Next() {
		var event CredentialPolicyEvent
		if err := rows.Scan(&event.ID, &event.Document, &event.State, &event.Reason); err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	return result, rows.Err()
}

func (s *Store) FinishCredentialPolicyEvent(ctx context.Context, id, reason string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE credential_policy_events SET state='complete',reason=? WHERE id=? AND state='pending'`, reason, id)
	return err
}
