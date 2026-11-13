import type { TFunction } from 'i18next';

const KNOWN_REFRESH_ERROR_CODES = [
  'common_refresh_unavailable',
  'common_refresh_not_completed',
  'common_refresh_source_failed',
  'common_refresh_required',
  'current_credential_source_unavailable',
  'identity_or_source_changed',
  'credential_quota_request_failed',
] as const;

export type QuotaRefreshErrorCode = (typeof KNOWN_REFRESH_ERROR_CODES)[number];

export function resolveQuotaRefreshErrorCode(message: string): string {
  const raw = message.trim();
  if (!raw) return 'common_refresh_unavailable';
  const code = raw.split(':')[0]?.trim() || raw;
  return code;
}

/** Localize opaque common-refresh machine codes for toast text. */
export function formatQuotaRefreshErrorMessage(message: string, t: TFunction): string {
  const code = resolveQuotaRefreshErrorCode(message);
  const translated = t(`accounts.quota_refresh_error_${code}`, { defaultValue: '' });
  if (translated) return translated;
  return message.trim() || t('accounts.quota_refresh_error_common_refresh_unavailable');
}
