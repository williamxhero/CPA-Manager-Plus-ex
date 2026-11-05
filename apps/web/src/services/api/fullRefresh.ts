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
  authIndex?: string;
  sourceId?: string;
  subjectId?: string;
  queryVersion?: number;
  sourceVersion?: string;
  state: string;
  reason?: string;
  stale: boolean;
  attempt?: BalanceObservation & Partial<CredentialQuotaObservation>;
  lastSuccess?: BalanceObservation & Partial<CredentialQuotaObservation>;
}
export interface CredentialRefreshBinding {
  scope: ResourceScope;
  targets: ResourceTarget[];
  authIndex: string;
  sourceId: string;
  subjectId: string;
  queryVersion: number;
  sourceVersion: string;
}

const canonical = (value: unknown): unknown => {
  if (Array.isArray(value)) return value.map(canonical);
  if (value && typeof value === 'object') {
    return Object.fromEntries(
      Object.entries(value as Record<string, unknown>)
        .sort(([a], [b]) => a.localeCompare(b))
        .map(([key, item]) => [key, canonical(item)])
    );
  }
  return value;
};
const sameCanonicalValue = (a: unknown, b: unknown) =>
  JSON.stringify(canonical(a)) === JSON.stringify(canonical(b));
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
  binding: CredentialRefreshBinding,
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
  if (!sameCanonicalValue(inventory.scope, binding.scope))
    throw new Error('identity_or_source_changed');
  const matches = inventory.sources.filter((s) => {
    const credential = s.request.credential;
    return (
      s.request.kind === 'credential_quota' &&
      credential?.auth_index === binding.authIndex &&
      credential?.sourceId === binding.sourceId &&
      credential?.subjectId === binding.subjectId &&
      credential?.version === binding.queryVersion &&
      s.sourceId === binding.sourceId &&
      s.subjectId === binding.subjectId &&
      // The Go inventory summary omits zero, but the issued request's version
      // above is mandatory and must still match exactly (including first use).
      (s.queryVersion === undefined ? 0 : s.queryVersion) === binding.queryVersion &&
      credential?.sourceVersion === binding.sourceVersion &&
      sameCanonicalValue(s.targets, binding.targets) &&
      sameCanonicalValue(credential.scope, binding.scope) &&
      sameCanonicalValue(credential.targets, binding.targets)
    );
  });
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
  if (completed.failed) {
    const failed = completed.items.find(
      (item) => item.attempt?.queryStatus && item.attempt.queryStatus !== 'success'
    );
    throw new Error(failed?.attempt?.errorCode || failed?.reason || 'common_refresh_source_failed');
  }
  return completed;
}
