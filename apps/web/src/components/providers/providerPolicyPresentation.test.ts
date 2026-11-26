import { expect, it } from 'vitest';
import { providerPolicyMoneyText } from './providerPolicyPresentation';

it('never synthesizes zero totals for unknown or non-finite money', () => {
  expect(providerPolicyMoneyText('USD', 'Infinity', 'unknown')).toBe('unknown');
  expect(providerPolicyMoneyText('usd', '1', 'unknown')).toBe('unknown');
  expect(providerPolicyMoneyText('USD', '0.0500', 'unknown')).toBe('USD 0.05');
  expect(providerPolicyMoneyText('CNY', '8.123456789012345678', 'unknown')).toBe('CNY 8.12');
});
