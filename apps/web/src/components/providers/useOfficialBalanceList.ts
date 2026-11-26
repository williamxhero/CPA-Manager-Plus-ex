import { useEffect, useMemo, useState } from 'react';
import {
  officialBalanceRequest,
  type BalanceBinding,
  type BalanceObservation,
  type OfficialBalanceStatus,
} from '@/services/api/officialBalance';
import { fullRefreshRequest, type RefreshInventory } from '@/services/api/fullRefresh';
import type { ProviderRow } from './ProviderTable/rowData';
import type { ProviderMoneyStatusConfig } from './useProviderMoneySummaries';
import { formatBalanceText, formatBalanceDisplay } from '@/utils/quota/formatBalanceAmount';
import { quotaRefreshDelayMs, quotaRefreshDue, readQuotaRefreshIntervalMs } from '@/utils/quota/refreshCadence';

const officialBalanceCache = new Map<string, { at: number; views: Record<string, OfficialBalanceView> }>();
const officialInventoryCache = new Map<string, { at: number; views: Record<string, OfficialBalanceView> }>();

export function clearOfficialBalanceCachesForTests() {
  officialBalanceCache.clear();
  officialInventoryCache.clear();
}

export interface OfficialBalanceView {
  text?: string;
  /** Raw decimal behind `text`; absent when the source has no countable amount. */
  amount?: string;
  pending?: boolean;
}

interface ObservedMoney {
  text: string;
  amount?: string;
}

function observationMoney(obs?: BalanceObservation): ObservedMoney | undefined {
  if (!obs || obs.queryStatus !== 'success') return undefined;
  const decimal = obs.amountDecimal;
  const wallet = formatBalanceText(obs.currency, decimal);
  if (wallet && decimal) return { text: wallet, amount: decimal };
  const first = obs.balances?.[0];
  const listed = formatBalanceText(first?.currency, first?.total);
  if (listed && first) return { text: listed, amount: first.total };
  if (obs.nativeQuota?.state === 'unlimited') return { text: 'unlimited' };
  return undefined;
}

function statusMoney(status: OfficialBalanceStatus): ObservedMoney | undefined {
  return observationMoney(status.lastSuccess) ?? observationMoney(status.attempt);
}

function isOfficialStatus(value: unknown): value is OfficialBalanceStatus {
  return !!value && typeof value === 'object' && typeof (value as OfficialBalanceStatus).adapter === 'string';
}

function rowResourceIds(row: ProviderRow): string[] {
  const ids = [row.raw.resourceId];
  if (row.kind === 'openai') ids.push(...row.raw.apiKeyEntries.map((entry) => entry.resourceId));
  return ids.filter((id): id is string => typeof id === 'string' && id.length > 0);
}

