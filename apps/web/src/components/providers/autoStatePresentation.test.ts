import { describe, expect, it } from 'vitest';
import {
  formatAutoPauseTime,
  getAutoStateMemberStateLabel,
  getAutoStatePauseText,
  getAutoStateReasonLabel,
  joinAutoStateLabel,
} from './autoStatePresentation';

const t = (key: string) => key;

describe('provider auto state presentation', () => {
  it('formats the pause instant in local time', () => {
    const observedAtMs = 1763970360000;
    // Independent source of truth: the same instant rendered by Intl in local time.
    const expected = new Date(observedAtMs).toLocaleString('sv-SE').slice(0, 16);
    expect(formatAutoPauseTime(observedAtMs)).toBe(expected);
    expect(formatAutoPauseTime(observedAtMs)).toMatch(/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}$/);
    expect(formatAutoPauseTime(undefined)).toBe('');
  });

  it('names the reason and time of a known automatic pause', () => {
    expect(
      getAutoStatePauseText(t, [
        {
          owner: 'core.upstream-balance.v1',
          reason: 'upstream_balance_exhausted',
          observedAtMs: 1763970360000,
        },
      ])
    ).toBe(
      `ai_providers.auto_state_pause_reason: ai_providers.auto_state_reason_upstream_balance_exhausted · ${formatAutoPauseTime(
        1763970360000
      )}`
    );
  });

  it('falls back to the raw owner for an unmapped reason and stays empty without one', () => {
    expect(
      getAutoStateReasonLabel(t, {
        owner: 'core.custom-policy.v1',
        reason: 'custom_pause',
        observedAtMs: 1763970360000,
      })
    ).toBe('core.custom-policy.v1');
    expect(getAutoStatePauseText(t, [])).toBe('');
    expect(getAutoStatePauseText(t, undefined)).toBe('');
    expect(joinAutoStateLabel('headline', '')).toBe('headline');
    expect(joinAutoStateLabel('headline', 'detail')).toBe('headline · detail');
  });

  it('labels each member state', () => {
    expect(getAutoStateMemberStateLabel(t, 'paused')).toBe('ai_providers.auto_state_member_paused');
    expect(getAutoStateMemberStateLabel(t, 'available')).toBe(
      'ai_providers.auto_state_member_available'
    );
  });
});
