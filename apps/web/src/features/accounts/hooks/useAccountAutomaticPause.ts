import { useEffect, useState } from 'react';
import { getCredentialPolicy } from '@/services/api/credentialPolicy';
import type { AccountAutomaticPauseRead } from '../model/accountAutomaticPause';

type ReadView = { identity: string; read: AccountAutomaticPauseRead };

/**
 * One policy read per detail-open. The overview is only mounted while its
 * drawer is open, so the list never issues one request per row, and a failed
 * read is a state for the row to report, never a thrown error or empty flicker.
 */
export function useAccountAutomaticPause(
  base: string | undefined,
  managementKey: string | undefined,
  authIndex: string | undefined
): AccountAutomaticPauseRead {
  const [view, setView] = useState<ReadView | null>(null);
  const identity =
    base && managementKey && authIndex ? JSON.stringify([base, managementKey, authIndex]) : '';

  useEffect(() => {
    if (!base || !managementKey || !authIndex) return;
    const authority = { base, managementKey, authIndex };
    const readIdentity = JSON.stringify([authority.base, authority.managementKey, authority.authIndex]);
    let active = true;
    const controller = new AbortController();
    getCredentialPolicy(
      authority.base,
      authority.managementKey,
      authority.authIndex,
      controller.signal
    )
      .then((status) => {
        if (active) setView({ identity: readIdentity, read: { kind: 'ready', status } });
      })
      .catch(() => {
        if (active) setView({ identity: readIdentity, read: { kind: 'failed' } });
      });
    return () => {
      active = false;
      controller.abort();
    };
  }, [base, managementKey, authIndex]);

  if (!identity) return { kind: 'missing' };
  return view?.identity === identity ? view.read : { kind: 'reading' };
}
