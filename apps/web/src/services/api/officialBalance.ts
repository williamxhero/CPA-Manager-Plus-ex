import type { ResourceScope, ResourceTarget } from './usageService';

export interface BalanceBinding {
  scope: ResourceScope;
  targets: ResourceTarget[];
}
export type OfficialBalanceAdapter =
  | 'deepseek'
  | 'deepinfra'
  | 'openai-compat-usage-v1'
  | 'volcengine-account-balance-v2022-01-01'
  | 'aliyun-prepaid-cash-v2017-12-14'
  | 'newapi-user-self-v1'
  | 'newapi-token-usage-v1'
  | 'newapi-browser-session-v1'
  | 'layso_billing_web_v1';
export interface BrowserSessionConfig {
  origin: string;
  dashboardPath: '/dashboard';
  browserInstanceId: string;
}
export interface WebBalanceSource {
  site: 'layso';
  accountId: string;
  tenantId: string;
  role: string;
  executionUser: string;
  browser: { family: 'microsoft-edge'; profile: 'Profile 1'; instanceId: string };
  currency: 'USD';
  parsingRule: string;
}
export interface VolcengineBalanceProfile {
  accountID: string;
  currency: string;
  unit: 'major';
  currencyUnitConfirmed: boolean;
  accountBindingConfirmed: boolean;
  proofReference: string;
}
export interface AliyunBalanceProfile {
  site: string;
  accountID: string;
  accountBindingConfirmed: boolean;
  proofReference: string;
}
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
  webEvidence?: {
    authority: string;
    identityHash: string;
    proofHash: string;
    parsingRule: string;
    verification: string;
  };
  aliyunAccount?: {
    accountID: string;
    identityType: 'Account' | 'RAMUser';
    proofReference: string;
    qualification: 'caller_account_matched_operator_asserted_source_binding_not_key_or_payer_verified';
  };
  volcengineAccount?: {
    accountID: string;
    unit: 'major';
    proofReference: string;
    qualification: 'operator_asserted_binding_and_units_not_live_verified';
  };
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
  observedAtMs?: number;
  available?: boolean;
  balances?: { currency: string; total: string; granted: string; toppedUp: string }[];
}
export interface OfficialBalanceStatus extends BalanceBinding {
  web?: WebBalanceSource;
  reason?: string;
  webSourceAuthority?: string;
  aliyun?: AliyunBalanceProfile;
  volcengine?: VolcengineBalanceProfile;
  newAPI?: NewAPIConfig;
  browserSession?: BrowserSessionConfig;
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

export class OfficialBalanceRequestError extends Error {
  constructor(readonly status: number) {
    super(status === 409 ? 'identity_or_configuration_changed' : 'balance_request_failed');
    this.name = 'OfficialBalanceRequestError';
  }
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
  if (!response.ok) throw new OfficialBalanceRequestError(response.status);
  return response.json() as Promise<T>;
}
