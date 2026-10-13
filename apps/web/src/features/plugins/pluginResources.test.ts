import { describe, expect, it } from 'vitest';
import type { PluginListEntry, PluginStoreEntry } from '@/types';
import {
  collectPluginResourceEntries,
  collectPluginResourceNavEntries,
  getPluginConfirmToken,
  getPluginRepositorySlug,
  isDefaultPluginStoreSource,
  isOfficialPlugin,
  isOfficialRepository,
  isPluginManagementNavVisible,
  isPluginResourceNavVisible,
} from './pluginResources';

const createPlugin = (patch: Partial<PluginListEntry> = {}): PluginListEntry => ({
  id: 'demo-plugin',
  path: 'plugins/demo-plugin.so',
  configured: true,
  registered: true,
  enabled: true,
  effectiveEnabled: true,
  supportsOAuth: false,
  logo: '',
  configFields: [],
  menus: [
    {
      path: '/v0/resource/plugins/demo-plugin/page',
      menu: 'Demo Plugin',
      description: 'Demo plugin page',
    },
  ],
  metadata: {
    name: 'Demo Plugin',
    version: '1.0.0',
    author: 'router-for-me',
    githubRepository: 'router-for-me/demo-plugin',
    logo: '',
    configFields: [],
  },
  ...patch,
});

const createStoreEntry = (patch: Partial<PluginStoreEntry> = {}): PluginStoreEntry => ({
  storeId: 'official/demo-plugin',
  sourceId: 'official',
  sourceName: 'official',
  sourceUrl: 'https://example.test/registry.json',
  id: 'demo-plugin',
  name: 'Demo Plugin',
  description: '',
  author: '',
  version: '1.0.0',
  repository: 'router-for-me/demo-plugin',
  installType: '',
  authRequired: false,
  authConfigured: false,
  platforms: [],
  logo: '',
  homepage: '',
  license: '',
  tags: [],
  installed: false,
  installedVersion: '',
  path: '',
  configured: false,
  registered: false,
  enabled: false,
  effectiveEnabled: false,
  updateAvailable: false,
  ...patch,
});

