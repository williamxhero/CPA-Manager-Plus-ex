import type { PanelFeatureAvailability } from '@/hooks/usePanelFeatureAvailability';

export const usageWorkspaceRoutes = {
  analysis: '/usage-analytics',
  archive: '/usage-maintenance',
  transfer: '/usage-maintenance?tab=transfer',
} as const;

export const monitoringWorkspaceRoutes = {
  data: '/monitoring',
  logs: '/logs',
  errors: '/logs?tab=errors',
} as const;

export const canAccessUsageMaintenance = (availability: PanelFeatureAvailability) =>
  availability.panelHostMode === 'manager_embedded' && availability.managerServiceAvailable;

export const getUsageWorkspaceLanding = (availability: PanelFeatureAvailability) =>
  availability.requestMonitoringAvailable
    ? usageWorkspaceRoutes.analysis
    : canAccessUsageMaintenance(availability)
      ? usageWorkspaceRoutes.archive
      : null;

export const getMonitoringWorkspaceLanding = (availability: PanelFeatureAvailability) =>
  availability.requestMonitoringAvailable
    ? monitoringWorkspaceRoutes.data
    : monitoringWorkspaceRoutes.logs;

export const isUsageWorkspacePath = (pathname: string) =>
  pathname === usageWorkspaceRoutes.analysis || pathname === usageWorkspaceRoutes.archive;

export const isMonitoringWorkspacePath = (pathname: string) =>
  pathname === monitoringWorkspaceRoutes.data || pathname === monitoringWorkspaceRoutes.logs;

export const monitoringTabsId = 'monitoring-workspace';
export const readLogTab = (params: URLSearchParams) =>
  params.get('tab') === 'errors' ? 'errors' : 'logs';
