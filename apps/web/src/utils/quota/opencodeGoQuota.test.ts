import { describe, expect, it, vi } from 'vitest';
import type { TFunction } from 'i18next';
import type { ClaudeQuotaState } from '@/types';
import { refreshQuotaWithConfig } from '@/components/quota/quotaRefresh';
import { collapseOpenCodeGoAuthFileDuplicates } from '@/features/authFiles/model/openCodeGoAuthFiles';
import { authFilesApi } from '@/services/api/authFiles';
import { apiClient } from '@/services/api/client';
import { sha256RawTextHex } from '@/utils/apiKeyHash';
import { isQuotaRefreshSupportedProvider } from '@/features/authFiles/constants';
import { OPENCODE_GO_CONFIG } from '@/components/quota/quotaConfigs';
import { buildAccountRows } from '@/features/accounts/model/accountRows';
import { buildAccountQuotaDisplayWindows } from '@/features/accounts/model/accountQuotaDisplayWindows';
import { buildAccountSubscriptionPresentation } from '@/features/accounts/model/accountSubscriptionPresentation';
import { getCredentialScopedQuotaState } from './credentialScope';
import {
  fetchOpenCodeGoQuota,
  OPENCODE_GO_QUOTA_PATH,
  parseOpenCodeGoQuota,
} from './opencodeGoQuota';

const t = ((key: string) => key) as TFunction;
const payload = {
  usage: {
    rolling: { status: 'ok', percent: 12.4, resets_at: '2026-10-05T12:00:00Z' },
    weekly: { status: 'ok', percent: 8.1, resetsAt: '2026-10-11T00:00:00Z' },
    monthly: { status: 'ok', percent: 5.7, resets_at: '2026-11-01T00:00:00Z' },
  },
};

describe('OpenCode Go account quota', () => {
  it('enables refresh and renders all three quota windows through the accounts model', () => {
    const file = { name: 'opencode.json', provider: 'opencode-go', auth_index: 'go-1' };
    expect(isQuotaRefreshSupportedProvider(file.provider)).toBe(true);
    expect(isQuotaRefreshSupportedProvider('opencode_go')).toBe(true);
    const data = parseOpenCodeGoQuota(payload, t);
    const state = OPENCODE_GO_CONFIG.buildSuccessState(data, file);
    const stores = {
      antigravityQuota: {},
      claudeQuota: {},
      codexQuota: {},
      devinQuota: {},
      kimiQuota: {},
      metaQuota: {},
      xaiQuota: {},
      opencodeGoQuota: { [OPENCODE_GO_CONFIG.getStoreKey!(file)]: state },
    };
    const row = buildAccountRows([file], stores)[0];
    const windows = buildAccountQuotaDisplayWindows(row, {
      stores,
      t,
      nowMs: Date.parse('2026-10-05T10:00:00Z'),
      translateQuotaWindowLabel: (label, key) => (key ? t(key) : (label ?? '')),
    });
    expect(windows.map((window) => window.kind)).toEqual(['five_hour', 'weekly', 'monthly']);
    expect(windows.map((window) => window.remainingPercent)).toEqual([87.6, 91.9, 94.3]);
    expect(row.quota.remainingPercent).toBe(87.6);
    expect(windows.every((window) => window.resetAtMs !== null)).toBe(true);
    const subscription = buildAccountSubscriptionPresentation({
      row,
      opencodeGoQuota: getCredentialScopedQuotaState(stores.opencodeGoQuota, row.raw),
      nowMs: Date.parse('2026-10-05T10:00:00Z'),
    });
    expect(subscription.remainingDays).toBe(27);
  });

  it('uses the selected credential and sends only its key identity to the plugin route', async () => {
    const download = vi
      .spyOn(authFilesApi, 'downloadJsonObject')
      .mockResolvedValue({ api_key: 'test-key' });
    const post = vi.spyOn(apiClient, 'post').mockResolvedValue(payload);
    const scope = { apiBase: 'http://localhost:18317', managementKey: 'test-management' };
    try {
      await fetchOpenCodeGoQuota({ name: 'selected.json' }, t, scope);
      expect(download).toHaveBeenCalledWith('selected.json', scope);
      expect(post).toHaveBeenCalledWith(
        OPENCODE_GO_QUOTA_PATH,
        { key_id: `opencode-go-key-${sha256RawTextHex('test-key')}` },
        expect.objectContaining({ cpampScopedRequest: true })
      );
    } finally {
      download.mockRestore();
      post.mockRestore();
    }
  });

  it('refreshes a collapsed credential as one account using its canonical plugin key', async () => {
    const keyId = `opencode-go-key-${'a'.repeat(64)}`;
    const fileName = `${keyId}.json`;
    const files = collapseOpenCodeGoAuthFileDuplicates([
      { name: fileName, id: fileName, provider: 'opencode-go', auth_index: 'go-1' },
      {
        name: fileName,
        id: keyId,
        provider: 'opencode-go',
        auth_index: 'go-1',
        supports_quota: true,
        quota_provider: 'opencode-go',
        recent_requests: [{ success: 3, failed: 0 }],
      },
    ]);
    expect(files).toHaveLength(1);
    const file = files[0];
    const download = vi.spyOn(authFilesApi, 'downloadJsonObject');
    const post = vi.spyOn(apiClient, 'post').mockResolvedValue(payload);
    let opencodeGoQuota: Record<string, ClaudeQuotaState> = {};
    try {
      const result = await refreshQuotaWithConfig({
        config: OPENCODE_GO_CONFIG,
        file,
        t,
        isCurrent: () => true,
        setQuota: (updater) => {
          opencodeGoQuota = typeof updater === 'function' ? updater(opencodeGoQuota) : updater;
        },
      });
      expect(result?.status).toBe('success');
      expect(post).toHaveBeenCalledExactlyOnceWith(
        OPENCODE_GO_QUOTA_PATH,
        { key_id: keyId },
        undefined
      );
      expect(download).not.toHaveBeenCalled();
      expect(Object.keys(opencodeGoQuota)).toEqual([`${fileName}::go-1`]);
      expect(opencodeGoQuota[`${fileName}::go-1`].windows).toHaveLength(3);
      const rows = buildAccountRows(files, {
        antigravityQuota: {},
        claudeQuota: {},
        codexQuota: {},
        devinQuota: {},
        kimiQuota: {},
        metaQuota: {},
        xaiQuota: {},
        opencodeGoQuota,
      });
      expect(rows).toHaveLength(1);
      expect(rows[0]).toMatchObject({
        provider: 'opencode-go',
        runtimeOnly: false,
        quota: { remainingPercent: 87.6, planType: 'OpenCode Go' },
        usage: { success: 3, failure: 0 },
      });
      expect(isQuotaRefreshSupportedProvider(rows[0].provider)).toBe(true);
    } finally {
      download.mockRestore();
      post.mockRestore();
    }
  });

  it('rejects incomplete responses instead of displaying a fabricated available quota', () => {
    expect(() => parseOpenCodeGoQuota({ usage: { rolling: {} } }, t)).toThrow();
    expect(() => parseOpenCodeGoQuota({}, t)).toThrow();
  });

  it('respects a rate limited window and does not fabricate a fixed monthly duration', () => {
    const data = parseOpenCodeGoQuota(
      {
        usage: {
          ...payload.usage,
          rolling: { ...payload.usage.rolling, status: 'rate-limited' },
        },
      },
      t
    );
    expect(data.windows[0].usedPercent).toBe(100);
    expect(data.windows[2].limitWindowSeconds).toBeNull();
  });
});
