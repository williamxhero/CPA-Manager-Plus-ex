import { matchPath } from 'react-router-dom';

export const pluginNavigation = {
  id: 'plugin-management',
  root: '/plugins',
  store: '/plugin-store',
  resource: '/plugin-pages/:pluginId/:menuIndex',
} as const;

export const getPluginWorkspaceRoute = (pathname: string) => {
  const path = pathname.toLowerCase().replace(/\/+$/, '');
  if (path === pluginNavigation.root) return 'management';
  if (path === pluginNavigation.store) return 'store';
  if (matchPath(pluginNavigation.resource, pathname)) return 'resource';
  return undefined;
};

export const getPluginManagementTab = (search: string) =>
  new URLSearchParams(search).get('tab') === 'store' ? 'store' : 'installed';

export function pluginManagementTarget(tab: 'installed' | 'store', search = '', hash = '') {
  const params = new URLSearchParams(search);
  if (tab === 'store') params.set('tab', 'store');
  else params.delete('tab');
  const next = params.toString();
  return pluginNavigation.root + (next ? '?' + next : '') + hash;
}
