import { expect, it } from 'vitest';
import type { ProviderPolicyStatus } from '@/services/api/providerPolicy';
import {
  providerPolicyMoneyText,
  providerPolicyPresentationLines,
} from './providerPolicyPresentation';

const t = (key: string, options?: Record<string, string>) =>
  options ? `${key}:${JSON.stringify(options)}` : key;

const status = (overrides: Partial<ProviderPolicyStatus> = {}): ProviderPolicyStatus =>
  ({
    providerId: 'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaa1',
    scope: {
      instance_id: '22222222-2222-2222-2222-222222222222',
      instance_incarnation: '33333333-3333-3333-3333-333333333333',
      boot_epoch: '44444444-4444-4444-4444-444444444444',
      snapshot_revision: 1,
    },
    targets: [],
    subjectId: 'subj',
    enabled: true,
    effective: false,
    generation: 1,
    owner: 'provider-balance/install',
    source: 'explicit',
    updatedAtMs: 1700000000000,
    bindingCurrent: true,
    locked: true,
    manualDisabled: false,
    ownPaused: true,
    applied: true,
    state: 'paused',
    reason: 'fixture',
    funding: [
      {
        memberIds: ['bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbb2'],
        query: {
          scope: {
            instance_id: '22222222-2222-2222-2222-222222222222',
            instance_incarnation: '33333333-3333-3333-3333-333333333333',
            boot_epoch: '44444444-4444-4444-4444-444444444444',
            snapshot_revision: 1,
          },
          targets: [],
          version: 1,
        },
        keyMatched: true,
        fresh: false,
        reason: 'success',
        observedAtMs: 1700000000000,
        balances: [{ currency: 'USD', total: '0.0500' }],
      },
    ],
    ...overrides,
  }) as ProviderPolicyStatus;

it('keeps list and detail on the same identity-fenced presentation lines', () => {
  const lines = providerPolicyPresentationLines(status(), t);
  expect(lines).toEqual([
    'ai_providers.money_summary_enabled · ai_providers.money_summary_explicit · ai_providers.money_summary_locked',
    'ai_providers.money_summary_not_ready · ai_providers.money_summary_own_paused',
    'ai_providers.money_summary_manual_open · paused · fixture',
    'ai_providers.money_summary_updated:{"time":"2023-11-14T22:13:20.000Z"}',
    'ai_providers.money_summary_source:{"id":"bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbb2"} · success · ai_providers.money_summary_stale',
    'ai_providers.money_summary_last_success:{"time":"2023-11-14T22:13:20.000Z"}',
    'USD 0.05',
  ]);
});

it('never synthesizes zero totals for unknown or non-finite money', () => {
  expect(providerPolicyMoneyText('USD', 'Infinity', 'unknown')).toBe('unknown');
  expect(providerPolicyMoneyText('usd', '1', 'unknown')).toBe('unknown');
  const lines = providerPolicyPresentationLines(
    status({ funding: [{ ...status().funding![0], balances: undefined, fresh: true }] }),
    t
  );
  expect(lines.some((line) => line.includes('USD'))).toBe(false);
  expect(lines[lines.length - 1]).toBe('ai_providers.money_summary_amount_unknown');
});
