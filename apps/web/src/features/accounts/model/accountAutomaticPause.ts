import type { CredentialPolicyStatus } from '@/services/api/credentialPolicy';
import type { AccountDetailField } from './accountDetailViewModel';

/**
 * Read-only automatic start/stop row for the account detail overview.
 *
 * The list can only say "exhausted/cooling", so it cannot distinguish "still
 * participating" from "the panel cannot read the policy at all". This model
 * renders exactly what the backend credential-policy read answered, and says
 * "unavailable" when nothing authoritative came back. It never derives a state
 * from the manual disabled flag, so manual stop and automatic pause stay
 * independent.
 */
export type AccountAutomaticPauseState = 'reading' | 'active' | 'paused' | 'unavailable';

export type AccountAutomaticPauseRead =
  | { kind: 'reading' }
  | { kind: 'ready'; status: CredentialPolicyStatus }
  | { kind: 'failed' }
  | { kind: 'missing' };

export interface AccountAutomaticPauseTiming {
  observedAtMs: number | null;
  nextReviewAtMs: number | null;
}

export interface AccountAutomaticPausePresentation {
  state: AccountAutomaticPauseState;
  stateLabelKey: string;
  reasonCode: string;
  reasonLabelKey: string | null;
  observedAtMs: number | null;
  nextReviewAtMs: number | null;
  fields: AccountDetailField[];
}

/** Pseudo codes the panel owns: a read that never reached the backend. */
export const PANEL_READ_FAILED_REASON = 'panel_read_failed';
export const PANEL_IDENTITY_UNAVAILABLE_REASON = 'panel_identity_unavailable';
export const PANEL_POLICY_IDENTITY_MISSING_REASON = 'panel_policy_identity_missing';

const REASON_LABEL_KEYS: Readonly<Record<string, string>> = {
  current_source_gate_ready: 'accounts.auto_pause_reason_current_source_gate_ready',
  policy_enabled: 'accounts.auto_pause_reason_policy_enabled',
  current_gate_confirmed: 'accounts.auto_pause_reason_current_gate_confirmed',
  current_binding_unverified: 'accounts.auto_pause_reason_current_binding_unverified',
  gate_core_unavailable: 'accounts.auto_pause_reason_gate_core_unavailable',
  gate_contract_unavailable: 'accounts.auto_pause_reason_gate_contract_unavailable',
  gate_connection_unavailable: 'accounts.auto_pause_reason_gate_connection_unavailable',
  gate_auth_error: 'accounts.auto_pause_reason_gate_auth_error',
  source_changed: 'accounts.auto_pause_reason_source_changed',
  quota_auto_stop_unconfirmed: 'accounts.auto_pause_reason_quota_auto_stop_unconfirmed',
  verified_selected_key_evidence_required:
    'accounts.auto_pause_reason_verified_selected_key_evidence_required',
  environment_locked_off: 'accounts.auto_pause_reason_environment_locked_off',
  [PANEL_READ_FAILED_REASON]: 'accounts.auto_pause_reason_panel_read_failed',
  [PANEL_IDENTITY_UNAVAILABLE_REASON]: 'accounts.auto_pause_reason_panel_identity_unavailable',
  [PANEL_POLICY_IDENTITY_MISSING_REASON]:
    'accounts.auto_pause_reason_panel_policy_identity_missing',
};

const STATE_LABEL_KEYS: Readonly<Record<AccountAutomaticPauseState, string>> = {
  reading: 'accounts.detail_overview_auto_pause_state_reading',
  active: 'accounts.detail_overview_auto_pause_state_active',
  paused: 'accounts.detail_overview_auto_pause_state_paused',
  unavailable: 'accounts.detail_overview_auto_pause_state_unavailable',
};

const LABEL_KEYS = {
  state: 'accounts.detail_overview_auto_pause_state',
  reason: 'accounts.detail_overview_auto_pause_reason',
  observed: 'accounts.detail_overview_auto_pause_observed',
  nextReview: 'accounts.detail_overview_auto_pause_next_review',
} as const;

