import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { createInstance } from 'i18next';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it } from 'vitest';
import en from '@/i18n/locales/en.json';
import ru from '@/i18n/locales/ru.json';
import zhCN from '@/i18n/locales/zh-CN.json';
import zhTW from '@/i18n/locales/zh-TW.json';
import { ResourceProtectionIndicator } from './ResourceProtectionIndicator';

const locales = { en, ru, 'zh-CN': zhCN, 'zh-TW': zhTW };

describe('localized automatic protection capability explanation', () => {
  for (const [language, locale] of Object.entries(locales)) {
    it.each(['unknown', 'unavailable'] as const)(
      `${language} describes %s as manager support, not health`,
      async (status) => {
        const i18n = createInstance();
        await i18n.init({ lng: language, resources: { [language]: { translation: locale } } });
        let renderer!: ReactTestRenderer;
        act(() => {
          renderer = create(
            <I18nextProvider i18n={i18n}>
              <ResourceProtectionIndicator status={status} />
            </I18nextProvider>
          );
        });
        const trigger = renderer.root.findByType('button');
        const text = locale.resource_protection;
        expect(trigger.props['aria-label']).toBe(
          `${text.title}: ${text[status]}. ${text[`${status}_description`]}`
        );
        expect(trigger.props['aria-label']).not.toContain('resource_protection.');
        expect(text[`${status}_description`].length).toBeGreaterThan(20);
        act(() => trigger.props.onClick());
        const tooltip = renderer.root.findByProps({ role: 'tooltip' });
        expect(trigger.props['aria-describedby']).toBe(tooltip.props.id);
        act(() => renderer.unmount());
      }
    );
  }
});
