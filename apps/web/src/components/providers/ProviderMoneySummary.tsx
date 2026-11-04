import { useTranslation } from 'react-i18next';
import type { ProviderMoneyView } from './useProviderMoneySummaries';
import { providerPolicyPresentationLines } from './providerPolicyPresentation';
import styles from './ProviderMoneySummary.module.scss';

export function ProviderMoneySummary({ view }: { view?: ProviderMoneyView }) {
  const { t } = useTranslation();
  const data = view?.data;
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
        providerPolicyPresentationLines(data, t).map((line, index) => (
          <div key={index}>{line}</div>
        ))
      )}
    </div>
  );
}
