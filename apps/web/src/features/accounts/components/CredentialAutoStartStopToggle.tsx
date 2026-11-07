import { Button } from '@/components/ui/Button';
import { IconPower } from '@/components/ui/icons';
import { useCredentialAutoStartStop } from '@/features/accounts/hooks/useCredentialAutoStartStop';
import { useTranslation } from 'react-i18next';
import styles from './CredentialAutoStartStopToggle.module.scss';

interface CredentialAutoStartStopToggleProps {
  managerServiceBase: string;
  managementKey: string;
  authIndex?: string;
  revision?: string | number;
  hasQuota?: boolean;
}

export function CredentialAutoStartStopToggle({
  managerServiceBase,
  managementKey,
  authIndex,
  revision,
  hasQuota = false,
}: CredentialAutoStartStopToggleProps) {
  const { t } = useTranslation();
  const { data, busy, error, hasRemaining, setEnabled } = useCredentialAutoStartStop(
    managerServiceBase,
    managementKey,
    authIndex,
    revision
  );
  const on = data?.enabled === true;
  const quotaPresent = hasRemaining === true || hasQuota;
  const known = Boolean(data?.subjectId);
  const blocked =
    known && !on && (!data?.bindingCurrent || data?.availability.state !== 'available');
  return (
    <Button
      variant="secondary"
      size="sm"
      iconOnly
      type="button"
      className={styles.button}
      aria-pressed={on}
      aria-label={t('accounts.auto_start_stop')}
      title={t('accounts.auto_start_stop_aria')}
      data-credential-auto-start-stop={on ? 'on' : 'off'}
      data-auto-quota={on ? (quotaPresent ? 'present' : 'absent') : undefined}
      data-reason={error || undefined}
      data-availability={data?.availability?.reason || undefined}
      disabled={busy || Boolean(error) || blocked}
      onClick={() => void setEnabled(!on)}
    >
      <IconPower size={15} />
    </Button>
  );
}
