import {
  formatPercent,
  formatQuotaResetDisplay,
  type AccountQuotaLifecycleBarOverride,
  type AntigravityQuotaMatrix,
} from '@/features/accounts/model/accountsPagePresentation';
import { useTranslation } from 'react-i18next';
import {
  resolveQuotaBarRemainingPercent,
  resolveQuotaBarTone,
} from '../model/quotaBarPresentation';
import { formatQuotaUnitFraction } from '../model/quotaUnitFraction';
import styles from '../AccountsPage.module.scss';

interface AccountQuotaMatrixProps {
  accountKey: string;
  matrix: AntigravityQuotaMatrix;
  lifecycleBarOverride?: AccountQuotaLifecycleBarOverride;
}

const quotaBarToneClass = {
  good: styles.quotaBarGood,
  warn: styles.quotaBarWarn,
  bad: styles.quotaBarBad,
  neutral: styles.quotaBarNeutral,
} as const;

const getMatrixBarClass = (
  remainingPercent: number | null,
  lifecycleBarOverride: AccountQuotaLifecycleBarOverride,
  usedPercent?: number | null
) =>
  quotaBarToneClass[resolveQuotaBarTone(remainingPercent, lifecycleBarOverride, usedPercent)];

export function AccountQuotaMatrix({
  accountKey,
  matrix,
  lifecycleBarOverride = null,
}: AccountQuotaMatrixProps) {
  const { t, i18n } = useTranslation();
  return (
    <span className={styles.quotaMatrix} data-account-quota-matrix={accountKey}>
      {matrix.rows.map((matrixRow) => (
        <span
          key={matrixRow.key}
          className={styles.quotaMatrixRow}
          data-account-quota-matrix-row={matrixRow.key}
        >
          <span className={styles.quotaMatrixWindowLabel}>{matrixRow.label}</span>
          <span className={styles.quotaMatrixCells}>
            {matrixRow.cells.map((cell) => {
              const windowRemaining = resolveQuotaBarRemainingPercent(
                cell.window.remainingPercent,
                cell.window.usedPercent
              );
              const windowWidth = Math.max(0, Math.min(100, windowRemaining ?? 0));
              const resetDisplay = formatQuotaResetDisplay(
                cell.window.resetAtMs,
                cell.window.resetLabel,
                i18n.language
              );
              const usedFraction = formatQuotaUnitFraction(cell.window);
              const valueText =
                usedFraction !== null
                  ? usedFraction === '-' ? '-' : `${t('accounts.detail_used')} ${usedFraction}`
                  : formatPercent(windowRemaining);
              const title = [
                `${cell.groupLabel} ${cell.window.label}: ${valueText}`,
                resetDisplay !== '-' ? `${t('accounts.col_reset')}: ${resetDisplay}` : '',
              ]
                .filter(Boolean)
                .join(' · ');
              return (
                <span
                  key={cell.window.key}
                  className={styles.quotaMatrixCell}
                  data-account-quota-matrix-cell={`${matrixRow.key}:${cell.groupLabel}`}
                  title={title}
                >
                  <span className={styles.quotaMatrixGroupLabel} title={cell.groupLabel}>
                    {cell.displayLabel}
                  </span>
                  <span
                    className={`${styles.quotaTrack} ${styles.quotaMatrixTrack}`}
                    aria-hidden="true"
                  >
                    <span
                      className={`${styles.quotaBar} ${getMatrixBarClass(
                        cell.window.remainingPercent,
                        lifecycleBarOverride,
                        cell.window.usedPercent
                      )}`}
                      style={{ width: `${windowWidth}%` }}
                    />
                  </span>
                  <strong className={styles.quotaMatrixPercent}>
                    {valueText}
                  </strong>
                </span>
              );
            })}
          </span>
        </span>
      ))}
    </span>
  );
}
