import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, expect, it, vi } from 'vitest';
import { ProviderTable } from './ProviderTable';
import { buildProviderRows } from './rowData';
import type { BalanceBinding } from '@/services/api/officialBalance';
import { saveProviderPolicy } from '@/services/api/providerPolicy';
import { beforeEach } from 'vitest';
import { setRuntimeControlsForTests } from '@/utils/quota/runtimeControls';
import {
  CACHED_BALANCE_SETTLE_MS,
  clearOfficialBalanceCachesForTests,
} from '../useOfficialBalanceList';

beforeEach(() => {
  clearOfficialBalanceCachesForTests();
  setRuntimeControlsForTests({ quotaRefreshIntervalMs: 15000, quotaFetchedAtMs: 0 });
});

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (s: string) =>
      ({
        'ai_providers.money_summary_aria': '资金实时状态摘要',
        'ai_providers.money_summary_pending': '正在核验 · 上次状态未确认',
        'ai_providers.money_summary_failed': '读取失败 · 上次状态未确认',
        'ai_providers.money_summary_unknown': '资金状态未知',
        'ai_providers.money_summary_enabled': '策略已启用',
        'ai_providers.money_summary_disabled': '策略已关闭',
        'ai_providers.money_summary_explicit': '显式设置',
        'ai_providers.money_summary_default_off': '默认关闭',
        'ai_providers.money_summary_locked': '已锁定',
        'ai_providers.money_summary_unlocked': '未锁定',
        'ai_providers.money_summary_effective': '可执行',
        'ai_providers.money_summary_not_ready': '策略未就绪',
        'ai_providers.money_summary_own_paused': '本策略已确认暂停',
        'ai_providers.money_summary_own_recovered': '本策略已确认恢复',
        'ai_providers.money_summary_unconfirmed': '门禁结果未确认',
        'ai_providers.money_summary_manual_disabled': '人工关闭',
        'ai_providers.money_summary_manual_open': '无人工关闭',
        'ai_providers.money_summary_updated': '更新时间：2023-11-14T22:13:20.000Z',
        'ai_providers.money_summary_source': '来源：{{id}}',
        'ai_providers.money_summary_stale': '陈旧',
        'ai_providers.money_summary_current': '当前',
        'ai_providers.money_summary_freshness_unknown': '新鲜度未知',
        'ai_providers.money_summary_last_success': 'lastSuccess：2023-11-14T22:13:20.000Z',
        'ai_providers.money_summary_amount_unknown': '金额未知',
        'ai_providers.money_summary_source_unverified': '来源未验证',
      } as Record<string, string>)[s] ?? s,
  }),
}));

const IDS = {
  A: 'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaa1',
  B: 'bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbb2',
  C: 'cccccccc-cccc-cccc-cccc-ccccccccccc3',
  D: 'dddddddd-dddd-dddd-dddd-ddddddddddd4',
  E: 'eeeeeeee-eeee-eeee-eeee-eeeeeeeeeee5',
  KA: 'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaa6',
  KB: 'bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbb7',
  KC: 'cccccccc-cccc-cccc-cccc-ccccccccccc8',
  KD: 'dddddddd-dddd-dddd-dddd-ddddddddddd9',
  KE: 'eeeeeeee-eeee-eeee-eeee-eeeeeeeeeee0',
} as const;

const target = (resource_id: string) => ({
  resource_id,
  incarnation: '11111111-1111-1111-1111-111111111111',
  identity_version: 1,
  credential_version: 1,
  config_version: 1,
  membership_version: 1,
});

const binding = (id: keyof typeof IDS, key: keyof typeof IDS): BalanceBinding => ({
  scope: {
    instance_id: '22222222-2222-2222-2222-222222222222',
    instance_incarnation: '33333333-3333-3333-3333-333333333333',
    boot_epoch: '44444444-4444-4444-4444-444444444444',
    snapshot_revision: 1,
  },
  targets: [target(IDS[id]), target(IDS[key])],
});

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
      resourceId: IDS.A,
      disabled: false,
      apiKeyEntries: [{ apiKey: 'synthetic' }],
    },
    {
      name: 'B',
      baseUrl: 'https://synthetic.invalid',
      resourceId: IDS.B,
      disabled: true,
      apiKeyEntries: [{ apiKey: 'synthetic' }],
    },
  ],
});

