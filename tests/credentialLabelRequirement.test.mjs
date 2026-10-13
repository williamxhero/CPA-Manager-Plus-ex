import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

// SPEC #7's later explicit naming requirement supersedes the original brief.
describe('credential entry follows the latest SPEC7 naming requirement', () => {
  it.each([
    ['zh-CN', '添加凭证'],
    ['en', 'Add credentials'],
    ['zh-TW', '新增憑證'],
  ])('uses the requested navigation and page title in %s', (locale, expected) => {
    const messages = JSON.parse(readFileSync(new URL(`../apps/web/src/i18n/locales/${locale}.json`, import.meta.url), 'utf8'));
    expect(messages.nav.plan_credentials).toBe(expected);
    expect(messages.plan_credentials.title).toBe(expected);
  });
});
