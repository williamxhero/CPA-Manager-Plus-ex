import type { AuthFileItem, ClaudeQuotaState, ClaudeQuotaWindow } from '@/types';
import { isRecord } from '@/utils/helpers';
import { formatQuotaResetTime } from '@/utils/quota/formatters';
import { buildQuotaCredentialIdentity } from '@/utils/quota/credentialScope';

// Core publishes this credential-bound inference evidence independently of
// optional cooldowns. Reset expiry alone must never turn it into recovery.
export function applyOpenCodeQuotaObservation(
  file: AuthFileItem,
  cached: ClaudeQuotaState | undefined
): ClaudeQuotaState | undefined {
  const quota = isRecord(file.quota) ? file.quota : null;
  const signals = quota && isRecord(quota.signals) ? quota.signals : null;
  const observedAt = typeof quota?.observed_at === 'string' ? Date.parse(quota.observed_at) : NaN;
  const window = signals?.['OpenCode-Go-Window'];
  if (
    !signals ||
    !Number.isFinite(observedAt) ||
    observedAt <= 0 ||
    signals['OpenCode-Go-Remaining'] !== '0' ||
    (window !== 'rolling' && window !== 'weekly' && window !== 'monthly') ||
    (cached?.status === 'success' && (cached.fetchedAtMs ?? 0) > observedAt)
  )
    return cached;
  const reset = signals['OpenCode-Go-Reset-At'];
  const resetAt = typeof reset === 'string' ? Date.parse(reset) : NaN;
  const rejected: ClaudeQuotaWindow = {
    id: window,
    label: window,
    labelKey: `accounts.detail_snapshot_window_${window === 'rolling' ? 'five_hour' : window}`,
    usedPercent: 100,
    resetLabel: Number.isFinite(resetAt) ? formatQuotaResetTime(resetAt) : '-',
    resetAtMs: Number.isFinite(resetAt) ? resetAt : null,
    resetAccuracy: Number.isFinite(resetAt) ? 'estimated' : 'unknown',
    modelScope: { kind: 'all', complete: true },
    limitWindowSeconds: window === 'rolling' ? 18000 : window === 'weekly' ? 604800 : null,
  };
  const windows = cached?.windows?.filter((w) => w.id !== window) ?? [];
  return {
    ...cached,
    ...buildQuotaCredentialIdentity(file),
    status: 'success',
    windows: [...windows, rejected],
    fetchedAtMs: observedAt,
    planType: cached?.planType ?? 'OpenCode Go',
  };
}
