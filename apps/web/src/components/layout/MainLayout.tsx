import {
  getMonitoringWorkspaceLanding,
  getUsageWorkspaceLanding,
  isMonitoringWorkspacePath,
  isUsageWorkspacePath,
} from '@/components/layout/usageMonitoringNavigation';
import {
  ReactNode,
  SVGProps,
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from 'react';
import { Link, useLocation, useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { PageTransition } from '@/components/common/PageTransition';
import { DatabaseMaintenanceBanner } from '@/components/common/DatabaseMaintenanceBanner';
import { DatabaseMaintenanceProvider } from '@/components/common/DatabaseMaintenanceContext';
import { MainRoutes } from '@/router/MainRoutes';
import { RouteTabs } from './RouteTabs';
import { routeTabPanel } from './routeTabPanel';
import { dashboardNavigation, getDashboardTab } from './dashboardNavigation';
import {
  accessNavigation,
  accountsTabTarget,
  getAccessTab,
  useAccessAccountSearch,
} from './accessNavigation';
import {
  IconGithub,
  IconSidebarAuthFiles,
  IconSidebarDashboard,
  IconSidebarMonitor,
  IconSidebarPlugins,
  IconSidebarProviders,
  IconSidebarUsage,
} from '@/components/ui/icons';
import {
  CPAMP_SYMBOL_COLOR_PNG_URL,
  CPAMP_WORDMARK_COLOR_PNG_URL,
  CPAMP_WORDMARK_ON_DARK_PNG_URL,
} from '@/assets/brand';
import {
  useAuthStore,
  useConfigStore,
  useLanguageStore,
  useNotificationStore,
  useThemeStore,
  useVisualEffectsStore,
} from '@/stores';
import { pluginsApi } from '@/services/api';
import {
  getPluginManagementTab,
  getPluginWorkspaceRoute,
  pluginManagementTarget,
  pluginNavigation,
} from '@/features/plugins/pluginNavigation';
import {
  collectPluginResourceNavEntries,
  isPluginManagementNavVisible,
  isPluginResourceNavVisible,
  PLUGIN_RESOURCES_REFRESH_EVENT,
  type PluginResourceEntry,
} from '@/features/plugins/pluginResources';
import { triggerHeaderRefresh } from '@/hooks/useHeaderRefresh';
import { usePanelFeatureAvailability } from '@/hooks/usePanelFeatureAvailability';
import { getDemoLogoutPath, prefixRouteBase, stripRouteBase } from '@/features/demo/demoMode';
import { LANGUAGE_LABEL_KEYS, LANGUAGE_ORDER } from '@/utils/constants';
import { isSupportedLanguage } from '@/utils/language';
import type { Theme, VisualEffectsMode } from '@/types';

const NAV_ICON_SIZE = 16;
const GITHUB_REPOSITORY_URL = 'https://github.com/williamxhero/CPA-Manager-Plus-ex';

const navigationIcons: Record<string, ReactNode> = {
  dashboard: <IconSidebarDashboard size={NAV_ICON_SIZE} />,
  aiProviders: <IconSidebarProviders size={NAV_ICON_SIZE} />,
  authFiles: <IconSidebarAuthFiles size={NAV_ICON_SIZE} />,
  usageAnalytics: <IconSidebarUsage size={NAV_ICON_SIZE} />,
  monitoring: <IconSidebarMonitor size={NAV_ICON_SIZE} />,
  plugins: <IconSidebarPlugins size={NAV_ICON_SIZE} />,
};

// Header action icons - smaller size for header buttons
const headerIconProps: SVGProps<SVGSVGElement> = {
  width: 16,
  height: 16,
  viewBox: '0 0 24 24',
  fill: 'none',
  stroke: 'currentColor',
  strokeWidth: 2,
  strokeLinecap: 'round',
  strokeLinejoin: 'round',
  'aria-hidden': 'true',
  focusable: 'false',
};

const headerIcons = {
  refresh: (
    <svg {...headerIconProps}>
      <path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8" />
      <path d="M21 3v5h-5" />
    </svg>
  ),
  language: (
    <svg {...headerIconProps}>
      <circle cx="12" cy="12" r="10" />
      <path d="M2 12h20" />
      <path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z" />
    </svg>
  ),
  sun: (
    <svg {...headerIconProps}>
      <circle cx="12" cy="12" r="4" />
      <path d="M12 2v2" />
      <path d="M12 20v2" />
      <path d="m4.93 4.93 1.41 1.41" />
      <path d="m17.66 17.66 1.41 1.41" />
      <path d="M2 12h2" />
      <path d="M20 12h2" />
      <path d="m6.34 17.66-1.41 1.41" />
      <path d="m19.07 4.93-1.41 1.41" />
    </svg>
  ),
  moon: (
    <svg {...headerIconProps}>
      <path d="M12 3a6 6 0 0 0 9 9 9 9 0 1 1-9-9z" />
    </svg>
  ),
  autoTheme: (
    <svg {...headerIconProps}>
      <rect x="4" y="5" width="16" height="11" rx="2" />
      <path d="M8 21h8" />
      <path d="M12 16v5" />
      <path d="M9 11a3 3 0 0 1 5.2-2" />
      <path d="M14.5 7v2h-2" />
      <path d="M15 11a3 3 0 0 1-5.2 2" />
      <path d="M9.5 15v-2h2" />
    </svg>
  ),
  visualEffectsFull: (
    <svg {...headerIconProps}>
      <path d="m12 3 1.85 5.15L19 10l-5.15 1.85L12 17l-1.85-5.15L5 10l5.15-1.85L12 3z" />
      <path d="M5 3v4" />
      <path d="M3 5h4" />
      <path d="M19 17v4" />
      <path d="M17 19h4" />
    </svg>
  ),
  visualEffectsReduced: (
    <svg {...headerIconProps}>
      <path d="M4 14a8 8 0 0 1 16 0" />
      <path d="M12 14l4-5" />
      <path d="M8 14h8" />
      <path d="M5 19h14" />
    </svg>
  ),
  logout: (
    <svg {...headerIconProps}>
      <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4" />
      <path d="m16 17 5-5-5-5" />
      <path d="M21 12H9" />
    </svg>
  ),
};

const THEME_OPTIONS: Array<{
  key: Theme;
  labelKey: string;
  icon: ReactNode;
}> = [
  { key: 'auto', labelKey: 'theme.auto', icon: headerIcons.autoTheme },
  { key: 'white', labelKey: 'theme.white', icon: headerIcons.sun },
  { key: 'dark', labelKey: 'theme.dark', icon: headerIcons.moon },
];

const VISUAL_EFFECTS_OPTIONS: Array<{
  key: VisualEffectsMode;
  labelKey: string;
  icon: ReactNode;
}> = [
  { key: 'full', labelKey: 'visual_effects.full', icon: headerIcons.visualEffectsFull },
  {
    key: 'reduced',
    labelKey: 'visual_effects.reduced',
    icon: headerIcons.visualEffectsReduced,
  },
];

type NavItem = {
  path: string;
  label: string;
  icon: ReactNode;
};

interface MainLayoutProps {
  routeBase?: string;
  demoMode?: boolean;
}

function MainLayoutContent({ routeBase = '', demoMode = false }: MainLayoutProps = {}) {
  const { t } = useTranslation();
  const { showNotification } = useNotificationStore();
  const location = useLocation();
  const navigate = useNavigate();
  const routePathname = stripRouteBase(location.pathname, routeBase);
  const dashboardTab = getDashboardTab(routePathname);

  const logout = useAuthStore((state) => state.logout);
  const connectionStatus = useAuthStore((state) => state.connectionStatus);
  const apiBase = useAuthStore((state) => state.apiBase);
  const supportsPlugin = useAuthStore((state) => state.supportsPlugin);
  const managementKey = useAuthStore((state) => state.managementKey);
  const accessTab = getAccessTab(routePathname, location.search);
  const accountsSearch = useAccessAccountSearch(
    routePathname,
    location.search,
    JSON.stringify([connectionStatus === 'connected', apiBase, managementKey])
  );

  const config = useConfigStore((state) => state.config);
  const fetchConfig = useConfigStore((state) => state.fetchConfig);
  const clearCache = useConfigStore((state) => state.clearCache);
  const featureAvailability = usePanelFeatureAvailability();

  const theme = useThemeStore((state) => state.theme);
  const setTheme = useThemeStore((state) => state.setTheme);
  const visualEffectsMode = useVisualEffectsStore((state) => state.mode);
  const setVisualEffectsMode = useVisualEffectsStore((state) => state.setMode);
  const language = useLanguageStore((state) => state.language);
  const setLanguage = useLanguageStore((state) => state.setLanguage);

  const [languageMenuOpen, setLanguageMenuOpen] = useState(false);
  const [themeMenuOpen, setThemeMenuOpen] = useState(false);
  const [visualEffectsMenuOpen, setVisualEffectsMenuOpen] = useState(false);
  const [pluginResources, setPluginResources] = useState<{
    scope: string;
    entries: PluginResourceEntry[];
  }>();
  const [pluginConnectionRevision, setPluginConnectionRevision] = useState(0);
  const pluginResourcesRequest = useRef(0);
  const contentRef = useRef<HTMLDivElement | null>(null);
  const languageMenuRef = useRef<HTMLDivElement | null>(null);
  const themeMenuRef = useRef<HTMLDivElement | null>(null);
  const visualEffectsMenuRef = useRef<HTMLDivElement | null>(null);
  const navigationRef = useRef<HTMLElement | null>(null);
  const headerRef = useRef<HTMLElement | null>(null);

  const fullBrandName = 'CPA Manager Plus';
  const isLogsPage = routePathname.startsWith('/logs');
  const pluginWorkspaceRoute = getPluginWorkspaceRoute(routePathname);
  const isPluginResourcePage = pluginWorkspaceRoute === 'resource';
  const configPluginsEnabled = config?.pluginsEnabled;
  const pluginControlMenuVisible = isPluginManagementNavVisible({
    supportsPlugin,
    pluginsEnabled: configPluginsEnabled,
  });

  const pluginWorkspaceVisible = Boolean(pluginWorkspaceRoute) && pluginControlMenuVisible;
  const pluginScope = JSON.stringify([apiBase, managementKey]);
  const pluginResourceTabs =
    pluginWorkspaceVisible &&
    connectionStatus === 'connected' &&
    pluginResources?.scope === pluginScope
      ? pluginResources.entries.map(({ route, label }) => ({ id: route, label, to: route }))
      : [];
  const pluginSearch = pluginWorkspaceRoute === 'management' ? location.search : '';
  const pluginHash = pluginWorkspaceRoute === 'management' ? location.hash : '';
  const pluginTab =
    pluginWorkspaceRoute === 'resource'
      ? pluginResourceTabs.find(({ to }) => to === routePathname.replace(/\/+$/, ''))?.id
      : pluginWorkspaceRoute === 'store'
        ? 'store'
        : getPluginManagementTab(pluginSearch);

  // 将顶部悬浮控制区高度写入 CSS 变量，供移动端粘性元素和浮层避让。
  useLayoutEffect(() => {
    const updateHeaderHeight = () => {
      const height = headerRef.current?.offsetHeight;
      if (height) {
        document.documentElement.style.setProperty('--header-height', `${height}px`);
      }
    };

    updateHeaderHeight();

    const resizeObserver =
      typeof ResizeObserver !== 'undefined' && headerRef.current
        ? new ResizeObserver(updateHeaderHeight)
        : null;
    if (resizeObserver && headerRef.current) {
      resizeObserver.observe(headerRef.current);
    }

    window.addEventListener('resize', updateHeaderHeight);

    return () => {
      if (resizeObserver) {
        resizeObserver.disconnect();
      }
      window.removeEventListener('resize', updateHeaderHeight);
    };
  }, []);

  // 将主内容区的中心点写入 CSS 变量，供底部浮层（配置面板操作栏、提供商导航）对齐到内容区
  useLayoutEffect(() => {
    const updateContentCenter = () => {
      const el = contentRef.current;
      if (!el) return;
      const rect = el.getBoundingClientRect();
      const centerX = rect.left + rect.width / 2;
      document.documentElement.style.setProperty('--content-center-x', `${centerX}px`);
    };

    updateContentCenter();

    const resizeObserver =
      typeof ResizeObserver !== 'undefined' && contentRef.current
        ? new ResizeObserver(updateContentCenter)
        : null;

    if (resizeObserver && contentRef.current) {
      resizeObserver.observe(contentRef.current);
    }

    window.addEventListener('resize', updateContentCenter);

    return () => {
      if (resizeObserver) {
        resizeObserver.disconnect();
      }
      window.removeEventListener('resize', updateContentCenter);
      document.documentElement.style.removeProperty('--content-center-x');
    };
  }, []);

  useEffect(() => {
    if (!languageMenuOpen) {
      return;
    }

    const handlePointerDown = (event: MouseEvent) => {
      if (!languageMenuRef.current?.contains(event.target as Node)) {
        setLanguageMenuOpen(false);
      }
    };

    const handleEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setLanguageMenuOpen(false);
      }
    };

    document.addEventListener('mousedown', handlePointerDown);
    document.addEventListener('keydown', handleEscape);

    return () => {
      document.removeEventListener('mousedown', handlePointerDown);
      document.removeEventListener('keydown', handleEscape);
    };
  }, [languageMenuOpen]);

  useEffect(() => {
    if (!themeMenuOpen) {
      return;
    }

    const handlePointerDown = (event: MouseEvent) => {
      if (!themeMenuRef.current?.contains(event.target as Node)) {
        setThemeMenuOpen(false);
      }
    };

    const handleEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setThemeMenuOpen(false);
      }
    };

    document.addEventListener('mousedown', handlePointerDown);
    document.addEventListener('keydown', handleEscape);

    return () => {
      document.removeEventListener('mousedown', handlePointerDown);
      document.removeEventListener('keydown', handleEscape);
    };
  }, [themeMenuOpen]);

  useEffect(() => {
    if (!visualEffectsMenuOpen) {
      return;
    }

    const handlePointerDown = (event: MouseEvent) => {
      if (!visualEffectsMenuRef.current?.contains(event.target as Node)) {
        setVisualEffectsMenuOpen(false);
      }
    };

    const handleEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setVisualEffectsMenuOpen(false);
      }
    };

    document.addEventListener('mousedown', handlePointerDown);
    document.addEventListener('keydown', handleEscape);

    return () => {
      document.removeEventListener('mousedown', handlePointerDown);
      document.removeEventListener('keydown', handleEscape);
    };
  }, [visualEffectsMenuOpen]);

  const toggleLanguageMenu = useCallback(() => {
    setLanguageMenuOpen((prev) => !prev);
    setThemeMenuOpen(false);
    setVisualEffectsMenuOpen(false);
  }, []);

  const toggleThemeMenu = useCallback(() => {
    setThemeMenuOpen((prev) => !prev);
    setLanguageMenuOpen(false);
    setVisualEffectsMenuOpen(false);
  }, []);

  const toggleVisualEffectsMenu = useCallback(() => {
    setVisualEffectsMenuOpen((prev) => !prev);
    setLanguageMenuOpen(false);
    setThemeMenuOpen(false);
  }, []);

  const handleThemeSelect = useCallback(
    (nextTheme: Theme) => {
      setTheme(nextTheme);
      setThemeMenuOpen(false);
    },
    [setTheme]
  );

  const handleVisualEffectsSelect = useCallback(
    (nextMode: VisualEffectsMode) => {
      setVisualEffectsMode(nextMode);
      setVisualEffectsMenuOpen(false);
    },
    [setVisualEffectsMode]
  );

  const handleLanguageSelect = useCallback(
    (nextLanguage: string) => {
      if (!isSupportedLanguage(nextLanguage)) {
        return;
      }
      setLanguage(nextLanguage);
      setLanguageMenuOpen(false);
    },
    [setLanguage]
  );

  useEffect(() => {
    fetchConfig().catch(() => {
      // ignore initial failure; login flow会提示
    });
  }, [fetchConfig]);

  useLayoutEffect(
    () =>
      useAuthStore.subscribe((next, previous) => {
        if (
          next.apiBase !== previous.apiBase ||
          next.managementKey !== previous.managementKey ||
          next.connectionStatus !== previous.connectionStatus ||
          next.supportsPlugin !== previous.supportsPlugin
        ) {
          pluginResourcesRequest.current += 1;
          setPluginResources(undefined);
          // A disconnect and reconnect can be batched into a render with the same credentials.
          setPluginConnectionRevision((revision) => revision + 1);
        }
      }),
    []
  );

  const loadPluginResources = useCallback(async () => {
    if (connectionStatus !== 'connected' || !pluginWorkspaceVisible) return;

    const request = ++pluginResourcesRequest.current;
    const plugins = await pluginsApi.list({ apiBase, managementKey });
    const auth = useAuthStore.getState();
    if (
      request !== pluginResourcesRequest.current ||
      auth.apiBase !== apiBase ||
      auth.managementKey !== managementKey ||
      auth.connectionStatus !== 'connected' ||
      !auth.supportsPlugin
    )
      return;
    setPluginResources({
      scope: pluginScope,
      entries: isPluginResourceNavVisible({
        supportsPlugin,
        pluginsEnabled: plugins.pluginsEnabled,
      })
        ? collectPluginResourceNavEntries(plugins.plugins)
        : [],
    });
  }, [
    apiBase,
    connectionStatus,
    managementKey,
    pluginScope,
    pluginWorkspaceVisible,
    supportsPlugin,
  ]);

  useLayoutEffect(() => {
    const refreshPluginResources = () => {
      if (!pluginControlMenuVisible) setPluginResources(undefined);
      void loadPluginResources().catch(() => {
        // Background reads keep the last successful catalog for this connection.
      });
    };
    const timer = window.setTimeout(refreshPluginResources, 0);

    window.addEventListener(PLUGIN_RESOURCES_REFRESH_EVENT, refreshPluginResources);

    return () => {
      pluginResourcesRequest.current += 1;
      window.clearTimeout(timer);
      window.removeEventListener(PLUGIN_RESOURCES_REFRESH_EVENT, refreshPluginResources);
    };
  }, [loadPluginResources, pluginConnectionRevision, pluginControlMenuVisible]);

  const dashboardNavItem: NavItem = {
    path: dashboardNavigation.overview,
    label: t('nav.dashboard'),
    icon: navigationIcons.dashboard,
  };
  const usageLanding = getUsageWorkspaceLanding(featureAvailability);
  const monitoringLanding = getMonitoringWorkspaceLanding(featureAvailability);
  const usageAnalyticsNavItem = usageLanding
    ? {
        path: usageLanding,
        label: t('nav.usage_analytics'),
        icon: navigationIcons.usageAnalytics,
      }
    : null;
  const monitoringNavItem: NavItem = {
    path: monitoringLanding,
    label: t('nav.monitoring_center'),
    icon: navigationIcons.monitoring,
  };
  const pluginControlNavItems: NavItem[] = pluginControlMenuVisible
    ? [
        {
          path: '/plugins',
          label: t('nav.plugins'),
          icon: navigationIcons.plugins,
        },
      ]
    : [];
  const navItems: NavItem[] = [
    monitoringNavItem,
    {
      path: '/ai-providers',
      label: t('nav.ai_providers_nav'),
      icon: navigationIcons.aiProviders,
    },
    {
      path: accessNavigation.accounts,
      label: t('nav.accounts_nav'),
      icon: navigationIcons.authFiles,
    },
    dashboardNavItem,
    ...(usageAnalyticsNavItem ? [usageAnalyticsNavItem] : []),
    ...pluginControlNavItems,
  ];
  const navOrder = navItems.map((item) => item.path);
  const getRouteOrder = (pathname: string) => {
    const trimmedPath =
      pathname.length > 1 && pathname.endsWith('/') ? pathname.slice(0, -1) : pathname;
    const normalizedPath = getDashboardTab(trimmedPath)
      ? dashboardNavigation.overview
      : isUsageWorkspacePath(trimmedPath) && usageLanding
        ? usageLanding
        : isMonitoringWorkspacePath(trimmedPath)
          ? monitoringLanding
          : trimmedPath;

    const aiProvidersIndex = navOrder.indexOf('/ai-providers');
    if (aiProvidersIndex !== -1) {
      if (normalizedPath === '/ai-providers') return aiProvidersIndex;
      if (normalizedPath.startsWith('/ai-providers/')) {
        if (normalizedPath.startsWith('/ai-providers/gemini')) return aiProvidersIndex + 0.1;
        if (normalizedPath.startsWith('/ai-providers/codex')) return aiProvidersIndex + 0.2;
        if (normalizedPath.startsWith('/ai-providers/claude')) return aiProvidersIndex + 0.3;
        if (normalizedPath.startsWith('/ai-providers/vertex')) return aiProvidersIndex + 0.4;
        if (normalizedPath.startsWith('/ai-providers/openai')) return aiProvidersIndex + 0.6;
        return aiProvidersIndex + 0.05;
      }
    }

    const exactIndex = navOrder.indexOf(normalizedPath);
    if (exactIndex !== -1) return exactIndex;
    const nestedIndex = navOrder.findIndex(
      (path) => path !== '/' && normalizedPath.startsWith(`${path}/`)
    );
    return nestedIndex === -1 ? null : nestedIndex;
  };

  const getTransitionVariant = useCallback((fromPathname: string, toPathname: string) => {
    const normalize = (pathname: string) => {
      const trimmed =
        pathname.length > 1 && pathname.endsWith('/') ? pathname.slice(0, -1) : pathname;
      return trimmed === '/dashboard' ? '/' : trimmed;
    };

    const from = normalize(fromPathname);
    const to = normalize(toPathname);
    const isAccounts = (pathname: string) => pathname === '/accounts';
    const isAiProviders = (pathname: string) =>
      pathname === '/ai-providers' || pathname.startsWith('/ai-providers/');
    if (isAccounts(from) && isAccounts(to)) return 'ios';
    if (isAiProviders(from) && isAiProviders(to)) return 'ios';
    return 'none';
  }, []);

  const handleRefreshAll = async () => {
    clearCache();
    const results = await Promise.allSettled([
      fetchConfig(undefined, true),
      loadPluginResources(),
      triggerHeaderRefresh(),
    ]);
    const rejected = results.find((result) => result.status === 'rejected');
    if (rejected && rejected.status === 'rejected') {
      const reason = rejected.reason;
      const message =
        typeof reason === 'string' ? reason : reason instanceof Error ? reason.message : '';
      showNotification(
        `${t('notification.refresh_failed')}${message ? `: ${message}` : ''}`,
        'error'
      );
      return;
    }
    showNotification(t('notification.data_refreshed'), 'success');
  };
  const handleLogout = () => {
    if (demoMode) {
      navigate(getDemoLogoutPath(routeBase), { replace: true });
      return;
    }
    logout();
  };
  const normalizedLocationPath =
    routePathname.length > 1 && routePathname.endsWith('/')
      ? routePathname.slice(0, -1)
      : routePathname;
  const currentPath = normalizedLocationPath === '/dashboard' ? '/' : normalizedLocationPath;
  const matchesNavPath = (item: NavItem, pathname: string) => {
    if (item === usageAnalyticsNavItem) return isUsageWorkspacePath(pathname);
    if (item === monitoringNavItem) return isMonitoringWorkspacePath(pathname);
    if (item.path === dashboardNavigation.overview) return Boolean(dashboardTab);
    if (item.path === accessNavigation.accounts) return Boolean(accessTab);
    if (item.path === pluginNavigation.root) return Boolean(pluginWorkspaceRoute);
    return pathname === item.path || pathname.startsWith(`${item.path}/`);
  };
  const activeNavItem = navItems.find((item) => matchesNavPath(item, currentPath));
  const workspaceTitle = dashboardTab
    ? t('nav.dashboard')
    : accessTab
      ? t('nav.accounts')
      : isUsageWorkspacePath(currentPath) && usageLanding
        ? t('nav.usage_analytics')
        : isMonitoringWorkspacePath(currentPath)
          ? t('nav.monitoring_center')
          : pluginWorkspaceVisible
            ? t('nav.plugins')
            : null;

  useLayoutEffect(() => {
    const revealActiveLink = () => {
      const nav = navigationRef.current;
      const link = nav?.querySelector<HTMLElement>('[aria-current="page"]');
      if (!nav || !link) return;
      const viewport = nav.getBoundingClientRect();
      const bounds = link.getBoundingClientRect();
      if (bounds.left < viewport.left) nav.scrollLeft += bounds.left - viewport.left;
      else if (bounds.right > viewport.right) nav.scrollLeft += bounds.right - viewport.right;
    };
    revealActiveLink();
    window.addEventListener('resize', revealActiveLink);
    return () => window.removeEventListener('resize', revealActiveLink);
  }, [activeNavItem?.path, language]);

  return (
    <div
      className={['app-shell', isPluginResourcePage ? 'plugin-resource-shell' : '']
        .filter(Boolean)
        .join(' ')}
    >
      <header className="main-header" ref={headerRef}>
        <div className="navbar">
          <div className="header-brand" title={fullBrandName}>
            <img
              src={CPAMP_SYMBOL_COLOR_PNG_URL}
              alt={fullBrandName}
              className="header-brand-symbol"
            />
            <img
              src={CPAMP_WORDMARK_COLOR_PNG_URL}
              alt=""
              className="header-brand-wordmark header-brand-wordmark-light"
            />
            <img
              src={CPAMP_WORDMARK_ON_DARK_PNG_URL}
              alt=""
              className="header-brand-wordmark header-brand-wordmark-dark"
            />
          </div>

          <nav
            className="workspace-navigation"
            ref={navigationRef}
            aria-label={t('common.navigation', { defaultValue: 'Navigation' })}
          >
            {navItems.map((item) => (
              <Link
                key={item.path}
                to={prefixRouteBase(item.path, routeBase)}
                className="workspace-nav-link"
                aria-current={item === activeNavItem ? 'page' : undefined}
              >
                <span className="workspace-nav-icon" aria-hidden="true">
                  {item.icon}
                </span>
                {item.label}
              </Link>
            ))}
          </nav>

          <div className="navbar-right">
            <Button
              variant="ghost"
              size="sm"
              onClick={handleRefreshAll}
              title={t('header.refresh_all')}
              aria-label={t('header.refresh_all')}
            >
              {headerIcons.refresh}
            </Button>

            <a
              className="btn btn-ghost btn-sm"
              href={GITHUB_REPOSITORY_URL}
              target="_blank"
              rel="noreferrer"
              title={t('header.open_github')}
              aria-label={t('header.open_github')}
            >
              <span>
                <IconGithub size={16} />
              </span>
            </a>

            <div
              className={`language-menu ${languageMenuOpen ? 'open' : ''}`}
              ref={languageMenuRef}
            >
              <Button
                variant="ghost"
                size="sm"
                onClick={toggleLanguageMenu}
                title={t('language.switch')}
                aria-label={t('language.switch')}
                aria-haspopup="menu"
                aria-expanded={languageMenuOpen}
              >
                {headerIcons.language}
              </Button>
              {languageMenuOpen && (
                <div
                  className="notification entering language-menu-popover"
                  role="menu"
                  aria-label={t('language.switch')}
                >
                  {LANGUAGE_ORDER.map((lang) => (
                    <button
                      key={lang}
                      type="button"
                      className={`language-menu-option ${language === lang ? 'active' : ''}`}
                      onClick={() => handleLanguageSelect(lang)}
                      role="menuitemradio"
                      aria-checked={language === lang}
                    >
                      <span>{t(LANGUAGE_LABEL_KEYS[lang])}</span>
                    </button>
                  ))}
                </div>
              )}
            </div>

            <div className={`theme-menu ${themeMenuOpen ? 'open' : ''}`} ref={themeMenuRef}>
              <Button
                variant="ghost"
                size="sm"
                onClick={toggleThemeMenu}
                title={t('theme.switch')}
                aria-label={t('theme.switch')}
                aria-haspopup="menu"
                aria-expanded={themeMenuOpen}
              >
                {theme === 'auto'
                  ? headerIcons.autoTheme
                  : theme === 'dark'
                    ? headerIcons.moon
                    : headerIcons.sun}
              </Button>
              {themeMenuOpen && (
                <div
                  className="notification entering theme-menu-popover"
                  role="menu"
                  aria-label={t('theme.switch')}
                >
                  {THEME_OPTIONS.map((option) => (
                    <button
                      key={option.key}
                      type="button"
                      className={`theme-option ${theme === option.key ? 'active' : ''}`}
                      onClick={() => handleThemeSelect(option.key)}
                      role="menuitemradio"
                      aria-checked={theme === option.key}
                      title={t(option.labelKey)}
                      aria-label={t(option.labelKey)}
                    >
                      <span className="theme-option-icon">{option.icon}</span>
                    </button>
                  ))}
                </div>
              )}
            </div>

            <div
              className={`visual-effects-menu ${visualEffectsMenuOpen ? 'open' : ''}`}
              ref={visualEffectsMenuRef}
            >
              <Button
                variant="ghost"
                size="sm"
                onClick={toggleVisualEffectsMenu}
                title={t('visual_effects.switch')}
                aria-label={t('visual_effects.switch')}
                aria-haspopup="menu"
                aria-expanded={visualEffectsMenuOpen}
              >
                {visualEffectsMode === 'full'
                  ? headerIcons.visualEffectsFull
                  : headerIcons.visualEffectsReduced}
              </Button>
              {visualEffectsMenuOpen && (
                <div
                  className="notification entering visual-effects-menu-popover"
                  role="menu"
                  aria-label={t('visual_effects.switch')}
                >
                  {VISUAL_EFFECTS_OPTIONS.map((option) => (
                    <button
                      key={option.key}
                      type="button"
                      className={`visual-effects-option ${
                        visualEffectsMode === option.key ? 'active' : ''
                      }`}
                      onClick={() => handleVisualEffectsSelect(option.key)}
                      role="menuitemradio"
                      aria-checked={visualEffectsMode === option.key}
                      title={t(option.labelKey)}
                      aria-label={t(option.labelKey)}
                    >
                      <span className="visual-effects-option-icon">{option.icon}</span>
                      <span className="visual-effects-option-label">{t(option.labelKey)}</span>
                    </button>
                  ))}
                </div>
              )}
            </div>

            <Button
              variant="ghost"
              size="sm"
              onClick={handleLogout}
              title={t('header.logout')}
              aria-label={t('header.logout')}
            >
              {headerIcons.logout}
            </Button>
          </div>
        </div>
      </header>

      <div className="main-body">
        <div
          className={[
            'content',
            isLogsPage ? 'content-logs' : '',
            isPluginResourcePage ? 'content-plugin-resource' : '',
          ]
            .filter(Boolean)
            .join(' ')}
          ref={contentRef}
        >
          <main
            className={[
              'main-content',
              isLogsPage ? 'main-content-logs' : '',
              isPluginResourcePage ? 'main-content-plugin-resource' : '',
            ]
              .filter(Boolean)
              .join(' ')}
          >
            {workspaceTitle && <h1 className="workspace-title">{workspaceTitle}</h1>}
            <DatabaseMaintenanceBanner />
            {dashboardTab && (
              <RouteTabs
                ariaLabel={t('nav.dashboard')}
                activeTab={dashboardTab}
                idBase={dashboardNavigation.id}
                routeBase={routeBase}
                items={[
                  {
                    id: 'overview',
                    label: t('dashboard.tab_overview'),
                    to: dashboardNavigation.overview,
                  },
                  {
                    id: 'config',
                    label: t('nav.config_management'),
                    to: dashboardNavigation.config,
                  },
                  { id: 'system', label: t('nav.system_info'), to: dashboardNavigation.system },
                ]}
              />
            )}
            {accessTab && (
              <RouteTabs
                ariaLabel={t('accounts.tabs_label')}
                activeTab={accessTab}
                idBase={accessNavigation.id}
                routeBase={routeBase}
                items={[
                  {
                    id: 'accounts',
                    label: t('accounts.tab_accounts'),
                    to: accountsTabTarget('accounts', accountsSearch),
                  },
                  {
                    id: 'health',
                    label: t('accounts.tab_health'),
                    to: accountsTabTarget('health', accountsSearch),
                  },
                  {
                    id: 'oauth',
                    label: t('accounts.tab_oauth'),
                    to: accountsTabTarget('oauth', accountsSearch),
                  },
                  {
                    id: 'credentials',
                    label: t('nav.plan_credentials'),
                    to: accessNavigation.credentials,
                  },
                  { id: 'login', label: t('nav.oauth'), to: accessNavigation.login },
                ]}
              />
            )}
            {pluginWorkspaceVisible && (
              <RouteTabs
                ariaLabel={t('nav.plugins')}
                activeTab={pluginTab}
                idBase={pluginNavigation.id}
                routeBase={routeBase}
                items={[
                  {
                    id: 'installed',
                    label: t('plugin_management.tab_installed'),
                    to: pluginManagementTarget('installed', pluginSearch, pluginHash),
                  },
                  {
                    id: 'store',
                    label: t('plugin_management.tab_store'),
                    to: pluginManagementTarget('store', pluginSearch, pluginHash),
                  },
                  ...pluginResourceTabs,
                ]}
              />
            )}
            <PageTransition
              panelProps={
                dashboardTab
                  ? routeTabPanel(dashboardNavigation.id, dashboardTab)
                  : accessTab
                    ? routeTabPanel(accessNavigation.id, accessTab)
                    : pluginWorkspaceVisible && pluginTab
                      ? routeTabPanel(pluginNavigation.id, pluginTab)
                      : undefined
              }
              key={routeBase || 'main'}
              render={(location) => <MainRoutes location={location} routeBase={routeBase} />}
              getRouteOrder={(pathname) => getRouteOrder(stripRouteBase(pathname, routeBase))}
              getTransitionVariant={(fromPathname, toPathname) =>
                getTransitionVariant(
                  stripRouteBase(fromPathname, routeBase),
                  stripRouteBase(toPathname, routeBase)
                )
              }
              scrollContainerRef={contentRef}
            />
          </main>
        </div>
      </div>
    </div>
  );
}

export function MainLayout(props: MainLayoutProps = {}) {
  return (
    <DatabaseMaintenanceProvider>
      <MainLayoutContent {...props} />
    </DatabaseMaintenanceProvider>
  );
}
