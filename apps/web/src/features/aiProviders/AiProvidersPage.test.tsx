import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { CoolingPolicy, GeminiKeyConfig, OpenAIProviderConfig, ProviderKeyConfig } from '@/types';
import type { ProviderAutoState } from '@/services/api/providerAutoState';

const mocks = vi.hoisted(() => ({
  config: {} as {
    geminiApiKeys: GeminiKeyConfig[];
    interactionsApiKeys: GeminiKeyConfig[];
    codexApiKeys: ProviderKeyConfig[];
    xaiApiKeys: ProviderKeyConfig[];
    metaApiKeys: ProviderKeyConfig[];
    claudeApiKeys: ProviderKeyConfig[];
    vertexApiKeys: ProviderKeyConfig[];
    openaiCompatibility: OpenAIProviderConfig[];
  },
  getConfig: vi.fn(),
  fetchConfig: vi.fn(),
  updateConfigValue: vi.fn(),
  clearCache: vi.fn(),
  updateGeminiKey: vi.fn(),
  updateVertexConfig: vi.fn(),
  updateMetaConfig: vi.fn(),
  getVertexConfigs: vi.fn(),
  getMetaConfigs: vi.fn(),
  getOpenAIProviders: vi.fn(),
  showNotification: vi.fn(),
  showConfirmation: vi.fn(),
  loadProviderKeyAliases: vi.fn(async () => undefined),
  providerKeyAliases: [] as Array<{ provider: string; apiKeyHash: string; alias: string }>,
  cacheValid: true,
  transitionLayer: null as { status: string } | null,
  auth: { apiBase: 'http://core.example', managementKey: 'synthetic-key', connectionStatus: 'connected' },
  headerRefresh: null as (() => Promise<void>) | null,
  featureAvailability: {
    checking: false,
    managerServiceAvailable: false,
    managerServiceBase: '',
  },
  getProviderAutoState: vi.fn(),
  getProviderPolicyMigration: vi.fn(async () => null),
  providerDetailAutoStates: [] as Array<ProviderAutoState | null | undefined>,
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

vi.mock('@/hooks/usePanelFeatureAvailability', () => ({
  usePanelFeatureAvailability: () => mocks.featureAvailability,
}));

vi.mock('@/services/api/providerAutoState', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/services/api/providerAutoState')>();
  return { ...actual, getProviderAutoState: mocks.getProviderAutoState };
});

vi.mock('@/services/api/providerPolicy', () => ({
  getProviderPolicyMigration: mocks.getProviderPolicyMigration,
}));

vi.mock('@/features/resourceProtection/resourceProtection', () => ({
  useResourceProtection: () => ({
    status: 'unknown',
    forProvider: () => ({ status: 'unknown' }),
    refresh: async () => undefined,
  }),
}));

vi.mock('@/hooks/useHeaderRefresh', () => ({
  useHeaderRefresh: (refresh: () => Promise<void>, enabled: boolean) => {
    mocks.headerRefresh = enabled ? refresh : null;
  },
}));
vi.mock('@/features/monitoring/hooks/useUsageData', () => ({
  useUsageData: () => ({
    providerKeyAliases: mocks.providerKeyAliases,
    loadProviderKeyAliases: mocks.loadProviderKeyAliases,
  }),
}));
vi.mock('@/components/common/PageTransitionLayer', () => ({
  usePageTransitionLayer: () => mocks.transitionLayer,
}));

vi.mock('@/stores', () => ({
  useAuthStore: Object.assign(
    (selector: (state: typeof mocks.auth) => unknown) => selector(mocks.auth),
    { getState: () => mocks.auth }
  ),
  useThemeStore: (selector: (state: { resolvedTheme: string }) => unknown) =>
    selector({ resolvedTheme: 'light' }),
  useConfigStore: (selector: (state: Record<string, unknown>) => unknown) =>
    selector({
      config: mocks.config,
      fetchConfig: mocks.fetchConfig,
      updateConfigValue: mocks.updateConfigValue,
      clearCache: mocks.clearCache,
      isCacheValid: () => mocks.cacheValid,
    }),
  useNotificationStore: () => ({
    showNotification: mocks.showNotification,
    showConfirmation: mocks.showConfirmation,
  }),
}));

vi.mock('@/services/api', () => ({
  configApi: { getConfig: mocks.getConfig },
  providersApi: {
    getVertexConfigs: mocks.getVertexConfigs,
    getMetaConfigs: mocks.getMetaConfigs,
    getOpenAIProviders: mocks.getOpenAIProviders,
    updateGeminiKey: mocks.updateGeminiKey,
    updateVertexConfig: mocks.updateVertexConfig,
    updateMetaConfig: mocks.updateMetaConfig,
  },
}));

vi.mock('@/components/providers', async () => {
  const policyFromOverride = (value: boolean | null | undefined): CoolingPolicy =>
    value === true ? 'disabled' : value === false ? 'enabled' : 'inherit';

  return {
    buildProviderRows: ({
      gemini = [],
      vertex = [],
      meta = [],
      openai = [],
    }: {
      gemini?: GeminiKeyConfig[];
      vertex?: ProviderKeyConfig[];
      meta?: ProviderKeyConfig[];
      openai?: OpenAIProviderConfig[];
    }) => [
      ...gemini.map((raw, originalIndex) => ({
        key: `gemini:${originalIndex}`,
        kind: 'gemini' as const,
        originalIndex,
        raw,
        modelNames: [],
        enabled: true,
        label: raw.apiKey,
        sortName: raw.apiKey,
        baseUrl: raw.baseUrl ?? '',
      })),
      ...vertex.map((raw, originalIndex) => ({
        key: `vertex:${originalIndex}`,
        kind: 'vertex' as const,
        originalIndex,
        raw,
        modelNames: [],
        enabled: true,
        label: raw.apiKey,
        sortName: raw.apiKey,
        baseUrl: raw.baseUrl ?? '',
      })),
      ...meta.map((raw, originalIndex) => ({
        key: `meta:${originalIndex}`,
        kind: 'meta' as const,
        originalIndex,
        raw,
        modelNames: [],
        enabled: true,
        label: raw.apiKey,
        sortName: raw.apiKey,
        baseUrl: raw.baseUrl ?? '',
      })),
      ...openai.map((raw, originalIndex) => ({
        key: `openai:${originalIndex}`,
        kind: 'openai' as const,
        originalIndex,
        raw,
        modelNames: [],
        enabled: true,
        label: raw.name,
        sortName: raw.name,
        baseUrl: raw.baseUrl,
      })),
    ],
    filterAndSortProviderRows: (rows: unknown[]) => rows,
    PROVIDER_KIND_LABELS: {
      all: 'All',
      gemini: 'Gemini',
      interactions: 'Interactions',
      codex: 'Codex',
      xai: 'xAI',
      meta: 'Muse (Meta)',
      claude: 'Claude',
      vertex: 'Vertex',
      openai: 'OpenAI',
    },
    ProviderToolbar: ({ onAdd }: { onAdd: (kind: 'openai') => void }) => (
      <button type="button" data-add-provider onClick={() => onAdd('openai')}>
        Add provider
      </button>
    ),
    ProviderTable: ({
      rows,
      onShowDetail,
    }: {
      rows: Array<Record<string, unknown>>;
      onShowDetail: (row: Record<string, unknown>) => void;
    }) => {
      return rows[0] ? (
        <button type="button" data-open-detail onClick={() => onShowDetail(rows[0])}>
          {String(rows[0].label)}
        </button>
      ) : null;
    },
    ProviderDetailDrawer: ({
      row,
      open,
      autoState,
      onToggleDisableCooling,
    }: {
      row: { raw: GeminiKeyConfig | ProviderKeyConfig } | null;
      open: boolean;
      autoState?: ProviderAutoState | null;
      onToggleDisableCooling: (
        row: { raw: GeminiKeyConfig | ProviderKeyConfig },
        policy: CoolingPolicy
      ) => void;
    }) => {
      if (!open || !row) return null;
      mocks.providerDetailAutoStates.push(autoState);
      return (
        <div>
          <span data-current-policy>{policyFromOverride(row.raw.disableCooling)}</span>
          {(['inherit', 'enabled', 'disabled'] as const).map((policy) => (
            <button
              type="button"
              key={policy}
              data-policy={policy}
              onClick={() => onToggleDisableCooling(row, policy)}
            >
              {policy}
            </button>
          ))}
        </div>
      );
    },
    ProviderHealthCheckDrawer: () => null,
    GeminiEditDrawer: () => null,
    CodexEditDrawer: () => null,
    ClaudeEditDrawer: () => null,
    OpenAIEditDrawer: ({ open }: { open: boolean }) => (open ? <div data-openai-editor /> : null),
    VertexEditDrawer: () => null,
    useProviderRecentRequests: () => ({
      usageByProvider: new Map(),
      loadRecentRequests: vi.fn(async () => undefined),
      refreshRecentRequests: vi.fn(async () => undefined),
    }),
  };
});

import { AiProvidersPage } from './AiProvidersPage';
import { apiClient } from '@/services/api/client';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const flush = async () => {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
    await Promise.resolve();
  });
};

