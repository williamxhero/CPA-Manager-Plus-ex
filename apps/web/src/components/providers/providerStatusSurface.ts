/** #84 consumer surface kinds: current policy vs historical vs credential-only. */

export type ProviderStatusEvidenceKind =
  | 'current_policy'
  | 'historical_probe'
  | 'historical_event'
  | 'credential_auto'
  | 'filter_only'
  | 'observation_only'
  | 'post_identity';

export type ProviderStatusConsumer = {
  id: string;
  surface: string;
  path: string;
  evidence: ProviderStatusEvidenceKind;
  notes: string;
};

/** Exhaustive registry of applicable status consumers for inventory + tests. */
export const PROVIDER_STATUS_CONSUMERS: readonly ProviderStatusConsumer[] = [
  {
    id: 'provider-table',
    surface: 'Provider table / narrow cards',
    path: 'ProviderTable.tsx + ProviderPolicyControl + ProviderMoneySummary',
    evidence: 'current_policy',
    notes: 'Name-leading auto marker + policy switch; manual config toggle stays in its own cell',
  },
  {
    id: 'provider-detail',
    surface: 'Provider detail drawer',
    path: 'ProviderDetailDrawer.tsx',
    evidence: 'current_policy',
    notes: 'Same providerPolicyPresentation lines as list',
  },
  {
    id: 'health-check',
    surface: 'Health check drawer',
    path: 'ProviderHealthCheckDrawer.tsx',
    evidence: 'historical_probe',
    notes: 'Probe results + config enable only; historic note required',
  },
  {
    id: 'accounts-credential',
    surface: 'Accounts grid / list / detail',
    path: 'CredentialAutoStartStopToggle.tsx',
    evidence: 'credential_auto',
    notes: 'Credential owner, not provider monetary; resume clears credential pause only',
  },
  {
    id: 'monitoring-filter',
    surface: 'Monitoring overview filters',
    path: 'MonitoringFiltersPanel.tsx',
    evidence: 'filter_only',
    notes: 'Provider is a filter key; no policy/money presentation',
  },
  {
    id: 'monitoring-events',
    surface: 'Monitoring realtime / action history',
    path: 'RealtimeEventsPanel.tsx',
    evidence: 'historical_event',
    notes: 'Archived request events; not current monetary evidence',
  },
  {
    id: 'inspection',
    surface: 'Inspection probe outcomes',
    path: 'monitoring/model/*Inspection*',
    evidence: 'historical_probe',
    notes: 'One-shot probe recommendations; not applied policy proof',
  },
  {
    id: 'official-balance',
    surface: 'Official balance panel',
    path: 'OfficialBalancePanel*.tsx',
    evidence: 'observation_only',
    notes: 'Funding observation adapters; must not invent pause',
  },
  {
    id: 'resource-protection',
    surface: 'Resource protection indicator',
    path: 'ResourceProtectionIndicator.tsx',
    evidence: 'observation_only',
    notes: 'Capability/identity orthogonal to monetary owner',
  },
  {
    id: 'create-import',
    surface: 'Create / import post-state',
    path: 'Providers page after identity issued',
    evidence: 'post_identity',
    notes: 'Reuse ProviderPolicyControl + presentation once binding exists; default OFF',
  },
] as const;

export function consumersByEvidence(
  kind: ProviderStatusEvidenceKind
): readonly ProviderStatusConsumer[] {
  return PROVIDER_STATUS_CONSUMERS.filter((c) => c.evidence === kind);
}

export function isHistoricalEvidence(kind: ProviderStatusEvidenceKind): boolean {
  return kind === 'historical_probe' || kind === 'historical_event';
}
