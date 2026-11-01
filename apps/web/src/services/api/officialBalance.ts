import type { ResourceScope, ResourceTarget } from './usageService';

export interface BalanceBinding {
  scope: ResourceScope;
  targets: ResourceTarget[];
}
export type OfficialBalanceAdapter =
  | 'deepseek'
  | 'deepinfra'
  | 'newapi-user-self-v1'
  | 'newapi-token-usage-v1';
export interface NewAPIConfig {
  conversion?: NewAPIConversionProfile;
  protocolConfirmed: boolean;
  instanceVersion: string;
  auth: string;
  userID?: string;
  accountScope?: string;
  nativeUnit: 'newapi_quota';
}
export interface NewAPIConversionProfile {
  confirmed: boolean;
  nativeUnit: 'newapi_quota';
  currency: string;
  moneyPerNativeQuota: string;
  proofReference: string;
  contractVersion: string;
}

export interface BalanceObservation {
  conversion?: NewAPIConversionProfile & {
    qualification: 'operator_asserted_contract_not_live_verified';
  };
  nativeQuota?: {
    unit: 'newapi_quota';
    state: 'finite' | 'unlimited' | 'unknown';
    remaining?: string;
    unlimited?: boolean;
  };
  semantic?: string;
  amountDecimal?: string;
  currency?: string;
  errorCode?: string;
  queryStatus: string;
  observedAtMs: number;
  available?: boolean;
  balances?: { currency: string; total: string; granted: string; toppedUp: string }[];
}
export interface OfficialBalanceStatus extends BalanceBinding {
  newAPI?: NewAPIConfig;
  version: number;
  adapter: string;
  endpoint: string;
  privateTargets?: string[];
  timeoutMs?: number;
  configured: boolean;
  state: string;
  managerDirectQuery: boolean;
  coreQuery: boolean;
  coreGate: boolean;
  stale: boolean;
  attempt?: BalanceObservation;
  lastSuccess?: BalanceObservation;
}
export interface BalanceJob {
  id: string;
  state: string;
  status?: OfficialBalanceStatus;
}

// Only explicit query credentials are submitted, never inferred/exported Core keys.
// Generic errors deliberately exclude request bodies, upstream text and secrets.
export async function officialBalanceRequest<T>(
  base: string,
  managementKey: string,
  method: 'GET' | 'PUT' | 'POST',
  query: string,
  body?: unknown,
  signal?: AbortSignal
): Promise<T> {
  const response = await fetch(
    `${base.replace(/\/$/, '')}/usage-service/official-balance${query}`,
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
      response.status === 409 ? 'identity_or_configuration_changed' : 'balance_request_failed'
    );
  return response.json() as Promise<T>;
}
