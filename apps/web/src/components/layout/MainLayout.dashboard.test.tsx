import { act, create, type ReactTestRenderer, type ReactTestInstance } from 'react-test-renderer';
import { createMemoryRouter, RouterProvider } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { useAuthStore, useConfigStore, useNotificationStore } from '@/stores';
import { setDemoMode } from '@/features/demo/demoMode';
import { DemoRouteAdapter } from '@/features/demo/DemoRouteAdapter';
import { notifyPluginResourcesChanged } from '@/features/plugins/pluginResources';
import { configApi } from '@/services/api/config';
import { logsApi } from '@/services/api/logs';
import { normalizePluginList, pluginsApi } from '@/services/api/plugins';
import { usageServiceApi } from '@/services/api/usageService';
import { MonitoringFiltersPanel } from '@/features/monitoring/components/MonitoringFiltersPanel';
import { MONITORING_CENTER_UI_STATE_STORAGE_KEY } from '@/features/monitoring/monitoringCenterUiState';
import { MainLayout } from './MainLayout';

let renderer: ReactTestRenderer | undefined;
let focusedId: string | undefined;

beforeEach(async () => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true);
  const values = new Map<string, string>();
  vi.stubGlobal('localStorage', {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => values.set(key, value),
    removeItem: (key: string) => values.delete(key),
  });
  const events = new EventTarget();
  vi.stubGlobal('window', {
    localStorage,
    location: { hash: '', origin: 'http://demo.local' },
    addEventListener: events.addEventListener.bind(events),
    removeEventListener: events.removeEventListener.bind(events),
    dispatchEvent: events.dispatchEvent.bind(events),
    setInterval,
    clearInterval,
    setTimeout,
    clearTimeout,
  });
  vi.stubGlobal('document', {
    documentElement: { style: { removeProperty: () => {} } },
    get activeElement() {
      return focusedId ? { id: focusedId } : null;
    },
    addEventListener: () => {},
    removeEventListener: () => {},
  });
  setDemoMode(true);
  useAuthStore.setState({
    apiBase: 'http://demo.local',
    connectionStatus: 'connected',
    supportsPlugin: true,
  });
  await i18n.changeLanguage('zh-CN');
});

