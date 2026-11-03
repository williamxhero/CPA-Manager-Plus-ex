package store

import (
	"context"
	"database/sql"
	"encoding/json"
)

// OfficialBalanceFence is host-private source metadata, never a caller permit.
type OfficialBalanceFence struct {
	SourceID string `json:"sourceId"`
	Version  int64  `json:"version"`
	QueryID  string `json:"queryId"`
}

type rowReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func validateOfficialBalanceFences(ctx context.Context, db rowReader, fences []OfficialBalanceFence) error {
	if len(fences) == 0 || len(fences) > 63 {
		return ErrBalanceConflict
	}
	seen := map[string]bool{}
	for _, f := range fences {
		if f.SourceID == "" || f.Version <= 0 || f.QueryID == "" || seen[f.SourceID] {
			return ErrBalanceConflict
		}
		seen[f.SourceID] = true
	}
	raw, err := json.Marshal(fences)
	if err != nil {
		return err
	}
	var count int
	// One SQLite statement closes ALL source versions/attempts at a single
	// snapshot after network validation. No per-source I/O gap remains here.
	err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM json_each(?) f JOIN official_balance_sources s ON s.id=json_extract(f.value,'$.sourceId') WHERE s.version=json_extract(f.value,'$.version') AND s.secret_cipher<>'' AND json_extract(s.attempt,'$.queryId')=json_extract(f.value,'$.queryId') AND json_extract(s.attempt,'$.queryStatus')='success' AND json_extract(s.last_success,'$.queryId')=json_extract(f.value,'$.queryId')`, string(raw)).Scan(&count)
	if err != nil {
		return err
	}
	if count != len(fences) {
		return ErrBalanceConflict
	}
	return nil
}

func (s *Store) ValidateOfficialBalanceFences(ctx context.Context, fences []OfficialBalanceFence) error {
	return validateOfficialBalanceFences(ctx, s.db, fences)
}
