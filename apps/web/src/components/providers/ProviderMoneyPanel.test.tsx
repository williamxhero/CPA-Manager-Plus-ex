import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, expect, it, vi } from 'vitest';
import { ProviderMoneyPanel } from './ProviderMoneyPanel';
import type { BalanceBinding } from '@/services/api/officialBalance';

const binding: BalanceBinding = {
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
const status = {
  ...binding,
  providerId: 'provider',
  enabled: false,
  effective: false,
  generation: 0,
  bindingCurrent: true,
  locked: true,
  manualDisabled: false,
  ownPaused: false,
  applied: false,
  state: 'off',
  reason: 'funding_coverage_unverified',
  updatedAtMs: 0,
  sources: [],
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
  vi.useRealTimers();
});
const text = () => JSON.stringify(view?.toJSON());
const button = (label: string) =>
  view!.root.findAllByType('button').find((b) => b.children.join('') === label)!;

it('rejects a foreign member observation even when the parent policy identity matches', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () =>
      Response.json({
        ...status,
        sources: [
          {
            scope: { ...binding.scope, instance_incarnation: 'other-account' },
            targets: [binding.targets[1]],
            state: 'success',
            stale: false,
            lastSuccess: { balances: [{ currency: 'USD', total: '999.0000' }] },
          },
        ],
      })
    )
  );
  await act(async () => {
    view = create(<ProviderMoneyPanel {...props} />);
  });
  expect(text()).not.toContain('USD 999.0000');
  expect(button('启用资金自动保护').props.disabled).toBe(true);
});

it('explicit enable uses the current generation and never shows a pending write as applied', async () => {
  let resolve!: (value: Response) => void;
  const fetcher = vi.fn(async (_url: string, init: RequestInit) =>
    init.method === 'PUT'
      ? new Promise<Response>((r) => {
          resolve = r;
        })
      : Response.json(status)
  );
  vi.stubGlobal('fetch', fetcher);
  await act(async () => {
    view = create(<ProviderMoneyPanel {...props} />);
  });
  expect(button('启用资金自动保护').props.disabled).toBeFalsy();
  await act(async () => {
    button('启用资金自动保护').props.onClick();
  });
  expect(text()).toContain('已关闭');
  expect(text()).not.toContain('已确认暂停');
  const init = fetcher.mock.calls.find((c) => c[1].method === 'PUT')![1];
  expect(JSON.parse(init.body as string)).toEqual({
    ...binding,
    providerId: 'provider',
    enabled: true,
    expectedGeneration: 0,
  });
  expect(init).toMatchObject({
    cache: 'no-store',
    redirect: 'error',
    headers: { Authorization: 'Bearer admin-a' },
  });
  await act(async () => {
    resolve(Response.json({ ...status, enabled: true, effective: true, generation: 1 }));
  });
  expect(text()).toContain('已启用');
  expect(text()).toContain('门禁结果未确认');
});

it('a failed save reads authority again without optimistic success or leaking an error body', async () => {
  const fetcher = vi.fn(async (_url: string, init: RequestInit) =>
    init.method === 'PUT'
      ? new Response('PRIVATE-UPSTREAM-SECRET', { status: 503 })
      : Response.json(status)
  );
  vi.stubGlobal('fetch', fetcher);
  await act(async () => {
    view = create(<ProviderMoneyPanel {...props} />);
  });
  await act(async () => {
    button('启用资金自动保护').props.onClick();
  });
  expect(fetcher.mock.calls.map((c) => c[1].method)).toEqual(['GET', 'PUT', 'GET']);
  expect(text()).toContain('已关闭');
  expect(text()).toContain('保存未确认');
  expect(text()).not.toContain('PRIVATE-UPSTREAM-SECRET');
});

it('refresh keeps the snapshot, failure locks writes, empty success replaces it and missing amounts never become zero', async () => {
  const observation = {
    scope: binding.scope,
    targets: [binding.targets[1]],
    state: 'network_error',
    stale: true,
    lastSuccess: {
      observedAtMs: 1793700000000,
      balances: [
        { currency: 'USD', total: '0.0500' },
        { currency: 'USD', total: 'NaN' },
        { currency: 'USD' },
      ],
    },
  };
  let resolve!: (value: Response) => void;
  const fetcher = vi
    .fn()
    .mockResolvedValueOnce(Response.json({ ...status, sources: [observation] }))
    .mockImplementationOnce(
      () =>
        new Promise<Response>((r) => {
          resolve = r;
        })
    )
    .mockResolvedValueOnce(Response.json(status));
  vi.stubGlobal('fetch', fetcher);
  await act(async () => {
    view = create(<ProviderMoneyPanel {...props} />);
  });
  await act(async () => {
    button('刷新资金保护状态').props.onClick();
  });
  expect(text()).toContain('USD 0.0500');
  expect(text()).toContain('金额未知');
  expect(text()).not.toContain('USD 0"');
  await act(async () => {
    resolve(new Response('', { status: 503 }));
  });
  expect(text()).toContain('USD 0.0500');
  expect(text()).toContain('请求失败');
  expect(button('启用资金自动保护').props.disabled).toBe(true);
  await act(async () => {
    button('刷新资金保护状态').props.onClick();
  });
  expect(text()).not.toContain('USD 0.0500');
  expect(button('启用资金自动保护').props.disabled).toBeFalsy();
});

it('late auth or capability responses cannot restore old data, even after returning to the same auth', async () => {
  let resolve!: (value: Response) => void;
  const fetcher = vi
    .fn()
    .mockResolvedValueOnce(Response.json(status))
    .mockImplementationOnce(
      () =>
        new Promise<Response>((r) => {
          resolve = r;
        })
    )
    .mockResolvedValueOnce(Response.json(status));
  vi.stubGlobal('fetch', fetcher);
  await act(async () => {
    view = create(<ProviderMoneyPanel {...props} />);
  });
  await act(async () => {
    button('刷新资金保护状态').props.onClick();
  });
  await act(async () => {
    view!.update(<ProviderMoneyPanel {...props} disabled />);
  });
  expect(text()).not.toContain('已关闭');
  await act(async () => {
    view!.update(<ProviderMoneyPanel {...props} managementKey="admin-b" />);
  });
  await act(async () => {
    resolve(Response.json({ ...status, enabled: true, generation: 9 }));
  });
  expect(text()).not.toContain('已启用');
  await act(async () => {
    view!.update(<ProviderMoneyPanel {...props} />);
  });
  expect(text()).not.toContain('已启用');
});
