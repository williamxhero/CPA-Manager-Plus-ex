package credentialquota

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
)

var ErrRecheckCovered = errors.New("quota reset already rechecked")

// StartRecheck performs the same authorized acquisition, or joins a flight.
// The terminal fence is checked while holding the same lock as publication so
// that a finishing hourly/manual acquisition cannot start a duplicate probe.
func (s *Service) StartRecheck(ctx context.Context, q Query, requiredFrom int64) (Job, error) {
	return s.start(ctx, q, requiredFrom)
}

// RecheckQueryStartedAt returns the latest terminal acquisition start for this
// exact source generation. A pending/interrupted job cannot advance it.
func (s *Service) RecheckQueryStartedAt(ctx context.Context, q Query) (int64, error) {
	return s.Store.FullRefreshDue(ctx, recheckCoverageKey(q))
}

func recheckCoverageKey(q Query) string {
	// A restart or an unrelated inventory revision alone does not replace
	// this source. Its target versions, IDs and query provenance still bind
	// the terminal credit. The real query always uses the full current scope.
	q.Scope.BootEpoch = ""
	q.Scope.Revision = 0
	q.TimeoutMS = 0
	q.Version = 0
	raw, _ := json.Marshal(q)
	h := sha256.Sum256(raw)
	return "quota-terminal:" + hex.EncodeToString(h[:])
}
