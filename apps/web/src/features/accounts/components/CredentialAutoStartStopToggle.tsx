import { Button } from '@/components/ui/Button';
import { useTranslation } from 'react-i18next';
import { ToggleSwitch } from '@/components/ui/ToggleSwitch';
import { useCredentialAutoStartStop } from '@/features/accounts/hooks/useCredentialAutoStartStop';
import styles from './CredentialAutoStartStopToggle.module.scss';

interface CredentialAutoStartStopToggleProps {
  managerServiceBase: string;
  managementKey: string;
  authIndex?: string;
  revision?: string | number;
}

/** Per-issued-subject intent. Own gate receipts are NOT effective availability. */
export function CredentialAutoStartStopToggle({
  managerServiceBase,
  managementKey,
  authIndex,
  revision,
}: CredentialAutoStartStopToggleProps) {
  const { t } = useTranslation();
  const { data, busy, error, setEnabled, resume, refresh } = useCredentialAutoStartStop(
    managerServiceBase,
    managementKey,
    authIndex,
    revision
  );
  return (
    <section
      className={styles.control}
      data-credential-auto-start-stop={busy ? 'pending' : data ? 'ready' : 'unavailable'}
    >
      <ToggleSwitch
        checked={data?.enabled ?? false}
        disabled={
          busy ||
          !data ||
          (!data.enabled && (!data.bindingCurrent || data.availability.state !== 'available'))
        }
        onChange={(v) => void setEnabled(v)}
        label={t('accounts.auto_start_stop')}
        labelPosition="left"
        ariaLabel={`${t('accounts.auto_start_stop')} ${authIndex ?? ''}`}
      />
      <div aria-live="polite">
        {busy && <span>{t('accounts.auto_start_stop_loading')}</span>}
        {data && (
          <>
            <small>
              {t(data.enabled ? 'credential_policy.saved_on' : 'credential_policy.saved_off')}
            </small>
            <details>
              <summary>{t('credential_policy.details')}</summary>
              {data && (
                <Button
                  variant="secondary"
                  size="xs"
                  type="button"
                  disabled={busy || !data.bindingCurrent}
                  onClick={() => void refresh()}
                >
                  {t('credential_policy.refresh')}
                </Button>
              )}
              <small>
                {' '}
                · {data.availability.state}: {data.availability.reason} · {data.reason}
              </small>
              <small>
                {' '}
                ·{' '}
                {data.manualDisabled
                  ? t('credential_policy.native_disabled')
                  : t('credential_policy.other_unknown')}
              </small>
              {data.legacyMigration && (
                <small>
                  {' '}
                  {t('credential_policy.migration', data.legacyMigration)} ·{' '}
                  {t('credential_policy.no_guess')}
                </small>
              )}
              {data.operation && (
                <small>
                  {' '}
                  {t('credential_policy.own_action', { ...data.operation })}
                  {t(
                    data.operation.currentApplied
                      ? 'credential_policy.applied_current'
                      : 'credential_policy.applied_unverified'
                  )}
                </small>
              )}
              {!data.enabled && data.operation?.action === 'pause' && (
                <Button
                  variant="secondary"
                  size="xs"
                  type="button"
                  disabled={busy || !data.bindingCurrent || data.availability.state !== 'available'}
                  onClick={() => void resume()}
                >
                  {t('credential_policy.resume')}
                </Button>
              )}
            </details>
          </>
        )}
        {!data && !busy && <small>{t('accounts.auto_start_stop_unavailable')}</small>}
        {error && (
          <span className={styles.error} role="alert">
            {error}
            {error === 'fresh_authoritative_evidence_required'
              ? t('credential_policy.refresh_required')
              : ''}
          </span>
        )}
      </div>
    </section>
  );
}