afterEach(async () => {
  await act(async () => renderer?.unmount());
  useNotificationStore.getState().clearAll();
  renderer = undefined;
  focusedId = undefined;
  setDemoMode(false);
  useAuthStore.setState({
    apiBase: '',
    managementKey: '',
    connectionStatus: 'disconnected',
    isAuthenticated: false,
    supportsPlugin: false,
  });
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

const renderNavigation = async (entry: string, base = '') => {
  const router = createMemoryRouter(
    [
      {
        path: base ? `${base}/*` : '/*',
        element: base ? (
          <DemoRouteAdapter routeBase={base}>
            <MainLayout routeBase={base} demoMode />
          </DemoRouteAdapter>
        ) : (
          <MainLayout />
        ),
      },
    ],
    { initialEntries: [entry] }
  );
  await act(async () => {
    renderer = create(<RouterProvider router={router} />, {
      createNodeMock: ({ props: elementProps }) => {
        const props = elementProps as { role?: string; id?: string; 'aria-label'?: string };
        if (props.role === 'tab') {
          return {
            focus: () => {
              focusedId = props.id;
            },
            getBoundingClientRect: () => ({ left: 0, right: 10 }),
          };
        }
        if (props.role === 'tablist' && props['aria-label'] === '仪表盘') {
          return {
            ownerDocument: document,
            contains: (element: { id: string } | null) =>
              element?.id === focusedId && Boolean(element),
            getBoundingClientRect: () => ({ left: 0, right: 100 }),
          };
        }
        return null;
      },
    });
  });
  return router;
};

const dashboardTabs = () =>
  renderer!.root
    .findByProps({ role: 'tablist', 'aria-label': '仪表盘' })
    .findAll((node) => typeof node.type === 'string' && node.props.role === 'tab');

const nodeText = (node: ReactTestInstance): string =>
  node.children.map((child) => (typeof child === 'string' ? child : nodeText(child))).join('');

const selectedTab = () => dashboardTabs().find((tab) => tab.props['aria-selected'] === true);

const clickEvent = () => ({
  button: 0,
  defaultPrevented: false,
  preventDefault() {
    this.defaultPrevented = true;
  },
});

const click = async (node: ReactTestInstance) => {
  await act(async () => node.props.onClick(clickEvent()));
};

describe('Dashboard navigation', () => {
  it('keeps keyboard focus with the selected tab when returning through demo history', async () => {
    const router = await renderNavigation('/demo/system?source=bookmark', '/demo');
    expect(router.state.location.search).toBe('?source=bookmark');
    expect(nodeText(selectedTab()!)).toBe('系统信息');
    expect(renderer!.root.findByProps({ role: 'tabpanel' }).props['aria-labelledby']).toBe(
      selectedTab()!.props.id
    );

    const systemTab = selectedTab()!;
    await act(async () => systemTab.props.onKeyDown({ key: 'Home', preventDefault() {} }));
    expect(focusedId).toBe(dashboardTabs()[0].props.id);
    expect(router.state.location.pathname).toBe('/demo/system');

    const overview = dashboardTabs()[0];
    await act(async () =>
      overview.props.onKeyDown({
        key: ' ',
        preventDefault() {},
        currentTarget: { click: () => overview.props.onClick(clickEvent()) },
      })
    );
    expect(router.state.location.pathname).toBe('/demo');
    expect(nodeText(selectedTab()!)).toBe('总览');

    await act(async () => router.navigate(-1));
    expect(router.state.location.pathname).toBe('/demo/system');
    expect(router.state.location.search).toBe('?source=bookmark');
    expect(nodeText(selectedTab()!)).toBe('系统信息');
    expect(focusedId).toBe(selectedTab()!.props.id);
  });

  it('opens system information from the dashboard tab and restores it during history navigation', async () => {
    const router = await renderNavigation('/dashboard');
    expect(dashboardTabs().map(nodeText)).toEqual(['总览', '配置面板', '系统信息']);
    expect(nodeText(selectedTab()!)).toBe('总览');

    await click(dashboardTabs()[2]);
    const system = router.state.location.pathname;
    expect(system).toBe('/system');
    expect(nodeText(selectedTab()!)).toBe('系统信息');

    await act(async () => router.navigate(-1));
    expect(router.state.location.pathname).toBe('/dashboard');
    expect(nodeText(selectedTab()!)).toBe('总览');
    await act(async () => router.navigate(1));
    expect(nodeText(selectedTab()!)).toBe('系统信息');

    const navigation = renderer!.root.findByProps({ className: 'workspace-navigation' });
    const links = navigation.findAllByType('a');
    expect(links.some((link) => nodeText(link) === '配置面板')).toBe(false);
    expect(links.filter((link) => link.props['aria-current'] === 'page').map(nodeText)).toEqual([
      '仪表盘',
    ]);
    expect(links.some((link) => nodeText(link) === '系统信息')).toBe(false);
    await click(links.find((link) => nodeText(link) === '仪表盘')!);
    await click(dashboardTabs()[2]);
    expect(router.state.location.pathname).toBe(system);
    expect(nodeText(selectedTab()!)).toBe('系统信息');
  });
});

describe('Access navigation', () => {
  it('opens the legacy credential and OAuth routes in one workspace and remembers the list search', async () => {
    const router = await renderNavigation('/accounts?search=account-probe');
    const tabs = () =>
      renderer!.root
        .findByProps({ role: 'tablist', 'aria-label': '额度接入管理视图' })
        .findAll((node) => typeof node.type === 'string' && node.props.role === 'tab');
    const selected = () => tabs().find((tab) => tab.props['aria-selected'] === true)!;

    const credentialNavigationLinks = () =>
      renderer!.root
        .findByProps({ className: 'workspace-navigation' })
        .findAllByType('a')
        .filter((link) => {
          const path = link.props.href;
          return (
            typeof path === 'string' &&
            /^\/(?:accounts|auth-files|quota|codex-inspection|plan-credentials|oauth)(?:[/?#]|$)/.test(
              path
            )
          );
        });

    expect(credentialNavigationLinks().map((link) => [link.props.href, nodeText(link)])).toEqual([
      ['/accounts', '额度接入'],
    ]);

    expect(tabs().map(nodeText)).toEqual([
      '凭证列表',
      '健康巡检',
      'OAuth 配置',
      '添加凭证',
      'OAuth 登录',
    ]);
    await click(tabs()[3]);
    expect(router.state.location.pathname).toBe('/plan-credentials');
    expect(nodeText(selected())).toBe('添加凭证');
    await click(tabs()[4]);
    expect(router.state.location.pathname).toBe('/oauth');
    expect(nodeText(selected())).toBe('OAuth 登录');
    expect(credentialNavigationLinks()[0].props['aria-current']).toBe('page');
    await click(tabs()[0]);
    expect(router.state.location.pathname).toBe('/accounts');
    expect(router.state.location.search).toContain('search=account-probe');
    await act(async () => router.navigate(-1));
    expect(nodeText(selected())).toBe('OAuth 登录');
  });
});

describe('Plugin navigation', () => {
  const tabs = () =>
    renderer!.root
      .findByProps({ role: 'tablist', 'aria-label': '插件管理' })
      .findAll((node) => typeof node.type === 'string' && node.props.role === 'tab');
  const resourcePaths = () =>
    tabs()
      .map((tab) => tab.props.href as string)
      .filter((href) => href.includes('/plugin-pages/'));
  const catalog = (id: string) =>
    normalizePluginList({
      plugins_enabled: true,
      plugins: [
        {
          id,
          effective_enabled: true,
          menus: [{ path: '/resource', menu: id }],
        },
      ],
    });

  it('opens a plugin resource under the same workspace and restores the installed tab from history', async () => {
    const router = await renderNavigation('/demo/plugins', '/demo');
    const navigation = () =>
      renderer!.root.findByProps({ className: 'workspace-navigation' }).findAllByType('a');
    expect(tabs().slice(0, 2).map(nodeText)).toEqual(['已安装', '插件商店']);
    await vi.waitFor(() =>
      expect(tabs().some((tab) => tab.props.href?.includes('/plugin-pages/'))).toBe(true)
    );
    const resource = tabs().find((tab) => tab.props.href?.includes('/plugin-pages/'))!;
    expect(resource).toBeDefined();
    expect(navigation().some((link) => link.props.href?.includes('/plugin-pages/'))).toBe(false);

    await click(resource);
    expect(router.state.location.pathname).toContain('/demo/plugin-pages/');
    expect(
      navigation()
        .filter((link) => link.props['aria-current'] === 'page')
        .map(nodeText)
    ).toEqual(['插件管理']);
    expect(tabs().find((tab) => tab.props['aria-selected'] === true)?.props.href).toBe(
      resource.props.href
    );
    await act(async () => router.navigate(-1));
    expect(tabs()[0].props['aria-selected']).toBe(true);
  });

  it('keeps the last catalog on background and header failures, then accepts an empty success', async () => {
    const reads = vi.spyOn(pluginsApi, 'list').mockResolvedValue(catalog('first'));
    await renderNavigation('/demo/plugin-store', '/demo');
    await vi.waitFor(() => expect(resourcePaths()).toEqual(['/demo/plugin-pages/first/0']));

    reads.mockRejectedValue(new Error('fixture catalog unavailable'));
    await act(async () => notifyPluginResourcesChanged());
    expect(resourcePaths()).toEqual(['/demo/plugin-pages/first/0']);
    expect(useNotificationStore.getState().notifications).toEqual([]);

    const refresh = renderer!.root
      .findAllByType('button')
      .find((button) => button.props['aria-label'] === i18n.t('header.refresh_all'))!;
    await act(async () => {
      await refresh.props.onClick();
    });
    expect(resourcePaths()).toEqual(['/demo/plugin-pages/first/0']);
    expect(useNotificationStore.getState().notifications).toEqual([
      expect.objectContaining({
        message: '刷新失败: fixture catalog unavailable',
        type: 'error',
      }),
    ]);

    reads.mockResolvedValue(normalizePluginList({ plugins_enabled: true, plugins: [] }));
    await act(async () => notifyPluginResourcesChanged());
    expect(resourcePaths()).toEqual([]);
  });

  it('clears the catalog across credential changes and ignores a late response from the old connection', async () => {
    const reads = vi.spyOn(pluginsApi, 'list').mockResolvedValue(catalog('first'));
    await renderNavigation('/demo/plugin-store', '/demo');
    await vi.waitFor(() => expect(resourcePaths()).toEqual(['/demo/plugin-pages/first/0']));

    let finishOldRead!: (response: ReturnType<typeof catalog>) => void;
    reads.mockImplementationOnce(() => new Promise((resolve) => (finishOldRead = resolve)));
    await act(async () => notifyPluginResourcesChanged());

    reads.mockRejectedValue(new Error('fixture second connection unavailable'));
    await act(async () => {
      useAuthStore.setState({ managementKey: 'second-connection' });
    });
    expect(resourcePaths()).toEqual([]);
    await vi.waitFor(() =>
      expect(reads).toHaveBeenCalledWith({
        apiBase: 'http://demo.local',
        managementKey: 'second-connection',
      })
    );
    await act(async () => finishOldRead(catalog('late-first')));
    expect(resourcePaths()).toEqual([]);

    reads.mockResolvedValue(catalog('second'));
    await act(async () => notifyPluginResourcesChanged());
    expect(resourcePaths()).toEqual(['/demo/plugin-pages/second/0']);

    reads.mockImplementationOnce(() => new Promise((resolve) => (finishOldRead = resolve)));
    await act(async () => notifyPluginResourcesChanged());
    let finishReconnect: ((response: ReturnType<typeof catalog>) => void) | undefined;
    reads.mockImplementation(
      () =>
        new Promise((resolve) => {
          finishReconnect = resolve;
        })
    );
    await act(async () => {
      useAuthStore.setState({ connectionStatus: 'disconnected' });
      useAuthStore.setState({ connectionStatus: 'connected' });
      finishOldRead(catalog('late-second'));
    });
    expect(resourcePaths()).toEqual([]);
    await vi.waitFor(() => expect(finishReconnect).toBeDefined());
    await act(async () => finishReconnect!(catalog('reconnected')));
    expect(resourcePaths()).toEqual(['/demo/plugin-pages/reconnected/0']);
  });
});

describe('Usage and monitoring navigation', () => {
  it('keeps error logs available independently when the Manager collector is disabled', async () => {
    setDemoMode(false);
    useAuthStore.setState({ managementKey: 'navigation-collector-disabled' });
    useConfigStore.setState({ config: null, cache: new Map(), error: null });
    vi.spyOn(usageServiceApi, 'getInfo').mockResolvedValue({ service: 'cpa-manager-plus' });
    vi.spyOn(usageServiceApi, 'getManagerConfig').mockResolvedValue({
      config: {
        cpaConnection: { cpaBaseUrl: 'http://fixture.local', managementKeyConfigured: true },
        collector: {
          enabled: false,
          collectorMode: 'queue',
          queue: 'fixture',
          popSide: 'left',
          batchSize: 10,
          pollIntervalMs: 5000,
          queryLimit: 100,
        },
        externalUsageService: { enabled: false, serviceBase: '' },
      },
    });
    let rejectConfig!: (cause: Error) => void;
    vi.spyOn(configApi, 'getConfig').mockImplementation(
      () =>
        new Promise((_, reject) => {
          rejectConfig = reject;
        })
    );
    const errorReads = vi.spyOn(logsApi, 'fetchErrorLogs').mockResolvedValue({ files: [] });
    const fileReads = vi.spyOn(logsApi, 'fetchLogs');
    await renderNavigation('/logs?tab=errors');
    const links = () =>
      renderer!.root.findByProps({ className: 'workspace-navigation' }).findAllByType('a');
    const tabs = () =>
      renderer!.root
        .findByProps({ role: 'tablist', 'aria-label': '请求监控' })
        .findAll((node) => typeof node.type === 'string' && node.props.role === 'tab');
    expect(links().find((link) => nodeText(link) === '用量分析')?.props.href).toBe(
      '/usage-maintenance'
    );
    expect(links().find((link) => nodeText(link) === '请求监控')?.props.href).toBe('/logs');
    expect(tabs().map((tab) => tab.props.href)).toEqual(['/logs', '/logs?tab=errors']);
    expect(errorReads).not.toHaveBeenCalled();
    await act(async () => rejectConfig(new Error('fixture config failure')));
    expect(errorReads).toHaveBeenCalled();
    expect(fileReads).not.toHaveBeenCalled();
  });

  it('keeps archive and transfer deep links alongside the six usage analysis tabs', async () => {
    const router = await renderNavigation('/demo/usage-maintenance?tab=transfer', '/demo');
    const tabs = () =>
      renderer!.root
        .findByProps({ role: 'tablist', 'aria-label': '用量分析' })
        .findAll((node) => typeof node.type === 'string' && node.props.role === 'tab');
    expect(tabs()).toHaveLength(8);
    expect(tabs()[7].props['aria-selected']).toBe(true);
    expect(tabs()[6].props.href).toBe('/demo/usage-maintenance');
    await click(tabs()[1]);
    expect(router.state.location.pathname).toBe('/demo/usage-analytics');
    expect(tabs()[1].props['aria-selected']).toBe(true);
    await click(tabs()[2]);
    expect(tabs()[2].props['aria-selected']).toBe(true);
    await act(async () => router.navigate(-1));
    expect(tabs()[1].props['aria-selected']).toBe(true);
    await act(async () => router.navigate(-1));
    expect(tabs()[7].props['aria-selected']).toBe(true);
  });

  it('restores a live monitor search across logs, and a new drill replaces it', async () => {
    const router = await renderNavigation('/demo/monitoring?search=first-drill', '/demo');
    const tabs = () =>
      renderer!.root
        .findByProps({ role: 'tablist', 'aria-label': '请求监控' })
        .findAll((node) => typeof node.type === 'string' && node.props.role === 'tab');
    const search = () =>
      renderer!.root
        .findByType('main')
        .findAllByType('input')
        .find((node) => typeof node.props.value === 'string' && node.props.value.includes('drill'));
    expect(tabs()[2].props['aria-selected']).toBe(true);
    expect(search()?.props.value).toBe('first-drill');
    await act(async () => search()?.props.onChange({ target: { value: 'edited-drill' } }));
    await click(tabs()[4]);
    await act(async () => router.navigate(-1));
    expect(router.state.location.search).toBe('?search=first-drill');
    expect(search()?.props.value).toBe('edited-drill');
    await click(tabs()[4]);
    await click(tabs()[2]);
    expect(search()?.props.value).toBe('edited-drill');
    await act(async () => router.navigate('/demo/monitoring?search=new-drill'));
    expect(search()?.props.value).toBe('new-drill');
    await act(async () => router.navigate(-1));
    expect(search()?.props.value).toBe('edited-drill');
    await click(tabs()[4]);
    await act(async () => {
      useAuthStore.setState({ apiBase: 'http://other.demo.local', managementKey: 'other-fixture' });
      useAuthStore.setState({ apiBase: 'http://demo.local', managementKey: '' });
    });
    await click(tabs()[2]);
    expect(search()).toBeUndefined();
  });

  it('keeps monitoring preferences per connection and does not migrate legacy filters', async () => {
    useAuthStore.setState({ managementKey: 'first-fixture', isAuthenticated: true });
    localStorage.setItem(
      MONITORING_CENTER_UI_STATE_STORAGE_KEY,
      JSON.stringify({ activeDataTab: 'apiKeys', autoRefreshMs: '60000', searchInput: 'private' })
    );
    const router = await renderNavigation('/monitoring');
    const filters = () => renderer!.root.findByType(MonitoringFiltersPanel).props;
    const selectedTab = () =>
      renderer!.root
        .findByType('main')
        .findAllByType('a')
        .find((node) => node.props.role === 'tab' && node.props['aria-selected'])?.props.id;

    expect(selectedTab()).toBe('monitoring-workspace-apiKeys');
    expect(filters().autoRefreshMs).toBe('60000');
    expect(filters().searchInput).toBe('');
    await act(async () => filters().onAutoRefreshChange('30000'));
    await act(async () => router.navigate('/monitoring?search=fresh-drill'));
    expect(selectedTab()).toBe('monitoring-workspace-realtime');
    expect(filters().searchInput).toBe('fresh-drill');
    expect(filters().autoRefreshMs).toBe('30000');

    await act(async () => router.navigate('/logs'));
    await act(async () => {
      useAuthStore.setState({ managementKey: 'second-fixture' });
    });
    await act(async () => router.navigate('/monitoring'));
    expect(filters().autoRefreshMs).toBe('5000');
    expect(filters().searchInput).toBe('');

    await act(async () => {
      useAuthStore.setState({ managementKey: 'first-fixture' });
    });
    expect(filters().autoRefreshMs).toBe('30000');
    expect(selectedTab()).toBe('monitoring-workspace-apiKeys');
  });

  it('keeps log tabs in the monitoring workspace and restores the selected log from history', async () => {
    const router = await renderNavigation('/demo/logs?tab=errors', '/demo');
    const tabs = () =>
      renderer!.root
        .findByProps({ role: 'tablist', 'aria-label': '请求监控' })
        .findAll((node) => typeof node.type === 'string' && node.props.role === 'tab');
    expect(tabs().map((tab) => tab.props.href)).toEqual([
      '/demo/monitoring?tab=accounts',
      '/demo/monitoring?tab=apiKeys',
      '/demo/monitoring?tab=realtime',
      '/demo/logs',
      '/demo/logs?tab=errors',
    ]);
    expect(tabs()[4].props['aria-selected']).toBe(true);
    await click(tabs()[3]);
    expect(router.state.location.search).toBe('');
    expect(tabs()[3].props['aria-selected']).toBe(true);
    await act(async () => router.navigate(-1));
    expect(tabs()[4].props['aria-selected']).toBe(true);
  });

  it('exposes six fixed destinations and highlights monitoring on an error-log bookmark', async () => {
    await renderNavigation('/demo/logs?tab=errors', '/demo');
    const links = renderer!.root
      .findByProps({ className: 'workspace-navigation' })
      .findAllByType('a');
    expect(links.map(nodeText)).toEqual([
      '请求监控',
      '付费接入',
      '额度接入',
      '仪表盘',
      '用量分析',
      '插件管理',
    ]);
    expect(links.filter((link) => link.props['aria-current'] === 'page').map(nodeText)).toEqual([
      '请求监控',
    ]);
  });
});