const MISSING_VALUE_KEYS = {
  reason: 'accounts.detail_overview_auto_pause_reason_missing',
  observed: 'accounts.detail_overview_auto_pause_observed_missing',
  nextReview: 'accounts.detail_overview_auto_pause_next_review_missing',
} as const;

export const ACCOUNT_AUTOMATIC_PAUSE_I18N_KEYS: readonly string[] = [
  'accounts.detail_overview_auto_pause_title',
  'accounts.detail_overview_auto_pause_active_hint',
  'accounts.detail_overview_auto_pause_paused_hint',
  'accounts.detail_overview_auto_pause_unavailable_hint',
  'accounts.detail_overview_auto_pause_reading_hint',
  ...Object.values(LABEL_KEYS),
  ...Object.values(STATE_LABEL_KEYS),
  ...Object.values(MISSING_VALUE_KEYS),
  ...Object.values(REASON_LABEL_KEYS),
];

const readTimestamp = (value: unknown): number | null =>
  typeof value === 'number' && Number.isFinite(value) && value > 0 && value <= 8_640_000_000_000_000
    ? value
    : null;

/**
 * The Manager's current credential-policy read issues neither an evaluation
 * timestamp nor a review schedule. Read them defensively so a newer payload is
 * shown exactly as sent, and never derive one from the local clock.
 */
export function readAccountAutomaticPauseTiming(
  status: CredentialPolicyStatus
): AccountAutomaticPauseTiming {
  const payload = status as CredentialPolicyStatus & {
    observedAtMs?: unknown;
    nextReviewAtMs?: unknown;
  };
  return {
    observedAtMs: readTimestamp(payload.observedAtMs),
    nextReviewAtMs: readTimestamp(payload.nextReviewAtMs),
  };
}

const firstNonEmpty = (...values: Array<string | undefined>): string =>
  values.find((value) => typeof value === 'string' && value.trim() !== '')?.trim() ?? '';

const reasonField = (code: string): AccountDetailField => {
  const labelKey = REASON_LABEL_KEYS[code];
  if (labelKey) {
    return {
      key: 'automaticPauseReason',
      labelKey: LABEL_KEYS.reason,
      value: labelKey,
      valueKind: 'i18n',
    };
  }
  if (code) {
    // Unknown backend codes stay verbatim; a blank reason never happens.
    return {
      key: 'automaticPauseReason',
      labelKey: LABEL_KEYS.reason,
      value: code,
      valueKind: 'text',
    };
  }
  return {
    key: 'automaticPauseReason',
    labelKey: LABEL_KEYS.reason,
    value: MISSING_VALUE_KEYS.reason,
    valueKind: 'i18n',
  };
};

const timeField = (
  key: string,
  labelKey: string,
  value: number | null,
  missingKey: string
): AccountDetailField =>
  value === null
    ? { key, labelKey, value: missingKey, valueKind: 'i18n' }
    : { key, labelKey, value, valueKind: 'timestamp' };

const readingPresentation = (): AccountAutomaticPausePresentation => {
  const readingValue = STATE_LABEL_KEYS.reading;
  return {
    state: 'reading',
    stateLabelKey: readingValue,
    reasonCode: '',
    reasonLabelKey: null,
    observedAtMs: null,
    nextReviewAtMs: null,
    fields: [
      { key: 'automaticPauseState', labelKey: LABEL_KEYS.state, value: readingValue, valueKind: 'i18n' },
      { key: 'automaticPauseReason', labelKey: LABEL_KEYS.reason, value: readingValue, valueKind: 'i18n' },
      { key: 'automaticPauseObservedAt', labelKey: LABEL_KEYS.observed, value: readingValue, valueKind: 'i18n' },
      { key: 'automaticPauseNextReview', labelKey: LABEL_KEYS.nextReview, value: readingValue, valueKind: 'i18n' },
    ],
  };
};

