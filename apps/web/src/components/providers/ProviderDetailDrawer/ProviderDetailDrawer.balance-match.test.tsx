import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, expect, it, vi } from 'vitest';
import type { BalanceBinding, OfficialBalanceStatus } from '@/services/api/officialBalance';
import { Select } from '@/components/ui/Select';
import { buildProviderRows } from '../ProviderTable/rowData';
import { ProviderDetailDrawer } from './ProviderDetailDrawer';

const binding: BalanceBinding = {
  scope: {
    instance_id: 'synthetic-instance',
    instance_incarnation: 'synthetic-incarnation',
    boot_epoch: 'synthetic-boot',
    snapshot_revision: 3,
  },
  targets: ['provider', 'key-a', 'key-b'].map((id) => ({
    resource_id: id,
    incarnation: 'synthetic-incarnation',
    identity_version: 1,
    credential_version: 2,
    config_version: 3,
    membership_version: 4,
  })),
};
const row = buildProviderRows({
  openai: [
    {
      name: 'Synthetic compatible provider',
      baseUrl: 'https://synthetic.example/v1',
      apiKeyEntries: [{ apiKey: 'INFERENCE-SECRET-CANARY' }],
    },
  ],
  gemini: [],
  codex: [],
  claude: [],
  vertex: [],
  usageByProvider: new Map(),
})[0];
const baseline: OfficialBalanceStatus = {
  ...binding,
  version: 0,
  adapter: '',
  endpoint: '',
  state: 'needs_configuration',
  configured: false,
  managerDirectQuery: false,
  coreQuery: false,
  coreGate: false,
  stale: false,
};
const evidence = {
  source: 'probe',
  host: 'synthetic.example',
  adapter: 'openai-compat-usage-v1',
  url: 'https://synthetic.example/v1/usage',
  status: 200,
  matchedFields: ['data.balance'],
  score: 100,
  timestamp: '2026-03-21T12:00:00Z',
};
const matched = {
  status: 'matched',
  adapter: evidence.adapter,
  endpoint: evidence.url,
  confidence: 100,
  evidence,
  candidates: [],
  applied: false,
  credential: 'UNEXPECTED-SECRET-CANARY',
};
const registered = {
  ...baseline,
  version: 1,
  adapter: 'deepseek',
  endpoint: 'https://manual.example/user/balance',
  configured: true,
  managerDirectQuery: true,
  state: 'ready',
};
let renderer: ReactTestRenderer | undefined;
const drawer = (open = true) => (
  <ProviderDetailDrawer
    content="balance"
    row={row}
    open={open}
    usageByProvider={new Map()}
    resolvedTheme="light"
    officialBalance={{
      base: 'http://synthetic-manager',
      managementKey: 'MANAGER-SECRET-CANARY',
      binding,
    }}
    actionsDisabled={false}
    toggleDisabled={false}
    onClose={() => {}}
    onEdit={() => {}}
    onDelete={() => {}}
    onToggle={() => {}}
    onToggleWebsockets={() => {}}
    onToggleCloak={() => {}}
    onToggleDisableCooling={() => {}}
  />
);
const text = () => JSON.stringify(renderer?.toJSON());
const protocol = () => renderer!.root.findByType(Select).props.value;
const endpoint = () => renderer!.root.findByProps({ 'aria-label': '查询 HTTPS 地址' }).props.value;
const applyButton = () =>
  renderer!.root.findAllByType('button').find((button) => button.children.includes('应用自动匹配'));
const render = async (open = true) => {
  await act(async () => {
    renderer = create(drawer(open));
  });
};
const apply = async () => {
  const button = applyButton();
  expect(button).toBeDefined();
  expect(button!.props.disabled).not.toBe(true);
  await act(async () => {
    button!.props.onClick();
  });
};
const routes = (
  status: OfficialBalanceStatus,
  preview: unknown,
  applied?: unknown,
  readback = status
) => {
  const matchRequests: unknown[] = [];
  let appliedOnce = false;
  const fetch = vi.fn(async (url: string, init: RequestInit) => {
    const parsed = new URL(url);
    if (parsed.pathname === '/usage-service/official-balance/match' && init.method === 'POST') {
      const body = JSON.parse(String(init.body));
      matchRequests.push(body);
      if (!body.apply) return Response.json(preview);
      appliedOnce = true;
      return Response.json(applied);
    }
    if (parsed.pathname === '/usage-service/official-balance' && init.method === 'GET') {
      expect(JSON.parse(parsed.searchParams.get('binding')!)).toEqual(binding);
      return Response.json(appliedOnce ? readback : status);
    }
    throw new Error('unexpected balance request');
  });
  vi.stubGlobal('fetch', fetch);
  return { fetch, matchRequests };
};

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

afterEach(async () => {
  if (renderer) await act(async () => renderer?.unmount());
  renderer = undefined;
  vi.unstubAllGlobals();
});

