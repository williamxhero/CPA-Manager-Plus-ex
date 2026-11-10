import { describe, expect, it } from 'vitest';
import type { AccountQuotaSnapshotWindow } from '@/services/api/usageService';
import { credentialQuotaListWindows } from './credentialQuotaListWindows';
import { buildAccountQuotaWindowDefinitions } from './accountQuotaWindowDefinitions';
import { mergeAccountQuotaSnapshotWindows } from './accountQuotaSnapshots';

const observedAtMs = Date.parse('2026-11-09T14:28:06Z');
const resetAt = '2026-11-16T13:40:41Z';
const snapshot = (
  providerWindowId: string,
  seconds: number,
  overrides: Partial<AccountQuotaSnapshotWindow> = {}
): AccountQuotaSnapshotWindow => ({
  provider_window_id: providerWindowId,
  window_kind: seconds === 18000 ? 'five_hour' : 'weekly',
  window_mode: 'fixed',
  model_scope_kind: 'family',
  model_scope_key: 'codex_main',
  source: 'response_header',
  observed_at_ms: observedAtMs - 60_000,
  boundary_accuracy: 'derived',
  cycle_end_ms: Date.parse(resetAt),
  duration_seconds: seconds,
  used_percent: 4,
  remaining_percent: 96,
  stale: false,
  ...overrides,
});

const definitions = (withFiveHour = false, seconds = 604800) =>
  buildAccountQuotaWindowDefinitions(
    credentialQuotaListWindows(
      JSON.stringify({
        windows: [
          ...(withFiveHour
            ? [{ name: 'primary', usedPercent: 70, limitWindowSeconds: 18000 }]
            : []),
          {
            name: withFiveHour ? 'secondary' : 'primary',
            usedPercent: 5,
            limitWindowSeconds: seconds,
            resetAt,
          },
        ],
      }),
      'codex',
      observedAtMs
    )
  );

describe('credential quota followed by delayed persisted snapshots', () => {
  it('keeps WXH at five-hour plus one weekly window after the snapshot response arrives', () => {
    const live = definitions(true);
    const before = mergeAccountQuotaSnapshotWindows(live, [], { provider: 'codex' });
    const after = mergeAccountQuotaSnapshotWindows(
      live,
      [snapshot('five-hour', 18000), snapshot('weekly', 604800)],
      { provider: 'codex' }
    );
    expect(before).toHaveLength(2);
    expect(after).toHaveLength(2);
    expect(after.map((window) => window.kind)).toEqual(['five_hour', 'weekly']);
    expect(after.map((window) => window.usedPercent)).toEqual([70, 5]);
  });

  it('keeps HM at one weekly window instead of reviving its expired former five-hour window', () => {
    const live = definitions();
    const before = mergeAccountQuotaSnapshotWindows(live, [], { provider: 'codex' });
    const after = mergeAccountQuotaSnapshotWindows(
      live,
      [
        snapshot('five-hour', 18000, {
          stale: true,
          observed_at_ms: observedAtMs - 12 * 86400_000,
          cycle_end_ms: observedAtMs - 11 * 86400_000,
        }),
        snapshot('weekly', 604800),
      ],
      { provider: 'codex' }
    );
    expect(before).toHaveLength(1);
    expect(after).toHaveLength(1);
    expect(after[0]).toMatchObject({
      providerWindowId: 'weekly',
      kind: 'weekly',
      usedPercent: 5,
      durationSeconds: 604800,
    });
  });

  it('preserves independent model quotas and newer snapshot evidence', () => {
    const after = mergeAccountQuotaSnapshotWindows(
      definitions(),
      [
        snapshot('spark-weekly', 604800, {
          model_scope_kind: 'models',
          model_scope_key: undefined,
          model_ids: ['gpt-5.3-codex-spark'],
        }),
        snapshot('five-hour', 18000, {
          observed_at_ms: observedAtMs + 1000,
          stale: true,
          cycle_end_ms: observedAtMs - 1000,
        }),
      ],
      { provider: 'codex' }
    );
    expect(after).toHaveLength(3);
  });

  it('preserves unexpired or unknown-duration windows without proof they were replaced', () => {
    const old = snapshot('five-hour', 18000, {
      stale: true,
      cycle_end_ms: observedAtMs - 1000,
    });
    const unknown = mergeAccountQuotaSnapshotWindows(definitions(false, 0), [old], {
      provider: 'codex',
    });
    const unexpired = mergeAccountQuotaSnapshotWindows(
      definitions(),
      [snapshot('five-hour', 18000)],
      { provider: 'codex' }
    );
    expect(unknown).toHaveLength(2);
    expect(unexpired).toHaveLength(2);
  });
});
