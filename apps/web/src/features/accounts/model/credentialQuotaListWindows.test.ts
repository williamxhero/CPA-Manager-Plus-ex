import { describe, expect, it } from 'vitest';
import {
  credentialQuotaListWindows,
  credentialQuotaResetCredits,
  mergeCredentialQuotaListWindows,
  mergeHostCodexResetCredits,
  quotaDisplayWindowsLookDepleted,
  selectCredentialQuotaWindowsObservation,
} from './credentialQuotaListWindows';
import type { CodexQuotaState } from '@/types';

describe('credentialQuotaResetCredits', () => {
  it('reads a host count without inventing credit details', () => {
    const evidence = credentialQuotaResetCredits(
      JSON.stringify({
        windows: [],
        resetCredits: { availableCount: 1, applicableAvailableCount: 0 },
      }),
      5_000
    );
    expect(evidence).toEqual({
      availableCount: 1,
      applicableAvailableCount: 0,
      observedAtMs: 5_000,
    });
  });

  it('rejects malformed, fractional and negative counts', () => {
    for (const resetCredits of [
      { availableCount: '1' },
      { availableCount: 1.5 },
      { availableCount: -1 },
      {},
      undefined,
    ]) {
      expect(
        credentialQuotaResetCredits(JSON.stringify({ windows: [], resetCredits }), 5_000)
      ).toBeNull();
    }
    expect(credentialQuotaResetCredits(undefined, 5_000)).toBeNull();
    expect(credentialQuotaResetCredits('not-json', 5_000)).toBeNull();
  });

  it('keeps the granted count when applicable evidence cannot be reconciled', () => {
    const evidence = credentialQuotaResetCredits(
      JSON.stringify({ resetCredits: { availableCount: 1, applicableAvailableCount: 2 } })
    );
    expect(evidence).toEqual({
      availableCount: 1,
      applicableAvailableCount: null,
      observedAtMs: null,
    });
  });
});

describe('mergeHostCodexResetCredits', () => {
  const quota = (overrides: Partial<CodexQuotaState> = {}): CodexQuotaState => ({
    status: 'success',
    windows: [],
    ...overrides,
  });

  it('publishes the host count while preserving existing credit details', () => {
    const detail = {
      id: 'credit-a',
      status: 'available' as const,
      grantedAt: '2026-11-12T21:31:10Z',
      expiresAt: '2026-12-12T21:31:10Z',
    };
    const merged = mergeHostCodexResetCredits(
      quota({ rateLimitResetCredits: [detail], rateLimitResetCreditsAvailableCount: 1 }),
      { availableCount: 2, applicableAvailableCount: 0, observedAtMs: 5_000 }
    );
    expect(merged?.rateLimitResetCreditsAvailableCount).toBe(2);
    expect(merged?.rateLimitResetCredits).toEqual([detail]);
    expect(merged?.resetCreditsCountEvidenceAtMs).toBe(5_000);
    expect(merged?.resetCreditsCountSource).toBe('summary');
  });

  it('clears the count when the host reports the allowance as consumed', () => {
    const merged = mergeHostCodexResetCredits(
      quota({ rateLimitResetCreditsAvailableCount: 1, resetCreditsCountEvidenceAtMs: 1_000 }),
      { availableCount: 0, applicableAvailableCount: null, observedAtMs: 5_000 }
    );
    expect(merged?.rateLimitResetCreditsAvailableCount).toBe(0);
  });

  it('never lets an older host read overwrite fresher local evidence', () => {
    const local = quota({
      rateLimitResetCreditsAvailableCount: 3,
      resetCreditsCountEvidenceAtMs: 9_000,
    });
    const merged = mergeHostCodexResetCredits(local, {
      availableCount: 0,
      applicableAvailableCount: null,
      observedAtMs: 5_000,
    });
    expect(merged).toBe(local);
  });

  it('does not fabricate windows when the credential has no local quota state', () => {
    const merged = mergeHostCodexResetCredits(undefined, {
      availableCount: 1,
      applicableAvailableCount: null,
      observedAtMs: 5_000,
    });
    expect(merged).toEqual(
      expect.objectContaining({
        status: 'success',
        windows: [],
        rateLimitResetCreditsAvailableCount: 1,
        resetCreditsCountEvidenceAtMs: 5_000,
      })
    );
    expect(merged?.rateLimitResetCredits).toBeUndefined();
  });

  it('keeps the post-consumption unknown state instead of resurrecting an older host count', () => {
    const mutated = quota({
      rateLimitResetCreditsAvailableCount: null,
      resetCreditsDetailStale: true,
      resetCreditsCountEvidenceAtMs: null,
      resetCreditsEvidenceAtMs: 9_000,
    });
    const merged = mergeHostCodexResetCredits(mutated, {
      availableCount: 1,
      applicableAvailableCount: 0,
      observedAtMs: 5_000,
    });
    expect(merged).toBe(mutated);
  });

  it('leaves the quota untouched without host evidence', () => {
    const local = quota({ rateLimitResetCreditsAvailableCount: 1 });
    expect(mergeHostCodexResetCredits(local, undefined)).toBe(local);
  });
});

