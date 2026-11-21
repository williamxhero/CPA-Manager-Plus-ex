import { useAuthStore } from './useAuthStore';

let generation = 0;

useAuthStore.subscribe((state, previous) => {
  if (
    state.apiBase !== previous.apiBase ||
    state.managementKey !== previous.managementKey ||
    state.isAuthenticated !== previous.isAuthenticated
  ) {
    generation += 1;
  }
});

export function useWorkspaceConnectionGeneration() {
  return useAuthStore(() => generation);
}

// Keep filters between routes, but not between connections or after logout.
export function createWorkspaceConnectionState<T>() {
  let snapshot: { generation: number; value: T } | undefined;
  return {
    read: (scope: number) => (snapshot?.generation === scope ? snapshot.value : undefined),
    write: (scope: number, value: T) => {
      if (scope === generation) snapshot = { generation: scope, value };
    },
  };
}
