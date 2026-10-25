import { act } from 'react';
import { create, type ReactTestInstance, type ReactTestRenderer } from 'react-test-renderer';
import { describe, expect, it, vi } from 'vitest';
import { Button } from '@/components/ui/Button';
import { ToggleSwitch } from '@/components/ui/ToggleSwitch';
import type { ProviderKeyConfig } from '@/types';
import { buildRecentRequestCompositeKey } from '@/utils/recentRequests';
import { ProviderStatusBar } from '../ProviderStatusBar';
import type { ProviderRecentUsageMap } from '../utils';
import { buildProviderRows, type ProviderRow } from './rowData';
import { filterAndSortProviderRows } from './sort';
import { ProviderTable } from './ProviderTable';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string) => key,
  }),
}));

const emptyInput = {
  gemini: [],
  codex: [],
  claude: [],
  vertex: [],
  openai: [],
  usageByProvider: new Map() as ProviderRecentUsageMap,
};

const getRows = (renderer: ReactTestRenderer) =>
  renderer.root.findAll(
    (node) => node.type === 'div' && node.props.role === 'row' && node.props.tabIndex === 0
  );

const getText = (node: ReactTestInstance): string =>
  node.children.map((child) => (typeof child === 'string' ? child : getText(child))).join('');

const clickButton = (button: ReactTestInstance) => {
  const onClick = button.props.onClick as (() => void) | undefined;
  if (!onClick) throw new Error('Button click handler not found');

  act(() => {
    onClick();
  });
};

const toggleSwitch = (toggle: ReactTestInstance, value: boolean) => {
  const onChange = toggle.props.onChange as ((value: boolean) => void) | undefined;
  if (!onChange) throw new Error('Toggle change handler not found');

  act(() => {
    onChange(value);
  });
};

const changeInput = (input: ReactTestInstance, value: string) => {
  const onChange = input.props.onChange as
    | ((event: { target: { value: string } }) => void)
    | undefined;
  if (!onChange) throw new Error('Input change handler not found');

  act(() => {
    onChange({ target: { value } });
  });
};

const blurInput = (input: ReactTestInstance) => {
  const onBlur = input.props.onBlur as (() => void) | undefined;
  if (!onBlur) throw new Error('Input blur handler not found');

  act(() => {
    onBlur();
  });
};

const keyDownInput = (input: ReactTestInstance, key: string) => {
  const onKeyDown = input.props.onKeyDown as
    | ((event: {
        key: string;
        preventDefault: () => void;
        currentTarget: { blur: () => void };
      }) => void)
    | undefined;
  const onBlur = input.props.onBlur as (() => void) | undefined;
  if (!onKeyDown || !onBlur) throw new Error('Input keyboard handlers not found');

  act(() => {
    onKeyDown({
      key,
      preventDefault: vi.fn(),
      currentTarget: {
        blur: onBlur,
      },
    });
  });
};

const getPriorityEditTrigger = (row: ReactTestInstance) => {
  const trigger = row
    .findAll((node) => node.type === 'button')
    .find(
      (button) =>
        button.props.type === 'button' &&
        button.props['aria-label'] === 'ai_providers.priority_edit'
    );
  if (!trigger) throw new Error('Priority edit trigger not found');
  return trigger;
};

const getPriorityInput = (row: ReactTestInstance) => {
  const input = getPriorityInputs(row)[0];
  if (!input) throw new Error('Priority input not found');
  return input;
};

const getPriorityInputs = (row: ReactTestInstance) =>
  row
    .findAll((node) => node.type === 'input')
    .filter((node) => node.props['aria-label'] === 'ai_providers.priority_edit');

const openPriorityEditor = (renderer: ReactTestRenderer, rowIndex = 0) => {
  clickButton(getPriorityEditTrigger(getRows(renderer)[rowIndex]));
  return getPriorityInput(getRows(renderer)[rowIndex]);
};