describe('credentialQuotaListWindows', () => {
  it('uses the actual seven-day duration for a sole Codex primary window', () => {
    const windows = credentialQuotaListWindows(
      JSON.stringify({
        windows: [
          {
            name: 'primary',
            usedPercent: 4,
            remainingFraction: 0.96,
            limitWindowSeconds: 604800,
          },
        ],
      }),
      'codex'
    );
    expect(windows).toEqual([
      expect.objectContaining({ kind: 'weekly', label: 'Weekly', limitWindowSeconds: 604800 }),
    ]);
  });

  it('treats Codex rate-limited used=0 remainingFraction=0 as depleted, not full', () => {
    const windows = credentialQuotaListWindows(
      JSON.stringify({
        windows: [
          {
            name: 'primary',
            usedPercent: 0,
            remainingFraction: 0,
            resetAt: '2026-11-14T22:07:42Z',
            sourceStatus: 'rate-limited',
            limitWindowSeconds: 18000,
          },
          {
            name: 'secondary',
            usedPercent: 100,
            remainingFraction: 0,
            resetAt: '2026-11-16T13:50:52Z',
            sourceStatus: 'rate-limited',
            limitWindowSeconds: 604800,
          },
        ],
      }),
      'codex',
      1_794_683_272_000
    );
    expect(windows).toEqual([
      expect.objectContaining({
        kind: 'five_hour',
        usedPercent: 100,
        remainingPercent: 0,
      }),
      expect.objectContaining({
        kind: 'weekly',
        usedPercent: 100,
        remainingPercent: 0,
      }),
    ]);
  });

  it('does not let one local Codex 5h replace both remote duration slots', () => {
    const local = [
      {
        key: 'five-hour',
        label: '5h',
        kind: 'five_hour' as const,
        usedPercent: 0,
        remainingPercent: 100,
        resetLabel: '2026-11-15T16:07:00Z',
        resetAccuracy: 'exact' as const,
        limitWindowSeconds: 18000,
        resetAtMs: Date.parse('2026-11-15T16:07:00Z'),
        fromMs: null,
        toMs: null,
        source: 'codex' as const,
        modelScope: { kind: 'family' as const, key: 'codex_main', complete: true },
        observedAtMs: 2_000,
        quotaProgressObservedAtMs: 2_000,
      },
    ];
    const remote = credentialQuotaListWindows(
      JSON.stringify({
        windows: [
          {
            name: 'primary',
            usedPercent: 0,
            remainingFraction: 0,
            resetAt: '2026-11-14T22:07:42Z',
            limitWindowSeconds: 18000,
          },
          {
            name: 'secondary',
            usedPercent: 100,
            remainingFraction: 0,
            resetAt: '2026-11-16T13:50:52Z',
            limitWindowSeconds: 604800,
          },
        ],
      }),
      'codex',
      1_000
    );
    const merged = mergeCredentialQuotaListWindows(local, remote);
    expect(merged.map((window) => window.kind).sort()).toEqual(['five_hour', 'weekly']);
    expect(merged.filter((window) => window.kind === 'five_hour')).toHaveLength(1);
    expect(merged.find((window) => window.kind === 'weekly')).toMatchObject({
      usedPercent: 100,
      remainingPercent: 0,
    });
  });

  it('replaces the same Codex main scope without duplicating its weekly window', () => {
    const local = [
      {
        key: 'weekly',
        label: 'Weekly',
        kind: 'weekly' as const,
        usedPercent: 5,
        remainingPercent: 95,
        resetLabel: '',
        resetAccuracy: 'exact' as const,
        limitWindowSeconds: 604800,
        resetAtMs: null,
        fromMs: null,
        toMs: null,
        source: 'codex' as const,
        modelScope: { kind: 'family' as const, key: 'codex_main', complete: true },
      },
    ];
    const remote = credentialQuotaListWindows(
      JSON.stringify({
        windows: [
          { name: 'primary', usedPercent: 34, limitWindowSeconds: 18000 },
          { name: 'secondary', usedPercent: 5, limitWindowSeconds: 604800 },
        ],
      }),
      'codex'
    );
    const merged = mergeCredentialQuotaListWindows(local, remote);
    expect(merged).toHaveLength(2);
    expect(merged.filter((w) => w.kind === 'weekly')).toHaveLength(1);
  });

  it('preserves independent model windows when replacing main quota windows', () => {
    const scoped = {
      key: 'spark-weekly',
      label: 'Spark weekly',
      kind: 'weekly' as const,
      usedPercent: 50,
      remainingPercent: 50,
      resetLabel: '',
      resetAccuracy: 'exact' as const,
      limitWindowSeconds: 604800,
      resetAtMs: null,
      fromMs: null,
      toMs: null,
      source: 'codex' as const,
      modelScope: { kind: 'models' as const, models: ['spark'], complete: true },
    };
    const remote = credentialQuotaListWindows(
      JSON.stringify({
        windows: [{ name: 'primary', usedPercent: 4, limitWindowSeconds: 604800 }],
      }),
      'codex'
    );
    expect(mergeCredentialQuotaListWindows([scoped], remote)).toEqual([...remote, scoped]);
  });

  it('does not treat independent model windows as duplicate primary windows', () => {
    const remote = credentialQuotaListWindows(
      JSON.stringify({
        windows: [
          { name: 'primary', usedPercent: 4, limitWindowSeconds: 604800 },
          { name: 'primary', model: 'spark', usedPercent: 25, limitWindowSeconds: 604800 },
        ],
      }),
      'codex'
    );
    expect(remote[0].key).not.toBe(remote[1].key);
    expect(remote[1].modelScope).toEqual({ kind: 'models', models: ['spark'], complete: true });
  });
  it('keeps an unknown duration instead of guessing five hours from the primary slot', () => {
    const windows = credentialQuotaListWindows(
      JSON.stringify({
        complete: false,
        windows: [
          {
            name: 'primary',
            usedPercent: 0,
            remainingFraction: 1,
            resetAt: '2026-11-09T12:00:00.000Z',
          },
        ],
      }),
      'codex',
      1_794_215_437_000
    );

    expect(windows).toEqual([
      expect.objectContaining({
        key: 'codex:primary',
        label: 'primary',
        kind: 'unknown',
        usedPercent: 0,
        remainingPercent: 100,
        source: 'codex',
        windowMode: 'unknown',
        observedAtMs: 1_794_215_437_000,
      }),
    ]);
  });

  it('replaces an older local five-hour window with the newer partial Codex window', () => {
    const local = [
      {
        key: 'five-hour',
        label: '5h',
        kind: 'five_hour' as const,
        usedPercent: 100,
        remainingPercent: 0,
        resetLabel: 'old',
        resetAccuracy: 'exact' as const,
        limitWindowSeconds: 18_000,
        resetAtMs: null,
        fromMs: null,
        toMs: null,
        modelScope: { kind: 'all' as const, complete: true },
      },
    ];
    const remote = credentialQuotaListWindows(
      JSON.stringify({ windows: [{ name: 'primary', usedPercent: 0, remainingFraction: 1 }] }),
      'codex',
      2_000
    );

    expect(mergeCredentialQuotaListWindows(local, remote)).toEqual(remote);
  });

  it('drops stale generic Codex long windows when a newer primary window is available', () => {
    const local = [
      {
        key: 'weekly',
        label: 'Weekly',
        kind: 'weekly' as const,
        usedPercent: 100,
        remainingPercent: 0,
        resetLabel: 'old',
        resetAccuracy: 'exact' as const,
        limitWindowSeconds: 7 * 86400,
        resetAtMs: null,
        fromMs: null,
        toMs: null,
        modelScope: { kind: 'all' as const, complete: true },
      },
    ];
    const remote = credentialQuotaListWindows(
      JSON.stringify({ windows: [{ name: 'primary', usedPercent: 0, remainingFraction: 1 }] }),
      'codex',
      2_000
    );

    expect(mergeCredentialQuotaListWindows(local, remote)).toEqual(remote);
  });

  it('selects a depleted attempt over a healthier lastSuccess cache', () => {
    const depleted = JSON.stringify({
      windows: [{ name: 'weekly', usedPercent: 100, remainingFraction: 0 }],
    });
    const healthy = JSON.stringify({
      windows: [{ name: 'weekly', usedPercent: 0, remainingFraction: 1 }],
    });
    const selected = selectCredentialQuotaWindowsObservation({
      attempt: { queryStatus: 'success', windowsJson: depleted, observedAtMs: 2_000 },
      lastSuccess: { queryStatus: 'success', windowsJson: healthy, observedAtMs: 1_000 },
    });
    expect(selected).toMatchObject({ windowsJson: depleted, observedAtMs: 2_000 });
  });

  it('lets a live depleted local window override a healthier host cache', () => {
    const local = [
      {
        key: 'opencode-go:weekly',
        label: 'weekly',
        kind: 'weekly' as const,
        usedPercent: 100,
        remainingPercent: 0,
        resetLabel: 'now',
        resetAccuracy: 'exact' as const,
        limitWindowSeconds: null,
        resetAtMs: Date.parse('2026-11-13T02:00:00Z'),
        fromMs: null,
        toMs: null,
        source: 'opencode-go' as const,
        windowMode: 'calendar' as const,
        observedAtMs: 2_000,
        modelScope: { kind: 'all' as const, complete: true },
      },
    ];
    const remote = credentialQuotaListWindows(
      JSON.stringify({
        windows: [
          {
            name: 'weekly',
            usedPercent: 0,
            remainingFraction: 1,
            resetAt: '2026-11-16T00:00:00Z',
            sourceStatus: 'ok',
          },
        ],
      }),
      'opencode-go',
      1_000
    );

    const merged = mergeCredentialQuotaListWindows(local, remote);
    expect(merged.find((window) => window.kind === 'weekly')).toMatchObject({
      usedPercent: 100,
      remainingPercent: 0,
      observedAtMs: 2_000,
    });
  });

  it('detects depleted display windows used to suppress stale first paint', () => {
    expect(
      quotaDisplayWindowsLookDepleted([
        {
          key: 'opencode-go:monthly',
          label: 'monthly',
          kind: 'monthly',
          usedPercent: 100,
          remainingPercent: 0,
          resetLabel: '',
          resetAccuracy: 'exact',
          limitWindowSeconds: null,
          resetAtMs: null,
          fromMs: null,
          toMs: null,
          source: 'opencode-go',
        },
      ])
    ).toBe(true);
    expect(
      quotaDisplayWindowsLookDepleted([
        {
          key: 'opencode-go:weekly',
          label: 'weekly',
          kind: 'weekly',
          usedPercent: 10,
          remainingPercent: 90,
          resetLabel: '',
          resetAccuracy: 'exact',
          limitWindowSeconds: null,
          resetAtMs: null,
          fromMs: null,
          toMs: null,
          source: 'opencode-go',
        },
      ])
    ).toBe(false);
  });

  it('replaces stale local OpenCode monthly exhaustion with authoritative lastSuccess windows', () => {
    const local = [
      {
        key: 'opencode-go:monthly',
        label: 'monthly',
        kind: 'monthly' as const,
        usedPercent: 100,
        remainingPercent: 0,
        resetLabel: '2026-11-06T10:14:55Z',
        resetAccuracy: 'exact' as const,
        limitWindowSeconds: null,
        resetAtMs: Date.parse('2026-11-06T10:14:55Z'),
        fromMs: null,
        toMs: null,
        source: 'opencode-go' as const,
        windowMode: 'calendar' as const,
        modelScope: { kind: 'all' as const, complete: true },
      },
      {
        key: 'opencode-go:weekly',
        label: 'weekly',
        kind: 'weekly' as const,
        usedPercent: 0,
        remainingPercent: 100,
        resetLabel: '2026-11-16T00:00:00Z',
        resetAccuracy: 'exact' as const,
        limitWindowSeconds: null,
        resetAtMs: Date.parse('2026-11-16T00:00:00Z'),
        fromMs: null,
        toMs: null,
        source: 'opencode-go' as const,
        windowMode: 'calendar' as const,
        modelScope: { kind: 'all' as const, complete: true },
      },
    ];
    const remote = credentialQuotaListWindows(
      JSON.stringify({
        windows: [
          {
            name: 'rolling',
            usedPercent: 0,
            remainingFraction: 1,
            resetAt: '2026-11-13T05:30:04.213Z',
            sourceStatus: 'ok',
          },
          {
            name: 'weekly',
            usedPercent: 0,
            remainingFraction: 1,
            resetAt: '2026-11-16T00:00:00Z',
            sourceStatus: 'ok',
          },
          {
            name: 'monthly',
            usedPercent: 0,
            remainingFraction: 1,
            resetAt: '2026-12-13T00:27:12Z',
            sourceStatus: 'ok',
          },
        ],
      }),
      'opencode-go',
      1_794_529_804_000
    );

    const merged = mergeCredentialQuotaListWindows(local, remote);
    expect(merged).toEqual(remote);
    expect(merged.find((window) => window.kind === 'monthly')).toMatchObject({
      usedPercent: 0,
      remainingPercent: 100,
    });
  });
});
