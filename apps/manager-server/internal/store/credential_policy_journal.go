package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
)

var ErrPolicyOperationConflict = errors.New("credential policy operation conflict")

type CredentialPolicyOperation struct {
	ID         string `json:"opId"`
	SubjectID  string `json:"subjectId"`
	Generation int64  `json:"policyGeneration"`
	Owner      string `json:"owner"`
	Automatic  bool   `json:"automatic"`
	State      string `json:"state"`
	Body       string `json:"-"`
	Result     string `json:"-"`
	LastError  string `json:"error,omitempty"`
	Revision   int64  `json:"revision"`
	Attempted  bool   `json:"attempted"`
}

const policyOperationColumns = `id,subject_id,generation,owner,automatic,state,body,result,last_error,revision,attempted`

type policyRow interface{ Scan(...any) error }

func scanPolicyOperation(row policyRow) (r CredentialPolicyOperation, err error) {
	err = row.Scan(&r.ID, &r.SubjectID, &r.Generation, &r.Owner, &r.Automatic, &r.State, &r.Body, &r.Result, &r.LastError, &r.Revision, &r.Attempted)
	return
}
func (s *Store) CredentialPolicyOperation(ctx context.Context, id string) (CredentialPolicyOperation, error) {
	return scanPolicyOperation(s.db.QueryRowContext(ctx, `SELECT `+policyOperationColumns+` FROM credential_policy_operations WHERE id=?`, id))
}

func (s *Store) LatestCredentialPolicyOperation(ctx context.Context, subject string) (CredentialPolicyOperation, error) {
	r, err := scanPolicyOperation(s.db.QueryRowContext(ctx, `SELECT `+policyOperationColumns+` FROM credential_policy_operations WHERE subject_id=? ORDER BY rowid DESC LIMIT 1`, subject))
	if errors.Is(err, sql.ErrNoRows) {
		return CredentialPolicyOperation{}, nil
	}
	return r, err
}

