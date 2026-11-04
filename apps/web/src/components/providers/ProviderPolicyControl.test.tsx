import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { useState } from 'react';
import { afterEach, expect, it, vi } from 'vitest';
import { ProviderPolicyControl } from './ProviderPolicyControl';
import { buildProviderRows } from './ProviderTable/rowData';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }));

const id = '11111111-1111-4111-8111-111111111111';
const childId = '66666666-6666-4666-8666-666666666666';
const scope = {
  instance_id: '22222222-2222-4222-8222-222222222222',
  instance_incarnation: '33333333-3333-4333-8333-333333333333',
  boot_epoch: '44444444-4444-4444-8444-444444444444',
  snapshot_revision: 7,
};
const target = {
  resource_id: id,
  incarnation: '55555555-5555-4555-8555-555555555555',
  identity_version: 1,
  credential_version: 3,
  config_version: 4,
  membership_version: 1,
};
const binding = {
  scope,
  targets: [target, { ...target, resource_id: childId, membership_version: 0 }],
};
const row = buildProviderRows({
  gemini: [],
  codex: [],
  claude: [],
  vertex: [],
  openai: [
    {
      name: 'Representative',
      resourceId: id,
      baseUrl: 'https://synthetic.invalid/v1',
      apiKeyEntries: [{ apiKey: 'NEVER-SEND-RAW-KEY', resourceId: childId }],
    },
  ],
  usageByProvider: new Map(),
})[0];
const connection = { base: 'http://fixture/', managementKey: 'panel-key' };
const status = (overrides = {}) => ({
  ...binding,
  providerId: id,
  subjectId: 'issued-subject',
  enabled: false,
  effective: false,
  generation: 0,
  owner: 'cpa-manager.provider-balance.v1/installation',
  source: 'default_off',
  updatedAtMs: 0,
  bindingCurrent: true,
  locked: false,
  manualDisabled: false,
  ownPaused: false,
  applied: false,
  state: 'off',
  reason: 'funding_coverage_unverified',
  ...overrides,
});
let renderer: ReactTestRenderer | undefined;
const control = () => renderer!.root.findByProps({ role: 'switch' });
const mount = async (props = {}) => {
  await act(async () => {
    renderer = create(
      <ProviderPolicyControl row={row} binding={binding} connection={connection} {...props} />
    );
  });
};
afterEach(async () => {
  if (renderer) await act(async () => renderer!.unmount());
  renderer = undefined;
  vi.unstubAllGlobals();
});

it('reads only the issued parent UUID, starts OFF, and keeps manager authentication private', async () => {
  const fetch = vi.fn(async () => Response.json(status()));
  vi.stubGlobal('fetch', fetch);
  await mount();
  expect(fetch).toHaveBeenCalledExactlyOnceWith(
    `http://fixture/usage-service/provider-policy?providerId=${id}`,
    expect.objectContaining({
      method: 'GET',
      headers: { Authorization: 'Bearer panel-key', 'Content-Type': 'application/json' },
      cache: 'no-store',
      redirect: 'error',
    })
  );
  expect(control().props['aria-checked']).toBe(false);
  expect(control().props.disabled).toBe(false);
  expect(JSON.stringify(renderer!.toJSON())).not.toContain('panel-key');
  expect(JSON.stringify(fetch.mock.calls)).not.toContain('NEVER-SEND-RAW-KEY');
});

it('sends the full generation-bound PUT and stays OFF until separate trustworthy readback', async () => {
  let finish!: (response: Response) => void;
  let reads = 0;
  const saved = status({
    enabled: true,
    effective: true,
    generation: 1,
    source: 'explicit',
    state: 'observing',
  });
  const fetch = vi.fn(async (_url: string, init: RequestInit) => {
    if (init.method === 'PUT') return Response.json(saved);
    if (reads++ === 0) return Response.json(status());
    return new Promise<Response>((resolve) => {
      finish = resolve;
    });
  });
  vi.stubGlobal('fetch', fetch);
  await mount();
  await act(async () => control().props.onClick({ stopPropagation: vi.fn() }));
  expect(control().props['aria-checked']).toBe(false);
  expect(control().props.disabled).toBe(true);
  const put = fetch.mock.calls.find(([, init]) => init.method === 'PUT')!;
  expect(put[0]).toBe('http://fixture/usage-service/provider-policy');
  expect(JSON.parse(String(put[1].body))).toEqual({
    ...binding,
    providerId: id,
    expectedGeneration: 0,
    enabled: true,
  });
  await act(async () => finish(Response.json(saved)));
  expect(control().props['aria-checked']).toBe(true);
  expect(control().props.disabled).toBe(false);
});

