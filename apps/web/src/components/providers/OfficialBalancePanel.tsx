import { useEffect, useMemo, useRef, useState } from 'react';
import {
  officialBalanceRequest,
  type BalanceBinding,
  type BalanceJob,
  type OfficialBalanceStatus,
} from '@/services/api/officialBalance';

export interface OfficialBalancePanelProps {
  base: string;
  managementKey: string;
  binding?: BalanceBinding;
  disabled?: boolean;
  supported?: boolean;
}

export function OfficialBalancePanel({
  base,
  managementKey,
  binding,
  disabled = false,
  supported = true,
}: OfficialBalancePanelProps) {
  const bindingJSON = JSON.stringify(binding);
  const context = useMemo(
    () => ({ base, managementKey, bindingJSON }),
    [base, managementKey, bindingJSON]
  );
  const current = useRef(context);
  const [view, setView] = useState<{
    context: typeof context;
    data?: OfficialBalanceStatus;
    error?: string;
    busy?: boolean;
  }>({ context });
  const [editor, setEditor] = useState({
    context,
    secret: '',
    endpoint: '',
    permits: undefined as string | undefined,
    clear: false,
  });
  const pending = useRef<AbortController | null>(null);
  const data = view.context === context ? view.data : undefined;
  const busy = view.context === context && view.busy;
  const edit =
    editor.context === context
      ? editor
      : {
          context,
          secret: '',
          endpoint: '',
          permits: undefined as string | undefined,
          clear: false,
        };
  const usable = !!base && !!managementKey && !!binding && !disabled && supported;

  useEffect(() => {
    current.current = context;
    const abort = new AbortController();
    pending.current = abort;
    if (usable) {
      officialBalanceRequest<OfficialBalanceStatus>(
        base,
        managementKey,
        'GET',
        `?binding=${encodeURIComponent(bindingJSON)}`,
        undefined,
        abort.signal
      )
        .then((value) => {
          if (!abort.signal.aborted && current.current === context)
            setView({ context, data: value });
        })
        .catch(() => {
          if (!abort.signal.aborted && current.current === context)
            setView({ context, error: 'balance_unavailable' });
        });
    }
    return () => {
      abort.abort();
      pending.current?.abort();
    };
  }, [context, usable, base, managementKey, bindingJSON]);

  const run = async (action: 'save' | 'query') => {
    if (!usable || !binding || !data || busy) return;
    const abort = new AbortController();
    pending.current?.abort();
    pending.current = abort;
    const previous = data;
    setView({ context, data: previous, busy: true });
    try {
      if (action === 'save') {
        await officialBalanceRequest(
          base,
          managementKey,
          'PUT',
          '',
          {
            ...binding,
            version: previous.version,
            adapter: 'deepseek',
            endpoint: edit.endpoint || previous.endpoint || 'https://api.deepseek.com/user/balance',
            privateTargets:
              edit.permits !== undefined
                ? edit.permits
                    .split(',')
                    .map((ip) => ip.trim())
                    .filter(Boolean)
                : (previous.privateTargets ?? []),
            timeoutMs: previous.timeoutMs ?? 15000,
            secret: edit.secret,
            clearSecret: edit.clear,
          },
          abort.signal
        );
        // Save is not success until authenticated server readback succeeds.
        const value = await officialBalanceRequest<OfficialBalanceStatus>(
          base,
          managementKey,
          'GET',
          `?binding=${encodeURIComponent(bindingJSON)}`,
          undefined,
          abort.signal
        );
        if (current.current === context && !abort.signal.aborted) {
          setView({ context, data: value });
          setEditor({
            context,
            secret: '',
            endpoint: '',
            permits: undefined as string | undefined,
            clear: false,
          });
        }
      } else {
        let job = await officialBalanceRequest<BalanceJob>(
          base,
          managementKey,
          'POST',
          '',
          { ...binding, version: previous.version },
          abort.signal
        );
        const deadline = Date.now() + 65000;
        while (job.state === 'running' && Date.now() < deadline) {
          await new Promise((resolve) => setTimeout(resolve, 100));
          if (abort.signal.aborted) return;
          job = await officialBalanceRequest<BalanceJob>(
            base,
            managementKey,
            'GET',
            `?job=${encodeURIComponent(job.id)}`,
            undefined,
            abort.signal
          );
        }
        if (job.state !== 'complete' || !job.status) throw new Error('balance_query_interrupted');
        if (current.current === context && !abort.signal.aborted)
          setView({ context, data: job.status });
      }
    } catch {
      if (current.current === context && !abort.signal.aborted)
        setView({ context, data: previous, error: 'balance_request_failed_reload_identity' });
    }
  };

  return (
    <section aria-label="官方余额" aria-busy={busy}>
      <h4>官方余额 · Manager 独立只读查询</h4>
      <p>不代表 Core 查询或门禁 ACK；不修改推理凭证或自动策略。</p>
      {!supported ? (
        <p role="status">unsupported · 本票尚未交付此来源协议；不以缺少认证冒充已支持。</p>
      ) : !usable ? (
        <p role="status">identity_unavailable · 需完整、已核实的来源身份</p>
      ) : (
        <>
          <p role="status">
            {data?.state ?? 'loading'}
            {data?.stale ? ' · stale' : ''}
          </p>
          {view.context === context && view.error && <p role="alert">{view.error}</p>}
          {data && (
            <>
              <p>
                查询凭据：{data.configured ? 'configured' : 'needs_configuration'} · 服务端加密保存
              </p>
              <p>
                首个已验证协议：DeepSeek GET /user/balance。DeepInfra
                及其他协议：unsupported（待适配）。
              </p>
              <label>
                只读查询密钥
                <input
                  aria-label="只读查询密钥"
                  type="password"
                  autoComplete="new-password"
                  value={edit.secret}
                  onChange={(event) => setEditor({ ...edit, secret: event.target.value })}
                  disabled={busy}
                />
              </label>
              <p>空白保留现有密钥；不要填写 Core 管理密钥。</p>
              <label>
                <input
                  aria-label="明确清除查询密钥"
                  type="checkbox"
                  checked={edit.clear}
                  disabled={busy}
                  onChange={(event) => setEditor({ ...edit, clear: event.target.checked })}
                />
                明确清除查询密钥
              </label>
              <label>
                查询 HTTPS 地址
                <input
                  aria-label="查询 HTTPS 地址"
                  value={edit.endpoint || data.endpoint || 'https://api.deepseek.com/user/balance'}
                  disabled={busy}
                  onChange={(event) => setEditor({ ...edit, endpoint: event.target.value })}
                />
              </label>
              <label>
                明确允许的私网 IP（逐项精确授权，逗号分隔）
                <input
                  aria-label="允许的私网 IP"
                  value={edit.permits ?? data.privateTargets?.join(',') ?? ''}
                  disabled={busy}
                  onChange={(event) => setEditor({ ...edit, permits: event.target.value })}
                />
              </label>
              <button
                type="button"
                disabled={busy || (edit.clear && !!edit.secret)}
                onClick={() => void run('save')}
              >
                保存余额来源
              </button>
              <button
                type="button"
                disabled={busy || !data.configured || !data.managerDirectQuery}
                onClick={() => void run('query')}
              >
                手动查询余额
              </button>
              {data.attempt && (
                <p>
                  最近尝试：{data.attempt.queryStatus} ·{' '}
                  {new Date(data.attempt.observedAtMs).toISOString()}
                </p>
              )}
              {data.lastSuccess && (
                <>
                  <p>
                    最近成功：{new Date(data.lastSuccess.observedAtMs).toISOString()} ·
                    account_wallet · 来源 {data.endpoint}
                  </p>
                  <p>平台可用标志：{String(data.lastSuccess.available)}（不等于余额为零）</p>
                  <ul>
                    {data.lastSuccess.balances?.map((wallet) => (
                      <li key={wallet.currency}>
                        {wallet.currency} 总余额 {wallet.total} · 赠送部分 {wallet.granted} ·
                        充值部分 {wallet.toppedUp}
                      </li>
                    ))}
                  </ul>
                  <p>不同币种不求和；赠送与充值是总余额的组成部分，不重复累计。</p>
                </>
              )}
            </>
          )}
        </>
      )}
    </section>
  );
}
