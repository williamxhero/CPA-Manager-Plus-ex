import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, expect, it, vi } from 'vitest';
import { CredentialQuotaPanel } from './CredentialQuotaPanel';

afterEach(() => vi.unstubAllGlobals());

it('invalidates persisted row display on a credential mutation even if auth_index is reused', async () => {
  let finish!: (value: Response) => void;
  const pending = new Promise<Response>((resolve) => {
    finish = resolve;
  });
  let calls = 0;
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => (++calls === 1 ? json(status(42)) : pending))
  );
  let renderer!: ReactTestRenderer;
  await act(async () => {
    renderer = create(
      <CredentialQuotaPanel base="http://manager" managementKey="test" authIndex="a" revision={1} />
    );
  });
  await act(async () => {
    renderer.update(
      <CredentialQuotaPanel base="http://manager" managementKey="test" authIndex="a" revision={2} />
    );
  });
  expect(JSON.stringify(renderer.toJSON())).not.toContain('42');
  await act(async () => {
    finish(json(status(77)));
  });
  expect(JSON.stringify(renderer.toJSON())).toContain('77');
  await act(async () => renderer.unmount());
});

const status = (percent = 42) => ({
  scope: { instance_id: 'issued-instance' },
  targets: [{ resource_id: 'issued-resource' }],
  protocol: 'codex-oauth-wham',
  state: 'ready',
  reason: 'selected_oauth',
  version: 1,
  credentialQuery: { state: 'available', reason: 'scoped_credential_query_v1' },
  accountProvenance: 'selected_oauth_account',
  stale: false,
  lastSuccess: {
    observedAtMs: 1793500000000,
    windowsJson: JSON.stringify({
      complete: true,
      accountProvenance: 'selected_oauth_account',
      recoveryEligible: false,
      windows: [
        { name: 'primary', usedPercent: percent, remainingFraction: 0.58, sourceStatus: 'allowed' },
      ],
    }),
  },
});
const json = (value: unknown) => new Response(JSON.stringify(value), { status: 200 });

it('keeps the prior window mounted through refresh and reconciles the persisted result', async () => {
  let finish!: (response: Response) => void;
  const waiting = new Promise<Response>((resolve) => {
    finish = resolve;
  });
  let read = 0;
  const request = vi.fn(async (_url: string, options?: RequestInit) => {
    if (_url.endsWith('/full-refresh') && options?.method === 'GET') return json({sources:[{id:'host-source',queryCapable:true,request:{kind:'credential_quota',credential:{...status(),auth_index:'a',sourceVersion:'host-source-v1'}},targets:status().targets}]});
    if (options?.method === 'POST') return waiting;
    return json(status(read++ ? 80 : 42));
  });
  vi.stubGlobal('fetch', request);
  let renderer!: ReactTestRenderer;
  await act(async () => {
    renderer = create(
      <CredentialQuotaPanel base="http://manager" managementKey="test" authIndex="a" />
    );
  });
  await act(async () => {
    renderer.root.findByType('button').props.onClick();
  });
  expect(JSON.stringify(renderer.toJSON())).toContain('42');
  expect(renderer.root.findByType('button').props.disabled).toBe(true);
  await act(async () => {
    finish(json({ id: 'job', state: 'complete', deadlineMs: Date.now() + 1000 }));
  });
  expect(JSON.stringify(renderer.toJSON())).toContain('80');
  expect(request.mock.calls.filter((call) => call[1]?.method === 'POST')).toHaveLength(1);
  expect(JSON.parse(String(request.mock.calls.find(c=>c[1]?.method === 'POST')?.[1]?.body))).toMatchObject({purpose:'manual',source:{kind:'credential_quota',credential:{auth_index:'a',version:1,targets:[{resource_id:'issued-resource'}]}}});
  expect(request.mock.calls.filter(c=>c[1]?.method==='POST').map(c=>c[0])).toEqual(['http://manager/usage-service/full-refresh']);
  await act(async () => renderer.unmount());
});

