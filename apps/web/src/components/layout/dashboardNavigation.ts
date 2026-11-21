export const dashboardNavigation = {
  id: 'dashboard',
  overview: '/',
  config: '/config',
  system: '/system',
} as const;

export function getDashboardTab(pathname: string) {
  // Match the router's case-insensitive paths, including its legacy dashboard alias.
  switch (pathname.toLowerCase()) {
    case '/':
    case '/dashboard':
      return 'overview';
    case '/config':
    case '/settings':
    case '/api-keys':
      return 'config';
    case '/system':
    case '/system/updates':
      return 'system';
    default:
      return null;
  }
}
