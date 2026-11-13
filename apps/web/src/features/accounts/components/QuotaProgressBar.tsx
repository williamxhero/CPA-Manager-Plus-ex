import styles from './QuotaProgressBar.module.scss';

export interface QuotaProgressBarProps {
  percent: number | null;
  highThreshold: number;
  mediumThreshold: number;
}

export function QuotaProgressBar({
  percent,
  highThreshold: _highThreshold,
  mediumThreshold: _mediumThreshold,
}: QuotaProgressBarProps) {
  // Remaining-capacity bars stay green for any remaining > 0; depleted is red.
  const normalized = percent === null ? null : Math.min(100, Math.max(0, percent));
  const fillClass =
    normalized === null
      ? styles.fillMedium
      : normalized <= 0
        ? styles.fillLow
        : styles.fillHigh;

  return (
    <div className={styles.track}>
      <div
        className={`${styles.fill} ${fillClass}`}
        style={{ width: `${Math.round(normalized ?? 0)}%` }}
      />
    </div>
  );
}
