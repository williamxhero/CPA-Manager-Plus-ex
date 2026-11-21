import { act, create, type ReactTestRenderer, type ReactTestInstance } from 'react-test-renderer';
import { createMemoryRouter, RouterProvider } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { setDemoMode } from '@/features/demo/demoMode';
import { DemoRouteAdapter } from '@/features/demo/DemoRouteAdapter';
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
  vi.stubGlobal('window', {
    localStorage,
    location: { hash: '', origin: 'http://demo.local' },
    addEventListener: () => {},
    removeEventListener: () => {},
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
  await i18n.changeLanguage('zh-CN');
});

afterEach(async () => {
  await act(async () => renderer?.unmount());
  renderer = undefined;
  focusedId = undefined;
  setDemoMode(false);
  vi.unstubAllGlobals();
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

  it('opens one system page from either entry and restores the selected tab during history navigation', async () => {
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

    const sidebar = renderer!.root.findByType('aside');
    const links = sidebar.findAllByType('a');
    expect(links.some((link) => nodeText(link) === '配置面板')).toBe(false);
    expect(links.filter((link) => link.props['aria-current'] === 'page').map(nodeText)).toEqual([
      '仪表盘',
    ]);
    const shortcut = links.find((link) => nodeText(link) === '系统信息')!;
    await click(links.find((link) => nodeText(link) === '仪表盘')!);
    await click(shortcut);
    expect(router.state.location.pathname).toBe(system);
    expect(nodeText(selectedTab()!)).toBe('系统信息');
  });
});
