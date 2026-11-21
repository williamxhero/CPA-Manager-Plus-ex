import { useLayoutEffect, useRef } from 'react';
import { Link, parsePath, useLocation, useNavigate, useResolvedPath } from 'react-router-dom';
import {
  SegmentedTabs,
  type SegmentedTabItem,
  type SegmentedTabsLinkProps,
} from '@/components/ui/SegmentedTabs';
import { prefixRouteBase } from '@/features/demo/demoMode';
import { routeTabPanel } from './routeTabPanel';
import styles from './RouteTabs.module.scss';

export type RouteTab<Id extends string> = SegmentedTabItem<Id> & { to: string };

// Tie the handoff to the new router location. The in-memory request is consumed once,
// so a later reload or history traversal cannot restore focus from history state.
const keyboardFocusRequests = new WeakMap<object, HTMLElement>();

function routeTabTarget(to: string, routeBase: string) {
  const { pathname = '/', search = '', hash = '' } = parsePath(to);
  return `${prefixRouteBase(pathname, routeBase)}${search}${hash}`;
}

function RouteTabLink({ onKeyDown, to, ...props }: SegmentedTabsLinkProps) {
  const { pathname } = useLocation();
  const target = useResolvedPath(to);
  const navigate = useNavigate();

  return (
    <Link
      {...props}
      to={to}
      onKeyDown={(event) => {
        if (
          target.pathname !== pathname &&
          (event.key === 'Enter' || event.key === ' ') &&
          !event.defaultPrevented &&
          !event.altKey &&
          !event.ctrlKey &&
          !event.metaKey &&
          !event.shiftKey
        ) {
          event.preventDefault();
          const state = {};
          keyboardFocusRequests.set(state, event.currentTarget);
          void navigate(to, { state });
          return;
        }
        onKeyDown(event);
      }}
    />
  );
}

export function RouteTabs<Id extends string>({
  ariaLabel,
  items,
  activeTab,
  idBase,
  routeBase,
}: {
  ariaLabel: string;
  items: ReadonlyArray<RouteTab<Id>>;
  activeTab: Id | undefined;
  idBase: string;
  routeBase: string;
}) {
  const headerRef = useRef<HTMLElement>(null);
  const { state } = useLocation();

  useLayoutEffect(() => {
    if (!state || typeof state !== 'object' || !activeTab) return;
    const source = keyboardFocusRequests.get(state);
    const header = headerRef.current;
    if (!source || !header || source.id !== `${idBase}-${activeTab}`) return;
    const destination = header.querySelector<HTMLElement>('[role="tab"][aria-selected="true"]');
    if (!destination || destination.closest('[inert]')) return;
    keyboardFocusRequests.delete(state);
    const { activeElement, body } = header.ownerDocument;
    if (activeElement === source || activeElement === body) {
      destination.focus({ preventScroll: true });
    }
  }, [activeTab, idBase, state]);

  return (
    <header className={styles.header} ref={headerRef}>
      <SegmentedTabs
        items={items.map((item) => ({ ...item, to: routeTabTarget(item.to, routeBase) }))}
        activeTab={activeTab}
        ariaLabel={ariaLabel}
        idBase={idBase}
        panelId={activeTab && routeTabPanel(idBase, activeTab).id}
        linkComponent={RouteTabLink}
        keepActiveVisible
      />
    </header>
  );
}
