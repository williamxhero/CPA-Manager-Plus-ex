import { describe, expect, it } from 'vitest';
import {
  PROVIDER_STATUS_CONSUMERS,
  consumersByEvidence,
  isHistoricalEvidence,
} from './providerStatusSurface';

describe('providerStatusSurface registry (#84)', () => {
  it('registers every required consumer surface exactly once', () => {
    const ids = PROVIDER_STATUS_CONSUMERS.map((c) => c.id);
    expect(new Set(ids).size).toBe(ids.length);
    for (const required of [
      'provider-table',
      'provider-detail',
      'health-check',
      'accounts-credential',
      'monitoring-filter',
      'monitoring-events',
      'inspection',
      'official-balance',
      'resource-protection',
      'create-import',
    ]) {
      expect(ids).toContain(required);
    }
  });

  it('marks health / inspection / events as historical, never current_policy', () => {
    for (const c of [
      ...consumersByEvidence('historical_probe'),
      ...consumersByEvidence('historical_event'),
    ]) {
      expect(isHistoricalEvidence(c.evidence)).toBe(true);
      expect(c.evidence).not.toBe('current_policy');
    }
    expect(consumersByEvidence('current_policy').map((c) => c.id).sort()).toEqual([
      'provider-detail',
      'provider-table',
    ]);
  });

  it('keeps credential auto-pause on a separate evidence kind from provider money', () => {
    const credential = consumersByEvidence('credential_auto');
    expect(credential).toHaveLength(1);
    expect(credential[0]?.id).toBe('accounts-credential');
    expect(credential[0]?.notes).toMatch(/Credential owner/i);
  });
});
