import { afterEach, describe, expect, it, vi } from 'vitest';
import type { AccountQuotaDisplayWindow } from './accountQuotaDisplayWindows';
import {
  loadCredentialQuotaWindowCache,
  saveCredentialQuotaWindowCache,
} from './credentialQuotaWindowCache';

const STORAGE_KEY = 'cpa.accounts.credential-quota-windows.v1';

const installStorage = () => {
  const values = new Map<string, string>();
  vi.stubGlobal('localStorage', {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => {
      values.set(key, String(value));
    },
    removeItem: (key: string) => {
      values.delete(key);
    },
    clear: () => values.clear(),
    key: (index: number) => Array.from(values.keys())[index] ?? null,
    get length() {
      return values.size;
    },
  } satisfies Storage);
  return values;
};

const windowFixture: AccountQuotaDisplayWindow = {
  key: 'opencode-go:monthly',
  providerWindowId: 'monthly',
  label: 'Monthly',
  kind: 'monthly',
  remainingPercent: 95,
  usedPercent: 5,
  resetLabel: '2026-12-13T00:27:12Z',
  resetAccuracy: 'exact',
  limitWindowSeconds: null,
  resetAtMs: 1796948832000,
  fromMs: null,
  toMs: null,
  source: 'opencode-go',
  windowMode: 'calendar',
  modelScope: { kind: 'all', complete: true },
  observedAtMs: 1794736096000,
  quotaProgressObservedAtMs: 1794736096000,
};

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('credentialQuotaWindowCache', () => {
  it('round-trips host windows per connection scope', () => {
    installStorage();
    const windows = new Map([['go-1', [windowFixture]]]);

    saveCredentialQuotaWindowCache('scope-a', windows);

    const loaded = loadCredentialQuotaWindowCache('scope-a');
    expect(loaded.get('go-1')).toHaveLength(1);
    expect(loaded.get('go-1')?.[0]).toMatchObject(windowFixture);
    expect(loadCredentialQuotaWindowCache('scope-b').size).toBe(0);
  });

  it('drops the scope when the host has no cached windows', () => {
    installStorage();
    saveCredentialQuotaWindowCache('scope-a', new Map([['go-1', [windowFixture]]]));

    saveCredentialQuotaWindowCache('scope-a', new Map());

    expect(loadCredentialQuotaWindowCache('scope-a').size).toBe(0);
  });

  it('drops windows whose reset time already passed', () => {
    installStorage();
    const expired = {
      ...windowFixture,
      key: 'opencode-go:rolling',
      resetAtMs: Date.now() - 60_000,
    };
    saveCredentialQuotaWindowCache('scope-a', new Map([['go-1', [expired, windowFixture]]]));

    const loaded = loadCredentialQuotaWindowCache('scope-a');
    expect(loaded.get('go-1')).toHaveLength(1);
    expect(loaded.get('go-1')?.[0].key).toBe('opencode-go:monthly');
  });

  it('ignores malformed or unreadable entries', () => {
    const values = installStorage();
    values.set(STORAGE_KEY, '{"version":1,"scopes":{"scope-a":{"updatedAtMs":1,"entries":[]}}}');
    expect(loadCredentialQuotaWindowCache('scope-a').size).toBe(0);

    localStorage.setItem(STORAGE_KEY, 'not-json');
    expect(loadCredentialQuotaWindowCache('scope-a').size).toBe(0);

    localStorage.setItem(
      STORAGE_KEY,
      JSON.stringify({
        version: 1,
        scopes: {
          'scope-a': {
            updatedAtMs: 1,
            entries: [
              ['broken', [{ key: 'x' }]],
              ['go-1', [windowFixture]],
            ],
          },
        },
      })
    );
    const loaded = loadCredentialQuotaWindowCache('scope-a');
    expect(loaded.get('go-1')).toHaveLength(1);
    expect(loaded.get('go-1')?.[0]).toMatchObject(windowFixture);
  });
});
