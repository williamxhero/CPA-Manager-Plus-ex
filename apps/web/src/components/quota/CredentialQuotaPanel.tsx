import { Button } from '@/components/ui/Button';
import { useTranslation } from 'react-i18next';
import { refreshCredentialSource, type RefreshProgress } from '@/services/api/fullRefresh';
import { useEffect, useMemo, useRef, useState } from 'react';
import {
  credentialQuotaRequest,
  type CredentialQuotaStatus,
  type QuotaWindows,
} from '@/services/api/credentialQuota';

export function CredentialQuotaPanel({
  base,
  managementKey,
  authIndex,
  revision,
}: {
  base: string;
  managementKey: string;
  authIndex?: string;
  revision?: string | number;
}) {
  const { t } = useTranslation();
  const context = useMemo(
    () => ({ base, managementKey, authIndex, revision }),
    [base, managementKey, authIndex, revision]
  );
  const current = useRef(context);
  const pending = useRef<AbortController | null>(null);
  const [view, setView] = useState<{
    context: typeof context;
    data?: CredentialQuotaStatus;
    error?: string;
    busy?: boolean;
    progress?: RefreshProgress;
  }>({ context });
  const data = view.context === context ? view.data : undefined;
  const busy = view.context === context && view.busy;
  const usable = !!base && !!managementKey && !!authIndex;
  useEffect(() => {
    current.current = context;
    const abort = new AbortController();
    pending.current = abort;
    if (usable)
      credentialQuotaRequest<CredentialQuotaStatus>(
        base,
        managementKey,
        'GET',
        `?authIndex=${encodeURIComponent(authIndex!)}`,
        undefined,
        abort.signal
      )
        .then((data) => {
          if (!abort.signal.aborted && current.current === context) setView({ context, data });
        })
        .catch(() => {
          if (!abort.signal.aborted && current.current === context)
            setView({ context, error: 'credential_quota_unavailable' });
        });
    return () => {
      abort.abort();
      pending.current?.abort();
    };
  }, [context, usable, base, managementKey, authIndex]);
  let windows: QuotaWindows | undefined;
  try {
    if (data?.lastSuccess?.windowsJson) windows = JSON.parse(data.lastSuccess.windowsJson);
  } catch {
    /* No invented windows. */
  }
  const refresh = async () => {
    if (!usable || !data || busy || current.current !== context) return;
    pending.current?.abort();
    const abort = new AbortController();
    pending.current = abort;
    const valid = () => !abort.signal.aborted && current.current === context;
    setView({ context, data, busy: true });
    try {
      const job = await refreshCredentialSource(
        base,
        managementKey,
        {
          scope: data.scope,
          targets: data.targets,
          authIndex: data.auth_index ?? authIndex!,
          sourceId: data.sourceId,
          subjectId: data.subjectId,
          queryVersion: data.queryVersion ?? data.version,
          sourceVersion: data.sourceVersion,
        },
        abort.signal,
        (progress) => {
          if (valid()) setView({ context, data, busy: true, progress });
        }
      );
      if (!valid()) return;
      const next = await credentialQuotaRequest<CredentialQuotaStatus>(
        base,
        managementKey,
        'GET',
        `?authIndex=${encodeURIComponent(authIndex!)}`,
        undefined,
        abort.signal
      );
      if (valid())
        setView({
          context,
          data: next,
          error:
            job.state === 'running' || job.state === 'discarded'
              ? 'quota_query_not_completed'
              : undefined,
        });
    } catch (error) {
      if (valid())
        setView({
          context,
          data:
            error instanceof Error && error.message === 'identity_or_source_changed'
              ? undefined
              : data,
          error: 'credential_quota_request_failed',
        });
    }
  };
  return (
    <section aria-label={t('credential_policy.backend_quota')} data-credential-quota="true">
      <h3>{t('credential_policy.windows_title')}</h3>
      {!usable ? (
        <p>runtime_binding_unavailable</p>
      ) : !data ? (
        <p>{(view.context === context && view.error) || 'loading'}</p>
      ) : (
        <>
          <p>
            credential_query_v1: {data.credentialQuery?.state ?? 'unknown'} ·{' '}
            {t('credential_policy.query_not_gate')}
          </p>
          <p>
            {data.protocol} · {data.attempt?.queryStatus ?? data.state} · {data.reason}
          </p>
          <p>
            {data.accountProvenance} ·{' '}
            {data.manualDisabled ? 'manual_disabled' : t('credential_policy.query_independent')}
          </p>
          {data.lastSuccess && (
            <p>
              {t('credential_policy.last_success')}:{' '}
              {new Date(data.lastSuccess.observedAtMs).toISOString()} {data.stale ? 'stale' : ''}
            </p>
          )}
          {windows?.windows.map((window, index) => (
            <p key={`${window.name}:${window.model ?? ''}:${index}`}>
              {t('credential_policy.window_usage', {
                name: window.name,
                model: window.model,
                percent: window.usedPercent,
                status: window.sourceStatus,
                reset: window.resetAt,
              })}{' '}
              <span data-quota-window-value="true">
                {window.usedPercent}% · {window.sourceStatus ?? 'unknown'}
              </span>
            </p>
          ))}
          {windows && (
            <p>
              {windows.complete ? 'complete' : 'partial'} · {windows.accountProvenance} ·{' '}
              {t('credential_policy.query_not_gate')}
            </p>
          )}
          <Button
            variant="secondary"
            size="sm"
            type="button"
            disabled={busy || data.state !== 'ready' || data.credentialQuery?.state !== 'available'}
            onClick={() => void refresh()}
          >
            {t(busy ? 'credential_policy.querying' : 'credential_policy.backend_refresh')}
          </Button>
        </>
      )}
      {view.context === context && view.progress && (
        <p role="status">{t('credential_policy.progress', { ...view.progress })}</p>
      )}
      {view.context === context && view.error && data && <p role="alert">{view.error}</p>}
    </section>
  );
}
