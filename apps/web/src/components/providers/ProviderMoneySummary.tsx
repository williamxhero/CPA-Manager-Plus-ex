import { useTranslation } from 'react-i18next';
import type { ProviderMoneyView } from './useProviderMoneySummaries';
import type { OfficialBalanceView } from './useOfficialBalanceList';
import {
  classifyBalanceAmount,
  formatBalanceDisplay,
  formatBalanceText,
  type BalanceAmountClass,
} from '@/utils/quota/formatBalanceAmount';
import styles from './ProviderMoneySummary.module.scss';

interface MoneyAmountLine {
  text: string;
  amount?: string;
}

const TONE_CLASS = {
  positive: styles.amountPositive,
  nonPositive: styles.amountNonPositive,
  unknown: '',
} satisfies Record<BalanceAmountClass, string>;

/** The 16×16 pause glyph the paid-access list already uses for a stopped provider. */
function PauseGlyph() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
      <circle cx="8" cy="8" r="8" fill="currentColor" />
      <rect x="5" y="4.6" width="2.2" height="6.8" rx="1.1" fill="white" />
      <rect x="8.8" y="4.6" width="2.2" height="6.8" rx="1.1" fill="white" />
    </svg>
  );
}

function policyAmountLines(view?: ProviderMoneyView): MoneyAmountLine[] {
  const lines: MoneyAmountLine[] = [];
  for (const source of view?.data?.funding ?? []) {
    for (const balance of source.balances ?? []) {
      const text = formatBalanceText(balance.currency, balance.total);
      if (text) lines.push({ text, amount: balance.total });
    }
  }
  return lines;
}

export function ProviderMoneySummary({
  view,
  official,
  unbound = false,
}: {
  view?: ProviderMoneyView;
  official?: OfficialBalanceView;
  /** Core issued no host-signed binding for this row, so no amount can be attributed to it. */
  unbound?: boolean;
}) {
  const { t } = useTranslation();
  const lines: MoneyAmountLine[] = official?.text
    ? [{ text: formatBalanceDisplay(official.text), amount: official.amount }]
    : policyAmountLines(view);
  // Every known raw amount decides whether this cell is exhausted; one positive
  // source means money is left, so no line carries the pause glyph.
  const exhausted = classifyBalanceAmount(lines.map((line) => line.amount)) === 'nonPositive';
  const pending = (official?.pending || view?.pending) && lines.length === 0;
  return (
    <div
      className={styles.summary}
      aria-label={t('ai_providers.table_col_balance')}
      aria-live="polite"
    >
      {pending ? (
        <span>…</span>
      ) : lines.length > 0 ? (
        lines.map((line, index) => {
          // Each line is coloured by its own sign, not by the cell-wide verdict.
          const tone = classifyBalanceAmount([line.amount]);
          return (
            <div
              key={`${index}:${line.text}`}
              className={`${styles.amountLine} ${TONE_CLASS[tone]}`.trim()}
              data-balance-tone={tone}
            >
              {line.text}
              {exhausted && index === lines.length - 1 && (
                <span
                  className={styles.pauseGlyph}
                  role="img"
                  aria-label={t('ai_providers.balance_exhausted_aria')}
                  title={t('ai_providers.balance_exhausted_aria')}
                  data-balance-paused="true"
                >
                  <PauseGlyph />
                </span>
              )}
            </div>
          );
        })
      ) : unbound ? (
        <span
          title={t('resource_protection.unbound_description')}
          aria-label={`${t('resource_protection.unbound')} ${t('resource_protection.unbound_description')}`}
        >
          {t('resource_protection.unbound')}
        </span>
      ) : (
        <span>{t('ai_providers.money_summary_amount_unknown')}</span>
      )}
    </div>
  );
}
