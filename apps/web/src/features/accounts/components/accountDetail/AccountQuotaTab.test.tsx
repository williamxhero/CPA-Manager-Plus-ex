import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type {
  AccountDetailQuotaWindow,
  AccountDetailViewModel,
} from '@/features/accounts/model/accountDetailViewModel';
import { AccountQuotaTab } from './AccountQuotaTab';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

vi.mock('react-i18next', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-i18next')>();
  return {
    ...actual,
    useTranslation: () => ({
      t: (key: string, options?: Record<string, unknown>) => {
        if (key === 'codex_quota.reset_credit_expiry_remaining_less_than_minute') {
          return '<1 min';
        }
        if (key === 'codex_quota.reset_credit_expiry_item') {
          return `Credit ${options?.index ?? ''}`;
        }
        if (!options) return key;
        const params = Object.entries(options)
          .map(([name, value]) => `${name}=${String(value)}`)
          .join(',');
        return `${key}:${params}`;
      },
      i18n: {
        language: 'en',
      },
    }),
  };
});

const readText = (value: unknown): string => {
  if (typeof value === 'string' || typeof value === 'number') return String(value);
  if (Array.isArray(value)) return value.map(readText).join('');
  if (value && typeof value === 'object' && 'children' in value) {
    return readText((value as { children?: unknown }).children);
  }
  return '';
};

describe('AccountQuotaTab quota units', () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it('renders account-wide used fractions instead of remaining while preserving other quota values', () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date(2024, 1, 15, 12));
    const makeWindow = (
      overrides: Partial<AccountDetailQuotaWindow>
    ): AccountDetailQuotaWindow => ({
      key: 'five-hour',
      label: '5H',
      kind: 'five_hour',
      usedPercent: 60,
      remainingPercent: 40,
      resetLabel: '-',
      resetAtMs: null,
      resetAccuracy: 'unknown',
      windowMode: 'unknown',
      modelScope: { kind: 'all', complete: true },
      usage: null,
      currentUsage: null,
      previousUsage: null,
      previousPeriod: null,
      forecast: null,
      ...overrides,
    });
    const detailView = {
      identity: { provider: 'test' },
      quota: {
        windows: [
          makeWindow({}),
          makeWindow({ key: 'weekly', kind: 'weekly' }),
          makeWindow({ key: 'monthly', kind: 'monthly' }),
          makeWindow({ key: 'missing', kind: 'weekly', usedPercent: null }),
          makeWindow({ key: 'billing', kind: 'billing', amountLabel: '$100 / $250' }),
          makeWindow({
            key: 'model',
            modelScope: { kind: 'models', models: ['test-model'], complete: true },
          }),
        ],
        resetCreditsAvailableCount: null,
        resetCreditExpiries: [],
        cooldown: null,
      },
      history: null,
    } as unknown as AccountDetailViewModel;
    let renderer!: ReactTestRenderer;
    act(() => {
      renderer = create(
        <AccountQuotaTab
          detailView={detailView}
          windowUsageError=""
          historyAvailable={false}
          historyRefreshing={false}
          onRefreshHistory={vi.fn()}
          onResetQuota={vi.fn()}
          resetQuotaDisabled={false}
        />
      );
    });
    const standard = renderer.root.findByProps({ 'data-quota-window-group': 'standard' });
    expect(standard.findAllByProps({ 'data-quota-used-fraction': 'true' }).map(readText)).toEqual([
      'accounts.detail_used: 3.0/5',
      'accounts.detail_used: 4.2/7',
      'accounts.detail_used: 17.4/29',
      'accounts.detail_used: -',
    ]);
    expect(readText(standard)).not.toContain('40%');
    const other = renderer.root.findByProps({ 'data-quota-window-group': 'other' });
    expect(readText(other)).toContain('accounts.detail_used: 60%');
    expect(readText(other)).toContain('$100 / $250');
    const model = renderer.root.findByProps({ 'data-quota-window-group': 'model' });
    expect(model.findAllByProps({ 'data-quota-used-fraction': 'true' })).toHaveLength(0);
    expect(readText(model)).toContain('40%');
    act(() => renderer.unmount());
  });
});

describe('AccountQuotaTab timer crossing expiry', () => {
  const baseNow = 1_000_000;

  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(baseNow);
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  const makeDetailView = (
    expiresAtMs: number,
    availableCount: number | null = 1
  ): AccountDetailViewModel =>
    ({
      identity: {
        rowKey: 'codex.json\u0000auth-1',
        name: 'codex.json',
        provider: 'codex',
        type: 'codex',
        authIndex: 'auth-1',
        account: 'test@example.com',
      },
      quota: {
        windows: [],
        resetCreditsAvailableCount: availableCount,
        resetCreditExpiries: [{ id: 'credit-1', expiresAtMs }],
        cooldown: null,
      },
      history: null,
    } as unknown as AccountDetailViewModel);

  it('hides expired credit row when fake timer crosses expiry without altering reset records panel existence', () => {
    const expiresAtMs = baseNow + 30_000; // 30 seconds later
    const detailView = makeDetailView(expiresAtMs, 1);

    let renderer!: ReactTestRenderer;
    act(() => {
      renderer = create(
        <AccountQuotaTab
          detailView={detailView}
          windowUsageError=""
          historyAvailable={false}
          historyRefreshing={false}
          onRefreshHistory={vi.fn()}
          onResetQuota={vi.fn()}
          resetQuotaDisabled={false}
        />
      );
    });

    // 1. Initial render: credit-1 row is visible with "<1 min"
    const expiryElementBefore = renderer.root.findAllByProps({
      'data-quota-reset-credit-expiry': 'credit-1',
    });
    expect(expiryElementBefore).toHaveLength(1);
    expect(expiryElementBefore[0].children.join('')).toContain('<1 min');

    // Reset records panel is visible
    expect(
      renderer.root.findAllByProps({ 'data-account-quota-reset-records': 'true' })
    ).toHaveLength(1);
    expect(
      renderer.root.findByProps({ 'data-quota-reset-action': 'true' })
    ).toBeTruthy();

    // 2. Advance fake timer by 60s (interval fires, nowMs advances to baseNow + 60_000 > expiresAtMs)
    act(() => {
      vi.advanceTimersByTime(60_000);
    });

    // 3. Expiry row should now be gone because nowMs > expiresAtMs
    const expiryElementAfter = renderer.root.findAllByProps({
      'data-quota-reset-credit-expiry': 'credit-1',
    });
    expect(expiryElementAfter).toHaveLength(0);

    // 4. Reset records panel and reset action button remain visible because count is still known (1)
    expect(
      renderer.root.findAllByProps({ 'data-account-quota-reset-records': 'true' })
    ).toHaveLength(1);
    expect(
      renderer.root.findByProps({ 'data-quota-reset-action': 'true' })
    ).toBeTruthy();
  });
});
