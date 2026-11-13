import { describe, expect, it } from 'vitest';
import {
  resolveQuotaBarRemainingPercent,
  resolveQuotaBarTone,
} from './quotaBarPresentation';

describe('resolveQuotaBarRemainingPercent', () => {
  it('prefers used percent over a conflicting remaining reading', () => {
    expect(resolveQuotaBarRemainingPercent(0, 0)).toBe(100);
    expect(resolveQuotaBarRemainingPercent(0, 30)).toBe(70);
  });

  it('falls back to remaining when used is unknown', () => {
    expect(resolveQuotaBarRemainingPercent(55, null)).toBe(55);
    expect(resolveQuotaBarRemainingPercent(null, null)).toBeNull();
  });
});

describe('resolveQuotaBarTone', () => {
  it('keeps any remaining capacity green', () => {
    expect(resolveQuotaBarTone(1)).toBe('good');
    expect(resolveQuotaBarTone(19)).toBe('good');
    expect(resolveQuotaBarTone(100)).toBe('good');
  });

  it('marks depleted remaining as bad', () => {
    expect(resolveQuotaBarTone(0)).toBe('bad');
  });

  it('does not let lifecycle error override a known remaining reading', () => {
    expect(resolveQuotaBarTone(80, 'bad')).toBe('good');
    expect(resolveQuotaBarTone(0, 'bad', 0)).toBe('good');
    expect(resolveQuotaBarTone(null, 'bad')).toBe('bad');
    expect(resolveQuotaBarTone(null, 'neutral')).toBe('neutral');
  });
});