const status = (id: keyof typeof IDS, key: keyof typeof IDS) => {
  const body = binding(id, key);
  const member = body.targets[1];
  return {
    ...body,
    providerId: IDS[id],
    subjectId: `subject-${id}`,
    owner: 'provider-balance',
    enabled: id === 'A',
    effective: false,
    generation: 1,
    bindingCurrent: true,
    source: 'explicit',
    locked: true,
    manualDisabled: id === 'B',
    ownPaused: id === 'A',
    applied: id === 'A',
    state: id === 'A' ? 'paused' : 'off',
    reason: 'fixture',
    updatedAtMs: 1700000000000,
    funding: [
      {
        memberIds: [member.resource_id],
        keyMatched: true,
        fresh: id === 'A',
        reason: id === 'A' ? 'success' : 'failed',
        query: { ...body, version: 1, targets: [member] },
        observedAtMs: 1700000000000,
        balances: [
          {
            currency: id === 'A' ? 'USD' : 'CNY',
            total: id === 'A' ? '0.0500' : '8.123456789012345678',
          },
        ],
      },
    ],
  };
};

const props = {
  rows,
  loading: false,
  actionsDisabled: false,
  toggleDisabled: false,
  resolvedTheme: 'light',
  emptyState: null,
  onShowDetail: vi.fn(),
  onEdit: vi.fn(),
  onDelete: vi.fn(),
  onToggle: vi.fn(),
  onPriorityChange: vi.fn(),
  moneyStatus: {
    base: 'http://synthetic-manager',
    managementKey: 'admin',
    bindingForRow: (r: (typeof rows)[number]) =>
      r.raw.resourceId === IDS.A ? binding('A', 'KA') : binding('B', 'KB'),
  },
};
let view: ReactTestRenderer | undefined;
const text = () => JSON.stringify(view?.toJSON());
afterEach(async () => {
  if (view) await act(async () => view?.unmount());
  view = undefined;
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

it('shows live native per-member money and independent manual/policy state on every issued row without opening details', async () => {
  const fetcher = vi.fn(async (url: string, init: RequestInit) => {
    expect(init.method).toBe('GET');
    const id = new URL(url).searchParams.get('providerId')!;
    return Response.json(id === IDS.A ? status('A', 'KA') : status('B', 'KB'));
  });
  vi.stubGlobal('fetch', fetcher);
  await act(async () => {
    view = create(<ProviderTable {...props} />);
  });
  expect(text()).toContain('USD 0.05');
  expect(text()).toContain('CNY 8.12');
  expect(text()).not.toContain('人工关闭');
  expect(text()).not.toContain('本策略已确认暂停');
  expect(text()).not.toContain('provider_policy.unavailable');
  expect(fetcher).toHaveBeenCalledTimes(5);
  expect(props.onShowDetail).not.toHaveBeenCalled();
  expect(props.onToggle).not.toHaveBeenCalled();
});

it('retains values while refreshing, marks failed reads unverified, and replaces with successful empty funding', async () => {
  vi.useFakeTimers();
  let mode = 'success';
  let release!: (r: Response) => void;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      if (!String(url).includes('provider-policy')) return Response.json({});
      const id = new URL(url).searchParams.get('providerId')!;
      const body = id === IDS.A ? status('A', 'KA') : status('B', 'KB');
      if (mode === 'pending') return new Promise<Response>((r) => {
        release = r;
      });
      if (mode === 'fail') throw new Error('upstream synthetic');
      return Response.json(mode === 'empty' ? { ...body, funding: [] } : body);
    })
  );
  const one = { ...props, rows: rows.slice(0, 1) };
  await act(async () => {
    view = create(<ProviderTable {...one} />);
  });
  mode = 'pending';
  await act(async () => {
    await vi.advanceTimersByTimeAsync(15000);
  });
  expect(text()).toContain('USD 0.05');
  mode = 'fail';
  await act(async () => {
    release(Response.error());
  });
  expect(text()).toContain('USD 0.05');
  mode = 'empty';
  await act(async () => {
    await vi.advanceTimersByTimeAsync(15000);
  });
  expect(text()).not.toContain('USD 0.05');
  expect(text()).toContain('—');
});

