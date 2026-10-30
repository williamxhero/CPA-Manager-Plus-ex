import { useCallback, useLayoutEffect, useMemo, useRef, useState } from 'react';
import type { PanelFeatureAvailability } from '@/hooks/usePanelFeatureAvailability';
import { usageServiceApi } from '@/services/api/usageService';
import { useAuthStore } from '@/stores';
import { useUsageServiceStore } from '@/stores/useUsageServiceStore';

export type ResourceProtectionCapability = 'unknown' | 'unavailable';
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
  // Only this manager's unconfigured integration is recognized. Even a global
  // "available" flag cannot establish a positive, bound per-resource contract.
  return 'unavailable';
}

// Call once at page level, never per row. No persistent evidence cache or polling.
export function useResourceProtection(availability: ManagerAvailability) {
  const apiBase = useAuthStore((state) => state.apiBase);
  const managementKey = useAuthStore((state) => state.managementKey);
  const revision = useUsageServiceStore((state) => state.revision);
  const scope = useMemo(
    () => ({
      apiBase,
      managementKey,
      revision,
      managerBase: availability.managerServiceBase,
      ready: !availability.checking && availability.managerServiceAvailable === true,
    }),
    [
      apiBase,
      managementKey,
      revision,
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
      .getResourceCapabilities(scope.managerBase, scope.managementKey, pending.signal)
      .then((response) => {
        if (generation.current === request && currentScope.current === scope) {
          setResult({ scope, status: readCapability(response) });
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
  return { status, refresh };
}
