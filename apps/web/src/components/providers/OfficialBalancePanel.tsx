import { useEffect, useMemo, useRef, useState } from 'react';
import {
  officialBalanceRequest,
  type BalanceBinding,
  type BalanceJob,
  type OfficialBalanceStatus,
  type OfficialBalanceAdapter,
  type NewAPIConfig,
} from '@/services/api/officialBalance';

const endpoints: Record<OfficialBalanceAdapter, string> = {
  deepseek: 'https://api.deepseek.com/user/balance',
  deepinfra: 'https://api.deepinfra.com/payment/checklist',
  'newapi-user-self-v1': '',
  'newapi-token-usage-v1': '',
};
const isAdapter = (value: string | undefined): value is OfficialBalanceAdapter =>
  value === 'deepseek' ||
  value === 'deepinfra' ||
  value === 'newapi-user-self-v1' ||
  value === 'newapi-token-usage-v1';
const nativeContract = (adapter: string): NewAPIConfig => ({
  protocolConfirmed: false,
  instanceVersion: '',
  nativeUnit: 'newapi_quota',
  auth:
    adapter === 'newapi-token-usage-v1' ? 'inference-key-readonly-v1' : 'user-pat-with-user-id-v1',
});

export interface OfficialBalancePanelProps {
  adapter?: OfficialBalanceAdapter;
  base: string;
  managementKey: string;
  binding?: BalanceBinding;
  disabled?: boolean;
  supported?: boolean;
  compatibleCandidate?: boolean;
}

