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
      // The pause glyph carries meaning, so the badge itself names it rather
      // than leaving auto-pause to the hover-only title.
      aria-label={autoPaused ? `${label} · ${t('accounts.auto_pause_badge_hint')}` : undefined}
      data-account-health-auto-paused={autoPaused ? 'true' : undefined}
    >
      {label}
      {autoPaused ? <IconPause size={12} className={styles.badgePauseGlyph} /> : null}
    </span>
  );
}
