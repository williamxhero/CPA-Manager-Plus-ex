import { useId } from 'react';
import { useTranslation } from 'react-i18next';
import { IconPower } from '@/components/ui/icons';
import {
  ACCOUNT_AUTOMATIC_PAUSE_TITLE_KEY,
  buildAccountAutomaticPausePresentation,
  type AccountAutomaticPauseRead,
} from '@/features/accounts/model/accountAutomaticPause';
import { AccountOverviewFieldGrid } from './AccountOverviewFieldGrid';
import styles from '@/features/accounts/AccountsPage.module.scss';

export interface AccountAutomaticPauseSectionProps {
  /** The drawer owns the read; this section only presents what it was given. */
  read: AccountAutomaticPauseRead;
}

export function AccountAutomaticPauseSection({ read }: AccountAutomaticPauseSectionProps) {
  const { t } = useTranslation();
  const headingId = useId();
  const presentation = buildAccountAutomaticPausePresentation(read);
  const hint = t(presentation.hintKey);

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
          <h3 id={headingId}>{t(ACCOUNT_AUTOMATIC_PAUSE_TITLE_KEY)}</h3>
        </div>
      </div>
      <p className={styles.overviewDecisionReason} title={hint}>
        {hint}
      </p>
      <AccountOverviewFieldGrid fields={presentation.fields} />
    </section>
  );
}