it.each(['auth', 'base', 'binding', 'capability', 'page'])(
  'hides prior scope and rejects a late response after %s changes including return to the original scope',
  async (change) => {
    vi.useFakeTimers();
    let pending = false;
    let release!: (r: Response) => void;
    const fetcher = vi.fn(async (url: string) =>
      pending
        ? new Promise<Response>((r) => {
            release = r;
          })
        : Response.json(
            new URL(url).searchParams.get('providerId') === IDS.A
              ? status('A', 'KA')
              : status('B', 'KB')
          )
    );
    vi.stubGlobal('fetch', fetcher);
    const one = { ...props, rows: rows.slice(0, 1) };
    await act(async () => {
      view = create(<ProviderTable {...one} />);
    });
    expect(text()).toContain('USD 0.05');
    pending = true;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(15000);
    });
    const old = release;
    const changed = {
      ...one,
      moneyStatus: {
        ...one.moneyStatus,
        ...(change === 'auth' ? { managementKey: 'other' } : {}),
        ...(change === 'base' ? { base: 'http://other' } : {}),
        ...(change === 'capability' ? { disabled: true } : {}),
        ...(change === 'binding'
          ? {
              bindingForRow: () => ({
                ...binding('A', 'KA'),
                scope: { ...binding('A', 'KA').scope, boot_epoch: '55555555-5555-5555-5555-555555555555' },
              }),
            }
          : {}),
      },
      ...(change === 'page' ? { rows: rows.slice(1) } : {}),
    };
    await act(async () => {
      view!.update(<ProviderTable {...changed} />);
    });
    expect(text()).not.toContain('USD 0.05');
    await act(async () => {
      view!.update(<ProviderTable {...one} />);
      old(Response.json(status('A', 'KA')));
    });
    expect(text()).not.toContain('USD 0.05');
  }
);

it('bounds concurrent reads, times out blocked rows, and cancels only its own requests on unmount', async () => {
  vi.useFakeTimers();
  const extra = buildProviderRows({
    gemini: [],
    codex: [],
    claude: [],
    vertex: [],
    usageByProvider: new Map(),
    openai: (['A', 'B', 'C', 'D', 'E'] as const).map((name) => ({
      name,
      baseUrl: 'https://synthetic.invalid',
      apiKeyEntries: [],
      resourceId: IDS[name],
    })),
  });
  const signals: AbortSignal[] = [];
  const fetcher = vi.fn(async (_url: string, init: RequestInit) => {
    signals.push(init.signal as AbortSignal);
    return new Promise<Response>(() => {});
  });
  vi.stubGlobal('fetch', fetcher);
  await act(async () => {
    view = create(
      <ProviderTable
        {...props}
        rows={extra}
        moneyStatus={{
          ...props.moneyStatus,
          bindingForRow: (r) => {
            const name = (Object.keys(IDS) as (keyof typeof IDS)[]).find(
              (k) => IDS[k] === r.raw.resourceId && k.length === 1
            );
            return name
              ? binding(name, (`K${name}` as keyof typeof IDS))
              : undefined;
          },
        }}
      />
    );
  });
  expect(fetcher).toHaveBeenCalledTimes(5);
  await act(async () => {
    await vi.advanceTimersByTimeAsync(12000);
  });
  expect(fetcher).toHaveBeenCalledTimes(9);
  expect(signals.filter((signal) => signal.aborted).length).toBeGreaterThanOrEqual(4);
  expect(text()).not.toContain('读取失败');
  await act(async () => {
    view!.unmount();
  });
  view = undefined;
  expect(signals.every((s) => s.aborted)).toBe(true);
  await act(async () => {
    await vi.advanceTimersByTimeAsync(60000);
  });
  expect(fetcher).toHaveBeenCalledTimes(9);
});

