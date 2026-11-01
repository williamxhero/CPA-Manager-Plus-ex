import type { ResourceScope, ResourceTarget } from './usageService';
import type { BalanceObservation } from './officialBalance';
import type { CredentialQuotaObservation } from './credentialQuota';

export interface RefreshSource {
  id: string;
  queryCapable: boolean;
  request: {
    kind: string;
    credential?: { auth_index: string; [key: string]: unknown };
    official?: { [key: string]: unknown };
  };
  targets: ResourceTarget[];
  state: string;
  reason?: string;
  stale: boolean;
  attempt?: BalanceObservation & Partial<CredentialQuotaObservation>;
  lastSuccess?: BalanceObservation & Partial<CredentialQuotaObservation>;
}
export interface RefreshProgress {
  id: string;
  state: string;
  pending: number;
  succeeded: number;
  failed: number;
  notQueried: number;
  queueDeadlineMs: number;
  items: RefreshSource[];
}
export interface RefreshInventory {
  scope: ResourceScope;
  sources: RefreshSource[];
  latestRound?: RefreshProgress;
}
export async function fullRefreshRequest<T>(
  base: string,
  key: string,
  body?: unknown,
  id?: string,
  signal?: AbortSignal
): Promise<T> {
  const response = await fetch(
    `${base.replace(/\/$/, '')}/usage-service/full-refresh${id ? `?id=${encodeURIComponent(id)}` : ''}`,
    {
      method: body === undefined ? 'GET' : 'POST',
      headers: { Authorization: `Bearer ${key}`, 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
      cache: 'no-store',
      redirect: 'error',
      signal,
    }
  );
  if (!response.ok)
    throw new Error(
      response.status === 409 ? 'identity_or_source_changed' : 'common_refresh_unavailable'
    );
  return response.json() as Promise<T>;
}
// Polling observes ONE explicitly requested backend job. It never schedules
// private quota acquisition, browser recovery or periodic refresh.
export async function waitFullRefresh(
  base: string,
  key: string,
  job: RefreshProgress,
  signal?: AbortSignal,
  onProgress?: (p: RefreshProgress) => void
) {
  const deadline = Date.now() + 60_000;
  while (job.state === 'running' && Date.now() < deadline && !signal?.aborted) {
    onProgress?.(job);
    await new Promise((r) => setTimeout(r, 150));
    signal?.throwIfAborted();
    job = await fullRefreshRequest<RefreshProgress>(base, key, undefined, job.id, signal);
  }
  signal?.throwIfAborted();
  onProgress?.(job);
  if (job.state === 'running' || job.state === 'cancelled' || job.state === 'discarded')
    throw new Error('common_refresh_not_completed');
  return job;
}
export async function refreshCredentialSource(
  base: string,
  key: string,
  authIndex: string,
  signal?: AbortSignal,
  onProgress?: (p: RefreshProgress) => void
) {
  const inventory = await fullRefreshRequest<RefreshInventory>(
    base,
    key,
    undefined,
    undefined,
    signal
  );
  const matches = inventory.sources.filter(
    (s) => s.request.kind === 'credential_quota' && s.request.credential?.auth_index === authIndex
  );
  if (matches.length !== 1 || !matches[0].queryCapable)
    throw new Error('current_credential_source_unavailable');
  const job = await fullRefreshRequest<RefreshProgress>(
    base,
    key,
    { purpose: 'manual', source: matches[0].request },
    undefined,
    signal
  );
  const completed = await waitFullRefresh(base, key, job, signal, onProgress);
  if (completed.failed) throw new Error('common_refresh_source_failed');
  return completed;
}
