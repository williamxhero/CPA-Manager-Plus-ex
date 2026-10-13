import { useTranslation } from 'react-i18next';
import { InfoTooltip } from '@/components/ui/InfoTooltip';
import { ToggleSwitch } from '@/components/ui/ToggleSwitch';
import { useCredentialAutoStartStop } from '@/features/accounts/hooks/useCredentialAutoStartStop';
import styles from './CredentialAutoStartStopToggle.module.scss';

interface CredentialAutoStartStopToggleProps {
  managerServiceBase: string;
  managementKey: string;
}

/**
 * SPEC13 credential toolbar switch. The label is strictly “自动启停” and the
 * control reflects the real persisted provider-quota auto start/stop state.
 * OFF pauses automatic disable/recovery without re-enabling historical
 * credentials; ON resumes quota checks. Load / save failures and the
 * environment lock are surfaced honestly instead of showing a stale ON/OFF.
 */
export function CredentialAutoStartStopToggle({
  managerServiceBase,
  managementKey,
}: CredentialAutoStartStopToggleProps) {
  const { t } = useTranslation();
  const { status, enabled, locked, saving, loadError, saveError, setEnabled } =
    useCredentialAutoStartStop(managerServiceBase, managementKey);

  const available = Boolean(managerServiceBase && managementKey);
  const disabled = !available || status !== 'ready' || saving || locked;

  const renderHint = () => {
    if (!available) return t('accounts.auto_start_stop_unavailable');
    if (status === 'loading') return t('accounts.auto_start_stop_loading');
    if (status === 'error') {
      return t('accounts.auto_start_stop_load_failed', { message: loadError });
    }
    if (locked) return t('accounts.auto_start_stop_locked_hint');
    return t(enabled ? 'accounts.auto_start_stop_on_hint' : 'accounts.auto_start_stop_off_hint');
  };

  return (
    <div className={styles.control} data-credential-auto-start-stop={status}>
      <ToggleSwitch
        checked={enabled}
        onChange={(value) => void setEnabled(value)}
        disabled={disabled}
        ariaLabel={t('accounts.auto_start_stop_aria')}
        label={t('accounts.auto_start_stop')}
        labelPosition="left"
      />
      {saveError ? (
        <span className={styles.error} role="alert">
          {t('accounts.auto_start_stop_save_failed', { message: saveError })}
        </span>
      ) : (
        <InfoTooltip
          content={renderHint()}
          ariaLabel={t('accounts.auto_start_stop_tooltip_aria')}
          className={styles.help}
        />
      )}
    </div>
  );
}
