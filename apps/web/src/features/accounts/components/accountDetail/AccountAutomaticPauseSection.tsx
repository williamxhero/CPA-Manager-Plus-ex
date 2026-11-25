import { useId } from 'react';
import { useTranslation } from 'react-i18next';
import { IconPower } from '@/components/ui/icons';
import {
  buildAccountAutomaticPausePresentation,
  type AccountAutomaticPauseState,
} from '@/features/accounts/model/accountAutomaticPause';
import { useAccountAutomaticPause } from '@/features/accounts/hooks/useAccountAutomaticPause';
import { AccountOverviewFieldGrid } from './AccountOverviewFieldGrid';
import styles from '@/features/accounts/AccountsPage.module.scss';

const HINT_KEYS: Readonly<Record<AccountAutomaticPauseState, string>> = {
  reading: 'accounts.detail_overview_auto_pause_reading_hint',
  active: 'accounts.detail_overview_auto_pause_active_hint',
  paused: 'accounts.detail_overview_auto_pause_paused_hint',
  unavailable: 'accounts.detail_overview_auto_pause_unavailable_hint',
};

export interface AccountAutomaticPauseSectionProps {
  managerServiceBase?: string;
  managementKey?: string;
  authIndex?: string;
}

/**
 * Read-only automatic start/stop row. No switch, no write path: it only reports
 * what the backend credential-policy read answered for this credential.
 */
export function AccountAutomaticPauseSection({
  managerServiceBase,
  managementKey,
  authIndex,
}: AccountAutomaticPauseSectionProps) {
  const { t } = useTranslation();
  const headingId = useId();
  const read = useAccountAutomaticPause(managerServiceBase, managementKey, authIndex);
  const presentation = buildAccountAutomaticPausePresentation(read);
  const hintKey = HINT_KEYS[presentation.state];

  return (
    <section
      className={styles.overviewCard}
      aria-labelledby={headingId}
      data-overview-section="automatic-pause"
      data-overview-auto-pause={presentation.state}
    >
      <div className={styles.overviewCardHeader}>
        <div className={styles.overviewSectionHeading}>
          <span className={styles.overviewSectionIcon} aria-hidden="true">
            <IconPower size={18} />
          </span>
          <h3 id={headingId}>{t('accounts.detail_overview_auto_pause_title')}</h3>
        </div>
      </div>
      <p className={styles.overviewDecisionReason} title={t(hintKey)}>
        {t(hintKey)}
      </p>
      <AccountOverviewFieldGrid fields={presentation.fields} />
    </section>
  );
}
