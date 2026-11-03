import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, expect, it, vi } from 'vitest';
import { ProviderTable } from './ProviderTable';
import { buildProviderRows } from './rowData';
import type { BalanceBinding } from '@/services/api/officialBalance';
import { providerPolicyRequest } from '@/services/api/providerPolicy';

vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (s: string) => ({
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
} as Record<string, string>)[s] ?? s }) }));
const binding = (id: string): BalanceBinding => ({
  scope: { instance_id: 'instance', instance_incarnation: 'incarnation', boot_epoch: 'boot', snapshot_revision: 1 },
  targets: [id, `${id}-key`].map(resource_id => ({ resource_id, incarnation: 'v1', identity_version: 1, credential_version: 1, config_version: 1, membership_version: 1 })),
});
const rows = buildProviderRows({ gemini: [], codex: [], claude: [], vertex: [], usageByProvider: new Map(), openai:
  ['A', 'B'].map(name => ({ name, baseUrl: 'https://synthetic.invalid', resourceId: name, disabled: name === 'B', apiKeyEntries: [{ apiKey: 'synthetic' }] })) });
const status = (id: string) => ({ ...binding(id), providerId: id, enabled: id === 'A', effective: false,
  generation: 1, bindingCurrent: true, source: 'explicit', locked: true, manualDisabled: id === 'B',
  ownPaused: id === 'A', applied: id === 'A', state: id === 'A' ? 'paused' : 'off', reason: 'fixture', updatedAtMs: 1700000000000,
  sources: [{ scope: binding(id).scope, targets: [binding(id).targets[1]], state: 'success', stale: false,
    lastSuccess: { observedAtMs: 1700000000000, balances: [{ currency: id === 'A' ? 'USD' : 'CNY', total: id === 'A' ? '0.0500' : '8.123456789012345678' }] } }] });
const props = { rows, loading: false, actionsDisabled: false, toggleDisabled: false, resolvedTheme: 'light',
  emptyState: null, onShowDetail: vi.fn(), onEdit: vi.fn(), onDelete: vi.fn(), onToggle: vi.fn(), onPriorityChange: vi.fn(),
  moneyStatus: { base: 'http://synthetic-manager', managementKey: 'admin', bindingForRow: (r: typeof rows[number]) => binding(r.raw.resourceId!) } };
let view: ReactTestRenderer | undefined;
const text = () => JSON.stringify(view?.toJSON());
afterEach(async () => { if (view) await act(async () => view?.unmount()); view = undefined; vi.unstubAllGlobals(); vi.useRealTimers(); });

it('shows live native per-member money and independent manual/policy state on every issued row without opening details', async () => {
  const fetcher = vi.fn(async (url: string, init: RequestInit) => {
    expect(init.method).toBe('GET');
    return Response.json(status(new URL(url).searchParams.get('providerId')!));
  });
  vi.stubGlobal('fetch', fetcher);
  await act(async () => { view = create(<ProviderTable {...props} />); });
  expect(text()).toContain('USD 0.0500');
  expect(text()).toContain('CNY 8.123456789012345678');
  expect(text()).toContain('人工关闭');
  expect(text()).toContain('本策略已确认暂停');
  expect(text()).toContain('2023-11-14T22:13:20.000Z');
  expect(fetcher).toHaveBeenCalledTimes(2);
  expect(props.onShowDetail).not.toHaveBeenCalled();
  expect(props.onToggle).not.toHaveBeenCalled();
});

it('retains values while refreshing, marks failed reads unverified, and replaces with successful empty sources', async () => {
  vi.useFakeTimers();
  let mode = 'success';
  let release!: (r: Response) => void;
  vi.stubGlobal('fetch', vi.fn(async (url: string) => {
    const id = new URL(url).searchParams.get('providerId')!;
    if (mode === 'pending') return new Promise<Response>(r => { release = r; });
    if (mode === 'fail') throw new Error('upstream synthetic');
    return Response.json(mode === 'empty' ? { ...status(id), sources: [] } : status(id));
  }));
  const one = { ...props, rows: rows.slice(0, 1) };
  await act(async () => { view = create(<ProviderTable {...one} />); });
  mode = 'pending';
  await act(async () => { await vi.advanceTimersByTimeAsync(15000); });
  expect(text()).toContain('USD 0.0500');
  expect(text()).toContain('上次状态未确认');
  mode = 'fail';
  await act(async () => { release(Response.error()); });
  expect(text()).toContain('USD 0.0500');
  expect(text()).toContain('上次状态未确认');
  mode = 'empty';
  await act(async () => { await vi.advanceTimersByTimeAsync(15000); });
  expect(text()).not.toContain('USD 0.0500');
  expect(text()).toContain('金额未知');
  expect(text()).not.toContain('上次状态未确认');
});

