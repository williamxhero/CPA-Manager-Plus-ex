package fullrefresh

import (
	"context"
	"reflect"
	"sort"
	"time"

	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
)

// scheduleQuotaRechecks uses the same source queue and durable terminal credit
// as manual and hourly acquisitions. Core alone interprets the observation.
func (s *Service) scheduleQuotaRechecks(ctx context.Context, inv Inventory, now int64) {
	hints := s.Identity.QuotaRechecks(ctx, inv.connection, inv.Scope)
	if len(hints) == 0 {
		return
	}
	byIndex := make(map[string]SourceStatus, len(hints))
	for _, row := range inv.Sources {
		if q := row.Request.Credential; row.QueryCapable && q != nil && q.AuthIndex != "" {
			byIndex[q.AuthIndex] = row
		}
	}
	type dueSource struct {
		row          SourceStatus
		requiredFrom int64
	}
	due := make(map[string]dueSource, len(hints))
	for _, hint := range hints {
		row, ok := byIndex[hint.AuthIndex]
		if !ok || row.RetryNotBeforeMS > now || hint.ResetID == "" ||
			hint.ObservedAtMS <= 0 || hint.RecheckAtMS <= hint.ObservedAtMS || hint.PublishedAtMS <= 0 ||
			hint.PublishedAtMS > now || hint.RecheckAtMS > now {
			continue
		}
		q := row.Request.Credential
		if hint.Scope != q.Scope || !reflect.DeepEqual(hint.Targets, q.Targets) ||
			hint.SourceVersion != q.SourceVersion || hint.SourceID != q.SourceID || hint.SubjectID != q.SubjectID {
			continue
		}
		// A query starting before the reset or the publication of this exact
		// gate generation does not count. One acquisition can cover multiple
		// due windows, including concurrent manual or ordinary hourly reads.
		requiredFrom := max(hint.RecheckAtMS, hint.PublishedAtMS)
		if due[hint.AuthIndex].requiredFrom < requiredFrom {
			due[hint.AuthIndex] = dueSource{row, requiredFrom}
		}
	}
	ordered := make([]dueSource, 0, len(due))
	for _, candidate := range due {
		ordered = append(ordered, candidate)
	}
	sort.Slice(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if a.requiredFrom != b.requiredFrom {
			return a.requiredFrom < b.requiredFrom
		}
		return a.row.AuthIndex < b.row.AuthIndex
	})
	// Bound work in a scheduler cycle. Uncovered sources remain due for the
	// next cycle. The existing shared acquisition budget limits provider I/O.
	const maxRechecksPerCycle = 16
	taken := 0
	for _, candidate := range ordered {
		row := candidate.row
		if ctx.Err() != nil || taken == maxRechecksPerCycle {
			return
		}
		q := row.Request.Credential
		if ready, err := s.quotaRecheckPending(ctx, *q, candidate.requiredFrom); err != nil || !ready {
			continue
		}
		if _, err := s.startKnownSource(ctx, inv, row, row.Request, Recovery, candidate.requiredFrom); err == nil {
			taken++
		}
	}
}

func (s *Service) quotaRecheckPending(ctx context.Context, q cq.Query, requiredFrom int64) (bool, error) {
	// A synthetic scheduler tick must not grant acquisition before the real
	// host clock reaches the provider's reported reset.
	if time.Now().UnixMilli() < requiredFrom {
		return false, nil
	}
	start, err := s.Credential.RecheckQueryStartedAt(ctx, q)
	return start < requiredFrom, err
}
