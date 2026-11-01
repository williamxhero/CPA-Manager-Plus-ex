package fullrefresh

import (
	"context"
	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
)

func (s *Service) SubscribeStarted(observer StartedObserver) {
	if observer == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.startedObservers = append(s.startedObservers, observer)
}
func (s *Service) notifyStarted(ctx context.Context, event StartedObservation) error {
	event.WalletMembers = s.walletMembersForAttempt(ctx, event.AttemptID)
	s.mu.Lock()
	event.Purpose = s.purposes[event.AttemptID]
	if event.Purpose == "" {
		event.Purpose = Manual
	}
	observers := append([]StartedObserver(nil), s.startedObservers...)
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return ErrUnavailable
	}
	for _, observer := range observers {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := observer(ctx, event); err != nil {
			return err
		}
	}
	return ctx.Err()
}
func (s *Service) startedOfficial(ctx context.Context, job ob.Job) error {
	q := ob.Query{Binding: job.Binding, Version: job.Version}
	return s.notifyStarted(ctx, StartedObservation{Source: SourceRequest{Kind: Official, Official: &q}, AttemptID: job.ID, DeadlineMS: job.DeadlineMS})
}
func (s *Service) startedCredential(ctx context.Context, job cq.Job) error {
	q := job.Query
	return s.notifyStarted(ctx, StartedObservation{Source: SourceRequest{Kind: Credential, Credential: &q}, AttemptID: job.ID, DeadlineMS: job.DeadlineMS})
}
