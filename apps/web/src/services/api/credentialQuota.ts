import type { ResourceScope, ResourceTarget } from './usageService';

export interface QuotaBinding {
  scope: ResourceScope;
  targets: ResourceTarget[];
}
export interface CredentialQuotaSource extends QuotaBinding {
  sourceVersion: string;
  sourceId: string;
  subjectId: string;
  queryVersion?: number;
  auth_index?: string;
  protocol: string;
  state: string;
  reason: string;
  accountProvenance: string;
  manualDisabled: boolean;
}
export interface CredentialQuotaObservation {
  queryId: string;
  queryStatus: string;
  observedAtMs: number;
  windowsJson?: string;
  errorCode?: string;
}
export interface CredentialQuotaStatus extends CredentialQuotaSource {
  nextPeriodicCheckMs?: number;
  credentialQuery: { state: 'available' | 'unknown'; reason: string };
  version: number;
  stale: boolean;
  coreGate: false;
  attempt?: CredentialQuotaObservation;
  lastSuccess?: CredentialQuotaObservation;
}
export interface CredentialQuotaWindow {
  name: string;
  model?: string;
  usedPercent: number;
  remainingFraction: number;
  resetAt?: string;
  sourceStatus?: string;
  limitWindowSeconds?: number;
}
export interface QuotaWindows {
  windows: CredentialQuotaWindow[];
  complete: boolean;
  accountProvenance: string;
  recoveryEligible: boolean;
}
export interface CredentialQuotaJob {
  id: string;
  state: string;
  deadlineMs: number;
}

// A 409 only says "the request conflicts with the current source state". The
// machine reason distinguishes a changed binding from a credential that simply
// has no bound source, so the operator is not told to refresh a page that will
// keep failing.
const QUOTA_FAILURE_CODES: Record<string, string> = {
  identity_changed: 'identity_or_source_changed',
  source_version_conflict: 'identity_or_source_changed',
  current_credential_source_unavailable: 'current_credential_source_unavailable',
};

const quotaFailureCode = async (response: Response): Promise<string> => {
  let reason = '';
  try {
    const body: unknown = await response.json();
    if (body && typeof body === 'object' && typeof (body as { error?: unknown }).error === 'string')
      reason = (body as { error: string }).error;
  } catch {
    /* Non-JSON rejection: fall back to the HTTP status. */
  }
  return (
    QUOTA_FAILURE_CODES[reason] ??
    (response.status === 409 ? 'identity_or_source_changed' : 'credential_quota_request_failed')
  );
};

// GET reads persisted observations only. No legacy API-call/credential download.
export async function credentialQuotaRequest<T>(
  base: string,
  managementKey: string,
  method: 'GET' | 'POST',
  query: string,
  body?: unknown,
  signal?: AbortSignal
): Promise<T> {
  const response = await fetch(
    `${base.replace(/\/$/, '')}/usage-service/credential-quota${query}`,
    {
      method,
      headers: { Authorization: `Bearer ${managementKey}`, 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
      signal,
      cache: 'no-store',
      redirect: 'error',
    }
  );
  if (!response.ok) throw new Error(await quotaFailureCode(response));
  return response.json() as Promise<T>;
}
