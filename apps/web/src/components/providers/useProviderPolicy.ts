import { useCallback, useLayoutEffect, useMemo, useRef, useState } from 'react';
import {
  getProviderPolicy,
  recoverProviderPolicy,
  saveProviderPolicy,
  ProviderPolicyRequestError,
  validProviderPolicyBinding,
  type ProviderPolicyStatus,
} from '@/services/api/providerPolicy';
import type { ProviderPolicyControlProps } from './ProviderPolicyControl';

const sameSubject = (a: ProviderPolicyStatus, b: ProviderPolicyStatus) =>
  a.subjectId === b.subjectId && a.owner === b.owner && a.source === b.source;

// Mount-lifetime subscriptions only: no persisted authority, network retry or
// shared success cache. A rejected exact authority revokes *all* mounted marks
// before any captured sibling handler can issue another stale mutation.
const rejectedAuthorityListeners = new Map<string, Set<() => void>>();
const rejectAuthority = (authority: string) => {
  for (const revoke of [...(rejectedAuthorityListeners.get(authority) ?? [])]) revoke();
};

export function useProviderPolicy({
  row,
  binding,
  connection,
  disabled,
}: ProviderPolicyControlProps) {
  const bindingJSON = JSON.stringify(binding);
  const providerId = row.kind === 'openai' ? row.raw.resourceId : undefined;
  const membersJSON = JSON.stringify(
    row.kind === 'openai' ? row.raw.apiKeyEntries.map((entry) => entry.resourceId) : []
  );
  const context = useMemo(() => {
    const parsed = bindingJSON ? JSON.parse(bindingJSON) : undefined;
    const candidate = parsed && providerId ? { ...parsed, providerId } : undefined;
    const members: unknown[] = JSON.parse(membersJSON);
    const valid =
      candidate &&
      validProviderPolicyBinding(candidate) &&
      members.length === candidate.targets.length - 1 &&
      new Set(members).size === members.length &&
      candidate.targets
        .slice(1)
        .every((target: { resource_id: string }) => members.includes(target.resource_id));
    return {
      authority: JSON.stringify([
        bindingJSON,
        providerId,
        membersJSON,
        connection?.base,
        connection?.managementKey,
      ]),
      binding: valid ? candidate : undefined,
      base: connection?.base ?? '',
      managementKey: connection?.managementKey ?? '',
      disabled: !!(disabled || connection?.disabled),
    };
  }, [
    bindingJSON,
    providerId,
    membersJSON,
    connection?.base,
    connection?.managementKey,
    connection?.disabled,
    disabled,
  ]);
  const [readRevision, setReadRevision] = useState(0);
  const [confirmation, setConfirmation] = useState<{
    authority: string;
    token: string;
  } | null>(null);
  const ready = !!(context.binding && context.base && context.managementKey && !context.disabled);
  const [view, setView] = useState<{
    context: typeof context;
    data?: ProviderPolicyStatus;
    busy?: boolean;
    error?: 'conflict' | 'unavailable' | 'reconciliation';
    readOnly?: boolean;
  }>({ context, busy: ready });
  const data = view.context === context ? view.data : undefined;
  const busy = ready && (view.context !== context || !!view.busy);
  const error = view.context === context ? view.error : undefined;
  const writeLocked = view.context === context && !!view.readOnly;
  const recoveryToken = JSON.stringify([
    data?.generation,
    data?.gateGeneration,
    data?.sourceVersion,
  ]);
  const confirmationCurrent =
    confirmation?.authority === context.authority && confirmation.token === recoveryToken;
  const current = useRef<typeof context | null>(null);
  const currentData = useRef<ProviderPolicyStatus | undefined>(undefined);
  const currentView = useRef(view);
  const generation = useRef(0);
  const pending = useRef(false);
  const abort = useRef<AbortController | null>(null);
  const fence = useRef<{
    authority: string;
    generation: number;
    subjectId?: string;
    owner?: string;
    enabled?: boolean;
  } | null>(null);
  const accept = useCallback(
    (next: ProviderPolicyStatus) => {
      const previous = fence.current;
      if (
        !previous ||
        previous.authority !== context.authority ||
        next.generation < previous.generation ||
        (previous.subjectId !== undefined &&
          (next.subjectId !== previous.subjectId || next.owner !== previous.owner)) ||
        (next.generation === previous.generation &&
          previous.enabled !== undefined &&
          next.enabled !== previous.enabled)
      )
        throw new ProviderPolicyRequestError(409);
      fence.current = {
        authority: context.authority,
        generation: next.generation,
        subjectId: next.subjectId,
        owner: next.owner,
        enabled: next.enabled,
      };
    },
    [context.authority]
  );

  useLayoutEffect(() => {
    currentData.current = data;
    currentView.current = view;
  }, [data, view]);
  useLayoutEffect(() => {
    const listeners = rejectedAuthorityListeners.get(context.authority) ?? new Set<() => void>();
    const revoke = () => {
      if (current.current !== context) return;
      generation.current += 1;
      abort.current?.abort();
      pending.current = false;
      currentData.current = undefined;
      const revoked = { context, error: 'conflict' as const };
      currentView.current = revoked;
      setView(revoked);
    };
    listeners.add(revoke);
    rejectedAuthorityListeners.set(context.authority, listeners);
    return () => {
      listeners.delete(revoke);
      if (!listeners.size) rejectedAuthorityListeners.delete(context.authority);
    };
  }, [context]);
  useLayoutEffect(() => {
    current.current = context;
    if (fence.current?.authority !== context.authority)
      fence.current = { authority: context.authority, generation: 0 };
    pending.current = ready;
    const operation = ++generation.current;
    const controller = new AbortController();
    abort.current = controller;
    const previous = currentData.current;
    const previousReadOnly =
      currentView.current.context.authority === context.authority && !!currentView.current.readOnly;
    const valid = () => current.current === context && generation.current === operation;
    if (ready) {
      void Promise.resolve()
        .then(() => {
          if (!valid()) return undefined;
          setView({ context, data: previous, busy: true, readOnly: previousReadOnly });
          return getProviderPolicy(
            context.base,
            context.managementKey,
            context.binding,
            controller.signal
          );
        })
        .then((next) => {
          if (!valid() || !next) return;
          if (
            previous &&
            (next.subjectId !== previous.subjectId ||
              next.owner !== previous.owner ||
              next.generation < previous.generation)
          )
            throw new ProviderPolicyRequestError(409);
          accept(next);
          setView({ context, data: next });
        })
        .catch((failure: unknown) => {
          if (valid()) {
            if (
              failure instanceof ProviderPolicyRequestError &&
              [401, 403].includes(failure.status)
            ) {
              rejectAuthority(context.authority);
              return;
            }
            const identityRejected =
              failure instanceof ProviderPolicyRequestError &&
              [401, 403, 409].includes(failure.status);
            setView({
              context,
              data: identityRejected ? undefined : previous,
              error: identityRejected
                ? 'conflict'
                : previousReadOnly
                  ? 'reconciliation'
                  : 'unavailable',
              readOnly: !identityRejected && previousReadOnly,
            });
          }
        })
        .finally(() => {
          if (valid()) pending.current = false;
        });
    }
    return () => {
      current.current = null;
      generation.current += 1;
      controller.abort();
      abort.current?.abort();
    };
  }, [context, ready, connection?.revision, readRevision, accept]);

  const toggle = async () => {
    // Fence captured UI handlers as well as network callbacks. Abort alone is
    // insufficient: a server/proxy can finish an already accepted old request.
    if (
      !ready ||
      !data ||
      busy ||
      writeLocked ||
      pending.current ||
      current.current !== context ||
      currentView.current !== view ||
      currentData.current !== data ||
      (!data.enabled && (data.locked || !data.bindingCurrent || error))
    )
      return;
    pending.current = true;
    const operation = ++generation.current;
    const valid = () => current.current === context && generation.current === operation;
    abort.current?.abort();
    const controller = new AbortController();
    abort.current = controller;
    setView({ context, data, busy: true, error });
    let minimumGeneration = data.generation;
    let acknowledged: ProviderPolicyStatus | undefined;
    let authorityRejected = false;
    try {
      const saved = await saveProviderPolicy(
        context.base,
        context.managementKey,
        context.binding,
        data.generation,
        !data.enabled,
        controller.signal
      );
      // A default source becomes explicit on the first save. Owner and subject
      // cannot change; subsequent readback must match the new saved source.
      if (!valid()) return;
      if (
        saved.subjectId !== data.subjectId ||
        saved.owner !== data.owner ||
        saved.generation <= data.generation ||
        saved.enabled === data.enabled
      )
        throw new ProviderPolicyRequestError(409);
      minimumGeneration = saved.generation;
      accept(saved);
      acknowledged = saved;
      const next = await getProviderPolicy(
        context.base,
        context.managementKey,
        context.binding,
        controller.signal
      );
      if (!valid()) return;
      if (!sameSubject(next, saved) || next.generation < saved.generation)
        throw new ProviderPolicyRequestError(409);
      accept(next);
      setView({ context, data: next });
    } catch (failure) {
      if (!valid()) return;
      if (failure instanceof ProviderPolicyRequestError && [401, 403].includes(failure.status)) {
        authorityRejected = true;
        // Management identity rejection revokes the old mark, not a 503 stale
        // receipt. Recover only by a fresh read; never replay the rejected PUT.
        rejectAuthority(context.authority);
      } else if (failure instanceof ProviderPolicyRequestError && failure.status === 409) {
        // A conflict revokes the old view immediately, including its ON mark.
        setView({ context, busy: true, error: 'conflict' });
        try {
          const next = await getProviderPolicy(
            context.base,
            context.managementKey,
            context.binding,
            controller.signal
          );
          if (!valid()) return;
          if (
            next.subjectId !== data.subjectId ||
            next.owner !== data.owner ||
            next.generation < minimumGeneration
          )
            throw new ProviderPolicyRequestError(409);
          accept(next);
          setView({ context, data: next, error: 'conflict' });
          if (!acknowledged) connection?.onChanged?.();
        } catch (recoveryFailure) {
          if (valid()) {
            if (
              recoveryFailure instanceof ProviderPolicyRequestError &&
              [401, 403].includes(recoveryFailure.status)
            ) {
              authorityRejected = true;
              rejectAuthority(context.authority);
            } else setView({ context, error: 'conflict' });
          }
        }
      } else {
        if (acknowledged) {
          // PUT acceptance and failed reconciliation are distinct facts. The
          // acknowledged generation may be shown, but no dependent mutation is
          // permitted until a current GET recovers the baseline.
          setView({ context, data: acknowledged, readOnly: true, error: 'reconciliation' });
        } else {
          // No newer save was acknowledged. Retain the trusted baseline and
          // the intentional OFF affordance, without claiming PUT success.
          setView({ context, data, error: 'unavailable' });
        }
      }
    } finally {
      if (valid()) {
        pending.current = false;
        if (acknowledged && !authorityRejected) connection?.onChanged?.();
      }
    }
  };

  const refresh = () => {
    if (!ready || pending.current || current.current !== context || currentView.current !== view)
      return;
    setConfirmation(null);
    setReadRevision((value) => value + 1);
  };

  const requestRecoveryConfirmation = () => {
    if (
      !ready ||
      !data ||
      busy ||
      writeLocked ||
      pending.current ||
      current.current !== context ||
      !data.recoveryAvailable ||
      data.enabled ||
      !data.gateGeneration ||
      !data.sourceVersion
    )
      return;
    setConfirmation({ authority: context.authority, token: recoveryToken });
  };

  const cancelRecoveryConfirmation = () => setConfirmation(null);

  const recover = async () => {
    if (
      !ready ||
      !data ||
      busy ||
      writeLocked ||
      pending.current ||
      current.current !== context ||
      currentView.current !== view ||
      currentData.current !== data ||
      !confirmationCurrent ||
      !data.recoveryAvailable ||
      data.enabled ||
      !data.gateGeneration ||
      !data.sourceVersion
    )
      return;
    pending.current = true;
    const operation = ++generation.current;
    const valid = () => current.current === context && generation.current === operation;
    abort.current?.abort();
    const controller = new AbortController();
    abort.current = controller;
    setConfirmation(null);
    setView({ context, data, busy: true, error });
    let acknowledged: ProviderPolicyStatus | undefined;
    try {
      const saved = await recoverProviderPolicy(
        context.base,
        context.managementKey,
        context.binding,
        data.generation,
        data.gateGeneration,
        data.sourceVersion,
        controller.signal
      );
      if (!valid()) return;
      if (
        saved.subjectId !== data.subjectId ||
        saved.owner !== data.owner ||
        saved.generation < data.generation ||
        saved.enabled
      )
        throw new ProviderPolicyRequestError(409);
      accept(saved);
      acknowledged = saved;
      const next = await getProviderPolicy(
        context.base,
        context.managementKey,
        context.binding,
        controller.signal
      );
      if (!valid()) return;
      if (!sameSubject(next, saved) || next.generation < saved.generation)
        throw new ProviderPolicyRequestError(409);
      accept(next);
      setView({ context, data: next });
      connection?.onChanged?.();
    } catch (failure) {
      if (!valid()) return;
      if (failure instanceof ProviderPolicyRequestError && [401, 403].includes(failure.status)) {
        rejectAuthority(context.authority);
        return;
      }
      if (failure instanceof ProviderPolicyRequestError && failure.status === 409) {
        setView({ context, busy: true, error: 'conflict' });
        try {
          const next = await getProviderPolicy(
            context.base,
            context.managementKey,
            context.binding,
            controller.signal
          );
          if (!valid()) return;
          accept(next);
          setView({ context, data: next, error: 'conflict' });
        } catch {
          if (valid()) setView({ context, error: 'conflict' });
        }
        return;
      }
      if (acknowledged) {
        setView({ context, data: acknowledged, readOnly: true, error: 'reconciliation' });
      } else {
        try {
          const next = await getProviderPolicy(
            context.base,
            context.managementKey,
            context.binding,
            controller.signal
          );
          if (!valid()) return;
          accept(next);
          setView({ context, data: next, error: 'unavailable' });
        } catch {
          if (valid()) setView({ context, data, error: 'unavailable' });
        }
      }
    } finally {
      if (valid()) pending.current = false;
    }
  };

  return {
    data,
    busy,
    error,
    toggle,
    refresh,
    recover,
    requestRecoveryConfirmation,
    cancelRecoveryConfirmation,
    confirmationCurrent,
    canRead: ready,
    writeLocked,
    locked: !ready || !data || !!(data.locked || !data.bindingCurrent || error),
  };
}
