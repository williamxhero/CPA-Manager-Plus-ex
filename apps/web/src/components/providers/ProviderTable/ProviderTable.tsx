import {
  useEffect,
  useRef,
  useState,
  useSyncExternalStore,
  type ChangeEvent,
  type KeyboardEvent,
  type MouseEvent,
  type ReactNode,
} from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { ToggleSwitch } from '@/components/ui/ToggleSwitch';
import {
  IconCheck,
  IconEye,
  IconPencil,
  IconPower,
  IconTrash2,
  IconX,
} from '@/components/ui/icons';
import type { ResourceProtectionCapability, ResourceProtectionEvidence } from '@/features/resourceProtection/resourceProtection';
import type { ProviderPolicyConnection } from '../ProviderPolicyControl';
import {
  providerAutoId,
  readProviderAutoUsage,
  SERVER_PROVIDER_AUTO_USAGE,
  subscribeProviderAutoUsage,
  writeProviderAutoUsage,
} from '../providerAutoUsage';
import { ProviderMoneySummary } from '../ProviderMoneySummary';
import {
  useProviderMoneySummaries,
  type ProviderMoneyStatusConfig,
} from '../useProviderMoneySummaries';
import { useOfficialBalanceList } from '../useOfficialBalanceList';
import { apiClient } from '@/services/api/client';
import { loadRuntimeControls } from '@/utils/quota/runtimeControls';
import { ProviderStatusBar } from '../ProviderStatusBar';
import { getProviderKindIcon, PROVIDER_KIND_LABELS } from './kindMeta';
import type { ProviderRow } from './rowData';
import styles from './ProviderTable.module.scss';

interface ProviderTableProps {
  rows: ProviderRow[];
  moneyStatus?: ProviderMoneyStatusConfig;
  resourceProtection?: ResourceProtectionCapability;
  resourceProtectionForRow?: (row: ProviderRow) => ResourceProtectionEvidence;
  providerPolicyConnection?: ProviderPolicyConnection;
  loading: boolean;
  actionsDisabled: boolean;
  toggleDisabled: boolean;
  resolvedTheme: string;
  emptyState: ReactNode;
  onShowDetail: (row: ProviderRow) => void;
  onShowBalance?: (row: ProviderRow) => void;
  onEdit: (row: ProviderRow) => void;
  onDelete: (row: ProviderRow) => void;
  onToggle: (row: ProviderRow, enabled: boolean) => void;
  onPriorityChange: (row: ProviderRow, priority: number) => void;
}

const stopPropagation = (event: MouseEvent) => {
  event.stopPropagation();
};

const priorityToDraft = (priority: number | undefined) =>
  Number.isFinite(priority) ? String(Math.trunc(priority ?? 0)) : '';

interface PriorityControlProps {
  row: ProviderRow;
  disabled: boolean;
  editLabel: string;
  onPriorityChange: (row: ProviderRow, priority: number) => void;
}

