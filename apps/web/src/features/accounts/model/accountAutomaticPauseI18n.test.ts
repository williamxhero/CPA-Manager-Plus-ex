import { describe, expect, it } from 'vitest';
import en from '@/i18n/locales/en.json';
import ru from '@/i18n/locales/ru.json';
import zhCN from '@/i18n/locales/zh-CN.json';
import zhTW from '@/i18n/locales/zh-TW.json';
import { ACCOUNT_AUTOMATIC_PAUSE_I18N_KEYS } from './accountAutomaticPause';

const locales = { en, ru, 'zh-CN': zhCN, 'zh-TW': zhTW } as const;

const accountsNamespace = (locale: unknown): Record<string, unknown> => {
  const accounts = (locale as { accounts?: unknown } | null)?.accounts;
  if (!accounts || typeof accounts !== 'object') throw new Error('missing accounts namespace');
  return accounts as Record<string, unknown>;
};

describe('automatic pause copy', () => {
  it.each(Object.keys(locales))('has non-empty copy for every rendered key in %s', (locale) => {
    const accounts = accountsNamespace(locales[locale as keyof typeof locales]);
    const missing = ACCOUNT_AUTOMATIC_PAUSE_I18N_KEYS.filter((key) => {
      const local = key.replace(/^accounts\./, '');
      const value = accounts[local];
      return typeof value !== 'string' || value.trim() === '';
    });
    expect(missing).toEqual([]);
  });

  it('does not leave the four locales on the same English words', () => {
    const values = (locale: keyof typeof locales) =>
      ACCOUNT_AUTOMATIC_PAUSE_I18N_KEYS.map(
        (key) => accountsNamespace(locales[locale])[key.replace(/^accounts\./, '')]
      );
    expect(values('zh-CN')).not.toEqual(values('en'));
    expect(values('zh-TW')).not.toEqual(values('zh-CN'));
    expect(values('ru')).not.toEqual(values('en'));
  });
});
