import { describe, expect, it } from 'vitest';
import {
  credentialQuotaListWindows,
  mergeCredentialQuotaListWindows,
} from './credentialQuotaListWindows';

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
});
