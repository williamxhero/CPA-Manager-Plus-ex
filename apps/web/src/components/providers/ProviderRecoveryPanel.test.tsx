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
