import { describe, expect, it } from 'vitest';
import type { AuthFileItem, ClaudeQuotaState } from '@/types';
import { buildQuotaCredentialIdentity } from '@/utils/quota/credentialScope';
import { resolveAccountQuota, type AccountQuotaStores } from './accountQuotaSummary';
import { buildAccountRows } from './accountRows';
import { buildAccountQuotaDisplayWindows } from './accountQuotaDisplayWindows';
import type { TFunction } from 'i18next';

const observedAt = Date.parse('2026-11-18T10:00:00Z');
const file: AuthFileItem = {
  name: 'oc-hxm.json',
  provider: 'opencode-go',
  authIndex: 'hxm',
  quota: {
    observed_at: new Date(observedAt).toISOString(),
    signals: {
      'OpenCode-Go-Window': 'weekly',
      'OpenCode-Go-Remaining': '0',
      'OpenCode-Go-Reset-At': '2026-11-23T02:00:00Z',
    },
  },
};
const cache: ClaudeQuotaState = {
  ...buildQuotaCredentialIdentity(file),
  status: 'success',
  fetchedAtMs: observedAt - 1000,
  windows: ['rolling', 'weekly', 'monthly'].map((id) => ({
    id,
    label: id,
    usedPercent: 10,
    resetLabel: '-',
    resetAtMs: null,
  })),
};
const stores = (quota = cache): AccountQuotaStores => ({
  antigravityQuota: {},
  claudeQuota: {},
  codexQuota: {},
  devinQuota: {},
  kimiQuota: {},
  metaQuota: {},
  xaiQuota: {},
  opencodeGoQuota: { [file.name]: quota },
});
const t = ((key: string) => key) as TFunction;

describe('OpenCode first authoritative quota rejection', () => {
  it('overrides an old positive summary and only the rejected display window', () => {
    expect(resolveAccountQuota(file, stores())).toMatchObject({
      status: 'exhausted',
      remainingPercent: 0,
    });
    const row = buildAccountRows([file], stores())[0];
    const windows = buildAccountQuotaDisplayWindows(row, {
      stores: stores(),
      t,
      translateQuotaWindowLabel: (label) => label ?? '',
      nowMs: observedAt,
    });
    expect(windows.find((w) => w.key === 'weekly')?.remainingPercent).toBe(0);
    expect(windows.find((w) => w.key === 'rolling')?.remainingPercent).toBe(90);
    expect(windows.find((w) => w.key === 'monthly')?.remainingPercent).toBe(90);
  });
  it('does not recover just because reset is in the past or a refresh failed', () => {
    expect(
      resolveAccountQuota(
        file,
        stores({ ...cache, status: 'error', fetchedAtMs: observedAt + 1000 })
      )
    ).toMatchObject({ status: 'exhausted', remainingPercent: 0 });
  });
  it('accepts a newer successful same-credential read, not another credential cache', () => {
    expect(
      resolveAccountQuota(file, stores({ ...cache, fetchedAtMs: observedAt + 1000 }))
    ).toMatchObject({ remainingPercent: 90 });
    expect(
      resolveAccountQuota(
        file,
        stores({ ...cache, authFileKey: 'other', fetchedAtMs: observedAt + 1000 })
      )
    ).toMatchObject({ status: 'exhausted', remainingPercent: 0 });
  });
  it('ignores ordinary rate limits and non-OpenCode providers', () => {
    expect(resolveAccountQuota({ ...file, quota: { signals: {} } }, stores())).toMatchObject({
      remainingPercent: 90,
    });
    expect(resolveAccountQuota({ ...file, provider: 'claude' }, stores())).not.toMatchObject({
      status: 'exhausted',
    });
  });
});