it('visibly separates saved/effective/lock/manual/own-pause receipts without a sliding toggle', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () =>
      Response.json(
        status({
          enabled: true,
          effective: false,
          locked: true,
          manualDisabled: true,
          ownPaused: true,
          applied: false,
          generation: 8,
          source: 'explicit',
          updatedAtMs: 1793696400000,
          state: 'pending',
        })
      )
    )
  );
  await mount();
  expect(control().props.disabled).toBe(false); // Locked ON still permits OFF.
  const text = JSON.stringify(renderer!.toJSON());
  for (const label of [
    'provider_policy.saved',
    'provider_policy.on',
    'provider_policy.effective',
    'provider_policy.off',
    'provider_policy.locked',
    'common.yes',
    'provider_policy.manual',
    'provider_policy.manual_off',
    'provider_policy.own_pause',
    'provider_policy.states.pending',
    'provider_policy.auto_pending_marker',
    'funding_coverage_unverified',
    '2026-11-03T09:00:00.000Z',
  ])
    expect(text).toContain(label);
  expect(
    renderer!.root.findByProps({ 'data-auto-pause-marker': 'pending' }).props.role
  ).toBe('status');
  expect(control().props['aria-describedby']).toBeTruthy();
  expect(renderer!.root.findAllByType('input')).toHaveLength(0);
  expect(renderer!.root.findByType('time').props.dateTime).toBe('2026-11-03T09:00:00.000Z');
});

it.each([false, true])(
  '503 retains trusted saved=%s, visibly locks ON, and still permits saved ON to turn OFF',
  async (enabled) => {
    const fetch = vi.fn(async (_url: string, init: RequestInit) =>
      init.method === 'PUT'
        ? new Response('RAW-SECRET-PROXY-ERROR', { status: 503 })
        : Response.json(status({ enabled, effective: enabled, generation: 7, source: 'explicit' }))
    );
    vi.stubGlobal('fetch', fetch);
    await mount();
    await act(async () => control().props.onClick({ stopPropagation: vi.fn() }));
    expect(control().props['aria-checked']).toBe(enabled);
    expect(control().props.disabled).toBe(!enabled);
    const output = JSON.stringify(renderer!.toJSON());
    expect(output).toContain('provider_policy.errors.unavailable');
    expect(output).not.toContain('RAW-SECRET-PROXY-ERROR');
    const alerts = renderer!.root.findAllByProps({ role: 'alert' });
    expect(alerts).toHaveLength(1);
    const lock = renderer!.root.findAll((node) => node.props['data-policy-locked'] === true);
    expect(lock).toHaveLength(1);
    expect(fetch).toHaveBeenCalledTimes(2);
  }
);

it('renders exact native per-source funds, issued member scope, last success and stale/unknown without inventing totals', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () =>
      Response.json(
        status({
          funding: [
            {
              memberIds: [childId],
              query: { ...binding, version: 3 },
              keyMatched: true,
              fresh: false,
              reason: 'current_runtime_key_matched',
              balances: [
                {
                  currency: 'USD',
                  total: '0.100000000000000001',
                  granted: '0',
                  toppedUp: '0.100000000000000001',
                },
                { currency: 'CNY', total: '-0.05', granted: '0', toppedUp: '-0.05' },
              ],
              observedAtMs: 1793696400000,
              queryId: 'issued-query-id',
            },
          ],
        })
      )
    )
  );
  await mount();
  const output = JSON.stringify(renderer!.toJSON());
  for (const literal of [
    'USD',
    '0.100000000000000001',
    'CNY',
    '-0.05',
    childId,
    'provider_policy.stale',
    'provider_policy.per_source_only',
  ])
    expect(output).toContain(literal);
  expect(output).not.toContain('0.050000000000000001');
});

it('rejects a readback generation older than the accepted PUT even on conflict refetch', async () => {
  let reads = 0;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_url: string, init: RequestInit) =>
      init.method === 'PUT'
        ? Response.json(status({ enabled: true, generation: 9, source: 'explicit' }))
        : Response.json(
            reads++ === 0
              ? status({ generation: 7, source: 'explicit' })
              : status({ generation: 8, source: 'explicit' })
          )
    )
  );
  await mount();
  await act(async () => control().props.onClick({ stopPropagation: vi.fn() }));
  expect(control().props.disabled).toBe(true);
  expect(JSON.stringify(renderer!.toJSON())).not.toContain('provider_policy.saved');
  expect(renderer!.root.findAllByProps({ role: 'alert' })).toHaveLength(1);
});