const openDetail = async (renderer: ReactTestRenderer) => {
  await act(async () => {
    renderer.root.findByProps({ 'data-open-detail': true }).props.onClick();
  });
};

describe('AiProvidersPage cooling policy mutation', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.cacheValid = true;
    mocks.transitionLayer = null;
    mocks.config = {
      geminiApiKeys: [{ apiKey: 'gemini-key' }],
      interactionsApiKeys: [],
      codexApiKeys: [],
      xaiApiKeys: [],
      metaApiKeys: [],
      claudeApiKeys: [],
      vertexApiKeys: [],
      openaiCompatibility: [],
    };
    mocks.getConfig.mockImplementation(async () => mocks.config);
    mocks.getVertexConfigs.mockImplementation(async () => mocks.config.vertexApiKeys);
    mocks.getMetaConfigs.mockImplementation(async () => mocks.config.metaApiKeys);
    mocks.getOpenAIProviders.mockResolvedValue([]);
    mocks.updateGeminiKey.mockImplementation(
      async (_original: GeminiKeyConfig, next: GeminiKeyConfig) => {
        mocks.config = { ...mocks.config, geminiApiKeys: [next] };
      }
    );
    mocks.updateVertexConfig.mockImplementation(
      async (_original: ProviderKeyConfig, next: ProviderKeyConfig) => {
        mocks.config = { ...mocks.config, vertexApiKeys: [next] };
      }
    );
    mocks.updateMetaConfig.mockImplementation(
      async (_original: ProviderKeyConfig, next: ProviderKeyConfig) => {
        mocks.config = { ...mocks.config, metaApiKeys: [next] };
      }
    );
  });

  it.each([
    [undefined, 'disabled', true],
    [undefined, 'enabled', false],
    [true, 'inherit', null],
    [false, 'inherit', null],
    [true, 'enabled', false],
    [false, 'disabled', true],
  ] as const)(
    'sends %j -> %s as disable-cooling %j',
    async (initialOverride, nextPolicy, expectedOverride) => {
      mocks.config.geminiApiKeys = [
        {
          apiKey: 'gemini-key',
          ...(initialOverride === undefined ? {} : { disableCooling: initialOverride }),
        },
      ];
      let renderer!: ReactTestRenderer;
      await act(async () => {
        renderer = create(<AiProvidersPage />);
      });
      await flush();
      await openDetail(renderer);

      await act(async () => {
        renderer.root.findByProps({ 'data-policy': nextPolicy }).props.onClick();
        await Promise.resolve();
        await Promise.resolve();
      });
      await flush();

      expect(mocks.updateGeminiKey).toHaveBeenCalledWith(
        expect.objectContaining({ apiKey: 'gemini-key' }),
        expect.objectContaining({
          apiKey: 'gemini-key',
          disableCooling: expectedOverride,
        }),
        expect.objectContaining({ connection: apiClient.captureConnection() })
      );
      expect(renderer.root.findByProps({ 'data-current-policy': true }).children.join('')).toBe(
        nextPolicy
      );

      act(() => renderer.unmount());
    }
  );

  it('rolls an optimistic cooling policy change back after a failed request', async () => {
    mocks.updateGeminiKey.mockRejectedValueOnce(new Error('save failed'));
    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(<AiProvidersPage />);
    });
    await flush();
    await openDetail(renderer);

    await act(async () => {
      renderer.root.findByProps({ 'data-policy': 'disabled' }).props.onClick();
      await Promise.resolve();
      await Promise.resolve();
    });
    await flush();

    expect(renderer.root.findByProps({ 'data-current-policy': true }).children.join('')).toBe(
      'inherit'
    );
    expect(mocks.showNotification).toHaveBeenCalledWith(
      expect.stringContaining('notification.update_failed'),
      'error'
    );

    act(() => renderer.unmount());
  });

  it.each([
    [undefined, 'enabled', false],
    [true, 'inherit', null],
  ] as const)(
    'sends the Vertex %j -> %s transition as disable-cooling %j',
    async (initialOverride, nextPolicy, expectedOverride) => {
      mocks.config.geminiApiKeys = [];
      mocks.config.vertexApiKeys = [
        {
          apiKey: 'vertex-key',
          baseUrl: 'https://vertex.example.com',
          ...(initialOverride === undefined ? {} : { disableCooling: initialOverride }),
        },
      ];
      let renderer!: ReactTestRenderer;
      await act(async () => {
        renderer = create(<AiProvidersPage />);
      });
      await flush();
      await openDetail(renderer);

      await act(async () => {
        renderer.root.findByProps({ 'data-policy': nextPolicy }).props.onClick();
        await Promise.resolve();
        await Promise.resolve();
      });
      await flush();

      expect(mocks.updateVertexConfig).toHaveBeenCalledWith(
        expect.objectContaining({ apiKey: 'vertex-key' }),
        expect.objectContaining({
          apiKey: 'vertex-key',
          disableCooling: expectedOverride,
        }),
        expect.objectContaining({ connection: apiClient.captureConnection() })
      );
      expect(renderer.root.findByProps({ 'data-current-policy': true }).children.join('')).toBe(
        nextPolicy
      );

      act(() => renderer.unmount());
    }
  );

  it('rolls a failed Vertex cooling policy mutation back', async () => {
    mocks.config.geminiApiKeys = [];
    mocks.config.vertexApiKeys = [{ apiKey: 'vertex-key', baseUrl: 'https://vertex.example.com' }];
    mocks.updateVertexConfig.mockRejectedValueOnce(new Error('vertex save failed'));
    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(<AiProvidersPage />);
    });
    await flush();
    await openDetail(renderer);

    await act(async () => {
      renderer.root.findByProps({ 'data-policy': 'disabled' }).props.onClick();
      await Promise.resolve();
      await Promise.resolve();
    });
    await flush();

    expect(renderer.root.findByProps({ 'data-current-policy': true }).children.join('')).toBe(
      'inherit'
    );
    expect(mocks.updateConfigValue).toHaveBeenLastCalledWith(
      'vertex-api-key',
      expect.arrayContaining([expect.not.objectContaining({ disableCooling: true })])
    );
    expect(mocks.showNotification).toHaveBeenCalledWith(
      expect.stringContaining('notification.update_failed'),
      'error'
    );

    act(() => renderer.unmount());
  });

  it('sends the Meta undefined -> enabled transition as disable-cooling false', async () => {
    mocks.config.geminiApiKeys = [];
    mocks.config.metaApiKeys = [{ apiKey: 'meta-key', baseUrl: 'https://api.meta.ai/v1' }];
    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(<AiProvidersPage />);
    });
    await flush();
    await openDetail(renderer);

    await act(async () => {
      renderer.root.findByProps({ 'data-policy': 'enabled' }).props.onClick();
      await Promise.resolve();
      await Promise.resolve();
    });
    await flush();

    expect(mocks.updateMetaConfig).toHaveBeenCalledWith(
      expect.objectContaining({ apiKey: 'meta-key' }),
      expect.objectContaining({ apiKey: 'meta-key', disableCooling: false }),
      expect.objectContaining({ connection: apiClient.captureConnection() })
    );
    expect(renderer.root.findByProps({ 'data-current-policy': true }).children.join('')).toBe(
      'enabled'
    );

    act(() => renderer.unmount());
  });
});

