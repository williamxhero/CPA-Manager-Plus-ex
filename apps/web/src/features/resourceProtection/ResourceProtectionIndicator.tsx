import { useTranslation } from 'react-i18next';
import type { RuntimeResourceBinding } from '@/services/api/usageService';
import { InfoTooltip } from '@/components/ui/InfoTooltip';
import type { ResourceProtectionCapability } from './resourceProtection';
import styles from './ResourceProtectionIndicator.module.scss';

// Informational manager capability only; never an account health or enforcement ACK.
export function ResourceProtectionIndicator({
  status = 'unknown',
  binding,
}: {
  status?: ResourceProtectionCapability;
  binding?: RuntimeResourceBinding;
}) {
  const { t } = useTranslation();
  const title = t('resource_protection.title');
  const label = `${title}: ${t(`resource_protection.${status}`)}`;
  const description = t(`resource_protection.${status}_description`);
  return (
    <span
      className={styles.indicator}
      title={label}
      onClick={(event) => event.stopPropagation()}
      onKeyDown={(event) => event.stopPropagation()}
    >
      <InfoTooltip
        ariaLabel={`${label}. ${description}`}
        content={
          <>
            <strong>{label}</strong>
            <br />
            {description}
            {binding && (
              <code>{JSON.stringify({ scope: binding.scope, targets: binding.targets })}</code>
            )}
          </>
        }
      />
    </span>
  );
}