const unavailablePresentation = (
  status: CredentialPolicyStatus | null,
  fallbackReason: string,
  preferFallback = false
): AccountAutomaticPausePresentation => {
  const timing = status
    ? readAccountAutomaticPauseTiming(status)
    : { observedAtMs: null, nextReviewAtMs: null };
  const backendReason = firstNonEmpty(
    status?.availability?.reason,
    status?.reason,
    status?.operation?.reason
  );
  const reasonCode = preferFallback
    ? firstNonEmpty(fallbackReason, backendReason)
    : firstNonEmpty(backendReason, fallbackReason);
  return {
    state: 'unavailable',
    stateLabelKey: STATE_LABEL_KEYS.unavailable,
    reasonCode,
    reasonLabelKey: REASON_LABEL_KEYS[reasonCode] ?? null,
    observedAtMs: timing.observedAtMs,
    nextReviewAtMs: timing.nextReviewAtMs,
    fields: [
      {
        key: 'automaticPauseState',
        labelKey: LABEL_KEYS.state,
        value: STATE_LABEL_KEYS.unavailable,
        valueKind: 'i18n',
      },
      reasonField(reasonCode),
      timeField('automaticPauseObservedAt', LABEL_KEYS.observed, timing.observedAtMs, MISSING_VALUE_KEYS.observed),
      timeField('automaticPauseNextReview', LABEL_KEYS.nextReview, timing.nextReviewAtMs, MISSING_VALUE_KEYS.nextReview),
    ],
  };
};

const readyPresentation = (
  state: 'active' | 'paused',
  reasonCode: string,
  timing: AccountAutomaticPauseTiming
): AccountAutomaticPausePresentation => ({
  state,
  stateLabelKey: STATE_LABEL_KEYS[state],
  reasonCode,
  reasonLabelKey: REASON_LABEL_KEYS[reasonCode] ?? null,
  observedAtMs: timing.observedAtMs,
  nextReviewAtMs: timing.nextReviewAtMs,
  fields: [
    {
      key: 'automaticPauseState',
      labelKey: LABEL_KEYS.state,
      value: STATE_LABEL_KEYS[state],
      valueKind: 'i18n',
    },
    reasonField(reasonCode),
    timeField(
      'automaticPauseObservedAt',
      LABEL_KEYS.observed,
      timing.observedAtMs,
      MISSING_VALUE_KEYS.observed
    ),
    timeField(
      'automaticPauseNextReview',
      LABEL_KEYS.nextReview,
      timing.nextReviewAtMs,
      MISSING_VALUE_KEYS.nextReview
    ),
  ],
});

export function buildAccountAutomaticPausePresentation(
  read: AccountAutomaticPauseRead
): AccountAutomaticPausePresentation {
  if (read.kind === 'reading') return readingPresentation();
  if (read.kind === 'failed') return unavailablePresentation(null, PANEL_READ_FAILED_REASON);
  if (read.kind === 'missing') return unavailablePresentation(null, PANEL_IDENTITY_UNAVAILABLE_REASON);

  const { status } = read;
  const applied = status.applied === true || status.operation?.currentApplied === true;
  if (applied) {
    // An applied pause is a real restriction: show it even when the quota
    // capability is unavailable, never as "unreadable unprotection".
    const reasonCode = firstNonEmpty(
      status.operation?.reason,
      status.reason,
      status.availability?.reason
    );
    return readyPresentation('paused', reasonCode, readAccountAutomaticPauseTiming(status));
  }
  if (!status.subjectId) {
    // Without a policy subject the panel cannot attribute the rest of the
    // payload, so the missing identity is the only honest reason to report.
    return unavailablePresentation(status, PANEL_POLICY_IDENTITY_MISSING_REASON, true);
  }
  if (status.availability?.state !== 'available') {
    // An unavailable capability explains itself; a silent one keeps the
    // "no reason was reported" text instead of borrowing another cause.
    return unavailablePresentation(status, '');
  }
  const reasonCode = firstNonEmpty(
    status.reason,
    status.availability?.reason,
    status.operation?.reason
  );
  return readyPresentation('active', reasonCode, readAccountAutomaticPauseTiming(status));
}