it('prefills a match with safe evidence and applies using only the issued binding', async () => {
  const { fetch, matchRequests } = routes(
    baseline,
    matched,
    { ...matched, applied: true },
    {
      ...registered,
      adapter: evidence.adapter,
      endpoint: evidence.url,
    }
  );
  await render();
  expect(matchRequests).toEqual([{ binding }]);
  expect(protocol()).toBe(evidence.adapter);
  expect(endpoint()).toBe(evidence.url);
  for (const label of [
    '自动匹配',
    '100%',
    'probe',
    'synthetic.example',
    evidence.url,
    'HTTP 200',
    'data.balance',
  ]) {
    expect(text()).toContain(label);
  }
  expect(text()).not.toContain('SECRET-CANARY');

  await apply();
  expect(matchRequests).toEqual([{ binding }, { binding, apply: true }]);
  expect(fetch.mock.calls.map(([, init]) => init.method)).toEqual(['GET', 'POST', 'POST', 'GET']);
  expect(text()).toContain('已应用');
  expect(text()).not.toContain('SECRET-CANARY');
});

it('shows unmatched candidates with their HTTP statuses and failure reasons', async () => {
  const { matchRequests } = routes(baseline, {
    status: 'unmatched',
    confidence: 0,
    applied: false,
    reason: 'no_usable_candidate',
    candidates: [
      {
        adapter: evidence.adapter,
        evidence: { ...evidence, status: 401, reason: 'upstream_status', matchedFields: [] },
      },
      {
        adapter: 'deepseek',
        evidence: {
          ...evidence,
          adapter: 'deepseek',
          url: 'https://synthetic.example/user/balance',
          status: 200,
          reason: 'contract_mismatch',
          matchedFields: [],
        },
      },
    ],
  });
  await render();
  expect(matchRequests).toEqual([{ binding }]);
  for (const label of [
    '未匹配',
    evidence.url,
    'HTTP 401',
    'upstream_status',
    '/user/balance',
    'HTTP 200',
    'contract_mismatch',
  ]) {
    expect(text()).toContain(label);
  }
  expect(text()).not.toContain('—');
  expect(applyButton()).toBeUndefined();
});

it('drops non-contract fields, URLs with userinfo, and raw failure text', async () => {
  routes(baseline, {
    status: 'unmatched',
    reason: 'SECRET-CANARY',
    candidates: [
      { evidence: { ...evidence, url: 'https://user:SECRET-CANARY@synthetic.example/v1/usage' } },
      {
        evidence: {
          ...evidence,
          host: 'SECRET-CANARY',
          reason: 'SECRET-CANARY',
          matchedFields: ['data.balance', 'SECRET-CANARY'],
        },
      },
    ],
  });
  await render();
  const list = renderer!.root.findByProps({ 'aria-label': '探测候选' });
  expect(list.findAllByType('li')).toHaveLength(1);
  expect(text()).toContain('data.balance');
  expect(text()).not.toContain('SECRET-CANARY');
});

it('does not match or replace a usable manual source', async () => {
  const { fetch, matchRequests } = routes(registered, matched);
  await render();
  expect(fetch).toHaveBeenCalledTimes(1);
  expect(matchRequests).toEqual([]);
  expect(protocol()).toBe(registered.adapter);
  expect(endpoint()).toBe(registered.endpoint);
  expect(applyButton()).toBeUndefined();
});

it('previews a stale source without overwriting it and honors Manager manual precedence on apply', async () => {
  const manual = { ...registered, stale: true };
  const { fetch, matchRequests } = routes(manual, matched, {
    ...matched,
    applyReason: 'manual_configuration',
  });
  await render();
  expect(protocol()).toBe(manual.adapter);
  expect(endpoint()).toBe(manual.endpoint);
  expect(text()).toContain(evidence.url);
  await apply();
  expect(matchRequests).toEqual([{ binding }, { binding, apply: true }]);
  expect(fetch.mock.calls.map(([, init]) => init.method)).toEqual(['GET', 'POST', 'POST', 'GET']);
  expect(text()).toContain('保留手动配置');
  expect(protocol()).toBe(manual.adapter);
  expect(endpoint()).toBe(manual.endpoint);
  expect(applyButton()).toBeUndefined();
});

it('does not request matching on page load even when a balance drawer has a row but is closed', async () => {
  const { fetch, matchRequests } = routes(baseline, matched);
  await render(false);
  expect(fetch).not.toHaveBeenCalled();
  await act(async () => {
    renderer!.update(drawer());
  });
  expect(matchRequests).toEqual([{ binding }]);
});

it('ignores a late match response after the drawer closes', async () => {
  let release!: (response: Response) => void;
  const deferred = new Promise<Response>((resolve) => {
    release = resolve;
  });
  const fetch = vi.fn(async (url: string, init: RequestInit) => {
    if (new URL(url).pathname.endsWith('/match')) return deferred;
    expect(init.method).toBe('GET');
    return Response.json(baseline);
  });
  vi.stubGlobal('fetch', fetch);
  await render();
  expect(fetch.mock.calls.map(([, init]) => init.method)).toEqual(['GET', 'POST']);
  const signal = fetch.mock.calls[1][1].signal!;
  await act(async () => {
    renderer!.update(drawer(false));
  });
  expect(signal.aborted).toBe(true);
  await act(async () => {
    release(Response.json(matched));
  });
  expect(applyButton()).toBeUndefined();
  expect(fetch).toHaveBeenCalledTimes(2);
});
