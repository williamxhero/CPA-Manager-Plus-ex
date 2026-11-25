import { createContext, useContext, useEffect, useMemo, useState } from 'react';
import { getCredentialPolicy } from '@/services/api/credentialPolicy';
import type { AccountAutomaticPauseRead } from '../model/accountAutomaticPause';

type ReadView = { identity: string; read: AccountAutomaticPauseRead };

const ReadCacheContext = createContext<Map<string, AccountAutomaticPauseRead> | null>(null);

/** Supplies the per-page policy read cache; without it every mount reads again. */
export const AccountAutomaticPauseReadCache = ReadCacheContext.Provider;

export function useAccountAutomaticPause(
  base: string | undefined,
  managementKey: string | undefined,
  authIndex: string | undefined
): AccountAutomaticPauseRead {
  const cache = useContext(ReadCacheContext);
  const [view, setView] = useState<ReadView | null>(null);
  const identity = useMemo(
    () => (base && managementKey && authIndex ? JSON.stringify([base, managementKey, authIndex]) : ''),
    [base, managementKey, authIndex]
  );
  const cached = identity ? cache?.get(identity) : undefined;

  useEffect(() => {
    if (!base || !managementKey || !authIndex) return;
    if (cache?.has(identity)) return;
    let active = true;
    const controller = new AbortController();
    const settle = (read: AccountAutomaticPauseRead) => {
      if (!active) return;
      cache?.set(identity, read);
      setView({ identity, read });
    };
    getCredentialPolicy(base, managementKey, authIndex, controller.signal)
      .then((status) => settle({ kind: 'ready', status }))
      .catch(() => settle({ kind: 'failed' }));
    return () => {
      active = false;
      controller.abort();
    };
  }, [base, managementKey, authIndex, identity, cache]);

  if (!identity) return { kind: 'missing' };
  const settled = cached ?? (view?.identity === identity ? view.read : undefined);
  return settled ?? { kind: 'reading' };
}