it('retains the generation fence after clearing an invalid view and explicitly rechecking', async () => {
  let reads = 0;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_url: string, init: RequestInit) =>
      init.method === 'PUT'
        ? Response.json(status({ enabled: true, generation: 9, source: 'explicit' }))
        : Response.json(status({ generation: reads++ === 0 ? 7 : 8, source: 'explicit' }))
    )
  );
  await mount();
  await act(async () => control().props.onClick({ stopPropagation: vi.fn() }));
  const read = renderer!.root.findByProps({ 'aria-label': 'provider_policy.read_status' });
  await act(async () => read.props.onClick({ stopPropagation: vi.fn() }));
  expect(JSON.stringify(renderer!.toJSON())).not.toContain('provider_policy.saved');
  expect(control().props.disabled).toBe(true);
});

it('rechecks status without platform refresh or PUT, retaining trusted ON if that GET returns 503', async () => {
  let reads = 0;
  const fetch = vi.fn(async () =>
    reads++ === 0
      ? Response.json(status({ enabled: true, generation: 5 }))
      : new Response(null, { status: 503 })
  );
  vi.stubGlobal('fetch', fetch);
  await mount();
  const read = renderer!.root.findByProps({ 'aria-label': 'provider_policy.read_status' });
  await act(async () => read.props.onClick({ stopPropagation: vi.fn() }));
  expect(fetch).toHaveBeenCalledTimes(2);
  expect(control().props['aria-checked']).toBe(true);
  expect(control().props.disabled).toBe(false);
  expect(JSON.stringify(renderer!.toJSON())).toContain('provider_policy.errors.unavailable');
});

it.each([
  ['absent proof', { binding: undefined }],
  ['credential rather than parent', { row: { ...row, kind: 'codex' } }],
  ['wrong parent marker', { row: { ...row, raw: { ...row.raw, resourceId: childId } } }],
  ['parent without member proof', { binding: { scope, targets: [target] } }],
  [
    'wrong member version',
    { binding: { scope: { ...scope, snapshot_revision: 0 }, targets: binding.targets } },
  ],
  ['duplicate issued member', { binding: { scope, targets: [target, target] } }],
  ['manager unavailable', { connection: { ...connection, disabled: true } }],
  ['missing authentication', { connection: { ...connection, managementKey: '' } }],
])('never guesses GET/PUT for %s', async (_name, props) => {
  const fetch = vi.fn();
  vi.stubGlobal('fetch', fetch);
  await mount(props);
  expect(control().props.disabled).toBe(true);
  await act(async () => control().props.onClick({ stopPropagation: vi.fn() }));
  expect(fetch).not.toHaveBeenCalled();
  expect(JSON.stringify(renderer!.toJSON())).toContain('provider_policy.unavailable');
});

it.each([
  { providerId: childId },
  { scope: { ...scope, snapshot_revision: 8 } },
  { targets: [target] },
  { targets: [target, { ...binding.targets[1], credential_version: 9 }] },
  { generation: -1 },
  { generation: 0.5 },
  { enabled: 'true' },
  { locked: undefined },
  { subjectId: '' },
  { state: 'healthy' },
  { updatedAtMs: -1 },
])('fails closed for malformed or differently bound status %j', async (changes) => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => Response.json(status({ enabled: true, ...changes })))
  );
  await mount();
  expect(control().props['aria-checked']).toBe(false);
  expect(control().props.disabled).toBe(true);
  expect(JSON.stringify(renderer!.toJSON())).not.toContain('provider_policy.saved');
});

