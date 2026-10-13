import type { TFunction } from 'i18next';

/**
 * Known quota failure codes emitted by the provider quota command / plugin.
 * The panel must not surface the raw internal text on its own: an operator
 * cannot tell what to do about `stale_cache` or `not_logged_in`.
 */
const KNOWN_QUOTA_FAILURES: ReadonlyArray<{ code: string; key: string }> = [
  { code: 'stale_cache', key: 'quota_error.stale_cache' },
  { code: 'not_logged_in', key: 'quota_error.not_logged_in' },
  { code: 'bsk_unavailable', key: 'quota_error.browser_unavailable' },
  { code: 'no_browser', key: 'quota_error.browser_unavailable' },
  { code: 'console_error', key: 'quota_error.console_error' },
  { code: 'invalid_arguments', key: 'quota_error.invalid_arguments' },
];

/**
 * Rewrite a known quota failure into an actionable, localized hint while
 * keeping the machine-readable code for diagnostics. Unknown failures are
 * returned unchanged so existing behaviour and tests are preserved.
 */
export const describeQuotaFailure = (message: string, t: TFunction): string => {
  const text = String(message ?? '');
  if (!text) return text;
  const match = KNOWN_QUOTA_FAILURES.find((entry) => text.includes(entry.code));
  if (!match) return text;
  const hint = t(match.key);
  // A missing translation returns the key itself; keep the raw text then.
  if (!hint || hint === match.key) return text;
  return `${hint} [${match.code}]`;
};