it('keeps missing and non-finite money unknown, stale funding visible and rejects foreign versions', async () => {
  vi.useFakeTimers();
  let mismatch = false;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      const id = new URL(url).searchParams.get('providerId')!;
      const value = id === IDS.A ? status('A', 'KA') : status('B', 'KB');
      const source = value.funding[0];
      if (mismatch) {
        // Foreign credential version fails the typed status fence → read error.
        return Response.json({
          ...value,
          funding: [
            {
              ...source,
              query: {
                ...source.query,
                targets: [{ ...source.query.targets[0], credential_version: 9 }],
              },
            },
          ],
          subjectId: '',
        });
      }
      return Response.json({
        ...value,
        funding: [
          {
            ...source,
            fresh: false,
            reason: 'failed',
            // Omit balances: unknown amount. Non-finite totals are dropped by
            // the typed funding reader rather than rendered as zero.
            balances: undefined,
          },
        ],
      });
    })
  );
  await act(async () => {
    view = create(<ProviderTable {...props} />);
  });
  expect(text()).toContain('—');
  expect(text()).not.toContain('USD 0');
  mismatch = true;
  await act(async () => {
    await vi.advanceTimersByTimeAsync(15000);
  });
  expect(text()).not.toContain('USD 0');
});

it('queries neither unissued rows nor unavailable management service', async () => {
  const fetcher = vi.fn();
  vi.stubGlobal('fetch', fetcher);
  await act(async () => {
    view = create(
      <ProviderTable
        {...props}
        moneyStatus={{ ...props.moneyStatus, bindingForRow: () => undefined }}
      />
    );
  });
  expect(text()).toContain('—');
  expect(fetcher).toHaveBeenCalledTimes(1);
  expect(String(fetcher.mock.calls[0][0])).toContain('/usage-service/full-refresh');
  await act(async () => {
    view!.update(
      <ProviderTable {...props} moneyStatus={{ ...props.moneyStatus, disabled: true }} />
    );
  });
  expect(fetcher).toHaveBeenCalledTimes(1);
});

it('invalidates reads overlapping a policy write and revalidates without reviving the old result', async () => {
  vi.useFakeTimers();
  let reads = 0;
  let updated = false;
  let release!: (r: Response) => void;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_url: string, init: RequestInit) => {
      if (init.method === 'PUT') {
        updated = true;
        return Response.json({ ...status('A', 'KA'), enabled: false, funding: [] });
      }
      if (++reads === 2)
        return new Promise<Response>((r) => {
          release = r;
        });
      return Response.json({
        ...status('A', 'KA'),
        enabled: !updated,
        funding: updated ? [] : status('A', 'KA').funding,
      });
    })
  );
  await act(async () => {
    view = create(<ProviderTable {...props} rows={rows.slice(0, 1)} />);
  });
  await act(async () => {
    await vi.advanceTimersByTimeAsync(15000);
  });
  await act(async () => {
    await saveProviderPolicy(
      props.moneyStatus.base,
      'admin',
      { ...binding('A', 'KA'), providerId: IDS.A },
      1,
      false,
      new AbortController().signal
    );
  });
  await act(async () => {
    release(Response.json(status('A', 'KA')));
  });
  expect(text()).not.toContain('USD 0.05');
});

it('shows a fresh cached balance without requesting full-refresh', async () => {
  setRuntimeControlsForTests({
    quotaRefreshIntervalMs: 3_600_000,
    quotaFetchedAtMs: Date.now(),
    balanceTexts: {
      [`openai:${IDS.A}`]: 'USD 4.56',
      [`openai:${IDS.B}`]: 'CNY 1.25',
    },
  });
  const fetcher = vi.fn(async () => Response.json({}));
  vi.stubGlobal('fetch', fetcher);
  await act(async () => {
    view = create(<ProviderTable {...props} />);
  });
  expect(text()).toContain('USD 4.56');
  expect(text()).toContain('CNY 1.25');
  expect(fetcher.mock.calls.some((call) => String(call[0]).includes('/usage-service/full-refresh'))).toBe(
    false
  );
});

