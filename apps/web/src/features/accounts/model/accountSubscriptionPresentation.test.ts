import { describe, expect, it } from 'vitest';
import type { AccountRow } from './accountRows';
import type { QwenQuotaState } from '@/utils/quota/qwenQuota';
import type { ClaudeQuotaState, CodexQuotaState } from '@/types';
import {
  buildAccountSubscriptionPresentation,
  parseValidSubscriptionUntilMs,
  resolveCodexSubscriptionUntilMs,
} from './accountSubscriptionPresentation';

const makeAccountRow = (overrides: Partial<AccountRow> = {}): AccountRow =>
  ({
    key: 'test:row:1',
    selectionKey: 'test:row:1',
    fileName: 'codex-test.json',
    provider: 'codex',
    accountLabel: 'test@example.com',
    planType: 'plus',
    priority: 0,
    disabled: false,
    runtimeOnly: false,
    authIndex: '1',
    updatedAtMs: 1000,
    statusMessage: '',
    raw: {
      name: 'codex-test.json',
      type: 'codex',
    },
    usage: {
      success: 0,
      failure: 0,
      successRate: 100,
      recentRequests: [],
    },
    quota: {
      status: 'ok',
      remainingPercent: 100,
      usedPercent: 0,
      resetLabel: '-',
      resetAtMs: null,
      resetAccuracy: 'unknown',
      planType: 'plus',
      source: 'none',
    },
    ...overrides,
  }) as AccountRow;

const makeCodexQuota = (overrides: Record<string, unknown> = {}): CodexQuotaState =>
  ({
    status: 'success',
    windows: [],
    ...overrides,
  }) as unknown as CodexQuotaState;

