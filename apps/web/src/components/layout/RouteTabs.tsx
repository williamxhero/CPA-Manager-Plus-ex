import { Link, parsePath } from 'react-router-dom';
import {
  SegmentedTabs,
  type SegmentedTabItem,
  type SegmentedTabsLinkProps,
} from '@/components/ui/SegmentedTabs';
import { prefixRouteBase } from '@/features/demo/demoMode';
import { routeTabPanel } from './routeTabPanel';
import styles from './RouteTabs.module.scss';

export type RouteTab<Id extends string> = SegmentedTabItem<Id> & { to: string };

function routeTabTarget(to: string, routeBase: string) {
  const { pathname = '/', search = '', hash = '' } = parsePath(to);
  return `${prefixRouteBase(pathname, routeBase)}${search}${hash}`;
}

function RouteTabLink(props: SegmentedTabsLinkProps) {
  return <Link {...props} />;
}

export function RouteTabs<Id extends string>({
  title,
  items,
  activeTab,
  idBase,
  routeBase,
}: {
  title: string;
  items: ReadonlyArray<RouteTab<Id>>;
  activeTab: Id;
  idBase: string;
  routeBase: string;
}) {
  return (
    <header className={styles.header}>
      <h1>{title}</h1>
      <SegmentedTabs
        items={items.map((item) => ({ ...item, to: routeTabTarget(item.to, routeBase) }))}
        activeTab={activeTab}
        ariaLabel={title}
        idBase={idBase}
        panelId={routeTabPanel(idBase, activeTab).id}
        linkComponent={RouteTabLink}
        keepActiveVisible
      />
    </header>
  );
}
