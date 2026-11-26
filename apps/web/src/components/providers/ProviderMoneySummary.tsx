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
      <circle cx="8" cy="8" r="8" fill="var(--danger-color, #ef4444)" />
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
}: {
  view?: ProviderMoneyView;
  official?: OfficialBalanceView;
}) {
  const { t } = useTranslation();
  const lines: MoneyAmountLine[] = official?.text
    ? [{ text: formatBalanceDisplay(official.text), amount: official.amount }]
    : policyAmountLines(view);
  // Every known raw amount decides the tone; one positive source means money is left.
  const tone = classifyBalanceAmount(lines.map((line) => line.amount));
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
        lines.map((line, index) => (
          <div
            key={`${index}:${line.text}`}
            className={`${styles.amountLine} ${TONE_CLASS[tone]}`.trim()}
            data-balance-tone={tone}
          >
            {line.text}
            {tone === 'nonPositive' && (
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
        ))
      ) : (
        <span>—</span>
      )}
    </div>
  );
}
