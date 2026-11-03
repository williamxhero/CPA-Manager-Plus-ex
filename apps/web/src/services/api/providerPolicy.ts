import type { BalanceBinding, OfficialBalanceStatus } from './officialBalance';

export interface ProviderPolicyStatus extends BalanceBinding {
  providerId: string;
  enabled: boolean;
  effective: boolean;
  generation: number;
  bindingCurrent: boolean;
  locked: boolean;
  manualDisabled: boolean;
  ownPaused: boolean;
  applied: boolean;
  state: string;
  reason: string;
  updatedAtMs: number;
  coordination?: { receiptPersisted: boolean; state: string };
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

export async function providerPolicyRequest(
  base: string,
  managementKey: string,
  binding: BalanceBinding,
  providerId: string,
  signal: AbortSignal,
  update?: { enabled: boolean; expectedGeneration: number }
): Promise<ProviderPolicyStatus> {
  const response = await fetch(
    `${base.replace(/\/$/, '')}/usage-service/provider-policy${update ? '' : `?providerId=${encodeURIComponent(providerId)}`}`,
    {
      method: update ? 'PUT' : 'GET',
      headers: { Authorization: `Bearer ${managementKey}`, 'Content-Type': 'application/json' },
      body: update ? JSON.stringify({ ...binding, providerId, ...update }) : undefined,
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
