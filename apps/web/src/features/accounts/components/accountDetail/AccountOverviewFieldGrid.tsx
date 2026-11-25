import { useTranslation } from 'react-i18next';
import type { AccountDetailField } from '@/features/accounts/model/accountDetailViewModel';
import { AccountDetailFieldValue } from './AccountDetailFieldList';
import styles from '@/features/accounts/AccountsPage.module.scss';

/** Shared two-column field grid used by every overview card. */
export function AccountOverviewFieldGrid({ fields }: { fields: AccountDetailField[] }) {
  const { t } = useTranslation();
  if (fields.length === 0) return null;
  return (
    <dl className={styles.overviewFieldGrid}>
      {fields.map((field) => (
        <div key={field.key}>
          <dt>{t(field.labelKey, { defaultValue: field.labelKey })}</dt>
          <dd>
            <AccountDetailFieldValue field={field} />
          </dd>
        </div>
      ))}
    </dl>
  );
}
