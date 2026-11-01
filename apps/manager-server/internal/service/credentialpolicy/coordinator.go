package credentialpolicy

import (
	"context"
	fr "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fullrefresh"
)

// BindCoordinator is startup wiring only. Source/Start/Job and query budgets
// remain owned by76; no independent query path is created here.
func (s *Service) BindCoordinator(coordinator *fr.Service) error {
	if coordinator == nil {
		return ErrInput
	}
	s.reconcileMu.Lock()
	if s.coordinator != nil {
		s.reconcileMu.Unlock()
		return ErrInput
	}
	s.coordinator = coordinator
	s.reconcileMu.Unlock()
	coordinator.SubscribeStarted(func(ctx context.Context, event fr.StartedObservation) error {
		if event.Source.Kind == fr.Credential && event.Source.Credential != nil {
			return s.CaptureCredentialQueryStart(ctx, *event.Source.Credential, event.AttemptID)
		}
		// Missing inference association never cancels independently authorized
		// official billing reads. It cannot create an automation fence/proof.
		return nil
	})
	return nil
}
