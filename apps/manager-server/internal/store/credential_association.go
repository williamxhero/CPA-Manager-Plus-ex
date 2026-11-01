package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
)

// All fields are private host references/contracts, never plaintext credentials,
// candidate MACs or HTTP output. SecretRef is the existing74 encrypted resolver CAS.
type CredentialAssociationCapture struct {
	ID               string
	JobID            string
	SourceID         string
	ConfigVersion    int64
	Config           string
	SecretRef        string
	PolicyID         string
	PolicyGeneration int64
	PolicyDocument   string
	Job              string
	Document         string
}

func (s *Store) CredentialAssociationCapture(ctx context.Context, id string) (CredentialAssociationCapture, error) {
	var doc string
	var c CredentialAssociationCapture
	err := s.db.QueryRowContext(ctx, `SELECT document FROM credential_association_captures WHERE id=?`, id).Scan(&doc)
	if err == nil && json.Unmarshal([]byte(doc), &c) != nil {
		err = ErrPolicyOperationConflict
	}
	return c, err
}
func checkAssociationTx(ctx context.Context, tx *sql.Tx, c CredentialAssociationCapture, complete bool) error {
	var version int64
	var config, ref, cipher, attempt string
	if err := tx.QueryRowContext(ctx, `SELECT version,config,secret_ref,secret_cipher,attempt FROM official_balance_sources WHERE id=?`, c.SourceID).Scan(&version, &config, &ref, &cipher, &attempt); err != nil {
		return err
	}
	if version != c.ConfigVersion || config != c.Config || ref != c.SecretRef || cipher == "" {
		return ErrBalanceConflict
	}
	var generation int64
	var document string
	err := tx.QueryRowContext(ctx, `SELECT generation,document FROM credential_policies WHERE id=?`, c.PolicyID).Scan(&generation, &document)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if generation != c.PolicyGeneration || document != c.PolicyDocument {
		return ErrPolicyConflict
	}
	var job string
	if err := tx.QueryRowContext(ctx, `SELECT document FROM official_balance_jobs WHERE id=? AND source_id=?`, c.JobID, c.SourceID).Scan(&job); err != nil {
		return err
	}
	var original, current struct {
		ID      string          `json:"id"`
		Version int64           `json:"version"`
		State   string          `json:"state"`
		Binding json.RawMessage `json:"binding"`
		Status  *struct {
			Attempt *struct {
				QueryID string `json:"queryId"`
			} `json:"attempt"`
		} `json:"status"`
	}
	if json.Unmarshal([]byte(c.Job), &original) != nil || json.Unmarshal([]byte(job), &current) != nil || original.ID != c.JobID || original.Version != c.ConfigVersion || original.State != "running" || current.ID != c.JobID || current.Version != c.ConfigVersion || string(original.Binding) != string(current.Binding) {
		return ErrBalanceConflict
	}
	if !complete {
		if job != c.Job {
			return ErrBalanceConflict
		}
		return nil
	}
	var observed struct {
		QueryID     string `json:"queryId"`
		QueryStatus string `json:"queryStatus"`
	}
	if json.Unmarshal([]byte(attempt), &observed) != nil || observed.QueryID != c.JobID || observed.QueryStatus != "success" || current.State != "complete" || current.Status == nil || current.Status.Attempt == nil || current.Status.Attempt.QueryID != c.JobID {
		return ErrBalanceConflict
	}
	return nil
}
func (s *Store) PutCredentialAssociationCapture(ctx context.Context, c CredentialAssociationCapture, scheduleKey ...string) error {
	if len(scheduleKey) > 1 {
		return ErrPolicyConflict
	}
	parsed, err := uuid.Parse(c.ID)
	if err != nil || parsed.String() != c.ID || !json.Valid([]byte(c.Document)) || len(c.Document) > 32<<10 {
		return ErrPolicyOperationConflict
	}
	encoded, err := json.Marshal(c)
	if err != nil || len(encoded) > 128<<10 {
		return ErrPolicyOperationConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := checkAssociationTx(ctx, tx, c, false); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO credential_association_captures(id,job_id,document) VALUES(?,?,?) ON CONFLICT(id) DO NOTHING`, c.ID, c.JobID, string(encoded)); err != nil {
		return err
	}
	var previous string
	if err := tx.QueryRowContext(ctx, `SELECT document FROM credential_association_captures WHERE id=?`, c.ID).Scan(&previous); err != nil {
		return err
	}
	if previous != string(encoded) {
		return ErrPolicyOperationConflict
	}
	if len(scheduleKey) == 1 {
		if err := dischargeCredentialPolicyScheduleHint(ctx, tx, c.PolicyID, c.PolicyGeneration, scheduleKey[0]); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) CheckCredentialAssociationCapture(ctx context.Context, c CredentialAssociationCapture, complete bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var doc string
	if err := tx.QueryRowContext(ctx, `SELECT document FROM credential_association_captures WHERE id=?`, c.ID).Scan(&doc); err != nil {
		return err
	}
	encoded, _ := json.Marshal(c)
	if doc != string(encoded) {
		return ErrPolicyOperationConflict
	}
	if err := checkAssociationTx(ctx, tx, c, complete); err != nil {
		return err
	}
	return tx.Commit()
}
