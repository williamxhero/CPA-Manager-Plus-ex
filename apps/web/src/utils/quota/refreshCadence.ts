const STORAGE_KEY = 'cpa.quotaRefreshIntervalMs';
export const QUOTA_REFRESH_INTERVALS = [0, 300000, 900000, 3600000, 21600000] as const;
const DEFAULT_INTERVAL_MS = 3600000;

export function readQuotaRefreshIntervalMs(): number {
  if (typeof localStorage === 'undefined') return DEFAULT_INTERVAL_MS;
  const raw = Number(localStorage.getItem(STORAGE_KEY));
  if (!Number.isFinite(raw) || raw < 0 || raw > 86_400_000) return DEFAULT_INTERVAL_MS;
  return raw;
}

export function writeQuotaRefreshIntervalMs(value: number) {
  if (typeof localStorage === 'undefined') return;
  localStorage.setItem(STORAGE_KEY, String(value));
}

export function quotaRefreshDue(fetchedAt: number): boolean {
  if (!fetchedAt) return true;
  const interval = readQuotaRefreshIntervalMs();
  if (interval <= 0) return false;
  return Date.now() - fetchedAt >= interval;
}

export function quotaRefreshDelayMs(fetchedAt: number): number | null {
  const interval = readQuotaRefreshIntervalMs();
  if (interval <= 0) return null;
  return Math.max(1000, fetchedAt + interval - Date.now());
}
