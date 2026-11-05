import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  QUOTA_REFRESH_INTERVALS,
  readQuotaRefreshIntervalMs,
  writeQuotaRefreshIntervalMs,
} from '@/utils/quota/refreshCadence';

const LABELS: Record<(typeof QUOTA_REFRESH_INTERVALS)[number], string> = {
  0: 'config_management.quota_refresh_manual',
  300000: 'config_management.quota_refresh_5m',
  900000: 'config_management.quota_refresh_15m',
  3600000: 'config_management.quota_refresh_1h',
  21600000: 'config_management.quota_refresh_6h',
};

export function QuotaRefreshIntervalControl() {
  const { t } = useTranslation();
  const [value, setValue] = useState(readQuotaRefreshIntervalMs);
  return (
    <label style={{ display: 'inline-flex', alignItems: 'center', gap: 8, marginBottom: 12 }}>
      <span>{t('config_management.quota_refresh_interval')}</span>
      <select
        aria-label={t('config_management.quota_refresh_interval')}
        value={value}
        onChange={(event) => {
          const next = Number(event.target.value);
          writeQuotaRefreshIntervalMs(next);
          setValue(next);
        }}
      >
        {QUOTA_REFRESH_INTERVALS.map((interval) => (
          <option key={interval} value={interval}>
            {t(LABELS[interval])}
          </option>
        ))}
      </select>
    </label>
  );
}
