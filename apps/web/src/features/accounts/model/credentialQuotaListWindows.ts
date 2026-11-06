import type { AccountQuotaDisplayWindow, AccountQuotaWindowKind, AccountQuotaWindowSource } from './accountQuotaDisplayWindows';

type StoredWindow = {
  name?: string;
  usedPercent?: number;
  remainingFraction?: number;
  resetAt?: string;
};

const kinds: Record<string, AccountQuotaWindowKind> = {
  rolling: 'five_hour',
  weekly: 'weekly',
  monthly: 'monthly',
};
const modes = {
  rolling: 'rolling',
  weekly: 'calendar',
  monthly: 'calendar',
} as const;

export function credentialQuotaListWindows(
  raw: string | undefined,
  source: Extract<AccountQuotaWindowSource, 'opencode-go' | 'qwen'>
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
    if (!window.name || typeof window.usedPercent !== 'number' || !Number.isFinite(window.usedPercent)) {
      return [];
    }
    const resetAtMs = window.resetAt ? Date.parse(window.resetAt) : Number.NaN;
    const remaining =
      typeof window.remainingFraction === 'number' && Number.isFinite(window.remainingFraction)
        ? Math.max(0, Math.min(100, window.remainingFraction * 100))
        : Math.max(0, Math.min(100, 100 - window.usedPercent));
    return [
      {
        key: `${source}:${window.name}`,
        label: window.name,
        kind: kinds[window.name] ?? 'unknown',
        remainingPercent: remaining,
        usedPercent: window.usedPercent,
        resetLabel: window.resetAt ?? '',
        resetAccuracy: 'exact' as const,
        limitWindowSeconds: null,
        resetAtMs: Number.isFinite(resetAtMs) ? resetAtMs : null,
        fromMs: null,
        toMs: null,
        source,
        windowMode: modes[window.name as keyof typeof modes] ?? 'fixed',
      },
    ];
  });
}
