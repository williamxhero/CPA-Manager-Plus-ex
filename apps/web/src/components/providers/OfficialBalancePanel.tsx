import { useEffect, useMemo, useRef, useState } from 'react';
import {
  officialBalanceRequest,
  OfficialBalanceRequestError,
  type BalanceBinding,
  type BalanceJob,
  type OfficialBalanceStatus,
  type OfficialBalanceAdapter,
  type NewAPIConfig,
  type NewAPIConversionProfile,
  type VolcengineBalanceProfile,
  type AliyunBalanceProfile,
  type WebBalanceSource,
} from '@/services/api/officialBalance';
import {
  VolcengineBalanceFields,
  type VolcengineBillingCredentials,
} from './VolcengineBalanceFields';
import { AliyunBalanceFields, type AliyunBillingCredentials } from './AliyunBalanceFields';
import { WebBalanceFields, WebBalanceObservation } from './WebBalanceFields';

const endpoints: Record<OfficialBalanceAdapter, string> = {
  deepseek: 'https://api.deepseek.com/user/balance',
  deepinfra: 'https://api.deepinfra.com/payment/checklist',
  'volcengine-account-balance-v2022-01-01': 'https://billing.volcengineapi.com/',
  'aliyun-prepaid-cash-v2017-12-14': 'https://business.aliyuncs.com/',
  'newapi-user-self-v1': '',
  'newapi-token-usage-v1': '',
  layso_billing_web_v1: 'https://layso.ai/billing',
};
const isAdapter = (value: string | undefined): value is OfficialBalanceAdapter =>
  value === 'deepseek' ||
  value === 'deepinfra' ||
  value === 'volcengine-account-balance-v2022-01-01' ||
  value === 'aliyun-prepaid-cash-v2017-12-14' ||
  value === 'newapi-user-self-v1' ||
  value === 'newapi-token-usage-v1' ||
  value === 'layso_billing_web_v1';
const observedTime = (ms: number | undefined): string =>
  typeof ms === 'number' && Number.isFinite(ms) && ms > 0 && !Number.isNaN(new Date(ms).getTime())
    ? new Date(ms).toISOString()
    : '未产生采集时间';
const nativeContract = (adapter: string): NewAPIConfig => ({
  protocolConfirmed: false,
  instanceVersion: '',
  nativeUnit: 'newapi_quota',
  auth:
    adapter === 'newapi-token-usage-v1' ? 'inference-key-readonly-v1' : 'user-pat-with-user-id-v1',
});
const emptyConversion = (): NewAPIConversionProfile => ({
  confirmed: false,
  nativeUnit: 'newapi_quota',
  currency: '',
  moneyPerNativeQuota: '',
  proofReference: '',
  contractVersion: '',
});

