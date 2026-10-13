import { useEffect } from 'react';
import { useAccountCredentialMutationRevisionStore } from '@/stores/useAccountCredentialMutationRevisionStore';

export function useAccountCredentialMutationSubscription(
  connectionFingerprint: string,
  synchronize: () => void | Promise<unknown>
): void {
  useEffect(() => {
    if (!connectionFingerprint) return;

    // Late credential creation can publish after Accounts has loaded its initial list.
    // Existing markers are handled by the page's marker effects; only wake for new revisions.
    return useAccountCredentialMutationRevisionStore.subscribe((state, previousState) => {
      const hasNewCredentialMutation = Object.entries(state.events).some(
        ([key, event]) =>
          event.connectionFingerprint === connectionFingerprint &&
          event.kind === 'credential' &&
          event.revision > (previousState.events[key]?.revision ?? 0)
      );
      if (hasNewCredentialMutation) void synchronize();
    });
  }, [connectionFingerprint, synchronize]);
}
