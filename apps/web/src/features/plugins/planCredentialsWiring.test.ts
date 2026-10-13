import { describe, expect, it } from 'vitest';
import en from '@/i18n/locales/en.json';
import ru from '@/i18n/locales/ru.json';
import zhCN from '@/i18n/locales/zh-CN.json';
import zhTW from '@/i18n/locales/zh-TW.json';
import layoutSource from '@/components/layout/MainLayout.tsx?raw';
import routesSource from '@/router/MainRoutes.tsx?raw';
import resourcePageSource from './PluginResourcePage.tsx?raw';

const planCredentialKeys = [
  'title',
  'loading',
  'empty',
  'required_field',
  'invalid_base_url',
  'success',
  'failed',
  'duplicate',
];

const locales = { en, ru, zhCN, zhTW };

const getPlanCredentials = (locale: unknown) =>
  (locale as { plan_credentials?: Record<string, string> }).plan_credentials;

const getPlanNav = (locale: unknown) => (locale as { nav: Record<string, string> }).nav;

// Source-level wiring follows the existing usage analytics wiring tests and
// stays independent of the credential page's implementation.
describe('plan credentials app wiring', () => {
  it('registers one fixed route without a plugin or feature gate', () => {
    expect(routesSource).toContain(
      "import { PlanCredentialsPage } from '@/pages/PlanCredentialsPage';"
    );
    expect(routesSource.match(/path: '\/plan-credentials'/g)).toHaveLength(1);
    expect(routesSource).toContain(
      "{ path: '/plan-credentials', element: <PlanCredentialsPage /> }"
    );
  });

  it('places one unconditional nav entry directly between accounts and OAuth', () => {
    const sectionsSource = layoutSource.slice(
      layoutSource.indexOf('const navSections: NavItem[][] = ['),
      layoutSource.indexOf('const navItems = navSections.flat();')
    );
    expect(layoutSource.match(/path: '\/plan-credentials'/g)).toHaveLength(1);
    expect(sectionsSource).toMatch(
      /path: '\/accounts',[\s\S]*?icon: sidebarIcons\.authFiles,\s*\},\s*\{\s*path: '\/plan-credentials',\s*label: t\('nav\.plan_credentials'\),\s*shortLabel: navShortLabel\('nav\.plan_credentials', t\('nav\.plan_credentials'\)\),\s*icon: sidebarIcons\.\w+,\s*\},\s*\{\s*path: '\/oauth',/
    );
  });

  it('uses the nav-only collector in the sidebar but retains direct plugin resources', () => {
    expect(layoutSource).toContain('collectPluginResourceNavEntries(plugins.plugins)');
    expect(layoutSource).not.toContain('collectPluginResourceEntries(plugins.plugins)');
    expect(resourcePageSource).toContain('collectPluginResourceEntries(data?.plugins ?? [])');
    expect(resourcePageSource).not.toContain('collectPluginResourceNavEntries');
    expect(routesSource).toContain("path: '/plugin-pages/:pluginId/:menuIndex'");
    expect(routesSource).toContain('<PluginResourcePage />');
  });

  it('snapshots the Simplified Chinese title and navigation labels', () => {
    expect({
      title: getPlanCredentials(zhCN)?.title,
      label: getPlanNav(zhCN).plan_credentials,
      shortLabel: getPlanNav(zhCN).plan_credentials_short,
    }).toMatchInlineSnapshot(`
      {
        "label": "添加凭证",
        "shortLabel": "凭证",
        "title": "添加凭证",
      }
    `);
    expect(getPlanCredentials(zhCN)?.duplicate).toBe('凭证已存在');
    expect(getPlanCredentials(zhCN)?.success).toBe('凭证添加成功');
  });

  it.each(Object.entries(locales))(
    'keeps all plan credential keys in locale parity: %s',
    (name, locale) => {
      const messages = getPlanCredentials(locale);
      expect(messages, name).toBeDefined();
      expect(Object.keys(messages ?? {}).sort(), name).toEqual([...planCredentialKeys].sort());
      for (const key of planCredentialKeys) {
        expect(messages?.[key], `${name}:plan_credentials.${key}`).toBeTypeOf('string');
        expect(messages?.[key].trim().length, `${name}:plan_credentials.${key}`).toBeGreaterThan(0);
      }
      expect(messages?.required_field, name).toContain('{{field}}');
      expect(getPlanNav(locale).plan_credentials, name).toBe(messages?.title);
      expect(getPlanNav(locale).plan_credentials_short.trim().length, name).toBeGreaterThan(0);
    }
  );

  it('uses the requested English and Traditional Chinese titles', () => {
    expect(getPlanCredentials(en)?.title).toBe('Add credentials');
    expect(getPlanCredentials(zhTW)?.title).toBe('新增憑證');
  });

  it.each(Object.entries(locales))(
    'labels the opencode-go provider tab as "OpenCode Go" in every locale: %s',
    (name, locale) => {
      const authFiles = (locale as { auth_files: Record<string, string> }).auth_files;
      expect(authFiles['filter_opencode-go'], name).toBe('OpenCode Go');
    }
  );
});
