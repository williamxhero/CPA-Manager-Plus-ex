import type { ProviderPolicyFunding, ProviderPolicyStatus } from '@/services/api/providerPolicy';

export type Translate = (key: string, options?: Record<string, string>) => string;

export const providerPolicyTimeText = (ms?: number) =>
  typeof ms === 'number' && Number.isFinite(ms) && ms > 0 && !Number.isNaN(new Date(ms).getTime())
    ? new Date(ms).toISOString()
    : 'unknown';

export const providerPolicyMoneyText = (currency: string, total: string, unknown: string) =>
  /^[A-Z]{3}$/.test(currency) &&
  typeof total === 'string' &&
  /^-?(0|[1-9]\d{0,59})(\.\d{1,30})?$/.test(total)
    ? `${currency} ${total}`
    : unknown;

/** Identity-fenced read-model lines shared by list summary and detail. */
export function providerPolicyIntentLines(data: ProviderPolicyStatus, t: Translate): string[] {
  return [
    [
      data.enabled ? t('ai_providers.money_summary_enabled') : t('ai_providers.money_summary_disabled'),
      data.source === 'explicit'
        ? t('ai_providers.money_summary_explicit')
        : t('ai_providers.money_summary_default_off'),
      data.locked ? t('ai_providers.money_summary_locked') : t('ai_providers.money_summary_unlocked'),
    ].join(' · '),
    [
      data.effective
        ? t('ai_providers.money_summary_effective')
        : t('ai_providers.money_summary_not_ready'),
      data.ownPaused && data.applied
        ? t('ai_providers.money_summary_own_paused')
        : data.applied
          ? t('ai_providers.money_summary_own_recovered')
          : t('ai_providers.money_summary_unconfirmed'),
    ].join(' · '),
    [
      data.manualDisabled
        ? t('ai_providers.money_summary_manual_disabled')
        : t('ai_providers.money_summary_manual_open'),
      data.state,
      data.reason,
    ].join(' · '),
    t('ai_providers.money_summary_updated', { time: providerPolicyTimeText(data.updatedAtMs) }),
  ];
}

export function providerPolicyFundingLines(
  funding: ProviderPolicyFunding[] | undefined,
  t: Translate
): string[] {
  if (!funding || funding.length === 0) {
    return [
      [
        t('ai_providers.money_summary_amount_unknown'),
        t('ai_providers.money_summary_source_unverified'),
      ].join(' · '),
    ];
  }
  const lines: string[] = [];
  for (const source of funding) {
    lines.push(
      [
        t('ai_providers.money_summary_source', { id: source.memberIds.join(',') }),
        source.reason,
        source.fresh
          ? t('ai_providers.money_summary_current')
          : t('ai_providers.money_summary_stale'),
      ].join(' · ')
    );
    lines.push(
      t('ai_providers.money_summary_last_success', {
        time: providerPolicyTimeText(source.observedAtMs),
      })
    );
    if (source.balances?.length) {
      for (const b of source.balances) {
        lines.push(
          providerPolicyMoneyText(
            b.currency,
            b.total,
            t('ai_providers.money_summary_amount_unknown')
          )
        );
      }
    } else {
      lines.push(t('ai_providers.money_summary_amount_unknown'));
    }
  }
  return lines;
}

export function providerPolicyPresentationLines(
  data: ProviderPolicyStatus,
  t: Translate
): string[] {
  return [...providerPolicyIntentLines(data, t), ...providerPolicyFundingLines(data.funding, t)];
}
