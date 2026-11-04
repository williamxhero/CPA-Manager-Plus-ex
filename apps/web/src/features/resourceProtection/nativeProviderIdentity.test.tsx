/// <reference types="node" />
import { createServer, type Server } from 'node:http';
import { once } from 'node:events';
import type { AddressInfo } from 'node:net';
import { act, create, type ReactTestRenderer, type ReactTestInstance } from 'react-test-renderer';
import { createInstance } from 'i18next';
import { I18nextProvider } from 'react-i18next';
import { afterEach, expect, it, vi } from 'vitest';
import { apiClient } from '@/services/api/client';
import { providersApi } from '@/services/api/providers';
import type { ResourceInventoryRow } from '@/services/api/usageService';
import { useAuthStore, useConfigStore } from '@/stores';
import { createMemoryRouter, RouterProvider, useOutletContext } from 'react-router-dom';
import {
  AiProvidersOpenAIEditLayout,
  type OpenAIEditOutletContext,
} from '@/features/aiProviders/AiProvidersOpenAIEditLayout';
import { OpenAIEditDrawer } from '@/components/providers/ProviderEditDrawer/OpenAIEditDrawer';
import { ProviderTable } from '@/components/providers/ProviderTable/ProviderTable';
import { buildProviderRows, type ProviderRow } from '@/components/providers/ProviderTable/rowData';
import en from '@/i18n/locales/en.json';
import { useResourceProtection } from './resourceProtection';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
const id = '11111111-1111-4111-8111-111111111111';
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
  membership_version: 0,
};
const policy = {
  source: 'startup',
  authIssueQueue: { enabled: false, envKey: 'B', configFileKey: 'b' },
  authIssueAutoDisable: { enabled: false, envKey: 'C', configFileKey: 'c' },
};
const sourceRow = (): ResourceInventoryRow => ({
  ...target,
  kind: 'credential',
  family: 'codex',
  identity: 'valid',
  catalog_readiness: 'ready',
  runtime_readiness: 'unknown',
  capabilities: { inventory: true, query: false, gate: false },
  manual: { disabled: true, excluded: false, zero_weight: false },
});
const catalog = () => ({
  schemaVersion: 1,
  identity: { state: 'unknown', reason: 'core_contract_unverified' },
  query: { state: 'unknown', reason: 'query_contract_unverified' },
  independentGate: { state: 'unavailable', reason: 'consumer_contract_unconfigured' },
  policy,
  negotiated: {
    contractVersion: 1,
    identity: { state: 'available', reason: 'durable_inventory' },
    query: { state: 'unavailable', reason: 'query_consumer_unconfigured' },
    independentGate: { state: 'unavailable', reason: 'consumer_contract_unconfigured' },
    policyLocked: true,
    snapshot: { version: 1, complete: true, ...scope, resources: [sourceRow()] },
  },
});
let server: Server | undefined;
let renderer: ReactTestRenderer | undefined;
let base = '';
function Harness({ rows }: { rows: ProviderRow[] }) {
  const protection = useResourceProtection(
    { checking: false, managerServiceAvailable: true, managerServiceBase: base },
    rows
  );
  return (
    <ProviderTable
      rows={rows}
      resourceProtection={protection.status === 'identity_only' ? 'unbound' : protection.status}
      resourceProtectionForRow={(row) => protection.forProvider(row, rows)}
      loading={false}
      actionsDisabled={false}
      toggleDisabled={false}
      resolvedTheme="light"
      emptyState={null}
      onShowDetail={() => {}}
      onEdit={() => {}}
      onDelete={() => {}}
      onToggle={() => {}}
      onPriorityChange={() => {}}
    />
  );
}
function RoutedEditControls() {
  const { form, setForm, handleSave } = useOutletContext<OpenAIEditOutletContext>();
  return (
    <>
      <input
        value={form.name}
        onChange={(e) => setForm((prev) => ({ ...prev, name: e.target.value }))}
      />
      <button onClick={handleSave}>{en.common.save}</button>
    </>
  );
}
const text = (node: ReactTestInstance): string =>
  node.children.map((child) => (typeof child === 'string' ? child : text(child))).join('');
