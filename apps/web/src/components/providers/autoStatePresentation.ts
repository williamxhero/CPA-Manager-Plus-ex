import type {
  ProviderAutoStateMemberName,
  ProviderAutoStateReason,
} from '@/services/api/providerAutoState';

type Translate = (key: string) => string;

/**
 * Known Core pause reasons. Anything else falls back to the raw owner string so
 * the panel never invents a cause or hides an unmapped one.
 */
const REASON_LABEL_KEYS: Readonly<Record<string, string>> = {
  upstream_balance_exhausted: 'ai_providers.auto_state_reason_upstream_balance_exhausted',
};

const MEMBER_STATE_LABEL_KEYS: Readonly<Record<ProviderAutoStateMemberName, string>> = {
  paused: 'ai_providers.auto_state_member_paused',
  available: 'ai_providers.auto_state_member_available',
};

const pad = (value: number): string => String(value).padStart(2, '0');

/** Local `YYYY-MM-DD HH:mm` pause time; empty for an unknown instant. */
export function formatAutoPauseTime(observedAtMs: number | undefined): string {
  if (typeof observedAtMs !== 'number' || !Number.isFinite(observedAtMs)) return '';
  const at = new Date(observedAtMs);
  return `${at.getFullYear()}-${pad(at.getMonth() + 1)}-${pad(at.getDate())} ${pad(
    at.getHours()
  )}:${pad(at.getMinutes())}`;
}

export function getAutoStateReasonLabel(
  t: Translate,
  reason: ProviderAutoStateReason | undefined
): string {
  if (!reason) return '';
  const key = REASON_LABEL_KEYS[reason.reason];
  return key ? t(key) : reason.owner || reason.reason;
}

/** `暂停原因：上游余额不足 · 2026-11-23 15:26`, empty when no reason is published. */
export function getAutoStatePauseText(
  t: Translate,
  reasons: ReadonlyArray<ProviderAutoStateReason> | undefined
): string {
  const primary = reasons?.[0];
  if (!primary) return '';
  const parts = [getAutoStateReasonLabel(t, primary), formatAutoPauseTime(primary.observedAtMs)]
    .filter(Boolean)
    .join(' · ');
  if (!parts) return '';
  return `${t('ai_providers.auto_state_pause_reason')}: ${parts}`;
}

export function getAutoStateMemberStateLabel(
  t: Translate,
  state: ProviderAutoStateMemberName
): string {
  return t(MEMBER_STATE_LABEL_KEYS[state]);
}

export function joinAutoStateLabel(headline: string, detail: string): string {
  return detail ? `${headline} · ${detail}` : headline;
}
