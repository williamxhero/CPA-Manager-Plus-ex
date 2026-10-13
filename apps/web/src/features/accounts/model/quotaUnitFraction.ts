import {
  isModelScopedAccountQuotaWindow,
  type AccountQuotaDisplayWindow,
} from './accountQuotaDisplayWindows';

export const getQuotaUnitCount = (kind: string | undefined, date = new Date()): number | null => {
  switch (kind) {
    case 'five_hour':
    case '5h':
      return 5;
    case 'daily':
      return 1;
    case 'weekly':
      return 7;
    case 'monthly':
      return new Date(date.getFullYear(), date.getMonth() + 1, 0).getDate();
    default:
      return null;
  }
};

type QuotaUnitWindow = Pick<
  AccountQuotaDisplayWindow,
  'kind' | 'windowMode' | 'modelScope' | 'source'
> & { usedPercent?: number | null };

// null means the caller should keep its existing display for this quota.
export const formatQuotaUnitFraction = (
  window: QuotaUnitWindow,
  date = new Date()
): string | null => {
  if (isModelScopedAccountQuotaWindow(window)) return null;
  const units = getQuotaUnitCount(window.kind, date);
  if (units === null) return null;
  const percent = window.usedPercent;
  if (typeof percent !== 'number' || !Number.isFinite(percent)) return '-';
  // Round in tenths first so 57% of five units does not become 2.849999….
  const usedUnits = Math.round((percent * units) / 10) / 10;
  return `${usedUnits.toFixed(1)}/${units}`;
};
