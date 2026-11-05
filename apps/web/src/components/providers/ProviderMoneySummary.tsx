import { useTranslation } from 'react-i18next';
import type { ProviderMoneyView } from './useProviderMoneySummaries';
import { providerPolicyMoneyText } from './providerPolicyPresentation';
import type { OfficialBalanceView } from './useOfficialBalanceList';
import { formatBalanceDisplay } from '@/utils/quota/formatBalanceAmount';
import styles from './ProviderMoneySummary.module.scss';

function policyAmounts(view?: ProviderMoneyView): string[] {
  const lines: string[] = [];
  for (const source of view?.data?.funding ?? []) {
    for (const balance of source.balances ?? []) {
      const text = providerPolicyMoneyText(balance.currency, balance.total, '');
      if (text) lines.push(text);
    }
  }
  return lines;
}

export function ProviderMoneySummary({
  view,
  official,
}: {
  view?: ProviderMoneyView;
  official?: OfficialBalanceView;
}) {
  const { t } = useTranslation();
  const amounts = official?.text
    ? [formatBalanceDisplay(official.text)]
    : policyAmounts(view);
  const pending = (official?.pending || view?.pending) && amounts.length === 0;
  return (
    <div
      className={styles.summary}
      aria-label={t('ai_providers.table_col_balance')}
      aria-live="polite"
    >
      {pending ? <span>…</span> : amounts.length > 0 ? amounts.map((line) => <div key={line}>{line}</div>) : <span>—</span>}
    </div>
  );
}
