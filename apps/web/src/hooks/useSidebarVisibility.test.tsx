import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { useLayoutEffect } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useSidebarVisibility } from './useSidebarVisibility';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

describe('responsive sidebar brand visibility', () => {
  let mobile = true;
  let renderer: ReactTestRenderer | undefined;
  const listeners = new Set<() => void>();
  let state: ReturnType<typeof useSidebarVisibility>;

  function Harness({ collapsed = true }: { collapsed?: boolean }) {
    const current = useSidebarVisibility(collapsed);
    useLayoutEffect(() => {
      state = current;
    });
    return <span data-labels={current.showSidebarLabels} data-open={current.sidebarOpen} />;
  }

  function mount(collapsed = true) {
    act(() => {
      renderer = create(<Harness collapsed={collapsed} />);
    });
  }

  function resize(toMobile: boolean) {
    act(() => {
      mobile = toMobile;
      for (const listener of listeners) listener();
    });
  }

  beforeEach(() => {
    mobile = true;
    listeners.clear();
    const media = {
      get matches() {
        return mobile;
      },
      addEventListener: (_event: string, listener: () => void) => listeners.add(listener),
      removeEventListener: (_event: string, listener: () => void) => listeners.delete(listener),
    };
    vi.stubGlobal('window', { matchMedia: vi.fn(() => media) });
  });

  afterEach(() => {
    act(() => renderer?.unmount());
    renderer = undefined;
    vi.unstubAllGlobals();
  });

  it('shows labels inside the open mobile drawer, even with a collapsed desktop preference', () => {
    mount();
    expect(state.showSidebarLabels).toBe(false);
    act(() => state.setSidebarOpen(true));
    expect(state.showSidebarLabels).toBe(true);
    expect(state.sidebarOpen).toBe(true);
  });

  it('closes the mobile drawer on desktop resize so wordmark cannot enter the 54px rail', () => {
    mount();
    act(() => state.setSidebarOpen(true));
    resize(false);
    expect(state.sidebarOpen).toBe(false);
    expect(state.showSidebarLabels).toBe(false);
    resize(true);
    expect(state.sidebarOpen).toBe(false);
    expect(state.showSidebarLabels).toBe(false);
  });

  it('retains labels for an expanded desktop sidebar after the mobile drawer closes', () => {
    mount(false);
    act(() => state.setSidebarOpen(true));
    resize(false);
    expect(state.sidebarOpen).toBe(false);
    expect(state.showSidebarLabels).toBe(true);
  });

  it('unsubscribes the media observer on unmount', () => {
    mount();
    expect(listeners.size).toBe(1);
    act(() => renderer?.unmount());
    renderer = undefined;
    expect(listeners.size).toBe(0);
  });
});
