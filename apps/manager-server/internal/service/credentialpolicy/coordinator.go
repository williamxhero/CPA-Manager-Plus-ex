package credentialpolicy

import (
	"context"
	fr "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fullrefresh"
	"log"
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
	coordinator.Subscribe(func(ctx context.Context, event fr.CompletedObservation) {
		if err := s.ObserveCompleted(ctx, event); err != nil {
			// The observer API has no error channel. Fail closed, without logging
			// source bodies, secrets, or arbitrary upstream error messages.
			log.Print("credential policy completion unavailable; no automatic action authorized")
		}
	})
	return nil
}