function PriorityControl({
  row,
  disabled,
  editLabel,
  onPriorityChange,
}: PriorityControlProps) {
  const [draft, setDraft] = useState(() => priorityToDraft(row.priority));
  const [isEditing, setIsEditing] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);
  const skipNextBlurCommitRef = useRef(false);
  const currentPriority = Number.isFinite(row.priority) ? Math.trunc(row.priority ?? 0) : 0;
  const displayPriority = priorityToDraft(row.priority) || '—';

  useEffect(() => {
    if (!isEditing) return;
    inputRef.current?.focus();
    inputRef.current?.select();
  }, [isEditing]);

  const commitDraft = () => {
    if (skipNextBlurCommitRef.current) {
      skipNextBlurCommitRef.current = false;
      return;
    }

    const trimmed = draft.trim();
    if (!trimmed) {
      setDraft(priorityToDraft(row.priority));
      setIsEditing(false);
      return;
    }

    const parsed = Number(trimmed);
    if (!Number.isFinite(parsed)) {
      setDraft(priorityToDraft(row.priority));
      setIsEditing(false);
      return;
    }

    const nextPriority = Math.trunc(parsed);
    setDraft(String(nextPriority));
    setIsEditing(false);
    if (nextPriority !== currentPriority || row.priority === undefined) {
      onPriorityChange(row, nextPriority);
    }
  };

  const startEditing = () => {
    if (disabled) return;
    skipNextBlurCommitRef.current = false;
    setDraft(priorityToDraft(row.priority));
    setIsEditing(true);
  };

  const handleInputChange = (event: ChangeEvent<HTMLInputElement>) => {
    setDraft(event.target.value);
  };

  const handleInputKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'Enter') {
      event.preventDefault();
      commitDraft();
      skipNextBlurCommitRef.current = true;
      event.currentTarget.blur();
    } else if (event.key === 'Escape') {
      event.preventDefault();
      skipNextBlurCommitRef.current = true;
      setDraft(priorityToDraft(row.priority));
      setIsEditing(false);
      event.currentTarget.blur();
    }
  };

  return (
    <div className={styles.priorityControl} onClick={stopPropagation}>
      {isEditing ? (
        <input
          ref={inputRef}
          className={styles.priorityInput}
          type="number"
          step={1}
          inputMode="numeric"
          value={draft}
          placeholder="—"
          disabled={disabled}
          aria-label={editLabel}
          title={editLabel}
          onChange={handleInputChange}
          onBlur={commitDraft}
          onKeyDown={handleInputKeyDown}
        />
      ) : (
        <button
          type="button"
          className={styles.priorityValueButton}
          disabled={disabled}
          aria-label={editLabel}
          title={editLabel}
          onClick={startEditing}
        >
          {displayPriority}
        </button>
      )}
    </div>
  );
}