describe('accountSubscriptionPresentation', () => {
  const FIXED_NOW_MS = 1_700_000_000_000; // e.g. base now

  describe('parseValidSubscriptionUntilMs', () => {
    it('supports seconds epoch (< 1e12)', () => {
      const seconds = 1_700_000_000;
      expect(parseValidSubscriptionUntilMs(seconds)).toBe(1_700_000_000_000);
      expect(parseValidSubscriptionUntilMs(`${seconds}`)).toBe(1_700_000_000_000);
    });

    it('supports milliseconds epoch (>= 1e12)', () => {
      const ms = 1_700_000_000_000;
      expect(parseValidSubscriptionUntilMs(ms)).toBe(ms);
      expect(parseValidSubscriptionUntilMs(`${ms}`)).toBe(ms);
    });

    it('supports ISO timestamp', () => {
      const iso = '2026-10-31T23:59:59Z';
      const expected = Date.parse(iso);
      expect(parseValidSubscriptionUntilMs(iso)).toBe(expected);
    });

    it('returns null for invalid/expired/negative inputs', () => {
      expect(parseValidSubscriptionUntilMs(null)).toBeNull();
      expect(parseValidSubscriptionUntilMs(undefined)).toBeNull();
      expect(parseValidSubscriptionUntilMs('')).toBeNull();
      expect(parseValidSubscriptionUntilMs('not-a-date')).toBeNull();
      expect(parseValidSubscriptionUntilMs(0)).toBeNull();
      expect(parseValidSubscriptionUntilMs(-100)).toBeNull();
    });
  });

  describe('resolveCodexSubscriptionUntilMs', () => {
    it('uses live codexQuota.subscriptionActiveUntil', () => {
      const row = makeAccountRow();
      const quota = makeCodexQuota({
        planType: 'plus',
        subscriptionActiveUntil: 1_700_100_000,
      });
      const result = resolveCodexSubscriptionUntilMs(row, quota);
      expect(result.liveSubscriptionUntilMs).toBe(1_700_100_000_000);
      expect(result.subscriptionUntilMs).toBe(1_700_100_000_000);
    });

    it('prioritizes live quota over token claim', () => {
      const row = makeAccountRow({
        raw: {
          name: 'codex-test.json',
          type: 'codex',
          metadata: {
            id_token: `header.${btoa(JSON.stringify({ chatgpt_subscription_active_until: 1_600_000_000 }))}.sig`,
          },
        },
      });
      const quota = makeCodexQuota({
        planType: 'plus',
        subscriptionActiveUntil: 1_800_000_000,
      });
      const result = resolveCodexSubscriptionUntilMs(row, quota);
      expect(result.liveSubscriptionUntilMs).toBe(1_800_000_000_000);
      expect(result.tokenSubscriptionUntilMs).toBe(1_600_000_000_000);
      expect(result.subscriptionUntilMs).toBe(1_800_000_000_000);
    });

    it('falls back to token claim when live quota is missing', () => {
      const row = makeAccountRow({
        raw: {
          name: 'codex-test.json',
          type: 'codex',
          id_token: `header.${btoa(JSON.stringify({ chatgpt_subscription_active_until: 1_750_000_000 }))}.sig`,
        },
      });
      const result = resolveCodexSubscriptionUntilMs(row, null);
      expect(result.liveSubscriptionUntilMs).toBeNull();
      expect(result.tokenSubscriptionUntilMs).toBe(1_750_000_000_000);
      expect(result.subscriptionUntilMs).toBe(1_750_000_000_000);
    });

    it('returns all nulls for non-Codex provider', () => {
      const row = makeAccountRow({ provider: 'claude' });
      const quota = makeCodexQuota({
        subscriptionActiveUntil: 1_800_000_000,
      });
      const result = resolveCodexSubscriptionUntilMs(row, quota);
      expect(result.subscriptionUntilMs).toBeNull();
    });
  });

  describe('buildAccountSubscriptionPresentation', () => {
    it('calculates remainingDays correctly for paid Codex with future expiry', () => {
      const futureMs = FIXED_NOW_MS + 23 * 86_400_000;
      const row = makeAccountRow();
      const quota = makeCodexQuota({
        planType: 'plus',
        subscriptionActiveUntil: futureMs,
      });
      const result = buildAccountSubscriptionPresentation({
        row,
        codexQuota: quota,
        nowMs: FIXED_NOW_MS,
      });

      expect(result.isPaidCodex).toBe(true);
      expect(result.subscriptionUntilMs).toBe(futureMs);
      expect(result.remainingDays).toBe(23);
      expect(result.planPresentation?.shortLabel).toBe('Plus');
    });

    it('does not display remainingDays when expired', () => {
      const pastMs = FIXED_NOW_MS - 1000;
      const row = makeAccountRow();
      const quota = makeCodexQuota({
        planType: 'plus',
        subscriptionActiveUntil: pastMs,
      });
      const result = buildAccountSubscriptionPresentation({
        row,
        codexQuota: quota,
        nowMs: FIXED_NOW_MS,
      });

      expect(result.isPaidCodex).toBe(true);
      expect(result.subscriptionUntilMs).toBe(pastMs);
      expect(result.remainingDays).toBeNull();
    });

    it('does not display remainingDays for Free plan', () => {
      const futureMs = FIXED_NOW_MS + 30 * 86_400_000;
      const row = makeAccountRow({ planType: 'free' });
      const quota = makeCodexQuota({
        planType: 'free',
        subscriptionActiveUntil: futureMs,
      });
      const result = buildAccountSubscriptionPresentation({
        row,
        codexQuota: quota,
        nowMs: FIXED_NOW_MS,
      });

      expect(result.isPaidCodex).toBe(false);
      expect(result.subscriptionUntilMs).toBeNull();
      expect(result.remainingDays).toBeNull();
    });

    it('does not display remainingDays for invalid expiry', () => {
      const row = makeAccountRow();
      const quota = makeCodexQuota({
        planType: 'team',
        subscriptionActiveUntil: 'invalid-date',
      });
      const result = buildAccountSubscriptionPresentation({
        row,
        codexQuota: quota,
        nowMs: FIXED_NOW_MS,
      });

      expect(result.isPaidCodex).toBe(true);
      expect(result.subscriptionUntilMs).toBeNull();
      expect(result.remainingDays).toBeNull();
    });

    it.each([
      [23 * 86_400_000, 23],
      [23 * 86_400_000 + 1, 24],
      [1000, 1],
      [0, null],
      [-1000, null],
    ])('uses the same Codex rounding at expiry offset %s', (offsetMs, remainingDays) => {
      const result = buildAccountSubscriptionPresentation({
        row: makeAccountRow(),
        codexQuota: makeCodexQuota({ subscriptionActiveUntil: FIXED_NOW_MS + offsetMs }),
        nowMs: FIXED_NOW_MS,
      });

      expect(result.remainingDays).toBe(remainingDays);
    });

    it('preserves paid Codex token fallback without live quota', () => {
      const futureMs = FIXED_NOW_MS + 20 * 86_400_000;
      const row = makeAccountRow({
        raw: {
          name: 'codex-test.json',
          type: 'codex',
          id_token: `header.${btoa(JSON.stringify({ chatgpt_subscription_active_until: futureMs / 1000 }))}.sig`,
        },
      });
      const result = buildAccountSubscriptionPresentation({ row, nowMs: FIXED_NOW_MS });

      expect(result.liveSubscriptionUntilMs).toBeNull();
      expect(result.tokenSubscriptionUntilMs).toBe(futureMs);
      expect(result.subscriptionUntilLabelKey).toBe('accounts.detail_subscription_until_token');
      expect(result.remainingDays).toBe(20);
    });

    describe('Qwen plan remaining days', () => {
      const row = makeAccountRow({ provider: 'qwen', planType: null });
      const makeQuota = (overrides: Partial<QwenQuotaState> = {}): QwenQuotaState => ({
        status: 'success', plan: 'Qwen Pro', planStatus: 'active', planStartMs: null,
        planEndMs: null, daysLeft: null, observedAtMs: FIXED_NOW_MS, metrics: [],
        windows: [{ id: 'monthly', label: 'Monthly', usedPercent: 20, remainingPercent: 80, resetAtMs: FIXED_NOW_MS + 30 * 86_400_000, resetsInDays: 30 }],
        ...overrides,
      });

      it('uses the actual plan end rather than monthly quota reset when daysLeft is absent', () => {
        const result = buildAccountSubscriptionPresentation({
          row, qwenQuota: makeQuota({ planEndMs: FIXED_NOW_MS + 12 * 86_400_000 }), nowMs: FIXED_NOW_MS,
        });
        expect(result.effectivePlanType).toBe('Qwen Pro');
        expect(result.planPresentation?.shortLabel).toBe('Qwen Pro');
        expect(result.remainingDays).toBe(12);
        expect(result.subscriptionUntilMs).toBe(FIXED_NOW_MS + 12 * 86_400_000);
        expect(result.isPaidCodex).toBe(false);
      });

      it.each([0, 12])('uses observed daysLeft %s without inventing an expiry date', (daysLeft) => {
        const result = buildAccountSubscriptionPresentation({ row, qwenQuota: makeQuota({ daysLeft }), nowMs: FIXED_NOW_MS });
        expect(result.remainingDays).toBe(daysLeft);
        expect(result.subscriptionUntilMs).toBeNull();
      });

      it.each([null, -1, NaN, Infinity])('keeps invalid or missing daysLeft %s unknown', (daysLeft) => {
        expect(buildAccountSubscriptionPresentation({ row, qwenQuota: makeQuota({ daysLeft }), nowMs: FIXED_NOW_MS }).remainingDays).toBeNull();
      });

      it('prefers explicit observed daysLeft over a computed plan-end countdown', () => {
        expect(buildAccountSubscriptionPresentation({ row, qwenQuota: makeQuota({ planEndMs: FIXED_NOW_MS + 12 * 86_400_000, daysLeft: 11 }), nowMs: FIXED_NOW_MS }).remainingDays).toBe(11);
      });

      it('does not show failed Qwen snapshot days or apply Qwen plans to other providers', () => {
        expect(buildAccountSubscriptionPresentation({ row: { ...row, planType: 'stale plan' }, qwenQuota: makeQuota({ status: 'error', daysLeft: 12 }), nowMs: FIXED_NOW_MS })).toMatchObject({ remainingDays: null, effectivePlanType: null, planPresentation: null });
        const result = buildAccountSubscriptionPresentation({ row: makeAccountRow({ provider: 'claude', planType: 'pro' }), qwenQuota: makeQuota({ daysLeft: 12 }), nowMs: FIXED_NOW_MS });
        expect(result.remainingDays).toBeNull();
        expect(result.effectivePlanType).toBe('pro');
      });
    });

    describe('OpenCode Go monthly remaining days', () => {
      const row = makeAccountRow({ provider: 'opencode-go', planType: 'OpenCode Go' });
      const makeQuota = (resetAtMs?: number | null): ClaudeQuotaState => ({
        status: 'success',
        planType: 'OpenCode Go',
        windows: [
          {
            id: 'rolling',
            label: '5h',
            usedPercent: 10,
            resetLabel: '-',
            resetAtMs: FIXED_NOW_MS + 1000,
          },
          {
            id: 'weekly',
            label: 'Weekly',
            usedPercent: 10,
            resetLabel: '-',
            resetAtMs: FIXED_NOW_MS + 7 * 86_400_000,
          },
          { id: 'monthly', label: 'Monthly', usedPercent: 10, resetLabel: '-', resetAtMs },
        ],
      });

      it.each([
        [23 * 86_400_000, 23],
        [23 * 86_400_000 + 1, 24],
        [1000, 1],
        [0, null],
        [-1000, null],
      ])('uses only the monthly reset at offset %s', (offsetMs, remainingDays) => {
        const result = buildAccountSubscriptionPresentation({
          row,
          opencodeGoQuota: makeQuota(FIXED_NOW_MS + offsetMs),
          nowMs: FIXED_NOW_MS,
        });

        expect(result.isPaidCodex).toBe(false);
        expect(result.remainingDays).toBe(remainingDays);
      });

      it.each([undefined, null])('returns null without quota (%s)', (opencodeGoQuota) => {
        expect(
          buildAccountSubscriptionPresentation({ row, opencodeGoQuota, nowMs: FIXED_NOW_MS }).remainingDays
        ).toBeNull();
      });

      it('does not fall back to rolling or weekly windows when monthly is absent', () => {
        const quota = makeQuota(FIXED_NOW_MS + 23 * 86_400_000);
        quota.windows = quota.windows.filter((window) => window.id !== 'monthly');
        expect(
          buildAccountSubscriptionPresentation({ row, opencodeGoQuota: quota, nowMs: FIXED_NOW_MS })
            .remainingDays
        ).toBeNull();
      });

      it.each([undefined, null, NaN, Infinity, 0, -1])(
        'returns null for an invalid monthly reset (%s)',
        (resetAtMs) => {
          expect(
            buildAccountSubscriptionPresentation({
              row,
              opencodeGoQuota: makeQuota(resetAtMs),
              nowMs: FIXED_NOW_MS,
            }).remainingDays
          ).toBeNull();
        }
      );

      it('does not apply OpenCode Go quota to another provider', () => {
        expect(
          buildAccountSubscriptionPresentation({
            row: makeAccountRow({ provider: 'claude', planType: 'pro' }),
            opencodeGoQuota: makeQuota(FIXED_NOW_MS + 23 * 86_400_000),
            nowMs: FIXED_NOW_MS,
          }).remainingDays
        ).toBeNull();
      });
    });

    it('does not display remainingDays for non-Codex provider', () => {
      const row = makeAccountRow({ provider: 'claude', planType: 'pro' });
      const result = buildAccountSubscriptionPresentation({
        row,
        nowMs: FIXED_NOW_MS,
      });

      expect(result.isPaidCodex).toBe(false);
      expect(result.remainingDays).toBeNull();
    });
  });
});
