import { useEffect, useMemo, useState } from 'react';
import {
  officialBalanceRequest,
  type BalanceBinding,
  type BalanceJob,
  type BalanceObservation,
  type OfficialBalanceStatus,
} from '@/services/api/officialBalance';
import { fullRefreshRequest, type RefreshInventory, type RefreshProgress } from '@/services/api/fullRefresh';
import type { ProviderRow } from './ProviderTable/rowData';
import type { ProviderMoneyStatusConfig } from './useProviderMoneySummaries';
import { formatBalanceText } from '@/utils/quota/formatBalanceAmount';
import { quotaRefreshDelayMs, quotaRefreshDue, readQuotaRefreshIntervalMs } from '@/utils/quota/refreshCadence';
import { loadRuntimeControls, readRuntimeControls, writeRuntimeControls } from '@/utils/quota/runtimeControls';

const officialBalanceCache = new Map<string, { at: number; views: Record<string, OfficialBalanceView> }>();
const officialInventoryCache = new Map<string, { at: number; views: Record<string, OfficialBalanceView> }>();
const KNOWN_EMPTY_BALANCE = '—';
// Resource bindings arrive after the first paint. A not-due read waits until
// that set stops changing, instead of starting an inventory request that the
// next paint aborts.
export const CACHED_BALANCE_SETTLE_MS = 800;

export function clearOfficialBalanceCachesForTests() {
  officialBalanceCache.clear();
  officialInventoryCache.clear();
}

export function balanceStorageKey(kind: string, resourceId: string): string {
  return /^[0-9a-f-]{36}$/i.test(resourceId) ? `${kind}:${resourceId}` : '';
}

export function balanceTextInsufficient(text?: string): boolean {
  if (!text || /unlimited/i.test(text)) return false;
  const numbers = text.match(/-?\d+(?:\.\d+)?/g);
  return !!numbers?.length && numbers.every((value) => Number(value) <= 0);
}

export interface OfficialBalanceView {
  text?: string;
  pending?: boolean;
}

const money = (currency: string | undefined, total: string | undefined) =>
  formatBalanceText(currency, total);

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

function storedRowBalance(
  texts: Record<string, string>,
  kind: string,
  resourceId: string,
  ids: string[]
): { known: boolean; text?: string } {
  const keys = [balanceStorageKey(kind, resourceId), ...ids.map((id) => balanceStorageKey(kind, id))].filter(
    (key, index, all) => key.length > 0 && all.indexOf(key) === index
  );
  const amounts = [...new Set(keys.map((key) => texts[key]).filter(displayableBalanceText))];
  if (amounts.length === 1) return { known: true, text: amounts[0] };
  if (amounts.length > 1) return { known: false };
  if (keys.some((key) => texts[key] === KNOWN_EMPTY_BALANCE)) return { known: true };
  return { known: false };
}

function rowResourceIds(row: ProviderRow): string[] {
  const ids = [row.raw.resourceId];
  if (row.kind === 'openai') ids.push(...row.raw.apiKeyEntries.map((entry) => entry.resourceId));
  return ids.filter((id): id is string => typeof id === 'string' && id.length > 0);
}

