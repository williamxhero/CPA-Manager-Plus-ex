import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  getRaw: vi.fn(),
  post: vi.fn(),
  postForm: vi.fn(),
  patch: vi.fn(),
  delete: vi.fn(),
}));
vi.mock('./client', () => ({
  apiClient: mocks,
  createScopedApiRequestConfig: (scope: { apiBase: string; managementKey: string }) => ({
    baseURL: `${scope.apiBase}/v0/management`,
    headers: { Authorization: `Bearer ${scope.managementKey}` },
    cpampScopedRequest: true,
  }),
}));
import { oauthApi } from './oauth';
import { planCredentialsApi, PLAN_CREDENTIAL_DEFAULT_URLS } from './planCredentials';
import { sha256RawTextHex } from '@/utils/apiKeyHash';

const scope = {
  apiBase: 'https://synthetic-cpa.invalid',
  managementKey: 'synthetic-management-key',
};
const baseURL = 'https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1';
const key = 'synthetic-key-a';
const id = 'qwen-key-7723abccf56a2b3cd06d8395c27cbc21a106fe4376267498edddc201942d8efa';
const name = `${id}.json`;
const runtime = {
  name,
  id,
  type: 'qwen',
  auth_index: 'synthetic-index',
  disabled: false,
  status: 'active',
};
const create = () =>
  oauthApi.submitPluginCredential(
    'qwen-cliproxyapi',
    '/v0/management/plugins/qwen-cliproxyapi/credentials',
    { api_key: key, base_url: baseURL, name: 'Synthetic alias' },
    scope
  );

beforeEach(() => {
  Object.values(mocks).forEach((mock) => mock.mockReset());
});

