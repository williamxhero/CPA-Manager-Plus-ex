import { afterEach, describe, expect, it, vi } from 'vitest';
import { act, create } from 'react-test-renderer';
import { createElement, useEffect } from 'react';
import {
  createWorkspaceConnectionState,
  useWorkspaceConnectionGeneration,
} from './workspaceConnectionState';
import { useAuthStore } from './useAuthStore';

vi.mock('@/services/storage/secureStorage', () => ({
  obfuscatedStorage: { getItem: () => null, setItem: () => undefined },
}));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
const originalAuth = useAuthStore.getState();
afterEach(() => useAuthStore.setState(originalAuth));

describe('workspace connection state', () => {
  it('keeps filters within one connection and drops them after key changes or logout', async () => {
    const filters = createWorkspaceConnectionState<string>();
    let scope = -1;
    const Reader = () => {
      const generation = useWorkspaceConnectionGeneration();
      useEffect(() => {
        scope = generation;
      }, [generation]);
      return null;
    };
    let renderer!: ReturnType<typeof create>;
    await act(async () => { renderer = create(createElement(Reader)); });
    await act(async () => {
      useAuthStore.setState({
        apiBase: 'https://manager.example',
        managementKey: 'first-fixture-key',
        isAuthenticated: true,
      });
    });
    const first = scope;
    filters.write(first, 'first connection filter');
    expect(filters.read(scope)).toBe('first connection filter');
    await act(async () => {
      useAuthStore.setState({ managementKey: 'second-fixture-key' });
    });
    expect(scope).not.toBe(first);
    filters.write(first, 'late first connection filter');
    expect(filters.read(scope)).toBeUndefined();
    filters.write(scope, 'second connection filter');
    await act(async () => {
      useAuthStore.setState({ isAuthenticated: false });
    });
    expect(filters.read(scope)).toBeUndefined();
    await act(async () => renderer.unmount());
  });
});
