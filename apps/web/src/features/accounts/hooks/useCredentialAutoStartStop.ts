import { refreshCredentialSource } from '@/services/api/fullRefresh';
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import {
  CredentialPolicyError,
  getCredentialPolicy,
  getStoredCredentialPolicy,
  listCredentialIntents,
  saveCredentialPolicy,
  resumeCredentialPolicy,
  type CredentialPolicyStatus,
} from '@/services/api/credentialPolicy';

const intentLoads = new Map<string, Promise<Map<string, boolean>>>();

function loadIntents(base: string, managementKey: string) {
  const key = `${base}\n${managementKey}`;
  const existing = intentLoads.get(key);
  if (existing) return existing;
  const pending = listCredentialIntents(base, managementKey)
    .then((rows) => new Map(rows.map((row) => [row.authIndex, row.enabled])))
    .finally(() => {
      intentLoads.delete(key);
    });
  intentLoads.set(key, pending);
  return pending;
}

// No selector/name-derived identity, optimistic ON, automatic ON retry, or page scheduler.
export function useCredentialAutoStartStop(
  base: string,
  managementKey: string,
  authIndex?: string,
  revision?: string | number
) {
  const context = useMemo(
    () => ({ base, managementKey, authIndex, revision }),
    [base, managementKey, authIndex, revision]
  );
  const current = useRef<typeof context | null>(context);
  const pending = useRef(false);
  useLayoutEffect(() => {
    current.current = context;
    return () => {
      if (current.current === context) current.current = null;
    };
  }, [context]);
  const [view, setView] = useState<{
    context: typeof context;
    data?: CredentialPolicyStatus;
    busy?: boolean;
    error?: string;
  }>({ context });
  const data = view.context === context ? view.data : undefined;
  const busy = view.context === context && view.busy;
  useEffect(() => {
    current.current = context;
    pending.current = false;
    const abort = new AbortController();
    if (base && managementKey && authIndex) {
      loadIntents(base, managementKey)
        .then((intents) => {
          if (abort.signal.aborted || current.current !== context || !intents.has(authIndex)) return;
          setView((previous) =>
            previous.context === context && previous.data
              ? { ...previous, data: { ...previous.data, enabled: intents.get(authIndex) === true } }
              : {
                  context,
                  data: { enabled: intents.get(authIndex) === true } as CredentialPolicyStatus,
                }
          );
        })
        .catch(() => {
          /* The icon stays at its last known value. A click loads the full status. */
        });
    }
    return () => {
      abort.abort();
    };
  }, [context, base, managementKey, authIndex]);
  const persist = async (enabled: boolean, resume = false) => {
    let status = data?.subjectId ? data : undefined;
    if (!status && authIndex && base && managementKey) {
      try {
        status = await getCredentialPolicy(base, managementKey, authIndex);
        if (current.current === context) setView({ context, data: status });
      } catch {
        if (current.current === context) setView({ context, error: 'credential_policy_unavailable' });
        return;
      }
    }
    if (!status || pending.current || current.current !== context) return;
    if (enabled && (!status.bindingCurrent || status.availability.state !== 'available')) return;
    pending.current = true;
    setView({ context, data: status, busy: true });
    const valid = () => current.current === context;
    const request = {
      scope: status.scope,
      targets: status.targets,
      auth_index: status.auth_index ?? '',
      sourceVersion: status.sourceVersion,
      expectedGeneration: status.generation,
      owner: status.owner,
    };
    let error: string | undefined;
    try {
      const saved = resume
        ? await resumeCredentialPolicy(base, managementKey, {
            ...request,
            opId: status.operation!.opId,
          })
        : await saveCredentialPolicy(base, managementKey, { ...request, enabled });
      if (!valid()) return;
      // PUT is not proof of the subsequently persisted/current policy.
      const read = await getStoredCredentialPolicy(base, managementKey, status.subjectId);
      if (!valid()) return;
      if (
        read.subjectId !== status.subjectId ||
        read.sourceVersion !== status.sourceVersion ||
        read.owner !== status.owner ||
        JSON.stringify(read.scope) !== JSON.stringify(saved.scope) ||
        JSON.stringify(read.targets) !== JSON.stringify(saved.targets) ||
        read.generation !== saved.generation ||
        read.enabled !== saved.enabled
      )
        throw new CredentialPolicyError(409, 'policy_generation_conflict');
      // Stored status is explicitly NOT current capability; attempt a fresh
      // selector read, but never substitute a different issued subject/source.
      let live = read;
      try {
        const next = await getCredentialPolicy(base, managementKey, authIndex!);
        if (
          next.subjectId === read.subjectId &&
          next.owner === read.owner &&
          next.sourceVersion === read.sourceVersion &&
          next.generation === read.generation &&
          next.enabled === read.enabled &&
          JSON.stringify(next.scope) === JSON.stringify(saved.scope) &&
          JSON.stringify(next.targets) === JSON.stringify(saved.targets)
        )
          live = next;
        else throw new CredentialPolicyError(409, 'identity_changed');
      } catch (e) {
        if (e instanceof CredentialPolicyError && e.code === 'identity_changed') throw e;
      }
      if (valid()) setView({ context, data: live });
    } catch (e) {
      error = e instanceof CredentialPolicyError ? e.code : 'credential_policy_request_failed';
      if (error === 'identity_changed') {
        if (valid()) setView({ context, error });
        return;
      }
      // A failed readback is unknown, not a successful toggle. Reconcile known
      // subject once, with no retry of user intent (especially no ON replay).
      try {
        const read = await getStoredCredentialPolicy(base, managementKey, status.subjectId);
        if (
          valid() &&
          read.subjectId === status.subjectId &&
          read.owner === status.owner &&
          read.sourceVersion === status.sourceVersion
        )
          setView({ context, data: read, error });
        else if (valid()) setView({ context, error });
      } catch {
        if (valid()) setView({ context, error });
      }
    } finally {
      if (valid()) pending.current = false;
    }
  };
  const refresh = async () => {
    if (!data || pending.current || current.current !== context || !authIndex) return;
    pending.current = true;
    setView({ context, data, busy: true });
    try {
      if (!data.querySubjectId) throw new Error('current_credential_source_unavailable');
      await refreshCredentialSource(base, managementKey, {
        scope: data.scope,
        targets: data.targets,
        authIndex: data.auth_index ?? '',
        sourceId: data.sourceId,
        subjectId: data.querySubjectId,
        queryVersion: data.queryVersion ?? 0,
        sourceVersion: data.sourceVersion,
      });
      const next = await getCredentialPolicy(base, managementKey, authIndex);
      if (current.current === context) {
        if (
          next.subjectId !== data.subjectId ||
          next.owner !== data.owner ||
          next.sourceVersion !== data.sourceVersion ||
          JSON.stringify(next.targets) !== JSON.stringify(data.targets)
        )
          setView({ context, error: 'identity_changed' });
        else setView({ context, data: next });
      }
    } catch {
      if (current.current === context)
        setView({ context, data, error: 'common_refresh_unavailable' });
    } finally {
      if (current.current === context) pending.current = false;
    }
  };
  return {
    data,
    busy,
    refresh,
    error: view.context === context ? view.error : undefined,
    setEnabled: (v: boolean) => persist(v),
    resume: () => persist(false, true),
  };
}
