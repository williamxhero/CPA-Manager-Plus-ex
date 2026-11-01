import { useCallback, useLayoutEffect, useMemo, useRef, useState } from 'react';
import type { PanelFeatureAvailability } from '@/hooks/usePanelFeatureAvailability';
import {
  usageServiceApi,
  type RuntimeResourceBinding,
  type ResourceScope,
  type ResourceTarget,
} from '@/services/api/usageService';
import { useAuthStore } from '@/stores';
import { useUsageServiceStore } from '@/stores/useUsageServiceStore';

export type ResourceProtectionCapability =
  | 'unknown'
  | 'unavailable'
  | 'identity_only'
  | 'unsupported_mode'
  | 'unbound';
type ManagerAvailability = Pick<
  PanelFeatureAvailability,
  'checking' | 'managerServiceAvailable' | 'managerServiceBase'
>;

const isRecord = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value);

function readCapability(value: unknown): ResourceProtectionCapability {
  if (!isRecord(value) || value.schemaVersion !== 1) return 'unknown';
  const { identity, query, independentGate, policy } = value;
  if (
    !isRecord(identity) ||
    identity.state !== 'unknown' ||
    identity.reason !== 'core_contract_unverified' ||
    !isRecord(query) ||
    query.state !== 'unknown' ||
    query.reason !== 'query_contract_unverified' ||
    !isRecord(independentGate) ||
    independentGate.state !== 'unavailable' ||
    independentGate.reason !== 'consumer_contract_unconfigured' ||
    !isRecord(policy) ||
    typeof policy.source !== 'string' ||
    (policy.updatedAtMs !== undefined &&
      (typeof policy.updatedAtMs !== 'number' || !Number.isFinite(policy.updatedAtMs)))
  )
    return 'unknown';
  for (const field of ['codexQuotaCooldown', 'authIssueQueue', 'authIssueAutoDisable']) {
    const capability = policy[field];
    if (
      !isRecord(capability) ||
      typeof capability.enabled !== 'boolean' ||
      typeof capability.envKey !== 'string' ||
      typeof capability.configFileKey !== 'string' ||
      (capability.locked !== undefined && typeof capability.locked !== 'boolean') ||
      (capability.configured !== undefined && typeof capability.configured !== 'boolean') ||
      (capability.source !== undefined && typeof capability.source !== 'string') ||
      (capability.dependsOn !== undefined && typeof capability.dependsOn !== 'string')
    ) {
      return 'unknown';
    }
  }
  if (value.negotiated !== undefined) {
    const negotiated = value.negotiated;
    if (
      !isRecord(negotiated) ||
      negotiated.contractVersion !== 1 ||
      !isRecord(negotiated.identity) ||
      typeof negotiated.policyLocked !== 'boolean' ||
      !isRecord(negotiated.independentGate) ||
      negotiated.independentGate.state !== 'unavailable' ||
      negotiated.independentGate.reason !== 'consumer_contract_unconfigured'
    )
      return 'unknown';
    if (negotiated.identity.state === 'unknown') return 'unknown';
    if (
      negotiated.identity.state === 'unavailable' &&
      negotiated.identity.reason === 'unsupported_mode'
    )
      return 'unsupported_mode';
    if (
      negotiated.identity.state === 'unavailable' &&
      [
        'not_enabled',
        'unsupported_mode',
        'unsupported_storage',
        'unsupported_filesystem',
        'not_ready',
      ].includes(String(negotiated.identity.reason))
    )
      return 'unavailable';
    const snapshot = negotiated.snapshot;
    if (
      negotiated.identity.state === 'available' &&
      negotiated.identity.reason === 'durable_inventory' &&
      isRecord(snapshot) &&
      snapshot.version === 1 &&
      snapshot.complete === true &&
      Array.isArray(snapshot.resources) &&
      ['instance_id', 'instance_incarnation', 'boot_epoch'].every(
        (key) =>
          typeof snapshot[key] === 'string' &&
          /^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$/.test(snapshot[key])
      ) &&
      Number.isSafeInteger(snapshot.snapshot_revision) &&
      Number(snapshot.snapshot_revision) > 0
    )
      return 'identity_only';
    return 'unknown';
  }
  // Only this manager's unconfigured integration is recognized. Even a global
  // "available" flag cannot establish a positive, bound per-resource contract.
  return 'unavailable';
}

// Validate only public producer evidence; never join by name, key or position.
const scopeKeys = [
  'instance_id',
  'instance_incarnation',
  'boot_epoch',
  'snapshot_revision',
] as const;
const targetKeys = [
  'resource_id',
  'incarnation',
  'identity_version',
  'credential_version',
  'config_version',
  'membership_version',
] as const;
const uuid = (value: unknown): value is string =>
  typeof value === 'string' && /^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$/.test(value);
