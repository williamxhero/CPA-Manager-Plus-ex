import { beforeEach, describe, expect, it, vi } from 'vitest';

const { request } = vi.hoisted(() => ({ request: vi.fn() }));

vi.mock('@/services/api', () => ({
  apiCallApi: { request },
  getApiCallErrorDetails: vi.fn(() => 'request failed'),
}));

import { testOpenAIKey } from './openAIKeyTest';

describe('testOpenAIKey', () => {
  beforeEach(() => {
    request.mockReset();
    request.mockResolvedValue({ statusCode: 200 });
  });

  it('checks /models without consuming chat completion quota', async () => {
    await testOpenAIKey({
      baseUrl: 'https://api.example.com/v1/',
      keyEntry: { apiKey: ' secret ', proxyUrl: 'http://proxy.example:8080' },
      headers: [{ key: 'X-Test', value: ' value ' }],
    });

    expect(request).toHaveBeenCalledWith(
      {
        method: 'GET',
        url: 'https://api.example.com/v1/models',
        proxyUrl: 'http://proxy.example:8080',
        header: {
          'X-Test': 'value',
          Authorization: 'Bearer secret',
        },
      },
      { timeout: 30_000 }
    );
  });

  it('uses the auth index token placeholder when testing a managed credential', async () => {
    await testOpenAIKey({
      baseUrl: 'http://127.0.0.1:8317',
      keyEntry: { apiKey: '' },
      headers: [],
      authIndex: 'qwen-main',
    });

    expect(request).toHaveBeenCalledWith(
      expect.objectContaining({
        authIndex: 'qwen-main',
        method: 'GET',
        url: 'http://127.0.0.1:8317/models',
        header: { Authorization: 'Bearer $TOKEN$' },
      }),
      expect.anything()
    );
  });

  it('keeps the same indexed blank entry keyless only when explicitly requested', async () => {
    await testOpenAIKey({
      baseUrl: 'https://api.example.com/v1',
      keyEntry: { apiKey: '', proxyUrl: 'http://proxy.example:8080' },
      headers: [],
      authIndex: 'managed-index',
      mode: { kind: 'chat', model: ' test-model ', auth: 'keyless' },
    });

    expect(request).toHaveBeenCalledWith(
      {
        authIndex: 'managed-index',
        proxyUrl: 'http://proxy.example:8080',
        method: 'POST',
        url: 'https://api.example.com/v1/chat/completions',
        header: { 'Content-Type': 'application/json' },
        data: JSON.stringify({
          model: 'test-model',
          messages: [{ role: 'user', content: 'Hi' }],
          stream: false,
          max_tokens: 5,
        }),
      },
      { timeout: 30_000 }
    );
  });

  it('supports explicit anonymous models and preserves custom authorization in chat', async () => {
    await testOpenAIKey({
      baseUrl: 'https://api.example.com/v1',
      keyEntry: { apiKey: '' },
      headers: [],
      authIndex: 'managed-index',
      mode: { kind: 'models', auth: 'keyless' },
    });
    expect(request.mock.calls[0][0]).toMatchObject({
      method: 'GET', header: undefined, authIndex: 'managed-index',
    });

    await testOpenAIKey({
      baseUrl: 'https://api.example.com/v1',
      keyEntry: { apiKey: '' },
      headers: [{ key: 'authorization', value: 'Custom value' }],
      authIndex: 'managed-index',
      mode: { kind: 'chat', model: 'test-model', auth: 'keyless' },
    });
    expect(request.mock.calls[1][0].header).toEqual({
      'Content-Type': 'application/json', authorization: 'Custom value',
    });
  });

  it('rejects empty keyed credentials and missing chat models before requests', async () => {
    const input = {
      baseUrl: 'https://api.example.com/v1',
      keyEntry: { apiKey: '' },
      headers: [],
      authIndex: 'managed-index',
    };
    await expect(testOpenAIKey({ ...input, mode: { kind: 'models', auth: 'keyed' } }))
      .rejects.toThrow('API key');
    await expect(testOpenAIKey({ ...input, mode: { kind: 'chat', model: '   ', auth: 'keyless' } }))
      .rejects.toThrow('model');
    expect(request).not.toHaveBeenCalled();
  });

  it('uses indexed placeholder for explicit keyed chat without overriding custom auth', async () => {
    await testOpenAIKey({
      baseUrl: 'https://api.example.com/v1',
      keyEntry: { apiKey: 'real-key' },
      headers: [],
      authIndex: 'managed-index',
      mode: { kind: 'chat', model: 'test-model', auth: 'keyed' },
    });
    expect(request.mock.calls[0][0].header).toEqual({
      'Content-Type': 'application/json', Authorization: 'Bearer $TOKEN$',
    });
  });

  it('preserves upstream error details', async () => {
    request.mockResolvedValueOnce({ statusCode: 429, bodyText: '{"error":"quota"}' });
    await expect(
      testOpenAIKey({
        baseUrl: 'https://api.example.com/v1',
        keyEntry: { apiKey: 'secret' },
        headers: [],
      })
    ).rejects.toThrow('request failed');
  });
});
