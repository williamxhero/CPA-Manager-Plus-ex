import { afterEach, describe, expect, it, vi } from 'vitest';
import { readListPageSize, writeListPageSize } from './listPageSize';

const store = new Map<string, string>();

describe('list page size', () => {
  afterEach(() => {
    store.clear();
    vi.unstubAllGlobals();
  });

  it('keeps each list page size and ignores values outside that list', () => {
    vi.stubGlobal('localStorage', {
      getItem: (key: string) => store.get(key) ?? null,
      setItem: (key: string, value: string) => {
        store.set(key, value);
      },
      removeItem: (key: string) => {
        store.delete(key);
      },
    });
    writeListPageSize('aiProviders', 50, [10, 20, 50]);
    writeListPageSize('codexInspection.results', 100, [20, 50, 100]);
    writeListPageSize('aiProviders', 999, [10, 20, 50]);
    expect(readListPageSize('aiProviders', [10, 20, 50], 10)).toBe(50);
    expect(readListPageSize('codexInspection.results', [20, 50, 100], 20)).toBe(100);
    expect(readListPageSize('missing', [10, 20, 50], 10)).toBe(10);
  });
});