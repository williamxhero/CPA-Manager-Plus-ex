import type { BalanceBinding } from './officialBalance';

export interface ProviderPolicyBinding extends BalanceBinding {
  providerId: string;
}

export interface ProviderPolicyFunding {
  memberIds: string[];
  query: BalanceBinding & { version: number };
  keyMatched: boolean;
  fresh: boolean;
  reason: string;
  balances?: { currency: string; total: string }[];
  observedAtMs?: number;
}

/** Saved intent is not a routing acknowledgement or effective availability. */
export interface ProviderPolicyStatus extends ProviderPolicyBinding {
  subjectId: string;
  enabled: boolean;
  effective: boolean;
  generation: number;
  owner: string;
  source: string;
  updatedAtMs: number;
  bindingCurrent: boolean;
  locked: boolean;
  manualDisabled: boolean;
  ownPaused: boolean;
  applied: boolean;
  recoveryAvailable?: boolean;
  gateGeneration?: number;
  sourceVersion?: string;
  state: 'off' | 'observing' | 'paused' | 'unavailable' | 'pending';
  reason: string;
  funding?: ProviderPolicyFunding[];
}

export class ProviderPolicyRequestError extends Error {
  readonly status: number;
  constructor(status: number) {
    super(status === 409 ? 'provider_policy_conflict' : 'provider_policy_unavailable');
    this.status = status;
  }
}

const scopeFields = [
  'instance_id',
  'instance_incarnation',
  'boot_epoch',
  'snapshot_revision',
] as const;
const targetFields = [
  'resource_id',
  'incarnation',
  'identity_version',
  'credential_version',
  'config_version',
  'membership_version',
] as const;
const uuid = (v: unknown) =>
  typeof v === 'string' && /^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$/.test(v);
const record = (v: unknown): v is Record<string, unknown> =>
  !!v && typeof v === 'object' && !Array.isArray(v);

export function validProviderPolicyBinding(binding: ProviderPolicyBinding): boolean {
  return (
    uuid(binding.providerId) &&
    record(binding.scope) &&
    scopeFields.every((key) =>
      key === 'snapshot_revision'
        ? Number.isSafeInteger(binding.scope[key]) && binding.scope[key] > 0
        : uuid(binding.scope[key])
    ) &&
    Array.isArray(binding.targets) &&
    binding.targets.length > 1 &&
    binding.targets.length <= 64 &&
    binding.targets.every(
      (target) =>
        record(target) &&
        targetFields.every((key) =>
          key === 'resource_id' || key === 'incarnation'
            ? uuid(target[key])
            : Number.isSafeInteger(target[key]) &&
              target[key] >= (key === 'membership_version' ? 0 : 1)
        )
    ) &&
    binding.targets[0].resource_id === binding.providerId &&
    new Set(binding.targets.map((target) => target.resource_id)).size === binding.targets.length
  );
}

