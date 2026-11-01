package store

import (
	"context"
	"database/sql"
	"encoding/json"
)

// Service-validated, secret-free proof/evidence. Persistence is not a Core ACK.
type CredentialAssociationEvidence struct{ CaptureID, Document string }

func checkAcceptedAssociationTx(ctx context.Context, tx *sql.Tx, id, document string) (CredentialAssociationCapture, error) {
	var capture CredentialAssociationCapture
	var raw, accepted string
	if err := tx.QueryRowContext(ctx, `SELECT c.document,e.document FROM credential_association_captures c JOIN credential_association_evidence e ON e.capture_id=c.id WHERE c.id=?`, id).Scan(&raw, &accepted); err != nil {
		return capture, err
	}
	if json.Unmarshal([]byte(raw), &capture) != nil || capture.ID != id || document != "" && accepted != document {
		return capture, ErrPolicyOperationConflict
	}
	return capture, checkAssociationTx(ctx, tx, capture, true)
}

// Accept atomically rechecks the original capture, literal profile/secret CAS,
// exact completed job/attempt and original policy generation before commit.
func (s *Store) AcceptCredentialAssociationEvidence(ctx context.Context, c CredentialAssociationCapture, document string) error {
	if len(document) == 0 || len(document) > 32<<10 || !json.Valid([]byte(document)) {
		return ErrPolicyOperationConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var original string
	if err := tx.QueryRowContext(ctx, `SELECT document FROM credential_association_captures WHERE id=?`, c.ID).Scan(&original); err != nil {
		return err
	}
	encoded, _ := json.Marshal(c)
	if original != string(encoded) {
		return ErrPolicyOperationConflict
	}
	if err := checkAssociationTx(ctx, tx, c, true); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO credential_association_evidence(capture_id,document) VALUES(?,?) ON CONFLICT(capture_id) DO NOTHING`, c.ID, document); err != nil {
		return err
	}
	var prior string
	if err := tx.QueryRowContext(ctx, `SELECT document FROM credential_association_evidence WHERE capture_id=?`, c.ID).Scan(&prior); err != nil {
		return err
	}
	if prior != document {
		return ErrPolicyOperationConflict
	}
	return tx.Commit()
}

func (s *Store) CredentialAssociationEvidenceForPolicy(ctx context.Context, id string) ([]CredentialAssociationEvidence, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT e.capture_id,e.document FROM credential_association_evidence e JOIN credential_association_captures c ON c.id=e.capture_id WHERE json_extract(c.document,'$.PolicyID')=? ORDER BY e.rowid DESC LIMIT 64`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CredentialAssociationEvidence
	for rows.Next() {
		var r CredentialAssociationEvidence
		if err := rows.Scan(&r.CaptureID, &r.Document); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) SaveCredentialPolicyWithAssociation(ctx context.Context, r CredentialPolicyRecord, expected int64, e CredentialAssociationEvidence) error {
	return s.saveCredentialPolicy(ctx, r, expected, func(tx *sql.Tx) error {
		capture, err := checkAcceptedAssociationTx(ctx, tx, e.CaptureID, e.Document)
		if err != nil {
			return err
		}
		if capture.PolicyID != r.ID || capture.PolicyGeneration != expected {
			return ErrPolicyConflict
		}
		return nil
	})
}
