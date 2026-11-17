import { create } from 'zustand';
import type {
  UsageHeaderSnapshot,
  UsageHeaderSnapshotsResponse,
} from '@/services/api/usageService';
import { sha256Hex } from '@/utils/apiKeyHash';

interface UsageHeaderSnapshotState {
  scopeKey: string;
  items: UsageHeaderSnapshot[];
  generatedAtMs: number;
  loadedAtMs: number;
  contentRevision: string;
  activateScope: (scopeKey: string) => boolean;
  commitResponse: (scopeKey: string, response: UsageHeaderSnapshotsResponse) => boolean;
}

const emptySnapshotState = {
  items: [] as UsageHeaderSnapshot[],
  generatedAtMs: 0,
  loadedAtMs: 0,
  contentRevision: '',
};

export const buildUsageHeaderSnapshotScopeKey = (
  serviceBase: string,
  managementKey: string
): string => {
  const normalizedBase = serviceBase.trim().replace(/\/+$/, '');
  if (!normalizedBase) return '';
  return sha256Hex(`${normalizedBase}\u0000${managementKey.trim()}`);
};

export const buildUsageHeaderSnapshotContentRevision = (items: UsageHeaderSnapshot[]): string =>
  items
    .map((item) => `${item.event_hash}\u0000${Math.trunc(item.timestamp_ms)}`)
    .sort()
    .join('\u0001');

const HEADER_READ_CACHE_KEY = 'cpa.usage-header-read.v1';
const readHeaderCache = (): Record<
  string,
  { items: UsageHeaderSnapshot[]; generatedAtMs: number; loadedAtMs: number }
> => {
  try {
    const state = JSON.parse(localStorage.getItem(HEADER_READ_CACHE_KEY) ?? '{}');
    return state && typeof state === 'object' && !Array.isArray(state) ? state : {};
  } catch {
    return {};
  }
};
const loadHeaderCache = (scopeKey: string) => {
  const cached = readHeaderCache()[scopeKey];
  if (
    !cached ||
    !Array.isArray(cached.items) ||
    !Number.isFinite(cached.generatedAtMs) ||
    !Number.isFinite(cached.loadedAtMs)
  )
    return emptySnapshotState;
  const items = cached.items
    .slice(0, 1000)
    .filter(
      (item) => item && typeof item.event_hash === 'string' && Number.isFinite(item.timestamp_ms)
    );
  return { ...cached, items, contentRevision: buildUsageHeaderSnapshotContentRevision(items) };
};
const saveHeaderCache = (scopeKey: string, snapshot: ReturnType<typeof loadHeaderCache>) => {
  try {
    const states = readHeaderCache();
    states[scopeKey] = snapshot;
    const retained = Object.entries(states)
      .sort((a, b) => b[1].loadedAtMs - a[1].loadedAtMs)
      .slice(0, 4);
    localStorage.setItem(HEADER_READ_CACHE_KEY, JSON.stringify(Object.fromEntries(retained)));
  } catch {
    /* First-paint storage is optional; never block a successful read. */
  }
};

export const useUsageHeaderSnapshotStore = create<UsageHeaderSnapshotState>((set, get) => ({
  scopeKey: '',
  ...emptySnapshotState,
  activateScope: (scopeKey) => {
    const normalizedScope = scopeKey.trim();
    if (get().scopeKey === normalizedScope) return false;
    set({ scopeKey: normalizedScope, ...loadHeaderCache(normalizedScope) });
    return true;
  },
  commitResponse: (scopeKey, response) => {
    const normalizedScope = scopeKey.trim();
    if (!normalizedScope || get().scopeKey !== normalizedScope) return false;
    const items = response.items ?? [];
    const generatedAtMs =
      Number.isFinite(response.generated_at_ms) && response.generated_at_ms > 0
        ? response.generated_at_ms
        : Date.now();
    const snapshot = {
      items,
      generatedAtMs,
      loadedAtMs: Date.now(),
      contentRevision: buildUsageHeaderSnapshotContentRevision(items),
    };
    saveHeaderCache(normalizedScope, snapshot);
    set(snapshot);
    return true;
  },
}));
