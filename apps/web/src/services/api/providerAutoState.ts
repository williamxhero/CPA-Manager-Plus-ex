/**
 * Read-only view of the auto start/stop projection Core publishes.
 *
 * The panel never derives this state from balance text: it renders exactly the
 * projection the Manager forwards. Every failure path resolves to `null` so a
 * missing projection can never surface as an error or an empty flicker.
 */
export type ProviderAutoStateName = 'available' | 'auto_paused' | 'partially_auto_paused';
export type ProviderAutoStateMemberName = 'paused' | 'available';

export interface ProviderAutoStateReason {
  owner: string;
  reason: string;
  observedAtMs?: number;
  resourceId?: string;
}

export interface ProviderAutoStateMember {
  resourceId: string;
  state: ProviderAutoStateMemberName;
  reasons: ProviderAutoStateReason[];
}

export interface ProviderAutoState {
  resourceId: string;
  state: ProviderAutoStateName;
  memberCount?: number;
  pausedCount?: number;
  reasons?: ProviderAutoStateReason[];
  members?: ProviderAutoStateMember[];
}

export interface ProviderAutoStateProjection {
  version: number;
  ready: boolean;
  revision: number;
  providers: ProviderAutoState[];
}

/** Projection entries indexed by the provider group resource id the rows carry. */
export type ProviderAutoStateMap = Readonly<Record<string, ProviderAutoState>>;

const AUTO_STATE_NAMES: readonly ProviderAutoStateName[] = [
  'available',
  'auto_paused',
  'partially_auto_paused',
];

const isRecord = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value);

const readString = (value: unknown): string => (typeof value === 'string' ? value.trim() : '');

const readNumber = (value: unknown): number | undefined =>
  typeof value === 'number' && Number.isFinite(value) ? value : undefined;

const readReason = (value: unknown): ProviderAutoStateReason | null => {
  if (!isRecord(value)) return null;
  const owner = readString(value.owner);
  const reason = readString(value.reason);
  if (!owner && !reason) return null;
  return {
    owner,
    reason,
    observedAtMs: readNumber(value.observedAtMs),
    resourceId: readString(value.resourceId) || undefined,
  };
};

const readReasons = (value: unknown): ProviderAutoStateReason[] =>
  Array.isArray(value)
    ? value.flatMap((item) => {
        const reason = readReason(item);
        return reason ? [reason] : [];
      })
    : [];

const readMember = (value: unknown): ProviderAutoStateMember | null => {
  if (!isRecord(value)) return null;
  const resourceId = readString(value.resourceId);
  if (!resourceId) return null;
  return {
    resourceId,
    state: value.state === 'paused' ? 'paused' : 'available',
    reasons: readReasons(value.reasons),
  };
};

const readProvider = (value: unknown): ProviderAutoState | null => {
  if (!isRecord(value)) return null;
  const resourceId = readString(value.resourceId);
  const state = AUTO_STATE_NAMES.find((name) => name === value.state);
  if (!resourceId || !state) return null;
  return {
    resourceId,
    state,
    memberCount: readNumber(value.memberCount),
    pausedCount: readNumber(value.pausedCount),
    reasons: readReasons(value.reasons),
    members: Array.isArray(value.members)
      ? value.members.flatMap((item) => {
          const member = readMember(item);
          return member ? [member] : [];
        })
      : [],
  };
};

/** Pure projection parser: unknown or malformed entries are dropped, never thrown. */
export function parseProviderAutoState(value: unknown): ProviderAutoStateProjection | null {
  if (!isRecord(value) || !Array.isArray(value.providers)) return null;
  return {
    version: readNumber(value.version) ?? 1,
    ready: value.ready === true,
    revision: readNumber(value.revision) ?? 0,
    providers: value.providers.flatMap((item) => {
      const provider = readProvider(item);
      return provider ? [provider] : [];
    }),
  };
}

export function indexProviderAutoStates(
  projection: ProviderAutoStateProjection | null
): ProviderAutoStateMap {
  if (!projection) return {};
  const index: Record<string, ProviderAutoState> = {};
  for (const provider of projection.providers) index[provider.resourceId] = provider;
  return index;
}

/**
 * Fetches the read-only projection. Returns `null` for every failure (transport,
 * status, body) so callers keep their last known state instead of an error.
 */
export async function getProviderAutoState(
  base: string,
  managementKey: string,
  signal?: AbortSignal
): Promise<ProviderAutoStateProjection | null> {
  try {
    const response = await fetch(`${base.replace(/\/$/, '')}/usage-service/provider-auto-state`, {
      method: 'GET',
      headers: { Authorization: `Bearer ${managementKey}` },
      signal,
      cache: 'no-store',
      redirect: 'error',
    });
    if (!response.ok) return null;
    return parseProviderAutoState(await response.json());
  } catch {
    return null;
  }
}
