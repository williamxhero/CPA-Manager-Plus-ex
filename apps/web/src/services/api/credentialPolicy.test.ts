import { afterEach, expect, it, vi } from 'vitest';
import {
  getCredentialPolicy,
  getStoredCredentialPolicy,
  saveCredentialPolicy,
} from './credentialPolicy';

afterEach(() => vi.unstubAllGlobals());

it('reads stored emergency intent by issued subject without treating cached binding as current', async () => {
  const status = {
    subjectId: 'issued-subject',
    enabled: false,
    generation: 2,
    bindingCurrent: false,
    applied: false,
  };
  const fetcher = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify(status)));
  vi.stubGlobal('fetch', fetcher);
  await expect(
    getStoredCredentialPolicy('http://manager.invalid/', 'panel-key', 'issued-subject')
  ).resolves.toEqual(status);
  expect(fetcher.mock.calls[0][0]).toBe(
    'http://manager.invalid/usage-service/credential-policy?subjectId=issued-subject'
  );
  expect(fetcher).toHaveBeenCalledTimes(1);
});

it('reads the authenticated per-credential backend status without legacy fallback', async () => {
  const status = {
    enabled: false,
    generation: 0,
    owner: 'cpa-manager.credential-policy.v1/11111111-1111-4111-8111-111111111111',
    state: 'off',
    reason: 'gate_contract_unavailable',
    applied: false,
    availability: { state: 'unavailable', reason: 'gate_contract_unavailable' },
  };
  const fetcher = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify(status)));
  vi.stubGlobal('fetch', fetcher);
  const signal = new AbortController().signal;
  await expect(
    getCredentialPolicy('http://manager.invalid/', 'synthetic-panel-key', 'a b', signal)
  ).resolves.toEqual(status);
  expect(fetcher).toHaveBeenCalledTimes(1);
  expect(fetcher).toHaveBeenCalledWith(
    'http://manager.invalid/usage-service/credential-policy?authIndex=a%20b',
    {
      method: 'GET',
      headers: { Authorization: 'Bearer synthetic-panel-key', 'Content-Type': 'application/json' },
      body: undefined,
      signal,
      cache: 'no-store',
      redirect: 'error',
    }
  );
});

it('saves only current binding and generation and returns backend state rather than optimistic ON', async () => {
  const update = {
    scope: {
      instance_id: 'instance',
      instance_incarnation: 'incarnation',
      boot_epoch: 'boot',
      snapshot_revision: 2,
    },
    targets: [
      {
        resource_id: 'resource',
        incarnation: 'credential',
        identity_version: 1,
        credential_version: 1,
        config_version: 1,
        membership_version: 0,
      },
    ],
    auth_index: 'current-index',
    sourceVersion: 'source-version',
    expectedGeneration: 4,
    enabled: true,
  };
  const backend = { enabled: false, generation: 5, state: 'pending', applied: false };
  const fetcher = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify(backend)));
  vi.stubGlobal('fetch', fetcher);
  await expect(
    saveCredentialPolicy('http://manager.invalid', 'panel-key', update)
  ).resolves.toEqual(backend);
  expect(fetcher).toHaveBeenCalledWith('http://manager.invalid/usage-service/credential-policy', {
    method: 'PUT',
    headers: { Authorization: 'Bearer panel-key', 'Content-Type': 'application/json' },
    body: JSON.stringify(update),
    signal: undefined,
    cache: 'no-store',
    redirect: 'error',
  });
});

it('preserves a safe backend conflict reason without leaking raw errors or attempting a legacy mutation', async () => {
  const fetcher = vi
    .fn<typeof fetch>()
    .mockResolvedValueOnce(
      new Response(JSON.stringify({ error: 'policy_generation_conflict' }), { status: 409 })
    )
    .mockResolvedValueOnce(new Response('POLICY-SECRET-CANARY', { status: 503 }));
  vi.stubGlobal('fetch', fetcher);
  await expect(
    getCredentialPolicy('http://manager.invalid', 'panel-key', 'a')
  ).rejects.toMatchObject({ code: 'policy_generation_conflict', status: 409 });
  await expect(
    getCredentialPolicy('http://manager.invalid', 'panel-key', 'a')
  ).rejects.toMatchObject({
    message: 'credential_policy_request_failed',
    code: 'credential_policy_request_failed',
    status: 503,
  });
  expect(fetcher).toHaveBeenCalledTimes(2);
});
