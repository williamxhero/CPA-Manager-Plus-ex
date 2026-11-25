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
  return (
    <span
      className={`${styles.badge} ${statusClass}`}
      title={title}
      data-account-health-auto-paused={autoPaused ? 'true' : undefined}
    >
      {t(labelKey)}
      {autoPaused ? <IconPause size={12} className={styles.badgePauseGlyph} /> : null}
    </span>
  );
}