it('locked funding still allows OFF, preserving manual disabled and this policy own pause', async () => {
  const before = status({
    enabled: true,
    effective: false,
    generation: 5,
    locked: true,
    manualDisabled: true,
    ownPaused: true,
    source: 'explicit',
    state: 'unavailable',
  });
  const after = { ...before, enabled: false, generation: 6, state: 'off' };
  let reads = 0;
  const fetch = vi.fn(async (_url: string, init: RequestInit) =>
    Response.json(init.method === 'PUT' || reads++ > 0 ? after : before)
  );
  vi.stubGlobal('fetch', fetch);
  await mount();
  const stopped = vi.fn();
  await act(async () => control().props.onClick({ stopPropagation: stopped }));
  expect(stopped).toHaveBeenCalledOnce();
  const put = fetch.mock.calls.find(([, init]) => init.method === 'PUT')!;
  expect(JSON.parse(String(put[1].body))).toEqual({
    ...binding,
    providerId: id,
    expectedGeneration: 5,
    enabled: false,
  });
  expect(control().props['aria-checked']).toBe(false);
  expect(control().props.disabled).toBe(true);
  expect(JSON.stringify(renderer!.toJSON())).toContain('provider_policy.off_retains_pause');
  expect(JSON.stringify(renderer!.toJSON())).toContain('provider_policy.manual_off');
});

it('clears invalid ON on 409 before refetch and never accepts a different policy subject', async () => {
  let finish!: (response: Response) => void;
  let reads = 0;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_url: string, init: RequestInit) => {
      if (init.method === 'PUT') return new Response(null, { status: 409 });
      if (reads++ === 0) return Response.json(status({ enabled: true, generation: 5 }));
      return new Promise<Response>((resolve) => {
        finish = resolve;
      });
    })
  );
  await mount();
  await act(async () => control().props.onClick({ stopPropagation: vi.fn() }));
  expect(control().props['aria-checked']).toBe(false);
  expect(control().props.disabled).toBe(true);
  expect(JSON.stringify(renderer!.toJSON())).not.toContain('provider_policy.saved');
  await act(async () =>
    finish(Response.json(status({ enabled: true, generation: 6, subjectId: 'different-subject' })))
  );
  expect(control().props['aria-checked']).toBe(false);
  expect(control().props.disabled).toBe(true);
});

it('accepts a newer same-subject conflict refetch without retrying the write', async () => {
  let reads = 0;
  const fetch = vi.fn(async (_url: string, init: RequestInit) =>
    init.method === 'PUT'
      ? new Response(null, { status: 409 })
      : Response.json(
          status({ generation: reads++ === 0 ? 5 : 6, enabled: true, source: 'explicit' })
        )
  );
  vi.stubGlobal('fetch', fetch);
  await mount();
  await act(async () => control().props.onClick({ stopPropagation: vi.fn() }));
  expect(control().props['aria-checked']).toBe(true);
  expect(control().props.disabled).toBe(false); // OFF remains possible after conflict.
  expect(fetch.mock.calls.filter(([, init]) => init.method === 'PUT')).toHaveLength(1);
  expect(fetch.mock.calls.filter(([, init]) => init.method === 'GET')).toHaveLength(2);
});

it.each(['provider', 'instance', 'boot', 'target-version', 'management-key'])(
  'ignores late old GET after %s replacement',
  async (change) => {
    let finish!: (response: Response) => void;
    let calls = 0;
    let nextBinding = binding;
    let nextRow = row;
    let nextConnection = connection;
    if (change === 'provider') {
      const nextId = '77777777-7777-4777-8777-777777777777';
      nextRow = { ...row, raw: { ...row.raw, resourceId: nextId } } as typeof row;
      nextBinding = {
        ...binding,
        targets: [{ ...target, resource_id: nextId }, binding.targets[1]],
      };
    } else if (change === 'management-key')
      nextConnection = { ...connection, managementKey: 'replacement-key' };
    else if (change === 'target-version')
      nextBinding = { ...binding, targets: [{ ...target, config_version: 6 }, binding.targets[1]] };
    else
      nextBinding = {
        ...binding,
        scope: {
          ...scope,
          [change === 'boot' ? 'boot_epoch' : 'instance_incarnation']:
            '77777777-7777-4777-8777-777777777777',
        },
      };
    const fetch = vi.fn(async () =>
      calls++ === 0
        ? new Promise<Response>((resolve) => {
            finish = resolve;
          })
        : Response.json(
            status({
              ...nextBinding,
              providerId: nextBinding.targets[0].resource_id,
              subjectId: 'replacement',
              generation: 3,
            })
          )
    );
    vi.stubGlobal('fetch', fetch);
    await mount();
    await act(async () =>
      renderer!.update(
        <ProviderPolicyControl row={nextRow} binding={nextBinding} connection={nextConnection} />
      )
    );
    expect(control().props['aria-checked']).toBe(false);
    await act(async () => finish(Response.json(status({ enabled: true, generation: 90 }))));
    expect(control().props['aria-checked']).toBe(false);
    expect(control().props.disabled).toBe(false);
  }
);

