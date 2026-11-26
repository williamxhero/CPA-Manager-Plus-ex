import { act } from 'react';
import { create, type ReactTestInstance, type ReactTestRenderer } from 'react-test-renderer';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { Button } from '@/components/ui/Button';
import { ToggleSwitch } from '@/components/ui/ToggleSwitch';
import type { ProviderKeyConfig } from '@/types';
import { buildRecentRequestCompositeKey } from '@/utils/recentRequests';
import { ProviderStatusBar } from '../ProviderStatusBar';
import type { ProviderRecentUsageMap } from '../utils';
import type { ProviderMoneyView } from '../useProviderMoneySummaries';
import { buildProviderRows, type ProviderRow } from './rowData';
import { filterAndSortProviderRows } from './sort';
import { ProviderTable } from './ProviderTable';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string) => key,
  }),
}));

const { moneyViews, officialViews } = vi.hoisted(() => ({
  moneyViews: {} as Record<string, unknown>,
  officialViews: {} as Record<string, unknown>,
}));

vi.mock('../useProviderMoneySummaries', () => ({
  useProviderMoneySummaries: (rows: ProviderRow[]) => {
    const views: Record<string, ProviderMoneyView> = {};
    for (const row of rows) {
      const view = moneyViews[row.key] as ProviderMoneyView | undefined;
      if (view) views[row.key] = view;
    }
    return views;
  },
}));

vi.mock('../useOfficialBalanceList', () => ({
  useOfficialBalanceList: (rows: ProviderRow[]) => {
    const views: Record<string, unknown> = {};
    for (const row of rows) {
      const view = officialViews[row.key];
      if (view) views[row.key] = view;
    }
    return views;
  },
  clearOfficialBalanceCachesForTests: () => undefined,
}));

const moneyView = (...balances: Array<[string, string]>): ProviderMoneyView =>
  ({
    data: {
      funding: [{ balances: balances.map(([currency, total]) => ({ currency, total })) }],
    },
  }) as unknown as ProviderMoneyView;

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

const balanceLines = (node: ReactTestInstance) =>
  node.findAll((child) => child.type === 'div' && child.props['data-balance-tone'] !== undefined);