describe('AiProvidersPage current-layer config refresh', () => {
  const openai = (name: string, nativeFunding = true): OpenAIProviderConfig => ({
    name, baseUrl: 'https://fixture.invalid', apiKeyEntries: [], nativeFunding,
  });
  const pendingRows = () => {
    let resolve!: (rows: OpenAIProviderConfig[]) => void;
    const promise = new Promise<OpenAIProviderConfig[]>((done) => { resolve = done; });
    return { promise, resolve };
  };
  const label = (renderer: ReactTestRenderer) =>
    renderer.root.findAllByProps({ 'data-open-detail': true })[0]?.children.join('');

  beforeEach(() => {
    vi.clearAllMocks();
    mocks.auth = { apiBase: 'http://core.example', managementKey: 'synthetic-key', connectionStatus: 'connected' };
    apiClient.setConfig(mocks.auth);
    mocks.cacheValid = true;
    mocks.transitionLayer = null;
    mocks.config = {
      geminiApiKeys: [],
      interactionsApiKeys: [],
      codexApiKeys: [],
      xaiApiKeys: [],
      metaApiKeys: [],
      claudeApiKeys: [],
      vertexApiKeys: [],
      openaiCompatibility: [],
    };
    mocks.getConfig.mockImplementation(async () => mocks.config);
    mocks.getVertexConfigs.mockImplementation(async () => mocks.config.vertexApiKeys);
    mocks.getOpenAIProviders.mockResolvedValue([]);
    mocks.updateGeminiKey.mockResolvedValue(undefined);
    mocks.updateVertexConfig.mockResolvedValue(undefined);
  });

  const renderPage = async () => {
    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(<AiProvidersPage />);
    });
    await flush();
    return renderer;
  };

  const updatePage = async (renderer: ReactTestRenderer) => {
    await act(async () => {
      renderer.update(<AiProvidersPage />);
    });
    await flush();
  };

  it('ignores a failed optimistic edit after switching Core connections', async () => {
    mocks.config.geminiApiKeys = [{ apiKey: 'A-config' }];
    let rejectSave!: (error: Error) => void;
    mocks.updateGeminiKey.mockImplementationOnce(() =>
      new Promise((_resolve, reject) => { rejectSave = reject; })
    );
    const renderer = await renderPage();
    await openDetail(renderer);
    const a = apiClient.captureConnection();
    await act(async () => {
      renderer.root.findByProps({ 'data-policy': 'disabled' }).props.onClick();
    });
    expect(mocks.updateGeminiKey).toHaveBeenCalledWith(
      expect.objectContaining({ apiKey: 'A-config' }),
      expect.objectContaining({ disableCooling: true }),
      expect.objectContaining({ connection: a })
    );
    mocks.auth = { ...mocks.auth, apiBase: 'http://other.example', managementKey: 'other-key' };
    apiClient.setConfig(mocks.auth);
    mocks.config.geminiApiKeys = [{ apiKey: 'B-config', disableCooling: false }];
    await updatePage(renderer);
    expect(label(renderer)).toBe('B-config');
    mocks.clearCache.mockClear();
    mocks.updateConfigValue.mockClear();
    const configReads = mocks.getConfig.mock.calls.length;
    await act(async () => { rejectSave(new Error('old save failed')); });
    expect(label(renderer)).toBe('B-config');
    expect(mocks.getConfig).toHaveBeenCalledTimes(configReads);
    expect(mocks.updateConfigValue).not.toHaveBeenCalled();
    expect(mocks.clearCache).not.toHaveBeenCalled();
    expect(mocks.showNotification).not.toHaveBeenCalled();
    act(() => renderer.unmount());
  });

  it('does not carry an unfinished provider editor across A to B to A', async () => {
    const renderer = await renderPage();
    const authA = { ...mocks.auth };
    const isEditorOpen = () => renderer.root.findAllByProps({ 'data-openai-editor': true }).length;
    await act(async () =>
      renderer.root.findByProps({ 'data-add-provider': true }).props.onClick()
    );
    expect(isEditorOpen()).toBe(1);
    mocks.auth = { ...authA, apiBase: 'http://other.example', managementKey: 'other-key' };
    apiClient.setConfig(mocks.auth);
    const bootstrapB = pendingRows();
    mocks.getOpenAIProviders.mockReturnValueOnce(bootstrapB.promise);
    await updatePage(renderer);
    expect(isEditorOpen()).toBe(0);
    await act(async () => bootstrapB.resolve([openai('new Core')]));
    expect(isEditorOpen()).toBe(0);
    mocks.auth = authA;
    apiClient.setConfig(mocks.auth);
    await updatePage(renderer);
    expect(isEditorOpen()).toBe(0);
    act(() => renderer.unmount());
  });

  it('revalidates provider configs on re-entry while keeping the local snapshot in view', async () => {
    mocks.getOpenAIProviders.mockResolvedValueOnce([openai('initial')]);
    const renderer = await renderPage();
    expect(mocks.getConfig).toHaveBeenCalledTimes(1);

    mocks.transitionLayer = { status: 'background' };
    await updatePage(renderer);

    const next = pendingRows();
    mocks.getOpenAIProviders.mockReturnValueOnce(next.promise);
    mocks.transitionLayer = { status: 'current' };
    await updatePage(renderer);

    expect(mocks.getConfig).toHaveBeenCalledTimes(2);
    expect(label(renderer)).toBe('initial');
    await act(async () => next.resolve([]));
    expect(label(renderer)).toBeUndefined();
    act(() => renderer.unmount());
  });

  it('does not adopt the previous connection’s shared cache while a scoped bootstrap is pending or fails', async () => {
    mocks.config = { ...mocks.config, openaiCompatibility: [openai('previous Core', false)] };
    const connected = { ...mocks.auth, apiBase: 'http://next-core.example', managementKey: 'next-key' };
    mocks.auth = connected;
    apiClient.setConfig(connected);
    let reject!: (error: Error) => void;
    mocks.getConfig.mockReturnValueOnce(new Promise((_resolve, fail) => { reject = fail; }));
    const view = await renderPage();
    expect(label(view)).toBeUndefined();
    expect(mocks.getConfig).toHaveBeenCalledWith(apiClient.captureConnection());
    expect(mocks.fetchConfig).not.toHaveBeenCalled();
    await act(async () => reject(new Error('scoped configuration unavailable')));
    expect(label(view)).toBeUndefined();
    expect(JSON.stringify(view.toJSON())).toContain('scoped configuration unavailable');
    expect(mocks.updateConfigValue).not.toHaveBeenCalled();
    act(() => view.unmount());
  });

  it('keeps the current rows visible and discards a refresh that resolves out of order', async () => {
    mocks.getOpenAIProviders.mockResolvedValueOnce([openai('initial')]);
    const view = await renderPage();
    expect(label(view)).toBe('initial');
    const stale = pendingRows();
    mocks.getOpenAIProviders.mockReturnValueOnce(stale.promise);
    let refresh!: Promise<void>;
    await act(async () => { refresh = mocks.headerRefresh!(); });
    expect(label(view)).toBe('initial');
    mocks.getOpenAIProviders.mockResolvedValueOnce([openai('replacement')]);
    await act(async () => { await mocks.headerRefresh!(); });
    expect(label(view)).toBe('replacement');

    await act(async () => {
      stale.resolve([openai('stale', false)]);
      await refresh;
    });
    expect(label(view)).toBe('replacement');
    expect(mocks.updateConfigValue.mock.calls).not.toContainEqual([
      'openai-compatibility', [openai('stale', false)],
    ]);
    act(() => view.unmount());
  });

  it.each(['connection', 'authorization', 'layer'])(
    'does not publish a response after leaving its %s, including re-entry',
    async (cause) => {
      const stale = pendingRows();
      mocks.getOpenAIProviders.mockReturnValueOnce(stale.promise);
      const view = await renderPage();
      if (cause === 'layer') {
        mocks.transitionLayer = { status: 'background' };
      } else {
        mocks.auth = {
          ...mocks.auth,
          ...(cause === 'connection' ? { apiBase: 'http://other.example' } : { managementKey: 'other-key' }),
        };
        apiClient.setConfig(mocks.auth);
      }
      mocks.getOpenAIProviders.mockResolvedValueOnce([openai('current')]);
      await updatePage(view);
      if (cause === 'layer') {
        mocks.transitionLayer = { status: 'current' };
        await updatePage(view);
      }
      expect(label(view)).toBe('current');
      await act(async () => stale.resolve([openai('stale', false)]));
      expect(label(view)).toBe('current');
      expect(mocks.updateConfigValue.mock.calls).not.toContainEqual([
        'openai-compatibility', [openai('stale', false)],
      ]);
      act(() => view.unmount());
    }
  );
});

