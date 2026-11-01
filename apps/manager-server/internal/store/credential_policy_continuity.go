package store

import (
	"context"
	"encoding/json"
)

func (s *Store) CredentialPolicies(ctx context.Context) ([]CredentialPolicyRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,generation,document FROM credential_policies ORDER BY rowid DESC LIMIT 10000`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CredentialPolicyRecord
	for rows.Next() {
		var r CredentialPolicyRecord
		if err := rows.Scan(&r.ID, &r.Generation, &r.Document); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Caller must verify an ACTUAL certified exact-target native refresh chain and
// re-read current authority first. This commits intent only, never quota evidence
// or a Core clear; it cannot overwrite an independently stored new-lifecycle intent.
func (s *Store) MigrateCredentialPolicyAuthority(ctx context.Context, previous, next CredentialPolicyRecord, tombstone string) error {
	type intent struct {
		Owner   string `json:"owner"`
		Enabled *bool  `json:"enabled"`
	}
	var a, b, tomb intent
	if previous.ID == "" || next.ID == "" || previous.Generation < 1 || previous.Generation >= 9007199254740991 || next.Generation != previous.Generation+1 || json.Unmarshal([]byte(previous.Document), &a) != nil || json.Unmarshal([]byte(next.Document), &b) != nil || json.Unmarshal([]byte(tombstone), &tomb) != nil || a.Enabled == nil || b.Enabled == nil || tomb.Enabled == nil || *a.Enabled != *b.Enabled || *tomb.Enabled || a.Owner == "" || a.Owner != b.Owner || a.Owner != tomb.Owner {
		return ErrPolicyConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	document := next.Document
	if previous.ID != next.ID {
		document = tombstone
	}
	result, err := tx.ExecContext(ctx, `UPDATE credential_policies SET generation=?,document=? WHERE id=? AND generation=? AND document=?`, next.Generation, document, previous.ID, previous.Generation, previous.Document)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrPolicyConflict
	}
	if previous.ID != next.ID {
		result, err = tx.ExecContext(ctx, `INSERT INTO credential_policies(id,generation,document) VALUES(?,?,?) ON CONFLICT(id) DO NOTHING`, next.ID, next.Generation, next.Document)
		if err != nil {
			return err
		}
		n, err = result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrPolicyConflict
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE credential_policy_operations SET state='superseded',revision=revision+1,last_error=CASE WHEN attempted=1 THEN 'outcome_unverified' ELSE 'source_authority_changed' END WHERE subject_id=? AND automatic=1 AND state IN ('queued','pending','error')`, previous.ID); err != nil {
		return err
	}
	return tx.Commit()
}
