import { useEffect, useId, useSyncExternalStore } from 'react';
import { useTranslation } from 'react-i18next';
import { Select } from '@/components/ui/Select';
import {
  QUOTA_REFRESH_INTERVALS,
  writeQuotaRefreshIntervalMs,
} from '@/utils/quota/refreshCadence';
import { loadRuntimeControls, readRuntimeControls, subscribeRuntimeControls } from '@/utils/quota/runtimeControls';
import styles from './VisualConfigEditor.module.scss';

const LABELS: Record<(typeof QUOTA_REFRESH_INTERVALS)[number], string> = {
  0: 'config_management.quota_refresh_manual',
  300000: 'config_management.quota_refresh_5m',
  900000: 'config_management.quota_refresh_15m',
  3600000: 'config_management.quota_refresh_1h',
  21600000: 'config_management.quota_refresh_6h',
};

export function QuotaRefreshIntervalControl({ disabled = false }: { disabled?: boolean }) {
  const { t } = useTranslation();
  const labelId = useId();
  const value = useSyncExternalStore(
    subscribeRuntimeControls,
    () => readRuntimeControls().quotaRefreshIntervalMs,
    () => 3_600_000
  );
  useEffect(() => {
    void loadRuntimeControls();
  }, []);
  return (
    <div className={styles.toggleRow}>
      <div id={labelId} className={styles.toggleTitle}>
        {t('config_management.quota_refresh_interval')}
      </div>
      <Select
        id={`${labelId}-select`}
        value={String(value)}
        disabled={disabled}
        ariaLabelledBy={labelId}
        options={QUOTA_REFRESH_INTERVALS.map((interval) => ({
          value: String(interval),
          label: t(LABELS[interval]),
        }))}
        onChange={(next) => {
          writeQuotaRefreshIntervalMs(Number(next));
        }}
      />
    </div>
  );
}