it('fences captured old-generation clicks and duplicate writes', async () => {
  let saved = status();
  const fetch = vi.fn(async (_url: string, init: RequestInit) => {
    if (init.method === 'PUT') saved = status({ enabled: true, generation: 1, source: 'explicit' });
    return Response.json(saved);
  });
  vi.stubGlobal('fetch', fetch);
  await mount();
  const oldClick = control().props.onClick;
  await act(async () => {
    oldClick({ stopPropagation: vi.fn() });
    oldClick({ stopPropagation: vi.fn() });
  });
  expect(control().props['aria-checked']).toBe(true);
  await act(async () => oldClick({ stopPropagation: vi.fn() }));
  expect(fetch.mock.calls.filter(([, init]) => init.method === 'PUT')).toHaveLength(1);
});

it('ignores late 409/PUT callbacks after identity replacement or unmount', async () => {
  let finish!: (response: Response) => void;
  const changed = vi.fn();
  const fetch = vi.fn(async (_url: string, init: RequestInit) =>
    init.method === 'PUT'
      ? new Promise<Response>((resolve) => {
          finish = resolve;
        })
      : Response.json(status())
  );
  vi.stubGlobal('fetch', fetch);
  await mount({ connection: { ...connection, onChanged: changed } });
  await act(async () => control().props.onClick({ stopPropagation: vi.fn() }));
  await act(async () => renderer!.unmount());
  renderer = undefined;
  await act(async () => finish(new Response(null, { status: 409 })));
  expect(fetch).toHaveBeenCalledTimes(2);
  expect(changed).not.toHaveBeenCalled();
});

it('synchronizes same-provider list/detail readbacks with distinct accessible descriptions', async () => {
  let saved = status();
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_url: string, init: RequestInit) => {
      if (init.method === 'PUT')
        saved = status({ enabled: true, generation: 1, source: 'explicit' });
      return Response.json(saved);
    })
  );
  function Harness() {
    const [revision, setRevision] = useState(0);
    const shared = { ...connection, revision, onChanged: () => setRevision((v) => v + 1) };
    return (
      <>
        <ProviderPolicyControl row={row} binding={binding} connection={shared} compact />
        <ProviderPolicyControl row={row} binding={binding} connection={shared} />
      </>
    );
  }
  await act(async () => {
    renderer = create(<Harness />);
  });
  const switches = () => renderer!.root.findAllByProps({ role: 'switch' });
  expect(switches().map((node) => node.props['aria-checked'])).toEqual([false, false]);
  const ids = switches().map((node) => node.props['aria-describedby']);
  expect(new Set(ids).size).toBe(2);
  await act(async () => switches()[0].props.onClick({ stopPropagation: vi.fn() }));
  expect(switches().map((node) => node.props['aria-checked'])).toEqual([true, true]);
});

it('rejects a captured pre-error ON handler even when 503 retained the same data object', async () => {
  const fetch = vi.fn(async (_url: string, init: RequestInit) =>
    init.method === 'PUT' ? new Response(null, { status: 503 }) : Response.json(status())
  );
  vi.stubGlobal('fetch', fetch);
  await mount();
  const oldClick = control().props.onClick;
  await act(async () => oldClick({ stopPropagation: vi.fn() }));
  expect(control().props.disabled).toBe(true);
  await act(async () => oldClick({ stopPropagation: vi.fn() }));
  expect(fetch.mock.calls.filter(([, init]) => init.method === 'PUT')).toHaveLength(1);
});

