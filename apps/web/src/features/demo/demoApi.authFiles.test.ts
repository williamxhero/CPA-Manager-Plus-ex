import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  handleDemoApiRequest,
  handleDemoFormRequest,
  handleDemoRawRequest,
  resetDemoAuthFileConfiguration,
} from './demoApi';
import { getDemoApiCallResult, resetDemoCredentialRefresh } from './demoFixtures';
import type { AuthFilesResponse } from '@/types/authFile';
import { authFilesApi } from '@/services/api/authFiles';
import { planCredentialsApi } from '@/services/api/planCredentials';
import { setDemoMode } from './demoMode';
import { sha256RawTextHex } from '@/utils/apiKeyHash';

const DEMO_AUTH_ID = 'codex-upgrade-demo-runtime';
const DEMO_AUTH_NAME = 'codex-upgrade-demo.json';
const FORCE_REFRESH_TIMESTAMP = '2000-01-01T00:00:00Z';

const getUpgradeDemoAuth = async () => {
  const response = await handleDemoApiRequest<AuthFilesResponse>('get', '/auth-files');
  const target = response.files.find((file) => file.id === DEMO_AUTH_ID);
  if (!target) throw new Error('missing Codex upgrade demo auth file');
  return target;
};

describe('auth file credential refresh demo API', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-07-22T09:30:00+08:00'));
    resetDemoCredentialRefresh();
    resetDemoAuthFileConfiguration();
  });

  afterEach(() => {
    setDemoMode(false);
    resetDemoCredentialRefresh();
    resetDemoAuthFileConfiguration();
    vi.useRealTimers();
  });

  it.each(['qwen', 'opencode-go'])(
    'creates a %s demo plan credential through POST and runtime PATCH',
    async (provider) => {
      setDemoMode(true);
      const scope = {
        apiBase: 'https://synthetic-cpa.invalid',
        managementKey: 'synthetic-management-key',
      };
      const fields = {
        api_key: `fictional-demo-${provider}-new-key`,
        base_url: 'https://synthetic-plan.invalid/v1',
        name: 'Synthetic plan',
      };
      const created = await planCredentialsApi.create(`${provider}-cliproxyapi`, fields, scope);
      const fileName = `${created.id}.json`;
      expect(await authFilesApi.downloadJsonObject(fileName, scope)).toEqual({
        type: provider,
        id: created.id,
        api_key: fields.api_key,
        base_url: fields.base_url,
        label: 'Synthetic plan',
        disabled: false,
      });
      const runtime = await authFilesApi.lookup({ name: fileName }, scope);
      expect(runtime).toHaveLength(1);
      expect(runtime[0]).toMatchObject({
        name: fileName,
        provider,
        type: provider,
        disabled: false,
        status: 'active',
        authIndex: expect.any(String),
      });
      expect(runtime[0]).not.toHaveProperty('api_key');
      expect(await authFilesApi.getModelsForAuthFile(runtime[0].id || fileName, scope)).toEqual([
        expect.objectContaining({ id: provider === 'qwen' ? 'qwen-plus' : 'opencode-go-demo' }),
      ]);
      await expect(
        planCredentialsApi.create(`${provider}-cliproxyapi`, fields, scope)
      ).rejects.toThrow('already occupied');
      resetDemoAuthFileConfiguration();
      expect(await authFilesApi.lookup({ name: fileName }, scope)).toEqual([]);
      await expect(authFilesApi.downloadText(fileName, scope)).rejects.toMatchObject({
        status: 404,
      });
    }
  );

  it.each(['qwen', 'opencode-go'])(
    'edits a disabled %s demo plan without enabling it or dropping metadata',
    async (provider) => {
      setDemoMode(true);
      const scope = {
        apiBase: 'https://synthetic-cpa.invalid',
        managementKey: 'synthetic-management-key',
      };
      const created = await planCredentialsApi.create(
        `${provider}-cliproxyapi`,
        {
          api_key: `fictional-demo-${provider}-edit-key`,
          base_url: 'https://synthetic-plan.invalid/v1',
          name: 'Old label',
        },
        scope
      );
      const name = `${created.id}.json`;
      await handleDemoApiRequest('patch', '/auth-files/fields', {
        name,
        disabled: true,
        quota: { remaining: 42 },
        excluded_models: ['old-model'],
      });
      const [before] = await authFilesApi.lookup({ name }, scope);
      expect(before.disabled).toBe(true);
      await planCredentialsApi.update(
        { name, runtimeId: before.id, authIndex: before.authIndex, provider },
        {
          api_key: 'fictional-demo-replacement-key',
          base_url: 'https://replacement-plan.invalid/v1',
          label: '',
          note: 'Updated note',
          'excluded-models': ['new-model'],
          excluded_models: null,
          disabled: false,
        },
        scope
      );
      expect(await authFilesApi.downloadJsonObject(name, scope)).toMatchObject({
        api_key: 'fictional-demo-replacement-key',
        base_url: 'https://replacement-plan.invalid/v1',
        label: '',
        note: 'Updated note',
        'excluded-models': ['new-model'],
        excluded_models: ['new-model'],
        disabled: true,
        quota: { remaining: 42 },
      });
      expect(await authFilesApi.lookup({ name }, scope)).toEqual([
        expect.objectContaining({ id: before.id, authIndex: before.authIndex, disabled: true }),
      ]);
    }
  );

  it('preserves exact raw plan snapshots and refuses stale rollback writes', async () => {
    const name = 'qwen-synthetic-snapshot.json';
    const original =
      '{\n "type":"qwen", "id":"snapshot-id", "api_key":"fictional-snapshot-key", "base_url":"https://synthetic-plan.invalid/v1", "disabled":true\n}';
    await handleDemoApiRequest('post', `/auth-files?name=${name}`, original);
    const read = async () =>
      (
        await handleDemoRawRequest(`/auth-files/download?name=${name}`)
      ).data.text() as Promise<string>;
    expect(await read()).toBe(original);
    // The real core lists an uploaded credential immediately (verified against an
    // isolated Manager), so the runtime locator is available right after the write.
    expect(
      (await handleDemoApiRequest<AuthFilesResponse>('get', '/auth-files')).files.some(
        (file) => file.name === name
      )
    ).toBe(true);
    await handleDemoApiRequest('patch', '/auth-files/fields', { name, label: 'Edited' });
    const current = await read();
    await expect(
      handleDemoApiRequest('post', `/auth-files?name=${name}`, original, {
        headers: { 'X-CPAMP-Auth-File-Expected-Content-SHA256': sha256RawTextHex(original) },
      })
    ).rejects.toMatchObject({ status: 409 });
    expect(await read()).toBe(current);
    await handleDemoApiRequest('post', `/auth-files?name=${name}`, original, {
      headers: { 'X-CPAMP-Auth-File-Expected-Content-SHA256': sha256RawTextHex(current) },
    });
    expect(await read()).toBe(original);
    expect(
      (await handleDemoApiRequest<AuthFilesResponse>('get', '/auth-files')).files.find(
        (file) => file.name === name
      )
    ).toMatchObject({ disabled: true });
  });

  it('simulates a delayed Free to Plus refresh for the runtime auth ID', async () => {
    resetDemoCredentialRefresh();
    const initial = await getUpgradeDemoAuth();
    const initialLastRefresh = initial.last_refresh;

    expect(initial).toMatchObject({
      id: DEMO_AUTH_ID,
      name: DEMO_AUTH_NAME,
      plan_type: 'free',
      id_token: { plan_type: 'free' },
    });
    expect(
      getDemoApiCallResult({
        authIndex: 'codex-upgrade-demo-01',
        url: 'https://chatgpt.com/backend-api/wham/usage',
      }).body
    ).toMatchObject({
      plan_type: 'free',
      subscription_active_until: null,
    });

    await handleDemoApiRequest('patch', '/auth-files/fields', {
      name: DEMO_AUTH_ID,
      expired: FORCE_REFRESH_TIMESTAMP,
      last_refresh: FORCE_REFRESH_TIMESTAMP,
    });

    const firstPoll = await getUpgradeDemoAuth();
    expect(firstPoll.plan_type).toBe('free');
    expect(firstPoll.last_refresh).toBe(initialLastRefresh);

    const completed = await getUpgradeDemoAuth();
    expect(completed).toMatchObject({
      plan_type: 'plus',
      id_token: {
        plan_type: 'plus',
        chatgpt_subscription_active_until: expect.any(String),
      },
    });
    expect(completed.statusMessage).toBeUndefined();
    expect(
      Date.parse(
        String((completed.id_token as Record<string, unknown>).chatgpt_subscription_active_until)
      )
    ).toBeGreaterThan(Date.now());
    const subscriptionActiveUntil = String(
      (completed.id_token as Record<string, unknown>).chatgpt_subscription_active_until
    );
    expect(subscriptionActiveUntil).toBe('2026-08-21T01:30:00.000Z');
    expect(completed.last_refresh).toBe('2026-07-22T01:30:00.000Z');
    expect(completed.last_refresh).not.toBe(initialLastRefresh);
    expect(
      getDemoApiCallResult({
        authIndex: 'codex-upgrade-demo-01',
        url: 'https://chatgpt.com/backend-api/wham/usage',
      }).body
    ).toMatchObject({
      plan_type: 'plus',
      subscription_active_until: subscriptionActiveUntil,
    });

    vi.advanceTimersByTime(7 * 24 * 60 * 60 * 1000);
    const later = await getUpgradeDemoAuth();
    expect(
      String((later.id_token as Record<string, unknown>).chatgpt_subscription_active_until)
    ).toBe(subscriptionActiveUntil);
    expect(
      getDemoApiCallResult({
        authIndex: 'codex-upgrade-demo-01',
        url: 'https://chatgpt.com/backend-api/wham/usage',
      }).body
    ).toMatchObject({
      plan_type: 'plus',
      subscription_active_until: subscriptionActiveUntil,
    });
  });

  it('does not upgrade the demo account for an ordinary fields patch', async () => {
    await handleDemoApiRequest('patch', '/auth-files/fields', {
      name: DEMO_AUTH_NAME,
      note: 'Demo note',
    });

    await getUpgradeDemoAuth();
    const target = await getUpgradeDemoAuth();

    expect(target.plan_type).toBe('free');
  });

  it('resets the upgraded account back to Free', async () => {
    await handleDemoApiRequest('patch', '/auth-files/fields', {
      name: DEMO_AUTH_NAME,
      expired: FORCE_REFRESH_TIMESTAMP,
      last_refresh: FORCE_REFRESH_TIMESTAMP,
    });
    await getUpgradeDemoAuth();
    expect((await getUpgradeDemoAuth()).plan_type).toBe('plus');

    resetDemoCredentialRefresh();

    expect((await getUpgradeDemoAuth()).plan_type).toBe('free');
  });

  it('downloads the same fictional credential by runtime id and file name', async () => {
    const byName = await handleDemoRawRequest(
      `/auth-files/download?name=${encodeURIComponent(DEMO_AUTH_NAME)}`
    );
    const byId = await handleDemoRawRequest(
      `/auth-files/download?name=${encodeURIComponent(DEMO_AUTH_ID)}`
    );
    const nameRecord = JSON.parse(await (byName.data as Blob).text()) as Record<string, unknown>;
    const idRecord = JSON.parse(await (byId.data as Blob).text()) as Record<string, unknown>;

    expect(nameRecord).toMatchObject({ type: 'codex', auth_index: 'codex-upgrade-demo-01' });
    expect(idRecord).toEqual(nameRecord);
    expect(nameRecord.access_token).toBe('fictional-demo-codex-access-token');
  });

  it('preserves provider-specific fictional configuration fields', async () => {
    const read = async (name: string) => {
      const response = await handleDemoRawRequest(
        `/auth-files/download?name=${encodeURIComponent(name)}`
      );
      return JSON.parse(await (response.data as Blob).text()) as Record<string, unknown>;
    };

    expect(await read('xai-ops.json')).toMatchObject({
      type: 'xai',
      using_api: false,
      websockets: true,
      base_url: 'https://grok-demo-gateway.invalid/v1',
    });
    expect(await read('xai-payg-buffer.json')).toMatchObject({
      type: 'xai',
      using_api: true,
      base_url: 'https://api.x.ai/v1',
    });
    expect(await read('claude-team-01.json')).toMatchObject({
      type: 'claude',
      cloak_mode: 'auto',
      cloak_strict_mode: 'true',
      cloak_sensitive_words: 'internal,confidential',
    });
  });

  it('persists ordinary configuration patches for the demo session and resets them', async () => {
    await handleDemoApiRequest('patch', '/auth-files/fields', {
      name: DEMO_AUTH_ID,
      note: 'Updated demo note',
      'excluded-models': ['gpt-5'],
      headers: { 'X-Demo-Tenant': '' },
    });

    const updated = await handleDemoRawRequest(
      `/auth-files/download?name=${encodeURIComponent(DEMO_AUTH_ID)}`
    );
    const updatedRecord = JSON.parse(await (updated.data as Blob).text()) as Record<
      string,
      unknown
    >;
    expect(updatedRecord.note).toBe('Updated demo note');
    expect(updatedRecord['excluded-models']).toEqual(['gpt-5']);
    expect(updatedRecord.headers).not.toHaveProperty('X-Demo-Tenant');

    resetDemoAuthFileConfiguration();
    const reset = await handleDemoRawRequest(
      `/auth-files/download?name=${encodeURIComponent(DEMO_AUTH_ID)}`
    );
    const resetRecord = JSON.parse(await (reset.data as Blob).text()) as Record<string, unknown>;
    expect(resetRecord.note).toBe('Fictional codex credential used by the CPAMP demo');
    expect(resetRecord['excluded-models']).toEqual(['o1-preview']);
  });

  it('matches real auth-file PATCH removal and zero-value semantics', async () => {
    await handleDemoApiRequest('patch', '/auth-files/fields', {
      name: DEMO_AUTH_ID,
      prefix: '   ',
      proxy_url: '',
      base_url: '',
      priority: 0,
      weight: null,
      note: '',
      'excluded-models': [],
      disable_cooling: false,
      request_retry: null,
      tool_prefix_disabled: false,
      headers: {
        Authorization: '',
        'X-Demo-Tenant': '   ',
      },
      websockets: false,
      using_api: false,
    });

    const updated = await handleDemoRawRequest(
      `/auth-files/download?name=${encodeURIComponent(DEMO_AUTH_ID)}`
    );
    const updatedRecord = JSON.parse(await (updated.data as Blob).text()) as Record<
      string,
      unknown
    >;

    expect(updatedRecord).not.toHaveProperty('prefix');
    expect(updatedRecord).not.toHaveProperty('proxy_url');
    expect(updatedRecord).not.toHaveProperty('base_url');
    expect(updatedRecord).not.toHaveProperty('priority');
    expect(updatedRecord).not.toHaveProperty('weight');
    expect(updatedRecord).not.toHaveProperty('note');
    expect(updatedRecord).not.toHaveProperty('excluded-models');
    expect(updatedRecord).not.toHaveProperty('excluded_models');
    expect(updatedRecord.disable_cooling).toBe(false);
    expect(updatedRecord).not.toHaveProperty('request_retry');
    expect(updatedRecord).not.toHaveProperty('tool_prefix_disabled');
    expect(updatedRecord).not.toHaveProperty('headers');
    expect(updatedRecord.websockets).toBe(false);
    expect(updatedRecord.using_api).toBe(false);
  });

  it('persists source-file uploads used to remove a legacy excluded_models alias', async () => {
    const formData = new FormData();
    formData.append(
      'file',
      new File(
        [
          JSON.stringify({
            type: 'codex',
            auth_index: 'codex-upgrade-demo-01',
            note: 'Uploaded source record',
            'excluded-models': ['gpt-5-codex'],
          }),
        ],
        DEMO_AUTH_NAME,
        { type: 'application/json' }
      )
    );

    const result = await handleDemoFormRequest<{
      uploaded: number;
      files: string[];
    }>('/auth-files', formData);
    const updated = await handleDemoRawRequest(
      `/auth-files/download?name=${encodeURIComponent(DEMO_AUTH_ID)}`
    );
    const updatedRecord = JSON.parse(await (updated.data as Blob).text()) as Record<
      string,
      unknown
    >;

    expect(result).toMatchObject({ uploaded: 1, files: [DEMO_AUTH_NAME] });
    expect(updatedRecord).toMatchObject({
      note: 'Uploaded source record',
      'excluded-models': ['gpt-5-codex'],
    });
    expect(updatedRecord).not.toHaveProperty('excluded_models');
  });

  it('reports invalid or non-object auth file uploads as failures', async () => {
    const invalidJson = new FormData();
    invalidJson.append(
      'file',
      new File(['{invalid'], 'invalid.json', { type: 'application/json' })
    );
    const invalidResult = await handleDemoFormRequest<{
      status: string;
      uploaded: number;
      files: string[];
      failed: Array<{ name: string; error: string }>;
    }>('/auth-files', invalidJson);

    expect(invalidResult).toMatchObject({
      status: 'error',
      uploaded: 0,
      files: [],
      failed: [{ name: 'invalid.json', error: 'Invalid auth file JSON' }],
    });

    const primitiveJson = new FormData();
    primitiveJson.append(
      'file',
      new File(['"not-an-auth-record"'], 'primitive.json', { type: 'application/json' })
    );
    const primitiveResult = await handleDemoFormRequest<{
      status: string;
      uploaded: number;
      failed: Array<{ name: string; error: string }>;
    }>('/auth-files', primitiveJson);

    expect(primitiveResult).toMatchObject({
      status: 'error',
      uploaded: 0,
      failed: [
        {
          name: 'primitive.json',
          error: 'Auth file must contain a JSON object or object array',
        },
      ],
    });

    const mixedArray = new FormData();
    mixedArray.append(
      'file',
      new File(
        [JSON.stringify([{ type: 'codex', auth_index: 'auth-1' }, 'invalid-member'])],
        DEMO_AUTH_NAME,
        { type: 'application/json' }
      )
    );
    const mixedResult = await handleDemoFormRequest<{
      status: string;
      uploaded: number;
      failed: Array<{ name: string; error: string }>;
    }>('/auth-files', mixedArray);

    expect(mixedResult).toMatchObject({
      status: 'error',
      uploaded: 0,
      failed: [
        {
          name: DEMO_AUTH_NAME,
          error: 'Auth file must contain a JSON object or object array',
        },
      ],
    });
  });

  it('serves runtime and static model catalogs for supported providers', async () => {
    const runtime = await handleDemoApiRequest<{ models: Array<{ id: string }> }>(
      'get',
      `/auth-files/models?name=${encodeURIComponent(DEMO_AUTH_ID)}`
    );
    const definitions = await handleDemoApiRequest<{ models: Array<{ id: string }> }>(
      'get',
      '/model-definitions/codex'
    );

    expect(runtime.models.map((model) => model.id)).toContain('gpt-5-codex');
    expect(definitions.models.map((model) => model.id)).toContain('o1-preview');
  });
});
