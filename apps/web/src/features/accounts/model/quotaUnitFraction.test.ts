import { afterEach, describe, expect, it, vi } from 'vitest';
import type { AccountQuotaDisplayWindow } from './accountQuotaDisplayWindows';
import { formatQuotaUnitFraction, getQuotaUnitCount } from './quotaUnitFraction';

const makeWindow = (overrides: Partial<AccountQuotaDisplayWindow> = {}) => ({
  kind: 'five_hour' as const,
  usedPercent: 60,
  windowMode: 'fixed' as const,
  modelScope: { kind: 'all' as const, complete: true },
  ...overrides,
});

afterEach(() => {
  vi.useRealTimers();
});

describe('quotaUnitFraction', () => {
  it.each([
    ['five_hour', 5],
    ['5h', 5],
    ['daily', 1],
    ['weekly', 7],
  ])('maps %s to %s units', (kind, units) => {
    expect(getQuotaUnitCount(kind)).toBe(units);
  });

  it.each([
    [2026, 1, 28, '16.8/28'],
    [2024, 1, 29, '17.4/29'],
    [2026, 3, 30, '18.0/30'],
    [2026, 9, 31, '18.6/31'],
    [2026, 11, 31, '18.6/31'],
  ])('uses the local month length for %s/%s', (year, month, days, expected) => {
    const date = new Date(year, month, 15, 12);
    expect(getQuotaUnitCount('monthly', date)).toBe(days);
    expect(formatQuotaUnitFraction(makeWindow({ kind: 'monthly' }), date)).toBe(expected);
  });

  it('reads the local date on each render rather than caching the month', () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date(2026, 1, 28, 23, 59));
    const window = makeWindow({ kind: 'monthly' });
    expect(formatQuotaUnitFraction(window)).toBe('16.8/28');
    vi.setSystemTime(new Date(2026, 2, 1));
    expect(formatQuotaUnitFraction(window)).toBe('18.6/31');
  });

  it.each([
    ['five_hour', 60, '3.0/5'],
    ['five_hour', 57, '2.9/5'],
    ['weekly', 60, '4.2/7'],
    ['five_hour', 2, '0.1/5'],
    ['weekly', 31, '2.2/7'],
    ['daily', 60, '0.6/1'],
    ['five_hour', 0, '0.0/5'],
    ['weekly', 100, '7.0/7'],
  ] as const)('formats %s used %s with one decimal', (kind, usedPercent, expected) => {
    expect(formatQuotaUnitFraction(makeWindow({ kind, usedPercent }))).toBe(expected);
  });

  it.each([null, undefined, NaN, Infinity, -Infinity])(
    'keeps unknown percent %s unknown',
    (usedPercent) => {
      expect(formatQuotaUnitFraction({ ...makeWindow(), usedPercent })).toBe('-');
    }
  );

  it.each(['billing', 'payg', 'product', 'summary', 'unknown', undefined] as const)(
    'does not convert unsupported kind %s',
    (kind) => {
      expect(getQuotaUnitCount(kind)).toBeNull();
      expect(formatQuotaUnitFraction(makeWindow({ kind }))).toBeNull();
    }
  );

  it.each(['unknown', 'non_window'] as const)(
    'converts a provider-level window with a known kind even when mode is %s',
    (windowMode) => {
      expect(formatQuotaUnitFraction(makeWindow({ windowMode }))).toBe('3.0/5');
    }
  );

  it.each([
    { kind: 'models' as const, models: ['test-model'], complete: true },
    { kind: 'family' as const, key: 'gemini', complete: true },
    { kind: 'all' as const, complete: false },
  ])('does not convert model-scoped quota %s', (modelScope) => {
    expect(formatQuotaUnitFraction(makeWindow({ modelScope }))).toBeNull();
  });

  it('converts the existing account-wide Codex main scope', () => {
    expect(
      formatQuotaUnitFraction(
        makeWindow({
          source: 'codex',
          modelScope: { kind: 'family', key: 'codex_main', complete: true },
        })
      )
    ).toBe('3.0/5');
  });
});
