import type { ReactNode } from 'react';
import { Card } from '@/components/ui/Card';
import { routeTabPanel } from '@/components/layout/routeTabPanel';
import {
  MonitoringTabsBar,
  type MonitoringTab,
} from '@/features/monitoring/components/MonitoringTabsBar';
import styles from '@/features/monitoring/MonitoringCenterPage.module.scss';

type MonitoringDataPanelProps<Id extends string> = {
  tabs: ReadonlyArray<MonitoringTab<Id>>;
  activeTab: Id;
  onTabChange: (tab: Id) => void;
  ariaLabel: string;
  actions?: ReactNode;
  idBase?: string;
  workspaceId?: string;
  renderContent: (tab: Id) => ReactNode;
};

export function MonitoringDataPanel<Id extends string>({
  tabs,
  activeTab,
  onTabChange,
  ariaLabel,
  actions,
  idBase = 'monitoring-data-tabs',
  workspaceId,
  renderContent,
}: MonitoringDataPanelProps<Id>) {
  if (workspaceId) {
    return (
      <Card className={styles.dataPanel}>
        {actions ? <div className={styles.dataPanelActions}>{actions}</div> : null}
        <div className={styles.dataPanelBody} {...routeTabPanel(workspaceId, activeTab)}>
          {renderContent(activeTab)}
        </div>
      </Card>
    );
  }
  return (
    <Card className={styles.dataPanel}>
      <div className={styles.dataPanelHeader}>
        <div className={styles.dataPanelTabs}>
          <MonitoringTabsBar
            tabs={tabs}
            activeTab={activeTab}
            onChange={onTabChange}
            ariaLabel={ariaLabel}
            idBase={idBase}
            variant="cards"
          />
        </div>
        {actions ? <div className={styles.dataPanelActions}>{actions}</div> : null}
      </div>

      <div className={styles.dataPanelBody}>
        {tabs.map((tab) => {
          const isActive = tab.id === activeTab;
          return (
            <div
              key={tab.id}
              role="tabpanel"
              id={`${idBase}-${tab.id}-panel`}
              aria-labelledby={`${idBase}-${tab.id}`}
              className={styles.dataPanelTabPanel}
              hidden={!isActive}
            >
              {isActive ? renderContent(tab.id) : null}
            </div>
          );
        })}
      </div>
    </Card>
  );
}
