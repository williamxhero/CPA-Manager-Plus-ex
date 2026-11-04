package store

import (
	"context"
	"database/sql"
	"errors"
)

// WebBrowserHostAttestation is host-local Profile 1 ↔ instance proof. It is
// never written by management HTTP enrollment registration.
type WebBrowserHostAttestation struct {
	ProofReference string
	Family         string
	Profile        string
	InstanceID     string
	ExecutionUser  string
	EvidenceKind   string
	VerifiedAtMS   int64
}

func (s *Store) SaveWebBrowserHostAttestation(ctx context.Context, value WebBrowserHostAttestation) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO official_balance_web_browser_attestations(
		proof_reference,family,profile,instance_id,execution_user,evidence_kind,verified_at_ms)
		VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(proof_reference) DO UPDATE SET
			family=excluded.family,profile=excluded.profile,instance_id=excluded.instance_id,
			execution_user=excluded.execution_user,evidence_kind=excluded.evidence_kind,
			verified_at_ms=excluded.verified_at_ms`,
		value.ProofReference, value.Family, value.Profile, value.InstanceID, value.ExecutionUser, value.EvidenceKind, value.VerifiedAtMS)
	return err
}

func (s *Store) WebBrowserHostAttestation(ctx context.Context, family, profile, instanceID, executionUser string) (WebBrowserHostAttestation, error) {
	var out WebBrowserHostAttestation
	err := s.db.QueryRowContext(ctx, `SELECT proof_reference,family,profile,instance_id,execution_user,evidence_kind,verified_at_ms
		FROM official_balance_web_browser_attestations
		WHERE family=? AND profile=? AND instance_id=? AND execution_user=?
		ORDER BY verified_at_ms DESC LIMIT 1`,
		family, profile, instanceID, executionUser).Scan(
		&out.ProofReference, &out.Family, &out.Profile, &out.InstanceID, &out.ExecutionUser, &out.EvidenceKind, &out.VerifiedAtMS)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrBalanceConflict
	}
	return out, err
}

func (s *Store) ClearWebBrowserHostAttestations(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM official_balance_web_browser_attestations`)
	return err
}
