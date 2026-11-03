import type { BalanceBinding, OfficialBalanceStatus } from './officialBalance';

export interface ProviderPolicyStatus extends BalanceBinding {
  providerId: string;
  enabled: boolean;
  effective: boolean;
  generation: number;
  source: string;
  bindingCurrent: boolean;
  locked: boolean;
  manualDisabled: boolean;
  ownPaused: boolean;
  applied: boolean;
  state: string;
  reason: string;
  updatedAtMs: number;
  recoveryAvailable?: boolean;
  coordination?: {
    receiptPersisted: boolean;
    state: string;
    generation?: number;
    sourceVersion?: string;
  };
  sources: OfficialBalanceStatus[];
}

// Compare issued identities, including all member/version fences. Object key
// order and the server's canonical member order are not identity changes.
export function providerBindingKey(binding: BalanceBinding): string {
  const s = binding.scope;
  return JSON.stringify([
    s.instance_id,
    s.instance_incarnation,
    s.boot_epoch,
    s.snapshot_revision,
    binding.targets
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

interface PolicyChange { base: string; managementKey: string; providerId: string }
const changeListeners = new Set<(change: PolicyChange) => void>();
export function subscribeProviderPolicyChanges(listener: (change: PolicyChange) => void) {
  changeListeners.add(listener);
  return () => { changeListeners.delete(listener); };
}

export async function providerPolicyRequest(...args: Parameters<typeof request>) {
  try {
    return await request(...args);
  } finally {
    // An unacknowledged write can still have persisted. Revalidate only the
    // matching connection/auth scope, including failed recovery attempts.
    if (args[5]) changeListeners.forEach(listener => listener({ base: args[0], managementKey: args[1], providerId: args[3] }));
  }
}

async function request(
  base: string,
  managementKey: string,
  binding: BalanceBinding,
  providerId: string,
  signal: AbortSignal,
  update?:
    | { enabled: boolean; expectedGeneration: number }
    | {
        action: 'recover';
        expectedGeneration: number;
        expectedGateGeneration: number;
        sourceVersion: string;
      }
): Promise<ProviderPolicyStatus> {
  const recovery = update && 'action' in update;
  const body = recovery
    ? {
        ...binding,
        providerId,
        expectedGeneration: update.expectedGeneration,
        expectedGateGeneration: update.expectedGateGeneration,
        sourceVersion: update.sourceVersion,
      }
    : update
      ? { ...binding, providerId, ...update }
      : undefined;
  const response = await fetch(
    `${base.replace(/\/$/, '')}/usage-service/provider-policy${recovery ? '/recover' : update ? '' : `?providerId=${encodeURIComponent(providerId)}`}`,
    {
      method: recovery ? 'POST' : update ? 'PUT' : 'GET',
      headers: { Authorization: `Bearer ${managementKey}`, 'Content-Type': 'application/json' },
      body: body ? JSON.stringify(body) : undefined,
      signal,
      cache: 'no-store',
      redirect: 'error',
    }
  );
  // Never echo upstream/auth bodies or query credentials to the page.
  if (!response.ok) throw new Error('资金保护请求失败，请刷新确认');
  const data: ProviderPolicyStatus = await response.json();
  if (
    !data ||
    !data.scope ||
    !Array.isArray(data.targets) ||
    data.providerId !== providerId ||
    providerBindingKey(data) !== providerBindingKey(binding) ||
    data.bindingCurrent !== true ||
    typeof data.enabled !== 'boolean' ||
    !Number.isSafeInteger(data.generation) ||
    data.generation < 0 ||
    !Array.isArray(data.sources) ||
    data.sources.some(
      (s) =>
        !s ||
        !s.scope ||
        !Array.isArray(s.targets) ||
        s.targets.length !== 1 ||
        s.targets[0].resource_id === providerId ||
        !binding.targets.some(
          (t) =>
            providerBindingKey({ scope: binding.scope, targets: [t] }) === providerBindingKey(s)
        )
    )
  ) {
    throw new Error('资金保护身份已变化，请刷新资源列表');
  }
  return data;
}
