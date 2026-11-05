export interface ProviderAutoUsage {
  enabled: boolean;
  ids: string[];
}

const KEY = 'cpa.providerAutoUsage';
const listeners = new Set<() => void>();

const EMPTY: ProviderAutoUsage = { enabled: false, ids: [] };

export function readProviderAutoUsage(): ProviderAutoUsage {
  if (typeof localStorage === 'undefined') return EMPTY;
  try {
    const parsed = JSON.parse(localStorage.getItem(KEY) || '') as Partial<ProviderAutoUsage>;
    return {
      enabled: parsed.enabled === true,
      ids: Array.isArray(parsed.ids) ? parsed.ids.filter((id) => typeof id === 'string') : [],
    };
  } catch {
    return EMPTY;
  }
}

export function writeProviderAutoUsage(next: ProviderAutoUsage) {
  localStorage.setItem(KEY, JSON.stringify(next));
  listeners.forEach((listener) => listener());
}

export function subscribeProviderAutoUsage(listener: () => void) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}
