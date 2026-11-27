import { expect, it } from 'vitest';

const locales = import.meta.glob<{ ai_providers?: Record<string, unknown> }>('./locales/*.json', {
  eager: true,
  import: 'default',
});

it.each(Object.entries(locales))('defines non-empty amount-unknown copy in %s', (_, locale) => {
  expect(locale.ai_providers?.money_summary_amount_unknown).toEqual(expect.stringMatching(/\S/));
});
