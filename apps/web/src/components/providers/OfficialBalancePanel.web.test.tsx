import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, expect, it, vi } from 'vitest';
import { OfficialBalancePanel } from './OfficialBalancePanel';
import type { BalanceBinding } from '@/services/api/officialBalance';

afterEach(() => vi.unstubAllGlobals());
const binding: BalanceBinding = {
  scope: {
    instance_id: 'ui-instance',
    instance_incarnation: 'ui-incarnation',
    boot_epoch: 'ui-boot',
    snapshot_revision: 1,
  },
  targets: [
    {
      resource_id: 'ui-resource',
      incarnation: 'ui-resource-incarnation',
      identity_version: 1,
      credential_version: 1,
      config_version: 1,
      membership_version: 0,
    },
  ],
};
const source = {
  site: 'layso',
  accountId: 'account-a',
  tenantId: 'tenant-a',
  role: 'billing-reader',
  executionUser: 'operator',
  browser: { family: 'microsoft-edge', profile: 'Profile 1', instanceId: 'edge-fixture' },
  currency: 'USD',
  parsingRule: 'controlled-fixture-rule',
};
const exact = '-9007199254740993.00000000100';
const status = (amount: string | null | undefined = exact) => ({
  ...binding,
  version: 1,
  adapter: 'layso_billing_web_v1',
  endpoint: 'https://layso.ai/dashboard/models',
  web: source,
  configured: true,
  managerDirectQuery: true,
  coreQuery: false,
  coreGate: false,
  state: 'network_error',
  stale: true,
  attempt: { queryStatus: 'network_error', errorCode: 'WEB_BROWSER_UNAVAILABLE' },
  lastSuccess: {
    queryStatus: 'success',
    semantic: 'web_account_balance',
    currency: 'USD',
    amountDecimal: amount,
    observedAtMs: 1793500000000,
  },
});

it.each([undefined, null, 'NaN', 'Infinity', '0.00000'])(
  'shows missing/non-finite amounts as unknown and keeps real zero exact: %s',
  async (amount) => {
    const data = { ...status(), lastSuccess: { ...status().lastSuccess, amountDecimal: amount } };
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => Response.json(data))
    );
    let renderer!: ReactTestRenderer;
    try {
      await act(async () => {
        renderer = create(
          <OfficialBalancePanel base="http://fixture" managementKey="test" binding={binding} />
        );
      });
      const text = JSON.stringify(renderer.toJSON());
      expect(text).toContain('WEB_BROWSER_UNAVAILABLE');
      expect(text).toContain('stale');
      expect(text).toContain('未产生采集时间');
      expect(text).not.toContain('Invalid Date');
      expect(text).not.toContain('1970-01-01');
      expect(text).not.toContain('平台可用标志');
      if (amount === '0.00000') expect(text).toContain('USD 0.00');
      else {
        expect(text).toContain('未知（未产生有效金额）');
        expect(text).not.toContain('USD 0');
      }
    } finally {
      await act(async () => renderer.unmount());
    }
  }
);

it('retains the web capture and draft during cache revalidation and failed GET, without collection', async () => {
  let reads = 0;
  let finish!: (response: Response) => void;
  const pending = new Promise<Response>((resolve) => {
    finish = resolve;
  });
  const request = vi.fn(async (_url: string, options?: RequestInit) => {
    expect(options?.method).toBe('GET');
    reads++;
    if (reads === 1) return Response.json(status());
    if (reads === 2) return pending;
    return new Response(null, { status: 503 });
  });
  vi.stubGlobal('fetch', request);
  let renderer!: ReactTestRenderer;
  try {
    await act(async () => {
      renderer = create(
        <OfficialBalancePanel base="http://fixture" managementKey="test" binding={binding} />
      );
    });
    const field = () => renderer.root.findByProps({ 'aria-label': '网页账户 ID' });
    const refresh = () =>
      renderer.root.findAllByType('button').find((n) => n.children.join('') === '刷新观测')!;
    await act(async () => field().props.onChange({ target: { value: 'unsaved-account' } }));
    await act(async () => refresh().props.onClick());
    expect(JSON.stringify(renderer.toJSON())).toContain('-9007199254740993.00');
    expect(refresh().props.disabled).toBe(true);
    await act(async () => finish(Response.json({ ...status(), state: 'success', stale: false })));
    expect(field().props.value).toBe('unsaved-account');
    await act(async () => refresh().props.onClick());
    expect(JSON.stringify(renderer.toJSON())).toContain('-9007199254740993.00');
    expect(JSON.stringify(renderer.toJSON())).toContain('stale');
    expect(JSON.stringify(renderer.toJSON())).toContain('balance_request_failed_reload_identity');
    expect(field().props.value).toBe('unsaved-account');
    expect(reads).toBe(3);
  } finally {
    await act(async () => renderer.unmount());
  }
});

it('hides invalidated web success after a saved account change whose readback fails', async () => {
  let saved = false;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_url: string, options?: RequestInit) => {
      if (options?.method === 'PUT') {
        saved = true;
        return Response.json({});
      }
      return saved ? new Response(null, { status: 503 }) : Response.json(status());
    })
  );
  let renderer!: ReactTestRenderer;
  try {
    await act(async () => {
      renderer = create(
        <OfficialBalancePanel base="http://fixture" managementKey="test" binding={binding} />
      );
    });
    await act(async () =>
      renderer.root
        .findByProps({ 'aria-label': '网页账户 ID' })
        .props.onChange({ target: { value: 'replacement-account' } })
    );
    await act(async () =>
      renderer.root
        .findAllByType('button')
        .find((n) => n.children.join('') === '保存余额来源')!
        .props.onClick()
    );
    expect(JSON.stringify(renderer.toJSON())).not.toContain(exact);
    expect(JSON.stringify(renderer.toJSON())).toContain('暂无成功观测');
    expect(
      renderer.root.findAllByType('button').find((n) => n.children.join('') === '保存余额来源')!
        .props.disabled
    ).toBe(true);
    expect(
      renderer.root.findAllByType('button').filter((n) => n.children.join('') === '重试回读')
    ).toHaveLength(1);
  } finally {
    await act(async () => renderer.unmount());
  }
});