it.each([401, 403])(
  'revokes verified ON after GET %s and recovers only by read without reviving captured callbacks',
  async (code) => {
    let finish!: (response: Response) => void;
    let reads = 0;
    const verified = status({ enabled: true, generation: 5, source: 'explicit' });
    const fetch = vi.fn(async (_url: string, _init: RequestInit) => {
      if (reads++ === 0) return Response.json(verified);
      if (reads === 2) return new Response('PRIVATE-AUTH-REJECTION', { status: code });
      return new Promise<Response>((resolve) => {
        finish = resolve;
      });
    });
    vi.stubGlobal('fetch', fetch);
    await mount();
    const oldClick = control().props.onClick;
    const oldRead = renderer!.root.findByProps({
      'aria-label': 'provider_policy.read_status',
    }).props.onClick;
    await act(async () => oldRead({ stopPropagation: vi.fn() }));
    expect(control().props['aria-checked']).toBe(false);
    expect(control().props.disabled).toBe(true);
    expect(JSON.stringify(renderer!.toJSON())).not.toContain('provider_policy.saved');
    expect(JSON.stringify(renderer!.toJSON())).not.toContain('PRIVATE-AUTH-REJECTION');
    expect(renderer!.root.findAllByProps({ role: 'alert' })).toHaveLength(1);
    await act(async () => {
      oldClick({ stopPropagation: vi.fn() });
      oldRead({ stopPropagation: vi.fn() });
    });
    expect(fetch).toHaveBeenCalledTimes(2);
    const retry = renderer!.root.findByProps({
      'aria-label': 'provider_policy.read_status',
    }).props.onClick;
    await act(async () => retry({ stopPropagation: vi.fn() }));
    expect(control().props['aria-checked']).toBe(false);
    expect(control().props.disabled).toBe(true);
    await act(async () => finish(Response.json(verified)));
    expect(control().props['aria-checked']).toBe(true);
    expect(control().props.disabled).toBe(false);
    await act(async () => {
      oldClick({ stopPropagation: vi.fn() });
      oldRead({ stopPropagation: vi.fn() });
      retry({ stopPropagation: vi.fn() });
    });
    expect(fetch).toHaveBeenCalledTimes(3);
    expect(fetch.mock.calls.every(([, init]) => init.method === 'GET')).toBe(true);
  }
);

it.each([401, 403].flatMap((code) => ['PUT', 'readback'].map((stage) => ({ code, stage }))))(
  'revokes verified ON after $stage $code without replaying intent or restoring old handlers on read recovery',
  async ({ code, stage }) => {
    const changed = vi.fn();
    let attempted = false;
    let denied = false;
    const before = status({ enabled: true, generation: 5, source: 'explicit' });
    const after = status({ enabled: false, generation: 6, source: 'explicit' });
    const fetch = vi.fn(async (_url: string, init: RequestInit) => {
      if (init.method === 'PUT') {
        attempted = true;
        if (stage === 'readback') return Response.json(after);
        denied = true;
        return new Response(null, { status: code });
      }
      if (!attempted) return Response.json(before);
      if (!denied) {
        denied = true;
        return new Response(null, { status: code });
      }
      return Response.json(after);
    });
    vi.stubGlobal('fetch', fetch);
    await mount({ connection: { ...connection, onChanged: changed } });
    const oldClick = control().props.onClick;
    const oldRead = renderer!.root.findByProps({
      'aria-label': 'provider_policy.read_status',
    }).props.onClick;
    await act(async () => oldClick({ stopPropagation: vi.fn() }));
    expect(control().props['aria-checked']).toBe(false);
    expect(control().props.disabled).toBe(true);
    expect(JSON.stringify(renderer!.toJSON())).not.toContain('provider_policy.saved');
    expect(changed).not.toHaveBeenCalled();
    const attempts = stage === 'PUT' ? 2 : 3;
    expect(fetch).toHaveBeenCalledTimes(attempts);
    await act(async () => {
      oldClick({ stopPropagation: vi.fn() });
      oldRead({ stopPropagation: vi.fn() });
      control().props.onClick({ stopPropagation: vi.fn() });
    });
    expect(fetch).toHaveBeenCalledTimes(attempts);
    await act(async () =>
      renderer!.root
        .findByProps({ 'aria-label': 'provider_policy.read_status' })
        .props.onClick({ stopPropagation: vi.fn() })
    );
    expect(control().props['aria-checked']).toBe(false);
    expect(control().props.disabled).toBe(false);
    expect(JSON.stringify(renderer!.toJSON())).toContain('provider_policy.saved');
    await act(async () => {
      oldClick({ stopPropagation: vi.fn() });
      oldRead({ stopPropagation: vi.fn() });
    });
    expect(fetch).toHaveBeenCalledTimes(attempts + 1);
    expect(fetch.mock.calls.filter(([, init]) => init.method === 'PUT')).toHaveLength(1);
    expect(fetch.mock.calls[fetch.mock.calls.length - 1][1].method).toBe('GET');
    expect(changed).not.toHaveBeenCalled();
  }
);