it('refreshes a cached insufficient balance before the interval elapses', async () => {
  setRuntimeControlsForTests({
    quotaRefreshIntervalMs: 3_600_000,
    quotaFetchedAtMs: Date.now(),
    balanceTexts: {
      [`openai:${IDS.A}`]: 'USD 0.00',
      [`openai:${IDS.B}`]: 'CNY 1.25',
    },
  });
  const fetcher = vi.fn(async (url: string, init?: RequestInit) => {
    const href = String(url);
    if (init?.method === 'POST' && href.includes('/usage-service/official-balance')) {
      return Response.json({
        id: 'job-1',
        state: 'complete',
        status: {
          adapter: 'deepinfra',
          lastSuccess: { queryStatus: 'success', currency: 'USD', amountDecimal: '2.50' },
        },
      });
    }
    if (href.includes('/usage-service/official-balance')) {
      return Response.json({
        adapter: 'deepinfra',
        configured: true,
        managerDirectQuery: true,
        version: 1,
        lastSuccess: { queryStatus: 'success', currency: 'USD', amountDecimal: '0.00' },
      });
    }
    return Response.json({});
  });
  vi.stubGlobal('fetch', fetcher);
  await act(async () => {
    view = create(<ProviderTable {...props} />);
  });
  expect(text()).toContain('USD 2.50');
  expect(text()).toContain('CNY 1.25');
  const urls = fetcher.mock.calls.map((call) => String(call[0]));
  expect(urls.some((url) => url.includes('/usage-service/full-refresh'))).toBe(false);
  expect(
    fetcher.mock.calls.some(
      (call) =>
        String(call[0]).includes('/usage-service/official-balance') && call[1]?.method === 'POST'
    )
  ).toBe(true);
});

it('keeps the cached balance when the binding arrives after the first paint', async () => {
  vi.useFakeTimers();
  let issue = false;
  setRuntimeControlsForTests({
    quotaRefreshIntervalMs: 3_600_000,
    quotaFetchedAtMs: Date.now(),
    balanceTexts: {
      [`openai:${IDS.A}`]: 'USD 0.00',
      [`openai:${IDS.B}`]: 'CNY 1.25',
    },
  });
  const issued = (r: (typeof rows)[number]) =>
    issue ? (r.raw.resourceId === IDS.A ? binding('A', 'KA') : binding('B', 'KB')) : undefined;
  const fetcher = vi.fn(async (url: string, init?: RequestInit) => {
    const href = String(url);
    if (init?.method === 'POST' && href.includes('/usage-service/official-balance')) {
      return Response.json({
        id: 'job-1',
        state: 'complete',
        status: {
          adapter: 'deepinfra',
          lastSuccess: { queryStatus: 'success', currency: 'USD', amountDecimal: '2.50' },
        },
      });
    }
    if (href.includes('/usage-service/official-balance')) {
      return Response.json({
        adapter: 'deepinfra',
        configured: true,
        managerDirectQuery: true,
        version: 1,
        lastSuccess: { queryStatus: 'success', currency: 'USD', amountDecimal: '0.00' },
      });
    }
    return Response.json({});
  });
  vi.stubGlobal('fetch', fetcher);
  const fullRefresh = () =>
    fetcher.mock.calls.some((call) => String(call[0]).includes('/usage-service/full-refresh'));
  await act(async () => {
    view = create(
      <ProviderTable {...props} moneyStatus={{ ...props.moneyStatus, bindingForRow: issued }} />
    );
  });
  expect(text()).toContain('USD 0.00');
  expect(text()).toContain('CNY 1.25');
  await act(async () => {
    await vi.advanceTimersByTimeAsync(CACHED_BALANCE_SETTLE_MS - 1);
  });
  expect(fullRefresh()).toBe(false);
  issue = true;
  await act(async () => {
    view!.update(
      <ProviderTable {...props} moneyStatus={{ ...props.moneyStatus, bindingForRow: issued }} />
    );
  });
  await act(async () => {
    await vi.advanceTimersByTimeAsync(2000);
  });
  expect(text()).toContain('USD 2.50');
  expect(text()).toContain('CNY 1.25');
  expect(fullRefresh()).toBe(false);
  expect(
    fetcher.mock.calls.some(
      (call) =>
        String(call[0]).includes('/usage-service/official-balance') && call[1]?.method === 'POST'
    )
  ).toBe(true);
});