const pauseGlyphs = (node: ReactTestInstance) =>
  node.findAll((child) => child.props['data-balance-paused'] !== undefined);

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
  beforeEach(() => {
    for (const key of Object.keys(moneyViews)) delete moneyViews[key];
    for (const key of Object.keys(officialViews)) delete officialViews[key];
  });

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
      resourceProtection?: 'unknown' | 'unavailable' | 'identity_only' | 'unsupported_mode';
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

  it('drops the automatic start/stop column from the header and every row', () => {
    const rows = filterAndSortProviderRows(
      buildProviderRows({ ...emptyInput, codex: codexConfigs })
    );
    const renderer = renderTable(rows);

    const headers = renderer.root.findAll(
      (node) => node.type === 'span' && node.props.role === 'columnheader'
    );
    expect(headers).toHaveLength(9);
    expect(getText(renderer.root as unknown as ReactTestInstance)).not.toContain(
      'ai_providers.auto_start_stop'
    );

    const renderedRows = getRows(renderer);
    expect(renderedRows).toHaveLength(4);
    for (const row of renderedRows) {
      expect(
        row.findAll((node) => node.props['data-provider-auto-state'] !== undefined)
      ).toHaveLength(0);
    }
    act(() => renderer.unmount());
  });

  it('marks an exhausted balance red with the pause glyph and keeps the manual switch independent', () => {
    const rows = buildProviderRows({ ...emptyInput, openai: [
      { name: 'native', baseUrl: 'https://api.deepseek.com', apiKeyEntries: [], nativeFunding: true, autoHeld: true, disabled: true },
    ] });
    moneyViews[rows[0].key] = moneyView(['USD', '0.00']);
    const onToggle = vi.fn();
    const renderer = renderTable(rows, { onToggle });
    const [row] = getRows(renderer);

    const [line] = balanceLines(row);
    expect(line.props['data-balance-tone']).toBe('nonPositive');
    expect(getText(line)).toBe('USD 0.00');
    const glyphs = pauseGlyphs(row);
    expect(glyphs).toHaveLength(1);
    expect(glyphs[0].props['aria-label']).toBe('ai_providers.balance_exhausted_aria');

    expect(
      row.findAll((node) => node.type === 'button' && node.props['aria-pressed'] !== undefined)
    ).toHaveLength(0);
    const manual = row.findByType(ToggleSwitch);
    expect(manual.props.checked).toBe(false);
    toggleSwitch(manual, true);
    expect(onToggle).toHaveBeenCalledWith(rows[0], true);
    act(() => renderer.unmount());
  });

  it('shows a positive balance in green without the pause glyph', () => {
    const rows = buildProviderRows({ ...emptyInput, openai: [
      { name: 'positive', baseUrl: 'https://positive.example/v1', apiKeyEntries: [] },
    ] });
    moneyViews[rows[0].key] = moneyView(['USD', '12.5']);
    const renderer = renderTable(rows);

    const [row] = getRows(renderer);
    const [line] = balanceLines(row);
    expect(line.props['data-balance-tone']).toBe('positive');
    expect(getText(line)).toBe('USD 12.50');
    expect(pauseGlyphs(row)).toHaveLength(0);
    act(() => renderer.unmount());
  });

  it('keeps a row without any known amount neutral', () => {
    const rows = buildProviderRows({ ...emptyInput, codex: codexConfigs });
    const renderer = renderTable(rows);

    for (const row of getRows(renderer)) {
      expect(balanceLines(row)).toHaveLength(0);
      expect(pauseGlyphs(row)).toHaveLength(0);
      const summary = row.findAll(
        (node) =>
          node.type === 'div' && node.props['aria-label'] === 'ai_providers.table_col_balance'
      );
      expect(summary).toHaveLength(1);
      expect(getText(summary[0])).toBe('—');
    }
    act(() => renderer.unmount());
  });

  it('does not read an uncountable official balance as zero or positive', () => {
    const rows = buildProviderRows({ ...emptyInput, openai: [
      { name: 'unlimited', baseUrl: 'https://unlimited.example/v1', apiKeyEntries: [] },
    ] });
    officialViews[rows[0].key] = { text: 'unlimited' };
    const renderer = renderTable(rows);

    const [row] = getRows(renderer);
    const [line] = balanceLines(row);
    expect(line.props['data-balance-tone']).toBe('unknown');
    expect(getText(line)).toBe('unlimited');
    expect(pauseGlyphs(row)).toHaveLength(0);
    act(() => renderer.unmount());
  });

  it('classifies the raw official amount instead of its rounded display text', () => {
    const rows = buildProviderRows({ ...emptyInput, openai: [
      { name: 'dust', baseUrl: 'https://dust.example/v1', apiKeyEntries: [] },
    ] });
    officialViews[rows[0].key] = { text: 'USD 0.00', amount: '0.004' };
    const renderer = renderTable(rows);

    const [row] = getRows(renderer);
    const [line] = balanceLines(row);
    expect(line.props['data-balance-tone']).toBe('positive');
    expect(getText(line)).toBe('USD 0.00');
    expect(pauseGlyphs(row)).toHaveLength(0);
    act(() => renderer.unmount());
  });

  it('colours each funding amount by its own sign and pauses none while one is positive', () => {
    const rows = buildProviderRows({ ...emptyInput, openai: [
      { name: 'mixed', baseUrl: 'https://mixed.example/v1', apiKeyEntries: [] },
    ] });
    moneyViews[rows[0].key] = moneyView(['USD', '0'], ['CNY', '8.12']);
    const renderer = renderTable(rows);

    const [row] = getRows(renderer);
    const lines = balanceLines(row);
    expect(lines).toHaveLength(2);
    expect(lines.map((line) => line.props['data-balance-tone'])).toEqual([
      'nonPositive',
      'positive',
    ]);
    expect(getText(lines[0])).toBe('USD 0.00');
    expect(getText(lines[1])).toBe('CNY 8.12');
    expect(pauseGlyphs(row)).toHaveLength(0);
    act(() => renderer.unmount());
  });

  it('shows the only pause glyph after the amounts when none is positive', () => {
    const rows = buildProviderRows({ ...emptyInput, openai: [
      { name: 'exhausted', baseUrl: 'https://exhausted.example/v1', apiKeyEntries: [] },
    ] });
    moneyViews[rows[0].key] = moneyView(['USD', '0'], ['CNY', '-2.5']);
    const renderer = renderTable(rows);

    const [row] = getRows(renderer);
    const lines = balanceLines(row);
    expect(lines).toHaveLength(2);
    expect(lines.map((line) => line.props['data-balance-tone'])).toEqual([
      'nonPositive',
      'nonPositive',
    ]);
    expect(getText(lines[0])).toBe('USD 0.00');
    expect(getText(lines[1])).toBe('CNY -2.50');
    const glyphs = pauseGlyphs(row);
    expect(glyphs).toHaveLength(1);
    expect(glyphs[0].props['aria-label']).toBe('ai_providers.balance_exhausted_aria');
    act(() => renderer.unmount());
  });

  it('only exposes the manual switch for key providers without Core funding', () => {
    const rows = buildProviderRows({ ...emptyInput, codex: codexConfigs });
    const renderer = renderTable(rows);
    for (const row of getRows(renderer)) {
      expect(row.findAllByType(ToggleSwitch)).toHaveLength(1);
      expect(row.findAll((node) => node.type === 'button' &&
        String(node.props['aria-label']).includes('resource_protection.'))).toHaveLength(0);
      expect(row.findAll((node) => node.type === 'button' &&
        node.props['aria-pressed'] !== undefined)).toHaveLength(0);
    }
    act(() => renderer.unmount());
  });

  it.each(['unknown', 'unavailable', 'identity_only', 'unsupported_mode'] as const)(
    'preserves keyboard actions and manual toggles with %s capability',
    (resourceProtection) => {
      const rows = buildProviderRows({ ...emptyInput, codex: [codexConfigs[1]] });
      const onShowDetail = vi.fn();
      const onToggle = vi.fn();
      const onEdit = vi.fn();
      const onDelete = vi.fn();
      const renderer = renderTable(rows, { onShowDetail, onToggle, onEdit, onDelete }, { resourceProtection });
      const row = getRows(renderer)[0];
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

  it('distinguishes unavailable and partial OpenAI statistics from no traffic', () => {
    const usageByProvider: ProviderRecentUsageMap = new Map([
      ['mixed-keyless', new Map([
        [
          buildRecentRequestCompositeKey('https://mixed-keyless.example/v1', 'tracked-key'),
          { success: 5, failed: 1, recentRequests: [] },
        ],
      ])],
    ]);
    const rows = buildProviderRows({
      ...emptyInput,
      openai: [
        {
          name: 'pure-keyless',
          baseUrl: 'https://pure-keyless.example/v1',
          apiKeyEntries: [],
        },
        {
          name: 'mixed-keyless',
          baseUrl: 'https://mixed-keyless.example/v1',
          apiKeyEntries: [
            { apiKey: 'tracked-key' },
            { apiKey: '', proxyUrl: 'socks5://proxy:1080' },
          ],
        },
      ],
      usageByProvider,
    });
    const renderer = renderTable(rows);
    const renderedRows = getRows(renderer);

    expect(getText(renderedRows[0])).toContain('ai_providers.openai_keys_count: 0');
    expect(getText(renderedRows[0])).toContain('ai_providers.usage_stats_unavailable');
    expect(getText(renderedRows[0])).not.toContain('status_bar.no_requests');

    expect(getText(renderedRows[1])).toContain('ai_providers.openai_keys_count: 1');
    expect(getText(renderedRows[1])).toContain('5');
    expect(getText(renderedRows[1])).toContain('ai_providers.usage_stats_partial');
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
