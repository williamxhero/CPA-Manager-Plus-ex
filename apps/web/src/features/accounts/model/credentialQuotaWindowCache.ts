import type { AccountQuotaDisplayWindow } from './accountQuotaDisplayWindows';
import type { CredentialQuotaResetCreditsEvidence } from './credentialQuotaListWindows';
import type {
  MonitoringAccountHistoryItem,
  MonitoringAccountWindowUsageItem,
} from '@/services/api/usageService';

export interface ScopedAccountHistory {
  item: MonitoringAccountHistoryItem;
  identitySignature: string;
}
export interface ListWindowUsageRead {
  items: Map<string, MonitoringAccountWindowUsageItem>;
  context: { pageKeys: string[]; identityByRowKey: Map<string, string>; asOfMs: number };
}

export interface CredentialQuotaReadCache {
  windows: Map<string, AccountQuotaDisplayWindow[]>;
  resetCredits: Map<string, CredentialQuotaResetCreditsEvidence>;
  resetCreditInvalidations: Map<string, number>;
  history: Map<string, ScopedAccountHistory>;
  listWindowUsage: ListWindowUsageRead | null;
}

const STORAGE_KEY = 'cpa.accounts.credential-quota-windows.v1';
const STORAGE_VERSION = 1;
const MAX_SCOPES = 4;
const MAX_ENTRIES_PER_SCOPE = 512;

interface StoredScope {
  updatedAtMs: number;
  entries: Array<[string, AccountQuotaDisplayWindow[]]>;
  resetCredits?: Array<[string, CredentialQuotaResetCreditsEvidence]>;
  resetCreditInvalidations?: Array<[string, number]>;
  history?: Array<[string, ScopedAccountHistory]>;
  listWindowUsage?: {
    items: Array<[string, MonitoringAccountWindowUsageItem]>;
    context: { pageKeys: string[]; identityByRowKey: Array<[string, string]>; asOfMs: number };
  } | null;
}

interface StoredState {
  version: number;
  scopes: Record<string, StoredScope>;
}

const readString = (value: unknown): string => (typeof value === 'string' ? value.trim() : '');

const readNumberOrNull = (value: unknown): number | null =>
  typeof value === 'number' && Number.isFinite(value) ? value : null;

const readOptionalString = (value: unknown): string | undefined => {
  const text = readString(value);
  return text || undefined;
};

const normalizeWindow = (value: unknown): AccountQuotaDisplayWindow | null => {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null;
  const window = value as Record<string, unknown>;
  const key = readString(window.key);
  const label = readString(window.label);
  if (!key || !label) return null;
  const modelScope =
    window.modelScope && typeof window.modelScope === 'object' && !Array.isArray(window.modelScope)
      ? (window.modelScope as AccountQuotaDisplayWindow['modelScope'])
      : undefined;
  return {
    key,
    label,
    providerWindowId: readOptionalString(window.providerWindowId),
    kind:
      typeof window.kind === 'string'
        ? (window.kind as AccountQuotaDisplayWindow['kind'])
        : undefined,
    remainingPercent: readNumberOrNull(window.remainingPercent),
    usedPercent: readNumberOrNull(window.usedPercent),
    resetLabel: typeof window.resetLabel === 'string' ? window.resetLabel : '-',
    resetAccuracy:
      window.resetAccuracy === 'exact' || window.resetAccuracy === 'estimated'
        ? window.resetAccuracy
        : 'unknown',
    limitWindowSeconds: readNumberOrNull(window.limitWindowSeconds),
    resetAtMs: readNumberOrNull(window.resetAtMs),
    fromMs: readNumberOrNull(window.fromMs),
    toMs: readNumberOrNull(window.toMs),
    amountLabel: readOptionalString(window.amountLabel),
    description: readOptionalString(window.description),
    groupLabel: readOptionalString(window.groupLabel),
    source:
      typeof window.source === 'string'
        ? (window.source as AccountQuotaDisplayWindow['source'])
        : undefined,
    observationSource:
      typeof window.observationSource === 'string'
        ? (window.observationSource as AccountQuotaDisplayWindow['observationSource'])
        : undefined,
    observedAtMs: readNumberOrNull(window.observedAtMs),
    quotaProgressObservedAtMs: readNumberOrNull(window.quotaProgressObservedAtMs),
    windowMode:
      typeof window.windowMode === 'string'
        ? (window.windowMode as AccountQuotaDisplayWindow['windowMode'])
        : undefined,
    cycleStartMs: readNumberOrNull(window.cycleStartMs),
    cycleEndMs: readNumberOrNull(window.cycleEndMs),
    modelScope,
    providerWindowAliases: Array.isArray(window.providerWindowAliases)
      ? window.providerWindowAliases.filter(
          (alias): alias is string => typeof alias === 'string' && alias.trim().length > 0
        )
      : undefined,
  };
};

