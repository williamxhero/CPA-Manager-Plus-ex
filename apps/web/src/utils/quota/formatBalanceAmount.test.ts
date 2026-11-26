import { describe, expect, it } from 'vitest';
import {
  classifyBalanceAmount,
  formatBalanceAmount,
  formatBalanceDisplay,
  formatBalanceText,
  parseDisplayedBalanceAmount,
} from './formatBalanceAmount';

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

describe('classifyBalanceAmount', () => {
  it('treats any known positive decimal as positive', () => {
    for (const total of ['1', '0.01', '12.5', '0.000000000000000001', '10.000000000000000000']) {
      expect(classifyBalanceAmount([total])).toBe('positive');
    }
  });

  it('treats zero and negative decimals as non-positive', () => {
    for (const total of ['0', '-0', '0.00', '-0.00', '-1', '-0.5', '-0.000000000000000001']) {
      expect(classifyBalanceAmount([total])).toBe('nonPositive');
    }
  });

  it('never invents an amount for unknown or malformed input', () => {
    expect(classifyBalanceAmount([])).toBe('unknown');
    expect(classifyBalanceAmount([undefined])).toBe('unknown');
    expect(classifyBalanceAmount([undefined, 'unlimited'])).toBe('unknown');
    for (const total of ['', 'unlimited', '1e3', '0x10', '1.2.3', ' 1 ', '+1', 'NaN', 'Infinity']) {
      expect(classifyBalanceAmount([total])).toBe('unknown');
    }
  });

  it('lets any positive source win over exhausted and unknown ones', () => {
    expect(classifyBalanceAmount(['-1', '0.0001'])).toBe('positive');
    expect(classifyBalanceAmount([undefined, '5'])).toBe('positive');
    expect(classifyBalanceAmount(['-3.5', '0'])).toBe('nonPositive');
    expect(classifyBalanceAmount([undefined, '0'])).toBe('nonPositive');
    expect(classifyBalanceAmount(['0', '0.00'])).toBe('nonPositive');
  });
});

describe('parseDisplayedBalanceAmount', () => {
  it('reads the decimal amount out of a rendered balance', () => {
    expect(parseDisplayedBalanceAmount('USD -1.50')).toBe('-1.50');
    expect(parseDisplayedBalanceAmount('CNY 8.123456789012345678')).toBe('8.123456789012345678');
    expect(parseDisplayedBalanceAmount('unlimited')).toBeUndefined();
    expect(parseDisplayedBalanceAmount('USD 1,00')).toBeUndefined();
  });
});
