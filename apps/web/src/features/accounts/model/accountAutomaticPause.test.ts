import { describe, expect, it } from 'vitest';
import type { CredentialPolicyStatus } from '@/services/api/credentialPolicy';
import {
  ACCOUNT_AUTOMATIC_PAUSE_I18N_KEYS,
  buildAccountAutomaticPausePresentation,
  readAccountAutomaticPauseTiming,
} from './accountAutomaticPause';

const makeStatus = (overrides: Partial<CredentialPolicyStatus> = {}): CredentialPolicyStatus => ({
  subjectId: 'subject-1',
  bindingCurrent: true,
  enabled: true,
  generation: 3,
  owner: 'manager',
  state: 'armed',
  reason: 'policy_enabled',
  availability: { state: 'available', reason: 'current_source_gate_ready' },
  queryAvailability: { state: 'available', reason: 'verified_selected_key_evidence' },
  applied: false,
  ...overrides,
}) as CredentialPolicyStatus;

const fieldByKey = (
  presentation: ReturnType<typeof buildAccountAutomaticPausePresentation>,
  key: string
) => {
  const field = presentation.fields.find((candidate) => candidate.key === key);
  if (!field) throw new Error(`field not found: ${key}`);
  return field;
};

describe('buildAccountAutomaticPausePresentation', () => {
  it('exposes one read-only row per asked fact', () => {
    const presentation = buildAccountAutomaticPausePresentation({ kind: 'reading' });
    expect(presentation.fields.map((field) => field.labelKey)).toEqual([
      'accounts.detail_overview_auto_pause_state',
      'accounts.detail_overview_auto_pause_reason',
      'accounts.detail_overview_auto_pause_observed',
      'accounts.detail_overview_auto_pause_next_review',
    ]);
  });

  it('shows a reading state instead of an empty row before the first answer', () => {
    const presentation = buildAccountAutomaticPausePresentation({ kind: 'reading' });
    expect(presentation.state).toBe('reading');
    expect(presentation.stateLabelKey).toBe('accounts.detail_overview_auto_pause_state_reading');
    for (const field of presentation.fields) {
      expect(field.value).toBe('accounts.detail_overview_auto_pause_state_reading');
      expect(field.valueKind).toBe('i18n');
    }
  });

  it('reads an available, unpaused policy as participating', () => {
    const presentation = buildAccountAutomaticPausePresentation({
      kind: 'ready',
      status: makeStatus(),
    });
    expect(presentation.state).toBe('active');
    expect(presentation.stateLabelKey).toBe('accounts.detail_overview_auto_pause_state_active');
    expect(fieldByKey(presentation, 'automaticPauseReason')).toMatchObject({
      value: 'accounts.auto_pause_reason_policy_enabled',
      valueKind: 'i18n',
    });
    expect(fieldByKey(presentation, 'automaticPauseObservedAt').value).toBe(
      'accounts.detail_overview_auto_pause_observed_missing'
    );
    expect(fieldByKey(presentation, 'automaticPauseNextReview').value).toBe(
      'accounts.detail_overview_auto_pause_next_review_missing'
    );
  });

  it('reads an applied pause as auto-paused with the backend reason', () => {
    const presentation = buildAccountAutomaticPausePresentation({
      kind: 'ready',
      status: makeStatus({
        reason: '',
        applied: true,
        operation: { reason: 'current_gate_confirmed', action: 'pause' } as never,
      }),
    });
    expect(presentation.state).toBe('paused');
    expect(presentation.stateLabelKey).toBe('accounts.detail_overview_auto_pause_state_paused');
    expect(fieldByKey(presentation, 'automaticPauseReason')).toMatchObject({
      value: 'accounts.auto_pause_reason_current_gate_confirmed',
      valueKind: 'i18n',
    });
  });

  it('keeps an applied pause visible when quota capability is unavailable', () => {
    const presentation = buildAccountAutomaticPausePresentation({
      kind: 'ready',
      status: makeStatus({
        applied: true,
        availability: { state: 'unavailable', reason: 'gate_core_unavailable' },
      }),
    });
    expect(presentation.state).toBe('paused');
  });

  it('reads an unavailable capability as unavailable instead of protected or stopped', () => {
    const presentation = buildAccountAutomaticPausePresentation({
      kind: 'ready',
      status: makeStatus({
        availability: { state: 'unavailable', reason: 'gate_core_unavailable' },
      }),
    });
    expect(presentation.state).toBe('unavailable');
    expect(presentation.stateLabelKey).toBe('accounts.detail_overview_auto_pause_state_unavailable');
    expect(fieldByKey(presentation, 'automaticPauseReason')).toMatchObject({
      value: 'accounts.auto_pause_reason_gate_core_unavailable',
      valueKind: 'i18n',
    });
  });

  it('reads a failed request as unavailable', () => {
    const presentation = buildAccountAutomaticPausePresentation({ kind: 'failed' });
    expect(presentation.state).toBe('unavailable');
    expect(fieldByKey(presentation, 'automaticPauseReason')).toMatchObject({
      value: 'accounts.auto_pause_reason_panel_read_failed',
      valueKind: 'i18n',
    });
  });

  it('reads a missing subject identity as unavailable without inventing a state', () => {
    const presentation = buildAccountAutomaticPausePresentation({ kind: 'missing' });
    expect(presentation.state).toBe('unavailable');
    expect(fieldByKey(presentation, 'automaticPauseReason')).toMatchObject({
      value: 'accounts.auto_pause_reason_panel_identity_unavailable',
      valueKind: 'i18n',
    });
  });

  it('reads a status without a policy subject as unavailable', () => {
    const presentation = buildAccountAutomaticPausePresentation({
      kind: 'ready',
      status: makeStatus({ subjectId: '' }),
    });
    expect(presentation.state).toBe('unavailable');
    expect(fieldByKey(presentation, 'automaticPauseReason')).toMatchObject({
      value: 'accounts.auto_pause_reason_panel_policy_identity_missing',
      valueKind: 'i18n',
    });
  });

  it('falls back to the raw reason when the code is unknown', () => {
    const presentation = buildAccountAutomaticPausePresentation({
      kind: 'ready',
      status: makeStatus({ availability: { state: 'unavailable', reason: 'gate_future_reason' } }),
    });
    expect(fieldByKey(presentation, 'automaticPauseReason')).toMatchObject({
      value: 'gate_future_reason',
      valueKind: 'text',
    });
  });

  it('never leaves the reason empty', () => {
    const presentation = buildAccountAutomaticPausePresentation({
      kind: 'ready',
      status: makeStatus({ reason: '', availability: { state: 'unavailable', reason: '' } }),
    });
    expect(fieldByKey(presentation, 'automaticPauseReason')).toMatchObject({
      value: 'accounts.detail_overview_auto_pause_reason_missing',
      valueKind: 'i18n',
    });
  });
});