const getStorage = (): Storage | null => {
  try {
    return typeof localStorage !== 'undefined' ? localStorage : null;
  } catch {
    return null;
  }
};

const readStoredState = (): StoredState => {
  const storage = getStorage();
  if (!storage) return { version: STORAGE_VERSION, scopes: {} };
  try {
    const raw = storage.getItem(STORAGE_KEY);
    if (!raw) return { version: STORAGE_VERSION, scopes: {} };
    const parsed = JSON.parse(raw) as Partial<StoredState>;
    if (parsed.version !== STORAGE_VERSION || !parsed.scopes || typeof parsed.scopes !== 'object') {
      return { version: STORAGE_VERSION, scopes: {} };
    }
    return { version: STORAGE_VERSION, scopes: parsed.scopes };
  } catch {
    return { version: STORAGE_VERSION, scopes: {} };
  }
};

const isExpiredWindow = (window: AccountQuotaDisplayWindow, nowMs: number): boolean =>
  typeof window.resetAtMs === 'number' &&
  Number.isFinite(window.resetAtMs) &&
  window.resetAtMs > 0 &&
  window.resetAtMs <= nowMs;

export const loadCredentialQuotaWindowCache = (
  scopeKey: string
): Map<string, AccountQuotaDisplayWindow[]> => {
  const normalizedScopeKey = scopeKey.trim();
  const result = new Map<string, AccountQuotaDisplayWindow[]>();
  if (!normalizedScopeKey) return result;
  const scope = readStoredState().scopes[normalizedScopeKey];
  if (!scope || !Array.isArray(scope.entries)) return result;
  const nowMs = Date.now();
  scope.entries.slice(0, MAX_ENTRIES_PER_SCOPE).forEach((entry) => {
    if (!Array.isArray(entry) || entry.length !== 2) return;
    const authIndex = readString(entry[0]);
    if (!authIndex || !Array.isArray(entry[1])) return;
    const windows = entry[1]
      .map(normalizeWindow)
      .filter((window): window is AccountQuotaDisplayWindow => window !== null)
      .filter((window) => !isExpiredWindow(window, nowMs));
    if (windows.length > 0) result.set(authIndex, windows);
  });
  return result;
};

// Count-only evidence belongs to the same connection-scoped first-paint read
// as windows. Never persist credit ids, tokens or invented expiry dates.
export const loadCredentialQuotaReadCache = (scopeKey: string): CredentialQuotaReadCache => {
  const result: CredentialQuotaReadCache = {
    windows: loadCredentialQuotaWindowCache(scopeKey),
    resetCredits: new Map(),
    resetCreditInvalidations: new Map(),
    history: new Map(),
    listWindowUsage: null,
  };
  const scope = readStoredState().scopes[scopeKey.trim()];
  if (!scope) return result;
  if (Array.isArray(scope.history)) {
    for (const entry of scope.history.slice(0, MAX_ENTRIES_PER_SCOPE)) {
      if (!Array.isArray(entry) || entry.length !== 2) continue;
      const [rowKey, scoped] = entry;
      if (
        typeof rowKey !== 'string' ||
        !scoped ||
        typeof scoped.identitySignature !== 'string' ||
        !scoped.item ||
        scoped.item.row_key !== rowKey ||
        typeof scoped.item.matched !== 'boolean' ||
        !Number.isFinite(scoped.item.total_requests)
      )
        continue;
      result.history.set(rowKey, scoped);
    }
  }
  const usage = scope.listWindowUsage;
  if (
    usage &&
    Array.isArray(usage.items) &&
    Array.isArray(usage.context?.pageKeys) &&
    Array.isArray(usage.context?.identityByRowKey) &&
    Number.isFinite(usage.context.asOfMs)
  ) {
    result.listWindowUsage = {
      items: new Map(
        usage.items
          .slice(0, MAX_ENTRIES_PER_SCOPE)
          .filter(
            (entry) =>
              Array.isArray(entry) &&
              entry.length === 2 &&
              typeof entry[0] === 'string' &&
              entry[1] &&
              Number.isFinite(entry[1].from_ms) &&
              Number.isFinite(entry[1].to_ms)
          )
      ),
      context: {
        pageKeys: usage.context.pageKeys.filter((key) => typeof key === 'string'),
        identityByRowKey: new Map(
          usage.context.identityByRowKey.filter(
            (entry) =>
              Array.isArray(entry) &&
              entry.length === 2 &&
              entry.every((value) => typeof value === 'string')
          )
        ),
        asOfMs: usage.context.asOfMs,
      },
    };
  }
  if (Array.isArray(scope.resetCreditInvalidations)) {
    for (const entry of scope.resetCreditInvalidations.slice(0, MAX_ENTRIES_PER_SCOPE)) {
      if (!Array.isArray(entry) || entry.length !== 2) continue;
      const index = readString(entry[0]);
      const at = readNumberOrNull(entry[1]);
      if (index && at !== null && at > 0) result.resetCreditInvalidations.set(index, at);
    }
  }
  if (Array.isArray(scope.resetCredits)) {
    for (const entry of scope.resetCredits.slice(0, MAX_ENTRIES_PER_SCOPE)) {
      if (!Array.isArray(entry) || entry.length !== 2) continue;
      const index = readString(entry[0]);
      const credit = entry[1];
      if (!index || !credit || typeof credit !== 'object') continue;
      const count = readNumberOrNull(credit.availableCount);
      if (count === null || !Number.isSafeInteger(count) || count < 0 || count > 1000) continue;
      const at = readNumberOrNull(credit.observedAtMs);
      const invalidatedAt = result.resetCreditInvalidations.get(index);
      if (invalidatedAt !== undefined && (at === null || at <= invalidatedAt)) continue;
      const applicable = readNumberOrNull(credit.applicableAvailableCount);
      result.resetCredits.set(index, {
        availableCount: count,
        applicableAvailableCount:
          applicable !== null &&
          Number.isSafeInteger(applicable) &&
          applicable >= 0 &&
          applicable <= count
            ? applicable
            : null,
        observedAtMs: at !== null && at > 0 ? at : null,
      });
    }
  }
  return result;
};