function readTarget(value: unknown): ResourceTarget | null {
  if (!isRecord(value) || !uuid(value.resource_id) || !uuid(value.incarnation)) return null;
  for (const key of [
    'identity_version',
    'credential_version',
    'config_version',
    'membership_version',
  ]) {
    if (
      !Number.isSafeInteger(value[key]) ||
      Number(value[key]) < (key === 'membership_version' ? 0 : 1)
    )
      return null;
  }
  return {
    resource_id: value.resource_id,
    incarnation: value.incarnation,
    identity_version: Number(value.identity_version),
    credential_version: Number(value.credential_version),
    config_version: Number(value.config_version),
    membership_version: Number(value.membership_version),
  };
}
function readBindings(value: unknown): RuntimeResourceBinding[] {
  if (!isRecord(value) || !isRecord(value.negotiated)) return [];
  const n = value.negotiated;
  if (
    !isRecord(n.bindingCapability) ||
    n.bindingCapability.state !== 'available' ||
    n.bindingCapability.reason !== 'host_issued_bindings' ||
    !isRecord(n.snapshot) ||
    !Array.isArray(n.snapshot.resources) ||
    !Array.isArray(n.bindings)
  )
    return [];
  const snapshot = n.snapshot;
  const resources = n.snapshot.resources;
  const accepted: RuntimeResourceBinding[] = [];
  const seen = new Set<string>();
  for (const binding of n.bindings) {
    if (
      !isRecord(binding) ||
      typeof binding.auth_index !== 'string' ||
      !/^[0-9a-f]{16}$/.test(binding.auth_index) ||
      seen.has(binding.auth_index) ||
      !isRecord(binding.scope) ||
      !scopeKeys.every(
        (key) => binding.scope && isRecord(binding.scope) && binding.scope[key] === snapshot[key]
      ) ||
      !Array.isArray(binding.targets) ||
      binding.targets.length === 0
    )
      return [];
    const targets: ResourceTarget[] = [];
    for (const target of binding.targets) {
      const parsed = readTarget(target);
      if (
        !parsed ||
        !resources.some(
          (row) =>
            isRecord(row) &&
            row.identity === 'valid' &&
            row.catalog_readiness === 'ready' &&
            targetKeys.every((key) => row[key] === parsed[key])
        )
      )
        return [];
      targets.push(parsed);
    }
    if (binding.runtime_alias !== targets[0].resource_id) return [];
    const scope: ResourceScope = {
      instance_id: String(snapshot.instance_id),
      instance_incarnation: String(snapshot.instance_incarnation),
      boot_epoch: String(snapshot.boot_epoch),
      snapshot_revision: Number(snapshot.snapshot_revision),
    };
    accepted.push({
      auth_index: binding.auth_index,
      runtime_alias: targets[0].resource_id,
      scope,
      targets,
    });
    seen.add(binding.auth_index);
  }
  return accepted;
}

// Call once at page level, never per row. No persistent evidence cache or polling.
export function useResourceProtection(availability: ManagerAvailability, sourceRevision?: unknown) {
  const apiBase = useAuthStore((state) => state.apiBase);
  const managementKey = useAuthStore((state) => state.managementKey);
  const revision = useUsageServiceStore((state) => state.revision);
  const scope = useMemo(
    () => ({
      apiBase,
      managementKey,
      revision,
      sourceRevision,
      managerBase: availability.managerServiceBase,
      ready: !availability.checking && availability.managerServiceAvailable === true,
    }),
    [
      apiBase,
      managementKey,
      revision,
      sourceRevision,
      availability.managerServiceBase,
      availability.checking,
      availability.managerServiceAvailable,
    ]
  );
  const currentScope = useRef<typeof scope | null>(null);
  const generation = useRef(0);
  const controller = useRef<AbortController | null>(null);
  const [result, setResult] = useState<{
    scope: typeof scope;
    status: ResourceProtectionCapability;
    bindings: RuntimeResourceBinding[];
  } | null>(null);

  const load = useCallback(() => {
    if (currentScope.current !== scope) return;
    const request = ++generation.current;
    controller.current?.abort();
    controller.current = null;
    if (!scope.ready || !scope.managerBase || !scope.apiBase || !scope.managementKey) return;
    const pending = new AbortController();
    controller.current = pending;
    return usageServiceApi
      .getResourceCapabilities(scope.managerBase, scope.managementKey, pending.signal, true, true)
      .then((response) => {
        if (generation.current === request && currentScope.current === scope) {
          setResult({ scope, status: readCapability(response), bindings: readBindings(response) });
        }
      })
      .catch(() => {
        // Loading, old managers, auth/transport failures and invalid payloads all
        // remain unknown. Never display response/error text (it can contain secrets).
      });
  }, [scope]);

  const refresh = useCallback(() => {
    if (currentScope.current !== scope) return;
    setResult(null);
    return load();
  }, [scope, load]);

  useLayoutEffect(() => {
    currentScope.current = scope;
    void load();
    return () => {
      currentScope.current = null;
      generation.current += 1;
      controller.current?.abort();
      controller.current = null;
    };
  }, [scope, load]);

  // Invalidate during the context-changing render, not after the next response.
  const status: ResourceProtectionCapability = result?.scope === scope ? result.status : 'unknown';
  const forCredential = (
    authIndex: unknown
  ): { status: ResourceProtectionCapability; binding?: RuntimeResourceBinding } => {
    if (status !== 'identity_only') return { status };
    const binding =
      result?.scope === scope
        ? result.bindings.find((item) => item.auth_index === String(authIndex ?? ''))
        : undefined;
    return binding ? { status: 'identity_only', binding } : { status: 'unbound' };
  };
  return { status, refresh, forCredential };
}