export function ProviderTable({
  rows,
  moneyStatus,
  loading,
  actionsDisabled,
  toggleDisabled,
  resolvedTheme,
  emptyState,
  onShowDetail,
  onShowBalance,
  onEdit,
  onDelete,
  onToggle,
  onPriorityChange,
}: ProviderTableProps) {
  const { t } = useTranslation();
  const money = useProviderMoneySummaries(rows, moneyStatus);
  const officialBalances = useOfficialBalanceList(rows, moneyStatus);
  const autoUsage = useSyncExternalStore(
    subscribeProviderAutoUsage,
    readProviderAutoUsage,
    () => SERVER_PROVIDER_AUTO_USAGE
  );
  const hasRemainingQuota = (key: string) => {
    const official = officialBalances[key];
    const summary = money[key];
    const lines = official?.text
      ? [official.text]
      : (summary?.data?.funding ?? []).flatMap((source) =>
          (source.balances ?? []).flatMap((balance) =>
            balance.currency && balance.total ? [`${balance.currency} ${balance.total}`] : []
          )
        );
    if (lines.length === 0 || ((official?.pending || summary?.pending) && !official?.text)) return false;
    const text = lines.join(' ');
    if (/unlimited/i.test(text)) return true;
    const numbers = text.match(/-?\d+(?:\.\d+)?/g);
    return !!numbers && numbers.some((value) => Number(value) > 0);
  };
  const participating = (row: ProviderRow) =>
    autoUsage.enabled && (autoUsage.ids.length === 0 || autoUsage.ids.includes(providerAutoId(row)));
  const sentHold = useRef<Record<string, boolean>>({});
  const holdRows = useRef(rows);
  const holdBalances = useRef(officialBalances);
  holdRows.current = rows;
  holdBalances.current = officialBalances;
  const holdSignature = [
    autoUsage.enabled ? '1' : '0',
    autoUsage.ids.join(','),
    rows
      .map((row) => {
        const view = officialBalances[row.key];
        return `${providerAutoId(row)}:${row.raw.autoHeld === true ? 1 : 0}:${view?.pending ? 1 : 0}:${view?.text ?? ''}`;
      })
      .join('|'),
  ].join(';');
  useEffect(() => {
    let cancelled = false;
    const paths: Record<ProviderRow['kind'], string> = {
      gemini: '/gemini-api-key',
      interactions: '/interactions-api-key',
      codex: '/codex-api-key',
      xai: '/xai-api-key',
      meta: '/meta-api-key',
      claude: '/claude-api-key',
      vertex: '/vertex-api-key',
      openai: '/openai-compatibility',
    };
    const snapshotRows = holdRows.current;
    const balances = holdBalances.current;
    const usage = readProviderAutoUsage();
    const textIsZero = (key: string) => {
      const text = balances[key]?.text ?? '';
      if (!text || /unlimited/i.test(text)) return false;
      const numbers = text.match(/-?\d+(?:\.\d+)?/g);
      return !!numbers && numbers.length > 0 && numbers.every((value) => Number(value) === 0);
    };
    void (async () => {
      await loadRuntimeControls();
      if (cancelled) return;
      for (const row of snapshotRows) {
        if (cancelled) return;
        const id = providerAutoId(row);
        const rowParticipating = usage.enabled && (usage.ids.length === 0 || usage.ids.includes(id));
        const balance = balances[row.key];
        if (rowParticipating && (!balance || balance.pending)) continue;
        const held = rowParticipating && textIsZero(row.key);
        const rawHeld = row.raw.autoHeld === true;
        if ((sentHold.current[id] ?? rawHeld) === held) continue;
        sentHold.current[id] = held;
        try {
          await apiClient.patch(paths[row.kind], { index: row.originalIndex, value: { 'auto-held': held } });
        } catch {
          if (!cancelled) sentHold.current[id] = rawHeld;
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [holdSignature]);

  if (loading && rows.length === 0) {
    return <div className="hint">{t('common.loading')}</div>;
  }

  if (rows.length === 0) {
    return <>{emptyState}</>;
  }

  const handleRowKeyDown = (event: KeyboardEvent<HTMLDivElement>, row: ProviderRow) => {
    if (event.target !== event.currentTarget) return;
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      onShowDetail(row);
    }
  };

  return (
    <div className={styles.table} role="table" aria-label={t('ai_providers.table_aria_label')}>
      <div className={`${styles.headerRow}`} role="row">
        <span role="columnheader">{t('ai_providers.table_col_type')}</span>
        <span role="columnheader">{t('ai_providers.table_col_identity')}</span>
        <span role="columnheader">{t('ai_providers.table_col_balance')}</span>
        <span role="columnheader">{t('common.base_url')}</span>
        <span role="columnheader" className={styles.cellModelsHeader}>
          {t('ai_providers.table_col_models')}
        </span>
        <span role="columnheader" className={styles.cellPriorityHeader}>
          {t('common.priority')}
        </span>
        <span role="columnheader" className={styles.cellRecentHeader}>
          {t('ai_providers.table_col_recent')}
        </span>
        <span role="columnheader">{t('ai_providers.config_toggle_label')}</span>
        <span role="columnheader">{t('ai_providers.auto_start_stop')}</span>
        <span role="columnheader" className={styles.cellActionsHeader}>
          {t('ai_providers.table_col_actions')}
        </span>
      </div>

      {rows.map((row) => {
        const kindLabel = PROVIDER_KIND_LABELS[row.kind];
        return (
          <div
            key={row.key}
            role="row"
            tabIndex={0}
            className={`${styles.row} ${row.enabled ? '' : styles.rowDisabled}`}
            onClick={() => onShowDetail(row)}
            onKeyDown={(event) => handleRowKeyDown(event, row)}
            aria-label={`${kindLabel} ${row.label}`}
          >
            <div className={styles.cellType} role="cell">
              <span className={styles.kindIconFrame} aria-hidden="true">
                <img
                  src={getProviderKindIcon(row.kind, resolvedTheme)}
                  alt=""
                  className={`${styles.kindIcon} ${
                    row.kind === 'codex'
                      ? styles.kindIconCodex
                      : row.kind === 'openai'
                        ? styles.kindIconOpenai
                        : ''
                  }`}
                />
              </span>
              <span className={styles.kindLabel}>{kindLabel}</span>
            </div>

            <div className={styles.cellIdentity} role="cell">
              <div className={styles.identityMain}>
                <span className={styles.identityLabel} title={row.label}>
                  {row.label}
                </span>
                {row.kind === 'openai' && (
                  <span className={styles.identityMeta}>
                    {t('ai_providers.openai_keys_count')}: {row.keyCount}
                  </span>
                )}
              </div>
            </div>

            <div
              className={styles.cellBalance}
              role="cell"
              onClick={(event) => {
                event.stopPropagation();
                (onShowBalance ?? onShowDetail)(row);
              }}
            >
              <ProviderMoneySummary view={money[row.key]} official={officialBalances[row.key]} />
            </div>

            <div className={styles.cellUrl} role="cell" title={row.baseUrl || undefined}>
              {row.baseUrl || '—'}
            </div>

            <div className={`${styles.cellModels} ${styles.cellNumeric}`} role="cell">
              <span className={styles.cellCaption}>{t('ai_providers.table_col_models')}</span>
              {row.modelCount}
            </div>

            <div className={`${styles.cellPriority} ${styles.cellNumeric}`} role="cell">
              <span className={styles.cellCaption}>{t('common.priority')}</span>
              <PriorityControl
                key={`${row.key}:${row.priority ?? 'unset'}`}
                row={row}
                disabled={actionsDisabled}
                editLabel={t('ai_providers.priority_edit')}
                onPriorityChange={onPriorityChange}
              />
            </div>

            <div className={styles.cellRecent} role="cell">
              {row.usageStatsCoverage === 'unavailable' ? (
                <span className={styles.noRecent}>{t('ai_providers.usage_stats_unavailable')}</span>
              ) : row.stats.success + row.stats.failure > 0 ? (
                <>
                  <span className={styles.statSuccess} title={t('stats.success')}>
                    <IconCheck size={12} /> {row.stats.success}
                  </span>
                  <span className={styles.statFailure} title={t('stats.failure')}>
                    <IconX size={12} /> {row.stats.failure}
                  </span>
                  <div className={styles.recentBarWrap}>
                    <ProviderStatusBar statusData={row.statusData} />
                  </div>
                  {row.usageStatsCoverage === 'partial' && (
                    <span className={styles.noRecent}>{t('ai_providers.usage_stats_partial')}</span>
                  )}
                </>
              ) : (
                <span className={styles.noRecent}>
                  {row.usageStatsCoverage === 'partial'
                    ? t('ai_providers.usage_stats_partial')
                    : t('status_bar.no_requests')}
                </span>
              )}
            </div>

            <div className={styles.cellToggle} role="cell" onClick={stopPropagation}>
              <ToggleSwitch
                ariaLabel={t('ai_providers.config_toggle_label')}
                checked={row.enabled}
                disabled={toggleDisabled}
                onChange={(value) => onToggle(row, value)}
              />
            </div>

            <div className={styles.cellAuto} role="cell" onClick={stopPropagation}>
              <Button
                variant="secondary"
                size="sm"
                iconOnly
                onClick={() => {
                  const id = providerAutoId(row);
                  if (!participating(row)) {
                    writeProviderAutoUsage({
                      enabled: true,
                      ids: autoUsage.enabled ? [...autoUsage.ids, id] : [id],
                    });
                    return;
                  }
                  const nextIds =
                    autoUsage.ids.length === 0
                      ? rows.filter((item) => providerAutoId(item) !== id).map((item) => providerAutoId(item))
                      : autoUsage.ids.filter((item) => item !== id);
                  writeProviderAutoUsage({ enabled: autoUsage.enabled, ids: nextIds });
                }}
                disabled={actionsDisabled}
                aria-pressed={participating(row)}
                aria-label={t('ai_providers.auto_start_stop')}
                data-provider-auto-start-stop={participating(row) ? 'on' : 'off'}
                data-auto-quota={
                  participating(row) ? (hasRemainingQuota(row.key) ? 'present' : 'absent') : undefined
                }
              >
                <IconPower size={15} />
              </Button>
            </div>

            <div className={styles.cellActions} role="cell" onClick={stopPropagation}>
              <Button
                variant="secondary"
                size="xs"
                iconOnly
                onClick={() => onEdit(row)}
                disabled={actionsDisabled}
                aria-label={t('common.edit')}
                title={t('common.edit')}
              >
                <IconPencil size={14} />
              </Button>
              <Button
                variant="danger"
                size="xs"
                iconOnly
                onClick={() => onDelete(row)}
                disabled={actionsDisabled}
                aria-label={t('common.delete')}
                title={t('common.delete')}
              >
                <IconTrash2 size={14} />
              </Button>
              <Button
                variant="secondary"
                size="xs"
                iconOnly
                onClick={() => onShowDetail(row)}
                disabled={actionsDisabled}
                aria-label={t('ai_providers.detail_button')}
                title={t('ai_providers.detail_button')}
              >
                <IconEye size={14} />
              </Button>
            </div>
          </div>
        );
      })}
    </div>
  );
}
