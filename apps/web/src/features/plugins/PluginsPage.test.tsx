import { MemoryRouter, useNavigate } from 'react-router-dom';
import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { beforeEach, expect, it, vi } from 'vitest';
import { Button } from '@/components/ui/Button';
import { triggerHeaderRefresh } from '@/hooks/useHeaderRefresh';
import type { PluginListResponse, PluginStoreEntry, PluginStoreResponse } from '@/types';
import { PluginsPage } from './PluginsPage';

const mocks = vi.hoisted(() => ({
  t: (key: string) => key,
  listInstalled: vi.fn(),
  listStore: vi.fn(),
  install: vi.fn(),
  notify: vi.fn(),
  key: 'test-key',
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
      managementKey: mocks.key,
      supportsPlugin: true,
    }),
  useConfigStore: (selector: (state: object) => unknown) => selector({ clearCache: mocks.noop }),
  useNotificationStore: (selector: (state: object) => unknown) =>
    selector({ showNotification: mocks.notify, showConfirmation: mocks.noop }),
}));

vi.mock('@/services/api', () => ({
  pluginsApi: { list: mocks.listInstalled },
  pluginStoreApi: { list: mocks.listStore, install: mocks.install },
}));

vi.mock('@/components/ui/Modal', () => ({
  Modal: ({ open }: { open: boolean }) => (open ? <div role="dialog" /> : null),
}));

beforeEach(() => {
  vi.resetAllMocks();
  mocks.key = 'test-key';
});

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
    const search = (area = 'plugin_store') =>
      renderer.root
        .findAllByType('input')
        .find((input) => input.props['aria-label'] === area + '.search_label')!;
    const status = () =>
      renderer.root
        .findAllByType('button')
        .find((button) => button.props.id === 'plugin-store-filter-updates')!;

    expect(mocks.listStore).not.toHaveBeenCalled();
    await act(async () =>
      search('plugin_management').props.onChange({ target: { value: 'local' } })
    );
    await click('plugin_management.install_plugin');
    expect(mocks.listStore).toHaveBeenCalledTimes(1);
    await act(async () => {
      search().props.onChange({ target: { value: 'catalog' } });
      status().props.onClick();
    });

    await click('plugin_store.manage');
    expect(search('plugin_management').props.value).toBe('local');
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
    await click('plugin_store.manage');
    mocks.key = 'new-connection-key';
    await act(async () => {
      renderer.update(
        <MemoryRouter initialEntries={['/plugins']}>
          <PluginsPage />
        </MemoryRouter>
      );
    });
    expect(search('plugin_management').props.value).toBe('');
  } finally {
    await act(async () => renderer?.unmount());
    vi.unstubAllGlobals();
  }
});

const storeEntry: PluginStoreEntry = {
  storeId: 'official:sample',
  sourceId: 'official',
  sourceName: 'official',
  sourceUrl: '',
  id: 'sample',
  name: 'Sample Plugin',
  description: '',
  author: '',
  version: '1.0.0',
  repository: 'https://github.com/router-for-me/sample',
  installType: 'local',
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
};

function Back() {
  const navigate = useNavigate();
  return (
    <button type="button" onClick={() => navigate(-1)}>
      Back
    </button>
  );
}

it.each([false, true])(
  'dismisses store dialogs on Back and gates settlement (started=%s)',
  async (started) => {
    vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true);
    const store = {
      pluginsEnabled: true,
      pluginsDir: 'plugins',
      plugins: [storeEntry],
      sources: [],
      sourceErrors: [],
    };
    mocks.listInstalled.mockResolvedValue({
      pluginsEnabled: true,
      pluginsDir: 'plugins',
      plugins: [],
    });
    mocks.listStore.mockResolvedValue(store);
    let finishInstall!: (result: { restartRequired: boolean }) => void;
    mocks.install.mockReturnValue(
      new Promise((resolve) => {
        finishInstall = resolve;
      })
    );
    let renderer!: ReactTestRenderer;
    try {
      await act(async () => {
        renderer = create(
          <MemoryRouter initialEntries={['/plugins', '/plugins?tab=store']}>
            <Back />
            <PluginsPage />
          </MemoryRouter>
        );
      });
      const click = (label: string) =>
        act(async () => {
          renderer.root
            .findAllByType(Button)
            .find((b) =>
              Array.isArray(b.props.children)
                ? b.props.children.includes(label)
                : b.props.children === label
            )!
            .props.onClick();
        });
      await click('plugin_store.install');
      expect(renderer.root.findAllByProps({ role: 'dialog' })).toHaveLength(1);
      const confirm = renderer.root.find(
        (node) => typeof node.type === 'function' && node.type.name === 'PluginInstallOptionsModal'
      ).props.onConfirm;
      let mutation: Promise<void> | undefined;
      if (started) {
        await act(async () => {
          mutation = confirm();
        });
        expect(mocks.install).toHaveBeenCalledOnce();
      }

      await act(async () => renderer.root.findByType(Back).findByType('button').props.onClick());
      expect(renderer.root.findAllByProps({ role: 'dialog' })).toHaveLength(0);
      if (started) {
        await act(async () => finishInstall({ restartRequired: false }));
        expect(mocks.notify).toHaveBeenCalledWith('plugin_store.install_success', 'success');
      } else {
        await act(async () => {
          await confirm();
        });
        expect(mocks.install).not.toHaveBeenCalled();
      }
      expect(mocks.listStore).toHaveBeenCalledTimes(1);
      mocks.listStore.mockResolvedValue({
        ...store,
        plugins: [{ ...storeEntry, installed: true, installedVersion: '1.0.0' }],
      });
      await click('plugin_management.install_plugin');
      await act(async () => {
        await mutation;
      });
      expect(mocks.listStore).toHaveBeenCalledTimes(2);
      expect(renderer.root.findAllByProps({ role: 'dialog' })).toHaveLength(0);
      if (!started) {
        await act(async () => {
          await confirm();
        });
        expect(mocks.install).not.toHaveBeenCalled();
      }
    } finally {
      await act(async () => renderer?.unmount());
      vi.unstubAllGlobals();
    }
  }
);
