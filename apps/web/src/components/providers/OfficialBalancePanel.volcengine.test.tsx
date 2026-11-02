import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, expect, it, vi } from 'vitest';
import { OfficialBalancePanel } from './OfficialBalancePanel';
import { ProviderDetailDrawer } from './ProviderDetailDrawer/ProviderDetailDrawer';
import { buildProviderRows } from './ProviderTable/rowData';
import type { BalanceBinding } from '@/services/api/officialBalance';

afterEach(() => vi.unstubAllGlobals());
const binding: BalanceBinding = {
  scope: {
    instance_id: 'issued-instance',
    instance_incarnation: 'issued-incarnation',
    boot_epoch: 'issued-boot',
    snapshot_revision: 1,
  },
  targets: [
    {
      resource_id: 'issued-resource',
      incarnation: 'issued-resource-incarnation',
      identity_version: 1,
      credential_version: 1,
      config_version: 1,
      membership_version: 1,
    },
  ],
};

// Supplemental component seam only; the real Manager/SQLite/Core lane is separate.
it('saves an explicitly confirmed prepaid profile with write-only AK/SK and clears secrets after readback', async () => {
  let written: Record<string, unknown> | undefined;
  const profile = {
    accountID: '12345678901234567890123456789012',
    currency: 'CNY',
    unit: 'major',
    currencyUnitConfirmed: true,
    accountBindingConfirmed: true,
    proofReference: 'account-proof-v1',
  };
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_url: string, options?: RequestInit) => {
      if (options?.method === 'PUT') {
        written = JSON.parse(String(options.body));
        return Response.json({});
      }
      return Response.json({
        ...binding,
        version: written ? 2 : 1,
        adapter: written ? 'volcengine-account-balance-v2022-01-01' : '',
        endpoint: written ? 'https://billing.volcengineapi.com/' : '',
        configured: !!written,
        state: written ? 'configured' : 'needs_configuration',
        managerDirectQuery: !!written,
        coreQuery: false,
        coreGate: false,
        stale: false,
        ...(written ? { volcengine: profile } : {}),
      });
    })
  );
  let renderer!: ReactTestRenderer;
  await act(async () => {
    renderer = create(
      <OfficialBalancePanel
        base="http://fixture"
        managementKey="test"
        binding={binding}
        compatibleCandidate
      />
    );
  });
  try {
    const input = (label: string) => renderer.root.findByProps({ 'aria-label': label });
    const button = () =>
      renderer.root.findAllByType('button').find((n) => n.children.join('') === '保存余额来源')!;
    expect(
      renderer.root
        .findAllByType('option')
        .some((n) => n.props.value === 'volcengine-account-balance-v2022-01-01')
    ).toBe(true);
    const change = async (label: string, value: string) => {
      await act(async () => input(label).props.onChange({ target: { value } }));
    };
    const check = async (label: string) => {
      await act(async () => input(label).props.onChange({ target: { checked: true } }));
    };
    await change('官方余额协议', 'volcengine-account-balance-v2022-01-01');
    expect(button().props.disabled).toBe(true);
    await change('资金账户 ID', profile.accountID);
    await change('火山账务币种', 'CNY');
    await change('来源账户关联证据', profile.proofReference);
    await check('币种与主单位已确认');
    await check('来源账户关联已确认');
    await change('火山账务 AK', 'SYNTHETIC-AK');
    expect(button().props.disabled).toBe(true);
    await change('火山账务 SK', 'SYNTHETIC-SK');
    expect(input('查询 HTTPS 地址').props.value).toBe('https://billing.volcengineapi.com/');
    expect(input('查询 HTTPS 地址').props.readOnly).toBe(true);
    expect(button().props.disabled).toBeFalsy();
    await act(async () => button().props.onClick());
    expect(written?.volcengine).toEqual(profile);
    expect(written?.secret).toBe('{"accessKeyID":"SYNTHETIC-AK","secretAccessKey":"SYNTHETIC-SK"}');
    expect(written?.version).toBe(1);
    expect(written?.targets).toEqual(binding.targets);
    expect(input('火山账务 AK').props.value).toBe('');
    expect(input('火山账务 SK').props.value).toBe('');
    expect(JSON.stringify(renderer.toJSON())).not.toContain('SYNTHETIC-');
    expect(JSON.stringify(renderer.toJSON())).toContain('管理员声明');
  } finally {
    await act(async () => renderer.unmount());
  }
});

