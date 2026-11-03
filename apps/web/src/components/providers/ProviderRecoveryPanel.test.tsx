import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, expect, it, vi } from 'vitest';
import { ProviderMoneyPanel } from './ProviderMoneyPanel';

const binding = {
  scope: {
    instance_id: 'instance',
    instance_incarnation: 'incarnation',
    boot_epoch: 'boot',
    snapshot_revision: 1,
  },
  targets: ['provider', 'key'].map((resource_id) => ({
    resource_id,
    incarnation: 'version',
    identity_version: 1,
    credential_version: 1,
    config_version: 1,
    membership_version: 1,
  })),
};
const initial = {
  ...binding,
  providerId: 'provider',
  enabled: false,
  effective: false,
  generation: 2,
  source: 'explicit',
  bindingCurrent: true,
  locked: true,
  manualDisabled: false,
  ownPaused: true,
  applied: true,
  state: 'historical_pause',
  reason: 'explicit_recovery_required',
  updatedAtMs: 1793700000000,
  sources: [],
  recoveryAvailable: true,
  coordination: {
    generation: 1,
    sourceVersion: 'a'.repeat(64),
    receiptPersisted: true,
    state: 'paused',
  },
};
const props = {
  base: 'http://synthetic-manager',
  managementKey: 'admin-a',
  binding,
  providerId: 'provider',
};
let view: ReactTestRenderer | undefined;
afterEach(async () => {
  if (view) await act(async () => view?.unmount());
  view = undefined;
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  vi.useRealTimers();
});

it.each(['generation', 'gateGeneration', 'sourceVersion'])(
  'requires a new confirmation when the observed %s changes',
  async (field) => {
    vi.useFakeTimers();
    let state = initial;
    const fetcher = vi.fn(async () => Response.json(state));
    vi.stubGlobal('fetch', fetcher);
    await act(async () => { view = create(<ProviderMoneyPanel {...props} />); });
    await act(async () => { button('恢复本策略历史暂停').props.onClick(); });
    const oldConfirm = button('确认仅恢复本策略暂停').props.onClick;
    state = {
      ...initial,
      generation: field === 'generation' ? 3 : 2,
      coordination: {
        ...initial.coordination,
        generation: field === 'gateGeneration' ? 2 : 1,
        sourceVersion: field === 'sourceVersion' ? 'b'.repeat(64) : 'a'.repeat(64),
      },
    };
    await act(async () => { await vi.advanceTimersByTimeAsync(15000); });
    expect(view!.root.findAllByProps({ role: 'alertdialog' })).toHaveLength(0);
    await act(async () => { oldConfirm(); });
    expect(fetcher.mock.calls).toHaveLength(2); // GET only, no stale POST.
  }
);

it('reads back unknown recovery without retrying the command', async () => {
  let written = false;
  const fetcher = vi.fn(async (_url: string, init: RequestInit) => {
    if (init.method === 'POST') { written = true; return Response.json({}, { status: 503 }); }
    return Response.json(written ? {
      ...initial, applied: false, recoveryAvailable: false, state: 'recovery_pending',
      coordination: { ...initial.coordination, generation: 2, receiptPersisted: false },
    } : initial);
  });
  vi.stubGlobal('fetch', fetcher);
  await act(async () => { view = create(<ProviderMoneyPanel {...props} />); });
  await act(async () => { button('恢复本策略历史暂停').props.onClick(); });
  await act(async () => { button('确认仅恢复本策略暂停').props.onClick(); });
  expect(text()).toContain('恢复结果未确认');
  expect(text()).not.toContain('已显式恢复本策略暂停');
  expect(button('恢复本策略历史暂停').props.disabled).toBe(true);
  expect(fetcher.mock.calls.filter((c) => c[1].method === 'POST')).toHaveLength(1);
  expect(fetcher.mock.calls.filter((c) => c[1].method === 'GET')).toHaveLength(2);
});