export function OfficialBalancePanel({
  base,
  managementKey,
  binding,
  disabled = false,
  supported = true,
  adapter = 'deepseek',
  compatibleCandidate = false,
}: OfficialBalancePanelProps) {
  const bindingJSON = JSON.stringify(binding);
  const context = useMemo(
    () => ({ base, managementKey, bindingJSON, adapter, compatibleCandidate }),
    [base, managementKey, bindingJSON, adapter, compatibleCandidate]
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
    adapter: undefined as OfficialBalanceAdapter | undefined,
    secret: '',
    endpoint: '',
    permits: undefined as string | undefined,
    clear: false,
    newAPI: undefined as NewAPIConfig | undefined,
  });
  const pending = useRef<AbortController | null>(null);
  const data = view.context === context ? view.data : undefined;
  const busy = view.context === context && view.busy;
  const edit =
    editor.context === context
      ? editor
      : {
          context,
          adapter: undefined as OfficialBalanceAdapter | undefined,
          secret: '',
          endpoint: '',
          permits: undefined as string | undefined,
          clear: false,
          newAPI: undefined as NewAPIConfig | undefined,
        };
  const selectedAdapter =
    edit.adapter ?? (isAdapter(data?.adapter) ? data.adapter : compatibleCandidate ? '' : adapter);
  const isNewAPI =
    selectedAdapter === 'newapi-user-self-v1' || selectedAdapter === 'newapi-token-usage-v1';
  const contract =
    edit.newAPI ??
    (data?.adapter === selectedAdapter ? data.newAPI : undefined) ??
    nativeContract(selectedAdapter);
  const endpoint =
    edit.endpoint ||
    (data?.adapter === selectedAdapter ? data.endpoint : '') ||
    (selectedAdapter ? endpoints[selectedAdapter] : '');
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
            adapter: selectedAdapter,
            endpoint,
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
            ...(isNewAPI ? { newAPI: contract } : {}),
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
            adapter: undefined as OfficialBalanceAdapter | undefined,
            secret: '',
            endpoint: '',
            permits: undefined as string | undefined,
            clear: false,
            newAPI: undefined as NewAPIConfig | undefined,
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
                DeepSeek：账户钱包；DeepInfra：有符号预付净额。NewAPI：仅显式确认的实例协议与原生额度，不表示当前站点已授权可用。
              </p>
              <label>
                官方余额协议
                <select
                  aria-label="官方余额协议"
                  value={selectedAdapter}
                  disabled={busy}
                  onChange={(event) => {
                    if (isAdapter(event.target.value))
                      setEditor({
                        ...edit,
                        adapter: event.target.value,
                        endpoint: '',
                        secret: '',
                        clear: false,
                        newAPI: undefined,
                      });
                  }}
                >
                  <option value="" disabled>
                    手动选择已确认协议
                  </option>
                  <option value="deepseek">DeepSeek · account_wallet</option>
                  <option value="deepinfra">DeepInfra · signed_prepaid_net</option>
                  <option value="newapi-user-self-v1">NewAPI · account_wallet · 原生额度</option>
                  <option value="newapi-token-usage-v1">NewAPI · key_remaining · 原生额度</option>
                </select>
              </label>
              {compatibleCandidate && (
                <p>候选协议未确认；不会按域名自动选择、探测或推断授权/单位。</p>
              )}
              {isNewAPI && (
                <fieldset disabled={busy}>
                  <legend>显式实例来源合同</legend>
                  <label>
                    <input
                      type="checkbox"
                      aria-label="实例协议已确认"
                      checked={contract.protocolConfirmed}
                      onChange={(e) =>
                        setEditor({
                          ...edit,
                          newAPI: { ...contract, protocolConfirmed: e.target.checked },
                        })
                      }
                    />
                    实例协议、版本、授权与账户范围已由管理员独立确认
                  </label>
                  <label>
                    实例协议版本
                    <input
                      aria-label="实例协议版本"
                      value={contract.instanceVersion}
                      onChange={(e) =>
                        setEditor({
                          ...edit,
                          newAPI: { ...contract, instanceVersion: e.target.value },
                        })
                      }
                    />
                  </label>
                  <label>
                    NewAPI 认证方式
                    <select
                      aria-label="NewAPI 认证方式"
                      value={contract.auth}
                      onChange={(e) =>
                        setEditor({ ...edit, newAPI: { ...contract, auth: e.target.value } })
                      }
                    >
                      {selectedAdapter === 'newapi-user-self-v1' ? (
                        <>
                          <option value="user-pat-with-user-id-v1">
                            独立用户 PAT + 匹配用户 ID
                          </option>
                          <option value="user-pat-v1">已确认的 PAT-only 版本</option>
                        </>
                      ) : (
                        <option value="inference-key-readonly-v1">
                          选定推理 Key 的只读用量协议
                        </option>
                      )}
                    </select>
                  </label>
                  {selectedAdapter === 'newapi-user-self-v1' && (
                    <>
                      <label>
                        查询账户范围
                        <input
                          aria-label="查询账户范围"
                          value={contract.accountScope ?? ''}
                          onChange={(e) =>
                            setEditor({
                              ...edit,
                              newAPI: { ...contract, accountScope: e.target.value },
                            })
                          }
                        />
                      </label>
                      <label>
                        NewAPI 用户 ID
                        <input
                          aria-label="NewAPI 用户 ID"
                          value={contract.userID ?? ''}
                          onChange={(e) =>
                            setEditor({ ...edit, newAPI: { ...contract, userID: e.target.value } })
                          }
                        />
                      </label>
                    </>
                  )}
                  <label>
                    原生额度单位
                    <input aria-label="原生额度单位" readOnly value={contract.nativeUnit} />
                  </label>
                  <p>
                    原生 quota
                    不是货币。货币/倍率/换算证据尚未配置，确认后的精确货币换算仍未实现；不得默认 USD
                    或 500000。当前真实站点版本、授权、单位仍待外部证据。
                  </p>
                  <p>
                    账户、认证、版本、单位或来源合同变化会清除旧查询密钥；空白不能跨合同复用旧 PAT。
                  </p>
                </fieldset>
              )}
              {data.newAPI && (
                <p aria-label="已保存 NewAPI 来源合同">
                  已保存合同：{data.newAPI.instanceVersion} · {data.newAPI.auth} · 用户{' '}
                  {data.newAPI.userID ?? 'key-only'} · 账户{' '}
                  {data.newAPI.accountScope ?? 'independent-key'} · {data.newAPI.nativeUnit} ·
                  confirmed={String(data.newAPI.protocolConfirmed)}
                </p>
              )}
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
              <p>空白仅在来源合同未变时保留现有密钥；不要填写 Core 管理密钥。</p>
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
                  value={endpoint}
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
                disabled={
                  busy ||
                  !selectedAdapter ||
                  (isNewAPI && !contract.protocolConfirmed) ||
                  (edit.clear && !!edit.secret)
                }
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
                    {data.lastSuccess.semantic} · 来源 {data.endpoint}
                  </p>
                  {data.lastSuccess.nativeQuota ? (
                    <>
                      <p>
                        原生额度（不是货币）：
                        {data.lastSuccess.nativeQuota.remaining ??
                          data.lastSuccess.nativeQuota.state}{' '}
                        · {data.lastSuccess.nativeQuota.unit}
                      </p>
                      <p>
                        额度状态：{data.lastSuccess.nativeQuota.state}；unlimited/unknown
                        不代表零，也不是耗尽或恢复证据。Key 剩余量不是账户钱包。
                      </p>
                    </>
                  ) : data.lastSuccess.semantic === 'signed_prepaid_net' ? (
                    <>
                      <p>
                        DeepInfra 预付净额：{data.lastSuccess.currency}{' '}
                        {data.lastSuccess.amountDecimal}
                      </p>
                      <p>
                        {data.lastSuccess.amountDecimal?.startsWith('-')
                          ? '负值表示账户欠款。'
                          : '正值表示预付净信用；零值仅来自实际有效观测。'}
                      </p>
                      <p>
                        第一方账单客户端的
                        signed_prepaid_net；并非全部赠送额度、消费限额或通用实时可支配钱包。不推定
                        recent 的结算时间语义。
                      </p>
                    </>
                  ) : (
                    <>
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
        </>
      )}
    </section>
  );
}