const emptyWebSource = (): WebBalanceSource => ({
  site: 'layso',
  accountId: '',
  tenantId: '',
  role: '',
  executionUser: '',
  browser: { family: 'microsoft-edge', profile: 'Profile 1', instanceId: '' },
  currency: 'USD',
  parsingRule: '',
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
    readbackRequired?: boolean;
    baselineRequired?: boolean;
  }>({ context });
  const [editor, setEditor] = useState({
    context,
    adapter: undefined as OfficialBalanceAdapter | undefined,
    secret: '',
    endpoint: '',
    permits: undefined as string | undefined,
    clear: false,
    newAPI: undefined as NewAPIConfig | undefined,
    volcengine: undefined as VolcengineBalanceProfile | undefined,
    volcengineSecret: undefined as VolcengineBillingCredentials | undefined,
    aliyun: undefined as AliyunBalanceProfile | undefined,
    aliyunSecret: undefined as AliyunBillingCredentials | undefined,
    web: undefined as WebBalanceSource | undefined,
  });
  const pending = useRef<AbortController | null>(null);
  const generation = useRef(0);
  const operationGeneration = generation.current;
  const data = view.context === context ? view.data : undefined;
  const busy = view.context === context && view.busy;
  const readbackRequired = view.context === context && view.readbackRequired;
  const baselineRequired = view.context === context && view.baselineRequired;
  const controlsDisabled = busy || readbackRequired || baselineRequired;
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
          volcengine: undefined as VolcengineBalanceProfile | undefined,
          volcengineSecret: undefined as VolcengineBillingCredentials | undefined,
          aliyun: undefined as AliyunBalanceProfile | undefined,
          aliyunSecret: undefined as AliyunBillingCredentials | undefined,
          web: undefined as WebBalanceSource | undefined,
        };
  const selectedAdapter =
    edit.adapter ?? (isAdapter(data?.adapter) ? data.adapter : compatibleCandidate ? '' : adapter);
  const isNewAPI =
    selectedAdapter === 'newapi-user-self-v1' || selectedAdapter === 'newapi-token-usage-v1';
  const isVolcengine = selectedAdapter === 'volcengine-account-balance-v2022-01-01';
  const isAliyun = selectedAdapter === 'aliyun-prepaid-cash-v2017-12-14';
  const isBillingProfile = isVolcengine || isAliyun;
  const isWeb = selectedAdapter === 'layso_billing_web_v1';
  const web =
    edit.web ?? (data?.adapter === selectedAdapter ? data.web : undefined) ?? emptyWebSource();
  const incompleteWeb =
    isWeb &&
    [
      web.accountId,
      web.tenantId,
      web.role,
      web.executionUser,
      web.browser.instanceId,
      web.parsingRule,
    ].some((value) => !/^[a-zA-Z0-9_.:@-]{1,128}$/.test(value));
  const aliyun = edit.aliyun ??
    (data?.adapter === selectedAdapter ? data.aliyun : undefined) ?? {
      site: '',
      accountID: '',
      accountBindingConfirmed: false,
      proofReference: '',
    };
  const aliyunSecret = edit.aliyunSecret ?? { accessKeyID: '', secretAccessKey: '' };
  const hasAliyunSecret = !!(aliyunSecret.accessKeyID || aliyunSecret.secretAccessKey);
  const incompleteAliyun =
    isAliyun &&
    (aliyun.site !== 'china' ||
      !aliyun.accountID.trim() ||
      !aliyun.accountBindingConfirmed ||
      !aliyun.proofReference.trim() ||
      (hasAliyunSecret && (!aliyunSecret.accessKeyID || !aliyunSecret.secretAccessKey)));
  const volcengine = edit.volcengine ??
    (data?.adapter === selectedAdapter ? data.volcengine : undefined) ?? {
      accountID: '',
      currency: '',
      unit: 'major' as const,
      currencyUnitConfirmed: false,
      accountBindingConfirmed: false,
      proofReference: '',
    };
  const volcengineSecret = edit.volcengineSecret ?? { accessKeyID: '', secretAccessKey: '' };
  const hasVolcengineSecret = !!(volcengineSecret.accessKeyID || volcengineSecret.secretAccessKey);
  const incompleteVolcengine =
    isVolcengine &&
    (!volcengine.accountID ||
      volcengine.currency !== 'CNY' ||
      !volcengine.currencyUnitConfirmed ||
      !volcengine.accountBindingConfirmed ||
      !volcengine.proofReference ||
      (hasVolcengineSecret &&
        (!volcengineSecret.accessKeyID || !volcengineSecret.secretAccessKey)));
  const contract =
    edit.newAPI ??
    (data?.adapter === selectedAdapter ? data.newAPI : undefined) ??
    nativeContract(selectedAdapter);
  const conversion = contract.conversion;
  const endpoint =
    edit.endpoint ||
    (data?.adapter === selectedAdapter ? data.endpoint : '') ||
    (selectedAdapter ? endpoints[selectedAdapter] : '');
  const usable = !!base && !!managementKey && !!binding && !disabled && supported;

  useEffect(() => {
    current.current = context;
    const operations = generation;
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
      operations.current++;
      abort.abort();
      pending.current?.abort();
    };
  }, [context, usable, base, managementKey, bindingJSON]);

  const run = async (action: 'save' | 'query' | 'reload') => {
    if (
      current.current !== context ||
      generation.current !== operationGeneration ||
      !usable ||
      !binding ||
      busy ||
      (action !== 'reload' && (!data || readbackRequired || baselineRequired))
    )
      return;
    // A new operation invalidates callbacks captured against an older baseline.
    generation.current++;
    const abort = new AbortController();
    pending.current?.abort();
    pending.current = abort;
    const previous = data;
    let saved = !!readbackRequired;
    setView({ context, data: previous, busy: true, readbackRequired, baselineRequired });
    try {
      if (action === 'save' && previous) {
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
              isBillingProfile || isWeb
                ? []
                : edit.permits !== undefined
                  ? edit.permits
                      .split(',')
                      .map((ip) => ip.trim())
                      .filter(Boolean)
                  : (previous.privateTargets ?? []),
            timeoutMs: previous.timeoutMs ?? 15000,
            secret: isWeb
              ? ''
              : isAliyun
                ? hasAliyunSecret
                  ? JSON.stringify(aliyunSecret)
                  : ''
                : isVolcengine
                  ? hasVolcengineSecret
                    ? JSON.stringify(volcengineSecret)
                    : ''
                  : edit.secret,
            clearSecret: isWeb ? false : edit.clear,
            ...(isNewAPI ? { newAPI: contract } : {}),
            ...(isVolcengine ? { volcengine } : {}),
            ...(isAliyun ? { aliyun } : {}),
            ...(isWeb ? { web, endpoint: endpoints.layso_billing_web_v1 } : {}),
          },
          abort.signal
        );
        saved = true;
      }
      if (action === 'save' || action === 'reload') {
        // The write succeeded, but only readback establishes the next editable baseline.
        if (current.current !== context || abort.signal.aborted) return;
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
          if (action === 'save' || readbackRequired || !previous)
            setEditor({
              context,
              adapter: undefined as OfficialBalanceAdapter | undefined,
              secret: '',
              endpoint: '',
              permits: undefined as string | undefined,
              clear: false,
              newAPI: undefined as NewAPIConfig | undefined,
              volcengine: undefined as VolcengineBalanceProfile | undefined,
              volcengineSecret: undefined as VolcengineBillingCredentials | undefined,
              aliyun: undefined as AliyunBalanceProfile | undefined,
              aliyunSecret: undefined as AliyunBillingCredentials | undefined,
              web: undefined as WebBalanceSource | undefined,
            });
        }
      } else if (previous) {
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
    } catch (error) {
      const identityRejected =
        error instanceof OfficialBalanceRequestError && [401, 403, 409].includes(error.status);
      if (current.current === context && !abort.signal.aborted)
        setView({
          context,
          data: identityRejected
            ? undefined
            : saved && previous && (isWeb || previous.web)
              ? { ...previous, lastSuccess: undefined, attempt: undefined }
              : previous?.lastSuccess
                ? { ...previous, stale: true }
                : previous,
          readbackRequired: saved,
          baselineRequired: baselineRequired || action === 'reload' || identityRejected,
          error: identityRejected
            ? `${saved ? '余额来源已保存；' : ''}identity_unavailable · 管理身份或来源版本已失效，请重试回读。`
            : saved
              ? '余额来源已保存，但回读失败；请重试回读后再操作。'
              : 'balance_request_failed_reload_identity',
        });
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
            {data?.state ?? (baselineRequired ? 'identity_unavailable' : 'loading')}
            {data?.stale ? ' · stale' : ''}
          </p>
          {view.context === context && view.error && <p role="alert">{view.error}</p>}
          {(readbackRequired || baselineRequired) && (
            <button type="button" disabled={busy} onClick={() => void run('reload')}>
              重试回读
            </button>
          )}
          {data && (
            <>
              <p>
                {data.adapter === 'layso_billing_web_v1' ? '网页来源' : '查询凭据'}：
                {data.configured ? 'configured' : 'needs_configuration'} ·{' '}
                {data.adapter === 'layso_billing_web_v1' ? '无需查询密钥' : '服务端加密保存'}
              </p>
              {data.adapter === 'layso_billing_web_v1' && <WebBalanceObservation status={data} />}
              <p>
                DeepSeek：账户钱包；DeepInfra：有符号预付净额。NewAPI：仅显式确认的实例协议与原生额度，不表示当前站点已授权可用。
              </p>
              <label>
                官方余额协议
                <select
                  aria-label="官方余额协议"
                  value={selectedAdapter}
                  disabled={controlsDisabled}
                  onChange={(event) => {
                    if (isAdapter(event.target.value))
                      setEditor({
                        ...edit,
                        adapter: event.target.value,
                        endpoint: '',
                        secret: '',
                        clear: false,
                        newAPI: undefined,
                        volcengine: undefined,
                        volcengineSecret: undefined,
                        aliyun: undefined,
                        aliyunSecret: undefined,
                        web: undefined,
                      });
                  }}
                >
                  <option value="" disabled>
                    手动选择已确认协议
                  </option>
                  <option value="deepseek">DeepSeek · account_wallet</option>
                  <option value="deepinfra">DeepInfra · signed_prepaid_net</option>
                  <option value="volcengine-account-balance-v2022-01-01">
                    火山引擎 · account_wallet · 独立账务授权
                  </option>
                  <option value="aliyun-prepaid-cash-v2017-12-14">
                    阿里云 · prepaid cash · 独立只读账务授权
                  </option>
                  <option value="newapi-user-self-v1">NewAPI · account_wallet · 原生额度</option>
                  <option value="newapi-token-usage-v1">NewAPI · key_remaining · 原生额度</option>
                  <option value="layso_billing_web_v1">LAYSO · 只读网页账户余额</option>
                </select>
              </label>
              {compatibleCandidate && (
                <p>候选协议未确认；不会按域名自动选择、探测或推断授权/单位。</p>
              )}
              {isVolcengine && (
                <VolcengineBalanceFields
                  disabled={!!controlsDisabled}
                  profile={volcengine}
                  credentials={volcengineSecret}
                  onProfile={(value) => setEditor({ ...edit, volcengine: value })}
                  onCredentials={(value) => setEditor({ ...edit, volcengineSecret: value })}
                />
              )}
              {isAliyun && (
                <AliyunBalanceFields
                  disabled={!!controlsDisabled}
                  profile={aliyun}
                  credentials={aliyunSecret}
                  onProfile={(value) => setEditor({ ...edit, aliyun: value })}
                  onCredentials={(value) => setEditor({ ...edit, aliyunSecret: value })}
                />
              )}
              {isWeb && (
                <WebBalanceFields
                  source={web}
                  disabled={!!controlsDisabled}
                  onChange={(value) => setEditor({ ...edit, web: value })}
                />
              )}
              {isNewAPI && (
                <fieldset disabled={controlsDisabled}>
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
                    原生 quota 不是货币。不配置换算合同就只保留原生额度；不得默认 USD 或 500000。
                    当前真实站点版本、授权、单位仍待外部证据，管理员勾选不是独立现场核验。
                  </p>
                  <label>
                    <input
                      type="checkbox"
                      aria-label="启用明确额度换算"
                      checked={!!conversion}
                      onChange={(e) =>
                        setEditor({
                          ...edit,
                          newAPI: {
                            ...contract,
                            conversion: e.target.checked ? emptyConversion() : undefined,
                          },
                        })
                      }
                    />
                    启用明确额度换算（可选，无默认币种或倍率）
                  </label>
                  {conversion && (
                    <fieldset>
                      <legend>明确换算合同：金额主单位 / ONE 原生额度</legend>
                      <label>
                        <input
                          type="checkbox"
                          aria-label="已确认换算合同"
                          checked={conversion.confirmed}
                          onChange={(e) =>
                            setEditor({
                              ...edit,
                              newAPI: {
                                ...contract,
                                conversion: { ...conversion, confirmed: e.target.checked },
                              },
                            })
                          }
                        />
                        管理员声明已取得该单位、币种、方向与倍率的证据
                      </label>
                      <label>
                        换算原生单位
                        <input aria-label="换算原生单位" readOnly value={conversion.nativeUnit} />
                      </label>
                      <label>
                        换算币种
                        <input
                          aria-label="换算币种"
                          value={conversion.currency}
                          onChange={(e) =>
                            setEditor({
                              ...edit,
                              newAPI: {
                                ...contract,
                                conversion: { ...conversion, currency: e.target.value },
                              },
                            })
                          }
                        />
                      </label>
                      <label>
                        每单位原生额度对应金额
                        <input
                          aria-label="每单位原生额度对应金额"
                          value={conversion.moneyPerNativeQuota}
                          onChange={(e) =>
                            setEditor({
                              ...edit,
                              newAPI: {
                                ...contract,
                                conversion: { ...conversion, moneyPerNativeQuota: e.target.value },
                              },
                            })
                          }
                        />
                      </label>
                      <label>
                        换算证据引用
                        <input
                          aria-label="换算证据引用"
                          value={conversion.proofReference}
                          onChange={(e) =>
                            setEditor({
                              ...edit,
                              newAPI: {
                                ...contract,
                                conversion: { ...conversion, proofReference: e.target.value },
                              },
                            })
                          }
                        />
                      </label>
                      <label>
                        换算合同版本
                        <input
                          aria-label="换算合同版本"
                          value={conversion.contractVersion}
                          onChange={(e) =>
                            setEditor({
                              ...edit,
                              newAPI: {
                                ...contract,
                                conversion: { ...conversion, contractVersion: e.target.value },
                              },
                            })
                          }
                        />
                      </label>
                      <p>
                        倍率方向：每 ONE 原生 quota
                        对应多少币种主单位，严格正十进制字符串；不填倒数，不换算
                        cents。证据引用仅填编号，不填密钥、URL或脚本。此合同是管理员声明，不证明当前现场账户或额度单位已独立核验。
                      </p>
                    </fieldset>
                  )}
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
              {data.newAPI?.conversion && (
                <p aria-label="已保存换算合同">
                  已保存换算：{data.newAPI.conversion.nativeUnit} ×{' '}
                  {data.newAPI.conversion.moneyPerNativeQuota} {data.newAPI.conversion.currency} ·
                  证据 {data.newAPI.conversion.proofReference} · 版本{' '}
                  {data.newAPI.conversion.contractVersion} · confirmed=
                  {String(data.newAPI.conversion.confirmed)} ·
                  operator_asserted_contract_not_live_verified
                </p>
              )}
              {!isBillingProfile && !isWeb && (
                <label>
                  只读查询密钥
                  <input
                    aria-label="只读查询密钥"
                    type="password"
                    autoComplete="new-password"
                    value={edit.secret}
                    onChange={(event) => setEditor({ ...edit, secret: event.target.value })}
                    disabled={controlsDisabled}
                  />
                </label>
              )}
              {!isWeb && (
                <>
                  <p>空白仅在来源合同未变时保留现有密钥；不要填写 Core 管理密钥。</p>
                  <label>
                    <input
                      aria-label="明确清除查询密钥"
                      type="checkbox"
                      checked={edit.clear}
                      disabled={controlsDisabled}
                      onChange={(event) => setEditor({ ...edit, clear: event.target.checked })}
                    />
                    明确清除查询密钥
                  </label>
                </>
              )}
              <label>
                查询 HTTPS 地址
                <input
                  aria-label="查询 HTTPS 地址"
                  value={endpoint}
                  readOnly={isBillingProfile || isWeb}
                  disabled={controlsDisabled}
                  onChange={(event) => setEditor({ ...edit, endpoint: event.target.value })}
                />
              </label>
              {!isBillingProfile && !isWeb && (
                <label>
                  明确允许的私网 IP（逐项精确授权，逗号分隔）
                  <input
                    aria-label="允许的私网 IP"
                    value={edit.permits ?? data.privateTargets?.join(',') ?? ''}
                    disabled={controlsDisabled}
                    onChange={(event) => setEditor({ ...edit, permits: event.target.value })}
                  />
                </label>
              )}
              <button
                type="button"
                disabled={
                  controlsDisabled ||
                  !selectedAdapter ||
                  (isNewAPI && !contract.protocolConfirmed) ||
                  (isNewAPI && !!conversion && !conversion.confirmed) ||
                  incompleteVolcengine ||
                  incompleteAliyun ||
                  incompleteWeb ||
                  (edit.clear &&
                    (isAliyun
                      ? hasAliyunSecret
                      : isVolcengine
                        ? hasVolcengineSecret
                        : !!edit.secret))
                }
                onClick={() => void run('save')}
              >
                保存余额来源
              </button>
              {!readbackRequired && !baselineRequired && (
                <button type="button" disabled={busy} onClick={() => void run('reload')}>
                  刷新观测
                </button>
              )}
              <button
                type="button"
                disabled={controlsDisabled || !data.configured || !data.managerDirectQuery}
                onClick={() => void run('query')}
              >
                手动查询余额
              </button>
              {data.attempt && (
                <p>
                  最近尝试：{data.attempt.queryStatus} · {observedTime(data.attempt.observedAtMs)}
                  {data.attempt.errorCode ? ` · ${data.attempt.errorCode}` : ''}
                </p>
              )}
              {data.lastSuccess && (
                <>
                  <p>
                    最近成功：{observedTime(data.lastSuccess.observedAtMs)} ·
                    {data.lastSuccess.semantic} · 来源 {data.endpoint}
                  </p>
                  {data.adapter === 'layso_billing_web_v1' ? null : data.lastSuccess.nativeQuota ? (
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
                      {data.lastSuccess.nativeQuota.state === 'finite' &&
                        data.lastSuccess.amountDecimal !== undefined &&
                        data.lastSuccess.conversion && (
                          <>
                            <p>
                              明确合同换算金额：{data.lastSuccess.currency}{' '}
                              {data.lastSuccess.amountDecimal}
                              ；与原生额度是同一余额的两种表示，不能相加。
                            </p>
                            <p>
                              换算证据：{data.lastSuccess.conversion.proofReference} ·{' '}
                              {data.lastSuccess.conversion.contractVersion} ·{' '}
                              {data.lastSuccess.conversion.qualification}
                              ；不是独立现场账户或单位验证。
                            </p>
                          </>
                        )}
                    </>
                  ) : data.lastSuccess.aliyunAccount ? (
                    <>
                      <p>
                        阿里预付费现金余额：{data.lastSuccess.currency}{' '}
                        {data.lastSuccess.amountDecimal}
                      </p>
                      <p>
                        仅取 AvailableCashAmount
                        与同次响应币种；负值保留现金欠款，不与信用额度或可用额度相加。
                      </p>
                      <p>
                        查询账户 {data.lastSuccess.aliyunAccount.accountID} · 身份{' '}
                        {data.lastSuccess.aliyunAccount.identityType} · 关联证据{' '}
                        {data.lastSuccess.aliyunAccount.proofReference} ·{' '}
                        {data.lastSuccess.aliyunAccount.qualification}
                      </p>
                      <p>
                        {data.lastSuccess.aliyunAccount.qualification ===
                        'caller_account_matched_operator_asserted_source_binding_not_key_or_payer_verified'
                          ? 'STS caller 账户已匹配；来源关联仍是管理员声明，所选推理 Key 或最终 payer 未验证。'
                          : '账户验证资格未确认；不能作为所选推理 Key 或最终 payer 归属证明。'}
                        不是 Coding Plan 额度、Core 门禁 ACK 或自动恢复依据。
                      </p>
                    </>
                  ) : data.lastSuccess.volcengineAccount ? (
                    <>
                      <p>
                        资金账户可用余额：{data.lastSuccess.currency}{' '}
                        {data.lastSuccess.amountDecimal}
                      </p>
                      <p>
                        负值保留账户欠款；可用余额直接取自官方结果，不与现金、信控或冻结金额再次加减。
                      </p>
                      <p>
                        账户 {data.lastSuccess.volcengineAccount.accountID} · 单位{' '}
                        {data.lastSuccess.volcengineAccount.unit} · 关联证据{' '}
                        {data.lastSuccess.volcengineAccount.proofReference} ·{' '}
                        {data.lastSuccess.volcengineAccount.qualification}
                      </p>
                      <p>
                        币种与账户关联是管理员声明，不是独立现场核验，也不是凭证 Coding Plan
                        配额或自动恢复依据。
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