describe('readAccountAutomaticPauseTiming', () => {
  it('reads a real evaluation time and review schedule when the payload carries them', () => {
    const status = {
      ...makeStatus(),
      observedAtMs: 1_700_000_000_000,
      nextReviewAtMs: 1_700_000_600_000,
    } as CredentialPolicyStatus;
    expect(readAccountAutomaticPauseTiming(status)).toEqual({
      observedAtMs: 1_700_000_000_000,
      nextReviewAtMs: 1_700_000_600_000,
    });
    const presentation = buildAccountAutomaticPausePresentation({ kind: 'ready', status });
    expect(fieldByKey(presentation, 'automaticPauseObservedAt')).toMatchObject({
      value: 1_700_000_000_000,
      valueKind: 'timestamp',
    });
    expect(fieldByKey(presentation, 'automaticPauseNextReview')).toMatchObject({
      value: 1_700_000_600_000,
      valueKind: 'timestamp',
    });
  });

  it('reports unjudged and unscheduled instead of a fabricated time', () => {
    expect(readAccountAutomaticPauseTiming(makeStatus())).toEqual({
      observedAtMs: null,
      nextReviewAtMs: null,
    });
    const malformed = {
      ...makeStatus(),
      observedAtMs: 'soon',
      nextReviewAtMs: Number.NaN,
    } as unknown as CredentialPolicyStatus;
    expect(readAccountAutomaticPauseTiming(malformed)).toEqual({
      observedAtMs: null,
      nextReviewAtMs: null,
    });
  });
});

describe('account automatic pause i18n keys', () => {
  it('names every key the row renders', () => {
    const presentations = [
      buildAccountAutomaticPausePresentation({ kind: 'reading' }),
      buildAccountAutomaticPausePresentation({ kind: 'ready', status: makeStatus() }),
      buildAccountAutomaticPausePresentation({
        kind: 'ready',
        status: makeStatus({
          applied: true,
          availability: { state: 'unavailable', reason: 'gate_core_unavailable' },
        }),
      }),
      buildAccountAutomaticPausePresentation({ kind: 'failed' }),
      buildAccountAutomaticPausePresentation({ kind: 'missing' }),
      buildAccountAutomaticPausePresentation({
        kind: 'ready',
        status: makeStatus({ availability: { state: 'unavailable', reason: 'gate_future_reason' } }),
      }),
    ];
    const rendered = new Set<string>([
      'accounts.detail_overview_auto_pause_title',
      'accounts.detail_overview_auto_pause_active_hint',
      'accounts.detail_overview_auto_pause_paused_hint',
      'accounts.detail_overview_auto_pause_unavailable_hint',
      'accounts.detail_overview_auto_pause_reading_hint',
    ]);
    for (const presentation of presentations) {
      rendered.add(presentation.stateLabelKey);
      for (const field of presentation.fields) {
        rendered.add(field.labelKey);
        if (field.valueKind === 'i18n') rendered.add(String(field.value));
      }
    }
    for (const key of rendered) expect(ACCOUNT_AUTOMATIC_PAUSE_I18N_KEYS).toContain(key);
    expect(new Set(ACCOUNT_AUTOMATIC_PAUSE_I18N_KEYS).size).toBe(
      ACCOUNT_AUTOMATIC_PAUSE_I18N_KEYS.length
    );
  });
});
