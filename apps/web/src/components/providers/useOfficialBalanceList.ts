import { useEffect, useMemo, useState } from 'react';
import {
  officialBalanceRequest,
  type BalanceBinding,
  type BalanceJob,
  type BalanceObservation,
  type OfficialBalanceStatus,
} from '@/services/api/officialBalance';
import { fullRefreshRequest, type RefreshInventory } from '@/services/api/fullRefresh';
import type { ProviderRow } from './ProviderTable/rowData';
import type { ProviderMoneyStatusConfig } from './useProviderMoneySummaries';
import { quotaRefreshDelayMs, quotaRefreshDue, readQuotaRefreshIntervalMs } from '@/utils/quota/refreshCadence';
import { loadRuntimeControls, readRuntimeControls, writeRuntimeControls } from '@/utils/quota/runtimeControls';

const officialBalanceCache = new Map<string, { at: number; views: Record<string, OfficialBalanceView> }>();
const officialInventoryCache = new Map<string, { at: number; views: Record<string, OfficialBalanceView> }>();

export interface OfficialBalanceView {
  text?: string;
  pending?: boolean;
}

const money = (currency: string | undefined, total: string | undefined) =>
  currency &&
  /^[A-Z]{3}$/.test(currency) &&
  typeof total === 'string' &&
  /^-?(0|[1-9]\d{0,59})(\.\d{1,30})?$/.test(total)
    ? `${currency} ${total}`
    : undefined;

function observationText(obs?: BalanceObservation): string | undefined {
  if (!obs || obs.queryStatus !== 'success') return undefined;
  const wallet = money(obs.currency, obs.amountDecimal);
  if (wallet) return wallet;
  const first = obs.balances?.[0];
  const listed = money(first?.currency, first?.total);
  if (listed) return listed;
  if (obs.nativeQuota?.state === 'unlimited') return 'unlimited';
  return undefined;
}

function displayableBalanceText(text?: string): text is string {
  return (
    text === 'unlimited' ||
    (!!text && /^[A-Z]{3} -?(?:0|[1-9]\d{0,59})(?:\.\d{1,30})?$/.test(text))
  );
}

