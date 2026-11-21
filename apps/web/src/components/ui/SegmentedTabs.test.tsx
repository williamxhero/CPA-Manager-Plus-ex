import { renderToStaticMarkup } from 'react-dom/server';
import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { describe, expect, it, vi } from 'vitest';
import {
  SegmentedTabs,
  type SegmentedTabItem,
  type SegmentedTabsLinkProps,
} from './SegmentedTabs';

const baseItems: ReadonlyArray<SegmentedTabItem<'visual' | 'source' | 'manager'>> = [
  { id: 'visual', label: 'Visual' },
  { id: 'source', label: 'Source' },
  { id: 'manager', label: 'Manager', disabled: true },
];

function TestLink({ to, children, ...props }: SegmentedTabsLinkProps) {
  return (
    <a href={to} {...props}>
      {children}
    </a>
  );
}

describe('SegmentedTabs', () => {
  it('preserves manual rail scroll when only badge content changes', () => {
    let railWidth = 100;
    const rail = {
      scrollLeft: 0,
      getBoundingClientRect: () => ({ left: 0, right: railWidth }),
    };
    let onResize: (() => void) | undefined;
    vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true);
    vi.stubGlobal('ResizeObserver', class {
      constructor(callback: () => void) {
        onResize = callback;
      }

      observe() {}
      disconnect() {}
    });

    const focusVisual = vi.fn();
    const onChange = vi.fn();
    const renderTabs = (count: number) => (
      <SegmentedTabs
        items={[
          { id: 'visual', label: 'Visual' },
          { id: 'source', label: <span>Source {count}</span> },
          { id: 'manager', label: 'Manager' },
        ]}
        activeTab="source"
        onChange={onChange}
        ariaLabel="Editor tabs"
        keepActiveVisible
      />
    );

    let renderer: ReactTestRenderer | undefined;
    try {
      act(() => {
        renderer = create(renderTabs(1), {
          createNodeMock: ({ props: elementProps }) => {
            const props = elementProps as { role?: string; id?: string };
            if (props.role === 'tablist') return rail;
            const isSource = props.id === 'segmented-tabs-source';
            const offset = isSource ? 120 : props.id === 'segmented-tabs-manager' ? 240 : 0;
            return {
              focus: isSource ? vi.fn() : focusVisual,
              getBoundingClientRect: () => ({
                left: offset - rail.scrollLeft,
                right: offset + 60 - rail.scrollLeft,
              }),
            };
          },
        });
      });
      expect(rail.scrollLeft).toBe(80);

      rail.scrollLeft = 140;
      act(() => renderer!.update(renderTabs(2)));
      expect(renderer!.root.findByType('span').children.join('')).toBe('Source 2');
      expect(rail.scrollLeft).toBe(140);

      expect(onResize).toBeDefined();
      railWidth = 90;
      onResize?.();
      expect(rail.scrollLeft).toBe(120);

      const sourceTab = renderer!.root.findByProps({ id: 'segmented-tabs-source' });
      act(() => sourceTab.props.onKeyDown({ key: 'Home', preventDefault: vi.fn() }));
      expect(focusVisual).toHaveBeenCalledWith({ preventScroll: true });
      expect(onChange).toHaveBeenCalledWith('visual');
      expect(rail.scrollLeft).toBe(0);
    } finally {
      act(() => renderer?.unmount());
      vi.unstubAllGlobals();
    }
  });

  it('renders one tab per item', () => {
    const markup = renderToStaticMarkup(
      <SegmentedTabs
        items={baseItems}
        activeTab="visual"
        onChange={() => {}}
        ariaLabel="Editor tabs"
      />
    );

    expect(markup.match(/role="tab"/g) ?? []).toHaveLength(3);
    expect(markup).toContain('role="tablist"');
    expect(markup).toContain('aria-label="Editor tabs"');
  });

  it('marks the active tab with aria-selected and tabIndex', () => {
    const markup = renderToStaticMarkup(
      <SegmentedTabs
        items={baseItems}
        activeTab="source"
        onChange={() => {}}
        ariaLabel="Editor tabs"
      />
    );

    expect(markup).toContain('id="segmented-tabs-source" aria-selected="true" tabindex="0"');
    expect(markup).toContain('id="segmented-tabs-visual" aria-selected="false" tabindex="-1"');
  });

  it('calls onChange when clicking an enabled inactive tab', () => {
    const changes: string[] = [];
    let renderer: ReactTestRenderer;

    act(() => {
      renderer = create(
        <SegmentedTabs
          items={baseItems}
          activeTab="visual"
          onChange={(tab) => changes.push(tab)}
          ariaLabel="Editor tabs"
        />
      );
    });

    const tabs = renderer!.root.findAllByProps({ role: 'tab' });

    act(() => {
      tabs[1].props.onClick({ preventDefault: () => {} });
    });

    expect(changes).toEqual(['source']);
  });

  it('does not call onChange for disabled tabs', () => {
    const changes: string[] = [];
    let renderer: ReactTestRenderer;

    act(() => {
      renderer = create(
        <SegmentedTabs
          items={baseItems}
          activeTab="visual"
          onChange={(tab) => changes.push(tab)}
          ariaLabel="Editor tabs"
        />
      );
    });

    const tabs = renderer!.root.findAllByProps({ role: 'tab' });

    act(() => {
      tabs[2].props.onClick({ preventDefault: () => {} });
    });

    expect(changes).toEqual([]);
  });

  it('renders link tabs when a link component is provided', () => {
    const markup = renderToStaticMarkup(
      <SegmentedTabs
        items={[
          { id: 'local', label: 'Local', to: '/codex-inspection' },
          { id: 'server', label: 'Server', to: '/codex-inspection/server' },
        ]}
        activeTab="server"
        ariaLabel="Inspection mode"
        linkComponent={TestLink}
      />
    );

    expect(markup).toContain('href="/codex-inspection/server"');
    expect(markup).toContain('aria-selected="true"');
  });
});
