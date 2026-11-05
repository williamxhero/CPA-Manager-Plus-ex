import { describe, expect, it } from 'vitest';
import { formatBalanceAmount, formatBalanceText } from './formatBalanceAmount';

describe('formatBalanceAmount', () => {
  it('keeps two decimal places', () => {
    expect(formatBalanceAmount('4.561448')).toBe('4.56');
    expect(formatBalanceAmount('50.33')).toBe('50.33');
    expect(formatBalanceAmount('0.3458')).toBe('0.35');
    expect(formatBalanceAmount('-0.026312')).toBe('-0.03');
    expect(formatBalanceAmount('0')).toBe('0.00');
    expect(formatBalanceAmount('-0.004')).toBe('0.00');
    expect(formatBalanceText('USD', '4.561448')).toBe('USD 4.56');
    expect(formatBalanceAmount('nope')).toBeUndefined();
  });
});
