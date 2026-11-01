/// <reference types="node" />
import { createServer, type Server } from 'node:http';
import { once } from 'node:events';
import type { AddressInfo } from 'node:net';
import { afterEach, expect, it } from 'vitest';
import { apiClient } from './client';
import { providersApi } from './providers';

const marker = '11111111-1111-4111-8111-111111111111';
let server: Server | undefined;
afterEach(async () => {
  if (server) {
    const closing = new Promise<void>((resolve) => server!.close(() => resolve()));
    server.closeAllConnections();
    await closing;
    server = undefined;
  }
});

it('round-trips an issued native marker through public load and edited save when raw config is unavailable', async () => {
  let saved: unknown;
  server = createServer(async (req, res) => {
    if (req.headers.authorization !== 'Bearer synthetic-panel-key') {
      res.writeHead(401).end();
      return;
    }
    res.setHeader('Content-Type', 'application/json');
    if (req.url === '/v0/management/codex-api-key' && req.method === 'GET') {
      res.end(
        JSON.stringify({
          'codex-api-key': [
            {
              'api-key': 'CANARY-NATIVE-KEY',
              'base-url': 'https://synthetic.invalid/v1',
              'resource-id': marker,
            },
          ],
        })
      );
      return;
    }
    if (req.url === '/v0/management/config') {
      res.writeHead(503).end('{}');
      return;
    }
    if (req.url === '/v0/management/codex-api-key' && req.method === 'PUT') {
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
  apiClient.setConfig({
    apiBase: `http://127.0.0.1:${(server.address() as AddressInfo).port}`,
    managementKey: 'synthetic-panel-key',
  });
  const configs = await providersApi.getCodexConfigs();
  expect(configs[0]).toMatchObject({ resourceId: marker });
  await providersApi.saveCodexConfigs([
    { ...configs[0], apiKey: 'CANARY-REPLACED-KEY', priority: 3 },
  ]);
  expect(saved).toEqual([
    {
      'api-key': 'CANARY-REPLACED-KEY',
      'base-url': 'https://synthetic.invalid/v1',
      'resource-id': marker,
      priority: 3,
    },
  ]);
});

it.each([
  ['gemini-api-key', providersApi.getGeminiKeys, providersApi.saveGeminiKeys],
  ['interactions-api-key', providersApi.getInteractionsKeys, providersApi.saveInteractionsKeys],
  ['vertex-api-key', providersApi.getVertexConfigs, providersApi.saveVertexConfigs],
  ['meta-api-key', providersApi.getMetaConfigs, providersApi.saveMetaConfigs],
  ['claude-api-key', providersApi.getClaudeConfigs, providersApi.saveClaudeConfigs],
  ['xai-api-key', providersApi.getXAIConfigs, providersApi.saveXAIConfigs],
] as const)(
  'preserves issued %s source markers without a raw-config merge',
  async (section, load, save) => {
    let saved: unknown;
    server = createServer(async (req, res) => {
      res.setHeader('Content-Type', 'application/json');
      if (req.url === '/v0/management/config') {
        res.writeHead(503).end('{}');
        return;
      }
      if (req.url === `/v0/management/${section}` && req.method === 'GET') {
        res.end(
          JSON.stringify({
            [section]: [
              {
                'api-key': 'CANARY-NATIVE',
                'base-url': 'https://synthetic.invalid/v1',
                'resource-id': marker,
              },
            ],
          })
        );
        return;
      }
      if (req.url === `/v0/management/${section}` && req.method === 'PUT') {
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
    apiClient.setConfig({
      apiBase: `http://127.0.0.1:${(server.address() as AddressInfo).port}`,
      managementKey: 'synthetic-panel-key',
    });
    const configs = await load();
    expect(configs[0]).toMatchObject({ resourceId: marker });
    await save(configs);
    expect(saved).toEqual([
      {
        'api-key': 'CANARY-NATIVE',
        'base-url': 'https://synthetic.invalid/v1',
        'resource-id': marker,
      },
    ]);
  }
);

it('preserves distinct issued OpenAI group/member markers through public load and save', async () => {
  const member = '22222222-2222-4222-8222-222222222222';
  let saved: unknown;
  server = createServer(async (req, res) => {
    res.setHeader('Content-Type', 'application/json');
    if (req.url === '/v0/management/config') {
      res.writeHead(503).end('{}');
      return;
    }
    if (req.url === '/v0/management/openai-compatibility' && req.method === 'GET') {
      res.end(
        JSON.stringify({
          'openai-compatibility': [
            {
              name: 'renamable',
              'base-url': 'https://synthetic.invalid/v1',
              'resource-id': marker,
              'api-key-entries': [{ 'api-key': 'CANARY-OAI', 'resource-id': member }],
            },
          ],
        })
      );
      return;
    }
    if (req.url === '/v0/management/openai-compatibility' && req.method === 'PUT') {
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
  apiClient.setConfig({
    apiBase: `http://127.0.0.1:${(server.address() as AddressInfo).port}`,
    managementKey: 'synthetic-panel-key',
  });
  const configs = await providersApi.getOpenAIProviders();
  expect(configs[0]).toMatchObject({ resourceId: marker, apiKeyEntries: [{ resourceId: member }] });
  await providersApi.saveOpenAIProviders([{ ...configs[0], name: 'renamed' }]);
  expect(saved).toEqual([
    {
      name: 'renamed',
      'base-url': 'https://synthetic.invalid/v1',
      'resource-id': marker,
      'api-key-entries': [{ 'api-key': 'CANARY-OAI', 'resource-id': member }],
    },
  ]);
});

it('does not invent source markers for unmarked submitted rows from a raw key/position match', async () => {
  let saved: unknown;
  server = createServer(async (req, res) => {
    res.setHeader('Content-Type', 'application/json');
    if (req.url === '/v0/management/config') {
      res.end(
        JSON.stringify({
          'codex-api-key': [
            {
              'api-key': 'CANARY-NATIVE',
              'base-url': 'https://synthetic.invalid/v1',
              'resource-id': marker,
            },
          ],
        })
      );
      return;
    }
    if (req.method === 'PUT') {
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
  apiClient.setConfig({
    apiBase: `http://127.0.0.1:${(server.address() as AddressInfo).port}`,
    managementKey: 'synthetic-panel-key',
  });
  await providersApi.saveCodexConfigs([
    { apiKey: 'CANARY-NATIVE', baseUrl: 'https://synthetic.invalid/v1' },
  ]);
  expect(saved).toEqual([
    { 'api-key': 'CANARY-NATIVE', 'base-url': 'https://synthetic.invalid/v1' },
  ]);
});

it('preserves the matching issued source record when rows reorder and keys change together', async () => {
  const other = '22222222-2222-4222-8222-222222222222';
  let saved: unknown;
  const raw = [
    {
      'api-key': 'CANARY-A',
      'base-url': 'https://synthetic.invalid/v1',
      'resource-id': marker,
      'source-note': 'A',
    },
    {
      'api-key': 'CANARY-B',
      'base-url': 'https://synthetic.invalid/v1',
      'resource-id': other,
      'source-note': 'B',
    },
  ];
  server = createServer(async (req, res) => {
    res.setHeader('Content-Type', 'application/json');
    if (req.method === 'GET') {
      res.end(JSON.stringify({ 'codex-api-key': raw }));
      return;
    }
    let data = '';
    for await (const chunk of req) data += chunk;
    saved = JSON.parse(data);
    res.end('{}');
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  apiClient.setConfig({
    apiBase: `http://127.0.0.1:${(server.address() as AddressInfo).port}`,
    managementKey: 'synthetic-panel-key',
  });
  const loaded = await providersApi.getCodexConfigs();
  await providersApi.saveCodexConfigs([
    { ...loaded[1], apiKey: 'CANARY-B-NEW' },
    { ...loaded[0], apiKey: 'CANARY-A-NEW' },
  ]);
  expect(saved).toEqual([
    {
      'api-key': 'CANARY-B-NEW',
      'base-url': 'https://synthetic.invalid/v1',
      'resource-id': other,
      'source-note': 'B',
    },
    {
      'api-key': 'CANARY-A-NEW',
      'base-url': 'https://synthetic.invalid/v1',
      'resource-id': marker,
      'source-note': 'A',
    },
  ]);
});

it('retains an explicitly loaded marker across a form-style update that reconstructs editable fields', async () => {
  let saved: unknown;
  const raw = {
    'api-key': 'CANARY-NATIVE',
    'base-url': 'https://synthetic.invalid/v1',
    'resource-id': marker,
  };
  server = createServer(async (req, res) => {
    res.setHeader('Content-Type', 'application/json');
    if (req.method === 'GET') {
      res.end(JSON.stringify({ 'codex-api-key': [raw] }));
      return;
    }
    let data = '';
    for await (const chunk of req) data += chunk;
    saved = JSON.parse(data);
    res.end('{}');
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  apiClient.setConfig({
    apiBase: `http://127.0.0.1:${(server.address() as AddressInfo).port}`,
    managementKey: 'synthetic-panel-key',
  });
  const original = (await providersApi.getCodexConfigs())[0];
  await providersApi.updateCodexConfig(original, {
    apiKey: 'CANARY-EDITED',
    baseUrl: 'https://synthetic.invalid/v1',
  });
  expect(saved).toEqual([
    {
      'api-key': 'CANARY-EDITED',
      'base-url': 'https://synthetic.invalid/v1',
      'resource-id': marker,
    },
  ]);
});

it('updates the explicitly issued OpenAI source after same-name providers reorder', async () => {
  const other = '22222222-2222-4222-8222-222222222222';
  let saved: unknown;
  const a = {
    name: 'same-name',
    'base-url': 'https://synthetic.invalid/v1',
    'resource-id': marker,
    'api-key-entries': [],
  };
  const b = { ...a, 'resource-id': other };
  server = createServer(async (req, res) => {
    res.setHeader('Content-Type', 'application/json');
    if (req.method === 'GET') {
      res.end(
        JSON.stringify({
          'openai-compatibility': req.url === '/v0/management/config' ? [b, a] : [a, b],
        })
      );
      return;
    }
    let data = '';
    for await (const chunk of req) data += chunk;
    saved = JSON.parse(data);
    res.end('{}');
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  apiClient.setConfig({
    apiBase: `http://127.0.0.1:${(server.address() as AddressInfo).port}`,
    managementKey: 'synthetic-panel-key',
  });
  const original = (await providersApi.getOpenAIProviders())[0];
  await providersApi.updateOpenAIProvider(original.name, 0, { ...original, name: 'renamed' });
  expect(saved).toEqual([b, { ...a, name: 'renamed' }]);
});

it('rejects an edit when the submitted issued marker is ambiguous in the current source', async () => {
  let writes = 0;
  const raw = { 'api-key': 'CANARY-NATIVE', 'resource-id': marker };
  server = createServer(async (req, res) => {
    res.setHeader('Content-Type', 'application/json');
    if (req.method === 'GET') {
      res.end(JSON.stringify({ 'codex-api-key': [raw, { ...raw }] }));
      return;
    }
    writes++;
    res.end('{}');
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  apiClient.setConfig({
    apiBase: `http://127.0.0.1:${(server.address() as AddressInfo).port}`,
    managementKey: 'synthetic-panel-key',
  });
  const original = (await providersApi.getCodexConfigs())[0];
  await expect(
    providersApi.updateCodexConfig(original, { ...original, apiKey: 'CANARY-EDITED' })
  ).rejects.toThrow('Provider configuration changed');
  expect(writes).toBe(0);
});