export function useOfficialBalanceList(rows: ProviderRow[], config?: ProviderMoneyStatusConfig) {
  const base = config?.base;
  const managementKey = config?.managementKey;
  const enabled = Boolean(base && managementKey && !config?.disabled);
  const issued = rows.flatMap((row) => {
    const binding = config?.bindingForRow(row);
    return binding?.targets?.length ? [{ key: row.key, binding }] : [];
  });
  const serialized = JSON.stringify(issued);
  const bindings = useMemo(
    () => JSON.parse(serialized) as { key: string; binding: BalanceBinding }[],
    [serialized]
  );
  const inventoryQuery = JSON.stringify(rows.map((row) => [row.key, rowResourceIds(row)]));
  const inventoryRows = useMemo(
    () => JSON.parse(inventoryQuery) as [string, string[]][],
    [inventoryQuery]
  );
  const balanceKey = JSON.stringify([base, managementKey, serialized]);
  const inventoryKey = JSON.stringify([base, managementKey, inventoryQuery]);
  const balanceContext = useMemo(() => ({ balanceKey }), [balanceKey, enabled]);
  const inventoryContext = useMemo(() => ({ inventoryKey }), [inventoryKey, enabled]);
  const [views, setViews] = useState<{
    context: typeof balanceContext;
    values: Record<string, OfficialBalanceView>;
  }>();
  const [inventoryViews, setInventoryViews] = useState<{
    context: typeof inventoryContext;
    values: Record<string, OfficialBalanceView>;
  }>();
  const bindingsSettled = config?.bindingsSettled !== false;

  useEffect(() => {
    if (!base || !managementKey || !enabled || bindings.length === 0) return;
    let mounted = true;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const active = new Set<AbortController>();
    const run = async (force = false) => {
      const cached = officialBalanceCache.get(balanceKey);
      if (!force && cached && !quotaRefreshDue(cached.at)) {
        const delay = quotaRefreshDelayMs(cached.at);
        if (delay) timer = setTimeout(() => void run(true), delay);
        return;
      }
      const queue = bindings;
      const collected = { ...cached?.views };
      const pending = { ...collected };
      for (const row of queue)
        pending[row.key] = {
          text: collected[row.key]?.text,
          amount: collected[row.key]?.amount,
          pending: true,
        };
      setViews({ context: balanceContext, values: pending });
      const publish = (key: string, view: OfficialBalanceView) => {
        collected[key] = view;
        setViews((previous) =>
          previous?.context === balanceContext
            ? { context: balanceContext, values: { ...previous.values, [key]: view } }
            : previous
        );
      };
      let index = 0;
      const worker = async () => {
        while (mounted && index < queue.length) {
          const row = queue[index++];
          const controller = new AbortController();
          active.add(controller);
          const timeout = setTimeout(() => controller.abort(), 12000);
          const canceled = new Promise<never>((_, reject) => {
            controller.signal.addEventListener('abort', () => reject(new Error('read canceled')), {
              once: true,
            });
          });
          const query = `?binding=${encodeURIComponent(JSON.stringify(row.binding))}`;
          let observed: ObservedMoney | undefined;
          try {
            const status = await Promise.race([
              officialBalanceRequest<OfficialBalanceStatus>(
                base,
                managementKey,
                'GET',
                query,
                undefined,
                controller.signal
              ),
              canceled,
            ]);
            observed = isOfficialStatus(status) ? statusMoney(status) : undefined;
          } catch {
            observed = undefined;
          } finally {
            clearTimeout(timeout);
            active.delete(controller);
          }
          if (mounted) {
            // A failed read keeps the same connection's last observation visible.
            const kept = observed ?? cached?.views[row.key];
            publish(row.key, { text: kept?.text, amount: kept?.amount, pending: false });
          }
        }
      };
      await Promise.all([worker(), worker()]);
      if (mounted) officialBalanceCache.set(balanceKey, { at: Date.now(), views: collected });
      const delay = readQuotaRefreshIntervalMs();
      if (mounted && delay) timer = setTimeout(() => void run(true), delay);
    };
    void run(false);
    return () => {
      mounted = false;
      clearTimeout(timer);
      active.forEach((controller) => controller.abort());
    };
  }, [balanceContext, balanceKey, base, bindings, enabled, managementKey]);

  useEffect(() => {
    if (!base || !managementKey || !enabled || !bindingsSettled) return;
    const bound = new Set(bindings.map((entry) => entry.key));
    if (inventoryRows.every(([key]) => bound.has(key))) return;
    const controller = new AbortController();
    let mounted = true;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const run = async (force = false) => {
      const cached = officialInventoryCache.get(inventoryKey);
      if (!force && cached && !quotaRefreshDue(cached.at)) {
        const delay = quotaRefreshDelayMs(cached.at);
        if (delay) timer = setTimeout(() => void run(true), delay);
        return;
      }
      try {
        const inventory = await fullRefreshRequest<RefreshInventory>(
          base, managementKey, undefined, undefined, controller.signal
        );
        if (!mounted || !Array.isArray(inventory.sources)) return;
        const byResource = new Map<string, ObservedMoney>();
        for (const source of inventory.sources) {
          if (!source.request?.official) continue;
          const observed = observationMoney(source.lastSuccess) ?? observationMoney(source.attempt);
          if (!observed) continue;
          for (const target of source.targets ?? []) {
            if (target.resource_id && !byResource.has(target.resource_id))
              byResource.set(target.resource_id, observed);
          }
        }
        const next: Record<string, OfficialBalanceView> = {};
        for (const [key, ids] of inventoryRows) {
          const observed = ids.map((id) => byResource.get(id));
          const texts = [...new Set(observed.map((item) => item?.text).filter(Boolean))];
          if (texts.length !== 1) continue;
          // Only carry a raw amount every member agrees on, so a rounded hint
          // never decides the tone on its own.
          const amounts = [
            ...new Set(
              observed.filter((item) => item?.text === texts[0]).map((item) => item?.amount)
            ),
          ];
          const text = texts[0];
          next[key] =
            amounts.length === 1
              ? { text, amount: amounts[0], pending: false }
              : { text, pending: false };
        }
        officialInventoryCache.set(inventoryKey, { at: Date.now(), views: next });
        setInventoryViews({ context: inventoryContext, values: next });
      } catch {
        // A failed status read leaves the same connection's last observation visible.
      } finally {
        const delay = readQuotaRefreshIntervalMs();
        if (mounted && delay) timer = setTimeout(() => void run(true), delay);
      }
    };
    void run();
    return () => {
      mounted = false;
      clearTimeout(timer);
      controller.abort();
    };
  }, [base, bindings, bindingsSettled, enabled, inventoryContext, inventoryKey, inventoryRows, managementKey]);

  if (!enabled) return {};
  const cachedInventory = officialInventoryCache.get(inventoryKey)?.views ?? {};
  const merged: Record<string, OfficialBalanceView> = {
    ...(inventoryViews?.context === inventoryContext ? inventoryViews.values : cachedInventory),
  };
  const scopedBalances =
    views?.context === balanceContext ? views.values : officialBalanceCache.get(balanceKey)?.views;
  for (const [key, view] of Object.entries(scopedBalances ?? {})) {
    if (view.text || !merged[key]) merged[key] = view;
  }
  return Object.fromEntries(
    Object.entries(merged).map(([key, view]) => [
      key, { ...view, text: view.text ? formatBalanceDisplay(view.text) : undefined },
    ])
  );
}