describe('ProviderTable', () => {
  const codexConfigs: ProviderKeyConfig[] = [
    { apiKey: 'low-key', baseUrl: 'https://low.example.com/v1', priority: 1 },
    {
      apiKey: 'disabled-key',
      baseUrl: 'https://disabled.example.com/v1',
      priority: 99,
      excludedModels: ['*'],
    },
    { apiKey: 'high-key', baseUrl: 'https://high.example.com/v1', priority: 9 },
    { apiKey: 'unset-key', baseUrl: 'https://unset.example.com/v1' },
  ];

  const renderTable = (
    rows: ProviderRow[],
    handlers: {
      onShowDetail?: (row: ProviderRow) => void;
      onEdit?: (row: ProviderRow) => void;
      onDelete?: (row: ProviderRow) => void;
      onToggle?: (row: ProviderRow, enabled: boolean) => void;
      onPriorityChange?: (row: ProviderRow, priority: number) => void;
    } = {},
    options: {
      actionsDisabled?: boolean;
      toggleDisabled?: boolean;
      resourceProtection?: 'unknown' | 'unavailable';
    } = {}
  ) => {
    let renderer!: ReactTestRenderer;
    act(() => {
      renderer = create(
        <ProviderTable
          rows={rows}
          resourceProtection={options.resourceProtection}
          loading={false}
          actionsDisabled={options.actionsDisabled ?? false}
          toggleDisabled={options.toggleDisabled ?? false}
          resolvedTheme="light"
          emptyState={<div>empty</div>}
          onShowDetail={handlers.onShowDetail ?? (() => {})}
          onEdit={handlers.onEdit ?? (() => {})}
          onDelete={handlers.onDelete ?? (() => {})}
          onToggle={handlers.onToggle ?? (() => {})}
          onPriorityChange={handlers.onPriorityChange ?? (() => {})}
        />
      );
    });
    return renderer;
  };

  it('places an unknown automatic protection capability before every name, including disabled rows', () => {
    const rows = buildProviderRows({ ...emptyInput, codex: codexConfigs });
    const renderer = renderTable(rows);
    for (const row of getRows(renderer)) {
      const indicator = row.findAll((node) => node.type === 'button' &&
        String(node.props['aria-label']).includes('resource_protection.unknown'));
      expect(indicator).toHaveLength(1);
      const identity = row.findAll((node) => node.type === 'div' && node.props.role === 'cell')[1];
      expect(identity.findAllByType('button')).toContain(indicator[0]);
      expect(getText(identity)).not.toContain('normal');
      // Informational tooltip is a sibling of the provider name, never a nested button.
      expect(indicator[0].findAllByType('button')).toEqual([indicator[0]]);
    }
    act(() => renderer.unmount());
  });

  it.each(['unknown', 'unavailable'] as const)(
    'keeps %s capability informational while preserving row keyboard, actions and disabled-row manual toggle',
    (resourceProtection) => {
      const rows = buildProviderRows({ ...emptyInput, codex: [codexConfigs[1]] });
      const onShowDetail = vi.fn();
      const onToggle = vi.fn();
      const onEdit = vi.fn();
      const onDelete = vi.fn();
      const renderer = renderTable(rows, { onShowDetail, onToggle, onEdit, onDelete }, { resourceProtection });
      const row = getRows(renderer)[0];
      const indicator = row.findAll((node) => node.type === 'button' &&
        String(node.props['aria-label']).includes(`resource_protection.${resourceProtection}`))[0];
      // Bubble host events as React would, through the tooltip boundary to the row.
      const bubble = (name: string, key?: string) => {
        let stopped = false;
        let current: ReactTestInstance | null = indicator;
        while (current && !stopped) {
          if (typeof current.type === 'string') {
            current.props[name]?.({ key, target: indicator, currentTarget: current,
              preventDefault: vi.fn(), stopPropagation: () => { stopped = true; } });
          }
          current = current.parent;
        }
        expect(stopped).toBe(true);
      };
      act(() => { bubble('onClick'); bubble('onKeyDown', 'Enter'); bubble('onKeyDown', ' '); });
      expect(indicator.props['aria-expanded']).toBe(true);
      expect(onShowDetail).not.toHaveBeenCalled();
      expect(row.findAll((node) => node.type === 'span' && node.props.title === rows[0].label)[0].children)
        .toEqual([rows[0].label]);
      for (const key of ['Enter', ' ']) {
        act(() => row.props.onKeyDown({ key, target: row, currentTarget: row, preventDefault: vi.fn() }));
      }
      expect(onShowDetail).toHaveBeenCalledTimes(2);
      const toggle = row.findByType(ToggleSwitch);
      expect(toggle.props.disabled).toBe(false);
      toggleSwitch(toggle, true);
      expect(onToggle).toHaveBeenCalledWith(rows[0], true);
      clickButton(row.findAllByType(Button).find((button) => button.props['aria-label'] === 'common.edit')!);
      clickButton(row.findAllByType(Button).find((button) => button.props['aria-label'] === 'common.delete')!);
      expect(onEdit).toHaveBeenCalledWith(rows[0]);
      expect(onDelete).toHaveBeenCalledWith(rows[0]);
      act(() => renderer.unmount());
    }
  );

  it('keeps sorted row actions mapped to original config indexes', () => {
    const rows = filterAndSortProviderRows(
      buildProviderRows({ ...emptyInput, codex: codexConfigs })
    );
    const onEdit = vi.fn();
    const onToggle = vi.fn();
    const onShowDetail = vi.fn();

    const renderer = renderTable(rows, { onEdit, onToggle, onShowDetail });

    const renderedRows = getRows(renderer);
    expect(renderedRows).toHaveLength(4);

    // 默认按优先级降序：high-key(9) 在最前，停用行排最后
    expect(getText(renderedRows[0])).toContain('https://high.example.com/v1');
    expect(getText(renderedRows[renderedRows.length - 1])).toContain(
      'https://disabled.example.com/v1'
    );

    const editButton = renderedRows[0]
      .findAllByType(Button)
      .find((button) => button.props['aria-label'] === 'common.edit');
    expect(editButton).toBeTruthy();
    clickButton(editButton!);
    expect(onEdit).toHaveBeenLastCalledWith(
      expect.objectContaining({ kind: 'codex', originalIndex: 2 })
    );

    toggleSwitch(renderedRows[0].findByType(ToggleSwitch), false);
    expect(onToggle).toHaveBeenLastCalledWith(
      expect.objectContaining({ kind: 'codex', originalIndex: 2 }),
      false
    );

    // 行点击打开详情
    act(() => {
      renderedRows[0].props.onClick();
    });
    expect(onShowDetail).toHaveBeenLastCalledWith(
      expect.objectContaining({ kind: 'codex', originalIndex: 2 })
    );
  });

  it('marks disabled rows and renders disabled toggle state', () => {
    const rows = filterAndSortProviderRows(
      buildProviderRows({ ...emptyInput, codex: codexConfigs })
    );
    const renderer = renderTable(rows);

    const lastRow = getRows(renderer)[3];

    const lastToggle = lastRow.findByType(ToggleSwitch);
    expect(lastToggle.props.checked).toBe(false);
  });

  it('shows priority as an edit trigger before entering edit mode', () => {
    const rows = filterAndSortProviderRows(
      buildProviderRows({ ...emptyInput, codex: codexConfigs })
    );
    const renderer = renderTable(rows);

    const firstRow = getRows(renderer)[0];
    const priorityTrigger = getPriorityEditTrigger(firstRow);
    expect(getText(priorityTrigger)).toBe('9');
    expect(getPriorityInputs(firstRow)).toHaveLength(0);

    const priorityInput = openPriorityEditor(renderer);
    expect(priorityInput.props.value).toBe('9');
  });

  it('commits a direct priority edit on blur without opening row detail', () => {
    const rows = filterAndSortProviderRows(
      buildProviderRows({ ...emptyInput, codex: codexConfigs })
    );
    const onPriorityChange = vi.fn();
    const onShowDetail = vi.fn();
    const renderer = renderTable(rows, { onPriorityChange, onShowDetail });

    const priorityInput = openPriorityEditor(renderer);

    changeInput(priorityInput, '42');
    blurInput(priorityInput);

    expect(onPriorityChange).toHaveBeenLastCalledWith(
      expect.objectContaining({ kind: 'codex', originalIndex: 2 }),
      42
    );
    expect(onShowDetail).not.toHaveBeenCalled();
  });

  it('commits a direct priority edit with Enter only once', () => {
    const rows = filterAndSortProviderRows(
      buildProviderRows({ ...emptyInput, codex: codexConfigs })
    );
    const onPriorityChange = vi.fn();
    const renderer = renderTable(rows, { onPriorityChange });

    const priorityInput = openPriorityEditor(renderer);

    changeInput(priorityInput, '12');
    keyDownInput(priorityInput, 'Enter');

    expect(onPriorityChange).toHaveBeenCalledTimes(1);
    expect(onPriorityChange).toHaveBeenLastCalledWith(
      expect.objectContaining({ kind: 'codex', originalIndex: 2 }),
      12
    );
  });

  it('cancels a direct priority edit with Escape without committing on blur', () => {
    const rows = filterAndSortProviderRows(
      buildProviderRows({ ...emptyInput, codex: codexConfigs })
    );
    const onPriorityChange = vi.fn();
    const renderer = renderTable(rows, { onPriorityChange });

    const priorityInput = openPriorityEditor(renderer);

    changeInput(priorityInput, '12');
    keyDownInput(priorityInput, 'Escape');

    const updatedTrigger = getPriorityEditTrigger(getRows(renderer)[0]);
    expect(getText(updatedTrigger)).toBe('9');
    expect(onPriorityChange).not.toHaveBeenCalled();
  });

  it('restores blank or invalid direct priority drafts without committing', () => {
    const rows = filterAndSortProviderRows(
      buildProviderRows({ ...emptyInput, codex: codexConfigs })
    );
    const onPriorityChange = vi.fn();
    const renderer = renderTable(rows, { onPriorityChange });

    let priorityInput = openPriorityEditor(renderer);

    changeInput(priorityInput, '');
    blurInput(priorityInput);
    let priorityTrigger = getPriorityEditTrigger(getRows(renderer)[0]);
    expect(getText(priorityTrigger)).toBe('9');

    priorityInput = openPriorityEditor(renderer);
    changeInput(priorityInput, 'not-a-number');
    blurInput(priorityInput);
    priorityTrigger = getPriorityEditTrigger(getRows(renderer)[0]);
    expect(getText(priorityTrigger)).toBe('9');
    expect(onPriorityChange).not.toHaveBeenCalled();
  });

  it('does not submit unchanged direct priority edits', () => {
    const rows = filterAndSortProviderRows(
      buildProviderRows({ ...emptyInput, codex: codexConfigs })
    );
    const onPriorityChange = vi.fn();
    const renderer = renderTable(rows, { onPriorityChange });

    const priorityInput = openPriorityEditor(renderer);

    changeInput(priorityInput, '9');
    blurInput(priorityInput);

    expect(onPriorityChange).not.toHaveBeenCalled();
  });

  it('disables inline priority controls with the rest of row actions', () => {
    const rows = filterAndSortProviderRows(
      buildProviderRows({ ...emptyInput, codex: codexConfigs })
    );
    const renderer = renderTable(rows, {}, { actionsDisabled: true });

    const firstRow = getRows(renderer)[0];
    const priorityTrigger = getPriorityEditTrigger(firstRow);

    expect(priorityTrigger.props.disabled).toBe(true);
    expect(getPriorityInputs(firstRow)).toHaveLength(0);
  });

  it('renders the provided empty state when there are no rows', () => {
    const renderer = renderTable([]);
    expect(getText(renderer.root as unknown as ReactTestInstance)).toContain('empty');
  });

  it('shows a placeholder instead of the status bar for zero-traffic rows', () => {
    const usageByProvider: ProviderRecentUsageMap = new Map([
      [
        'codex',
        new Map([
          [
            buildRecentRequestCompositeKey('https://high.example.com/v1', 'high-key'),
            { success: 82, failed: 6, recentRequests: [] },
          ],
        ]),
      ],
    ]);
    const rows = filterAndSortProviderRows(
      buildProviderRows({ ...emptyInput, codex: codexConfigs, usageByProvider })
    );
    const renderer = renderTable(rows);

    const renderedRows = getRows(renderer);
    // high-key 行有流量：渲染统计与状态条
    expect(getText(renderedRows[0])).toContain('82');
    expect(renderedRows[0].findAllByType(ProviderStatusBar)).toHaveLength(1);
    expect(getText(renderedRows[0])).not.toContain('status_bar.no_requests');

    // 其余零流量行：仅占位文本，不渲染状态条
    expect(getText(renderedRows[1])).toContain('status_bar.no_requests');
    expect(renderedRows[1].findAllByType(ProviderStatusBar)).toHaveLength(0);
  });
});
