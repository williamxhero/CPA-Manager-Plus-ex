export function routeTabPanel(idBase: string, activeTab: string) {
  return {
    id: `${idBase}-panel`,
    role: 'tabpanel' as const,
    'aria-labelledby': `${idBase}-${activeTab}`,
    tabIndex: 0,
  };
}
