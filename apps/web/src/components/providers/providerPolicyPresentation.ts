import { formatBalanceText } from '@/utils/quota/formatBalanceAmount';

/** Identity-fenced money text for the read-only list balance cell. */
export const providerPolicyMoneyText = (currency: string, total: string, unknown: string) =>
  formatBalanceText(currency, total) ?? unknown;
