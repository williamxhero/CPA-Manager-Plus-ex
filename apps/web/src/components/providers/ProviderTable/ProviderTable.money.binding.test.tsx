import { act, create, type ReactTestInstance, type ReactTestRenderer } from 'react-test-renderer';
import { beforeEach, expect, it, vi } from 'vitest';
import type { ResourceProtectionEvidence } from '@/features/resourceProtection/resourceProtection';
import type { ProviderMoneyView } from '../useProviderMoneySummaries';
import type { OfficialBalanceView } from '../useOfficialBalanceList';
import { buildProviderRows, type ProviderRow } from './rowData';
import { ProviderTable } from './ProviderTable';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (s: string) =>
      (
        ({
          'ai_providers.table_col_balance': '余额',
          'ai_providers.balance_exhausted_aria': '余额已耗尽',
          'resource_protection.unbound': '未验证资源身份关联',
        }) as Record<string, string>
      )[s] ?? s,
  }),
}));

const { moneyViews, officialViews } = vi.hoisted(() => ({
  moneyViews: {} as Record<string, ProviderMoneyView>,
  officialViews: {} as Record<string, OfficialBalanceView>,
}));

vi.mock('../useProviderMoneySummaries', () => ({
  useProviderMoneySummaries: (rows: ProviderRow[]) => {
    const views: Record<string, ProviderMoneyView> = {};
    for (const row of rows) {
      const view = moneyViews[row.key];
      if (view) views[row.key] = view;
    }
    return views;
  },
}));

vi.mock('../useOfficialBalanceList', () => ({
  useOfficialBalanceList: (rows: ProviderRow[]) => {
    const views: Record<string, OfficialBalanceView> = {};
    for (const row of rows) {
      const view = officialViews[row.key];
      if (view) views[row.key] = view;
    }
    return views;
  },
  clearOfficialBalanceCachesForTests: () => undefined,
}));

const UNBOUND_COPY = '未验证资源身份关联';

const rows = buildProviderRows({
  gemini: [],
  codex: [],
  claude: [],
  vertex: [],
  usageByProvider: new Map(),
  openai: [
    {
      name: 'A',
      baseUrl: 'https://synthetic.invalid',
      resourceId: 'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaa1',
      disabled: false,
      apiKeyEntries: [{ apiKey: 'synthetic' }],
    },
  ],
});

const moneyView = (...balances: Array<[string, string]>): ProviderMoneyView =>
  ({
    data: {
      funding: [{ balances: balances.map(([currency, total]) => ({ currency, total })) }],
    },
  }) as unknown as ProviderMoneyView;

const scope = {
  instance_id: '22222222-2222-2222-2222-222222222222',
  instance_incarnation: '33333333-3333-3333-3333-333333333333',
  boot_epoch: '44444444-4444-4444-4444-444444444444',
  snapshot_revision: 1,
};

const target = {
  resource_id: 'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaa1',
  incarnation: '11111111-1111-1111-1111-111111111111',
  identity_version: 1,
  credential_version: 1,
  config_version: 1,
  membership_version: 1,
};

const boundEvidence = (): ResourceProtectionEvidence => ({
  status: 'identity_only',
  binding: { scope, targets: [target] },
});
const unboundEvidence = (): ResourceProtectionEvidence => ({ status: 'unbound' });

const renderTable = (protection: () => ResourceProtectionEvidence) => {
  let renderer!: ReactTestRenderer;
  act(() => {
    renderer = create(
      <ProviderTable
        rows={rows}
        resourceProtectionForRow={protection}
        loading={false}
        actionsDisabled={false}
        toggleDisabled={false}
        resolvedTheme="light"
        emptyState={null}
        onShowDetail={() => {}}
        onEdit={() => {}}
        onDelete={() => {}}
        onToggle={() => {}}
        onPriorityChange={() => {}}
      />
    );
  });
  return renderer;
};

const nodeText = (node: ReactTestInstance): string =>
  node.children.map((child) => (typeof child === 'string' ? child : nodeText(child))).join('');

/** The balance cell of the rendered row, so a value cannot pass in another cell. */
const balanceCellText = (renderer: ReactTestRenderer) => {
  const row = renderer.root.findAll(
    (node) => node.type === 'div' && node.props.role === 'row' && node.props.tabIndex === 0
  );
  expect(row).toHaveLength(1);
  const summary = row[0].findAll(
    (node) => node.type === 'div' && node.props['aria-label'] === '余额'
  );
  expect(summary).toHaveLength(1);
  return nodeText(summary[0]);
};

beforeEach(() => {
  for (const key of Object.keys(moneyViews)) delete moneyViews[key];
  for (const key of Object.keys(officialViews)) delete officialViews[key];
});

it('renders the unbound wording instead of a placeholder when the row has no host-signed binding and no amount', () => {
  const renderer = renderTable(unboundEvidence);
  const cell = balanceCellText(renderer);
  expect(cell).toBe(UNBOUND_COPY);
  expect(cell).not.toContain('—');
  act(() => renderer.unmount());
});

it('keeps the amount on a row whose resource identity is bound', () => {
  moneyViews[rows[0].key] = moneyView(['USD', '12.5']);
  const renderer = renderTable(boundEvidence);
  const cell = balanceCellText(renderer);
  expect(cell).toBe('USD 12.50');
  expect(cell).not.toContain(UNBOUND_COPY);
  act(() => renderer.unmount());
});

it('keeps the existing unlimited copy on a row whose resource identity is bound', () => {
  officialViews[rows[0].key] = { text: 'unlimited' };
  const renderer = renderTable(boundEvidence);
  const cell = balanceCellText(renderer);
  expect(cell).toBe('unlimited');
  expect(cell).not.toContain(UNBOUND_COPY);
  act(() => renderer.unmount());
});
