import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from 'react';
import { useTranslation } from 'react-i18next';
import {
  buildProviderRows,
  ClaudeEditDrawer,
  CodexEditDrawer,
  filterAndSortProviderRows,
  GeminiEditDrawer,
  OpenAIEditDrawer,
  PROVIDER_KIND_LABELS,
  ProviderDetailDrawer,
  type ProviderPolicyConnection,
  ProviderHealthCheckDrawer,
  ProviderTable,
  ProviderToolbar,
  VertexEditDrawer,
  useProviderRecentRequests,
  type ProviderHealthCheckApplyAction,
  type ProviderKind,
  type ProviderKindFilter,
  type ProviderRow,
  type ProviderSortDirection,
  type ProviderSortOption,
} from '@/components/providers';
import {
  withDisableAllModelsRule,
  withoutDisableAllModelsRule,
} from '@/components/providers/utils';
import { usePageTransitionLayer } from '@/components/common/PageTransitionLayer';
import { useUsageData } from '@/features/monitoring/hooks/useUsageData';
import { Button } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';
import { EmptyState } from '@/components/ui/EmptyState';
import { Select } from '@/components/ui/Select';
import { usePanelFeatureAvailability } from '@/hooks/usePanelFeatureAvailability';
import { useResourceProtection } from '@/features/resourceProtection/resourceProtection';
import { useHeaderRefresh } from '@/hooks/useHeaderRefresh';
import { configApi, providersApi } from '@/services/api';
import { apiClient } from '@/services/api/client';
import type { ProviderMutationScope } from '@/services/api/providers';
import { readListPageSize, writeListPageSize } from '@/utils/listPageSize';
import { loadRuntimeControls, runtimeBalancesReady, subscribeRuntimeControls } from '@/utils/quota/runtimeControls';
import {
  getProviderPolicyMigration,
  type ProviderPolicyMigrationStatus,
} from '@/services/api/providerPolicy';
import {
  getProviderAutoState,
  indexProviderAutoStates,
  type ProviderAutoStateMap,
} from '@/services/api/providerAutoState';
import { useAuthStore, useConfigStore, useNotificationStore, useThemeStore } from '@/stores';
import {
  CloakConfig,
  type Config,
  coolingPolicyToOverride,
  type CoolingPolicy,
  GeminiKeyConfig,
  OpenAIProviderConfig,
  ProviderKeyConfig,
} from '@/types';
import { buildProviderDeleteSecondConfirmation } from './model/deleteConfirmation';
import styles from './AiProvidersPage.module.scss';

type ConfigMutation = ProviderMutationScope & { isCurrent: () => boolean };

const PROVIDER_TABLE_DEFAULT_PAGE_SIZE = 10;
const PROVIDER_TABLE_PAGE_SIZE_OPTIONS = [10, 20, 50] as const;

const DEFAULT_CLOAK_CONFIG: CloakConfig = {
  mode: 'auto',
  strictMode: false,
  sensitiveWords: [],
};

