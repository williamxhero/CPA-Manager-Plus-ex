import { act, create, type ReactTestInstance, type ReactTestRenderer } from 'react-test-renderer';
import { beforeEach, expect, it, vi } from 'vitest';
import type { ResourceProtectionEvidence } from '@/features/resourceProtection/resourceProtection';
import type { ProviderMoneyView } from '../useProviderMoneySummaries';
import type { OfficialBalanceView } from '../useOfficialBalanceList';
import { buildProviderRows, type ProviderRow } from './rowData';
import { ProviderTable } from './ProviderTable';

// Resolve the copy from the shipped locale file, so the test fails if the component ever
// stops reusing these keys or the keys lose their wording.
vi.mock('react-i18next', async () => {
  const locale = (await import('@/i18n/locales/zh-CN.json')).default;
  const dictionary: Record<string, string> = {
    'ai_providers.table_col_balance': locale.ai_providers.table_col_balance,
    'ai_providers.money_summary_amount_unknown': locale.ai_providers.money_summary_amount_unknown,
    'ai_providers.balance_exhausted_aria': locale.ai_providers.balance_exhausted_aria,
    'resource_protection.unbound': locale.resource_protection.unbound,
    'resource_protection.unbound_description': locale.resource_protection.unbound_description,
  };
  return { useTranslation: () => ({ t: (key: string) => dictionary[key] ?? key }) };
});

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

/**
 * The unbound wording and its explanation are only read once the module above has loaded the
 * locale, so the expectations are taken from the same file at runtime.
 */
const localePromise = import('@/i18n/locales/zh-CN.json').then((module) => module.default);

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
const unknownEvidence = (): ResourceProtectionEvidence => ({ status: 'unknown' });

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

/** The balance summary of the rendered row, so a value cannot pass in another cell. */
const balanceSummary = (renderer: ReactTestRenderer): ReactTestInstance => {
  const row = renderer.root.findAll(
    (node) => node.type === 'div' && node.props.role === 'row' && node.props.tabIndex === 0
  );
  expect(row).toHaveLength(1);
  const summary = row[0].findAll(
    (node) => node.type === 'div' && node.props['aria-label'] === '余额'
  );
  expect(summary).toHaveLength(1);
  return summary[0];
};

const balanceCellText = (renderer: ReactTestRenderer) => nodeText(balanceSummary(renderer));

beforeEach(() => {
  for (const key of Object.keys(moneyViews)) delete moneyViews[key];
  for (const key of Object.keys(officialViews)) delete officialViews[key];
});

it('renders the unbound wording instead of a placeholder when the row has no host-signed binding and no amount', async () => {
  const locale = await localePromise;
  const renderer = renderTable(unboundEvidence);
  const cell = balanceCellText(renderer);
  expect(cell).toBe(locale.resource_protection.unbound);
  expect(cell).not.toContain('—');
  act(() => renderer.unmount());
});

it('explains the unbound wording with the existing description on hover and to assistive tech', async () => {
  const locale = await localePromise;
  const renderer = renderTable(unboundEvidence);
  const summary = balanceSummary(renderer);
  const wording = summary.findAll(
    (node) => node.type === 'span' && nodeText(node) === locale.resource_protection.unbound
  );
  expect(wording).toHaveLength(1);
  expect(wording[0].props.title).toBe(locale.resource_protection.unbound_description);
  expect(wording[0].props['aria-label']).toBe(
    locale.resource_protection.unbound + ' ' + locale.resource_protection.unbound_description
  );
  act(() => renderer.unmount());
});

it('labels the amount unknown while the capability probe has not settled', async () => {
  const renderer = renderTable(unknownEvidence);
  const cell = balanceCellText(renderer);
  expect(cell).toBe((await localePromise).ai_providers.money_summary_amount_unknown);
  act(() => renderer.unmount());
});

it('labels the amount unknown when the capability is unavailable or unsupported', async () => {
  const unknown = (await localePromise).ai_providers.money_summary_amount_unknown;
  for (const status of ['unavailable', 'unsupported_mode'] as const) {
    const renderer = renderTable(() => ({ status }));
    expect(balanceCellText(renderer)).toBe(unknown);
    act(() => renderer.unmount());
  }
});

it('keeps the amount on a row whose resource identity is bound', async () => {
  const locale = await localePromise;
  moneyViews[rows[0].key] = moneyView(['USD', '12.5']);
  const renderer = renderTable(boundEvidence);
  const cell = balanceCellText(renderer);
  expect(cell).toBe('USD 12.50');
  expect(cell).not.toContain(locale.resource_protection.unbound);
  act(() => renderer.unmount());
});

it('keeps the existing unlimited copy on a row whose resource identity is bound', async () => {
  const locale = await localePromise;
  officialViews[rows[0].key] = { text: 'unlimited' };
  const renderer = renderTable(boundEvidence);
  const cell = balanceCellText(renderer);
  expect(cell).toBe('unlimited');
  expect(cell).not.toContain(locale.resource_protection.unbound);
  act(() => renderer.unmount());
});
