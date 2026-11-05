import { describe, expect, it } from 'vitest';
import { formatBalanceAmount, formatBalanceDisplay, formatBalanceText } from './formatBalanceAmount';

describe('formatBalanceAmount', () => {
  it('keeps two decimal places', () => {
    expect(formatBalanceAmount('4.561448')).toBe('4.56');
    expect(formatBalanceAmount('50.33')).toBe('50.33');
    expect(formatBalanceAmount('0.3458')).toBe('0.35');
    expect(formatBalanceAmount('-0.026312')).toBe('-0.03');
    expect(formatBalanceAmount('0')).toBe('0.00');
    expect(formatBalanceAmount('50.3')).toBe('50.30');
    expect(formatBalanceAmount('4.5')).toBe('4.50');
    expect(formatBalanceAmount('-0.004')).toBe('0.00');
    expect(formatBalanceText('USD', '4.561448')).toBe('USD 4.56');
    expect(formatBalanceDisplay('USD 4.5')).toBe('USD 4.50');
    expect(formatBalanceDisplay('CNY 50.3')).toBe('CNY 50.30');
    expect(formatBalanceDisplay('USD 4.561448')).toBe('USD 4.56');
    expect(formatBalanceDisplay('unlimited')).toBe('unlimited');
    expect(formatBalanceAmount('nope')).toBeUndefined();
  });
});
