import { useEffect, useMemo, useState } from 'react';
import {
  getProviderPolicy,
  providerBindingKey,
  subscribeProviderPolicyChanges,
  type ProviderPolicyBinding,
  type ProviderPolicyStatus,
} from '@/services/api/providerPolicy';
import type { BalanceBinding } from '@/services/api/officialBalance';
import type { ProviderRow } from './ProviderTable/rowData';
import { readQuotaRefreshIntervalMs } from '@/utils/quota/refreshCadence';

export interface ProviderMoneyStatusConfig {
  base: string;
  managementKey: string;
  disabled?: boolean;
  bindingForRow: (row: ProviderRow) => BalanceBinding | undefined;
}
export interface ProviderMoneyView {
  data?: ProviderPolicyStatus;
  failed?: boolean;
  pending?: boolean;
}
interface IssuedRow {
  key: string;
  id: string;
  binding: ProviderPolicyBinding;
}

function asPolicyBinding(
  providerId: string,
  binding: BalanceBinding
): ProviderPolicyBinding | undefined {
  const next = { ...binding, providerId };
  return next.targets[0]?.resource_id === providerId ? next : undefined;
}

// Read only the visible, issued rows. A sweep never overlaps its successor and
// a blocked row cannot monopolize either of the two read slots indefinitely.
export function useProviderMoneySummaries(rows: ProviderRow[], config?: ProviderMoneyStatusConfig) {
  const issued = rows.flatMap((row): IssuedRow[] => {
    const raw = config?.bindingForRow(row);
    const binding =
      row.kind === 'openai' && row.raw.resourceId && raw
        ? asPolicyBinding(row.raw.resourceId, raw)
        : undefined;
    return binding ? [{ key: row.key, id: binding.providerId, binding }] : [];
  });
  const key = JSON.stringify([
    config?.base,
    config?.managementKey,
    config?.disabled,
    issued.map((r) => [r.key, r.id, providerBindingKey(r.binding)]),
  ]);
  // A fresh identity object fences even A -> B -> A and pagination changes.
  // The key includes the complete issued identity, never just a provider name.
  const context = useMemo(() => ({ key }), [key]);
  const [view, setView] = useState<{
    context: typeof context;
    rows: Record<string, ProviderMoneyView>;
  }>({ context, rows: {} });
  const serialized = JSON.stringify(issued);
  const bindings: IssuedRow[] = useMemo(() => JSON.parse(serialized), [serialized]);
  const base = config?.base;
  const managementKey = config?.managementKey;
  const disabled = config?.disabled;
  const [revision, setRevision] = useState(0);
  useEffect(
    () =>
      subscribeProviderPolicyChanges((change) => {
        if (
          change.base !== base ||
          change.managementKey !== managementKey ||
          !bindings.some((row) => row.id === change.providerId)
        )
          return;
        setView((previous) =>
          previous.context !== context
            ? previous
            : {
                context,
                rows: Object.fromEntries(
                  Object.entries(previous.rows).map(([id, value]) => [
                    id,
                    { ...value, failed: false, pending: true },
                  ])
                ),
              }
        );
        setRevision((value) => value + 1);
      }),
    [context, bindings, base, managementKey]
  );
  useEffect(() => {
    if (!base || !managementKey || disabled || bindings.length === 0) return;
    let mounted = true;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const active = new Set<AbortController>();
    const sweep = async () => {
      if (mounted)
        setView((previous) => ({
          context,
          rows: Object.fromEntries(
            bindings.map((row) => [
              row.key,
              {
                ...(previous.context === context ? previous.rows[row.key] : {}),
                pending: true,
                failed: false,
              },
            ])
          ),
        }));
      let index = 0;
      const worker = async () => {
        while (mounted && index < bindings.length) {
          const row = bindings[index++];
          const controller = new AbortController();
          active.add(controller);
          // Abort also settles the deadline race if a dependency ignores signal.
          const timeout = setTimeout(() => controller.abort(), 12000);
          const canceled = new Promise<never>((_, reject) => {
            controller.signal.addEventListener(
              'abort',
              () => reject(new Error('read canceled')),
              { once: true }
            );
          });
          let next: ProviderMoneyView;
          try {
            const data = await Promise.race([
              getProviderPolicy(base, managementKey, row.binding, controller.signal),
              canceled,
            ]);
            next = { data };
          } catch {
            next = { failed: true };
          } finally {
            clearTimeout(timeout);
            active.delete(controller);
          }
          if (mounted)
            setView((previous) => ({
              context,
              rows: {
                ...(previous.context === context ? previous.rows : {}),
                [row.key]: next.failed
                  ? {
                      ...(previous.context === context ? previous.rows[row.key] : {}),
                      failed: true,
                    }
                  : next,
              },
            }));
        }
      };
      await Promise.all([worker(), worker()]);
      const interval = readQuotaRefreshIntervalMs();
      if (mounted && interval > 0) timer = setTimeout(() => void sweep(), interval);
    };
    void sweep();
    return () => {
      mounted = false;
      clearTimeout(timer);
      active.forEach((controller) => controller.abort());
    };
  }, [context, bindings, base, managementKey, disabled, revision]);
  return view.context === context ? view.rows : {};
}
