import { useTranslation } from 'react-i18next';
import { IconPause } from '@/components/ui/icons';
import styles from '@/features/accounts/AccountsPage.module.scss';

export function AccountListHealthBadge({
  statusClass,
  labelKey,
  title,
  autoPaused,
}: {
  statusClass: string;
  labelKey: string;
  title: string;
  autoPaused: boolean;
}) {
  const { t } = useTranslation();
  const label = t(labelKey);
  return (
    <span
      className={`${styles.badge} ${statusClass}`}
      title={title}
      data-account-health-auto-paused={autoPaused ? 'true' : undefined}
    >
      {label}
      {autoPaused ? (
        <>
          <IconPause size={12} className={styles.badgePauseGlyph} />
          {/* The pause glyph carries meaning, so the badge carries that meaning as
              text. It is visually hidden because the hover title already shows
              it, and a roleless span may not own an aria-label. */}
          <span className={styles.srOnly}>{t('accounts.auto_pause_badge_hint')}</span>
        </>
      ) : null}
    </span>
  );
}
