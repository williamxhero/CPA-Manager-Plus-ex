import {
  inferAccountQuotaWindowKind,
  type AccountQuotaDisplayWindow,
  type AccountQuotaWindowKind,
  type AccountQuotaWindowSource,
} from './accountQuotaDisplayWindows';
import {
  inferCodexQuotaScopeFromProviderWindowId,
  isCodexMainQuotaModelScope,
} from '@/utils/quota/codexQuota';

type StoredWindow = {
  name?: string;
  usedPercent?: number;
  remainingFraction?: number;
  resetAt?: string;
  limitWindowSeconds?: number;
  model?: string;
};

const kinds: Record<string, AccountQuotaWindowKind> = {
  rolling: 'five_hour',
  primary: 'five_hour',
  weekly: 'weekly',
  secondary: 'weekly',
  monthly: 'monthly',
};
const modes = {
  rolling: 'rolling',
  primary: 'rolling',
  weekly: 'calendar',
  secondary: 'calendar',
  monthly: 'calendar',
} as const;

const labels: Record<string, string> = {
  primary: '5h',
  secondary: 'Weekly',
};

export function credentialQuotaListWindows(
  raw: string | undefined,
  source: Extract<AccountQuotaWindowSource, 'codex' | 'opencode-go' | 'qwen'>,
  observedAtMs?: number | null
): AccountQuotaDisplayWindow[] {
  if (!raw) return [];
  let parsed: { windows?: StoredWindow[] };
  try {
    parsed = JSON.parse(raw) as { windows?: StoredWindow[] };
  } catch {
    return [];
  }
  if (!Array.isArray(parsed.windows)) return [];
  return parsed.windows.flatMap((window) => {
    if (
      !window.name ||
      typeof window.usedPercent !== 'number' ||
      !Number.isFinite(window.usedPercent)
    ) {
      return [];
    }
    const resetAtMs = window.resetAt ? Date.parse(window.resetAt) : Number.NaN;
    const seconds =
      typeof window.limitWindowSeconds === 'number' &&
      Number.isSafeInteger(window.limitWindowSeconds) &&
      window.limitWindowSeconds > 0
        ? window.limitWindowSeconds
        : null;
    const codex = source === 'codex';
    // Primary/secondary are upstream slots, not fixed-duration window types.
    const kind = codex
      ? (inferAccountQuotaWindowKind({ key: '', label: '', limitWindowSeconds: seconds }) ??
        'unknown')
      : (kinds[window.name] ?? 'unknown');
    const durationLabels: Partial<Record<AccountQuotaWindowKind, string>> = {
      five_hour: '5h',
      daily: 'Daily',
      weekly: 'Weekly',
      monthly: 'Monthly',
    };
    // Derive remaining from used% so a stale remainingFraction cannot disagree
    // with the "已用 x/y" label (e.g. used 0 + remainingFraction 0 → empty red bar).
    const remaining = Math.max(0, Math.min(100, 100 - window.usedPercent));
    return [
      {
        key: `${source}:${window.model ? `${window.model}:` : ''}${window.name}`,
        // Use the same logical identity as persisted quota snapshots. Upstream
        // primary/secondary slots can change duration when the plan changes.
        providerWindowId:
          codex && !window.model && kind !== 'unknown'
            ? kind === 'five_hour'
              ? 'five-hour'
              : kind
            : undefined,
        label: codex ? (durationLabels[kind] ?? window.name) : (labels[window.name] ?? window.name),
        kind,
        remainingPercent: remaining,
        usedPercent: window.usedPercent,
        resetLabel: window.resetAt ?? '',
        resetAccuracy: 'exact' as const,
        limitWindowSeconds: seconds,
        resetAtMs: Number.isFinite(resetAtMs) ? resetAtMs : null,
        cycleStartMs:
          codex && Number.isFinite(resetAtMs) && seconds !== null
            ? resetAtMs - seconds * 1000
            : null,
        cycleEndMs: codex && Number.isFinite(resetAtMs) ? resetAtMs : null,
        fromMs: null,
        toMs: null,
        source,
        windowMode: codex
          ? kind === 'unknown'
            ? 'unknown'
            : kind === 'five_hour'
              ? 'rolling'
              : 'calendar'
          : (modes[window.name as keyof typeof modes] ?? 'fixed'),
        modelScope: codex
          ? window.model
            ? { kind: 'models' as const, models: [window.model], complete: true }
            : inferCodexQuotaScopeFromProviderWindowId(window.name)
          : undefined,
        observedAtMs:
          typeof observedAtMs === 'number' && Number.isFinite(observedAtMs) ? observedAtMs : null,
        quotaProgressObservedAtMs:
          typeof observedAtMs === 'number' && Number.isFinite(observedAtMs) && observedAtMs > 0
            ? observedAtMs
            : null,
      },
    ];
  });
}

