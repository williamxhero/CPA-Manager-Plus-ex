package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
)

var ErrPolicyLease = errors.New("credential policy coordinator lease unavailable")

// Holder is ephemeral process authority, distinct from the persistent gate owner.
// Epoch persists across release/restart; every side effect must recheck it.
type CredentialPolicyLease struct {
	Holder    string
	Epoch     int64
	ExpiresMS int64
}

func (s *Store) AcquireCredentialPolicyLease(ctx context.Context, holder string, now, ttl int64) (CredentialPolicyLease, error) {
	lease := CredentialPolicyLease{Holder: holder}
	if id, err := uuid.Parse(holder); err != nil || id.String() != holder || now <= 0 || ttl <= 0 || ttl > 60000 || now > 9007199254740991-ttl {
		return lease, ErrPolicyLease
	}
	err := s.db.QueryRowContext(ctx, `INSERT INTO credential_policy_lease(singleton,holder,epoch,expires_ms) VALUES(1,?,1,?) ON CONFLICT(singleton) DO UPDATE SET holder=excluded.holder,epoch=credential_policy_lease.epoch+1,expires_ms=excluded.expires_ms WHERE credential_policy_lease.expires_ms<=? OR credential_policy_lease.holder=? RETURNING epoch,expires_ms`, holder, now+ttl, now, holder).Scan(&lease.Epoch, &lease.ExpiresMS)
	if errors.Is(err, sql.ErrNoRows) {
		return lease, ErrPolicyLease
	}
	return lease, err
}
func (s *Store) CheckCredentialPolicyLease(ctx context.Context, lease CredentialPolicyLease, now int64) error {
	var valid bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM credential_policy_lease WHERE singleton=1 AND holder=? AND epoch=? AND expires_ms>?)`, lease.Holder, lease.Epoch, now).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return ErrPolicyLease
	}
	return nil
}
func (s *Store) ReleaseCredentialPolicyLease(ctx context.Context, lease CredentialPolicyLease) error {
	result, err := s.db.ExecContext(ctx, `UPDATE credential_policy_lease SET holder='',expires_ms=0 WHERE singleton=1 AND holder=? AND epoch=?`, lease.Holder, lease.Epoch)
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
