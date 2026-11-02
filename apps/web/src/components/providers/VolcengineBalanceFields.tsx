import type { VolcengineBalanceProfile } from '@/services/api/officialBalance';

export interface VolcengineBillingCredentials {
  accessKeyID: string;
  secretAccessKey: string;
}

export function VolcengineBalanceFields({
  disabled,
  profile,
  credentials,
  onProfile,
  onCredentials,
}: {
  disabled: boolean;
  profile: VolcengineBalanceProfile;
  credentials: VolcengineBillingCredentials;
  onProfile: (value: VolcengineBalanceProfile) => void;
  onCredentials: (value: VolcengineBillingCredentials) => void;
}) {
  return (
    <fieldset disabled={disabled}>
      <legend>火山引擎 · 预付费账户余额（不是 Coding Plan）</legend>
      <label>
        资金账户 ID
        <input
          aria-label="资金账户 ID"
          value={profile.accountID}
          inputMode="numeric"
          maxLength={32}
          onChange={(e) => onProfile({ ...profile, accountID: e.target.value })}
        />
      </label>
      <label>
        账户币种（不按域名推断）
        <select
          aria-label="火山账务币种"
          value={profile.currency}
          onChange={(e) => onProfile({ ...profile, currency: e.target.value })}
        >
          <option value="">请选择已确认币种</option>
          <option value="CNY">CNY · 主单位元</option>
        </select>
      </label>
      <label>
        <input
          aria-label="币种与主单位已确认"
          type="checkbox"
          checked={profile.currencyUnitConfirmed}
          onChange={(e) => onProfile({ ...profile, currencyUnitConfirmed: e.target.checked })}
        />
        已独立确认账户采用 CNY / 元，接口不返回币种
      </label>
      <label>
        来源账户关联证据
        <input
          aria-label="来源账户关联证据"
          value={profile.proofReference}
          maxLength={128}
          onChange={(e) => onProfile({ ...profile, proofReference: e.target.value })}
        />
      </label>
      <label>
        <input
          aria-label="来源账户关联已确认"
          type="checkbox"
          checked={profile.accountBindingConfirmed}
          onChange={(e) => onProfile({ ...profile, accountBindingConfirmed: e.target.checked })}
        />
        已独立确认本来源与资金账户的关联
      </label>
      <p>以上为管理员声明，不是查询成功产生的现场账户或推理 Key 归属验证。证据仅填非敏感编号。</p>
      <label>
        火山账务 AK
        <input
          aria-label="火山账务 AK"
          type="password"
          autoComplete="new-password"
          value={credentials.accessKeyID}
          onChange={(e) => onCredentials({ ...credentials, accessKeyID: e.target.value })}
        />
      </label>
      <label>
        火山账务 SK
        <input
          aria-label="火山账务 SK"
          type="password"
          autoComplete="new-password"
          value={credentials.secretAccessKey}
          onChange={(e) => onCredentials({ ...credentials, secretAccessKey: e.target.value })}
        />
      </label>
      <p>
        仅使用另行授权的只读账务
        AK/SK，不是推理密钥。两项同时留空仅保留未变合同的旧密钥；更换账户或合同需重新授权。
      </p>
    </fieldset>
  );
}
