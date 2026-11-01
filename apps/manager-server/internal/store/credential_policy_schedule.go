package store

import (
	"context"
	"database/sql"
)

// ActivateCredentialPolicyScheduleHints is the scheduler-cycle cutoff. The
// first write takes the SQLite writer reservation: ON commits linearize before
// this transaction (included), or after it (deferred to the next cycle).
// Failed inventory/admission leaves the exact source due for a later cycle.
// This is scheduling only, never a new observation, Started fence or gate ACK.
func (s *Store) ActivateCredentialPolicyScheduleHints(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `DELETE FROM full_refresh_schedule WHERE id IN (SELECT h.source_key FROM credential_policy_schedule_hints h JOIN credential_policies p ON p.id=h.subject_id AND p.generation=h.generation JOIN credential_policy_identity i ON i.singleton=1 WHERE json_extract(p.document,'$.enabled')=1 AND json_extract(p.document,'$.owner')='cpa-manager.credential-policy.v1/'||i.installation_id)`)
	if err != nil {
		return err
	}
	// Same snapshot: obsolete OFF/generation/foreign-owner hints are discarded,
	// not activated. Concurrent new intent cannot be consumed by this cutoff.
	if _, err = tx.ExecContext(ctx, `DELETE FROM credential_policy_schedule_hints`); err != nil {
		return err
	}
	return tx.Commit()
}

// Called ONLY in the original trusted running-Job capture transaction. An
// already-started current-generation acquisition covers this work hint BEFORE
// it can complete/earn scheduling credit. Late old-generation jobs cannot.
func dischargeCredentialPolicyScheduleHint(ctx context.Context, tx *sql.Tx, subject string, generation int64, key string) error {
	if key == "" {
		return nil
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM credential_policy_schedule_hints WHERE subject_id=? AND generation=? AND source_key=? AND EXISTS(SELECT 1 FROM credential_policies p JOIN credential_policy_identity i ON i.singleton=1 WHERE p.id=credential_policy_schedule_hints.subject_id AND p.generation=credential_policy_schedule_hints.generation AND json_extract(p.document,'$.enabled')=1 AND json_extract(p.document,'$.owner')='cpa-manager.credential-policy.v1/'||i.installation_id)`, subject, generation, key)
	return err
}
