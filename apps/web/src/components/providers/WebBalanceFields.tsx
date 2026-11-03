import type { OfficialBalanceStatus, WebBalanceSource } from '@/services/api/officialBalance';

export function WebBalanceFields({
  source,
  disabled,
  onChange,
}: {
  source: WebBalanceSource;
  disabled: boolean;
  onChange: (value: WebBalanceSource) => void;
}) {
  return (
    <fieldset disabled={disabled}>
      <legend>LAYSO 只读网页来源</legend>
      <p>仅登记预期身份；保存不代表浏览器、账户或解析规则已核验。就绪由服务端独立核验。</p>
      <p>Microsoft Edge · Profile 1 · USD；只读固定账单页，不需要查询密钥。</p>
      {(
        [
          ['accountId', '网页账户 ID'],
          ['tenantId', '网页租户 ID'],
          ['role', '网页角色'],
          ['executionUser', '网页执行用户'],
          ['parsingRule', '网页解析规则'],
        ] as const
      ).map(([key, label]) => (
        <label key={key}>
          {label}
          <input
            aria-label={label}
            value={source[key]}
            maxLength={128}
            onChange={(e) => onChange({ ...source, [key]: e.target.value })}
          />
        </label>
      ))}
      <label>
        Edge 实例 ID
        <input
          aria-label="Edge 实例 ID"
          value={source.browser.instanceId}
          maxLength={128}
          onChange={(e) =>
            onChange({ ...source, browser: { ...source.browser, instanceId: e.target.value } })
          }
        />
      </label>
    </fieldset>
  );
}

export function WebBalanceObservation({ status }: { status: OfficialBalanceStatus }) {
  const last = status.lastSuccess;
  const amount = last?.amountDecimal;
  const validAmount = typeof amount === 'string' && /^-?\d+(?:\.\d+)?$/.test(amount);
  return (
    <>
      <p>来源：LAYSO 只读网页 · {status.endpoint}</p>
      {status.web && (
        <p aria-label="已保存网页绑定">
          账户 {status.web.accountId} · 租户 {status.web.tenantId} · 角色 {status.web.role} ·
          执行用户 {status.web.executionUser} · {status.web.browser.family} ·{' '}
          {status.web.browser.profile} · 实例 {status.web.browser.instanceId} · 解析规则{' '}
          {status.web.parsingRule}
        </p>
      )}
      <p>
        查询就绪：{status.managerDirectQuery ? 'ready' : 'unavailable'}
        {status.reason ? ` · ${status.reason}` : ''}
      </p>
      {last ? (
        <>
          <p>
            网页账户余额：
            {validAmount ? `${last.currency || '币种未知'} ${amount}` : '未知（未产生有效金额）'}
          </p>
          {last.webEvidence && (
            <p>
              观测验证：{last.webEvidence.verification} · {last.webEvidence.parsingRule}
            </p>
          )}
        </>
      ) : (
        <p>暂无成功观测</p>
      )}
    </>
  );
}