const isDepletedQuotaWindow = (window: AccountQuotaDisplayWindow): boolean =>
  (typeof window.remainingPercent === 'number' &&
    Number.isFinite(window.remainingPercent) &&
    window.remainingPercent <= 0) ||
  (typeof window.usedPercent === 'number' &&
    Number.isFinite(window.usedPercent) &&
    window.usedPercent >= 100);

const windowsJsonLooksDepleted = (raw?: string): boolean => {
  if (!raw) return false;
  try {
    const parsed = JSON.parse(raw) as { windows?: StoredWindow[] };
    const windows = Array.isArray(parsed.windows) ? parsed.windows : [];
    return windows.some(
      (window) =>
        (typeof window.remainingFraction === 'number' &&
          Number.isFinite(window.remainingFraction) &&
          window.remainingFraction <= 0) ||
        (typeof window.usedPercent === 'number' &&
          Number.isFinite(window.usedPercent) &&
          window.usedPercent >= 100)
    );
  } catch {
    return false;
  }
};

/** Prefer a live insufficient attempt over a healthier cached lastSuccess. */
export function selectCredentialQuotaWindowsObservation(status: {
  attempt?: { queryStatus?: string; windowsJson?: string; observedAtMs?: number };
  lastSuccess?: { queryStatus?: string; windowsJson?: string; observedAtMs?: number };
}): { windowsJson?: string; observedAtMs?: number } | undefined {
  const attempt =
    status.attempt?.queryStatus === 'success' && status.attempt.windowsJson
      ? status.attempt
      : undefined;
  const lastSuccess = status.lastSuccess?.windowsJson ? status.lastSuccess : undefined;
  if (attempt && windowsJsonLooksDepleted(attempt.windowsJson)) return attempt;
  if (
    attempt &&
    (!lastSuccess ||
      typeof attempt.observedAtMs !== 'number' ||
      typeof lastSuccess.observedAtMs !== 'number' ||
      attempt.observedAtMs >= lastSuccess.observedAtMs)
  ) {
    return attempt;
  }
  return lastSuccess ?? attempt;
}

const quotaWindowObservedAtMs = (window: AccountQuotaDisplayWindow): number => {
  const candidates = [window.quotaProgressObservedAtMs, window.observedAtMs];
  let latest = 0;
  for (const value of candidates) {
    if (typeof value === 'number' && Number.isFinite(value) && value > latest) latest = value;
  }
  return latest;
};

export function mergeCredentialQuotaListWindows(
  local: AccountQuotaDisplayWindow[],
  credential: AccountQuotaDisplayWindow[]
): AccountQuotaDisplayWindow[] {
  if (credential.length === 0) return local;
  const remoteKinds = new Set(credential.map((window) => window.kind));
  const codexScope = (window: AccountQuotaDisplayWindow): string | null => {
    if (window.source && window.source !== 'codex') return null;
    const scope = window.modelScope;
    if (
      !scope ||
      (scope.kind === 'all' && scope.complete !== false) ||
      isCodexMainQuotaModelScope(scope)
    )
      return 'main';
    return JSON.stringify([
      scope.kind,
      scope.key,
      [...(scope.models ?? [])].sort(),
      scope.complete,
    ]);
  };
  const sameSlot = (left: AccountQuotaDisplayWindow, right: AccountQuotaDisplayWindow): boolean => {
    if (left.source === 'codex' || right.source === 'codex') {
      return codexScope(left) !== null && codexScope(left) === codexScope(right);
    }
    return left.kind !== undefined && left.kind === right.kind;
  };
  // Newer local observations (including live「额度不足」) beat an older host cache.
  // Untimestamped local cache must not override a timestamped lastSuccess reading.
  const preferLocalOverRemote = (
    localWindow: AccountQuotaDisplayWindow,
    remoteWindow: AccountQuotaDisplayWindow
  ): boolean => {
    const localAt = quotaWindowObservedAtMs(localWindow);
    const remoteAt = quotaWindowObservedAtMs(remoteWindow);
    if (localAt > 0 && remoteAt > 0) return localAt >= remoteAt;
    if (localAt > 0 && remoteAt === 0) return true;
    if (localAt === 0 && remoteAt > 0) return false;
    return (
      isDepletedQuotaWindow(localWindow) &&
      !isDepletedQuotaWindow(remoteWindow) &&
      localAt === 0 &&
      remoteAt === 0
    );
  };
  const mergedRemote = credential.map((remote) => {
    const override = local.find(
      (candidate) => sameSlot(candidate, remote) && preferLocalOverRemote(candidate, remote)
    );
    return override ?? remote;
  });
  return [
    ...mergedRemote,
    ...local.filter((window) => {
      if (credential.some((remote) => sameSlot(window, remote))) {
        return false;
      }
      if (window.source === 'codex') return true;
      return (
        !remoteKinds.has(window.kind) ||
        window.modelScope?.kind !== 'all' ||
        window.modelScope.complete !== true
      );
    }),
  ];
}