it.each(['auth', 'base', 'binding', 'capability', 'page'])(
  'hides prior scope and rejects a late response after %s changes including return to the original scope', async change => {
    vi.useFakeTimers();
    let pending = false;
    let release!: (r: Response) => void;
    const fetcher = vi.fn(async (url: string) => pending
      ? new Promise<Response>(r => { release = r; })
      : Response.json(status(new URL(url).searchParams.get('providerId')!)));
    vi.stubGlobal('fetch', fetcher);
    const one = { ...props, rows: rows.slice(0, 1) };
    await act(async () => { view = create(<ProviderTable {...one} />); });
    expect(text()).toContain('USD 0.0500');
    pending = true;
    await act(async () => { await vi.advanceTimersByTimeAsync(15000); });
    const old = release;
    const changed = { ...one, moneyStatus: { ...one.moneyStatus,
      ...(change === 'auth' ? { managementKey: 'other' } : {}),
      ...(change === 'base' ? { base: 'http://other' } : {}),
      ...(change === 'capability' ? { disabled: true } : {}),
      ...(change === 'binding' ? { bindingForRow: () => ({ ...binding('A'), scope: { ...binding('A').scope, boot_epoch: 'new' } }) } : {}),
    }, ...(change === 'page' ? { rows: rows.slice(1) } : {}) };
    await act(async () => { view!.update(<ProviderTable {...changed} />); });
    expect(text()).not.toContain('USD 0.0500');
    await act(async () => { view!.update(<ProviderTable {...one} />); old(Response.json(status('A'))); });
    expect(text()).not.toContain('USD 0.0500');
  }
);

it('bounds concurrent reads, times out blocked rows, and cancels only its own requests on unmount', async () => {
  vi.useFakeTimers();
  const extra = buildProviderRows({ gemini: [], codex: [], claude: [], vertex: [], usageByProvider: new Map(), openai:
    ['A','B','C','D','E'].map(name => ({ name, baseUrl: 'https://synthetic.invalid', apiKeyEntries: [], resourceId: name })) });
  const signals: AbortSignal[] = [];
  const fetcher = vi.fn(async (_url: string, init: RequestInit) => {
    signals.push(init.signal as AbortSignal);
    return new Promise<Response>(() => {});
  });
  vi.stubGlobal('fetch', fetcher);
  await act(async () => { view = create(<ProviderTable {...props} rows={extra} />); });
  expect(fetcher).toHaveBeenCalledTimes(2);
  await act(async () => { await vi.advanceTimersByTimeAsync(12000); });
  expect(fetcher).toHaveBeenCalledTimes(4);
  expect(signals.slice(0, 2).every(s => s.aborted)).toBe(true);
  expect(text()).toContain('读取失败');
  await act(async () => { view!.unmount(); }); view = undefined;
  expect(signals.every(s => s.aborted)).toBe(true);
  await act(async () => { await vi.advanceTimersByTimeAsync(60000); });
  expect(fetcher).toHaveBeenCalledTimes(4);
});

it('keeps missing and non-finite money unknown, stale lastSuccess visible and rejects foreign versions', async () => {
  vi.useFakeTimers();
  let mismatch = false;
  vi.stubGlobal('fetch', vi.fn(async (url: string) => {
    const id = new URL(url).searchParams.get('providerId')!;
    const value = status(id);
    return Response.json({ ...value, sources: [{ ...value.sources[0], state: 'failed', stale: true,
      ...(mismatch ? { targets: [{ ...binding(id).targets[1], credential_version: 9 }] } : {}),
      lastSuccess: { observedAtMs: 1700000000000, balances: [{ currency: 'USD', total: 'Infinity' }, { currency: 'USD' }] } }] });
  }));
  await act(async () => { view = create(<ProviderTable {...props} />); });
  expect(text()).toContain('陈旧');
  expect(text()).toContain('failed');
  expect(text()).toContain('金额未知');
  expect(text()).not.toContain('USD 0');
  mismatch = true;
  await act(async () => { await vi.advanceTimersByTimeAsync(15000); });
  expect(text()).toContain('上次状态未确认');
});

it('queries neither unissued rows nor unavailable management service', async () => {
  const fetcher = vi.fn(); vi.stubGlobal('fetch', fetcher);
  await act(async () => { view = create(<ProviderTable {...props} moneyStatus={{ ...props.moneyStatus, bindingForRow: () => undefined }} />); });
  expect(text()).toContain('资金状态未知');
  await act(async () => { view!.update(<ProviderTable {...props} moneyStatus={{ ...props.moneyStatus, disabled: true }} />); });
  expect(fetcher).not.toHaveBeenCalled();
});

it('invalidates reads overlapping a policy write and revalidates without reviving the old result', async () => {
  vi.useFakeTimers();
  let reads = 0;
  let updated = false;
  let release!: (r: Response) => void;
  vi.stubGlobal('fetch', vi.fn(async (_url: string, init: RequestInit) => {
    if (init.method === 'PUT') updated = true;
    else if (++reads === 2) return new Promise<Response>(r => { release = r; });
    return Response.json({ ...status('A'), enabled: !updated, sources: updated ? [] : status('A').sources });
  }));
  await act(async () => { view = create(<ProviderTable {...props} rows={rows.slice(0, 1)} />); });
  await act(async () => { await vi.advanceTimersByTimeAsync(15000); });
  await act(async () => { await providerPolicyRequest(props.moneyStatus.base, 'admin', binding('A'), 'A', new AbortController().signal, { enabled: false, expectedGeneration: 1 }); });
  await act(async () => { release(Response.json(status('A'))); });
  expect(text()).toContain('策略已关闭');
  expect(text()).not.toContain('策略已启用');
  expect(text()).not.toContain('USD 0.0500');
});
