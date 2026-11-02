import type { AliyunBalanceProfile } from '@/services/api/officialBalance';

export interface AliyunBillingCredentials {
  accessKeyID: string;
  secretAccessKey: string;
}

export function AliyunBalanceFields({
  disabled,
  profile,
  credentials,
  onProfile,
  onCredentials,
}: {
  disabled: boolean;
  profile: AliyunBalanceProfile;
  credentials: AliyunBillingCredentials;
  onProfile: (value: AliyunBalanceProfile) => void;
  onCredentials: (value: AliyunBillingCredentials) => void;
}) {
  return (
    <fieldset disabled={disabled}>
      <legend>阿里云 · 预付费现金余额（不是 Coding Plan）</legend>
      <label>
        阿里云站点
        <select
          aria-label="阿里云站点"
          value={profile.site}
          onChange={(e) => onProfile({ ...profile, site: e.target.value })}
        >
          <option value="">请选择已确认站点</option>
          <option value="china">China · 中国站</option>
        </select>
      </label>
      <label>
        预期阿里云账户 ID
        <input
          aria-label="预期阿里云账户 ID"
          value={profile.accountID}
          inputMode="numeric"
          onChange={(e) => onProfile({ ...profile, accountID: e.target.value })}
        />
      </label>
      <label>
        阿里来源账户关联证据
        <input
          aria-label="阿里来源账户关联证据"
          value={profile.proofReference}
          maxLength={128}
          onChange={(e) => onProfile({ ...profile, proofReference: e.target.value })}
        />
      </label>
      <label>
        <input
          aria-label="阿里来源账户关联已确认"
          type="checkbox"
          checked={profile.accountBindingConfirmed}
          onChange={(e) => onProfile({ ...profile, accountBindingConfirmed: e.target.checked })}
        />
        已独立确认本 AI 提供商来源与中国站查询账户的关联
      </label>
      <p>
        关联是管理员声明；即使 STS caller 账户匹配，也不证明所选推理 Key 归属或最终
        payer。证据仅填非敏感编号。
      </p>
      <label>
        阿里账务 AK
        <input
          aria-label="阿里账务 AK"
          type="password"
          autoComplete="new-password"
          value={credentials.accessKeyID}
          onChange={(e) => onCredentials({ ...credentials, accessKeyID: e.target.value })}
        />
      </label>
      <label>
        阿里账务 SK
        <input
          aria-label="阿里账务 SK"
          type="password"
          autoComplete="new-password"
          value={credentials.secretAccessKey}
          onChange={(e) => onCredentials({ ...credentials, secretAccessKey: e.target.value })}
        />
      </label>
      <p>
        仅使用另行授权的只读 BSS AK/SK，不是推理密钥；需 bss:DescribeAcccount
        权限。两项同时留空仅保留未变合同的旧密钥，更换账户或合同需重新授权。
      </p>
      <p>
        现金仅取
        AvailableCashAmount；币种取同次官方响应，不人工指定或按域名推断。不查询消费、套餐或自动登录。
      </p>
    </fieldset>
  );
}
