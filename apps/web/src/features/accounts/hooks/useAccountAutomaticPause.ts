import { useEffect, useState } from 'react';
import { getCredentialPolicy } from '@/services/api/credentialPolicy';
import type { AccountAutomaticPauseRead } from '../model/accountAutomaticPause';

type ReadEntry = { scope: string; read: AccountAutomaticPauseRead };

const readScope = (
  base: string | undefined,
  managementKey: string | undefined,
  authIndex: string | undefined,
  openToken: number
): string =>
  base && managementKey && authIndex
    ? JSON.stringify([base, managementKey, authIndex, openToken])
    : '';

/**
 * The detail drawer owns one credential-policy read per open.
 *
 * `openToken` identifies the open: a new open reads once, and every tab switch
 * inside that same open keeps the answer it already has. When the drawer closes
 * the request is aborted and the answer dropped, so reopening reads again
 * instead of showing a previous open's stale result, and a changed credential
 * behind the same auth index can never inherit the old one.
 */
export function useAccountAutomaticPause(
  base: string | undefined,
  managementKey: string | undefined,
  authIndex: string | undefined,
  openToken: number
): AccountAutomaticPauseRead {
  const scope = readScope(base, managementKey, authIndex, openToken);
  const [entry, setEntry] = useState<ReadEntry | null>(null);

  useEffect(() => {
    if (!base || !managementKey || !authIndex || !scope) return;
    let active = true;
    const controller = new AbortController();
    getCredentialPolicy(base, managementKey, authIndex, controller.signal).then(
      (status) => {
        if (active) setEntry({ scope, read: { kind: 'ready', status } });
      },
      () => {
        if (active) setEntry({ scope, read: { kind: 'failed' } });
      }
    );
    return () => {
      active = false;
      controller.abort();
    };
  }, [authIndex, base, managementKey, scope]);

  if (!scope) return { kind: 'missing' };
  if (entry && entry.scope === scope) return entry.read;
  return { kind: 'reading' };
}
