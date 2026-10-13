import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { oauthApi } from '@/services/api/oauth';

vi.mock('@/features/demo/demoMode', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/features/demo/demoMode')>()),
  isDemoMode: () => false,
}));

let server: Server;
let scope: { apiBase: string; managementKey: string };
let response: unknown;
let responseStatus: number;
let requests: Array<{
  url?: string;
  method?: string;
  authorization?: string;
  contentType?: string;
  body: string;
}>;

let storedAuthFile: { name: string; text: string } | null = null;
let publishStatus = 200;

beforeEach(async () => {
  requests = [];
  response = { ok: true, id: 'fixture-credential-id', label: 'Work' };
  responseStatus = 200;
  storedAuthFile = null;
  publishStatus = 200;
  server = createServer((request, reply) => {
    let body = '';
    request.setEncoding('utf8');
    request.on('data', (chunk: string) => {
      body += chunk;
    });
    request.on('end', () => {
      requests.push({
        url: request.url,
        method: request.method,
        authorization: request.headers.authorization,
        contentType: request.headers['content-type'],
        headers: request.headers as Record<string, string | undefined>,
        body,
      });
      const url = request.url ?? '';
      const method = (request.method ?? 'GET').toUpperCase();
      const path = url.split('?')[0].replace(/^\/v0\/management/, '');
      const send = (status: number, payload: unknown) => {
        reply.writeHead(status, { 'Content-Type': 'application/json' });
        reply.end(JSON.stringify(payload));
      };
      // Error scenarios use the blanket fixture response so error text propagates unchanged.
      if (responseStatus !== 200) {
        reply.writeHead(responseStatus, { 'Content-Type': 'application/json' });
        reply.end(JSON.stringify(response));
        return;
      }
      // After the plugin route POST the publish flow drives the core file APIs:
      // occupancy read, runtime locator read, plugin-aware PATCH.
      if (path === '/auth-files/download') {
        const name = new URL(url, 'http://fixture').searchParams.get('name') ?? '';
        if (storedAuthFile && storedAuthFile.name === name)
          return send(200, JSON.parse(storedAuthFile.text));
        return send(404, { error: 'file not found' });
      }
      if (path === '/auth-files' && method === 'GET') {
        const record = storedAuthFile
          ? (JSON.parse(storedAuthFile.text) as Record<string, unknown>)
          : null;
        return send(200, {
          files: storedAuthFile
            ? [
                {
                  name: storedAuthFile.name,
                  id: record?.id,
                  type: record?.type,
                  provider: record?.type,
                  auth_index: 'fixture-index',
                  disabled: false,
                  status: 'active',
                },
              ]
            : [],
        });
      }
      if (path === '/auth-files' && method === 'POST') {
        const name = new URL(url, 'http://fixture').searchParams.get('name') ?? '';
        storedAuthFile = { name, text: body };
        return send(200, { status: 'ok' });
      }
      if (path === '/auth-files' && method === 'DELETE') {
        storedAuthFile = null;
        return send(200, { status: 'ok' });
      }
      if (path === '/auth-files/fields' && method === 'PATCH') {
        if (publishStatus !== 200) return send(publishStatus, { error: 'auth file identity mismatch' });
        if (storedAuthFile) {
          // `name` is the selector, not a stored field.
          const fields = JSON.parse(body) as Record<string, unknown>;
          delete fields.name;
          storedAuthFile = {
            ...storedAuthFile,
            text: JSON.stringify({
              ...(JSON.parse(storedAuthFile.text) as Record<string, unknown>),
              ...fields,
            }),
          };
        }
        return send(200, { status: 'ok' });
      }
      if (path === '/auth-files/models') return send(200, { models: [{ id: 'fixture-model' }] });
      reply.writeHead(responseStatus, { 'Content-Type': 'application/json' });
      reply.end(JSON.stringify(response));
    });
  });
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  scope = {
    apiBase: `http://127.0.0.1:${(server.address() as AddressInfo).port}`,
    managementKey: 'fixture-manager-key',
  };
});
afterEach(async () => {
  await new Promise<void>((resolve, reject) =>
    server.close((error) => (error ? reject(error) : resolve()))
  );
});

describe('plugin credential management HTTP contract', () => {
  it.each(['qwen-cliproxyapi', 'opencode-go-cliproxyapi'])(
    'routes %s credentials through the plugin-aware core file APIs, never the plugin route',
    async (pluginId) => {
      const provider = pluginId === 'qwen-cliproxyapi' ? 'qwen' : 'opencode-go';
      // A node HTTP fixture cannot serve the Blob download the publish flow needs, so the
      // call rejects here; this test pins the routing decision, not the completion.
      await expect(
        oauthApi.submitPluginCredential(
          pluginId,
          `/v0/management/plugins/${pluginId}/credentials`,
          { base_url: 'https://api.example/v1', api_key: 'fixture-secret', name: 'Work' },
          scope
        )
      ).rejects.toThrow();
      const sequence = requests.map((entry) => `${entry.method} ${(entry.url ?? '').split('?')[0]}`);
      expect(sequence[0]).toBe('GET /v0/management/auth-files');
      expect(sequence).toContain('POST /v0/management/auth-files');
      expect(sequence.some((entry) => entry.includes('/plugins/'))).toBe(false);
      const create = requests.find(
        (entry) => entry.method === 'POST' && (entry.url ?? '').startsWith('/v0/management/auth-files?')
      );
      expect(create?.authorization).toBe('Bearer fixture-manager-key');
      expect(JSON.parse(create?.body ?? '{}')).toMatchObject({
        type: provider,
        api_key: 'fixture-secret',
        base_url: 'https://api.example/v1',
      });
    }
  );

  it('preserves login-start metadata returned by an isolated HTTP fixture', async () => {
    response = {
      url: '/plugin/login',
      metadata: {
        auth_kind: 'manual_api_key',
        submit_path: '/v0/management/plugins/qwen-cliproxyapi/credentials',
        fields: [{ name: 'api_key' }],
      },
    };
    expect(await oauthApi.startAuth('qwen', scope)).toEqual(response);
    expect(requests[0].url).toBe('/v0/management/qwen-auth-url');
  });

  it('surfaces HTTP plugin error text unchanged', async () => {
    responseStatus = 422;
    response = { error: 'plugin says: invalid base url' };
    await expect(
      oauthApi.submitPluginCredential(
        'qwen-cliproxyapi',
        '/v0/management/plugins/qwen-cliproxyapi/credentials',
        { api_key: 'fixture-secret' },
        scope
      )
    ).rejects.toThrow('plugin says: invalid base url');
  });

  it('rejects external, cross-plugin, traversal, and query destinations before sending any credentials', async () => {
    for (const path of [
      'https://evil.example/credentials',
      '//evil.example/credentials',
      '/v0/management/plugins/other/credentials',
      '/v0/management/plugins/qwen-cliproxyapi/../other/credentials',
      '/v0/management/plugins/qwen-cliproxyapi/%2e%2e/other',
      '/v0/management/plugins/qwen-cliproxyapi/credentials?key=secret',
    ]) {
      await expect(
        oauthApi.submitPluginCredential(
          'qwen-cliproxyapi',
          path,
          { api_key: 'fixture-secret' },
          scope
        )
      ).rejects.toThrow('Invalid plugin credential submit path');
    }
    expect(requests).toHaveLength(0);
  });
});
