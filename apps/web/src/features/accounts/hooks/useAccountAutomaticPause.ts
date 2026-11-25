import { useEffect, useState } from 'react';
import { getCredentialPolicy } from '@/services/api/credentialPolicy';
import type { AccountAutomaticPauseRead } from '../model/accountAutomaticPause';

type ReadEntry = { scope: string; read: AccountAutomaticPauseRead };

const readScope = (
  base: string | undefined,
  managementKey: string | undefined,
  authIndex: string | undefined,
  openToken: number,
  revision: string
): string =>
  base && managementKey && authIndex
    ? JSON.stringify([base, managementKey, authIndex, openToken, revision])
    : '';

/**
 * The detail drawer owns one credential-policy read per open.
 *
 * `openToken` identifies the open: a new open reads once, and every tab switch
 * inside that same open keeps the answer it already has. When the drawer closes
 * the request is aborted and the answer dropped, so reopening reads again
 * instead of showing a previous open's stale result.
 *
 * `revision` identifies the credential behind that auth index. The index is not
 * a lease on one credential: a workspace refresh, a new login, or a different
 * file at the same index changes what the index points at. When the revision
 * changes the read is repeated inside the same open, so the drawer can never
 * show the answer read for the credential that used to be there.
 */
export function useAccountAutomaticPause(
  base: string | undefined,
  managementKey: string | undefined,
  authIndex: string | undefined,
  openToken: number,
  revision: string
): AccountAutomaticPauseRead {
  const scope = readScope(base, managementKey, authIndex, openToken, revision);
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
  }, [authIndex, base, managementKey, revision, scope]);

  if (!scope) return { kind: 'missing' };
  if (entry && entry.scope === scope) return entry.read;
  return { kind: 'reading' };
}
