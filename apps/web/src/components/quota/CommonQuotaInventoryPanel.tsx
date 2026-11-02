import { Button } from '@/components/ui/Button';
import { useTranslation } from 'react-i18next';
import { useEffect, useMemo, useRef, useState } from 'react';
import {
  fullRefreshRequest,
  waitFullRefresh,
  type RefreshInventory,
  type RefreshProgress,
} from '@/services/api/fullRefresh';
export function CommonQuotaInventoryPanel({
  base,
  managementKey,
}: {
  base: string;
  managementKey: string;
}) {
  const { t } = useTranslation();
  const context = useMemo(() => ({ base, managementKey }), [base, managementKey]);
  const current = useRef<typeof context | null>(context),
    pending = useRef(false);
  const activeRequest = useRef<AbortController | null>(null);
  const [view, setView] = useState<{
    context: typeof context;
    inventory?: RefreshInventory;
    progress?: RefreshProgress;
    busy?: boolean;
    error?: string;
  }>({ context });
  useEffect(() => {
    current.current = context;
    pending.current = false;
    const abort = new AbortController();
    activeRequest.current = abort;
    fullRefreshRequest<RefreshInventory>(base, managementKey, undefined, undefined, abort.signal)
      .then((inventory) => {
        if (!abort.signal.aborted && current.current === context) setView({ context, inventory });
      })
      .catch(() => {
        if (!abort.signal.aborted && current.current === context)
          setView({ context, error: 'common_inventory_unavailable' });
      });
    return () => {
      abort.abort();
      activeRequest.current?.abort();
      if (current.current === context) current.current = null;
    };
  }, [context, base, managementKey]);
  const data = view.context === context ? view : undefined;
  const refresh = async () => {
    if (pending.current || current.current !== context) return;
    activeRequest.current?.abort();
    const abort = new AbortController();
    activeRequest.current = abort;
    const valid = () => current.current === context && !abort.signal.aborted;
    pending.current = true;
    setView({ ...data, context, busy: true });
    try {
      const job = await fullRefreshRequest<RefreshProgress>(
        base,
        managementKey,
        { purpose: 'manual' },
        undefined,
        abort.signal
      );
      if (!valid()) return;
      const progress = await waitFullRefresh(base, managementKey, job, abort.signal, (p) => {
        if (valid()) setView({ ...data, context, busy: true, progress: p });
      });
      if (!valid()) return;
      const inventory = await fullRefreshRequest<RefreshInventory>(
        base,
        managementKey,
        undefined,
        undefined,
        abort.signal
      );
      if (valid()) setView({ context, inventory, progress });
    } catch {
      if (valid()) setView({ ...data, context, error: 'common_refresh_unavailable' });
    } finally {
      if (valid()) pending.current = false;
    }
  };
  return (
    <details data-common-quota="true">
      <summary>{t('credential_policy.inventory_title')}</summary>
      <Button
        variant="secondary"
        size="sm"
        type="button"
        disabled={data?.busy}
        onClick={() => void refresh()}
      >
        {t('credential_policy.refresh_all')}
      </Button>
      {data?.progress && (
        <p role="status">
          {data.progress.state} · pending {data.progress.pending} · success{' '}
          {data.progress.succeeded} · failed {data.progress.failed}
        </p>
      )}
      {data?.error && <p role="alert">{data.error}</p>}
      {data?.inventory?.sources.map((s) => (
        <div key={s.id || JSON.stringify(s.targets)}>
          <strong>
            {s.request.kind} · {s.id}
          </strong>
          <p>
            {s.attempt?.queryStatus ?? s.state} · {s.reason} · {s.stale ? 'stale' : ''}
          </p>
          {s.lastSuccess ? (
            <p>
              {s.lastSuccess.semantic ??
                (s.request.kind === 'credential_quota'
                  ? 'credential_windows'
                  : 'unknown_semantic')}{' '}
              ·{' '}
              {s.lastSuccess.nativeQuota
                ? `${s.lastSuccess.nativeQuota.unit} ${s.lastSuccess.nativeQuota.state} ${s.lastSuccess.nativeQuota.remaining ?? 'unknown'}`
                : `${s.lastSuccess.currency ?? ''} ${s.lastSuccess.amountDecimal ?? 'unknown'}`}{' '}
              · {new Date(s.lastSuccess.observedAtMs).toString()}
            </p>
          ) : (
            <p>{t('credential_policy.observation_unknown')}</p>
          )}
        </div>
      ))}
    </details>
  );
}
