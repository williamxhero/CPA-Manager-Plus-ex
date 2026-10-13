import { beforeEach, describe, expect, it, vi } from 'vitest';

const { mocks } = vi.hoisted(() => ({
  mocks: {
    get: vi.fn(),
    post: vi.fn(),
    delete: vi.fn(),
    getRaw: vi.fn(),
    patch: vi.fn(),
  },
}));

vi.mock('./client', () => ({
  apiClient: {
    get: mocks.get,
    post: mocks.post,
    delete: mocks.delete,
    getRaw: mocks.getRaw,
    patch: mocks.patch,
  },
  createScopedApiRequestConfig: (scope: { apiBase: string; managementKey: string }) => ({
    baseURL: `${scope.apiBase.replace(/\/+$/, '')}/v0/management`,
    headers: { Authorization: `Bearer ${scope.managementKey}` },
    cpampScopedRequest: true,
  }),
}));

import { oauthApi } from './oauth';
import { sha256RawTextHex } from '@/utils/apiKeyHash';

beforeEach(() => {
  mocks.get.mockReset();
  mocks.post.mockReset();
  mocks.delete.mockReset();
  mocks.getRaw.mockReset();
  mocks.patch.mockReset();
});

describe('oauthApi', () => {
  const requestScope = { apiBase: 'http://cpa.example:8317', managementKey: 'test-key' };

  it.each(['qwen-cliproxyapi', 'opencode-go-cliproxyapi'])(
    'publishes %s credentials to scoped core file APIs before returning the created label',
    async (pluginId) => {
      const fields = {
        base_url: 'https://upstream.example/v1',
        api_key: 'test-secret',
        name: 'Work',
      };
      const provider = pluginId === 'qwen-cliproxyapi' ? 'qwen' : 'opencode-go';
      const id = `${provider}-key-${sha256RawTextHex(`${fields.api_key}\0${fields.base_url}`)}`;
      const response = { ok: true, id, label: 'Work' };
      mocks.get
        .mockResolvedValueOnce({ files: [] })
        .mockResolvedValueOnce({
          files: [
            {
              name: `${id}.json`,
              id,
              type: provider,
              auth_index: 'synthetic-index',
              disabled: false,
              status: 'active',
            },
          ],
        })
        .mockResolvedValueOnce({
          files: [
            {
              name: `${id}.json`,
              id,
              type: provider,
              auth_index: 'synthetic-index',
              disabled: false,
              status: 'active',
            },
          ],
        })
        .mockResolvedValueOnce({ models: [{ id: 'synthetic-model' }] });
      mocks.getRaw
        .mockRejectedValueOnce(Object.assign(new Error('not found'), { status: 404 }))
        .mockResolvedValue({
          data: new Blob([
            JSON.stringify({
              type: provider,
              id,
              api_key: fields.api_key,
              base_url: fields.base_url,
              label: 'Work',
              disabled: false,
            }),
          ]),
        });
      mocks.post.mockResolvedValue({ status: 'ok' });
      mocks.patch.mockResolvedValue({ status: 'ok' });
      await expect(
        oauthApi.submitPluginCredential(
          pluginId,
          `/v0/management/plugins/${pluginId}/credentials`,
          fields,
          requestScope
        )
      ).resolves.toEqual(response);
      expect(mocks.post).toHaveBeenCalledWith(
        `/auth-files?name=${id}.json`,
        {
          type: provider,
          id,
          api_key: fields.api_key,
          base_url: fields.base_url,
          label: 'Work',
          disabled: false,
        },
        {
          baseURL: 'http://cpa.example:8317/v0/management',
          headers: { Authorization: 'Bearer test-key', 'X-CPAMP-Auth-File-Create-Only': 'true' },
          cpampScopedRequest: true,
        }
      );
      expect(mocks.patch).toHaveBeenCalledWith(
        '/auth-files/fields',
        expect.objectContaining({ name: `${id}.json`, api_key: fields.api_key }),
        expect.objectContaining({ baseURL: 'http://cpa.example:8317/v0/management' })
      );
    }
  );

  it.each([409, 400, 404, undefined])(
    'preserves credential failure status %s and plugin message',
    async (status) => {
      const error = Object.assign(new Error('plugin error verbatim'), { status });
      mocks.post.mockRejectedValue(error);
      await expect(
        oauthApi.submitPluginCredential(
          'sample-plugin',
          '/v0/management/plugins/sample-plugin/credentials',
          { base_url: 'https://upstream.example', api_key: 'test-secret', name: '' },
          requestScope
        )
      ).rejects.toBe(error);
    }
  );

  it.each([
    { ok: false, error: 'invalid credential' },
    { status: 'error', error: 'plugin failure' },
    {},
    { status: 'ok', uploaded: 0 },
  ])(
    'does not mistake an unsuccessful/malformed plan response for credential creation: %j',
    async (response) => {
      mocks.get.mockResolvedValue({ files: [] });
      mocks.getRaw.mockRejectedValue(Object.assign(new Error('not found'), { status: 404 }));
      mocks.post.mockResolvedValue(response);
      await expect(
        oauthApi.submitPluginCredential(
          'qwen-cliproxyapi',
          '/v0/management/plugins/qwen-cliproxyapi/credentials',
          { api_key: 'synthetic-key-a' },
          requestScope
        )
      ).rejects.toThrow('rollback could not be confirmed');
      expect(mocks.patch).not.toHaveBeenCalled();
    }
  );

  it('rejects a submit path outside the plugin management namespace before sending secrets', async () => {
    await expect(
      oauthApi.submitPluginCredential(
        'qwen-cliproxyapi',
        'https://untrusted.example/credentials',
        { api_key: 'test-secret' },
        requestScope
      )
    ).rejects.toThrow('Invalid plugin credential submit path');
    expect(mocks.post).not.toHaveBeenCalled();
  });

  it('marks built-in web UI OAuth starts with is_webui', async () => {
    mocks.get.mockResolvedValue({ url: 'https://auth.example/codex', state: 'state-1' });

    await oauthApi.startAuth('codex');

    expect(mocks.get).toHaveBeenCalledWith('/codex-auth-url', {
      params: { is_webui: true },
    });
  });

  it('starts plugin OAuth providers through their dynamic auth-url endpoint', async () => {
    mocks.get.mockResolvedValue({ url: 'https://auth.example/plugin', state: 'state-2' });

    await oauthApi.startAuth('sample-provider');

    expect(mocks.get).toHaveBeenCalledWith('/sample-provider-auth-url', {
      params: undefined,
    });
  });

  it('pins auth-link, polling, and callback requests to the captured CPA scope', async () => {
    const requestScope = {
      apiBase: 'http://old-cpa.local:8317',
      managementKey: 'old-cpa-key',
    };
    const scopedConfig = {
      baseURL: 'http://old-cpa.local:8317/v0/management',
      headers: { Authorization: 'Bearer old-cpa-key' },
      cpampScopedRequest: true,
    };
    mocks.get
      .mockResolvedValueOnce({ url: 'https://auth.example/codex', state: 'state-1' })
      .mockResolvedValueOnce({ status: 'wait' });
    mocks.post.mockResolvedValue({ status: 'ok' });

    await oauthApi.startAuth('codex', requestScope);
    await oauthApi.getAuthStatus('state-1', requestScope);
    await oauthApi.submitCallback('codex', 'http://localhost/callback?code=1', requestScope);

    expect(mocks.get).toHaveBeenNthCalledWith(1, '/codex-auth-url', {
      ...scopedConfig,
      params: { is_webui: true },
    });
    expect(mocks.get).toHaveBeenNthCalledWith(2, '/get-auth-status', {
      ...scopedConfig,
      params: { state: 'state-1' },
    });
    expect(mocks.post).toHaveBeenCalledWith(
      '/oauth-callback',
      {
        provider: 'codex',
        redirect_url: 'http://localhost/callback?code=1',
      },
      scopedConfig
    );
  });

  it('starts Devin OAuth with is_webui flag', async () => {
    mocks.get.mockResolvedValue({ url: 'https://auth.example/devin', state: 'state-devin-1' });

    await oauthApi.startAuth('devin');

    expect(mocks.get).toHaveBeenCalledWith('/devin-auth-url', {
      params: { is_webui: true },
    });
  });

  it('cancels an active OAuth session using DELETE /oauth-session with captured scope', async () => {
    const requestScope = {
      apiBase: 'http://cpa.example:8317',
      managementKey: 'cpa-key-1',
    };
    const scopedConfig = {
      baseURL: 'http://cpa.example:8317/v0/management',
      headers: { Authorization: 'Bearer cpa-key-1' },
      cpampScopedRequest: true,
    };
    mocks.delete.mockResolvedValue({ status: 'ok', cancelled: true });

    const result = await oauthApi.cancelSession('state-devin-1', requestScope);

    expect(mocks.delete).toHaveBeenCalledWith('/oauth-session', {
      ...scopedConfig,
      params: { state: 'state-devin-1' },
    });
    expect(result).toEqual({ status: 'ok', cancelled: true });
  });

  it('starts Meta OAuth without is_webui flag and preserves device flow fields', async () => {
    const metaResponse = {
      url: 'https://auth.example/device',
      state: 'state-meta-1',
      user_code: 'ABCD-EFGH',
      flow: 'device',
      expires_in: 600,
    };
    mocks.get.mockResolvedValue(metaResponse);

    const result = await oauthApi.startAuth('meta');

    expect(mocks.get).toHaveBeenCalledWith('/meta-auth-url', {
      params: undefined,
    });
    expect(result).toEqual(metaResponse);
  });
});