const indicators = () =>
  renderer!.root.findAll(
    (node) =>
      node.type === 'button' &&
      String(node.props['aria-label']).startsWith(en.resource_protection.title + ':')
  );
afterEach(async () => {
  if (renderer) {
    await act(async () => renderer!.unmount());
    renderer = undefined;
  }
  if (server) {
    const closed = new Promise<void>((resolve) => server!.close(() => resolve()));
    server.closeAllConnections();
    await closed;
    server = undefined;
  }
  vi.unstubAllGlobals();
});

it('renders a native provider association only from its issued marker and current catalog versions', async () => {
  vi.stubGlobal('window', {
    location: { host: '127.0.0.1' },
    addEventListener: () => {},
    removeEventListener: () => {},
  });
  const values = new Map<string, string>();
  vi.stubGlobal('localStorage', {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => values.set(key, value),
    removeItem: (key: string) => values.delete(key),
  });
  server = createServer(async (req, res) => {
    res.setHeader('Content-Type', 'application/json');
    if (req.headers.authorization !== 'Bearer synthetic-panel-key') {
      res.writeHead(401).end('{}');
      return;
    }
    if (req.url === '/v0/management/codex-api-key') {
      res.end(
        JSON.stringify({
          'codex-api-key': [
            {
              'api-key': 'CANARY-BOUND',
              'base-url': 'https://synthetic.invalid/v1',
              'resource-id': id,
            },
            { 'api-key': 'CANARY-UNBOUND', 'base-url': 'https://synthetic.invalid/v1' },
          ],
        })
      );
      return;
    }
    if (req.url?.startsWith('/usage-service/resource-capabilities?inventory=1')) {
      res.end(JSON.stringify(catalog()));
      return;
    }
    res.writeHead(404).end('{}');
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
  apiClient.setConfig({ apiBase: base, managementKey: 'synthetic-panel-key' });
  useAuthStore.setState({ apiBase: base, managementKey: 'synthetic-panel-key' });
  const codex = await providersApi.getCodexConfigs();
  const rows = buildProviderRows({
    gemini: [],
    codex,
    claude: [],
    vertex: [],
    openai: [],
    usageByProvider: new Map(),
  });
  const i18n = createInstance();
  await i18n.init({ lng: 'en', resources: { en: { translation: en } } });
  await act(async () => {
    renderer = create(
      <I18nextProvider i18n={i18n}>
        <Harness rows={rows} />
      </I18nextProvider>
    );
  });
  await vi.waitFor(async () => {
    await act(async () => {
      await Promise.resolve();
    });
    expect(
      indicators().filter((node) =>
        String(node.props['aria-label']).includes(en.resource_protection.identity_only)
      )
    ).toHaveLength(1);
  });
  expect(
    indicators().filter((node) =>
      String(node.props['aria-label']).includes(en.resource_protection.unbound)
    )
  ).toHaveLength(1);
  const bound = indicators().find((node) =>
    String(node.props['aria-label']).includes(en.resource_protection.identity_only)
  )!;
  await act(async () => bound.props.onClick());
  const tooltip = renderer!.root.findByProps({
    role: 'tooltip',
    id: bound.props['aria-describedby'],
  });
  expect(text(tooltip)).toContain(id);
  expect(text(tooltip)).toContain(scope.instance_id);
  expect(text(tooltip)).toContain('credential_version');
  expect(text(tooltip)).not.toMatch(/CANARY|synthetic-panel-key|synthetic.invalid/);
});

async function mountSourceFixture(native: Record<string, unknown[]>, summary: unknown) {
  vi.stubGlobal('window', {
    location: { host: '127.0.0.1' },
    addEventListener: () => {},
    removeEventListener: () => {},
  });
  const values = new Map<string, string>();
  vi.stubGlobal('localStorage', {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => values.set(key, value),
    removeItem: (key: string) => values.delete(key),
  });
  server = createServer(async (req, res) => {
    res.setHeader('Content-Type', 'application/json');
    if (req.headers.authorization !== 'Bearer synthetic-panel-key') {
      res.writeHead(401).end('{}');
      return;
    }
    if (req.url === '/v0/management/config') {
      res.writeHead(503).end('{}');
      return;
    }
    if (req.url?.startsWith('/v0/management/')) {
      res.end(JSON.stringify(native));
      return;
    }
    if (req.url?.startsWith('/usage-service/resource-capabilities?inventory=1')) {
      res.end(JSON.stringify(typeof summary === 'function' ? await summary() : summary));
      return;
    }
    res.writeHead(404).end('{}');
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
  apiClient.setConfig({ apiBase: base, managementKey: 'synthetic-panel-key' });
  useAuthStore.setState({ apiBase: base, managementKey: 'synthetic-panel-key' });
  const codex = native['codex-api-key'] ? await providersApi.getCodexConfigs() : [];
  const openai = native['openai-compatibility'] ? await providersApi.getOpenAIProviders() : [];
  const gemini = native['gemini-api-key'] ? await providersApi.getGeminiKeys() : [];
  const interactions = native['interactions-api-key']
    ? await providersApi.getInteractionsKeys()
    : [];
  const claude = native['claude-api-key'] ? await providersApi.getClaudeConfigs() : [];
  const xai = native['xai-api-key'] ? await providersApi.getXAIConfigs() : [];
  const meta = native['meta-api-key'] ? await providersApi.getMetaConfigs() : [];
  const vertex = native['vertex-api-key'] ? await providersApi.getVertexConfigs() : [];
  const rows = buildProviderRows({
    gemini,
    interactions,
    codex,
    xai,
    meta,
    claude,
    vertex,
    openai,
    usageByProvider: new Map(),
  });
  const i18n = createInstance();
  await i18n.init({ lng: 'en', resources: { en: { translation: en } } });
  await act(async () => {
    renderer = create(
      <I18nextProvider i18n={i18n}>
        <Harness rows={rows} />
      </I18nextProvider>
    );
  });
  return { rows, i18n };
}

it('associates an OpenAI provider and every distinct member with their exact issued targets', async () => {
  const group = '66666666-6666-4666-8666-666666666666';
  const groupTarget = { ...target, resource_id: group, membership_version: 2 };
  const summary = catalog();
  summary.negotiated.snapshot.resources = [
    { ...summary.negotiated.snapshot.resources[0], ...target, parent_id: group },
    {
      ...summary.negotiated.snapshot.resources[0],
      ...groupTarget,
      kind: 'provider' as const,
      members: [id],
    },
  ].map((row) => ({ ...row, family: 'openai-compatibility' }));
  await mountSourceFixture(
    {
      'openai-compatibility': [
        {
          name: 'same-display-name',
          'base-url': 'https://synthetic.invalid/v1',
          'resource-id': group,
          'api-key-entries': [{ 'api-key': 'CANARY-OAI', 'resource-id': id }],
        },
      ],
    },
    summary
  );
  await vi.waitFor(async () => {
    await act(async () => {
      await Promise.resolve();
    });
    expect(
      indicators().filter((node) =>
        String(node.props['aria-label']).includes(en.resource_protection.identity_only)
      )
    ).toHaveLength(1);
  });
  const bound = indicators()[0];
  await act(async () => bound.props.onClick());
  const tooltip = text(
    renderer!.root.findByProps({ role: 'tooltip', id: bound.props['aria-describedby'] })
  );
  expect(tooltip).toContain(group);
  expect(tooltip).toContain(id);
  expect(tooltip).toContain('membership_version');
  expect(tooltip).not.toMatch(/CANARY|synthetic.invalid|synthetic-panel-key/);
});

it.each(['drawer', 'routed editor'] as const)(
  'retains issued OpenAI group and member markers when the user edits and saves the real %s',
  async (mode) => {
    vi.stubGlobal('window', {
      location: { host: '127.0.0.1' },
      addEventListener: () => {},
      removeEventListener: () => {},
    });
    const values = new Map<string, string>();
    vi.stubGlobal('localStorage', {
      getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => values.set(key, value),
      removeItem: (key: string) => values.delete(key),
    });
    const member = '66666666-6666-4666-8666-666666666666';
    let saved: unknown;
    server = createServer(async (req, res) => {
      res.setHeader('Content-Type', 'application/json');
      if (req.headers.authorization !== 'Bearer synthetic-panel-key') {
        res.writeHead(401).end('{}');
        return;
      }
      if (
        req.method === 'GET' &&
        (req.url === '/v0/management/config' || req.url === '/v0/management/openai-compatibility')
      ) {
        res.end(
          JSON.stringify({
            'openai-compatibility': [
              {
                name: 'editable',
                'base-url': 'https://synthetic.invalid/v1',
                'resource-id': id,
                'api-key-entries': [{ 'api-key': 'CANARY-EDIT', 'resource-id': member }],
              },
            ],
          })
        );
        return;
      }
      if (req.method === 'PUT' && req.url === '/v0/management/openai-compatibility') {
        let data = '';
        for await (const chunk of req) data += chunk;
        saved = JSON.parse(data);
        res.end('{}');
        return;
      }
      res.writeHead(404).end('{}');
    });
    server.listen(0, '127.0.0.1');
    await once(server, 'listening');
    base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
    apiClient.setConfig({ apiBase: base, managementKey: 'synthetic-panel-key' });
    useAuthStore.setState({
      apiBase: base,
      managementKey: 'synthetic-panel-key',
      connectionStatus: 'connected',
    });
    useConfigStore.setState({ config: null, cache: new Map() });
    const i18n = createInstance();
    await i18n.init({ lng: 'en', resources: { en: { translation: en } } });
    const editor =
      mode === 'drawer' ? (
        <OpenAIEditDrawer
          open
          editIndex={0}
          disabled={false}
          onClose={() => {}}
          onSaved={() => {}}
        />
      ) : (
        <RouterProvider
          router={createMemoryRouter(
            [
              {
                path: '/ai-providers/openai/:index',
                element: <AiProvidersOpenAIEditLayout />,
                children: [{ index: true, element: <RoutedEditControls /> }],
              },
              { path: '/ai-providers', element: <div>Saved</div> },
            ],
            { initialEntries: ['/ai-providers/openai/0'] }
          )}
        />
      );
    await act(async () => {
      renderer = create(<I18nextProvider i18n={i18n}>{editor}</I18nextProvider>);
    });
    await vi.waitFor(async () => {
      await act(async () => {
        await Promise.resolve();
      });
      expect(
        renderer!.root.findAll((node) => node.type === 'input' && node.props.value === 'editable')
      ).toHaveLength(1);
    });
    await act(async () =>
      renderer!.root
        .find((node) => node.type === 'input' && node.props.value === 'editable')
        .props.onChange({ target: { value: 'renamed' } })
    );
    await act(async () => {
      await renderer!.root
        .find((node) => node.type === 'button' && text(node) === en.common.save)
        .props.onClick();
    });
    expect(saved).toMatchObject([
      {
        'resource-id': id,
        name: 'renamed',
        'api-key-entries': [{ 'resource-id': member, 'api-key': 'CANARY-EDIT' }],
      },
    ]);
  }
);

it.each(['gemini', 'interactions', 'codex', 'xai', 'meta', 'claude', 'vertex'] as const)(
  'resolves the supported native %s source through its real public normalizer',
  async (family) => {
    const summary = catalog();
    summary.negotiated.snapshot.resources[0].family = family;
    await mountSourceFixture(
      { [`${family}-api-key`]: [{ 'api-key': 'CANARY-NATIVE', 'resource-id': id }] },
      summary
    );
    await vi.waitFor(async () => {
      await act(async () => {
        await Promise.resolve();
      });
      expect(indicators()).toHaveLength(1);
      expect(indicators()[0].props['aria-label']).toContain(en.resource_protection.identity_only);
    });
  }
);

it('includes the issued native parent provider in the OfficialBalance binding set', async () => {
  const parent = '66666666-6666-4666-8666-666666666666';
  const summary = catalog();
  summary.negotiated.snapshot.resources = [
    { ...sourceRow(), family: 'claude', parent_id: parent },
    {
      ...sourceRow(),
      resource_id: parent,
      family: 'claude',
      kind: 'provider',
      members: [id],
      membership_version: 1,
      parent_id: undefined,
    },
  ];
  await mountSourceFixture(
    { 'claude-api-key': [{ 'api-key': 'CANARY-NATIVE', 'resource-id': id }] },
    summary
  );
  await vi.waitFor(async () => {
    await act(async () => {
      await Promise.resolve();
    });
    expect(indicators()).toHaveLength(1);
    expect(indicators()[0].props['aria-label']).toContain(en.resource_protection.identity_only);
  });
  const bound = indicators()[0];
  await act(async () => bound.props.onClick());
  const tip = text(
    renderer!.root.findByProps({ role: 'tooltip', id: bound.props['aria-describedby'] })
  );
  expect(tip).toContain(id);
  expect(tip).toContain(parent);
});

it('keeps a native credential unbound when its parent provider membership is incomplete', async () => {
  const parent = '66666666-6666-4666-8666-666666666666';
  const summary = catalog();
  summary.negotiated.snapshot.resources = [
    { ...sourceRow(), family: 'claude', parent_id: parent },
    {
      ...sourceRow(),
      resource_id: parent,
      family: 'claude',
      kind: 'provider',
      members: ['77777777-7777-4777-8777-777777777777'],
      membership_version: 1,
      parent_id: undefined,
    },
  ];
  await mountSourceFixture(
    { 'claude-api-key': [{ 'api-key': 'CANARY-NATIVE', 'resource-id': id }] },
    summary
  );
  await vi.waitFor(async () => {
    await act(async () => {
      await Promise.resolve();
    });
    expect(indicators()).toHaveLength(1);
    expect(indicators()[0].props['aria-label']).toContain(en.resource_protection.unbound);
  });
});

it.each([
  'absent marker',
  'malformed marker',
  'wrong family',
  'duplicate source',
  'duplicate catalog',
  'ambiguous catalog',
  'not ready catalog',
  'wrong kind',
  'bad version',
  'incomplete snapshot',
] as const)('keeps %s unbound without a label, key or ordinal fallback', async (scenario) => {
  const summary = catalog();
  const source: Record<string, unknown> = { 'api-key': 'CANARY-NATIVE', 'resource-id': id };
  const records = [source];
  const row = summary.negotiated.snapshot.resources[0];
  if (scenario === 'absent marker') delete source['resource-id'];
  if (scenario === 'malformed marker') source['resource-id'] = 'CANARY-NOT-AN-ID';
  if (scenario === 'wrong family') row.family = 'claude';
  if (scenario === 'duplicate source') records.push({ ...source });
  if (scenario === 'duplicate catalog') summary.negotiated.snapshot.resources.push({ ...row });
  if (scenario === 'ambiguous catalog') row.identity = 'ambiguous';
  if (scenario === 'not ready catalog') row.catalog_readiness = 'not_ready';
  if (scenario === 'wrong kind') row.kind = 'provider';
  if (scenario === 'bad version') row.credential_version = 0;
  if (scenario === 'incomplete snapshot') summary.negotiated.snapshot.complete = false;
  await mountSourceFixture({ 'codex-api-key': records }, summary);
  await vi.waitFor(async () => {
    await act(async () => {
      await Promise.resolve();
    });
    expect(indicators()).toHaveLength(records.length);
    const expected =
      scenario === 'incomplete snapshot'
        ? en.resource_protection.unknown
        : en.resource_protection.unbound;
    expect(indicators().every((node) => String(node.props['aria-label']).includes(expected))).toBe(
      true
    );
  });
  const indicator = indicators()[0];
  await act(async () => indicator.props.onClick());
  expect(
    text(renderer!.root.findByProps({ role: 'tooltip', id: indicator.props['aria-describedby'] }))
  ).not.toMatch(/CANARY|resource_id|synthetic.invalid|synthetic-panel-key/);
});

it('refuses an OpenAI group when a submitted member has no issued marker', async () => {
  const group = '66666666-6666-4666-8666-666666666666';
  const summary = catalog();
  summary.negotiated.snapshot.resources = [
    { ...sourceRow(), family: 'openai-compatibility', parent_id: group },
    {
      ...sourceRow(),
      resource_id: group,
      family: 'openai-compatibility',
      kind: 'provider',
      members: [id],
      membership_version: 2,
    },
  ];
  await mountSourceFixture(
    {
      'openai-compatibility': [
        {
          name: 'same-display-name',
          'base-url': 'https://synthetic.invalid/v1',
          'resource-id': group,
          'api-key-entries': [{ 'api-key': 'CANARY-OAI' }],
        },
      ],
    },
    summary
  );
  await vi.waitFor(async () => {
    await act(async () => {
      await Promise.resolve();
    });
    expect(indicators()[0].props['aria-label']).toContain(en.resource_protection.unbound);
  });
});

it('refuses a provider association when an issued member marker is duplicated in another source group', async () => {
  const group = '66666666-6666-4666-8666-666666666666';
  const otherGroup = '77777777-7777-4777-8777-777777777777';
  const summary = catalog();
  summary.negotiated.snapshot.resources = [
    { ...sourceRow(), family: 'openai-compatibility', parent_id: group },
    {
      ...sourceRow(),
      resource_id: group,
      family: 'openai-compatibility',
      kind: 'provider',
      members: [id],
      membership_version: 2,
    },
  ];
  await mountSourceFixture(
    {
      'openai-compatibility': [group, otherGroup].map((marker) => ({
        name: 'same-name',
        'base-url': 'https://synthetic.invalid/v1',
        'resource-id': marker,
        'api-key-entries': [{ 'api-key': 'CANARY-OAI', 'resource-id': id }],
      })),
    },
    summary
  );
  await vi.waitFor(async () => {
    await act(async () => {
      await Promise.resolve();
    });
    expect(
      indicators().every((node) =>
        String(node.props['aria-label']).includes(en.resource_protection.unbound)
      )
    ).toBe(true);
  });
});

it('discards a late native-source negotiation after the source list changes', async () => {
  let completeOld: (value: unknown) => void = () => {};
  const oldResponse = new Promise<unknown>((resolve) => {
    completeOld = resolve;
  });
  let calls = 0;
  const unavailable = catalog();
  const response = {
    ...unavailable,
    negotiated: {
      ...unavailable.negotiated,
      identity: { state: 'unavailable', reason: 'not_ready' },
      snapshot: undefined,
    },
  };
  const { rows, i18n } = await mountSourceFixture(
    { 'codex-api-key': [{ 'api-key': 'CANARY-NATIVE', 'resource-id': id }] },
    () => (++calls === 1 ? oldResponse : Promise.resolve(response))
  );
  await vi.waitFor(() => expect(calls).toBe(1));
  const changedRows = buildProviderRows({
    gemini: [],
    codex: [{ apiKey: 'CANARY-REPLACED', resourceId: id }],
    claude: [],
    vertex: [],
    openai: [],
    usageByProvider: new Map(),
  });
  await act(async () =>
    renderer!.update(
      <I18nextProvider i18n={i18n}>
        <Harness rows={changedRows} />
      </I18nextProvider>
    )
  );
  expect(indicators()[0].props['aria-label']).toContain(en.resource_protection.unknown);
  await vi.waitFor(async () => {
    await act(async () => {
      await Promise.resolve();
    });
    expect(indicators()[0].props['aria-label']).toContain(en.resource_protection.unavailable);
  });
  await act(async () => {
    completeOld(catalog());
    await new Promise((resolve) => setTimeout(resolve, 30));
  });
  expect(indicators()[0].props['aria-label']).toContain(en.resource_protection.unavailable);
  expect(rows[0].raw.resourceId).toBe(id);
});