function readStatus(value: unknown, binding: ProviderPolicyBinding): ProviderPolicyStatus {
  if (
    !record(value) ||
    value.providerId !== binding.providerId ||
    !record(value.scope) ||
    !scopeFields.every(
      (key) => (value.scope as Record<string, unknown>)[key] === binding.scope[key]
    ) ||
    !Array.isArray(value.targets) ||
    value.targets.length !== binding.targets.length ||
    !binding.targets.every(
      (target) =>
        (value.targets as unknown[]).filter(
          (candidate) =>
            record(candidate) && targetFields.every((key) => candidate[key] === target[key])
        ).length === 1
    ) ||
    ![
      'enabled',
      'effective',
      'bindingCurrent',
      'locked',
      'manualDisabled',
      'ownPaused',
      'applied',
    ].every((key) => typeof value[key] === 'boolean') ||
    !['subjectId', 'owner', 'source'].every(
      (key) => typeof value[key] === 'string' && value[key].length > 0
    ) ||
    typeof value.reason !== 'string' ||
    !['off', 'observing', 'paused', 'unavailable', 'pending'].includes(String(value.state)) ||
    !Number.isSafeInteger(value.generation) ||
    Number(value.generation) < 0 ||
    !Number.isSafeInteger(value.updatedAtMs) ||
    Number(value.updatedAtMs) < 0 ||
    (value.recoveryAvailable !== undefined && typeof value.recoveryAvailable !== 'boolean') ||
    (value.gateGeneration !== undefined &&
      (!Number.isSafeInteger(value.gateGeneration) || Number(value.gateGeneration) < 0)) ||
    (value.sourceVersion !== undefined &&
      (typeof value.sourceVersion !== 'string' ||
        !/^[0-9a-f]{64}$/.test(value.sourceVersion)))
  ) {
    throw new ProviderPolicyRequestError(409);
  }
  // Optional funding is display-only and must be scoped to these exact issued
  // targets. Malformed/old summaries are unknown, never a synthetic zero.
  return {
    ...value,
    funding: readFunding(value.funding, binding),
  } as unknown as ProviderPolicyStatus;
}

function readFunding(
  value: unknown,
  binding: ProviderPolicyBinding
): ProviderPolicyFunding[] | undefined {
  if (!Array.isArray(value) || value.length > 64) return undefined;
  const result: ProviderPolicyFunding[] = [];
  for (const source of value) {
    if (
      !record(source) ||
      !Array.isArray(source.memberIds) ||
      !source.memberIds.every((id) =>
        binding.targets.slice(1).some((target) => target.resource_id === id)
      ) ||
      new Set(source.memberIds).size !== source.memberIds.length ||
      typeof source.keyMatched !== 'boolean' ||
      typeof source.fresh !== 'boolean' ||
      typeof source.reason !== 'string' ||
      !record(source.query) ||
      !record(source.query.scope) ||
      !scopeFields.every(
        (key) => (source.query as BalanceBinding).scope[key] === binding.scope[key]
      ) ||
      !Number.isSafeInteger(source.query.version) ||
      Number(source.query.version) < 0 ||
      !Array.isArray(source.query.targets) ||
      source.query.targets.length === 0 ||
      !source.query.targets.every(
        (candidate) =>
          record(candidate) &&
          binding.targets.some((target) =>
            targetFields.every((key) => candidate[key] === target[key])
          )
      ) ||
      (source.observedAtMs !== undefined &&
        (!Number.isSafeInteger(source.observedAtMs) ||
          Number(source.observedAtMs) <= 0 ||
          Number(source.observedAtMs) > 8640000000000000))
    )
      return undefined;
    if (
      source.balances !== undefined &&
      (!Array.isArray(source.balances) ||
        source.balances.length > 32 ||
        !source.balances.every(
          (balance) =>
            record(balance) &&
            typeof balance.currency === 'string' &&
            /^[A-Z]{3}$/.test(balance.currency) &&
            typeof balance.total === 'string' &&
            /^-?(0|[1-9][0-9]{0,59})(\.[0-9]{1,30})?$/.test(balance.total)
        ))
    )
      return undefined;
    result.push(source as unknown as ProviderPolicyFunding);
  }
  return result;
}

export function providerBindingKey(binding: BalanceBinding): string {
  return JSON.stringify([
    binding.scope,
    [...binding.targets]
      .map((t) => [
        t.resource_id,
        t.incarnation,
        t.identity_version,
        t.credential_version,
        t.config_version,
        t.membership_version,
      ])
      .sort(),
  ]);
}

interface PolicyChange {
  base: string;
  managementKey: string;
  providerId: string;
}
const changeListeners = new Set<(change: PolicyChange) => void>();
export function subscribeProviderPolicyChanges(listener: (change: PolicyChange) => void) {
  changeListeners.add(listener);
  return () => {
    changeListeners.delete(listener);
  };
}
function notifyPolicyChange(base: string, managementKey: string, providerId: string) {
  changeListeners.forEach((listener) => listener({ base, managementKey, providerId }));
}