it.each(
  [401, 403].flatMap((code) => ['success', 'rejection'].map((lateResult) => ({ code, lateResult })))
)(
  'fences a late $lateResult from GET-only recovery after $code when management identity changes',
  async ({ code, lateResult }) => {
    let finish!: (response: Response) => void;
    let reads = 0;
    const changed = vi.fn();
    const fetch = vi.fn(async (_url: string, _init: RequestInit) => {
      reads += 1;
      if (reads === 1) return Response.json(status({ enabled: true, generation: 5 }));
      if (reads === 2) return new Response(null, { status: code });
      if (reads === 3)
        return new Promise<Response>((resolve) => {
          finish = resolve;
        });
      return Response.json(status({ subjectId: 'replacement-subject', generation: 1 }));
    });
    vi.stubGlobal('fetch', fetch);
    await mount();
    const oldClick = control().props.onClick;
    const read = () =>
      renderer!.root.findByProps({ 'aria-label': 'provider_policy.read_status' }).props.onClick;
    await act(async () => read()({ stopPropagation: vi.fn() }));
    expect(control().props['aria-checked']).toBe(false);
    const oldRetry = read();
    await act(async () => oldRetry({ stopPropagation: vi.fn() }));
    await act(async () =>
      renderer!.update(
        <ProviderPolicyControl
          row={row}
          binding={binding}
          connection={{ ...connection, managementKey: 'replacement-key', onChanged: changed }}
        />
      )
    );
    expect(control().props['aria-checked']).toBe(false);
    expect(control().props.disabled).toBe(false);
    await act(async () =>
      finish(
        lateResult === 'success'
          ? Response.json(status({ enabled: true, generation: 90 }))
          : new Response(null, { status: code })
      )
    );
    expect(control().props['aria-checked']).toBe(false);
    expect(control().props.disabled).toBe(false);
    expect(renderer!.root.findAllByProps({ role: 'alert' })).toHaveLength(0);
    await act(async () => {
      oldClick({ stopPropagation: vi.fn() });
      oldRetry({ stopPropagation: vi.fn() });
    });
    expect(fetch).toHaveBeenCalledTimes(4);
    expect(fetch.mock.calls.every(([, init]) => init.method === 'GET')).toBe(true);
    expect(changed).not.toHaveBeenCalled();
  }
);

it('requires confirmation before posting explicit OFF recovery and keeps policy OFF', async () => {
  const sourceVersion = 'a'.repeat(64);
  const pausedOff = status({
    enabled: false,
    ownPaused: true,
    applied: true,
    recoveryAvailable: true,
    gateGeneration: 4,
    sourceVersion,
    generation: 3,
    source: 'explicit',
    reason: 'policy_off_retains_owned_pause',
  });
  const recovered = status({
    enabled: false,
    ownPaused: false,
    applied: true,
    recoveryAvailable: false,
    gateGeneration: 5,
    sourceVersion,
    generation: 3,
    source: 'explicit',
  });
  let posts = 0;
  const fetch = vi.fn(async (url: string, init: RequestInit = {}) => {
    if (init.method === 'POST') {
      posts += 1;
      expect(String(url)).toBe('http://fixture/usage-service/provider-policy/recover');
      expect(JSON.parse(String(init.body))).toEqual({
        ...binding,
        expectedGeneration: 3,
        expectedGateGeneration: 4,
        sourceVersion,
      });
      return Response.json(recovered);
    }
    return Response.json(posts === 0 ? pausedOff : recovered);
  });
  vi.stubGlobal('fetch', fetch);
  await mount();
  expect(JSON.stringify(renderer!.toJSON())).toContain('provider_policy.recover');
  expect(
    renderer!.root.findByProps({ 'data-auto-pause-marker': 'applied' }).props.role
  ).toBe('status');
  expect(JSON.stringify(renderer!.toJSON())).toContain('provider_policy.auto_paused_marker');
  expect(renderer!.root.findAllByProps({ role: 'alertdialog' })).toHaveLength(0);
  const recoverButton = renderer!.root
    .findAllByType('button')
    .find((node) => node.children.includes('provider_policy.recover'))!;
  expect(recoverButton.props.disabled).toBe(false);
  await act(async () => recoverButton.props.onClick({ stopPropagation: vi.fn() }));
  expect(renderer!.root.findAllByProps({ role: 'alertdialog' })).toHaveLength(1);
  expect(posts).toBe(0);
  await act(async () =>
    renderer!.root
      .findAllByType('button')
      .find((node) => node.children.includes('provider_policy.recover_confirm'))!
      .props.onClick({ stopPropagation: vi.fn() })
  );
  expect(posts).toBe(1);
  expect(control().props['aria-checked']).toBe(false);
  expect(JSON.stringify(renderer!.toJSON())).not.toContain('provider_policy.recover_confirm');
  expect(renderer!.root.findAllByProps({ 'data-auto-pause-marker': 'applied' })).toHaveLength(0);
  expect(fetch.mock.calls.some(([, init]) => init?.method === 'PUT')).toBe(false);
});