describe('plugin resource helpers', () => {
  it('hides the plugin management nav when plugins are explicitly disabled', () => {
    expect(
      isPluginManagementNavVisible({ supportsPlugin: true, pluginsEnabled: true })
    ).toBe(true);
    expect(
      isPluginManagementNavVisible({ supportsPlugin: true, pluginsEnabled: false })
    ).toBe(false);
    expect(isPluginManagementNavVisible({ supportsPlugin: true })).toBe(true);
    expect(
      isPluginManagementNavVisible({ supportsPlugin: false, pluginsEnabled: true })
    ).toBe(false);
  });

  it('shows plugin resource nav only when supported and globally enabled', () => {
    expect(
      isPluginResourceNavVisible({ supportsPlugin: true, pluginsEnabled: true })
    ).toBe(true);
    expect(
      isPluginResourceNavVisible({ supportsPlugin: true, pluginsEnabled: false })
    ).toBe(false);
    expect(isPluginResourceNavVisible({ supportsPlugin: true })).toBe(false);
    expect(
      isPluginResourceNavVisible({ supportsPlugin: false, pluginsEnabled: true })
    ).toBe(false);
  });

  it('collects only effective plugin menus with resource paths', () => {
    const entries = collectPluginResourceEntries([
      createPlugin(),
      createPlugin({
        id: 'inactive-plugin',
        effectiveEnabled: false,
        menus: [
          {
            path: '/v0/resource/plugins/inactive/page',
            menu: 'Inactive',
            description: '',
          },
        ],
      }),
      createPlugin({
        id: 'empty-menu-plugin',
        menus: [
          {
            path: '',
            menu: 'Empty Menu',
            description: '',
          },
        ],
      }),
    ]);

    expect(entries).toHaveLength(1);
    expect(entries[0]).toMatchObject({
      pluginID: 'demo-plugin',
      pluginTitle: 'Demo Plugin',
      label: 'Demo Plugin',
      route: '/plugin-pages/demo-plugin/0',
    });
  });

  it('suppresses both plan plugins only in navigation and keeps every direct resource entry', () => {
    const plugins = [
      createPlugin({ id: 'qwen-cliproxyapi' }),
      createPlugin({
        id: 'opencode-go-cliproxyapi',
        menus: [
          { path: '/opencode/quota', menu: 'OpenCode Go Quota', description: '' },
          { path: '/opencode/details', menu: 'Details', description: '' },
        ],
      }),
      createPlugin(),
    ];
    const directEntries = collectPluginResourceEntries(plugins);

    expect(collectPluginResourceNavEntries(plugins)).toEqual([directEntries[3]]);
    expect(
      directEntries.map(({ pluginID, menuIndex, route, menu }) => ({
        pluginID,
        menuIndex,
        route,
        path: menu.path,
      }))
    ).toEqual([
      {
        pluginID: 'qwen-cliproxyapi',
        menuIndex: 0,
        route: '/plugin-pages/qwen-cliproxyapi/0',
        path: '/v0/resource/plugins/demo-plugin/page',
      },
      {
        pluginID: 'opencode-go-cliproxyapi',
        menuIndex: 0,
        route: '/plugin-pages/opencode-go-cliproxyapi/0',
        path: '/opencode/quota',
      },
      {
        pluginID: 'opencode-go-cliproxyapi',
        menuIndex: 1,
        route: '/plugin-pages/opencode-go-cliproxyapi/1',
        path: '/opencode/details',
      },
      {
        pluginID: 'demo-plugin',
        menuIndex: 0,
        route: '/plugin-pages/demo-plugin/0',
        path: '/v0/resource/plugins/demo-plugin/page',
      },
    ]);
  });

  it.each([
    'Qwen-cliproxyapi',
    'qwen-cliproxyapi-extra',
    ' qwen-cliproxyapi',
    'opencode-go-cliproxyapi-extra',
    'OpenCode-go-cliproxyapi',
    'other-plugin',
  ])('keeps non-exact plugin IDs in resource navigation: %s', (id) => {
    const plugins = [createPlugin({ id })];
    expect(collectPluginResourceNavEntries(plugins)).toEqual(
      collectPluginResourceEntries(plugins)
    );
    expect(collectPluginResourceNavEntries(plugins)).toHaveLength(1);
  });

  it('keeps resource eligibility rules in the nav-only collector', () => {
    expect(
      collectPluginResourceNavEntries([
        createPlugin({ effectiveEnabled: false }),
        createPlugin({ menus: [{ path: ' ', menu: 'Empty', description: '' }] }),
      ])
    ).toEqual([]);
    expect(collectPluginResourceNavEntries([])).toEqual([]);
  });

  it('normalizes repository slugs for plugin install confirmation', () => {
    expect(getPluginRepositorySlug('router-for-me/demo.git')).toBe('router-for-me/demo');
    expect(getPluginRepositorySlug('https://github.com/owner/repo.git')).toBe('owner/repo');
    expect(getPluginConfirmToken(createStoreEntry({ repository: '' }))).toBe('demo-plugin');
  });

  it('detects only official router-for-me GitHub repositories as first-party', () => {
    expect(isOfficialRepository('router-for-me/demo')).toBe(true);
    expect(isOfficialRepository('https://github.com/router-for-me/demo')).toBe(true);
    expect(isOfficialRepository('https://github.com.evil.test/router-for-me/demo')).toBe(false);
    expect(isOfficialRepository('other/demo')).toBe(false);
    expect(isOfficialPlugin(createStoreEntry())).toBe(true);
    expect(isOfficialPlugin(createStoreEntry({ sourceId: 'third-party' }))).toBe(false);
    expect(isOfficialPlugin(createStoreEntry({ repository: 'other/demo' }))).toBe(false);
  });

  it('recognizes the default plugin store source', () => {
    expect(isDefaultPluginStoreSource(createStoreEntry({ sourceId: 'official' }))).toBe(true);
    expect(isDefaultPluginStoreSource(createStoreEntry({ sourceId: '', sourceName: 'Official' }))).toBe(
      true
    );
    expect(isDefaultPluginStoreSource(createStoreEntry({ sourceId: 'custom', sourceName: 'Custom' }))).toBe(
      false
    );
  });
});
