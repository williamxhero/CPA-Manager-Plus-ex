import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, expect, it, vi } from 'vitest';
import { OfficialBalancePanel } from './OfficialBalancePanel';
import type { BalanceBinding } from '@/services/api/officialBalance';

afterEach(() => vi.unstubAllGlobals());
const binding: BalanceBinding = {
  scope: {
    instance_id: 'review-instance',
    instance_incarnation: 'review-incarnation',
    boot_epoch: 'review-boot',
    snapshot_revision: 1,
  },
  targets: [
    {
      resource_id: 'review-resource',
      incarnation: 'review-resource-incarnation',
      identity_version: 1,
      credential_version: 1,
      config_version: 1,
      membership_version: 0,
    },
  ],
};
const exact = '-9007199254740993.00000000100';
const status = () => ({
  ...binding,
  version: 1,
  adapter: 'layso_billing_web_v1',
  endpoint: 'https://layso.ai/dashboard/models',
  web: {
    site: 'layso',
    accountId: 'review-account',
    tenantId: 'review-tenant',
    role: 'billing-reader',
    executionUser: 'review-operator',
    browser: { family: 'microsoft-edge', profile: 'Profile 1', instanceId: 'review-edge' },
    currency: 'USD',
    parsingRule: 'controlled-review-rule',
  },
  configured: true,
  managerDirectQuery: true,
  state: 'success',
  stale: false,
  lastSuccess: {
    queryStatus: 'success',
    semantic: 'web_account_balance',
    currency: 'USD',
    amountDecimal: exact,
    observedAtMs: 1793500000000,
  },
});

it('locks a retained baseline after failed cache GET and preserves the draft through successful retry', async () => {
  const methods: string[] = [];
  let reads = 0;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_url: string, options?: RequestInit) => {
      methods.push(options!.method!);
      if (++reads === 2) return new Response(null, { status: 503 });
      return Response.json(status());
    })
  );
  let renderer!: ReactTestRenderer;
  try {
    await act(async () => {
      renderer = create(
        <OfficialBalancePanel base="http://fixture" managementKey="test" binding={binding} />
      );
    });
    const button = (label: string) =>
      renderer.root.findAllByType('button').find((n) => n.children.join('') === label)!;
    const field = () => renderer.root.findByProps({ 'aria-label': '网页账户 ID' });
    await act(async () => field().props.onChange({ target: { value: 'unsaved-review-account' } }));
    const capturedSave = button('保存余额来源').props.onClick;
    const capturedQuery = button('手动查询余额').props.onClick;
    await act(async () => button('刷新观测').props.onClick());
    expect(JSON.stringify(renderer.toJSON())).toContain(exact);
    expect(JSON.stringify(renderer.toJSON())).toContain('stale');
    expect(field().props.value).toBe('unsaved-review-account');
    expect(button('保存余额来源').props.disabled).toBe(true);
    expect(button('手动查询余额').props.disabled).toBe(true);
    expect(renderer.root.findByType('fieldset').props.disabled).toBe(true);
    expect(JSON.stringify(renderer.toJSON())).not.toContain('余额来源已保存');
    // Captured/disabled callbacks must enforce the same lock, not only the DOM.
    await act(async () => {
      button('保存余额来源').props.onClick();
      button('手动查询余额').props.onClick();
      capturedSave();
      capturedQuery();
    });
    expect(methods).toEqual(['GET', 'GET']);
    await act(async () => button('重试回读').props.onClick());
    expect(field().props.value).toBe('unsaved-review-account');
    expect(button('保存余额来源').props.disabled).toBeFalsy();
    expect(button('手动查询余额').props.disabled).toBeFalsy();
    expect(renderer.root.findByType('fieldset').props.disabled).toBeFalsy();
    await act(async () => {
      capturedSave();
      capturedQuery();
    });
    expect(methods).toEqual(['GET', 'GET', 'GET']);
  } finally {
    await act(async () => renderer.unmount());
  }
});

it.each(
  [401, 403, 409].flatMap((code) =>
    ['刷新观测', '手动查询余额'].map((action) => ({ code, action }))
  )
)(
  'invalidates the web snapshot after management rejection $code during $action, without fallback',
  async ({ code, action }) => {
    const methods: string[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_url: string, options?: RequestInit) => {
        methods.push(options!.method!);
        return methods.length === 1
          ? Response.json(status())
          : new Response(null, { status: code });
      })
    );
    let renderer!: ReactTestRenderer;
    try {
      await act(async () => {
        renderer = create(
          <OfficialBalancePanel base="http://fixture" managementKey="test" binding={binding} />
        );
      });
      const button = (label: string) =>
        renderer.root.findAllByType('button').find((n) => n.children.join('') === label)!;
      expect(JSON.stringify(renderer.toJSON())).toContain(exact);
      await act(async () => button(action).props.onClick());
      const text = JSON.stringify(renderer.toJSON());
      expect(text).not.toContain(exact);
      expect(text).not.toContain('review-account');
      expect(text).not.toContain('ready');
      expect(text).toContain('identity_unavailable');
      // Retry is a read; it cannot replay a query/configuration write.
      const retry = button('重试回读');
      expect(retry).toBeDefined();
      await act(async () => retry.props.onClick());
      expect(methods).toEqual(['GET', action === '刷新观测' ? 'GET' : 'POST', 'GET']);
      expect(JSON.stringify(renderer.toJSON())).not.toContain(exact);
    } finally {
      await act(async () => renderer.unmount());
    }
  }
);

it('recovers only through a successful read and never revives callbacks captured before rejection', async () => {
  const methods: string[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_url: string, options?: RequestInit) => {
      methods.push(options!.method!);
      if (methods.length === 2) return new Response(null, { status: 401 });
      if (methods.length >= 3)
        return Response.json({
          ...status(),
          version: 2,
          web: { ...status().web, accountId: 'recovered-account' },
          lastSuccess: undefined,
          managerDirectQuery: false,
          state: 'unavailable',
          reason: 'web_source_verification_required',
        });
      return Response.json(status());
    })
  );
  let renderer!: ReactTestRenderer;
  try {
    await act(async () => {
      renderer = create(
        <OfficialBalancePanel base="http://fixture" managementKey="test" binding={binding} />
      );
    });
    const button = (label: string) =>
      renderer.root.findAllByType('button').find((n) => n.children.join('') === label)!;
    await act(async () =>
      renderer.root
        .findByProps({ 'aria-label': '网页账户 ID' })
        .props.onChange({ target: { value: 'private-draft-before-rejection' } })
    );
    const capturedSave = button('保存余额来源').props.onClick;
    const capturedQuery = button('手动查询余额').props.onClick;
    await act(async () => button('刷新观测').props.onClick());
    expect(JSON.stringify(renderer.toJSON())).not.toContain(exact);
    await act(async () => {
      capturedSave();
      capturedQuery();
    });
    expect(methods).toEqual(['GET', 'GET']);
    await act(async () => button('重试回读').props.onClick());
    expect(JSON.stringify(renderer.toJSON())).not.toContain(exact);
    expect(renderer.root.findByProps({ 'aria-label': '网页账户 ID' }).props.value).toBe(
      'recovered-account'
    );
    expect(JSON.stringify(renderer.toJSON())).not.toContain('private-draft-before-rejection');
    expect(button('保存余额来源').props.disabled).toBeFalsy();
    await act(async () => {
      capturedSave();
      capturedQuery();
    });
    expect(methods).toEqual(['GET', 'GET', 'GET']);
  } finally {
    await act(async () => renderer.unmount());
  }
});
