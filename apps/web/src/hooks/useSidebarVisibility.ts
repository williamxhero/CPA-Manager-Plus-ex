import { useEffect, useState } from 'react';

/** Ephemeral mobile drawer state, independent of the persisted desktop collapse preference. */
export function useSidebarVisibility(sidebarCollapsed: boolean) {
  const [sidebarOpen, setSidebarOpen] = useState(false);
  useEffect(() => {
    if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return;
    const mobile = window.matchMedia('(max-width: 768px)');
    const closeDesktopDrawer = () => {
      if (!mobile.matches) setSidebarOpen(false);
    };
    mobile.addEventListener('change', closeDesktopDrawer);
    return () => mobile.removeEventListener('change', closeDesktopDrawer);
  }, []);
  return {
    sidebarOpen,
    setSidebarOpen,
    showSidebarLabels: !sidebarCollapsed || sidebarOpen,
  };
}
