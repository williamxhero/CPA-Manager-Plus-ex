import type { TFunction } from 'i18next';
import { describe, expect, it } from 'vitest';
import { describeQuotaFailure } from './quotaFailureMessage';

const makeT = (table: Record<string, string>) =>
  ((key: string) => table[key] ?? key) as unknown as TFunction;

const t = makeT({
  'quota_error.stale_cache': '额度缓存已过期，请重新登录百炼控制台后重试。',
  'quota_error.not_logged_in': '百炼控制台登录已过期，请重新登录后重试。',
  'quota_error.browser_unavailable': '浏览器未连接，请处理后重试。',
  'quota_error.console_error': '百炼控制台返回错误，请稍后重试。',
  'quota_error.invalid_arguments': '额度命令参数无效，请检查插件配置。',
});

describe('describeQuotaFailure', () => {
  it('turns a stale_cache failure into an actionable hint while keeping the code', () => {
    const result = describeQuotaFailure(
      'stale_cache: quota cache exceeds --max-age; refresh it before reading',
      t
    );
    expect(result).toContain('重新登录百炼控制台');
    expect(result).toContain('[stale_cache]');
    expect(result).not.toContain('max-age');
  });

  it('maps an expired console login to an actionable hint', () => {
    const result = describeQuotaFailure('not_logged_in: Bailian console login expired', t);
    expect(result).toContain('登录已过期');
    expect(result).toContain('[not_logged_in]');
  });

  it('maps a missing browser to a single hint for both codes', () => {
    expect(describeQuotaFailure('bsk_unavailable: no browser', t)).toBe(
      '浏览器未连接，请处理后重试。 [bsk_unavailable]'
    );
    expect(describeQuotaFailure('no_browser: none connected', t)).toBe(
      '浏览器未连接，请处理后重试。 [no_browser]'
    );
  });

  it('leaves unknown failures untouched', () => {
    const raw = 'some_unexpected_failure: boom';
    expect(describeQuotaFailure(raw, t)).toBe(raw);
  });

  it('returns the raw text when no translation is available', () => {
    const raw = 'stale_cache: quota cache exceeds --max-age';
    expect(describeQuotaFailure(raw, makeT({}))).toBe(raw);
  });

  it('handles empty input without throwing', () => {
    expect(describeQuotaFailure('', t)).toBe('');
  });
});
