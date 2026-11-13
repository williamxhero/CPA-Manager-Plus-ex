import { useAuthStore } from '@/stores/useAuthStore';

export interface RuntimeControls {
  quotaRefreshIntervalMs: number;
  quotaFetchedAtMs: number;
  autoStartStopEnabled: boolean;
  autoStartStopIds: string[];
  balanceTexts: Record<string, string>;
}

const DEFAULTS: RuntimeControls = {
  quotaRefreshIntervalMs: 1_800_000,
  quotaFetchedAtMs: 0,
  autoStartStopEnabled: false,
  autoStartStopIds: [],
  balanceTexts: {},
};

const SAFE_AUTO_ID =
  /^(gemini|interactions|codex|xai|meta|claude|vertex|openai):(?:[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}|[0-9]{1,6})$/;

let current: RuntimeControls = { ...DEFAULTS, balanceTexts: {}, autoStartStopIds: [] };
let loaded = false;
let balanceLoadSettled = false;
const listeners = new Set<() => void>();
const BALANCE_CACHE_KEY = 'cpa.providerBalanceCache.v2';
const BALANCE_TEXT_KEY =
  /^(gemini|interactions|codex|xai|meta|claude|vertex|openai):[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

function clone(value: RuntimeControls): RuntimeControls {
  return {
    quotaRefreshIntervalMs: value.quotaRefreshIntervalMs,
    quotaFetchedAtMs: value.quotaFetchedAtMs,
    autoStartStopEnabled: value.autoStartStopEnabled,
    autoStartStopIds: [...value.autoStartStopIds],
    balanceTexts: { ...value.balanceTexts },
  };
}

function sameIds(left: string[], right: string[]) {
  return left.length === right.length && left.every((id, index) => id === right[index]);
}

function sameTexts(left: Record<string, string>, right: Record<string, string>) {
  const keys = Object.keys(left);
  return keys.length === Object.keys(right).length && keys.every((key) => left[key] === right[key]);
}

function sameControls(left: RuntimeControls, right: RuntimeControls) {
  return (
    left.quotaRefreshIntervalMs === right.quotaRefreshIntervalMs &&
    left.quotaFetchedAtMs === right.quotaFetchedAtMs &&
    left.autoStartStopEnabled === right.autoStartStopEnabled &&
    sameIds(left.autoStartStopIds, right.autoStartStopIds) &&
    sameTexts(left.balanceTexts, right.balanceTexts)
  );
}

export function safeAutoIds(ids: string[]): string[] {
  const seen = new Set<string>();
  const next: string[] = [];
  for (const id of ids) {
    if (!SAFE_AUTO_ID.test(id) || seen.has(id) || next.length === 500) continue;
    seen.add(id);
    next.push(id);
  }
  return next;
}

export function readRuntimeControls(): RuntimeControls {
  return current;
}

export function runtimeControlsLoaded(): boolean {
  return loaded;
}

export function runtimeBalancesReady(): boolean {
  return loaded || balanceLoadSettled || Object.keys(current.balanceTexts).length > 0;
}

export function subscribeRuntimeControls(listener: () => void) {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

function notify() {
  listeners.forEach((listener) => listener());
}

function publish(next: RuntimeControls) {
  const cloned = clone(next);
  if (sameControls(current, cloned)) return;
  current = cloned;
  notify();
}

function balanceCachePayload(value: RuntimeControls) {
  const balanceTexts: Record<string, string> = {};
  for (const [key, text] of Object.entries(value.balanceTexts)) {
    if (!BALANCE_TEXT_KEY.test(key) || text.length === 0 || text.length > 80) continue;
    balanceTexts[key] = text;
  }
  return { quotaFetchedAtMs: value.quotaFetchedAtMs, balanceTexts };
}

function readStoredBalanceCache(): Pick<RuntimeControls, 'quotaFetchedAtMs' | 'balanceTexts'> | null {
  try {
    if (typeof localStorage === 'undefined') return null;
    const raw = localStorage.getItem(BALANCE_CACHE_KEY);
    if (!raw) return null;
    return balanceCachePayload(normalize(JSON.parse(raw) as Partial<RuntimeControls>));
  } catch {
    return null;
  }
}

function writeStoredBalanceCache(value: RuntimeControls) {
  try {
    if (typeof localStorage === 'undefined') return;
    localStorage.setItem(BALANCE_CACHE_KEY, JSON.stringify(balanceCachePayload(value)));
  } catch {
    // The memory copy still paints this view.
  }
}

const storedBalances = readStoredBalanceCache();
if (storedBalances && (storedBalances.quotaFetchedAtMs > 0 || Object.keys(storedBalances.balanceTexts).length > 0)) {
  current = {
    ...current,
    quotaFetchedAtMs: storedBalances.quotaFetchedAtMs,
    balanceTexts: storedBalances.balanceTexts,
  };
}

function normalize(value: Partial<RuntimeControls> | null | undefined): RuntimeControls {
  const interval = Number(value?.quotaRefreshIntervalMs);
  return {
    quotaRefreshIntervalMs:
      Number.isFinite(interval) && interval >= 0 && interval <= 86_400_000
        ? interval
        : DEFAULTS.quotaRefreshIntervalMs,
    quotaFetchedAtMs:
      Number.isFinite(Number(value?.quotaFetchedAtMs)) && Number(value?.quotaFetchedAtMs) > 0
        ? Number(value?.quotaFetchedAtMs)
        : 0,
    autoStartStopEnabled: value?.autoStartStopEnabled === true,
    autoStartStopIds: safeAutoIds(
      Array.isArray(value?.autoStartStopIds)
        ? value.autoStartStopIds.filter((id) => typeof id === 'string')
        : []
    ),
    balanceTexts:
      value?.balanceTexts && typeof value.balanceTexts === 'object' ? { ...value.balanceTexts } : {},
  };
}

async function request(method: 'GET' | 'PUT', body?: RuntimeControls): Promise<RuntimeControls> {
  const { apiBase, managementKey } = useAuthStore.getState();
  if (!apiBase || !managementKey) throw new Error('runtime_controls_signed_out');
  const response = await fetch(`${apiBase.replace(/\/$/, '')}/usage-service/runtime-controls`, {
    method,
    headers: {
      Authorization: `Bearer ${managementKey}`,
      'Content-Type': 'application/json',
    },
    body: body ? JSON.stringify(body) : undefined,
    cache: 'no-store',
  });
  if (!response.ok) throw new Error('runtime_controls_unavailable');
  return normalize((await response.json()) as Partial<RuntimeControls>);
}

let pending: Promise<RuntimeControls> | null = null;

export function loadRuntimeControls(): Promise<RuntimeControls> {
  if (loaded) return Promise.resolve(readRuntimeControls());
  if (!pending) {
    pending = request('GET')
      .then((controls) => {
        loaded = true;
        publish(controls);
        writeStoredBalanceCache(readRuntimeControls());
        return readRuntimeControls();
      })
      .catch(() => readRuntimeControls())
      .finally(() => {
        pending = null;
        balanceLoadSettled = true;
        notify();
      });
  }
  return pending;
}

let pendingWrite: Promise<void> = Promise.resolve();

export function writeRuntimeControls(next: RuntimeControls): Promise<void> {
  const task = pendingWrite.then(async () => {
    const latest = readRuntimeControls();
    const normalized = normalize({
      ...latest,
      ...next,
      quotaFetchedAtMs: Math.max(latest.quotaFetchedAtMs || 0, next.quotaFetchedAtMs || 0),
      balanceTexts: { ...latest.balanceTexts, ...next.balanceTexts },
    });
    publish(normalized);
    loaded = true;
    try {
      publish(await request('PUT', normalized));
    } catch {
      publish(normalized);
    }
    writeStoredBalanceCache(readRuntimeControls());
  });
  pendingWrite = task.then(
    () => undefined,
    () => undefined
  );
  return task;
}

export function setRuntimeControlsForTests(next: Partial<RuntimeControls>) {
  loaded = true;
  balanceLoadSettled = true;
  pending = null;
  publish(normalize({ ...DEFAULTS, ...next }));
}
