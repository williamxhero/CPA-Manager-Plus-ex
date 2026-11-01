import { useEffect, useMemo, useRef, useState } from 'react';
import {
  credentialQuotaRequest,
  type CredentialQuotaJob,
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
      let job = await credentialQuotaRequest<CredentialQuotaJob>(
        base,
        managementKey,
        'POST',
        '',
        {
          scope: data.scope,
          targets: data.targets,
          auth_index: authIndex,
          version: data.version,
          sourceVersion: data.sourceVersion,
        },
        abort.signal
      );
      while (valid() && job.state === 'running' && Date.now() < job.deadlineMs) {
        await new Promise((resolve) => setTimeout(resolve, 150));
        if (!valid()) return;
        job = await credentialQuotaRequest<CredentialQuotaJob>(
          base,
          managementKey,
          'GET',
          `/jobs/${encodeURIComponent(job.id)}`,
          undefined,
          abort.signal
        );
      }
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
    <section aria-label="凭证后端额度" data-credential-quota="true">
      <h3>凭证额度窗口</h3>
      {!usable ? (
        <p>runtime_binding_unavailable</p>
      ) : !data ? (
        <p>{(view.context === context && view.error) || 'loading'}</p>
      ) : (
        <>
          <p>credential_query_v1: {data.credentialQuery?.state ?? 'unknown'} · gate: unavailable</p>
          <p>
            {data.protocol} · {data.attempt?.queryStatus ?? data.state} · {data.reason}
          </p>
          <p>
            {data.accountProvenance} ·{' '}
            {data.manualDisabled ? 'manual_disabled' : '管理查询独立于推理启停'}
          </p>
          {data.lastSuccess && (
            <p>
              最近成功：{new Date(data.lastSuccess.observedAtMs).toISOString()}{' '}
              {data.stale ? 'stale' : ''}
            </p>
          )}
          {windows?.windows.map((window, index) => (
            <p key={`${window.name}:${window.model ?? ''}:${index}`}>
              {window.name} {window.model} · 已用 {window.usedPercent}% · {window.sourceStatus} ·{' '}
              {window.resetAt}
            </p>
          ))}
          {windows && (
            <p>
              {windows.complete ? 'complete' : 'partial'} · {windows.accountProvenance} · 非门禁 ACK
            </p>
          )}
          <button
            type="button"
            disabled={busy || data.state !== 'ready' || data.credentialQuery?.state !== 'available'}
            onClick={() => void refresh()}
          >
            {busy ? '查询中' : '刷新后端额度'}
          </button>
        </>
      )}
      {view.context === context && view.error && data && <p role="alert">{view.error}</p>}
    </section>
  );
}