it('refreshes a cached insufficient balance with no binding before the quota interval', async () => {
  vi.useFakeTimers();
  setRuntimeControlsForTests({
    quotaRefreshIntervalMs: 3_600_000,
    quotaFetchedAtMs: Date.now(),
    balanceTexts: {
      [`openai:${IDS.A}`]: 'USD 0.00',
      [`openai:${IDS.B}`]: 'CNY 1.25',
    },
  });
  const fetcher = vi.fn(async (url: string, init?: RequestInit) => {
    const href = String(url);
    if (init?.method === 'POST' && href.includes('/usage-service/full-refresh')) {
      return Response.json({
        id: 'job-1',
        state: 'complete',
        items: [
          {
            request: { kind: 'official', official: {} },
            targets: [target(IDS.A)],
            state: 'complete',
            stale: false,
            lastSuccess: { queryStatus: 'success', currency: 'USD', amountDecimal: '2.50' },
          },
        ],
      });
    }
    if (href.includes('/usage-service/full-refresh')) {
      return Response.json({
        sources: [
          {
            id: 'source-a',
            queryCapable: true,
            request: { kind: 'official', official: { adapter: 'deepinfra' } },
            targets: [target(IDS.A)],
            state: 'idle',
            stale: true,
            lastSuccess: { queryStatus: 'success', currency: 'USD', amountDecimal: '0.00' },
          },
        ],
      });
    }
    return Response.json({});
  });
  vi.stubGlobal('fetch', fetcher);
  const fullRefresh = () =>
    fetcher.mock.calls.filter((call) => String(call[0]).includes('/usage-service/full-refresh'));
  await act(async () => {
    view = create(
      <ProviderTable
        {...props}
        moneyStatus={{ ...props.moneyStatus, bindingForRow: () => undefined }}
      />
    );
  });
  expect(text()).toContain('USD 0.00');
  expect(fullRefresh()).toHaveLength(0);
  await act(async () => {
    await vi.advanceTimersByTimeAsync(CACHED_BALANCE_SETTLE_MS);
  });
  expect(text()).toContain('USD 2.50');
  expect(text()).toContain('CNY 1.25');
  expect(fullRefresh().some((call) => call[1]?.method === 'POST')).toBe(true);
});

it('keeps a cached insufficient balance when that source cannot be queried directly', async () => {
  setRuntimeControlsForTests({
    quotaRefreshIntervalMs: 3_600_000,
    quotaFetchedAtMs: Date.now(),
    balanceTexts: {
      [`openai:${IDS.A}`]: 'USD 0.00',
      [`openai:${IDS.B}`]: 'CNY 1.25',
    },
  });
  const fetcher = vi.fn(async (url: string) => {
    if (String(url).includes('/usage-service/official-balance')) {
      return Response.json({
        adapter: 'deepinfra',
        configured: false,
        managerDirectQuery: false,
        version: 1,
        lastSuccess: { queryStatus: 'success', currency: 'USD', amountDecimal: '0.00' },
      });
    }
    return Response.json({ sources: [] });
  });
  vi.stubGlobal('fetch', fetcher);
  await act(async () => {
    view = create(<ProviderTable {...props} />);
  });
  expect(text()).toContain('USD 0.00');
  expect(text()).toContain('CNY 1.25');
  expect(fetcher.mock.calls.some((call) => String(call[0]).includes('/usage-service/full-refresh'))).toBe(
    false
  );
});

it('does not read the inventory while bindings are still loading', async () => {
  vi.useFakeTimers();
  setRuntimeControlsForTests({
    quotaRefreshIntervalMs: 3_600_000,
    quotaFetchedAtMs: Date.now(),
    balanceTexts: {
      [`openai:${IDS.A}`]: 'USD 0.00',
      [`openai:${IDS.B}`]: 'CNY 1.25',
    },
  });
  const fetcher = vi.fn(async () => Response.json({ sources: [] }));
  vi.stubGlobal('fetch', fetcher);
  const fullRefresh = () =>
    fetcher.mock.calls.some((call) => String(call[0]).includes('/usage-service/full-refresh'));
  await act(async () => {
    view = create(
      <ProviderTable
        {...props}
        moneyStatus={{
          ...props.moneyStatus,
          bindingForRow: () => undefined,
          bindingsSettled: false,
        }}
      />
    );
  });
  expect(text()).toContain('USD 0.00');
  await act(async () => {
    await vi.advanceTimersByTimeAsync(CACHED_BALANCE_SETTLE_MS + 1000);
  });
  expect(fullRefresh()).toBe(false);
});
