import { afterEach, expect, it, vi } from 'vitest';
import { credentialQuotaRequest } from './credentialQuota';

afterEach(() => vi.unstubAllGlobals());

const reject = (status: number, body: unknown) => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => Response.json(body, { status, statusText: 'rejected' }))
  );
};

it('keeps an unbound credential distinct from a changed identity', async () => {
  reject(409, { error: 'current_credential_source_unavailable' });
  await expect(credentialQuotaRequest('http://fixture', 'key', 'GET', '?authIndex=a')).rejects.toThrow(
    'current_credential_source_unavailable'
  );
});

it('still reports a real identity conflict as a changed source', async () => {
  reject(409, { error: 'identity_changed' });
  await expect(credentialQuotaRequest('http://fixture', 'key', 'GET', '?authIndex=a')).rejects.toThrow(
    'identity_or_source_changed'
  );
});

it('falls back to the status when the rejection carries no machine reason', async () => {
  reject(409, { message: 'conflict' });
  await expect(credentialQuotaRequest('http://fixture', 'key', 'GET', '?authIndex=a')).rejects.toThrow(
    'identity_or_source_changed'
  );
  reject(500, {});
  await expect(credentialQuotaRequest('http://fixture', 'key', 'GET', '?authIndex=a')).rejects.toThrow(
    'credential_quota_request_failed'
  );
});
