import type { TFunction } from 'i18next';
import type { AuthFileItem, ClaudeQuotaWindow } from '@/types';
import { authFilesApi } from '@/services/api/authFiles';
import {
  apiClient,
  createScopedApiRequestConfig,
  type ApiClientRequestScope,
} from '@/services/api/client';
import { sha256RawTextHex } from '@/utils/apiKeyHash';
import { isRecord } from '@/utils/helpers';
import { formatQuotaResetTime, resolveAbsoluteQuotaReset } from './formatters';
import type { ClaudeQuotaData } from './providerRequests';
import type { QuotaFetchContext } from './metaQuota';

export const OPENCODE_GO_QUOTA_PATH = '/plugins/cpa-credential-opencode/quota-usage';

export const parseOpenCodeGoQuota = (payload: unknown, t: TFunction): ClaudeQuotaData => {
  const usage = isRecord(payload) && isRecord(payload.usage) ? payload.usage : null;
  if (!usage) throw new Error('OpenCode Go returned invalid quota data');
  const windows: ClaudeQuotaWindow[] = [];
  const definitions = [
    ['rolling', 'five_hour', 5 * 60 * 60],
    ['weekly', 'weekly', 7 * 24 * 60 * 60],
    ['monthly', 'monthly', null],
  ] as const;
  for (const [id, kind, duration] of definitions) {
    const raw = usage[id];
    if (!isRecord(raw) || typeof raw.percent !== 'number' || !Number.isFinite(raw.percent)) {
      throw new Error(`OpenCode Go returned invalid ${id} quota data`);
    }
    const resetValue = raw.resetsAt ?? raw.resets_at;
    const reset = resolveAbsoluteQuotaReset(
      typeof resetValue === 'string' || typeof resetValue === 'number' ? resetValue : null
    );
    const labelKey = `accounts.detail_snapshot_window_${kind}`;
    windows.push({
      id,
      label: t(labelKey),
      labelKey,
      usedPercent: raw.status === 'rate-limited' ? 100 : Math.max(0, Math.min(100, raw.percent)),
      resetLabel: formatQuotaResetTime(
        typeof resetValue === 'string' || typeof resetValue === 'number' ? resetValue : null
      ),
      ...reset,
      limitWindowSeconds: duration,
      modelScope: { kind: 'all', complete: true },
    });
  }
  return { windows, planType: 'OpenCode Go', quotaInventoryObserved: true };
};

export const fetchOpenCodeGoQuota = async (
  file: AuthFileItem,
  t: TFunction,
  requestScope?: ApiClientRequestScope,
  context?: QuotaFetchContext
): Promise<ClaudeQuotaData> => {
  const assertCurrent = () => {
    if (context?.isCurrent && !context.isCurrent()) throw new Error('Quota request superseded');
  };
  assertCurrent();
  const identity = [file.id, file.name?.replace(/\.json$/i, '')].find(
    (value) => typeof value === 'string' && /^opencode-go-key-[a-f0-9]{64}$/.test(value)
  );
  let keyId = typeof identity === 'string' ? identity : '';
  if (!keyId) {
    const auth = await authFilesApi.downloadJsonObject(file.name, requestScope);
    assertCurrent();
    if (typeof auth.api_key !== 'string' || !auth.api_key.trim()) {
      throw new Error('OpenCode Go credential is missing api_key');
    }
    keyId = `opencode-go-key-${sha256RawTextHex(auth.api_key)}`;
  }
  const response = await apiClient.post<unknown>(
    OPENCODE_GO_QUOTA_PATH,
    { key_id: keyId },
    requestScope ? createScopedApiRequestConfig(requestScope) : undefined
  );
  assertCurrent();
  return parseOpenCodeGoQuota(response, t);
};