describe('AiProvidersPage automatic start/stop projection', () => {
  const projection = (state: 'available' | 'auto_paused', revision: number) => ({
    version: 1,
    ready: true,
    revision,
    providers: [
      {
        resourceId: 'rid-group',
        state,
        memberCount: 1,
        pausedCount: state === 'auto_paused' ? 1 : 0,
        reasons: [],
        members: [
          {
            resourceId: 'rid-member',
            state: state === 'auto_paused' ? 'paused' : 'available',
            reasons: [],
          },
        ],
      },
    ],
  });

  const lastAutoState = (): ProviderAutoState | null | undefined =>
    mocks.providerDetailAutoStates[mocks.providerDetailAutoStates.length - 1];
  const appliedState = () => lastAutoState()?.state;
  const groupRow = {
    name: 'group',
    baseUrl: 'https://fixture.invalid',
    resourceId: 'rid-group',
    apiKeyEntries: [{ apiKey: 'synthetic' }],
  };

  beforeEach(() => {
    vi.clearAllMocks();
    mocks.providerDetailAutoStates.length = 0;
    mocks.auth = {
      apiBase: 'http://core.example',
      managementKey: 'synthetic-key',
      connectionStatus: 'connected',
    };
    apiClient.setConfig(mocks.auth);
    mocks.cacheValid = true;
    mocks.transitionLayer = null;
    mocks.featureAvailability = {
      checking: false,
      managerServiceAvailable: true,
      managerServiceBase: 'http://manager.example',
    };
    mocks.getProviderAutoState.mockResolvedValue(null);
    mocks.getProviderPolicyMigration.mockResolvedValue(null);
    mocks.config = {
      geminiApiKeys: [],
      interactionsApiKeys: [],
      codexApiKeys: [],
      xaiApiKeys: [],
      metaApiKeys: [],
      claudeApiKeys: [],
      vertexApiKeys: [],
      openaiCompatibility: [],
    };
    mocks.getConfig.mockImplementation(async () => mocks.config);
    mocks.getVertexConfigs.mockImplementation(async () => mocks.config.vertexApiKeys);
    mocks.getOpenAIProviders.mockResolvedValue([groupRow]);
  });

  afterEach(() => {
    mocks.featureAvailability = {
      checking: false,
      managerServiceAvailable: false,
      managerServiceBase: '',
    };
  });

  const renderPage = async () => {
    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(<AiProvidersPage />);
    });
    await flush();
    return renderer;
  };

  const refresh = async () => {
    await act(async () => {
      await mocks.headerRefresh!();
    });
    await flush();
  };

  it('applies a refreshed projection whose providers changed at the same revision', async () => {
    mocks.getProviderAutoState.mockResolvedValueOnce(projection('available', 7));
    const view = await renderPage();
    await openDetail(view);
    expect(appliedState()).toBe('available');

    mocks.getProviderAutoState.mockResolvedValueOnce(projection('auto_paused', 7));
    await refresh();

    expect(appliedState()).toBe('auto_paused');
    act(() => view.unmount());
  });

  it('keeps the applied state untouched when a refresh repeats the same content', async () => {
    mocks.getProviderAutoState.mockResolvedValueOnce(projection('auto_paused', 7));
    const view = await renderPage();
    await openDetail(view);
    const applied = lastAutoState();
    expect(appliedState()).toBe('auto_paused');

    mocks.getProviderAutoState.mockResolvedValueOnce(projection('auto_paused', 8));
    await refresh();

    expect(appliedState()).toBe('auto_paused');
    expect(lastAutoState()).toBe(applied);
    act(() => view.unmount());
  });
});
