import { useTranslation } from 'react-i18next';
import type { ProviderMoneyView } from './useProviderMoneySummaries';
import styles from './ProviderMoneySummary.module.scss';

const timeText = (ms?: number) =>
  typeof ms === 'number' && Number.isFinite(ms) && ms > 0 && !Number.isNaN(new Date(ms).getTime())
    ? new Date(ms).toISOString()
    : 'unknown';
const moneyText = (currency: string, total: string, unknown: string) =>
  /^[A-Z]{3}$/.test(currency) &&
  typeof total === 'string' &&
  /^-?(0|[1-9]\d{0,59})(\.\d{1,30})?$/.test(total)
    ? `${currency} ${total}`
    : unknown;

export function ProviderMoneySummary({ view }: { view?: ProviderMoneyView }) {
  const { t } = useTranslation();
  const data = view?.data;
  const funding = data?.funding;
  return (
    <div
      className={styles.summary}
      aria-label={t('ai_providers.money_summary_aria')}
      aria-live="polite"
    >
      {view?.pending && <div role="status">{t('ai_providers.money_summary_pending')}</div>}
      {view?.failed && <div role="status">{t('ai_providers.money_summary_failed')}</div>}
      {!data ? (
        <span>{t('ai_providers.money_summary_unknown')}</span>
      ) : (
        <>
          <div>
            {data.enabled
              ? t('ai_providers.money_summary_enabled')
              : t('ai_providers.money_summary_disabled')}{' '}
            ·{' '}
            {data.source === 'explicit'
              ? t('ai_providers.money_summary_explicit')
              : t('ai_providers.money_summary_default_off')}{' '}
            ·{' '}
            {data.locked
              ? t('ai_providers.money_summary_locked')
              : t('ai_providers.money_summary_unlocked')}
          </div>
          <div>
            {data.effective
              ? t('ai_providers.money_summary_effective')
              : t('ai_providers.money_summary_not_ready')}{' '}
            ·{' '}
            {data.ownPaused && data.applied
              ? t('ai_providers.money_summary_own_paused')
              : data.applied
                ? t('ai_providers.money_summary_own_recovered')
                : t('ai_providers.money_summary_unconfirmed')}
          </div>
          <div>
            {data.manualDisabled
              ? t('ai_providers.money_summary_manual_disabled')
              : t('ai_providers.money_summary_manual_open')}{' '}
            · {data.state} · {data.reason}
          </div>
          <div>{t('ai_providers.money_summary_updated', { time: timeText(data.updatedAtMs) })}</div>
          {(!funding || funding.length === 0) && (
            <div>
              {t('ai_providers.money_summary_amount_unknown')} ·{' '}
              {t('ai_providers.money_summary_source_unverified')}
            </div>
          )}
          {funding?.map((source) => (
            <div key={source.memberIds.join(',')}>
              <div>
                {t('ai_providers.money_summary_source', { id: source.memberIds.join(',') })} ·{' '}
                {source.reason} ·{' '}
                {source.fresh
                  ? t('ai_providers.money_summary_current')
                  : t('ai_providers.money_summary_stale')}
              </div>
              <div>
                {t('ai_providers.money_summary_last_success', {
                  time: timeText(source.observedAtMs),
                })}
              </div>
              {source.balances?.length
                ? source.balances.map((b, i) => (
                    <div key={i}>
                      {moneyText(b.currency, b.total, t('ai_providers.money_summary_amount_unknown'))}
                    </div>
                  ))
                : (
                  <div>{t('ai_providers.money_summary_amount_unknown')}</div>
                )}
            </div>
          ))}
        </>
      )}
    </div>
  );
}