export function useOfficialBalanceList(rows: ProviderRow[], config?: ProviderMoneyStatusConfig) {
  const issued = rows.flatMap((row) => {
    const binding = config?.bindingForRow(row);
    const storageKey = balanceStorageKey(
      row.kind,
      typeof row.raw.resourceId === 'string' ? row.raw.resourceId : ''
    );
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
  const inventoryQuery = JSON.stringify(
    rows.map((row) => [
      row.key,
      row.kind,
      typeof row.raw.resourceId === 'string' ? row.raw.resourceId : '',
      rowResourceIds(row),
    ])
  );
  const inventoryRows = useMemo(
    () => JSON.parse(inventoryQuery) as [string, string, string, string[]][],
    [inventoryQuery]
  );
  const [inventoryViews, setInventoryViews] = useState<Record<string, OfficialBalanceView>>({});
  const bindingsSettled = config?.bindingsSettled !== false;

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
      const intervalFresh =
        !force && ((cached && !quotaRefreshDue(cached.at)) || (dueAt > 0 && !quotaRefreshDue(dueAt)));
      const refreshRows = bindings.filter((row) => {
        const text = seeded[row.key]?.text;
        if (balanceTextInsufficient(text)) return true;
        if (displayableBalanceText(text)) return false;
        return texts[row.storageKey] !== KNOWN_EMPTY_BALANCE;
      });
      if (intervalFresh && refreshRows.length === 0) {
        if (mounted) setViews(seeded);
        const delay = quotaRefreshDelayMs(cached && !quotaRefreshDue(cached.at) ? cached.at : dueAt);
        if (mounted && delay) timer = setTimeout(() => void run(true), delay);
        return;
      }
      const queue = intervalFresh ? refreshRows : bindings;
      if (mounted) {
        const pending = { ...seeded };
        for (const row of queue) pending[row.key] = { text: seeded[row.key]?.text, pending: true };
        setViews(pending);
      }
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
            const refreshNow =
              balanceTextInsufficient(seeded[row.key]?.text) || balanceTextInsufficient(text);
            if (
              (refreshNow || !text) &&
              isOfficialStatus(status) &&
              status.configured &&
              status.managerDirectQuery
            ) {
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
        const views = { ...seeded, ...collected };
        officialBalanceCache.set(cacheKey, { at, views });
        const balanceTexts = { ...readRuntimeControls().balanceTexts };
        for (const row of queue) {
          const text = views[row.key]?.text;
          if (row.storageKey && displayableBalanceText(text)) balanceTexts[row.storageKey] = text;
        }
        const partial = queue.length !== bindings.length;
        if (!partial && bindings.every((row) => !row.storageKey || balanceTexts[row.storageKey])) {
          await writeRuntimeControls({ ...readRuntimeControls(), quotaFetchedAtMs: at, balanceTexts });
        } else if (partial) {
          await writeRuntimeControls({ ...readRuntimeControls(), balanceTexts });
        }
      }
      const delay = queue.length === bindings.length ? readQuotaRefreshIntervalMs() : quotaRefreshDelayMs(dueAt);
      if (mounted && delay) timer = setTimeout(() => void run(true), delay);
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
    const cacheKey = `${base}\n${managementKey}\n${inventoryQuery}`;
    const controller = new AbortController();
    let mounted = true;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const bound = new Set(bindings.map((row) => row.key));
    const viewsFrom = (byResource: Map<string, string>) => {
      const next: Record<string, OfficialBalanceView> = {};
      for (const [key, , , ids] of inventoryRows) {
        const texts = [...new Set(ids.map((id) => byResource.get(id)).filter((item) => item))];
        if (texts.length === 1) next[key] = { text: texts[0], pending: false };
      }
      return next;
    };
    const remember = async (next: Record<string, OfficialBalanceView>, stampFetched: boolean) => {
      officialInventoryCache.set(cacheKey, { at: Date.now(), views: next });
      const balanceTexts = { ...readRuntimeControls().balanceTexts };
      for (const [key, kind, resourceId, ids] of inventoryRows) {
        const storageKey =
          balanceStorageKey(kind, resourceId) ||
          ids.map((id) => balanceStorageKey(kind, id)).find((key) => key.length > 0) ||
          '';
        if (!storageKey) continue;
        const text = next[key]?.text;
        if (displayableBalanceText(text)) balanceTexts[storageKey] = text;
        else if (stampFetched) balanceTexts[storageKey] = KNOWN_EMPTY_BALANCE;
      }
      const controls = { ...readRuntimeControls(), balanceTexts };
      if (stampFetched) controls.quotaFetchedAtMs = Date.now();
      await writeRuntimeControls(controls);
    };
    const run = async (force = false) => {
      await loadRuntimeControls();
      if (!mounted) return;
      const dueAt = readRuntimeControls().quotaFetchedAtMs;
      const texts = readRuntimeControls().balanceTexts;
      const seeded: Record<string, OfficialBalanceView> = {};
      let known = true;
      for (const [key, kind, resourceId, ids] of inventoryRows) {
        const stored = storedRowBalance(texts, kind, resourceId, ids);
        if (!stored.known) known = false;
        else if (stored.text) seeded[key] = { text: stored.text, pending: false };
      }
      const memory = officialInventoryCache.get(cacheKey);
      if (memory && !quotaRefreshDue(memory.at)) {
        for (const [key, view] of Object.entries(memory.views)) {
          if (view.text && !seeded[key]) seeded[key] = view;
        }
      }
      const unboundInsufficient = inventoryRows.some(
        ([key]) => !bound.has(key) && balanceTextInsufficient(seeded[key]?.text)
      );
      const intervalFresh = !force && dueAt > 0 && !quotaRefreshDue(dueAt);
      if (intervalFresh && !bindingsSettled) {
        if (mounted) setInventoryViews(seeded);
        return;
      }
      const scheduleHour = () => {
        const delay = quotaRefreshDelayMs(dueAt);
        if (mounted && delay) timer = setTimeout(() => void run(true), delay);
      };
      const loadInventory = async (stampFetched: boolean) => {
        try {
          const inventory = await fullRefreshRequest<RefreshInventory>(
            base,
            managementKey,
            undefined,
            undefined,
            controller.signal
          );
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
          const next = viewsFrom(byResource);
          if (mounted) setInventoryViews({ ...seeded, ...next });
          if (Array.isArray(inventory.sources)) await remember(next, stampFetched);
          const refreshIds = new Set(
            inventoryRows
              .filter(([key]) => !bound.has(key) && balanceTextInsufficient(seeded[key]?.text ?? next[key]?.text))
              .flatMap(([, , , ids]) => ids)
          );
          for (const source of inventory.sources ?? []) {
            if (!mounted || !source.queryCapable || !source.request?.official) continue;
            const touches = (source.targets ?? []).some((target) => refreshIds.has(target.resource_id));
            if (!touches) continue;
            let job = await fullRefreshRequest<RefreshProgress>(
              base,
              managementKey,
              { purpose: 'manual', source: source.request },
              undefined,
              controller.signal
            );
            for (let attempt = 0; attempt < 8 && job.state === 'running' && mounted; attempt++) {
              await new Promise((resolve) => setTimeout(resolve, 400));
              job = await fullRefreshRequest<RefreshProgress>(
                base,
                managementKey,
                undefined,
                job.id,
                controller.signal
              );
            }
            for (const item of job.items ?? []) {
              const text = observationText(item.lastSuccess) ?? observationText(item.attempt);
              if (!text) continue;
              for (const target of item.targets ?? []) {
                if (target.resource_id) byResource.set(target.resource_id, text);
              }
            }
          }
          if (refreshIds.size > 0) {
            const refreshed = viewsFrom(byResource);
            if (mounted) setInventoryViews({ ...seeded, ...refreshed });
            if (Array.isArray(inventory.sources)) await remember(refreshed, false);
          }
        } catch {
          if (mounted) setInventoryViews(seeded);
        }
      };
      if (intervalFresh && known && !unboundInsufficient) {
        if (mounted) setInventoryViews(seeded);
        scheduleHour();
        return;
      }
      if (intervalFresh) {
        if (mounted) setInventoryViews(seeded);
        if (mounted) {
          timer = setTimeout(() => {
            void loadInventory(false).finally(scheduleHour);
          }, CACHED_BALANCE_SETTLE_MS);
        }
        return;
      }
      await loadInventory(true);
    };
    void run(false);
    return () => {
      mounted = false;
      clearTimeout(timer);
      controller.abort();
    };
  }, [base, managementKey, disabled, inventoryRows, inventoryQuery, bindings, bindingsSettled]);

  const merged: Record<string, OfficialBalanceView> = { ...inventoryViews };
  for (const [key, view] of Object.entries(views)) {
    if (view.text || !merged[key]) merged[key] = view;
  }
  return merged;
}