async function request(
  base: string,
  managementKey: string,
  binding: ProviderPolicyBinding,
  signal?: AbortSignal,
  update?: { expectedGeneration: number; enabled: boolean }
): Promise<ProviderPolicyStatus> {
  const query = update ? '' : `?providerId=${encodeURIComponent(binding.providerId)}`;
  const response = await fetch(`${base.replace(/\/$/, '')}/usage-service/provider-policy${query}`, {
    method: update ? 'PUT' : 'GET',
    headers: { Authorization: `Bearer ${managementKey}`, 'Content-Type': 'application/json' },
    body: update ? JSON.stringify({ ...binding, ...update }) : undefined,
    signal,
    cache: 'no-store',
    redirect: 'error',
  });
  if (!response.ok) throw new ProviderPolicyRequestError(response.status);
  return readStatus(await response.json(), binding);
}

export const getProviderPolicy = (
  base: string,
  managementKey: string,
  binding: ProviderPolicyBinding,
  signal?: AbortSignal
) => request(base, managementKey, binding, signal);

export async function saveProviderPolicy(
  base: string,
  managementKey: string,
  binding: ProviderPolicyBinding,
  expectedGeneration: number,
  enabled: boolean,
  signal?: AbortSignal
): Promise<ProviderPolicyStatus> {
  try {
    return await request(base, managementKey, binding, signal, { expectedGeneration, enabled });
  } finally {
    // An unacknowledged write can still have persisted. Revalidate only the
    // matching connection/auth scope, including failed mutation attempts.
    notifyPolicyChange(base, managementKey, binding.providerId);
  }
}

export interface ProviderPolicyMigrationStatus {
  dialect: string;
  ready: boolean;
  pendingCount: number;
  reason?: string;
  protectionHold: boolean;
}

export async function getProviderPolicyMigration(
  base: string,
  managementKey: string,
  signal?: AbortSignal
): Promise<ProviderPolicyMigrationStatus> {
  const response = await fetch(
    `${base.replace(/\/$/, '')}/usage-service/provider-policy/migration`,
    {
      method: 'GET',
      headers: { Authorization: `Bearer ${managementKey}` },
      signal,
      cache: 'no-store',
      redirect: 'error',
    }
  );
  if (!response.ok) throw new ProviderPolicyRequestError(response.status);
  const value = await response.json();
  if (
    !record(value) ||
    typeof value.dialect !== 'string' ||
    typeof value.ready !== 'boolean' ||
    typeof value.protectionHold !== 'boolean' ||
    !Number.isSafeInteger(value.pendingCount) ||
    Number(value.pendingCount) < 0 ||
    (value.reason !== undefined && typeof value.reason !== 'string')
  ) {
    throw new ProviderPolicyRequestError(409);
  }
  return value as unknown as ProviderPolicyMigrationStatus;
}

export async function recoverProviderPolicy(
  base: string,
  managementKey: string,
  binding: ProviderPolicyBinding,
  expectedGeneration: number,
  expectedGateGeneration: number,
  sourceVersion: string,
  signal?: AbortSignal
): Promise<ProviderPolicyStatus> {
  try {
    const response = await fetch(
      `${base.replace(/\/$/, '')}/usage-service/provider-policy/recover`,
      {
        method: 'POST',
        headers: { Authorization: `Bearer ${managementKey}`, 'Content-Type': 'application/json' },
        body: JSON.stringify({
          ...binding,
          expectedGeneration,
          expectedGateGeneration,
          sourceVersion,
        }),
        signal,
        cache: 'no-store',
        redirect: 'error',
      }
    );
    if (!response.ok) throw new ProviderPolicyRequestError(response.status);
    return readStatus(await response.json(), binding);
  } finally {
    notifyPolicyChange(base, managementKey, binding.providerId);
  }
}
