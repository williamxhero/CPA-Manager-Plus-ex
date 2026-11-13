import type { AccountQuotaLifecycleBarOverride } from './accountsPagePresentation';

export type QuotaBarTone = 'good' | 'warn' | 'bad' | 'neutral';

/** Prefer used% when present so remainingFraction cannot disagree with the label. */
export const resolveQuotaBarRemainingPercent = (
  remainingPercent: number | null | undefined,
  usedPercent?: number | null
): number | null => {
  if (typeof usedPercent === 'number' && Number.isFinite(usedPercent)) {
    return Math.max(0, Math.min(100, 100 - usedPercent));
  }
  if (typeof remainingPercent === 'number' && Number.isFinite(remainingPercent)) {
    return Math.max(0, Math.min(100, remainingPercent));
  }
  return null;
};

/**
 * Remaining-capacity bars: any remaining > 0 is green; depleted is red.
 * Unknown stays neutral. Lifecycle overrides only apply when remaining is unknown.
 */
export const resolveQuotaBarTone = (
  remainingPercent: number | null | undefined,
  lifecycleBarOverride: AccountQuotaLifecycleBarOverride = null,
  usedPercent?: number | null
): QuotaBarTone => {
  const effective = resolveQuotaBarRemainingPercent(remainingPercent, usedPercent);
  if (effective !== null) {
    return effective <= 0 ? 'bad' : 'good';
  }
  if (lifecycleBarOverride === 'bad') return 'bad';
  if (lifecycleBarOverride === 'neutral') return 'neutral';
  return 'neutral';
};
