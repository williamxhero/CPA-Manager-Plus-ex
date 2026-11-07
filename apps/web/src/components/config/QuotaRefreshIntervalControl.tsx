import { useId } from 'react';
import { useTranslation } from 'react-i18next';
import { Input } from '@/components/ui/Input';
import styles from './VisualConfigEditor.module.scss';

export function QuotaRefreshIntervalControl({
  value,
  disabled = false,
  error,
  onChange,
}: {
  value: string;
  disabled?: boolean;
  error?: string;
  onChange: (value: string) => void;
}) {
  const { t } = useTranslation();
  const labelId = useId();

  return (
    <div className={styles.toggleRow}>
      <div id={labelId} className={styles.toggleTitle}>
        {t('config_management.quota_refresh_interval')}
      </div>
      <Input
        id={`${labelId}-input`}
        aria-labelledby={labelId}
        type="number"
        min="0"
        step="any"
        inputMode="decimal"
        value={value}
        disabled={disabled}
        onChange={(event) => onChange(event.target.value)}
        rightElement={t('config_management.quota_refresh_minutes_unit')}
        hint={t('config_management.quota_refresh_interval_hint')}
        error={error}
      />
    </div>
  );
}