it('hides recovery confirmation when unavailable and cancels without posting', async () => {
  const fetch = vi.fn(async () =>
    Response.json(
      status({
        enabled: false,
        ownPaused: true,
        applied: true,
        recoveryAvailable: false,
        generation: 2,
        source: 'explicit',
      })
    )
  );
  vi.stubGlobal('fetch', fetch);
  await mount();
  const recoverButton = renderer!.root
    .findAllByType('button')
    .find((node) => node.children.includes('provider_policy.recover'))!;
  expect(recoverButton.props.disabled).toBe(true);
  await act(async () => recoverButton.props.onClick({ stopPropagation: vi.fn() }));
  expect(renderer!.root.findAllByProps({ role: 'alertdialog' })).toHaveLength(0);
  expect(fetch.mock.calls.every(([, init]) => !init?.method || init.method === 'GET')).toBe(true);

  vi.stubGlobal(
    'fetch',
    vi.fn(async () =>
      Response.json(
        status({
          enabled: false,
          ownPaused: true,
          applied: true,
          recoveryAvailable: true,
          gateGeneration: 9,
          sourceVersion: 'b'.repeat(64),
          generation: 2,
          source: 'explicit',
        })
      )
    )
  );
  await act(async () =>
    renderer!.root
      .findByProps({ 'aria-label': 'provider_policy.read_status' })
      .props.onClick({ stopPropagation: vi.fn() })
  );
  const available = renderer!.root
    .findAllByType('button')
    .find((node) => node.children.includes('provider_policy.recover'))!;
  await act(async () => available.props.onClick({ stopPropagation: vi.fn() }));
  expect(renderer!.root.findAllByProps({ role: 'alertdialog' })).toHaveLength(1);
  await act(async () =>
    renderer!.root
      .findAllByType('button')
      .find((node) => node.children.includes('provider_policy.recover_cancel'))!
      .props.onClick({ stopPropagation: vi.fn() })
  );
  expect(renderer!.root.findAllByProps({ role: 'alertdialog' })).toHaveLength(0);
  expect(
    vi.mocked(globalThis.fetch).mock.calls.every(([, init]) => !init?.method || init.method === 'GET')
  ).toBe(true);
});

for (const initialEnabled of [false, true]) {
  for (const failure of ['503', 'transport'] as const) {
    it(`locks an acknowledged ${initialEnabled ? 'OFF' : 'ON'} save when readback ${failure} fails, then recovers by GET only`, async () => {
      const changed = vi.fn();
      let accepted = false;
      let failed = false;
      const before = status({ enabled: initialEnabled, generation: 5, source: 'explicit' });
      const saved = status({ enabled: !initialEnabled, generation: 6, source: 'explicit' });
      const fetch = vi.fn(async (_url: unknown, init: RequestInit) => {
        if (init.method === 'PUT') {
          accepted = true;
          return Response.json(saved);
        }
        if (accepted && !failed) {
          failed = true;
          if (failure === 'transport') throw new TypeError('controlled network failure');
          return new Response('', { status: 503 });
        }
        return Response.json(accepted ? saved : before);
      });
      vi.stubGlobal('fetch', fetch);
      await mount({ connection: { ...connection, onChanged: changed } });
      await act(async () => control().props.onClick({ stopPropagation: vi.fn() }));
      expect(control().props['aria-checked']).toBe(!initialEnabled);
      expect(control().props.disabled).toBe(true);
      expect(JSON.stringify(renderer!.toJSON())).toContain('provider_policy.errors.reconciliation');
      expect(changed).toHaveBeenCalledTimes(1);
      await act(async () => control().props.onClick({ stopPropagation: vi.fn() }));
      expect(fetch.mock.calls.filter(([, init]) => init.method === 'PUT')).toHaveLength(1);
      await act(async () =>
        renderer!.root
          .findByProps({ 'aria-label': 'provider_policy.read_status' })
          .props.onClick({ stopPropagation: vi.fn() })
      );
      expect(control().props['aria-checked']).toBe(!initialEnabled);
      expect(control().props.disabled).toBe(false);
      expect(fetch.mock.calls.filter(([, init]) => init.method === 'PUT')).toHaveLength(1);
    });
  }
}