export function AiProvidersPage() {
  const { t } = useTranslation();
  const { showNotification, showConfirmation } = useNotificationStore();
  const resolvedTheme = useThemeStore((state) => state.resolvedTheme);
  const apiBase = useAuthStore((state) => state.apiBase);
  const balanceManagementKey = useAuthStore((state) => state.managementKey);
  const connectionStatus = useAuthStore((state) => state.connectionStatus);
  const connection = apiClient.captureConnection();
  const connectionScope = useMemo(
    () => ({ apiBase, balanceManagementKey, connectionStatus, connection }),
    [apiBase, balanceManagementKey, connectionStatus, connection]
  );
  const pageTransitionLayer = usePageTransitionLayer();
  const isCurrentLayer = pageTransitionLayer ? pageTransitionLayer.status === 'current' : true;
  const loadScope = useMemo(
    () => (isCurrentLayer && connectionStatus === 'connected' ? connectionScope : null),
    [connectionScope, connectionStatus, isCurrentLayer]
  );
  const activeLoadScope = useRef<typeof loadScope>(null);
  const configRequestId = useRef(0);
  const viewGeneration = useRef(0);
  const activeMutation = useRef<ConfigMutation | null>(null);
  const [switching, setSwitching] = useState<{ key: string; generation: number } | null>(null);
  const pendingConfigRequest = useRef(false);
  const reloadAfterInterruption = useRef(false);
  useLayoutEffect(() => {
    activeLoadScope.current = loadScope;
    return () => {
      activeLoadScope.current = null;
      activeMutation.current = null;
      viewGeneration.current += 1;
      configRequestId.current += 1;
      reloadAfterInterruption.current ||= pendingConfigRequest.current;
      pendingConfigRequest.current = false;
    };
  }, [loadScope]);
  const featureAvailability = usePanelFeatureAvailability();

  const updateConfigValue = useConfigStore((state) => state.updateConfigValue);
  const clearCache = useConfigStore((state) => state.clearCache);

  const [configSnapshot, setConfigSnapshot] = useState<{
    config: Config;
    scope: typeof connectionScope;
  } | null>(null);
  const [finishedBootstrap, setFinishedBootstrap] = useState<typeof loadScope>(null);
  const hasSnapshot = configSnapshot?.scope === connectionScope;
  const loading = !hasSnapshot && finishedBootstrap !== connectionScope;
  const balancesReady = useSyncExternalStore(subscribeRuntimeControls, runtimeBalancesReady, () => false);
  const [error, setError] = useState('');
  const [providerPolicyRevision, setProviderPolicyRevision] = useState(0);
  const handleProviderPolicyChanged = useCallback(() => setProviderPolicyRevision((value) => value + 1), []);
  const { status: resourceProtection, forProvider, refresh: refreshResourceProtection } =
    useResourceProtection(
      { ...featureAvailability, checking: featureAvailability.checking || loading },
      hasSnapshot ? configSnapshot.config : null
    );

  const [geminiKeys, setGeminiKeys] = useState<GeminiKeyConfig[]>([]);
  const providerPolicyConnection: ProviderPolicyConnection = {
    base: featureAvailability.managerServiceBase,
    managementKey: balanceManagementKey,
    disabled: !featureAvailability.managerServiceAvailable,
    revision: providerPolicyRevision,
    onChanged: handleProviderPolicyChanged,
  };
  const [policyMigration, setPolicyMigration] = useState<ProviderPolicyMigrationStatus | null>(null);
  useEffect(() => {
    if (
      !featureAvailability.managerServiceAvailable ||
      !featureAvailability.managerServiceBase ||
      !balanceManagementKey
    ) {
      setPolicyMigration(null);
      return;
    }
    const controller = new AbortController();
    void getProviderPolicyMigration(
      featureAvailability.managerServiceBase,
      balanceManagementKey,
      controller.signal
    )
      .then(setPolicyMigration)
      .catch(() => {
        if (!controller.signal.aborted) setPolicyMigration(null);
      });
    return () => controller.abort();
  }, [
    featureAvailability.managerServiceAvailable,
    featureAvailability.managerServiceBase,
    balanceManagementKey,
    providerPolicyRevision,
  ]);
  const [providerAutoStates, setProviderAutoStates] = useState<ProviderAutoStateMap>({});
  const [autoStateRefreshToken, setAutoStateRefreshToken] = useState(0);
  useEffect(() => {
    if (
      !featureAvailability.managerServiceAvailable ||
      !featureAvailability.managerServiceBase ||
      !balanceManagementKey
    ) {
      return;
    }
    const controller = new AbortController();
    void getProviderAutoState(
      featureAvailability.managerServiceBase,
      balanceManagementKey,
      controller.signal
    ).then((projection) => {
      // A failed read resolves to null on purpose: keep the last known icons
      // instead of showing an error or flashing every provider available.
      if (!projection || controller.signal.aborted) return;
      setProviderAutoStates(indexProviderAutoStates(projection));
    });
    return () => controller.abort();
  }, [
    featureAvailability.managerServiceAvailable,
    featureAvailability.managerServiceBase,
    balanceManagementKey,
    autoStateRefreshToken,
  ]);
  const [interactionsKeys, setInteractionsKeys] = useState<GeminiKeyConfig[]>([]);
  const [codexConfigs, setCodexConfigs] = useState<ProviderKeyConfig[]>([]);
  const [xaiConfigs, setXAIConfigs] = useState<ProviderKeyConfig[]>([]);
  const [metaConfigs, setMetaConfigs] = useState<ProviderKeyConfig[]>([]);
  const [claudeConfigs, setClaudeConfigs] = useState<ProviderKeyConfig[]>([]);
  const [vertexConfigs, setVertexConfigs] = useState<ProviderKeyConfig[]>([]);
  const [openaiProviders, setOpenaiProviders] = useState<OpenAIProviderConfig[]>([]);

  const matchesView = useCallback(() => {
    const auth = useAuthStore.getState();
    return (
      loadScope !== null &&
      activeLoadScope.current === loadScope &&
      auth.connectionStatus === 'connected' &&
      auth.apiBase === loadScope.apiBase &&
      auth.managementKey === loadScope.balanceManagementKey &&
      apiClient.isCurrentConnection(loadScope.connection)
    );
  }, [loadScope]);
  const beginConfigMutation = (key: string): ConfigMutation | null => {
    if (!loadScope || !hasSnapshot || !matchesView() || activeMutation.current) return null;
    const generation = viewGeneration.current;
    const mutation: ConfigMutation = {
      connection: loadScope.connection,
      isCurrent: () =>
        generation === viewGeneration.current &&
        activeMutation.current === mutation &&
        matchesView(),
    };
    activeMutation.current = mutation;
    configRequestId.current += 1;
    pendingConfigRequest.current = false;
    setSwitching({ key, generation });
    return mutation;
  };
  const finishConfigMutation = (mutation: ConfigMutation) => {
    if (activeMutation.current !== mutation) return;
    activeMutation.current = null;
    setSwitching(null);
  };
  const configSwitchingKey =
    switching && switching.generation === viewGeneration.current && matchesView()
      ? switching.key
      : null;

  // 表格筛选 / 排序 / 详情状态
  const [kindFilter, setKindFilter] = useState<ProviderKindFilter>('all');
  const [searchText, setSearchText] = useState('');
  const [selectedModels, setSelectedModels] = useState<Set<string>>(new Set());
  const [sortOption, setSortOption] = useState<ProviderSortOption>('priority');
  const [sortDirection, setSortDirection] = useState<ProviderSortDirection>('desc');
  const [detailRowKey, setDetailRowKey] = useState<string | null>(null);
  const [detailContent, setDetailContent] = useState<'credential' | 'balance'>('credential');
  const [healthCheckOpen, setHealthCheckOpen] = useState(false);
  const [editor, setEditor] = useState<{
    kind: ProviderKind;
    index: number | null;
    scope: NonNullable<typeof loadScope>;
  } | null>(null);
  const activeEditor = editor?.scope === loadScope ? editor : null;
  const editDrawerKind = activeEditor?.kind;
  const editDrawerIndex = activeEditor?.index ?? null;
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(() =>
    readListPageSize(
      'aiProviders',
      PROVIDER_TABLE_PAGE_SIZE_OPTIONS,
      PROVIDER_TABLE_DEFAULT_PAGE_SIZE
    )
  );

  const disableControls = connectionStatus !== 'connected';
  const isSwitching = Boolean(configSwitchingKey);
  const actionsDisabled = disableControls || loading || isSwitching;

  const { usageByProvider, loadRecentRequests, refreshRecentRequests } = useProviderRecentRequests({
    enabled: isCurrentLayer,
  });
  const { providerKeyAliases, loadProviderKeyAliases } = useUsageData({
    loadUsageEvents: false,
  });

  const getErrorMessage = (err: unknown) => {
    if (err instanceof Error) return err.message;
    if (typeof err === 'string') return err;
    return '';
  };

  const loadConfigs = useCallback(async () => {
    if (!loadScope || !matchesView()) return;
    const requestId = ++configRequestId.current;
    const isCurrent = () => requestId === configRequestId.current && matchesView();
    pendingConfigRequest.current = true;
    setError('');
    try {
      const [configResult, vertexResult, openaiResult] = await Promise.allSettled([
        configApi.getConfig(loadScope.connection),
        providersApi.getVertexConfigs(loadScope.connection),
        providersApi.getOpenAIProviders(loadScope.connection),
      ]);

      if (!isCurrent()) return;
      if (configResult.status !== 'fulfilled') {
        throw configResult.reason;
      }

      const data = configResult.value;
      setGeminiKeys(data?.geminiApiKeys || []);
      setInteractionsKeys(data?.interactionsApiKeys || []);
      setCodexConfigs(data?.codexApiKeys || []);
      setXAIConfigs(data?.xaiApiKeys || []);
      setMetaConfigs(data?.metaApiKeys || []);
      setClaudeConfigs(data?.claudeApiKeys || []);
      if (!hasSnapshot && openaiResult.status !== 'fulfilled') {
        setOpenaiProviders((data?.openaiCompatibility || []).map((provider) => {
          const fallback = { ...provider };
          delete fallback.nativeFunding;
          return fallback;
        }));
      }

      setVertexConfigs(vertexResult.status === 'fulfilled' ? vertexResult.value : data?.vertexApiKeys || []);

      if (openaiResult.status === 'fulfilled') {
        setOpenaiProviders(openaiResult.value || []);
      } else {
        setError(getErrorMessage(openaiResult.reason) || t('notification.refresh_failed'));
      }
      setConfigSnapshot({
        scope: loadScope,
        config: {
          ...data,
          vertexApiKeys: vertexResult.status === 'fulfilled' ? vertexResult.value : data.vertexApiKeys,
          openaiCompatibility:
            openaiResult.status === 'fulfilled' ? openaiResult.value : data.openaiCompatibility,
        },
      });
    } catch (err: unknown) {
      if (isCurrent()) setError(getErrorMessage(err) || t('notification.refresh_failed'));
    } finally {
      if (isCurrent()) {
        pendingConfigRequest.current = false;
        setFinishedBootstrap(loadScope);
      }
    }
  }, [hasSnapshot, loadScope, matchesView, t]);

  const lastLoadScope = useRef<typeof loadScope>(null);
  const leftCurrentLayerRef = useRef(false);

  useEffect(() => {
    if (!loadScope) {
      // Editors invalidate the provider config cache before navigating back
      // (clearCache also drops the full-config cache). When this list becomes
      // the current layer again, reload so a committed save is never hidden
      // behind stale data — even when its read-back failed.
      leftCurrentLayerRef.current = true;
      return;
    }
    if (lastLoadScope.current !== loadScope) {
      lastLoadScope.current = loadScope;
      leftCurrentLayerRef.current = false;
      reloadAfterInterruption.current = false;
      void loadRuntimeControls();
      void loadConfigs();
    } else if (reloadAfterInterruption.current || leftCurrentLayerRef.current) {
      reloadAfterInterruption.current = false;
      void loadConfigs();
    }
    leftCurrentLayerRef.current = false;
    void loadRecentRequests().catch(() => {});
  }, [loadConfigs, loadRecentRequests, loadScope]);

  const handleRecentRequestsRefresh = useCallback(async () => {
    void refreshResourceProtection();
    setAutoStateRefreshToken((value) => value + 1);
    await Promise.all([
      refreshRecentRequests(),
      activeMutation.current ? undefined : loadConfigs(),
    ]);
  }, [loadConfigs, refreshRecentRequests, refreshResourceProtection]);

  useHeaderRefresh(handleRecentRequestsRefresh, isCurrentLayer);

  const openEditorDrawer = useCallback(
    (kind: ProviderKind, editIndex: number | null) => {
      if (!loadScope || !matchesView()) return;
      setDetailRowKey(null);
      setEditor({ kind, index: editIndex, scope: loadScope });
    },
    [loadScope, matchesView]
  );

  const closeEditorDrawer = useCallback(() => {
    setEditor((value) => (value?.scope === loadScope ? null : value));
  }, [loadScope]);

  const handleDrawerSaved = useCallback(() => {
    if (!matchesView()) return;
    void loadConfigs();
    void loadProviderKeyAliases();
  }, [matchesView, loadConfigs, loadProviderKeyAliases]);

  // 统一行集合与派生数据
  const rows = useMemo(
    () =>
      hasSnapshot
        ? buildProviderRows({
            gemini: geminiKeys,
            interactions: interactionsKeys,
            codex: codexConfigs,
            xai: xaiConfigs,
            meta: metaConfigs,
            claude: claudeConfigs,
            vertex: vertexConfigs,
            openai: openaiProviders,
            usageByProvider,
            providerKeyAliases,
          })
        : [],
    [
      claudeConfigs,
      codexConfigs,
      geminiKeys,
      hasSnapshot,
      interactionsKeys,
      metaConfigs,
      openaiProviders,
      providerKeyAliases,
      usageByProvider,
      vertexConfigs,
      xaiConfigs,
    ]
  );

  const allModelNames = useMemo(() => {
    const names = new Set<string>();
    rows.forEach((row) => {
      row.modelNames.forEach((name) => names.add(name));
    });
    return Array.from(names).sort();
  }, [rows]);

  useEffect(() => {
    // 配置变更后清理已不存在的模型筛选项，避免筛选结果一直为空。
    setSelectedModels((prev) => {
      if (prev.size === 0) return prev;

      const availableModels = new Set(allModelNames);
      const next = new Set(Array.from(prev).filter((name) => availableModels.has(name)));
      return next.size === prev.size ? prev : next;
    });
  }, [allModelNames]);

  const visibleRows = useMemo(
    () =>
      filterAndSortProviderRows(rows, {
        kind: kindFilter,
        searchText,
        selectedModels,
        sortOption,
        sortDirection,
      }),
    [kindFilter, rows, searchText, selectedModels, sortDirection, sortOption]
  );

  const totalPages = Math.max(1, Math.ceil(visibleRows.length / pageSize));
  const currentPage = Math.min(page, totalPages);
  const pageStart = (currentPage - 1) * pageSize;
  const pagedRows = visibleRows.slice(pageStart, pageStart + pageSize);
  const pageStartItem = visibleRows.length === 0 ? 0 : pageStart + 1;
  const pageEndItem = Math.min(visibleRows.length, pageStart + pageSize);

  useEffect(() => {
    setPage(1);
  }, [kindFilter, searchText, selectedModels, sortDirection, sortOption]);

  useEffect(() => {
    if (page === currentPage) return;
    setPage(currentPage);
  }, [currentPage, page]);

  const kindCounts = useMemo(() => {
    const counts: Record<ProviderKindFilter, number> = {
      all: rows.length,
      gemini: 0,
      interactions: 0,
      codex: 0,
      xai: 0,
      meta: 0,
      claude: 0,
      vertex: 0,
      openai: 0,
    };
    rows.forEach((row) => {
      counts[row.kind] += 1;
    });
    return counts;
  }, [rows]);

  const detailRow = useMemo(
    () => (detailRowKey ? (rows.find((row) => row.key === detailRowKey) ?? null) : null),
    [detailRowKey, rows]
  );
  const detailAutoState = useMemo(() => {
    const resourceId = detailRow?.raw.resourceId;
    return resourceId ? providerAutoStates[resourceId] : undefined;
  }, [detailRow, providerAutoStates]);

  const filtersActive = kindFilter !== 'all' || searchText.trim() !== '' || selectedModels.size > 0;

  const clearFilters = () => {
    setKindFilter('all');
    setSearchText('');
    setSelectedModels(new Set());
  };

  const applyProviderEnabledActions = async (
    actions: Map<string, ProviderHealthCheckApplyAction>
  ) => {
    if (actions.size === 0) return;
    if (activeMutation.current) return;

    const rowByKey = new Map(rows.map((row) => [row.key, row]));
    const previous = {
      gemini: geminiKeys,
      interactions: interactionsKeys,
      codex: codexConfigs,
      xai: xaiConfigs,
      meta: metaConfigs,
      claude: claudeConfigs,
      vertex: vertexConfigs,
      openai: openaiProviders,
    };
    let nextGemini = geminiKeys;
    let nextInteractions = interactionsKeys;
    let nextCodex = codexConfigs;
    let nextXAI = xaiConfigs;
    let nextMeta = metaConfigs;
    let nextClaude = claudeConfigs;
    let nextVertex = vertexConfigs;
    let nextOpenai = openaiProviders;
    const changed = {
      gemini: false,
      interactions: false,
      codex: false,
      xai: false,
      meta: false,
      claude: false,
      vertex: false,
      openai: false,
    };

    actions.forEach((action, providerKey) => {
      const row = rowByKey.get(providerKey);
      if (!row) return;
      const enabled = action === 'enable';
      if (row.enabled === enabled) return;

      if (row.kind === 'gemini') {
        const current = nextGemini[row.originalIndex];
        if (!current) return;
        const excludedModels = enabled
          ? withoutDisableAllModelsRule(current.excludedModels)
          : withDisableAllModelsRule(current.excludedModels);
        nextGemini = nextGemini.map((item, index) =>
          index === row.originalIndex ? { ...item, excludedModels } : item
        );
        changed.gemini = true;
      } else if (row.kind === 'interactions') {
        const current = nextInteractions[row.originalIndex];
        if (!current) return;
        const excludedModels = enabled
          ? withoutDisableAllModelsRule(current.excludedModels)
          : withDisableAllModelsRule(current.excludedModels);
        nextInteractions = nextInteractions.map((item, index) =>
          index === row.originalIndex ? { ...item, excludedModels } : item
        );
        changed.interactions = true;
      } else if (row.kind === 'codex') {
        const current = nextCodex[row.originalIndex];
        if (!current) return;
        const excludedModels = enabled
          ? withoutDisableAllModelsRule(current.excludedModels)
          : withDisableAllModelsRule(current.excludedModels);
        nextCodex = nextCodex.map((item, index) =>
          index === row.originalIndex ? { ...item, excludedModels } : item
        );
        changed.codex = true;
      } else if (row.kind === 'xai') {
        const current = nextXAI[row.originalIndex];
        if (!current) return;
        const excludedModels = enabled
          ? withoutDisableAllModelsRule(current.excludedModels)
          : withDisableAllModelsRule(current.excludedModels);
        nextXAI = nextXAI.map((item, index) =>
          index === row.originalIndex ? { ...item, excludedModels } : item
        );
        changed.xai = true;
      } else if (row.kind === 'meta') {
        const current = nextMeta[row.originalIndex];
        if (!current) return;
        const excludedModels = enabled
          ? withoutDisableAllModelsRule(current.excludedModels)
          : withDisableAllModelsRule(current.excludedModels);
        nextMeta = nextMeta.map((item, index) =>
          index === row.originalIndex ? { ...item, excludedModels } : item
        );
        changed.meta = true;
      } else if (row.kind === 'claude') {
        const current = nextClaude[row.originalIndex];
        if (!current) return;
        const excludedModels = enabled
          ? withoutDisableAllModelsRule(current.excludedModels)
          : withDisableAllModelsRule(current.excludedModels);
        nextClaude = nextClaude.map((item, index) =>
          index === row.originalIndex ? { ...item, excludedModels } : item
        );
        changed.claude = true;
      } else if (row.kind === 'vertex') {
        const current = nextVertex[row.originalIndex];
        if (!current) return;
        const excludedModels = enabled
          ? withoutDisableAllModelsRule(current.excludedModels)
          : withDisableAllModelsRule(current.excludedModels);
        nextVertex = nextVertex.map((item, index) =>
          index === row.originalIndex ? { ...item, excludedModels } : item
        );
        changed.vertex = true;
      } else {
        const current = nextOpenai[row.originalIndex];
        if (!current) return;
        nextOpenai = nextOpenai.map((item, index) =>
          index === row.originalIndex ? { ...item, disabled: !enabled } : item
        );
        changed.openai = true;
      }
    });

    if (!Object.values(changed).some(Boolean)) {
      showNotification(t('ai_providers.health_check_no_changes'), 'success');
      return;
    }

    const mutation = beginConfigMutation('health-check');
    if (!mutation) return;

    const applyLocalState = (
      gemini: GeminiKeyConfig[],
      interactions: GeminiKeyConfig[],
      codex: ProviderKeyConfig[],
      xai: ProviderKeyConfig[],
      meta: ProviderKeyConfig[],
      claude: ProviderKeyConfig[],
      vertex: ProviderKeyConfig[],
      openai: OpenAIProviderConfig[]
    ) => {
      if (changed.gemini) {
        setGeminiKeys(gemini);
        updateConfigValue('gemini-api-key', gemini);
        clearCache('gemini-api-key');
      }
      if (changed.interactions) {
        setInteractionsKeys(interactions);
        updateConfigValue('interactions-api-key', interactions);
        clearCache('interactions-api-key');
      }
      if (changed.codex) {
        setCodexConfigs(codex);
        updateConfigValue('codex-api-key', codex);
        clearCache('codex-api-key');
      }
      if (changed.xai) {
        setXAIConfigs(xai);
        updateConfigValue('xai-api-key', xai);
        clearCache('xai-api-key');
      }
      if (changed.meta) {
        setMetaConfigs(meta);
        updateConfigValue('meta-api-key', meta);
        clearCache('meta-api-key');
      }
      if (changed.claude) {
        setClaudeConfigs(claude);
        updateConfigValue('claude-api-key', claude);
        clearCache('claude-api-key');
      }
      if (changed.vertex) {
        setVertexConfigs(vertex);
        updateConfigValue('vertex-api-key', vertex);
        clearCache('vertex-api-key');
      }
      if (changed.openai) {
        setOpenaiProviders(openai);
        updateConfigValue('openai-compatibility', openai);
        clearCache('openai-compatibility');
      }
    };

    applyLocalState(
      nextGemini,
      nextInteractions,
      nextCodex,
      nextXAI,
      nextMeta,
      nextClaude,
      nextVertex,
      nextOpenai
    );

    try {
      const mutations: Array<() => Promise<unknown>> = [];
      if (changed.gemini) {
        nextGemini.forEach((item, index) => {
          if (item !== previous.gemini[index]) {
            mutations.push(() => providersApi.updateGeminiKey(previous.gemini[index], item, mutation));
          }
        });
      }
      if (changed.interactions) {
        nextInteractions.forEach((item, index) => {
          if (item !== previous.interactions[index]) {
            mutations.push(() =>
              providersApi.updateInteractionsKey(previous.interactions[index], item, mutation)
            );
          }
        });
      }
      if (changed.codex) {
        nextCodex.forEach((item, index) => {
          if (item !== previous.codex[index]) {
            mutations.push(() => providersApi.updateCodexConfig(previous.codex[index], item, mutation));
          }
        });
      }
      if (changed.xai) {
        nextXAI.forEach((item, index) => {
          if (item !== previous.xai[index]) {
            mutations.push(() => providersApi.updateXAIConfig(previous.xai[index], item, mutation));
          }
        });
      }
      if (changed.meta) {
        nextMeta.forEach((item, index) => {
          if (item !== previous.meta[index]) {
            mutations.push(() => providersApi.updateMetaConfig(previous.meta[index], item, mutation));
          }
        });
      }
      if (changed.claude) {
        nextClaude.forEach((item, index) => {
          if (item !== previous.claude[index]) {
            mutations.push(() => providersApi.updateClaudeConfig(previous.claude[index], item, mutation));
          }
        });
      }
      if (changed.vertex) {
        nextVertex.forEach((item, index) => {
          if (item !== previous.vertex[index]) {
            mutations.push(() => providersApi.updateVertexConfig(previous.vertex[index], item, mutation));
          }
        });
      }
      if (changed.openai) {
        nextOpenai.forEach((item, index) => {
          if (item !== previous.openai[index]) {
            mutations.push(() =>
              providersApi.updateOpenAIProvider(previous.openai[index].name, index, item, mutation)
            );
          }
        });
      }
      for (const mutate of mutations) {
        await mutate();
        if (!mutation.isCurrent()) return;
      }
      await loadConfigs();
      if (!mutation.isCurrent()) return;
      showNotification(t('ai_providers.health_check_apply_success'), 'success');
    } catch (err: unknown) {
      if (!mutation.isCurrent()) return;
      const message = getErrorMessage(err);
      await loadConfigs();
      if (!mutation.isCurrent()) return;
      showNotification(`${t('notification.update_failed')}: ${message}`, 'error');
      throw err;
    } finally {
      finishConfigMutation(mutation);
    }
  };

  const setHealthCheckProviderEnabled = async (providerKey: string, enabled: boolean) => {
    await applyProviderEnabledActions(new Map([[providerKey, enabled ? 'enable' : 'disable']]));
  };

  // 启停（key-based providers 走 excludedModels 规则）
  const setConfigEnabled = async (
    provider: Exclude<ProviderKind, 'openai'>,
    index: number,
    enabled: boolean
  ) => {
    if (provider === 'gemini' || provider === 'interactions') {
      const source = provider === 'gemini' ? geminiKeys : interactionsKeys;
      const current = source[index];
      if (!current) return;

      const switchingKey = `${provider}:${current.apiKey}`;
      const mutation = beginConfigMutation(switchingKey);
      if (!mutation) return;

      const previousList = source;
      const nextExcluded = enabled
        ? withoutDisableAllModelsRule(current.excludedModels)
        : withDisableAllModelsRule(current.excludedModels);
      const nextItem: GeminiKeyConfig = { ...current, excludedModels: nextExcluded };
      const nextList = previousList.map((item, idx) => (idx === index ? nextItem : item));

      if (provider === 'gemini') {
        setGeminiKeys(nextList);
        updateConfigValue('gemini-api-key', nextList);
        clearCache('gemini-api-key');
      } else {
        setInteractionsKeys(nextList);
        updateConfigValue('interactions-api-key', nextList);
        clearCache('interactions-api-key');
      }

      try {
        if (provider === 'gemini') {
          await providersApi.updateGeminiKey(current, nextItem, mutation);
          if (!mutation.isCurrent()) return;
        } else {
          await providersApi.updateInteractionsKey(current, nextItem, mutation);
          if (!mutation.isCurrent()) return;
        }
        await loadConfigs();
        if (!mutation.isCurrent()) return;
        showNotification(
          enabled ? t('notification.config_enabled') : t('notification.config_disabled'),
          'success'
        );
      } catch (err: unknown) {
        if (!mutation.isCurrent()) return;
        const message = getErrorMessage(err);
        if (provider === 'gemini') {
          setGeminiKeys(previousList);
          updateConfigValue('gemini-api-key', previousList);
          clearCache('gemini-api-key');
        } else {
          setInteractionsKeys(previousList);
          updateConfigValue('interactions-api-key', previousList);
          clearCache('interactions-api-key');
        }
        showNotification(`${t('notification.update_failed')}: ${message}`, 'error');
      } finally {
        finishConfigMutation(mutation);
      }
      return;
    }

    const source =
      provider === 'codex'
        ? codexConfigs
        : provider === 'xai'
          ? xaiConfigs
          : provider === 'meta'
            ? metaConfigs
            : provider === 'claude'
              ? claudeConfigs
              : vertexConfigs;
    const current = source[index];
    if (!current) return;

    const switchingKey = `${provider}:${current.apiKey}`;
    const mutation = beginConfigMutation(switchingKey);
    if (!mutation) return;

    const previousList = source;
    const nextExcluded = enabled
      ? withoutDisableAllModelsRule(current.excludedModels)
      : withDisableAllModelsRule(current.excludedModels);
    const nextItem: ProviderKeyConfig = { ...current, excludedModels: nextExcluded };
    const nextList = previousList.map((item, idx) => (idx === index ? nextItem : item));

    if (provider === 'codex') {
      setCodexConfigs(nextList);
      updateConfigValue('codex-api-key', nextList);
      clearCache('codex-api-key');
    } else if (provider === 'xai') {
      setXAIConfigs(nextList);
      updateConfigValue('xai-api-key', nextList);
      clearCache('xai-api-key');
    } else if (provider === 'meta') {
      setMetaConfigs(nextList);
      updateConfigValue('meta-api-key', nextList);
      clearCache('meta-api-key');
    } else if (provider === 'claude') {
      setClaudeConfigs(nextList);
      updateConfigValue('claude-api-key', nextList);
      clearCache('claude-api-key');
    } else {
      setVertexConfigs(nextList);
      updateConfigValue('vertex-api-key', nextList);
      clearCache('vertex-api-key');
    }

    try {
      if (provider === 'codex') {
        await providersApi.updateCodexConfig(current, nextItem, mutation);
        if (!mutation.isCurrent()) return;
      } else if (provider === 'xai') {
        await providersApi.updateXAIConfig(current, nextItem, mutation);
        if (!mutation.isCurrent()) return;
      } else if (provider === 'meta') {
        await providersApi.updateMetaConfig(current, nextItem, mutation);
        if (!mutation.isCurrent()) return;
      } else if (provider === 'claude') {
        await providersApi.updateClaudeConfig(current, nextItem, mutation);
        if (!mutation.isCurrent()) return;
      } else {
        await providersApi.updateVertexConfig(current, nextItem, mutation);
        if (!mutation.isCurrent()) return;
      }
      await loadConfigs();
      if (!mutation.isCurrent()) return;
      showNotification(
        enabled ? t('notification.config_enabled') : t('notification.config_disabled'),
        'success'
      );
    } catch (err: unknown) {
      if (!mutation.isCurrent()) return;
      const message = getErrorMessage(err);
      if (provider === 'codex') {
        setCodexConfigs(previousList);
        updateConfigValue('codex-api-key', previousList);
        clearCache('codex-api-key');
      } else if (provider === 'xai') {
        setXAIConfigs(previousList);
        updateConfigValue('xai-api-key', previousList);
        clearCache('xai-api-key');
      } else if (provider === 'meta') {
        setMetaConfigs(previousList);
        updateConfigValue('meta-api-key', previousList);
        clearCache('meta-api-key');
      } else if (provider === 'claude') {
        setClaudeConfigs(previousList);
        updateConfigValue('claude-api-key', previousList);
        clearCache('claude-api-key');
      } else {
        setVertexConfigs(previousList);
        updateConfigValue('vertex-api-key', previousList);
        clearCache('vertex-api-key');
      }
      showNotification(`${t('notification.update_failed')}: ${message}`, 'error');
    } finally {
      finishConfigMutation(mutation);
    }
  };

  const setOpenAIProviderEnabled = async (index: number, enabled: boolean) => {
    const current = openaiProviders[index];
    if (!current) return;

    const switchingKey = `openai:${current.name}:${index}`;
    const mutation = beginConfigMutation(switchingKey);
    if (!mutation) return;

    const previousList = openaiProviders;
    const nextItem: OpenAIProviderConfig = { ...current, disabled: !enabled };
    const nextList = previousList.map((item, idx) => (idx === index ? nextItem : item));

    setOpenaiProviders(nextList);
    updateConfigValue('openai-compatibility', nextList);
    clearCache('openai-compatibility');

    try {
      await providersApi.updateOpenAIProvider(current.name, index, nextItem, mutation);
      if (!mutation.isCurrent()) return;
      await loadConfigs();
      if (!mutation.isCurrent()) return;
      showNotification(
        enabled ? t('notification.config_enabled') : t('notification.config_disabled'),
        'success'
      );
    } catch (err: unknown) {
      if (!mutation.isCurrent()) return;
      const message = getErrorMessage(err);
      setOpenaiProviders(previousList);
      updateConfigValue('openai-compatibility', previousList);
      clearCache('openai-compatibility');
      showNotification(`${t('notification.update_failed')}: ${message}`, 'error');
    } finally {
      finishConfigMutation(mutation);
    }
  };

  const setProviderWebsocketsEnabled = async (
    provider: 'codex' | 'xai' | 'claude',
    index: number,
    enabled: boolean
  ) => {
    const source =
      provider === 'codex' ? codexConfigs : provider === 'xai' ? xaiConfigs : claudeConfigs;
    const current = source[index];
    if (!current) return;

    const switchingKey = `${provider}:${current.apiKey}:websockets`;
    const mutation = beginConfigMutation(switchingKey);
    if (!mutation) return;

    const previousList = source;
    const nextItem: ProviderKeyConfig = { ...current, websockets: enabled };
    const nextList = previousList.map((item, idx) => (idx === index ? nextItem : item));

    if (provider === 'codex') {
      setCodexConfigs(nextList);
      updateConfigValue('codex-api-key', nextList);
      clearCache('codex-api-key');
    } else if (provider === 'xai') {
      setXAIConfigs(nextList);
      updateConfigValue('xai-api-key', nextList);
      clearCache('xai-api-key');
    } else {
      setClaudeConfigs(nextList);
      updateConfigValue('claude-api-key', nextList);
      clearCache('claude-api-key');
    }

    try {
      if (provider === 'codex') {
        await providersApi.updateCodexConfig(current, nextItem, mutation);
        if (!mutation.isCurrent()) return;
        await loadConfigs();
        if (!mutation.isCurrent()) return;
        showNotification(t('notification.codex_config_updated'), 'success');
      } else if (provider === 'xai') {
        await providersApi.updateXAIConfig(current, nextItem, mutation);
        if (!mutation.isCurrent()) return;
        await loadConfigs();
        if (!mutation.isCurrent()) return;
        showNotification(t('notification.xai_config_updated'), 'success');
      } else {
        await providersApi.updateClaudeConfig(current, nextItem, mutation);
        if (!mutation.isCurrent()) return;
        await loadConfigs();
        if (!mutation.isCurrent()) return;
        showNotification(t('notification.claude_config_updated'), 'success');
      }
    } catch (err: unknown) {
      if (!mutation.isCurrent()) return;
      const message = getErrorMessage(err);
      if (provider === 'codex') {
        setCodexConfigs(previousList);
        updateConfigValue('codex-api-key', previousList);
        clearCache('codex-api-key');
      } else if (provider === 'xai') {
        setXAIConfigs(previousList);
        updateConfigValue('xai-api-key', previousList);
        clearCache('xai-api-key');
      } else {
        setClaudeConfigs(previousList);
        updateConfigValue('claude-api-key', previousList);
        clearCache('claude-api-key');
      }
      showNotification(`${t('notification.update_failed')}: ${message}`, 'error');
    } finally {
      finishConfigMutation(mutation);
    }
  };

  const setProviderCloakEnabled = async (
    provider: 'codex' | 'claude',
    index: number,
    enabled: boolean
  ) => {
    const source = provider === 'codex' ? codexConfigs : claudeConfigs;
    const current = source[index];
    if (!current) return;

    const switchingKey = `${provider}:${current.apiKey}:cloak`;
    const mutation = beginConfigMutation(switchingKey);
    if (!mutation) return;

    const previousList = source;
    const nextItem: ProviderKeyConfig = enabled
      ? { ...current, cloak: current.cloak ?? { ...DEFAULT_CLOAK_CONFIG, sensitiveWords: [] } }
      : { ...current };
    if (!enabled) {
      delete nextItem.cloak;
    }
    const nextList = previousList.map((item, idx) => (idx === index ? nextItem : item));

    if (provider === 'codex') {
      setCodexConfigs(nextList);
      updateConfigValue('codex-api-key', nextList);
      clearCache('codex-api-key');
    } else {
      setClaudeConfigs(nextList);
      updateConfigValue('claude-api-key', nextList);
      clearCache('claude-api-key');
    }

    try {
      if (provider === 'codex') {
        await providersApi.updateCodexConfig(current, nextItem, mutation);
        if (!mutation.isCurrent()) return;
        await loadConfigs();
        if (!mutation.isCurrent()) return;
        showNotification(t('notification.codex_config_updated'), 'success');
      } else {
        await providersApi.updateClaudeConfig(current, nextItem, mutation);
        if (!mutation.isCurrent()) return;
        await loadConfigs();
        if (!mutation.isCurrent()) return;
        showNotification(t('notification.claude_config_updated'), 'success');
      }
    } catch (err: unknown) {
      if (!mutation.isCurrent()) return;
      const message = getErrorMessage(err);
      if (provider === 'codex') {
        setCodexConfigs(previousList);
        updateConfigValue('codex-api-key', previousList);
        clearCache('codex-api-key');
      } else {
        setClaudeConfigs(previousList);
        updateConfigValue('claude-api-key', previousList);
        clearCache('claude-api-key');
      }
      showNotification(`${t('notification.update_failed')}: ${message}`, 'error');
    } finally {
      finishConfigMutation(mutation);
    }
  };

  const setProviderCoolingPolicy = async (
    provider: 'gemini' | 'interactions' | 'codex' | 'xai' | 'meta' | 'claude' | 'vertex' | 'openai',
    index: number,
    policy: CoolingPolicy
  ) => {
    const disableCooling = coolingPolicyToOverride(policy);
    if (provider === 'gemini' || provider === 'interactions') {
      const source = provider === 'gemini' ? geminiKeys : interactionsKeys;
      const current = source[index];
      if (!current) return;

      const switchingKey = `${provider}:${current.apiKey}:disable-cooling`;
      const mutation = beginConfigMutation(switchingKey);
      if (!mutation) return;

      const previousList = source;
      const nextItem: GeminiKeyConfig = { ...current, disableCooling };
      const nextList = previousList.map((item, idx) => (idx === index ? nextItem : item));

      if (provider === 'gemini') {
        setGeminiKeys(nextList);
        updateConfigValue('gemini-api-key', nextList);
        clearCache('gemini-api-key');
      } else {
        setInteractionsKeys(nextList);
        updateConfigValue('interactions-api-key', nextList);
        clearCache('interactions-api-key');
      }

      try {
        if (provider === 'gemini') {
          await providersApi.updateGeminiKey(current, nextItem, mutation);
          if (!mutation.isCurrent()) return;
        } else {
          await providersApi.updateInteractionsKey(current, nextItem, mutation);
          if (!mutation.isCurrent()) return;
        }
        await loadConfigs();
        if (!mutation.isCurrent()) return;
        showNotification(
          t(
            provider === 'gemini'
              ? 'notification.gemini_key_updated'
              : 'notification.interactions_key_updated'
          ),
          'success'
        );
      } catch (err: unknown) {
        if (!mutation.isCurrent()) return;
        const message = getErrorMessage(err);
        if (provider === 'gemini') {
          setGeminiKeys(previousList);
          updateConfigValue('gemini-api-key', previousList);
          clearCache('gemini-api-key');
        } else {
          setInteractionsKeys(previousList);
          updateConfigValue('interactions-api-key', previousList);
          clearCache('interactions-api-key');
        }
        showNotification(`${t('notification.update_failed')}: ${message}`, 'error');
      } finally {
        finishConfigMutation(mutation);
      }
      return;
    }

    if (provider === 'openai') {
      const current = openaiProviders[index];
      if (!current) return;

      const switchingKey = `${provider}:${current.name}:${index}:disable-cooling`;
      const mutation = beginConfigMutation(switchingKey);
      if (!mutation) return;

      const previousList = openaiProviders;
      const nextItem: OpenAIProviderConfig = { ...current, disableCooling };
      const nextList = previousList.map((item, idx) => (idx === index ? nextItem : item));

      setOpenaiProviders(nextList);
      updateConfigValue('openai-compatibility', nextList);
      clearCache('openai-compatibility');

      try {
        await providersApi.updateOpenAIProvider(current.name, index, nextItem, mutation);
        if (!mutation.isCurrent()) return;
        await loadConfigs();
        if (!mutation.isCurrent()) return;
        showNotification(t('notification.openai_provider_updated'), 'success');
      } catch (err: unknown) {
        if (!mutation.isCurrent()) return;
        const message = getErrorMessage(err);
        setOpenaiProviders(previousList);
        updateConfigValue('openai-compatibility', previousList);
        clearCache('openai-compatibility');
        showNotification(`${t('notification.update_failed')}: ${message}`, 'error');
      } finally {
        finishConfigMutation(mutation);
      }
      return;
    }

    const source =
      provider === 'codex'
        ? codexConfigs
        : provider === 'xai'
          ? xaiConfigs
          : provider === 'meta'
            ? metaConfigs
            : provider === 'vertex'
              ? vertexConfigs
              : claudeConfigs;
    const current = source[index];
    if (!current) return;

    const switchingKey = `${provider}:${current.apiKey}:disable-cooling`;
    const mutation = beginConfigMutation(switchingKey);
    if (!mutation) return;

    const previousList = source;
    const nextItem: ProviderKeyConfig = { ...current, disableCooling };
    const nextList = previousList.map((item, idx) => (idx === index ? nextItem : item));

    if (provider === 'codex') {
      setCodexConfigs(nextList);
      updateConfigValue('codex-api-key', nextList);
      clearCache('codex-api-key');
    } else if (provider === 'xai') {
      setXAIConfigs(nextList);
      updateConfigValue('xai-api-key', nextList);
      clearCache('xai-api-key');
    } else if (provider === 'meta') {
      setMetaConfigs(nextList);
      updateConfigValue('meta-api-key', nextList);
      clearCache('meta-api-key');
    } else if (provider === 'vertex') {
      setVertexConfigs(nextList);
      updateConfigValue('vertex-api-key', nextList);
      clearCache('vertex-api-key');
    } else {
      setClaudeConfigs(nextList);
      updateConfigValue('claude-api-key', nextList);
      clearCache('claude-api-key');
    }

    try {
      if (provider === 'codex') {
        await providersApi.updateCodexConfig(current, nextItem, mutation);
        if (!mutation.isCurrent()) return;
        await loadConfigs();
        if (!mutation.isCurrent()) return;
        showNotification(t('notification.codex_config_updated'), 'success');
      } else if (provider === 'xai') {
        await providersApi.updateXAIConfig(current, nextItem, mutation);
        if (!mutation.isCurrent()) return;
        await loadConfigs();
        if (!mutation.isCurrent()) return;
        showNotification(t('notification.xai_config_updated'), 'success');
      } else if (provider === 'meta') {
        await providersApi.updateMetaConfig(current, nextItem, mutation);
        if (!mutation.isCurrent()) return;
        await loadConfigs();
        if (!mutation.isCurrent()) return;
        showNotification(t('notification.meta_config_updated'), 'success');
      } else if (provider === 'vertex') {
        await providersApi.updateVertexConfig(current, nextItem, mutation);
        if (!mutation.isCurrent()) return;
        await loadConfigs();
        if (!mutation.isCurrent()) return;
        showNotification(t('notification.vertex_config_updated'), 'success');
      } else {
        await providersApi.updateClaudeConfig(current, nextItem, mutation);
        if (!mutation.isCurrent()) return;
        await loadConfigs();
        if (!mutation.isCurrent()) return;
        showNotification(t('notification.claude_config_updated'), 'success');
      }
    } catch (err: unknown) {
      if (!mutation.isCurrent()) return;
      const message = getErrorMessage(err);
      if (provider === 'codex') {
        setCodexConfigs(previousList);
        updateConfigValue('codex-api-key', previousList);
        clearCache('codex-api-key');
      } else if (provider === 'xai') {
        setXAIConfigs(previousList);
        updateConfigValue('xai-api-key', previousList);
        clearCache('xai-api-key');
      } else if (provider === 'meta') {
        setMetaConfigs(previousList);
        updateConfigValue('meta-api-key', previousList);
        clearCache('meta-api-key');
      } else if (provider === 'vertex') {
        setVertexConfigs(previousList);
        updateConfigValue('vertex-api-key', previousList);
        clearCache('vertex-api-key');
      } else {
        setClaudeConfigs(previousList);
        updateConfigValue('claude-api-key', previousList);
        clearCache('claude-api-key');
      }
      showNotification(`${t('notification.update_failed')}: ${message}`, 'error');
    } finally {
      finishConfigMutation(mutation);
    }
  };

  const setProviderPriority = async (row: ProviderRow, priority: number) => {
    const nextPriority = Math.trunc(priority);
    const switchingKey = `${row.key}:priority`;

    // 复用页面级切换锁，避免外层快捷优先级与抽屉保存、开关切换并发写入。
    if (row.kind === 'gemini' || row.kind === 'interactions') {
      const source = row.kind === 'gemini' ? geminiKeys : interactionsKeys;
      const current = source[row.originalIndex];
      if (!current || current.priority === nextPriority) return;

      const mutation = beginConfigMutation(switchingKey);

      if (!mutation) return;
      const previousList = source;
      const nextList = previousList.map((item, idx) =>
        idx === row.originalIndex ? { ...item, priority: nextPriority } : item
      );

      if (row.kind === 'gemini') {
        setGeminiKeys(nextList);
        updateConfigValue('gemini-api-key', nextList);
        clearCache('gemini-api-key');
      } else {
        setInteractionsKeys(nextList);
        updateConfigValue('interactions-api-key', nextList);
        clearCache('interactions-api-key');
      }

      try {
        if (row.kind === 'gemini') {
          await providersApi.updateGeminiKey(current, { ...current, priority: nextPriority }, mutation);
          if (!mutation.isCurrent()) return;
        } else {
          await providersApi.updateInteractionsKey(current, { ...current, priority: nextPriority }, mutation);
          if (!mutation.isCurrent()) return;
        }
        await loadConfigs();
        if (!mutation.isCurrent()) return;
        showNotification(
          t(
            row.kind === 'gemini'
              ? 'notification.gemini_key_updated'
              : 'notification.interactions_key_updated'
          ),
          'success'
        );
      } catch (err: unknown) {
        if (!mutation.isCurrent()) return;
        const message = getErrorMessage(err);
        if (row.kind === 'gemini') {
          setGeminiKeys(previousList);
          updateConfigValue('gemini-api-key', previousList);
          clearCache('gemini-api-key');
        } else {
          setInteractionsKeys(previousList);
          updateConfigValue('interactions-api-key', previousList);
          clearCache('interactions-api-key');
        }
        showNotification(`${t('notification.update_failed')}: ${message}`, 'error');
      } finally {
        finishConfigMutation(mutation);
      }
      return;
    }

    if (row.kind === 'openai') {
      const current = openaiProviders[row.originalIndex];
      if (!current || current.priority === nextPriority) return;

      const mutation = beginConfigMutation(switchingKey);

      if (!mutation) return;
      const previousList = openaiProviders;
      const nextList = previousList.map((item, idx) =>
        idx === row.originalIndex ? { ...item, priority: nextPriority } : item
      );

      setOpenaiProviders(nextList);
      updateConfigValue('openai-compatibility', nextList);
      clearCache('openai-compatibility');

      try {
        await providersApi.updateOpenAIProvider(current.name, row.originalIndex, {
          ...current,
          priority: nextPriority,
        }, mutation);
        if (!mutation.isCurrent()) return;
        await loadConfigs();
        if (!mutation.isCurrent()) return;
        showNotification(t('notification.openai_provider_updated'), 'success');
      } catch (err: unknown) {
        if (!mutation.isCurrent()) return;
        const message = getErrorMessage(err);
        setOpenaiProviders(previousList);
        updateConfigValue('openai-compatibility', previousList);
        clearCache('openai-compatibility');
        showNotification(`${t('notification.update_failed')}: ${message}`, 'error');
      } finally {
        finishConfigMutation(mutation);
      }
      return;
    }

    const source =
      row.kind === 'codex'
        ? codexConfigs
        : row.kind === 'xai'
          ? xaiConfigs
          : row.kind === 'meta'
            ? metaConfigs
            : row.kind === 'claude'
              ? claudeConfigs
              : vertexConfigs;
    const current = source[row.originalIndex];
    if (!current || current.priority === nextPriority) return;

    const mutation = beginConfigMutation(switchingKey);

    if (!mutation) return;
    const previousList = source;
    const nextList = previousList.map((item, idx) =>
      idx === row.originalIndex ? { ...item, priority: nextPriority } : item
    );

    if (row.kind === 'codex') {
      setCodexConfigs(nextList);
      updateConfigValue('codex-api-key', nextList);
      clearCache('codex-api-key');
    } else if (row.kind === 'xai') {
      setXAIConfigs(nextList);
      updateConfigValue('xai-api-key', nextList);
      clearCache('xai-api-key');
    } else if (row.kind === 'meta') {
      setMetaConfigs(nextList);
      updateConfigValue('meta-api-key', nextList);
      clearCache('meta-api-key');
    } else if (row.kind === 'claude') {
      setClaudeConfigs(nextList);
      updateConfigValue('claude-api-key', nextList);
      clearCache('claude-api-key');
    } else {
      setVertexConfigs(nextList);
      updateConfigValue('vertex-api-key', nextList);
      clearCache('vertex-api-key');
    }

    try {
      if (row.kind === 'codex') {
        await providersApi.updateCodexConfig(current, { ...current, priority: nextPriority }, mutation);
        if (!mutation.isCurrent()) return;
        await loadConfigs();
        if (!mutation.isCurrent()) return;
        showNotification(t('notification.codex_config_updated'), 'success');
      } else if (row.kind === 'xai') {
        await providersApi.updateXAIConfig(current, { ...current, priority: nextPriority }, mutation);
        if (!mutation.isCurrent()) return;
        await loadConfigs();
        if (!mutation.isCurrent()) return;
        showNotification(t('notification.xai_config_updated'), 'success');
      } else if (row.kind === 'meta') {
        await providersApi.updateMetaConfig(current, { ...current, priority: nextPriority }, mutation);
        if (!mutation.isCurrent()) return;
        await loadConfigs();
        if (!mutation.isCurrent()) return;
        showNotification(t('notification.meta_config_updated'), 'success');
      } else if (row.kind === 'claude') {
        await providersApi.updateClaudeConfig(current, { ...current, priority: nextPriority }, mutation);
        if (!mutation.isCurrent()) return;
        await loadConfigs();
        if (!mutation.isCurrent()) return;
        showNotification(t('notification.claude_config_updated'), 'success');
      } else {
        await providersApi.updateVertexConfig(current, { ...current, priority: nextPriority }, mutation);
        if (!mutation.isCurrent()) return;
        await loadConfigs();
        if (!mutation.isCurrent()) return;
        showNotification(t('notification.vertex_config_updated'), 'success');
      }
    } catch (err: unknown) {
      if (!mutation.isCurrent()) return;
      const message = getErrorMessage(err);
      if (row.kind === 'codex') {
        setCodexConfigs(previousList);
        updateConfigValue('codex-api-key', previousList);
        clearCache('codex-api-key');
      } else if (row.kind === 'xai') {
        setXAIConfigs(previousList);
        updateConfigValue('xai-api-key', previousList);
        clearCache('xai-api-key');
      } else if (row.kind === 'meta') {
        setMetaConfigs(previousList);
        updateConfigValue('meta-api-key', previousList);
        clearCache('meta-api-key');
      } else if (row.kind === 'claude') {
        setClaudeConfigs(previousList);
        updateConfigValue('claude-api-key', previousList);
        clearCache('claude-api-key');
      } else {
        setVertexConfigs(previousList);
        updateConfigValue('vertex-api-key', previousList);
        clearCache('vertex-api-key');
      }
      showNotification(`${t('notification.update_failed')}: ${message}`, 'error');
    } finally {
      finishConfigMutation(mutation);
    }
  };

  // 删除（按 provider 分派，沿用既有 API 契约）
  const deleteGemini = (index: number, target: string) => {
    const entry = geminiKeys[index];
    if (!entry || !matchesView()) return;
    const generation = viewGeneration.current;
    const revision = configRequestId.current;
    showConfirmation({
      title: t('ai_providers.gemini_delete_title', { defaultValue: 'Delete Gemini Key' }),
      message: t('ai_providers.gemini_delete_confirm'),
      variant: 'danger',
      confirmText: t('common.next'),
      secondConfirmation: buildProviderDeleteSecondConfirmation(
        t,
        PROVIDER_KIND_LABELS.gemini,
        target
      ),
      onConfirm: async () => {
        if (generation !== viewGeneration.current || revision !== configRequestId.current) return;
        const mutation = beginConfigMutation('delete');
        if (!mutation) return;
        try {
          await providersApi.deleteGeminiKey(entry.apiKey, entry.baseUrl, mutation);
          if (!mutation.isCurrent()) return;
          const next = geminiKeys.filter((_, idx) => idx !== index);
          setGeminiKeys(next);
          updateConfigValue('gemini-api-key', next);
          clearCache('gemini-api-key');
          showNotification(t('notification.gemini_key_deleted'), 'success');
        } catch (err: unknown) {
          if (!mutation.isCurrent()) return;
          const message = getErrorMessage(err);
          showNotification(`${t('notification.delete_failed')}: ${message}`, 'error');
        } finally {
          finishConfigMutation(mutation);
        }
      },
    });
  };

  const deleteInteractions = (index: number, target: string) => {
    const entry = interactionsKeys[index];
    if (!entry || !matchesView()) return;
    const generation = viewGeneration.current;
    const revision = configRequestId.current;
    showConfirmation({
      title: t('ai_providers.interactions_delete_title'),
      message: t('ai_providers.interactions_delete_confirm'),
      variant: 'danger',
      confirmText: t('common.next'),
      secondConfirmation: buildProviderDeleteSecondConfirmation(
        t,
        PROVIDER_KIND_LABELS.interactions,
        target
      ),
      onConfirm: async () => {
        if (generation !== viewGeneration.current || revision !== configRequestId.current) return;
        const mutation = beginConfigMutation('delete');
        if (!mutation) return;
        try {
          await providersApi.deleteInteractionsKey(entry.apiKey, entry.baseUrl, mutation);
          if (!mutation.isCurrent()) return;
          const next = interactionsKeys.filter((_, idx) => idx !== index);
          setInteractionsKeys(next);
          updateConfigValue('interactions-api-key', next);
          clearCache('interactions-api-key');
          showNotification(t('notification.interactions_key_deleted'), 'success');
        } catch (err: unknown) {
          if (!mutation.isCurrent()) return;
          const message = getErrorMessage(err);
          showNotification(`${t('notification.delete_failed')}: ${message}`, 'error');
        } finally {
          finishConfigMutation(mutation);
        }
      },
    });
  };

  const deleteProviderEntry = (
    type: 'codex' | 'xai' | 'claude' | 'meta',
    index: number,
    target: string
  ) => {
    const source =
      type === 'codex'
        ? codexConfigs
        : type === 'xai'
          ? xaiConfigs
          : type === 'meta'
            ? metaConfigs
            : claudeConfigs;
    const entry = source[index];
    if (!entry || !matchesView()) return;
    const generation = viewGeneration.current;
    const revision = configRequestId.current;
    showConfirmation({
      title: t(`ai_providers.${type}_delete_title`, {
        defaultValue: `Delete ${
          type === 'codex' ? 'Codex' : type === 'xai' ? 'xAI' : type === 'meta' ? 'Meta' : 'Claude'
        } Config`,
      }),
      message: t(`ai_providers.${type}_delete_confirm`),
      variant: 'danger',
      confirmText: t('common.next'),
      secondConfirmation: buildProviderDeleteSecondConfirmation(
        t,
        PROVIDER_KIND_LABELS[type],
        target
      ),
      onConfirm: async () => {
        if (generation !== viewGeneration.current || revision !== configRequestId.current) return;
        const mutation = beginConfigMutation('delete');
        if (!mutation) return;
        try {
          if (type === 'codex') {
            await providersApi.deleteCodexConfig(entry.apiKey, entry.baseUrl, mutation);
            if (!mutation.isCurrent()) return;
            const next = codexConfigs.filter((_, idx) => idx !== index);
            setCodexConfigs(next);
            updateConfigValue('codex-api-key', next);
            clearCache('codex-api-key');
            showNotification(t('notification.codex_config_deleted'), 'success');
          } else if (type === 'xai') {
            await providersApi.deleteXAIConfig(entry.apiKey, entry.baseUrl, mutation);
            if (!mutation.isCurrent()) return;
            const next = xaiConfigs.filter((_, idx) => idx !== index);
            setXAIConfigs(next);
            updateConfigValue('xai-api-key', next);
            clearCache('xai-api-key');
            showNotification(t('notification.xai_config_deleted'), 'success');
          } else if (type === 'meta') {
            await providersApi.deleteMetaConfig(entry.apiKey, entry.baseUrl, mutation);
            if (!mutation.isCurrent()) return;
            const next = metaConfigs.filter((_, idx) => idx !== index);
            setMetaConfigs(next);
            updateConfigValue('meta-api-key', next);
            clearCache('meta-api-key');
            showNotification(t('notification.meta_config_deleted'), 'success');
          } else {
            await providersApi.deleteClaudeConfig(entry.apiKey, entry.baseUrl, mutation);
            if (!mutation.isCurrent()) return;
            const next = claudeConfigs.filter((_, idx) => idx !== index);
            setClaudeConfigs(next);
            updateConfigValue('claude-api-key', next);
            clearCache('claude-api-key');
            showNotification(t('notification.claude_config_deleted'), 'success');
          }
        } catch (err: unknown) {
          if (!mutation.isCurrent()) return;
          const message = getErrorMessage(err);
          showNotification(`${t('notification.delete_failed')}: ${message}`, 'error');
        } finally {
          finishConfigMutation(mutation);
        }
      },
    });
  };

  const deleteVertex = (index: number, target: string) => {
    const entry = vertexConfigs[index];
    if (!entry || !matchesView()) return;
    const generation = viewGeneration.current;
    const revision = configRequestId.current;
    showConfirmation({
      title: t('ai_providers.vertex_delete_title', { defaultValue: 'Delete Vertex Config' }),
      message: t('ai_providers.vertex_delete_confirm'),
      variant: 'danger',
      confirmText: t('common.next'),
      secondConfirmation: buildProviderDeleteSecondConfirmation(
        t,
        PROVIDER_KIND_LABELS.vertex,
        target
      ),
      onConfirm: async () => {
        if (generation !== viewGeneration.current || revision !== configRequestId.current) return;
        const mutation = beginConfigMutation('delete');
        if (!mutation) return;
        try {
          await providersApi.deleteVertexConfig(entry.apiKey, entry.baseUrl, mutation);
          if (!mutation.isCurrent()) return;
          const next = vertexConfigs.filter((_, idx) => idx !== index);
          setVertexConfigs(next);
          updateConfigValue('vertex-api-key', next);
          clearCache('vertex-api-key');
          showNotification(t('notification.vertex_config_deleted'), 'success');
        } catch (err: unknown) {
          if (!mutation.isCurrent()) return;
          const message = getErrorMessage(err);
          showNotification(`${t('notification.delete_failed')}: ${message}`, 'error');
        } finally {
          finishConfigMutation(mutation);
        }
      },
    });
  };

  const deleteOpenai = (index: number, target: string) => {
    const entry = openaiProviders[index];
    if (!entry || !matchesView()) return;
    const generation = viewGeneration.current;
    const revision = configRequestId.current;
    showConfirmation({
      title: t('ai_providers.openai_delete_title', { defaultValue: 'Delete OpenAI Provider' }),
      message: t('ai_providers.openai_delete_confirm'),
      variant: 'danger',
      confirmText: t('common.next'),
      secondConfirmation: buildProviderDeleteSecondConfirmation(
        t,
        PROVIDER_KIND_LABELS.openai,
        target
      ),
      onConfirm: async () => {
        if (generation !== viewGeneration.current || revision !== configRequestId.current) return;
        const mutation = beginConfigMutation('delete');
        if (!mutation) return;
        try {
          await providersApi.deleteOpenAIProvider(entry.name, mutation);
          if (!mutation.isCurrent()) return;
          const next = openaiProviders.filter((_, idx) => idx !== index);
          setOpenaiProviders(next);
          updateConfigValue('openai-compatibility', next);
          clearCache('openai-compatibility');
          showNotification(t('notification.openai_provider_deleted'), 'success');
        } catch (err: unknown) {
          if (!mutation.isCurrent()) return;
          const message = getErrorMessage(err);
          showNotification(`${t('notification.delete_failed')}: ${message}`, 'error');
        } finally {
          finishConfigMutation(mutation);
        }
      },
    });
  };

  // 行级回调分派
  const handleRowToggle = (row: ProviderRow, enabled: boolean) => {
    if (row.kind === 'openai') {
      void setOpenAIProviderEnabled(row.originalIndex, enabled);
    } else {
      void setConfigEnabled(row.kind, row.originalIndex, enabled);
    }
  };

  const handleRowWebsocketsToggle = (row: ProviderRow, enabled: boolean) => {
    if (row.kind !== 'codex' && row.kind !== 'xai' && row.kind !== 'claude') return;
    void setProviderWebsocketsEnabled(row.kind, row.originalIndex, enabled);
  };

  const handleRowCloakToggle = (row: ProviderRow, enabled: boolean) => {
    if (row.kind !== 'codex' && row.kind !== 'claude') return;
    void setProviderCloakEnabled(row.kind, row.originalIndex, enabled);
  };

  const handleRowCoolingPolicyChange = (row: ProviderRow, policy: CoolingPolicy) => {
    if (
      row.kind !== 'gemini' &&
      row.kind !== 'interactions' &&
      row.kind !== 'codex' &&
      row.kind !== 'xai' &&
      row.kind !== 'meta' &&
      row.kind !== 'claude' &&
      row.kind !== 'vertex' &&
      row.kind !== 'openai'
    ) {
      return;
    }
    void setProviderCoolingPolicy(row.kind, row.originalIndex, policy);
  };

  const handleRowPriorityChange = (row: ProviderRow, priority: number) => {
    void setProviderPriority(row, priority);
  };

  const handleRowEdit = (row: ProviderRow) => {
    setDetailRowKey(null);
    openEditorDrawer(row.kind, row.originalIndex);
  };

  const handleRowDelete = (row: ProviderRow) => {
    setDetailRowKey(null);
    const target = row.label || row.sortName || row.baseUrl;
    if (row.kind === 'gemini') {
      deleteGemini(row.originalIndex, target);
    } else if (row.kind === 'interactions') {
      deleteInteractions(row.originalIndex, target);
    } else if (
      row.kind === 'codex' ||
      row.kind === 'xai' ||
      row.kind === 'claude' ||
      row.kind === 'meta'
    ) {
      deleteProviderEntry(row.kind, row.originalIndex, target);
    } else if (row.kind === 'vertex') {
      deleteVertex(row.originalIndex, target);
    } else {
      deleteOpenai(row.originalIndex, target);
    }
  };

  const handleAdd = (kind: ProviderKind) => {
    openEditorDrawer(kind, null);
  };

  const handlePageSizeChange = (value: string) => {
    const nextSize = Number.parseInt(value, 10);
    if (!PROVIDER_TABLE_PAGE_SIZE_OPTIONS.includes(nextSize as (typeof PROVIDER_TABLE_PAGE_SIZE_OPTIONS)[number])) {
      return;
    }
    writeListPageSize('aiProviders', nextSize, PROVIDER_TABLE_PAGE_SIZE_OPTIONS);
    setPageSize(nextSize);
    setPage(1);
  };

  const emptyState =
    rows.length > 0 && kindFilter !== 'all' && kindCounts[kindFilter] === 0 ? (
      // 当前类型尚无配置：直接给“添加该类型配置”入口，避免“清除筛选”死胡同
      <EmptyState
        title={t('ai_providers.kind_empty_title', { name: PROVIDER_KIND_LABELS[kindFilter] })}
        action={
          <Button size="sm" onClick={() => handleAdd(kindFilter)} disabled={actionsDisabled}>
            {t('ai_providers.add_kind_button', { name: PROVIDER_KIND_LABELS[kindFilter] })}
          </Button>
        }
      />
    ) : rows.length > 0 && filtersActive ? (
      <EmptyState
        title={t('ai_providers.table_filtered_empty_title')}
        description={t('ai_providers.table_filtered_empty_desc')}
        action={
          <Button variant="secondary" size="sm" onClick={clearFilters} disabled={actionsDisabled}>
            {t('ai_providers.clear_filters')}
          </Button>
        }
      />
    ) : (
      <EmptyState
        title={t('ai_providers.table_empty_title')}
        description={t('ai_providers.table_empty_desc')}
      />
    );

  return (
    <div className={styles.container}>
      <div className={styles.content}>
        {error && <div className="error-box">{error}</div>}

        <div>
          <ProviderToolbar
            kind={kindFilter}
            kindCounts={kindCounts}
            onKindChange={setKindFilter}
            searchText={searchText}
            onSearchTextChange={setSearchText}
            allModelNames={allModelNames}
            selectedModels={selectedModels}
            onSelectedModelsChange={setSelectedModels}
            sortOption={sortOption}
            onSortOptionChange={setSortOption}
            sortDirection={sortDirection}
            onSortDirectionChange={setSortDirection}
            disabled={actionsDisabled}
            resolvedTheme={resolvedTheme}
            onAdd={handleAdd}
            onHealthCheck={() => setHealthCheckOpen(true)}
            healthCheckDisabled={visibleRows.length === 0}
          />

          {policyMigration?.protectionHold && (
            <div className="hint" role="status" aria-live="polite">
              {t('ai_providers.policy_migration_hold', {
                count: policyMigration.pendingCount,
                reason: policyMigration.reason || 'migration_pending_rows',
              })}
            </div>
          )}
          <Card>
            <ProviderTable
              autoStates={providerAutoStates}
              moneyStatus={{
                base: featureAvailability.managerServiceBase,
                managementKey: balanceManagementKey,
                disabled: !featureAvailability.managerServiceAvailable,
                bindingForRow: (row) => forProvider(row, rows).binding,
                bindingsSettled: resourceProtection !== 'unknown',
              }}
              resourceProtection={resourceProtection}
              resourceProtectionForRow={(row) => forProvider(row, rows)}
              providerPolicyConnection={providerPolicyConnection}
              rows={balancesReady ? pagedRows : []}
              loading={loading || !balancesReady}
              actionsDisabled={actionsDisabled}
              toggleDisabled={actionsDisabled}
              resolvedTheme={resolvedTheme}
              emptyState={emptyState}
              onShowDetail={(row) => {
                setDetailContent('credential');
                setDetailRowKey(row.key);
              }}
              onShowBalance={(row) => {
                setDetailContent('balance');
                setDetailRowKey(row.key);
              }}
              onEdit={handleRowEdit}
              onDelete={handleRowDelete}
              onToggle={handleRowToggle}
              onPriorityChange={handleRowPriorityChange}
            />
            {balancesReady && visibleRows.length > 0 &&
              (visibleRows.length > PROVIDER_TABLE_DEFAULT_PAGE_SIZE ||
                pageSize !== PROVIDER_TABLE_DEFAULT_PAGE_SIZE) && (
                <div className={styles.paginationBar}>
                  <div className={styles.paginationInfo}>
                    {t('monitoring.pagination_info', {
                      current: currentPage,
                      total: totalPages,
                      start: pageStartItem,
                      end: pageEndItem,
                      count: visibleRows.length,
                    })}
                  </div>
                  <div className={styles.paginationControls}>
                    <div className={styles.pageSizeField}>
                      <span>{t('monitoring.page_size_label')}</span>
                      <Select
                        value={String(pageSize)}
                        options={PROVIDER_TABLE_PAGE_SIZE_OPTIONS.map((size) => ({
                          value: String(size),
                          label: t('monitoring.page_size_option', { count: size }),
                        }))}
                        onChange={handlePageSizeChange}
                        disabled={loading}
                        fullWidth={false}
                        ariaLabel={t('monitoring.page_size_label')}
                        className={styles.pageSizeSelect}
                        triggerClassName={styles.pageSizeSelectTrigger}
                      />
                    </div>
                    <Button
                      variant="secondary"
                      size="xs"
                      onClick={() => setPage(Math.max(1, currentPage - 1))}
                      disabled={loading || currentPage <= 1}
                    >
                      {t('monitoring.pagination_prev')}
                    </Button>
                    <Button
                      variant="secondary"
                      size="xs"
                      onClick={() => setPage(Math.min(totalPages, currentPage + 1))}
                      disabled={loading || currentPage >= totalPages}
                    >
                      {t('monitoring.pagination_next')}
                    </Button>
                  </div>
                </div>
              )}
          </Card>
        </div>
      </div>

      <ProviderDetailDrawer
        content={detailContent}
        autoState={detailAutoState}
        officialBalance={{ base: featureAvailability.managerServiceBase, managementKey: balanceManagementKey, binding: detailRow ? forProvider(detailRow, rows).binding : undefined, disabled: !featureAvailability.managerServiceAvailable }}
        providerPolicy={providerPolicyConnection}
        row={detailRow}
        open={detailRowKey !== null}
        usageByProvider={usageByProvider}
        resolvedTheme={resolvedTheme}
        actionsDisabled={actionsDisabled}
        toggleDisabled={actionsDisabled}
        onClose={() => setDetailRowKey(null)}
        onEdit={handleRowEdit}
        onDelete={handleRowDelete}
        onToggle={handleRowToggle}
        onToggleWebsockets={handleRowWebsocketsToggle}
        onToggleCloak={handleRowCloakToggle}
        onToggleDisableCooling={handleRowCoolingPolicyChange}
      />
      <ProviderHealthCheckDrawer
        open={healthCheckOpen}
        rows={visibleRows}
        actionsDisabled={actionsDisabled}
        onClose={() => setHealthCheckOpen(false)}
        onApplyResultActions={applyProviderEnabledActions}
        onSetProviderEnabled={setHealthCheckProviderEnabled}
      />
      <GeminiEditDrawer
        open={editDrawerKind === 'gemini'}
        editIndex={editDrawerIndex}
        disabled={actionsDisabled}
        onClose={closeEditorDrawer}
        onSaved={handleDrawerSaved}
      />
      <GeminiEditDrawer
        open={editDrawerKind === 'interactions'}
        editIndex={editDrawerIndex}
        disabled={actionsDisabled}
        onClose={closeEditorDrawer}
        onSaved={handleDrawerSaved}
        providerKind="interactions"
      />
      <CodexEditDrawer
        open={editDrawerKind === 'codex'}
        editIndex={editDrawerIndex}
        disabled={actionsDisabled}
        onClose={closeEditorDrawer}
        onSaved={handleDrawerSaved}
      />
      <CodexEditDrawer
        open={editDrawerKind === 'xai'}
        editIndex={editDrawerIndex}
        disabled={actionsDisabled}
        onClose={closeEditorDrawer}
        onSaved={handleDrawerSaved}
        providerKind="xai"
      />
      <CodexEditDrawer
        open={editDrawerKind === 'meta'}
        editIndex={editDrawerIndex}
        disabled={actionsDisabled}
        onClose={closeEditorDrawer}
        onSaved={handleDrawerSaved}
        providerKind="meta"
      />
      <VertexEditDrawer
        open={editDrawerKind === 'vertex'}
        editIndex={editDrawerIndex}
        disabled={actionsDisabled}
        onClose={closeEditorDrawer}
        onSaved={handleDrawerSaved}
      />
      <ClaudeEditDrawer
        open={editDrawerKind === 'claude'}
        editIndex={editDrawerIndex}
        disabled={actionsDisabled}
        onClose={closeEditorDrawer}
        onSaved={handleDrawerSaved}
      />
      <OpenAIEditDrawer
        open={editDrawerKind === 'openai'}
        editIndex={editDrawerIndex}
        disabled={actionsDisabled}
        onClose={closeEditorDrawer}
        onSaved={handleDrawerSaved}
      />
    </div>
  );
}
