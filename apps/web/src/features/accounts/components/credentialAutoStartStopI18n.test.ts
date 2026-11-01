import { describe, expect, it } from 'vitest';
import en from '@/i18n/locales/en.json';
import ru from '@/i18n/locales/ru.json';
import zhCN from '@/i18n/locales/zh-CN.json';
import zhTW from '@/i18n/locales/zh-TW.json';

/**
 * SPEC13 requires the credential toolbar auto start/stop switch to be
 * localized in all four supported languages (en / zh-CN / zh-TW / ru).
 */
const AUTO_START_STOP_KEYS = [
  'auto_start_stop',
  'auto_start_stop_aria',
  'auto_start_stop_loading',
  'auto_start_stop_unavailable',
  'auto_start_stop_load_failed',
  'auto_start_stop_locked_hint',
  'auto_start_stop_on_hint',
  'auto_start_stop_off_hint',
  'auto_start_stop_save_failed',
] as const;

const locales: Array<[string, (typeof en)['accounts']]> = [
  ['en', en.accounts],
  ['zh-CN', zhCN.accounts],
  ['zh-TW', zhTW.accounts],
  ['ru', ru.accounts],
];

describe('credential auto start/stop i18n coverage', () => {
  it.each(locales)('%s defines every auto start/stop key', (_locale, accounts) => {
    for (const key of AUTO_START_STOP_KEYS) {
      const value = (accounts as Record<string, unknown>)[key];
      expect(typeof value, `${key} must be a non-empty string`).toBe('string');
      expect((value as string).length).toBeGreaterThan(0);
    }
  });

  it('labels the toolbar switch strictly as 自动启停 in the Chinese locales', () => {
    expect(zhCN.accounts.auto_start_stop).toBe('自动启停');
    expect(zhTW.accounts.auto_start_stop).toBe('自動啟停');
  });

  it('keeps the message interpolation slot in the failure copy', () => {
    for (const [, accounts] of locales) {
      expect(accounts.auto_start_stop_load_failed).toContain('{{message}}');
      expect(accounts.auto_start_stop_save_failed).toContain('{{message}}');
    }
  });
});

it('ticket93 localizes all new independent-policy and common-refresh copy in every locale',()=>{
 const dictionaries=[en,zhCN,zhTW,ru] as Array<Record<string,unknown>>;
 const keys=['saved_on','saved_off','details','refresh','native_disabled','other_unknown','migration','no_guess','own_action','applied_current','applied_unverified','resume','refresh_required','inventory_title','refresh_all','progress','observation_unknown'];
 for(const dictionary of dictionaries) {
  const copy=dictionary.credential_policy as Record<string,string>;
  expect(copy).toBeDefined();
  for(const key of keys)expect(copy[key]?.length,key).toBeGreaterThan(0);
 }
 expect(JSON.stringify((en as Record<string,unknown>).credential_policy)).not.toMatch(/[\u3400-\u9fff]/);
});