export const saveCredentialQuotaReadCache = (
  scopeKey: string,
  cache: CredentialQuotaReadCache
): void => {
  const normalizedScopeKey = scopeKey.trim();
  const storage = getStorage();
  if (!normalizedScopeKey || !storage) return;
  const state = readStoredState();
  if (
    cache.windows.size === 0 &&
    cache.resetCredits.size === 0 &&
    cache.resetCreditInvalidations.size === 0 &&
    cache.history.size === 0 &&
    !cache.listWindowUsage
  ) {
    if (!state.scopes[normalizedScopeKey]) return;
    delete state.scopes[normalizedScopeKey];
  } else {
    const entries: Array<[string, AccountQuotaDisplayWindow[]]> = Array.from(
      cache.windows.entries()
    )
      .filter(([authIndex, windows]) => authIndex.trim().length > 0 && windows.length > 0)
      .slice(0, MAX_ENTRIES_PER_SCOPE);
    state.scopes[normalizedScopeKey] = {
      updatedAtMs: Date.now(),
      entries,
      resetCredits: Array.from(cache.resetCredits.entries()).slice(0, MAX_ENTRIES_PER_SCOPE),
      resetCreditInvalidations: Array.from(cache.resetCreditInvalidations.entries()).slice(
        0,
        MAX_ENTRIES_PER_SCOPE
      ),
      history: Array.from(cache.history.entries()).slice(0, MAX_ENTRIES_PER_SCOPE),
      listWindowUsage: cache.listWindowUsage
        ? {
            items: Array.from(cache.listWindowUsage.items.entries()).slice(
              0,
              MAX_ENTRIES_PER_SCOPE
            ),
            context: {
              ...cache.listWindowUsage.context,
              identityByRowKey: Array.from(
                cache.listWindowUsage.context.identityByRowKey.entries()
              ),
            },
          }
        : null,
    };
  }
  const retainedScopes = Object.entries(state.scopes)
    .filter(
      ([, scope]) =>
        scope && typeof scope.updatedAtMs === 'number' && Number.isFinite(scope.updatedAtMs)
    )
    .sort((left, right) => right[1].updatedAtMs - left[1].updatedAtMs)
    .slice(0, MAX_SCOPES);
  try {
    storage.setItem(
      STORAGE_KEY,
      JSON.stringify({ version: STORAGE_VERSION, scopes: Object.fromEntries(retainedScopes) })
    );
  } catch {
    // The in-memory host read still paints this session; storage is a first-paint cache.
  }
};

// Compatibility writer for callers that only have window evidence. Do not erase
// count evidence merely because an older caller updates its window projection.
export const saveCredentialQuotaWindowCache = (
  scopeKey: string,
  windows: Map<string, AccountQuotaDisplayWindow[]>
): void =>
  saveCredentialQuotaReadCache(scopeKey, { ...loadCredentialQuotaReadCache(scopeKey), windows });
