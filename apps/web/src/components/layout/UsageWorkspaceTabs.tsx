import { useTranslation } from 'react-i18next';
import { RouteTabs, type RouteTab } from './RouteTabs';
import { usageWorkspaceRoutes } from './usageMonitoringNavigation';
import {
  USAGE_ANALYTICS_TABS,
  type UsageAnalyticsTab,
} from '@/features/usage-analytics/usageAnalyticsModel';

type UsageWorkspaceTab = UsageAnalyticsTab | 'organize' | 'transfer';
export const usageTabsId = 'usage-workspace';

export function UsageWorkspaceTabs({
  activeTab,
  showAnalysis,
  showMaintenance,
  search = '',
}: {
  activeTab: UsageWorkspaceTab;
  showAnalysis: boolean;
  showMaintenance: boolean;
  search?: string;
}) {
  const { t } = useTranslation();
  const items: RouteTab<UsageWorkspaceTab>[] = [];
  if (showAnalysis) {
    for (const tab of USAGE_ANALYTICS_TABS) {
      const params = new URLSearchParams(
        activeTab === 'organize' || activeTab === 'transfer' ? '' : search
      );
      params.set('tab', tab);
      items.push({
        id: tab,
        label: t(`usage_analytics.tab_${tab}`),
        to: `${usageWorkspaceRoutes.analysis}?${params}`,
      });
    }
  }
  if (showMaintenance) {
    items.push(
      {
        id: 'organize',
        label: t('usage_maintenance.archive_management'),
        to: usageWorkspaceRoutes.archive,
      },
      {
        id: 'transfer',
        label: t('usage_maintenance.workspace_transfer'),
        to: usageWorkspaceRoutes.transfer,
      }
    );
  }
  return (
    <RouteTabs
      title={t('nav.usage_analytics')}
      items={items}
      activeTab={activeTab}
      idBase={usageTabsId}
      routeBase=""
    />
  );
}
