import {
  loadRuntimeControls,
  readRuntimeControls,
  safeAutoIds,
  subscribeRuntimeControls,
  writeRuntimeControls,
} from '@/utils/quota/runtimeControls';

export interface ProviderAutoUsage {
  enabled: boolean;
  ids: string[];
}

export const SERVER_PROVIDER_AUTO_USAGE: ProviderAutoUsage = { enabled: false, ids: [] };

const RESOURCE_ID = /^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/;

export function providerAutoId(row: {
  kind: string;
  originalIndex: number;
  raw: { resourceId?: string };
}): string {
  const resourceId = row.raw.resourceId;
  if (typeof resourceId === 'string' && RESOURCE_ID.test(resourceId)) return `${row.kind}:${resourceId}`;
  return `${row.kind}:${row.originalIndex}`;
}

let autoSnapshot: ProviderAutoUsage = SERVER_PROVIDER_AUTO_USAGE;

function sameIds(left: string[], right: string[]) {
  return left.length === right.length && left.every((id, index) => id === right[index]);
}

export function readProviderAutoUsage(): ProviderAutoUsage {
  const controls = readRuntimeControls();
  if (
    autoSnapshot.enabled !== controls.autoStartStopEnabled ||
    !sameIds(autoSnapshot.ids, controls.autoStartStopIds)
  ) {
    autoSnapshot = { enabled: controls.autoStartStopEnabled, ids: controls.autoStartStopIds };
  }
  return autoSnapshot;
}

export function writeProviderAutoUsage(next: ProviderAutoUsage) {
  void (async () => {
    await loadRuntimeControls();
    await writeRuntimeControls({
      ...readRuntimeControls(),
      autoStartStopEnabled: next.enabled,
      autoStartStopIds: safeAutoIds(next.ids),
    });
  })();
}

export function subscribeProviderAutoUsage(listener: () => void) {
  return subscribeRuntimeControls(listener);
}