func (s *Store) PendingCredentialPolicyOperations(ctx context.Context) ([]CredentialPolicyOperation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+policyOperationColumns+` FROM credential_policy_operations WHERE state IN ('queued','pending','error') ORDER BY rowid LIMIT 64`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CredentialPolicyOperation{}
	for rows.Next() {
		op, err := scanPolicyOperation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	return out, rows.Err()
}
func policyOperationGuard(ctx context.Context, tx *sql.Tx, op CredentialPolicyOperation, lease CredentialPolicyLease, now int64) error {
	var valid bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM credential_policy_lease WHERE singleton=1 AND holder=? AND epoch=? AND expires_ms>?)`, lease.Holder, lease.Epoch, now).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return ErrPolicyLease
	}
	var generation int64
	var owner, installation string
	var enabled bool
	err := tx.QueryRowContext(ctx, `SELECT generation,json_extract(document,'$.owner'),json_extract(document,'$.enabled'),(SELECT installation_id FROM credential_policy_identity WHERE singleton=1) FROM credential_policies WHERE id=?`, op.SubjectID).Scan(&generation, &owner, &enabled, &installation)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrPolicyConflict
	}
	if err != nil {
		return err
	}
	if generation != op.Generation || owner != op.Owner || owner != "cpa-manager.credential-policy.v1/"+installation || op.Automatic && !enabled {
		return ErrPolicyConflict
	}
	return nil
}
func (s *Store) BeginCredentialPolicyOperation(ctx context.Context, op CredentialPolicyOperation, lease CredentialPolicyLease, now int64) (CredentialPolicyOperation, error) {
	if id, err := uuid.Parse(op.ID); err != nil || id.String() != op.ID || len(op.Body) == 0 || len(op.Body) > 32<<10 {
		return op, ErrPolicyOperationConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return op, err
	}
	defer tx.Rollback()
	if err = policyOperationGuard(ctx, tx, op, lease, now); err != nil {
		return op, err
	}
	old, err := scanPolicyOperation(tx.QueryRowContext(ctx, `SELECT `+policyOperationColumns+` FROM credential_policy_operations WHERE id=?`, op.ID))
	if err == nil {
		if old.SubjectID != op.SubjectID || old.Generation != op.Generation || old.Owner != op.Owner || old.Automatic != op.Automatic || old.Body != op.Body {
			return old, ErrPolicyOperationConflict
		}
		return old, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return op, err
	}
	op.State = "queued"
	op.Revision = 1
	op.Result = ""
	op.LastError = ""
	op.Attempted = false
	_, err = tx.ExecContext(ctx, `INSERT INTO credential_policy_operations(`+policyOperationColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, op.ID, op.SubjectID, op.Generation, op.Owner, op.Automatic, op.State, op.Body, "", "", op.Revision, false)
	if err != nil {
		return op, err
	}
	return op, tx.Commit()
}
func (s *Store) CheckCredentialPolicyOperation(ctx context.Context, id string, lease CredentialPolicyLease, now int64) (CredentialPolicyOperation, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CredentialPolicyOperation{}, err
	}
	defer tx.Rollback()
	op, err := scanPolicyOperation(tx.QueryRowContext(ctx, `SELECT `+policyOperationColumns+` FROM credential_policy_operations WHERE id=?`, id))
	if err != nil {
		return op, err
	}
	if err = policyOperationGuard(ctx, tx, op, lease, now); err != nil {
		return op, err
	}
	if op.State != "queued" && op.State != "pending" && op.State != "error" {
		return op, ErrPolicyConflict
	}
	return op, tx.Commit()
}
func (s *Store) CompleteCredentialPolicyOperation(ctx context.Context, id string, revision int64, state, result, reason string, lease CredentialPolicyLease, now int64) error {
	if state != "pending" && state != "error" && state != "applied" && state != "conflict" && state != "no_action" {
		return ErrPolicyOperationConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	op, err := scanPolicyOperation(tx.QueryRowContext(ctx, `SELECT `+policyOperationColumns+` FROM credential_policy_operations WHERE id=?`, id))
	if err != nil {
		return err
	}
	if err = policyOperationGuard(ctx, tx, op, lease, now); err != nil {
		return err
	}
	if op.Revision != revision || op.State == "superseded" {
		return ErrPolicyConflict
	}
	// Only Claim marks an attempted write. A failed receipt/inventory read must
	// not freeze an unplanned envelope or invent an uncertain write outcome.
	_, err = tx.ExecContext(ctx, `UPDATE credential_policy_operations SET state=?,result=?,last_error=?,revision=revision+1 WHERE id=? AND revision=?`, state, result, reason, id, revision)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) PlanCredentialPolicyOperation(ctx context.Context, id string, revision int64, body string, lease CredentialPolicyLease, now int64) (CredentialPolicyOperation, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CredentialPolicyOperation{}, err
	}
	defer tx.Rollback()
	op, err := scanPolicyOperation(tx.QueryRowContext(ctx, `SELECT `+policyOperationColumns+` FROM credential_policy_operations WHERE id=?`, id))
	if err != nil {
		return op, err
	}
	if err = policyOperationGuard(ctx, tx, op, lease, now); err != nil {
		return op, err
	}
	if op.Attempted || !json.Valid([]byte(body)) || len(body) > 32<<10 {
		return op, ErrPolicyOperationConflict
	}
	if op.Revision != revision || op.State != "queued" && op.State != "pending" {
		return op, ErrPolicyConflict
	}
	_, err = tx.ExecContext(ctx, `UPDATE credential_policy_operations SET body=?,state='queued',revision=revision+1 WHERE id=? AND revision=?`, body, id, revision)
	if err != nil {
		return op, err
	}
	op.Body = body
	op.State = "queued"
	op.Revision++
	return op, tx.Commit()
}

// Claim is the local admission point for an automatic MANAGEMENT action. It
// commits unknown outcome before RPC. OFF supersedes all subsequent claims; an
// already claimed in-flight action remains unknown until real Core readback.
func (s *Store) ClaimCredentialPolicyOperation(ctx context.Context, id string, revision int64, lease CredentialPolicyLease, now int64) (CredentialPolicyOperation, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CredentialPolicyOperation{}, err
	}
	defer tx.Rollback()
	op, err := scanPolicyOperation(tx.QueryRowContext(ctx, `SELECT `+policyOperationColumns+` FROM credential_policy_operations WHERE id=?`, id))
	if err != nil {
		return op, err
	}
	if err = policyOperationGuard(ctx, tx, op, lease, now); err != nil {
		return op, err
	}
	if op.Revision != revision || op.State != "queued" && op.State != "pending" && op.State != "error" {
		return op, ErrPolicyConflict
	}
	_, err = tx.ExecContext(ctx, `UPDATE credential_policy_operations SET attempted=1,state='pending',last_error='outcome_unverified',revision=revision+1 WHERE id=? AND revision=?`, id, revision)
	if err != nil {
		return op, err
	}
	op.Attempted = true
	op.State = "pending"
	op.LastError = "outcome_unverified"
	op.Revision++
	return op, tx.Commit()
}

// Caller must have an explicit non-application/global-revision rejection. This
// atomic handover never rewrites the attempted body and cannot survive OFF.
func (s *Store) ReplanCredentialPolicyOperation(ctx context.Context, id string, revision int64, next CredentialPolicyOperation, lease CredentialPolicyLease, now int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	old, err := scanPolicyOperation(tx.QueryRowContext(ctx, `SELECT `+policyOperationColumns+` FROM credential_policy_operations WHERE id=?`, id))
	if err != nil {
		return err
	}
	if err := policyOperationGuard(ctx, tx, old, lease, now); err != nil {
		return err
	}
	if old.Revision != revision || old.State != "pending" || !old.Attempted {
		return ErrPolicyConflict
	}
	parsed, err := uuid.Parse(next.ID)
	if err != nil || parsed.String() != next.ID || next.ID == old.ID || next.SubjectID != old.SubjectID || next.Generation != old.Generation || next.Owner != old.Owner || next.Automatic != old.Automatic || !json.Valid([]byte(next.Body)) || len(next.Body) > 32<<10 {
		return ErrPolicyOperationConflict
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO credential_policy_operations(`+policyOperationColumns+`) VALUES(?,?,?,?,?,'queued',?,'','',1,0)`, next.ID, next.SubjectID, next.Generation, next.Owner, next.Automatic, next.Body); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE credential_policy_operations SET state='conflict',last_error='gate_cas_replanned',revision=revision+1 WHERE id=? AND revision=?`, id, revision); err != nil {
		return err
	}
	return tx.Commit()
}
