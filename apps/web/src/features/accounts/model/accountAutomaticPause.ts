import type { CredentialPolicyStatus } from '@/services/api/credentialPolicy';
import type { AccountDetailField } from './accountDetailViewModel';

export type AccountAutomaticPauseState =
  | 'reading'
  | 'active'
  | 'paused'
  | 'no_record'
  | 'unavailable';

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
  hintKey: string;
  reasonCode: string;
  reasonLabelKey: string | null;
  observedAtMs: number | null;
  nextReviewAtMs: number | null;
  fields: AccountDetailField[];
}

export const PANEL_READ_FAILED_REASON = 'panel_read_failed';
export const PANEL_IDENTITY_UNAVAILABLE_REASON = 'panel_identity_unavailable';
export const PANEL_POLICY_IDENTITY_MISSING_REASON = 'panel_policy_identity_missing';
export const NO_POLICY_RECORD_REASON = 'no_policy_record';

/** Backend read classification for a credential the manager holds no record for. */
const NO_POLICY_RECORD_MIGRATION_STATE = 'default_off';

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
  [NO_POLICY_RECORD_REASON]: 'accounts.auto_pause_reason_no_policy_record',
  [PANEL_READ_FAILED_REASON]: 'accounts.auto_pause_reason_panel_read_failed',
  [PANEL_IDENTITY_UNAVAILABLE_REASON]: 'accounts.auto_pause_reason_panel_identity_unavailable',
  [PANEL_POLICY_IDENTITY_MISSING_REASON]:
    'accounts.auto_pause_reason_panel_policy_identity_missing',
};

const STATE_LABEL_KEYS: Readonly<Record<AccountAutomaticPauseState, string>> = {
  reading: 'accounts.detail_overview_auto_pause_state_reading',
  active: 'accounts.detail_overview_auto_pause_state_active',
  paused: 'accounts.detail_overview_auto_pause_state_paused',
  no_record: 'accounts.detail_overview_auto_pause_state_no_record',
  unavailable: 'accounts.detail_overview_auto_pause_state_unavailable',
};

const HINT_KEYS: Readonly<Record<AccountAutomaticPauseState, string>> = {
  reading: 'accounts.detail_overview_auto_pause_reading_hint',
  active: 'accounts.detail_overview_auto_pause_active_hint',
  paused: 'accounts.detail_overview_auto_pause_paused_hint',
  no_record: 'accounts.detail_overview_auto_pause_no_record_hint',
  unavailable: 'accounts.detail_overview_auto_pause_unavailable_hint',
};

export const ACCOUNT_AUTOMATIC_PAUSE_TITLE_KEY = 'accounts.detail_overview_auto_pause_title';

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
  ACCOUNT_AUTOMATIC_PAUSE_TITLE_KEY,
  ...Object.values(HINT_KEYS),
  ...Object.values(LABEL_KEYS),
  ...Object.values(STATE_LABEL_KEYS),
  ...Object.values(MISSING_VALUE_KEYS),
  ...Object.values(REASON_LABEL_KEYS),
];

const readTimestamp = (value: unknown): number | null =>
  typeof value === 'number' && Number.isFinite(value) && value > 0 && value <= 8_640_000_000_000_000
    ? value
    : null;

export function readAccountAutomaticPauseTiming(
  status: CredentialPolicyStatus
): AccountAutomaticPauseTiming {
  return {
    observedAtMs: readTimestamp(status.observedAtMs),
    nextReviewAtMs: readTimestamp(status.nextReviewAtMs),
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
    hintKey: HINT_KEYS.reading,
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

const buildPresentation = (
  state: AccountAutomaticPauseState,
  reasonCode: string,
  timing: AccountAutomaticPauseTiming
): AccountAutomaticPausePresentation => ({
  state,
  stateLabelKey: STATE_LABEL_KEYS[state],
  hintKey: HINT_KEYS[state],
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

const unavailablePresentation = (
  status: CredentialPolicyStatus | null,
  fallbackReason: string,
  preferFallback = false
): AccountAutomaticPausePresentation => {
  const backendReason = firstNonEmpty(
    status?.availability?.reason,
    status?.reason,
    status?.operation?.reason
  );
  const reasonCode = preferFallback
    ? firstNonEmpty(fallbackReason, backendReason)
    : firstNonEmpty(backendReason, fallbackReason);
  return buildPresentation(
    'unavailable',
    reasonCode,
    status ? readAccountAutomaticPauseTiming(status) : { observedAtMs: null, nextReviewAtMs: null }
  );
};

export function buildAccountAutomaticPausePresentation(
  read: AccountAutomaticPauseRead
): AccountAutomaticPausePresentation {
  if (read.kind === 'reading') return readingPresentation();
  if (read.kind === 'failed') return unavailablePresentation(null, PANEL_READ_FAILED_REASON);
  if (read.kind === 'missing') return unavailablePresentation(null, PANEL_IDENTITY_UNAVAILABLE_REASON);

  const { status } = read;
  const operation = status.operation;
  // A confirmed clear is a current ACK as well: its action says the restriction
  // was lifted, so it must never be rendered as an applied pause. A pause is
  // claimed only when the backend reports a current restriction that is not a
  // clear, and its reason travels with it.
  const restrictedNow =
    (status.applied === true || operation?.currentApplied === true) &&
    operation?.action !== 'clear';
  if (restrictedNow) {
    const reasonCode = firstNonEmpty(operation?.reason, status.reason, status.availability?.reason);
    return buildPresentation('paused', reasonCode, readAccountAutomaticPauseTiming(status));
  }
  if (!status.subjectId) {
    return unavailablePresentation(status, PANEL_POLICY_IDENTITY_MISSING_REASON, true);
  }
  // Most credentials carry no policy record at all: automatic pause is always on
  // and Core pauses them on live evidence. That is a real state, not a failed
  // read, so it is named instead of being reported as unreadable.
  if (status.legacyMigration?.state === NO_POLICY_RECORD_MIGRATION_STATE) {
    const reasonCode =
      firstNonEmpty(
        status.availability?.state === 'available' ? '' : status.availability?.reason,
        status.reason
      ) || NO_POLICY_RECORD_REASON;
    return buildPresentation('no_record', reasonCode, readAccountAutomaticPauseTiming(status));
  }
  if (status.availability?.state !== 'available') {
    return unavailablePresentation(status, '');
  }
  const reasonCode = firstNonEmpty(
    status.reason,
    status.availability?.reason,
    status.operation?.reason
  );
  return buildPresentation('active', reasonCode, readAccountAutomaticPauseTiming(status));
}
