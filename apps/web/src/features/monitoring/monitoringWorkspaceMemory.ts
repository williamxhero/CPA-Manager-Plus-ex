import { useAuthStore } from '@/stores/useAuthStore';
import type { MonitoringCenterUiState } from './monitoringCenterUiState';

export type MonitoringDrillFilters = {
  authFile: string;
  authIndex: string;
  projectId: string;
  requestType: string;
  minLatencyMs: number | undefined;
  cacheStatus: string;
};

type MonitorSnapshot = {
  ui: MonitoringCenterUiState;
  drill: MonitoringDrillFilters;
};

const entries = new Map<string, MonitorSnapshot>();
let latest: MonitorSnapshot | undefined;
let generation = 0;

useAuthStore.subscribe((state, previous) => {
  if (
    state.apiBase !== previous.apiBase ||
    state.managementKey !== previous.managementKey ||
    (previous.isAuthenticated && !state.isAuthenticated)
  ) {
    entries.clear();
    latest = undefined;
    generation += 1;
  }
});

export const getMonitoringConnectionGeneration = () => generation;

export const readMonitoringWorkspaceMemory = (entry: string, reuseLatest: boolean) =>
  entries.get(entry) ?? (reuseLatest ? latest : undefined);
export const readMonitoringWorkspaceRefreshInterval = () => latest?.ui.autoRefreshMs;

export function writeMonitoringWorkspaceMemory(
  entry: string,
  connectionGeneration: number,
  snapshot: MonitorSnapshot
) {
  if (generation !== connectionGeneration) return;
  if (entries.size >= 40 && !entries.has(entry)) {
    const first = entries.keys().next();
    if (!first.done) entries.delete(first.value);
  }
  entries.set(entry, snapshot);
  latest = snapshot;
}

export function readMonitoringDrill(search: string) {
  const params = new URLSearchParams(search);
  return [
    'from_ms',
    'to_ms',
    'model',
    'api_key_hash',
    'status',
    'provider',
    'auth_file',
    'auth_index',
    'project_id',
    'request_type',
    'search',
    'min_latency_ms',
    'cache_status',
    'header_trace_id',
  ].some((param) => Boolean(params.get(param)?.trim()));
}

export function readMonitoringDrillFilters(search: string): MonitoringDrillFilters {
  const params = new URLSearchParams(search);
  const minLatencyMs = Number(params.get('min_latency_ms'));
  return {
    authFile: params.get('auth_file')?.trim() || '',
    authIndex: params.get('auth_index')?.trim() || '',
    projectId: params.get('project_id')?.trim() || '',
    requestType: params.get('request_type')?.trim() || '',
    minLatencyMs: Number.isFinite(minLatencyMs) && minLatencyMs > 0 ? minLatencyMs : undefined,
    cacheStatus: params.get('cache_status')?.trim() || '',
  };
}
