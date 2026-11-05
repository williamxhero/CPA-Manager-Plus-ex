const STORAGE_KEY = 'cpa.listPageSize';

type PageSizeMap = Record<string, number>;

function readMap(): PageSizeMap {
  if (typeof localStorage === 'undefined') return {};
  try {
    const raw = JSON.parse(localStorage.getItem(STORAGE_KEY) || '');
    if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return {};
    const next: PageSizeMap = {};
    for (const [key, value] of Object.entries(raw)) {
      const size = Number(value);
      if (key && Number.isFinite(size)) next[key] = size;
    }
    return next;
  } catch {
    return {};
  }
}

export function readListPageSize(
  id: string,
  allowed: readonly number[],
  fallback: number
): number {
  const size = readMap()[id];
  return typeof size === 'number' && allowed.includes(size) ? size : fallback;
}

export function writeListPageSize(id: string, size: number, allowed: readonly number[]) {
  if (!id || !allowed.includes(size) || typeof localStorage === 'undefined') return;
  try {
    const next = readMap();
    next[id] = size;
    localStorage.setItem(STORAGE_KEY, JSON.stringify(next));
  } catch {
    // List preferences are best-effort only.
  }
}
