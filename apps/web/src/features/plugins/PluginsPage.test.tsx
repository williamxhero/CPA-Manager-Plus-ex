import { MemoryRouter } from 'react-router-dom';
import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { expect, it, vi } from 'vitest';
import { Button } from '@/components/ui/Button';
import { triggerHeaderRefresh } from '@/hooks/useHeaderRefresh';
import type { PluginListResponse, PluginStoreResponse } from '@/types';
import { PluginsPage } from './PluginsPage';

const mocks = vi.hoisted(() => ({
  t: (key: string) => key,
  listInstalled: vi.fn(),
  listStore: vi.fn(),
  noop: () => {},
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: mocks.t, i18n: { language: 'en' } }),
}));

vi.mock('@/stores', () => ({
  useAuthStore: (selector: (state: object) => unknown) =>
    selector({
      apiBase: 'https://proxy.test',
      connectionStatus: 'connected',
      managementKey: 'test-key',
      supportsPlugin: true,
    }),
  useConfigStore: (selector: (state: object) => unknown) => selector({ clearCache: mocks.noop }),
  useNotificationStore: (selector: (state: object) => unknown) =>
    selector({ showNotification: mocks.noop, showConfirmation: mocks.noop }),
}));

vi.mock('@/services/api', () => ({
  pluginsApi: { list: mocks.listInstalled },
  pluginStoreApi: { list: mocks.listStore },
}));

it('preserves store filters across tab navigation and only refreshes the visible tab', async () => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true);
  mocks.listInstalled.mockResolvedValue({
    pluginsEnabled: true,
    pluginsDir: 'plugins',
    plugins: [],
  } satisfies PluginListResponse);
  mocks.listStore.mockResolvedValue({
    pluginsEnabled: true,
    pluginsDir: 'plugins',
    plugins: [],
    sources: [],
    sourceErrors: [],
  } satisfies PluginStoreResponse);

  let renderer!: ReactTestRenderer;
  try {
    await act(async () => {
      renderer = create(
        <MemoryRouter initialEntries={['/plugins']}>
          <PluginsPage />
        </MemoryRouter>
      );
    });
    const click = (label: string) =>
      act(async () => {
        renderer.root
          .findAllByType(Button)
          .find((button) => button.props.children.includes?.(label))!
          .props.onClick();
      });
    const search = () =>
      renderer.root
        .findAllByType('input')
        .find((input) => input.props['aria-label'] === 'plugin_store.search_label')!;
    const status = () =>
      renderer.root
        .findAllByType('button')
        .find((button) => button.props.id === 'plugin-store-filter-updates')!;

    expect(mocks.listStore).not.toHaveBeenCalled();
    await click('plugin_management.install_plugin');
    expect(mocks.listStore).toHaveBeenCalledTimes(1);
    await act(async () => {
      search().props.onChange({ target: { value: 'catalog' } });
      status().props.onClick();
    });

    await click('plugin_store.manage');
    await act(async () => {
      await triggerHeaderRefresh();
    });
    expect(mocks.listInstalled).toHaveBeenCalledTimes(3);
    expect(mocks.listStore).toHaveBeenCalledTimes(1);

    await click('plugin_management.install_plugin');
    expect(search().props.value).toBe('catalog');
    expect(status().props['aria-selected']).toBe(true);
    expect(mocks.listStore).toHaveBeenCalledTimes(2);
    await act(async () => {
      await triggerHeaderRefresh();
    });
    expect(mocks.listStore).toHaveBeenCalledTimes(3);
    expect(mocks.listInstalled).toHaveBeenCalledTimes(3);
  } finally {
    await act(async () => renderer?.unmount());
    vi.unstubAllGlobals();
  }
});
