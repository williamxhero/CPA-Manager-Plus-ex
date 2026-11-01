package worker

import "context"

// Main wires the shared LegacyGuard before startup. Legacy-only embedders with
// no guard retain compatibility; a wired guard NEVER falls back on current or
// unknown mode. Check both queued work and final mutations.
func (w *RateLimitAutoDisableWorker) checkLegacyAutomaticTarget(ctx context.Context, baseURL, key string) error {
	w.mu.RLock()
	guard := w.legacyGuard
	w.mu.RUnlock()
	if guard == nil {
		return nil
	}
	return guard.CheckTarget(ctx, baseURL, key)
}