it.each(['managementKey', 'disabled', 'providerId', 'base', 'binding'])(
  'invalidates confirmation through %s A to B to A, including a retained click',
  async (field) => {
    const fetcher = vi.fn(async () => Response.json(initial));
    vi.stubGlobal('fetch', fetcher);
    await act(async () => { view = create(<ProviderMoneyPanel {...props} />); });
    await act(async () => { button('恢复本策略历史暂停').props.onClick(); });
    const oldConfirm = button('确认仅恢复本策略暂停').props.onClick;
    const next = { ...props, [field]: field === 'disabled' ? true : field === 'binding' ? {
      ...binding, scope: { ...binding.scope, boot_epoch: 'new-boot' },
    } : 'changed' };
    await act(async () => { view!.update(<ProviderMoneyPanel {...next} />); });
    await act(async () => { view!.update(<ProviderMoneyPanel {...props} />); });
    expect(view!.root.findAllByProps({ role: 'alertdialog' })).toHaveLength(0);
    await act(async () => { oldConfirm(); });
    expect(fetcher.mock.calls.length).toBe(field === 'disabled' ? 2 : 3);
  }
);

it('discards a late recovery response after auth changes and supports cancellation', async () => {
  let resolve!: (r: Response) => void;
  const fetcher = vi.fn(async (_url: string, init: RequestInit) => init.method === 'POST'
    ? new Promise<Response>((r) => { resolve = r; }) : Response.json(initial));
  vi.stubGlobal('fetch', fetcher);
  await act(async () => { view = create(<ProviderMoneyPanel {...props} />); });
  await act(async () => { button('恢复本策略历史暂停').props.onClick(); });
  await act(async () => { button('取消恢复').props.onClick(); });
  expect(view!.root.findAllByProps({ role: 'alertdialog' })).toHaveLength(0);
  expect(fetcher.mock.calls.filter((c) => c[1].method === 'POST')).toHaveLength(0);
  await act(async () => { button('恢复本策略历史暂停').props.onClick(); });
  await act(async () => { button('确认仅恢复本策略暂停').props.onClick(); });
  await act(async () => { view!.update(<ProviderMoneyPanel {...props} managementKey="admin-b" />); });
  await act(async () => { resolve(Response.json({ ...initial, ownPaused: false, state: 'recovered_off' })); });
  expect(text()).not.toContain('已显式恢复本策略暂停');
  expect(text()).toContain('保留历史暂停');
});
const text = () => JSON.stringify(view?.toJSON());
const button = (label: string) =>
  view!.root.findAllByType('button').find((b) => b.children.join('') === label)!;

it('requires explicit confirmation and sends a fenced recovery without enabling policy or optimistic success', async () => {
  let resolve!: (r: Response) => void;
  const fetcher = vi.fn(async (_url: string, init: RequestInit) =>
    init.method === 'POST'
      ? new Promise<Response>((r) => {
          resolve = r;
        })
      : Response.json(initial)
  );
  vi.stubGlobal('fetch', fetcher);
  await act(async () => {
    view = create(<ProviderMoneyPanel {...props} />);
  });
  await act(async () => {
    button('恢复本策略历史暂停').props.onClick();
  });
  expect(fetcher.mock.calls.filter((c) => c[1].method === 'POST')).toHaveLength(0);
  expect(text()).toContain('人工关闭和其他策略限制将保留');
  await act(async () => {
    button('确认仅恢复本策略暂停').props.onClick();
  });
  const [url, request] = fetcher.mock.calls.find((c) => c[1].method === 'POST')!;
  expect(url).toBe('http://synthetic-manager/usage-service/provider-policy/recover');
  expect(JSON.parse(request.body as string)).toEqual({
    ...binding,
    providerId: 'provider',
    expectedGeneration: 2,
    expectedGateGeneration: 1,
    sourceVersion: 'a'.repeat(64),
  });
  expect(text()).toContain('保留历史暂停');
  expect(text()).not.toContain('已显式恢复本策略暂停');
  await act(async () => {
    resolve(
      Response.json({
        ...initial,
        ownPaused: false,
        recoveryAvailable: false,
        state: 'recovered_off',
        coordination: { ...initial.coordination, generation: 2, state: 'unpaused' },
      })
    );
  });
  expect(text()).toContain('已显式恢复本策略暂停');
  expect(text()).toContain('已关闭');
});
