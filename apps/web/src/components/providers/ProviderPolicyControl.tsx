import type { BalanceBinding } from '@/services/api/officialBalance';
import type { ProviderRow } from './ProviderTable/rowData';
import { useProviderPolicy } from './useProviderPolicy';
import { useTranslation } from 'react-i18next';
import styles from './ProviderPolicyControl.module.scss';
import { useId, type ReactNode } from 'react';

export interface ProviderPolicyConnection {
  base: string;
  managementKey: string;
  disabled?: boolean;
  revision?: number;
  onChanged?: () => void;
}

export interface ProviderPolicyControlProps {
  connection?: ProviderPolicyConnection;
  row: ProviderRow;
  binding?: BalanceBinding;
  disabled?: boolean;
  compact?: boolean;
  children?: ReactNode;
}

export function ProviderPolicyControl(props: ProviderPolicyControlProps) {
  const { t } = useTranslation();
  const {
    data,
    busy,
    error,
    locked,
    toggle,
    refresh,
    recover,
    requestRecoveryConfirmation,
    cancelRecoveryConfirmation,
    confirmationCurrent,
    canRead,
    writeLocked,
  } = useProviderPolicy(props);
  const id = useId();
  const state = data?.state ?? 'unavailable';
  const unavailable = !data && !busy;
  const timestamp =
    data && data.updatedAtMs > 0 && data.updatedAtMs <= 8640000000000000
      ? new Date(data.updatedAtMs).toISOString()
      : undefined;
  const label = data?.enabled ? t('provider_policy.on') : t('provider_policy.off');
  return (
    <div className={`${styles.root} ${props.compact ? '' : styles.drawer}`}>
      <span className={styles.heading}>
        <button
          id={`${id}-switch`}
          type="button"
          role="switch"
          className={styles.indicator}
          data-state={state}
          aria-label={`${t('provider_policy.title')}: ${props.row.label}`}
          aria-describedby={id}
          aria-checked={data?.enabled ?? false}
          disabled={busy || writeLocked || unavailable || (!data?.enabled && locked)}
          onKeyDown={(event) => {
            if (event.key === 'Enter' || event.key === ' ') event.stopPropagation();
          }}
          onClick={(event) => {
            event.stopPropagation();
            void toggle();
          }}
        >
          {busy
            ? t('provider_policy.loading')
            : unavailable
              ? t('provider_policy.unavailable')
              : label}
        </button>
        {data?.ownPaused && data.applied ? (
          <span
            className={styles.autoMarker}
            role="status"
            data-auto-pause-marker="applied"
            aria-label={t('provider_policy.auto_paused_marker_aria', {
              name: props.row.label,
            })}
            title={t('provider_policy.auto_paused_marker_hint')}
          >
            {t('provider_policy.auto_paused_marker')}
          </span>
        ) : data?.state === 'pending' ? (
          <span
            className={styles.autoMarker}
            role="status"
            data-auto-pause-marker="pending"
            aria-label={t('provider_policy.auto_pending_marker_aria', {
              name: props.row.label,
            })}
            title={t('provider_policy.auto_pending_marker_hint')}
          >
            {t('provider_policy.auto_pending_marker')}
          </span>
        ) : null}
        {props.children ?? <span>{props.row.label}</span>}
      </span>
      <div id={id} className={styles.details} aria-live="polite">
        {unavailable ? (
          <span className={styles.unavailable}>
            {t(canRead ? 'provider_policy.state_unavailable' : 'provider_policy.no_parent_proof')}
          </span>
        ) : data ? (
          <>
            <span>
              {t('provider_policy.saved')}:{' '}
              {data.enabled ? t('provider_policy.on') : t('provider_policy.off')}
            </span>
            <span>
              {t('provider_policy.effective')}:{' '}
              {data.effective ? t('provider_policy.on') : t('provider_policy.off')}
            </span>
            <span>
              {t('provider_policy.state')}: {t(`provider_policy.states.${data.state}`)}
            </span>
            <span data-policy-locked={locked}>
              {t('provider_policy.locked')}: {locked ? t('common.yes') : t('common.no')}
            </span>
            <span>
              {t('provider_policy.manual')}:{' '}
              {data.manualDisabled
                ? t('provider_policy.manual_off')
                : t('provider_policy.manual_on')}
            </span>
            <span>
              {t('provider_policy.own_pause')}: {data.ownPaused ? t('common.yes') : t('common.no')}
            </span>
            <details
              className={styles.technical}
              open={!props.compact}
              onClick={(event) => event.stopPropagation()}
              onKeyDown={(event) => {
                if (event.key === 'Enter' || event.key === ' ') event.stopPropagation();
              }}
            >
              <summary>{t('provider_policy.details')}</summary>
              <span>
                {t('provider_policy.source')}:{' '}
                {t(
                  `provider_policy.sources.${data.source === 'explicit' || data.source === 'default_off' ? data.source : 'unknown'}`
                )}
              </span>
              <span>
                {t('provider_policy.applied')}:{' '}
                {data.applied ? t('provider_policy.confirmed') : t('provider_policy.unconfirmed')}
              </span>
              <span>
                {t('provider_policy.binding')}:{' '}
                {t(data.bindingCurrent ? 'provider_policy.current' : 'provider_policy.changed')}
              </span>
              <span>
                {t('provider_policy.updated')}:{' '}
                <time dateTime={timestamp}>{timestamp ?? t('provider_policy.never')}</time>
              </span>
              <span>
                {t('provider_policy.scope')}: {data.providerId} · {data.targets.length - 1}{' '}
                {t('provider_policy.members')}
              </span>
              <span>
                {t('provider_policy.reason')}:{' '}
                {/^[a-z][a-z0-9_]{0,95}$/.test(data.reason)
                  ? data.reason
                  : t('provider_policy.unknown_reason')}
              </span>
              {!data.enabled && data.ownPaused && (
                <span>{t('provider_policy.off_retains_pause')}</span>
              )}
            </details>
            {!data.enabled && data.ownPaused && (
              <div className={styles.recovery}>
                <button
                  type="button"
                  disabled={busy || writeLocked || !data.recoveryAvailable}
                  onKeyDown={(event) => {
                    if (event.key === 'Enter' || event.key === ' ') event.stopPropagation();
                  }}
                  onClick={(event) => {
                    event.stopPropagation();
                    requestRecoveryConfirmation();
                  }}
                >
                  {t('provider_policy.recover')}
                </button>
                {confirmationCurrent && (
                  <div role="alertdialog" aria-label={t('provider_policy.recover_confirm_title')}>
                    <p>{t('provider_policy.recover_confirm_body')}</p>
                    <button
                      type="button"
                      disabled={busy || writeLocked || !data.recoveryAvailable}
                      onClick={(event) => {
                        event.stopPropagation();
                        void recover();
                      }}
                    >
                      {t('provider_policy.recover_confirm')}
                    </button>
                    <button
                      type="button"
                      onClick={(event) => {
                        event.stopPropagation();
                        cancelRecoveryConfirmation();
                      }}
                    >
                      {t('provider_policy.recover_cancel')}
                    </button>
                  </div>
                )}
              </div>
            )}
          </>
        ) : null}
      </div>
      {error && (
        <span className={styles.error} role="alert">
          {t(`provider_policy.errors.${error}`)}
        </span>
      )}
      <button
        type="button"
        className={styles.readStatus}
        aria-label={t('provider_policy.read_status')}
        onKeyDown={(event) => {
          if (event.key === 'Enter' || event.key === ' ') event.stopPropagation();
        }}
        disabled={busy || !canRead}
        onClick={(event) => {
          event.stopPropagation();
          refresh();
        }}
      >
        {t('provider_policy.read_status')}
      </button>
      {data && (
        <details
          className={styles.funding}
          onClick={(event) => event.stopPropagation()}
          onKeyDown={(event) => {
            if (event.key === 'Enter' || event.key === ' ') event.stopPropagation();
          }}
        >
          <summary>{t('provider_policy.funding')}</summary>
          <span>{t('provider_policy.per_source_only')}</span>
          {data.funding?.length ? (
            data.funding.map((source, index) => (
              <span className={styles.fundingSource} key={index}>
                <span>
                  {t('provider_policy.scope')}:{' '}
                  {source.memberIds.join(', ') || t('provider_policy.unverified')}
                </span>
                <span>
                  {t(source.fresh ? 'provider_policy.fresh' : 'provider_policy.stale')} ·{' '}
                  {t(
                    source.keyMatched ? 'provider_policy.key_matched' : 'provider_policy.unverified'
                  )}
                </span>
                {source.balances?.length ? (
                  source.balances.map((balance, balanceIndex) => (
                    <span key={balanceIndex}>
                      {balance.currency} {balance.total}
                    </span>
                  ))
                ) : (
                  <span>{t('provider_policy.funding_unknown')}</span>
                )}
                <span>
                  {t('provider_policy.last_success')}:{' '}
                  {source.observedAtMs ? (
                    <time dateTime={new Date(source.observedAtMs).toISOString()}>
                      {new Date(source.observedAtMs).toISOString()}
                    </time>
                  ) : (
                    t('provider_policy.never')
                  )}
                </span>
                <span>
                  {t('provider_policy.reason')}:{' '}
                  {/^[a-z][a-z0-9_]{0,95}$/.test(source.reason)
                    ? source.reason
                    : t('provider_policy.unknown_reason')}
                </span>
              </span>
            ))
          ) : (
            <span>{t('provider_policy.funding_unknown')}</span>
          )}
        </details>
      )}
    </div>
  );
}