it('offers the current Ark AI provider an explicit balance editor without choosing a protocol or plan from its hostname', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () =>
      Response.json({
        ...binding,
        version: 0,
        adapter: '',
        endpoint: '',
        configured: false,
        state: 'needs_configuration',
        managerDirectQuery: false,
        coreQuery: false,
        coreGate: false,
        stale: false,
      })
    )
  );
  const row = buildProviderRows({
    gemini: [],
    codex: [],
    claude: [],
    vertex: [],
    usageByProvider: new Map(),
    openai: [
      {
        name: 'Synthetic Ark',
        baseUrl: 'https://ark.cn-beijing.volces.com/api/v3',
        resourceId: 'issued-resource',
        apiKeyEntries: [{ apiKey: 'SYNTHETIC-INFERENCE-NOT-BILLING', resourceId: 'issued-key' }],
      },
    ],
  })[0];
  let renderer!: ReactTestRenderer;
  await act(async () => {
    renderer = create(
      <ProviderDetailDrawer
        row={row}
        open
        usageByProvider={new Map()}
        resolvedTheme="light"
        actionsDisabled={false}
        toggleDisabled={false}
        onClose={() => {}}
        onEdit={() => {}}
        onDelete={() => {}}
        onToggle={() => {}}
        onToggleWebsockets={() => {}}
        onToggleCloak={() => {}}
        onToggleDisableCooling={() => {}}
        officialBalance={{ base: 'http://fixture', managementKey: 'test', binding }}
      />
    );
  });
  try {
    expect(renderer.root.findByProps({ 'aria-label': '官方余额协议' }).props.value).toBe('');
    expect(JSON.stringify(renderer.toJSON())).toContain('不会按域名自动选择');
  } finally {
    await act(async () => renderer.unmount());
  }
});

it('shows exact stale account debt without fabricating wallet parts, availability, or plan quota', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () =>
      Response.json({
        ...binding,
        version: 3,
        adapter: 'volcengine-account-balance-v2022-01-01',
        endpoint: 'https://billing.volcengineapi.com/',
        configured: true,
        state: 'auth_error',
        managerDirectQuery: true,
        coreQuery: false,
        coreGate: false,
        stale: true,
        attempt: { queryStatus: 'auth_error', observedAtMs: 1793500100000 },
        lastSuccess: {
          queryStatus: 'success',
          observedAtMs: 1793500000000,
          semantic: 'account_wallet',
          amountDecimal: '-12345678901234567890.0000000001',
          currency: 'CNY',
          volcengineAccount: {
            accountID: '2100000000',
            unit: 'major',
            proofReference: 'account-proof-v1',
            qualification: 'operator_asserted_binding_and_units_not_live_verified',
          },
        },
      })
    )
  );
  let renderer!: ReactTestRenderer;
  await act(async () => {
    renderer = create(
      <OfficialBalancePanel
        base="http://fixture"
        managementKey="test"
        binding={binding}
        compatibleCandidate
      />
    );
  });
  try {
    const text = JSON.stringify(renderer.toJSON());
    expect(text).toContain('-12345678901234567890.0000000001');
    expect(text).toContain('stale');
    expect(text).toContain('auth_error');
    expect(text).toContain('account-proof-v1');
    expect(text).toContain('operator_asserted_binding_and_units_not_live_verified');
    expect(text).not.toContain('平台可用标志');
    expect(text).not.toContain('赠送部分');
    expect(text).not.toContain('充值部分');
  } finally {
    await act(async () => renderer.unmount());
  }
});
