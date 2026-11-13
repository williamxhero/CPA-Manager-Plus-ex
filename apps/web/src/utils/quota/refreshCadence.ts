import { loadRuntimeControls, readRuntimeControls, writeRuntimeControls } from './runtimeControls';

export const QUOTA_REFRESH_INTERVALS = [0, 300000, 900000, 1_800_000, 3600000, 21600000] as const;

export function readQuotaRefreshIntervalMs(): number {
  return readRuntimeControls().quotaRefreshIntervalMs;
}

export function writeQuotaRefreshIntervalMs(value: number) {
  void (async () => {
    await loadRuntimeControls();
    await writeRuntimeControls({ ...readRuntimeControls(), quotaRefreshIntervalMs: value });
  })();
}

export function quotaRefreshDue(fetchedAt = readRuntimeControls().quotaFetchedAtMs): boolean {
  if (!fetchedAt) return true;
  const interval = readQuotaRefreshIntervalMs();
  if (interval <= 0) return false;
  return Date.now() - fetchedAt >= interval;
}

export function quotaRefreshDelayMs(fetchedAt = readRuntimeControls().quotaFetchedAtMs): number | null {
  const interval = readQuotaRefreshIntervalMs();
  if (interval <= 0) return null;
  return Math.max(1000, fetchedAt + interval - Date.now());
}
