import type { ResourceScope, ResourceTarget } from './usageService';

export interface QuotaBinding {
  scope: ResourceScope;
  targets: ResourceTarget[];
}
export interface CredentialQuotaSource extends QuotaBinding {
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
  if (!response.ok)
    throw new Error(
      response.status === 409 ? 'identity_or_source_changed' : 'credential_quota_request_failed'
    );
  return response.json() as Promise<T>;
}