function statusText(status: OfficialBalanceStatus): string | undefined {
  return observationText(status.lastSuccess) ?? observationText(status.attempt);
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
  const issued = rows.flatMap((row) => {
    const binding = config?.bindingForRow(row);
    const storageKey =
      typeof row.raw.resourceId === 'string' &&
      /^[0-9a-f-]{36}$/i.test(row.raw.resourceId)
        ? `${row.kind}:${row.raw.resourceId}`
        : '';
    return binding?.targets?.length ? [{ key: row.key, storageKey, binding }] : [];
  });
  const serialized = JSON.stringify(issued);
  const bindings = useMemo(
    () => JSON.parse(serialized) as { key: string; storageKey: string; binding: BalanceBinding }[],
    [serialized]
  );
  const base = config?.base;
  const managementKey = config?.managementKey;
  const disabled = config?.disabled;
  const [views, setViews] = useState<Record<string, OfficialBalanceView>>({});
  const inventoryQuery = JSON.stringify(rows.map((row) => [row.key, rowResourceIds(row)]));
  const inventoryRows = useMemo(
    () => JSON.parse(inventoryQuery) as [string, string[]][],
    [inventoryQuery]
  );
  const [inventoryViews, setInventoryViews] = useState<Record<string, OfficialBalanceView>>({});

  useEffect(() => {
    if (!base || !managementKey || disabled || bindings.length === 0) {
      setViews({});
      return;
    }
    const cacheKey = `${base}\n${managementKey}\n${serialized}`;
    let mounted = true;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const active = new Set<AbortController>();
    const collected: Record<string, OfficialBalanceView> = {};
    const publish = (key: string, view: OfficialBalanceView) => {
      collected[key] = view;
      setViews((previous) => ({ ...previous, [key]: view }));
    };
    const run = async (force = false) => {
      await loadRuntimeControls();
      const dueAt = readRuntimeControls().quotaFetchedAtMs;
      const cached = officialBalanceCache.get(cacheKey);
      const texts = readRuntimeControls().balanceTexts;
      const seeded = Object.fromEntries(
        bindings.map((row) => {
          const cachedText = cached?.views[row.key]?.text;
          const text = displayableBalanceText(cachedText)
            ? cachedText
            : displayableBalanceText(texts[row.storageKey])
              ? texts[row.storageKey]
              : undefined;
          return [row.key, { text, pending: false }];
        })
      );
      const complete = bindings.every((row) => displayableBalanceText(seeded[row.key]?.text));
      if (!force && complete && ((cached && !quotaRefreshDue(cached.at)) || !quotaRefreshDue(dueAt))) {
        if (mounted) setViews(seeded);
        const delay = quotaRefreshDelayMs(dueAt);
        if (mounted && delay) timer = setTimeout(() => void run(true), delay);
        return;
      }
      if (mounted) {
        for (const row of bindings) collected[row.key] = { pending: true };
        setViews({ ...collected });
      }
      let index = 0;
      const worker = async () => {
        while (mounted && index < bindings.length) {
          const row = bindings[index++];
          const controller = new AbortController();
          active.add(controller);
          const timeout = setTimeout(() => controller.abort(), 12000);
          const canceled = new Promise<never>((_, reject) => {
            controller.signal.addEventListener('abort', () => reject(new Error('read canceled')), {
              once: true,
            });
          });
          const query = `?binding=${encodeURIComponent(JSON.stringify(row.binding))}`;
          let text: string | undefined;
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
            text = isOfficialStatus(status) ? statusText(status) : undefined;
            if (!text && isOfficialStatus(status) && status.configured && status.managerDirectQuery) {
              const posted = await Promise.race([
                officialBalanceRequest<BalanceJob>(
                  base,
                  managementKey,
                  'POST',
                  '',
                  { scope: row.binding.scope, targets: row.binding.targets, version: status.version },
                  controller.signal
                ),
                canceled,
              ]);
              let job = posted;
              for (let attempt = 0; attempt < 8 && job.state !== 'complete' && mounted; attempt++) {
                await new Promise((resolve) => setTimeout(resolve, 400));
                job = await Promise.race([
                  officialBalanceRequest<BalanceJob>(
                    base,
                    managementKey,
                    'GET',
                    `?job=${encodeURIComponent(job.id)}`,
                    undefined,
                    controller.signal
                  ),
                  canceled,
                ]);
              }
              text = job.status ? statusText(job.status) : undefined;
            }
          } catch {
            text = undefined;
          } finally {
            clearTimeout(timeout);
            active.delete(controller);
          }
          if (mounted) publish(row.key, { text, pending: false });
        }
      };
      await Promise.all([worker(), worker()]);
      if (mounted) {
        const at = Date.now();
        const views = { ...collected };
        officialBalanceCache.set(cacheKey, { at, views });
        const balanceTexts: Record<string, string> = {};
        for (const row of bindings) {
          const text = views[row.key]?.text;
          if (row.storageKey && displayableBalanceText(text)) balanceTexts[row.storageKey] = text;
        }
        if (bindings.every((row) => !row.storageKey || balanceTexts[row.storageKey])) {
          await writeRuntimeControls({ ...readRuntimeControls(), quotaFetchedAtMs: at, balanceTexts });
        }
      }
      const interval = readQuotaRefreshIntervalMs();
      if (mounted && interval > 0) timer = setTimeout(() => void run(true), interval);
    };
    void run(false);
    return () => {
      mounted = false;
      clearTimeout(timer);
      active.forEach((controller) => controller.abort());
    };
  }, [base, managementKey, disabled, bindings]);

  useEffect(() => {
    if (!base || !managementKey || disabled || inventoryRows.length === 0) {
      setInventoryViews({});
      return;
    }
    const controller = new AbortController();
    let mounted = true;
    void (async () => {
      await loadRuntimeControls();
      if (!mounted) return;
      await fullRefreshRequest<RefreshInventory>(base, managementKey, undefined, undefined, controller.signal)
      .then((inventory) => {
        if (!mounted) return;
        const byResource = new Map<string, string>();
        for (const source of inventory.sources ?? []) {
          if (!source.request?.official) continue;
          const text = observationText(source.lastSuccess) ?? observationText(source.attempt);
          if (!text) continue;
          for (const target of source.targets ?? []) {
            if (target.resource_id && !byResource.has(target.resource_id))
              byResource.set(target.resource_id, text);
          }
        }
        const next: Record<string, OfficialBalanceView> = {};
        for (const [key, ids] of inventoryRows) {
          const texts = [...new Set(ids.map((id) => byResource.get(id)).filter((item) => item))];
          if (texts.length === 1) next[key] = { text: texts[0], pending: false };
        }
        setInventoryViews(next);
      })
      .catch(() => {
        if (mounted) setInventoryViews({});
      });
    })();
    return () => {
      mounted = false;
      controller.abort();
    };
  }, [base, managementKey, disabled, inventoryRows]);

  const merged: Record<string, OfficialBalanceView> = { ...inventoryViews };
  for (const [key, view] of Object.entries(views)) {
    if (view.text || !merged[key]) merged[key] = view;
  }
  return merged;
}