it('hides a late prior-instance response and never derives an identity for an unbound row', async () => {
  let finish!: (response: Response) => void;
  const old = new Promise<Response>((resolve) => {
    finish = resolve;
  });
  const request = vi.fn(async (url: string) =>
    url.startsWith('http://old') ? old : json(status(77))
  );
  vi.stubGlobal('fetch', request);
  let renderer!: ReactTestRenderer;
  await act(async () => {
    renderer = create(
      <CredentialQuotaPanel base="http://old" managementKey="test" authIndex="a" />
    );
  });
  await act(async () => {
    renderer.update(<CredentialQuotaPanel base="http://new" managementKey="test" authIndex="b" />);
  });
  await act(async () => {
    finish(json(status(42)));
  });
  expect(JSON.stringify(renderer.toJSON())).toContain('77');
  expect(JSON.stringify(renderer.toJSON())).not.toContain('42');
  await act(async () => {
    renderer.update(<CredentialQuotaPanel base="http://new" managementKey="test" />);
  });
  expect(JSON.stringify(renderer.toJSON())).toContain('runtime_binding_unavailable');
  expect(request).toHaveBeenCalledTimes(2);
  await act(async () => renderer.unmount());
});

it('removes invalid old authority on conflict without reflecting authenticated error bodies', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_url: string, options?: RequestInit) =>
      options?.method === 'POST' ? new Response('SECRET-CANARY', { status: 409 }) : _url.endsWith('/full-refresh') ? json({sources:[{id:'host-source',queryCapable:true,request:{kind:'credential_quota',credential:{auth_index:'a'}}}]}) : json(status())
    )
  );
  let renderer!: ReactTestRenderer;
  await act(async () => {
    renderer = create(
      <CredentialQuotaPanel base="http://manager" managementKey="test" authIndex="a" />
    );
  });
  await act(async () => {
    renderer.root.findByType('button').props.onClick();
  });
  expect(JSON.stringify(renderer.toJSON())).not.toContain('42');
  expect(JSON.stringify(renderer.toJSON())).not.toContain('SECRET-CANARY');
  expect(JSON.stringify(renderer.toJSON())).toContain('credential_quota_request_failed');
  await act(async () => renderer.unmount());
});
it('reads persisted windows without querying or downloading credentials', async () => {
  const request = vi.fn(
    async (_url: string) =>
      new Response(
        JSON.stringify({
          protocol: 'opencode-go-usage',
          state: 'ready',
          version: 1,
          stale: true,
          attempt: { queryStatus: 'auth_error' },
          lastSuccess: {
            observedAtMs: 1793500000000,
            windowsJson: JSON.stringify({
              complete: false,
              accountProvenance: 'unverified',
              recoveryEligible: false,
              windows: [
                {
                  name: 'rolling',
                  usedPercent: 42,
                  remainingFraction: 0,
                  sourceStatus: 'rate-limited',
                },
              ],
            }),
          },
        }),
        { status: 200 }
      )
  );
  vi.stubGlobal('fetch', request);
  let renderer!: ReactTestRenderer;
  await act(async () => {
    renderer = create(
      <CredentialQuotaPanel
        base="http://manager"
        managementKey="test"
        authIndex="opaque-current-index"
      />
    );
  });
  expect(JSON.stringify(renderer.toJSON())).toContain('rate-limited');
  expect(JSON.stringify(renderer.toJSON())).toContain('stale');
  expect(JSON.stringify(renderer.toJSON())).toContain('auth_error');
  expect(JSON.stringify(renderer.toJSON())).toContain('42');
  expect(request).toHaveBeenCalledTimes(1);
  expect(request.mock.calls[0]?.[0]).toContain('/usage-service/credential-quota?authIndex=');
  await act(async () => renderer.unmount());
});
