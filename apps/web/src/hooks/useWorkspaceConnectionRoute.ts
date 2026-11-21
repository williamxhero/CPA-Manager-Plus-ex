import { useLayoutEffect, useState } from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import { useWorkspaceConnectionGeneration } from '@/stores/workspaceConnectionState';

// A fresh bookmark is accepted. An entry already on-screen when auth changes
// must not initialize or issue reads on the new connection with the old scope.
export function useWorkspaceConnectionRoute(
  enabled: boolean,
  staleParameters: ReadonlySet<string>
) {
  const generation = useWorkspaceConnectionGeneration();
  const location = useLocation();
  const navigate = useNavigate();
  const [accepted, setAccepted] = useState(generation);
  const next = new URLSearchParams(location.search);
  const stale = enabled && accepted !== generation &&
    [...staleParameters].some((parameter) => next.has(parameter));
  if (stale) for (const parameter of staleParameters) next.delete(parameter);
  const cleanSearch = next.toString();
  if (!stale && accepted !== generation) setAccepted(generation);

  useLayoutEffect(() => {
    if (!stale) return;
    void navigate(
      {
        pathname: location.pathname,
        search: cleanSearch ? `?${cleanSearch}` : '',
        hash: location.hash,
      },
      { replace: true }
    );
  }, [cleanSearch, location.hash, location.pathname, navigate, stale]);

  return { generation, stale };
}
