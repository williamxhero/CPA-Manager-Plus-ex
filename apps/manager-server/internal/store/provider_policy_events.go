package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

func (s *Store) ProviderPolicies(ctx context.Context) ([]ProviderPolicyRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT p.id,p.generation,p.document FROM provider_policies p LEFT JOIN provider_policy_attempts a ON a.id='policy/'||p.id ORDER BY COALESCE(a.position,0),p.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProviderPolicyRecord{}
	for rows.Next() {
		var r ProviderPolicyRecord
		if err := rows.Scan(&r.ID, &r.Generation, &r.Document); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type ProviderPolicyCapture struct {
	Seq                  int64
	SubjectID, AttemptID string
	Generation           int64
	Document             string
}

func (s *Store) CaptureProviderPolicy(ctx context.Context, r ProviderPolicyCapture) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO provider_policy_captures(subject_id,attempt_id,generation,document) SELECT ?,?,?,? WHERE EXISTS(SELECT 1 FROM provider_policies WHERE id=? AND generation=? AND json_extract(document,'$.enabled')=1) ON CONFLICT(subject_id,attempt_id) DO NOTHING`, r.SubjectID, r.AttemptID, r.Generation, r.Document, r.SubjectID, r.Generation)
	return err
}

func (s *Store) ProviderPolicyCapture(ctx context.Context, subject, attempt string) (ProviderPolicyCapture, error) {
	r := ProviderPolicyCapture{SubjectID: subject, AttemptID: attempt}
	err := s.db.QueryRowContext(ctx, `SELECT seq,generation,document FROM provider_policy_captures WHERE subject_id=? AND attempt_id=?`, subject, attempt).Scan(&r.Seq, &r.Generation, &r.Document)
	return r, err
}

func (s *Store) PutProviderPolicyEvent(ctx context.Context, id, document string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO provider_policy_events(id,document) VALUES(?,?) ON CONFLICT(id) DO NOTHING`, id, document)
	return err
}

type ProviderPolicyEvent struct{ ID, Document string }

type ProviderPolicyWork struct {
	Kind, ID, Document string
	Generation         int64
}

// One durable frontier covers BOTH pending decisions and completion events.
// Separate per-kind queues can starve events when slow decisions exhaust every
// reconciliation budget before the second queue is even reached.
func (s *Store) ProviderPolicyFrontier(ctx context.Context) ([]ProviderPolicyWork, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT w.kind,w.id,w.document,w.generation FROM (
		SELECT 'policy' AS kind,id,document,generation FROM provider_policies WHERE json_type(document,'$.pending')='object'
		UNION ALL SELECT 'event' AS kind,id,document,0 AS generation FROM provider_policy_events WHERE done=0
	) w LEFT JOIN provider_policy_attempts a ON a.id=w.kind||'/'||w.id ORDER BY COALESCE(a.position,0),w.kind,w.id LIMIT 64`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	work := []ProviderPolicyWork{}
	for rows.Next() {
		var item ProviderPolicyWork
		if err := rows.Scan(&item.Kind, &item.ID, &item.Document, &item.Generation); err != nil {
			return nil, err
		}
		work = append(work, item)
	}
	return work, rows.Err()
}

func (s *Store) ProviderPolicyEvents(ctx context.Context) ([]ProviderPolicyEvent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT e.id,e.document FROM provider_policy_events e LEFT JOIN provider_policy_attempts a ON a.id='event/'||e.id WHERE e.done=0 ORDER BY COALESCE(a.position,0),e.rowid LIMIT 64`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProviderPolicyEvent{}
	for rows.Next() {
		var r ProviderPolicyEvent
		if err := rows.Scan(&r.ID, &r.Document); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) CompleteProviderPolicyEvent(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE provider_policy_events SET done=1 WHERE id=?`, id)
	return err
}

// Same-generation decision updates are document-CAS and lease fenced. No
// successful decision can overwrite a concurrent switch or a different event.
func (s *Store) ReviseProviderPolicy(ctx context.Context, previous ProviderPolicyRecord, document string, lease CredentialPolicyLease, now int64, outcome ...string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE provider_policies SET document=? WHERE id=? AND generation=? AND document=? AND EXISTS(SELECT 1 FROM credential_policy_lease WHERE singleton=1 AND holder=? AND epoch=? AND expires_ms>?)`, document, previous.ID, previous.Generation, previous.Document, lease.Holder, lease.Epoch, now)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrProviderPolicyConflict
	}
	// Decision authority is journaled in the same transaction as the policy CAS.
	// Terminal transitions retain the immutable evidence snapshot instead of
	// deleting the only audit record of a conclusively unattempted decision.
	var before, after struct {
		Pending json.RawMessage `json:"pending"`
	}
	if json.Unmarshal([]byte(previous.Document), &before) != nil || json.Unmarshal([]byte(document), &after) != nil {
		return ErrProviderPolicyConflict
	}
	save := func(raw json.RawMessage, state string) error {
		if len(raw) == 0 || string(raw) == "null" {
			return nil
		}
		var d struct {
			Evidence string `json:"evidence"`
		}
		if json.Unmarshal(raw, &d) != nil || len(d.Evidence) != 64 {
			return ErrProviderPolicyConflict
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO provider_policy_decisions(id,subject_id,document,outcome) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET outcome=excluded.outcome`, d.Evidence, previous.ID, string(raw), state)
		return err
	}
	if len(after.Pending) > 0 && string(after.Pending) != "null" {
		if err := save(after.Pending, "pending"); err != nil {
			return err
		}
	} else {
		state := "retired_unattempted"
		if len(outcome) > 0 {
			state = outcome[0]
		}
		if err := save(before.Pending, state); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Failed local work rotates behind never-attempted work. This durable order is
// independent of wall-clock rollback and cannot turn a retry into fresh evidence.
func (s *Store) MarkProviderPolicyAttempt(ctx context.Context, id string, lease CredentialPolicyLease, now int64) error {
	result, err := s.db.ExecContext(ctx, `INSERT INTO provider_policy_attempts(id,position) SELECT ?,(SELECT COALESCE(MAX(position),0)+1 FROM provider_policy_attempts) WHERE EXISTS(SELECT 1 FROM credential_policy_lease WHERE singleton=1 AND holder=? AND epoch=? AND expires_ms>?) ON CONFLICT(id) DO UPDATE SET position=excluded.position`, id, lease.Holder, lease.Epoch, now)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrPolicyLease
	}
	return nil
}

// Started fences plus durable source jobs allow a scheduler to recover a lost
// completion notification after process death; it never fabricates a query.
func (s *Store) ProviderPolicyUnfinishedCaptures(ctx context.Context) ([]ProviderPolicyCapture, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT c.seq,c.subject_id,c.attempt_id,c.generation,c.document FROM provider_policy_captures c JOIN official_balance_jobs j ON j.id=c.attempt_id LEFT JOIN provider_policy_events e ON e.id=c.attempt_id WHERE e.id IS NULL AND json_valid(j.document) AND json_extract(j.document,'$.state') IN ('complete','cancelled','canceled','interrupted') ORDER BY c.seq LIMIT 64`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProviderPolicyCapture{}
	for rows.Next() {
		var r ProviderPolicyCapture
		if err := rows.Scan(&r.Seq, &r.SubjectID, &r.AttemptID, &r.Generation, &r.Document); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func IsProviderPolicyCaptureMissing(err error) bool { return errors.Is(err, sql.ErrNoRows) }
