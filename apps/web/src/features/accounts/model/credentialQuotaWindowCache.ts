import type { AccountQuotaDisplayWindow } from './accountQuotaDisplayWindows';

const STORAGE_KEY = 'cpa.accounts.credential-quota-windows.v1';
const STORAGE_VERSION = 1;
const MAX_SCOPES = 4;
const MAX_ENTRIES_PER_SCOPE = 512;

interface StoredScope {
  updatedAtMs: number;
  entries: Array<[string, AccountQuotaDisplayWindow[]]>;
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

export const saveCredentialQuotaWindowCache = (
  scopeKey: string,
  windowsByAuthIndex: ReadonlyMap<string, AccountQuotaDisplayWindow[]>
): void => {
  const normalizedScopeKey = scopeKey.trim();
  const storage = getStorage();
  if (!normalizedScopeKey || !storage) return;
  const state = readStoredState();
  if (windowsByAuthIndex.size === 0) {
    if (!state.scopes[normalizedScopeKey]) return;
    delete state.scopes[normalizedScopeKey];
  } else {
    const entries: Array<[string, AccountQuotaDisplayWindow[]]> = Array.from(
      windowsByAuthIndex.entries()
    )
      .filter(([authIndex, windows]) => authIndex.trim().length > 0 && windows.length > 0)
      .slice(0, MAX_ENTRIES_PER_SCOPE);
    if (entries.length === 0) {
      delete state.scopes[normalizedScopeKey];
    } else {
      state.scopes[normalizedScopeKey] = { updatedAtMs: Date.now(), entries };
    }
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