describe('plan credential runtime activation', () => {
  it('does not expose malformed credential source text in errors', async () => {
    mocks.getRaw.mockResolvedValue({ data: new Blob(['synthetic-key-sensitive']) });
    await expect(
      planCredentialsApi.update({ name, provider: 'qwen' }, { label: 'After' }, scope)
    ).rejects.toThrow('Credential source must contain one JSON object');
    expect(mocks.patch).not.toHaveBeenCalled();
  });

  it('does not delete a matching file when the initial POST acknowledgement was lost', async () => {
    mocks.get.mockResolvedValueOnce({ files: [] }).mockResolvedValue({ files: [runtime] });
    mocks.getRaw
      .mockRejectedValueOnce(Object.assign(new Error('not found'), { status: 404 }))
      .mockResolvedValue({
        data: new Blob([
          JSON.stringify({
            type: 'qwen',
            id,
            api_key: key,
            base_url: baseURL,
            label: 'Synthetic alias',
            disabled: false,
          }),
        ]),
      });
    mocks.post.mockRejectedValue(new Error('connection lost'));
    mocks.delete.mockResolvedValue({ status: 'ok' });
    await expect(create()).rejects.toThrow('rollback could not be confirmed');
    expect(mocks.delete).not.toHaveBeenCalled();
  });

  it('keeps legacy exclusions synchronized on the single plugin-aware PATCH', async () => {
    const before = {
      type: 'qwen',
      api_key: key,
      base_url: baseURL,
      disabled: true,
      excluded_models: ['old-model'],
    };
    const after = { ...before, excluded_models: ['new-model'] };
    mocks.getRaw
      .mockResolvedValueOnce({ data: new Blob([JSON.stringify(before)]) })
      .mockResolvedValue({ data: new Blob([JSON.stringify(after)]) });
    mocks.get.mockResolvedValue({ files: [{ ...runtime, disabled: true, status: 'disabled' }] });
    mocks.patch.mockResolvedValue({ status: 'ok' });
    await planCredentialsApi.update(
      { name, provider: 'qwen' },
      { 'excluded-models': ['new-model'], excluded_models: null },
      scope
    );
    expect(mocks.patch).toHaveBeenCalledWith(
      '/auth-files/fields',
      { name, 'excluded-models': ['new-model'], excluded_models: ['new-model'], disabled: true },
      expect.anything()
    );
  });

  it.each(['qwen', 'opencode-go'])(
    'accepts core-canonicalized exclusions for %s',
    async (provider) => {
      const before = {
        type: provider,
        api_key: key,
        base_url: PLAN_CREDENTIAL_DEFAULT_URLS[provider],
        disabled: false,
        excluded_models: ['old'],
      };
      const after = { ...before, excluded_models: ['new'] };
      mocks.getRaw
        .mockResolvedValueOnce({ data: new Blob([JSON.stringify(before)]) })
        .mockResolvedValueOnce({ data: new Blob([JSON.stringify(after)]) });
      mocks.get
        .mockResolvedValueOnce({ files: [{ ...runtime, type: provider }] })
        .mockResolvedValueOnce({ models: [{ id: 'model' }] });
      mocks.patch.mockResolvedValue({ status: 'ok' });
      await expect(
        planCredentialsApi.update({ name, provider }, { 'excluded-models': ['new'] }, scope)
      ).resolves.toBeUndefined();
    }
  );

  it.each(['qwen', 'opencode-go'])(
    'accepts clearing core-canonicalized exclusions for %s',
    async (provider) => {
      const before = {
        type: provider,
        api_key: key,
        base_url: PLAN_CREDENTIAL_DEFAULT_URLS[provider],
        disabled: false,
        excluded_models: ['old'],
        'excluded-models': ['old'],
      };
      const after: Record<string, unknown> = { ...before, excluded_models: [] };
      delete after['excluded-models'];
      mocks.getRaw
        .mockResolvedValueOnce({ data: new Blob([JSON.stringify(before)]) })
        .mockResolvedValueOnce({ data: new Blob([JSON.stringify(after)]) });
      mocks.get
        .mockResolvedValueOnce({ files: [{ ...runtime, type: provider }] })
        .mockResolvedValueOnce({ models: [{ id: 'model' }] });
      mocks.patch.mockResolvedValue({ status: 'ok' });
      await expect(
        planCredentialsApi.update({ name, provider }, { 'excluded-models': [] }, scope)
      ).resolves.toBeUndefined();
    }
  );

  it.each(['qwen', 'opencode-go'])(
    'accepts legacy exclusion aliases removed by core during unrelated saves for %s',
    async (provider) => {
      const before = {
        type: provider,
        api_key: key,
        base_url: PLAN_CREDENTIAL_DEFAULT_URLS[provider],
        disabled: true,
        'excluded-models': ['legacy'],
      };
      const after = { ...before, label: 'Renamed', excluded_models: ['legacy'] };
      delete (after as Record<string, unknown>)['excluded-models'];
      mocks.getRaw
        .mockResolvedValueOnce({ data: new Blob([JSON.stringify(before)]) })
        .mockResolvedValueOnce({ data: new Blob([JSON.stringify(after)]) });
      mocks.get.mockResolvedValue({ files: [{ ...runtime, type: provider, disabled: true }] });
      mocks.patch.mockResolvedValue({ status: 'ok' });
      await expect(
        planCredentialsApi.update({ name, provider }, { label: 'Renamed' }, scope)
      ).resolves.toBeUndefined();
    }
  );

  it.each(['other-provider', 'other-key', 'other-url'])(
    'refuses occupied digest files without overwriting (%s)',
    async (conflict) => {
      const existing = {
        type: conflict === 'other-provider' ? 'codex' : 'qwen',
        api_key: conflict === 'other-key' ? 'synthetic-key-b' : key,
        base_url: conflict === 'other-url' ? 'https://synthetic-other.invalid/v1' : baseURL,
      };
      mocks.get.mockResolvedValue({ files: [] });
      mocks.getRaw.mockResolvedValue({ data: new Blob([JSON.stringify(existing)]) });
      await expect(create()).rejects.toThrow('occupied');
      expect(mocks.post).not.toHaveBeenCalled();
      expect(mocks.patch).not.toHaveBeenCalled();
      expect(mocks.delete).not.toHaveBeenCalled();
    }
  );

  it('does not roll back a touched field changed by another caller before the first verification read', async () => {
    const before = {
      type: 'qwen',
      api_key: key,
      base_url: baseURL,
      disabled: true,
      note: 'original',
    };
    const concurrent = { ...before, api_key: 'synthetic-key-b', note: 'concurrent note' };
    mocks.getRaw
      .mockResolvedValueOnce({ data: new Blob([JSON.stringify(before)]) })
      .mockResolvedValue({ data: new Blob([JSON.stringify(concurrent)]) });
    mocks.get.mockResolvedValue({ files: [] });
    mocks.patch.mockResolvedValue({ status: 'ok' });
    mocks.post.mockResolvedValue({ status: 'ok' });
    await expect(
      planCredentialsApi.update(
        { name, provider: 'qwen' },
        { api_key: 'synthetic-key-b', note: 'requested note' },
        scope
      )
    ).rejects.toThrow('rollback could not be confirmed');
    expect(mocks.post).not.toHaveBeenCalled();
  });

  it('uses the provider default for a legacy blank stored URL on label-only edits', async () => {
    const before = { type: 'qwen', api_key: key, base_url: '', disabled: true, label: 'Before' };
    mocks.getRaw
      .mockResolvedValueOnce({ data: new Blob([JSON.stringify(before)]) })
      .mockResolvedValue({ data: new Blob([JSON.stringify({ ...before, label: 'After' })]) });
    mocks.get.mockResolvedValue({ files: [{ ...runtime, disabled: true, status: 'disabled' }] });
    mocks.patch.mockResolvedValue({ status: 'ok' });
    await expect(
      planCredentialsApi.update({ name, provider: 'qwen' }, { label: 'After' }, scope)
    ).resolves.toBeUndefined();
  });

  it('does not discard concurrent metadata edits during rollback', async () => {
    const before = {
      type: 'qwen',
      api_key: key,
      base_url: baseURL,
      disabled: true,
      note: 'original',
    };
    const concurrent = { ...before, api_key: 'synthetic-key-b', note: 'concurrent note' };
    mocks.getRaw
      .mockResolvedValueOnce({ data: new Blob([JSON.stringify(before)]) })
      .mockResolvedValue({ data: new Blob([JSON.stringify(concurrent)]) });
    mocks.get
      .mockResolvedValueOnce({ files: [] })
      .mockResolvedValue({ files: [{ ...runtime, disabled: true, status: 'disabled' }] });
    mocks.patch.mockResolvedValue({ status: 'ok' });
    mocks.post.mockResolvedValue({ status: 'ok' });
    await expect(
      planCredentialsApi.update({ name, provider: 'qwen' }, { api_key: 'synthetic-key-b' }, scope)
    ).rejects.toThrow('rollback could not be confirmed');
    expect(mocks.post).not.toHaveBeenCalled();
  });

  it('does not roll back over a concurrently replaced credential', async () => {
    const before = { type: 'qwen', api_key: key, base_url: baseURL, disabled: true };
    const concurrent = { ...before, api_key: 'synthetic-key-c' };
    mocks.getRaw
      .mockResolvedValueOnce({ data: new Blob([JSON.stringify(before)]) })
      .mockResolvedValue({ data: new Blob([JSON.stringify(concurrent)]) });
    mocks.patch.mockResolvedValue({ status: 'ok' });
    await expect(
      planCredentialsApi.update({ name, provider: 'qwen' }, { api_key: 'synthetic-key-b' }, scope)
    ).rejects.toThrow('rollback could not be confirmed');
    expect(mocks.post).not.toHaveBeenCalled();
  });

  it.each(['missing', 'wrong-provider'])(
    'restores the complete previous file and republishes it when edited runtime is %s',
    async (failure) => {
      const before = {
        type: 'qwen',
        id,
        api_key: key,
        base_url: baseURL,
        label: 'Before',
        disabled: true,
        custom: 'preserve',
      };
      const after = { ...before, api_key: 'synthetic-key-b', label: 'After' };
      const oldText = JSON.stringify(before);
      const nextText = JSON.stringify(after);
      mocks.getRaw
        .mockResolvedValueOnce({ data: new Blob([oldText]) })
        .mockResolvedValueOnce({ data: new Blob([nextText]) })
        .mockResolvedValueOnce({ data: new Blob([nextText]) })
        .mockResolvedValue({ data: new Blob([oldText]) });
      mocks.get
        .mockResolvedValueOnce({
          files: failure === 'missing' ? [] : [{ ...runtime, type: 'codex', disabled: true }],
        })
        .mockResolvedValue({ files: [{ ...runtime, disabled: true, status: 'disabled' }] });
      mocks.patch.mockResolvedValue({ status: 'ok' });
      mocks.post.mockResolvedValue({ status: 'ok' });
      await expect(
        planCredentialsApi.update(
          { name, runtimeId: id, provider: 'qwen' },
          { api_key: 'synthetic-key-b', label: 'After' },
          scope
        )
      ).rejects.toThrow('rolled back');
      expect(mocks.post).toHaveBeenCalledWith(
        `/auth-files?name=${name}`,
        oldText,
        expect.objectContaining({
          headers: expect.objectContaining({
            'X-CPAMP-Auth-File-Expected-Content-SHA256': sha256RawTextHex(nextText),
          }),
        })
      );
      expect(mocks.patch).toHaveBeenLastCalledWith(
        '/auth-files/fields',
        { name, api_key: key, base_url: baseURL, label: 'Before', disabled: true },
        expect.objectContaining({
          headers: expect.objectContaining({
            'X-CPAMP-Auth-File-Mutation-Identity': encodeURIComponent(
              JSON.stringify([{ name, provider: 'qwen' }])
            ),
            'X-CPAMP-Auth-File-Expected-Content-SHA256': sha256RawTextHex(oldText),
          }),
        })
      );
    }
  );

  it('edits in place without uploading and preserves a manually disabled credential', async () => {
    const oldText = JSON.stringify({
      type: 'qwen',
      id,
      api_key: key,
      base_url: baseURL,
      disabled: true,
      custom: 'preserve',
    });
    const nextText = JSON.stringify({
      type: 'qwen',
      id,
      api_key: 'synthetic-key-b',
      base_url: baseURL,
      disabled: true,
      custom: 'preserve',
    });
    mocks.getRaw
      .mockResolvedValueOnce({ data: new Blob([oldText]) })
      .mockResolvedValue({ data: new Blob([nextText]) });
    mocks.get.mockResolvedValue({ files: [{ ...runtime, disabled: true, status: 'disabled' }] });
    mocks.patch.mockResolvedValue({ status: 'ok' });

    await planCredentialsApi.update(
      { name, runtimeId: id, provider: 'qwen' },
      { api_key: 'synthetic-key-b' },
      scope
    );
    expect(mocks.patch).toHaveBeenCalledWith(
      '/auth-files/fields',
      { name: id, api_key: 'synthetic-key-b', disabled: true },
      expect.objectContaining({
        headers: expect.objectContaining({
          'X-CPAMP-Auth-File-Expected-Content-SHA256': sha256RawTextHex(oldText),
        }),
      })
    );
    expect(mocks.post).not.toHaveBeenCalled();
    expect(mocks.postForm).not.toHaveBeenCalled();
    expect(mocks.get).not.toHaveBeenCalledWith(
      expect.stringContaining('/models'),
      expect.anything()
    );
  });
  it('verifies the persisted creation fields after PATCH before reporting success', async () => {
    const saved = {
      type: 'qwen',
      id,
      api_key: key,
      base_url: baseURL,
      label: 'Synthetic alias',
      disabled: false,
    };
    mocks.get
      .mockResolvedValueOnce({ files: [] })
      .mockResolvedValue({ files: [runtime], models: [{ id: 'synthetic-model' }] });
    mocks.getRaw
      .mockRejectedValueOnce(Object.assign(new Error('not found'), { status: 404 }))
      .mockResolvedValueOnce({ data: new Blob([JSON.stringify(saved)]) })
      .mockResolvedValue({
        data: new Blob([JSON.stringify({ ...saved, api_key: 'synthetic-key-c' })]),
      });
    mocks.post.mockResolvedValue({ status: 'ok' });
    mocks.patch.mockResolvedValue({ status: 'ok' });
    await expect(create()).rejects.toThrow('rollback could not be confirmed');
    expect(mocks.delete).not.toHaveBeenCalled();
  });

  it('does not delete a newly created file modified concurrently before rollback', async () => {
    const saved = {
      type: 'qwen',
      id,
      api_key: key,
      base_url: baseURL,
      label: 'Synthetic alias',
      disabled: false,
    };
    mocks.get.mockResolvedValueOnce({ files: [] }).mockResolvedValue({ files: [] });
    mocks.getRaw
      .mockRejectedValueOnce(Object.assign(new Error('not found'), { status: 404 }))
      .mockResolvedValueOnce({ data: new Blob([JSON.stringify(saved)]) })
      .mockResolvedValue({
        data: new Blob([JSON.stringify({ ...saved, note: 'concurrent note' })]),
      });
    mocks.post.mockResolvedValue({ status: 'ok' });
    mocks.patch.mockResolvedValue({ status: 'ok' });
    mocks.delete.mockResolvedValue({ status: 'ok' });
    await expect(create()).rejects.toThrow('rollback could not be confirmed');
    expect(mocks.delete).not.toHaveBeenCalled();
  });

  it('removes a newly created file when runtime validation fails and reports rollback', async () => {
    const saved = JSON.stringify({
      type: 'qwen',
      id,
      api_key: key,
      base_url: baseURL,
      label: 'Synthetic alias',
      disabled: false,
    });
    mocks.get
      .mockResolvedValueOnce({ files: [] })
      .mockResolvedValueOnce({ files: [] })
      .mockResolvedValueOnce({ files: [runtime] });
    mocks.getRaw
      .mockRejectedValueOnce(Object.assign(new Error('not found'), { status: 404 }))
      .mockResolvedValue({ data: new Blob([saved]) });
    mocks.post.mockResolvedValue({ status: 'ok' });
    mocks.patch.mockResolvedValue({ status: 'ok' });
    mocks.delete.mockResolvedValue({ status: 'ok' });

    await expect(create()).rejects.toThrow('rolled back');
    expect(mocks.delete).toHaveBeenCalledWith(
      `/auth-files?name=${name}`,
      expect.objectContaining({
        headers: expect.objectContaining({
          'X-CPAMP-Auth-File-Physical-Name': name,
          'X-CPAMP-Auth-File-Expected-Content-SHA256': sha256RawTextHex(saved),
        }),
      })
    );
  });
  it('creates a file then publishes the same fields via plugin-aware PATCH before reporting success', async () => {
    mocks.get
      .mockResolvedValueOnce({ files: [] })
      .mockResolvedValueOnce({ files: [runtime] })
      .mockResolvedValueOnce({ files: [runtime] })
      .mockResolvedValueOnce({ models: [{ id: 'synthetic-model' }] });
    mocks.getRaw
      .mockRejectedValueOnce(Object.assign(new Error('not found'), { status: 404 }))
      .mockResolvedValue({
        data: new Blob([
          JSON.stringify({
            type: 'qwen',
            id,
            api_key: key,
            base_url: baseURL,
            label: 'Synthetic alias',
            disabled: false,
          }),
        ]),
      });
    mocks.post.mockResolvedValue({ status: 'ok', ok: true, id, label: 'Synthetic alias' });
    mocks.patch.mockResolvedValue({ status: 'ok' });

    await expect(create()).resolves.toMatchObject({ ok: true, id, label: 'Synthetic alias' });
    expect(mocks.post).toHaveBeenCalledWith(
      `/auth-files?name=${name}`,
      expect.objectContaining({ type: 'qwen', api_key: key, base_url: baseURL, disabled: false }),
      expect.objectContaining({
        headers: expect.objectContaining({ 'X-CPAMP-Auth-File-Create-Only': 'true' }),
      })
    );
    const patchCalls = mocks.patch.mock.calls;
    const patchCall = patchCalls[patchCalls.length - 1];
    expect(patchCall[0]).toBe('/auth-files/fields');
    expect(patchCall[1]).toMatchObject({
      name,
      api_key: key,
      base_url: baseURL,
      label: 'Synthetic alias',
      disabled: false,
    });
    expect(patchCall[2].baseURL).toBe(`${scope.apiBase}/v0/management`);
    expect(patchCall[2].headers['X-CPAMP-Auth-File-Mutation-Identity']).toBe(
      encodeURIComponent(
        JSON.stringify([{ name, provider: 'qwen', runtimeId: id, authIndex: 'synthetic-index' }])
      )
    );
  });
});
