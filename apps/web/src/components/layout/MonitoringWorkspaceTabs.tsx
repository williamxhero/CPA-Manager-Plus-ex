import { useTranslation } from 'react-i18next';
import type { ReactNode } from 'react';
import { RouteTabs, type RouteTab } from './RouteTabs';
import { monitoringTabsId, monitoringWorkspaceRoutes } from './usageMonitoringNavigation';
import type { MonitoringDataTab } from '@/features/monitoring/monitoringCenterUiState';

export type MonitoringWorkspaceTab = MonitoringDataTab | 'logs' | 'errors';
const monitoringLabels = {
  accounts: 'accounts',
  apiKeys: 'api_keys',
  realtime: 'realtime',
} as const;

export function MonitoringWorkspaceTabs({
  activeTab,
  showData,
  labels,
  search = '',
}: {
  activeTab: MonitoringWorkspaceTab;
  showData: boolean;
  labels?: Partial<Record<MonitoringDataTab, ReactNode>>;
  search?: string;
}) {
  const { t } = useTranslation();
  const items: RouteTab<MonitoringWorkspaceTab>[] = [];
  if (showData) {
    for (const tab of ['accounts', 'apiKeys', 'realtime'] as const) {
      const key = `monitoring.data_tab_${monitoringLabels[tab]}`;
      const params = new URLSearchParams(activeTab === tab ? search : '');
      params.set('tab', tab);
      items.push({
        id: tab,
        label: labels?.[tab] ?? t(`${key}_short`, { defaultValue: t(key) }),
        title: t(key),
        to: `${monitoringWorkspaceRoutes.data}?${params}`,
      });
    }
  }
  const logItems = [
    ['logs', 'logs.log_content'],
    ['errors', 'logs.error_logs_modal_title'],
  ] as const;
  for (const [tab, key] of logItems) {
    const params = new URLSearchParams(
      activeTab === 'logs' || activeTab === 'errors' ? search : ''
    );
    if (tab === 'errors') params.set('tab', 'errors');
    else params.delete('tab');
    items.push({
      id: tab,
      label: t(key),
      to: `${monitoringWorkspaceRoutes.logs}${params.size ? `?${params}` : ''}`,
    });
  }
  return (
    <RouteTabs
      ariaLabel={t('nav.monitoring_center')}
      items={items}
      activeTab={activeTab}
      idBase={monitoringTabsId}
      routeBase=""
    />
  );
}
