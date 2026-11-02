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
      resource_id: 'issued-provider',
      incarnation: 'issued-provider-incarnation',
      identity_version: 1,
      credential_version: 1,
      config_version: 1,
      membership_version: 1,
    },
  ],
};
const profile = {
  site: 'china',
  accountID: '1968132000123456',
  accountBindingConfirmed: true,
  proofReference: 'non-secret-source-reference',
};
const protocol = 'aliyun-prepaid-cash-v2017-12-14';
const emptyStatus = {
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
};

it('offers the Alibaba AI provider an explicit prepaid editor without inferring its protocol or billing credentials', async () => {
  const fetch = vi.fn(async () => Response.json(emptyStatus));
  vi.stubGlobal('fetch', fetch);
  const row = buildProviderRows({
    gemini: [],
    codex: [],
    claude: [],
    vertex: [],
    usageByProvider: new Map(),
    openai: [
      {
        name: 'Synthetic Alibaba',
        baseUrl: 'https://dashscope.aliyuncs.com/compatible-mode/v1',
        resourceId: binding.targets[0].resource_id,
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
    const input = (label: string) => renderer.root.findByProps({ 'aria-label': label });
    expect(input('官方余额协议').props.value).toBe('');
    expect(JSON.stringify(renderer.toJSON())).toContain('不会按域名自动选择');
    await act(async () => input('官方余额协议').props.onChange({ target: { value: protocol } }));
    expect(input('阿里云站点').props.value).toBe('');
    expect(input('预期阿里云账户 ID').props.value).toBe('');
    expect(input('阿里账务 AK').props.value).toBe('');
    expect(input('阿里账务 SK').props.value).toBe('');
    expect(fetch).toHaveBeenCalledTimes(1);
  } finally {
    await act(async () => renderer.unmount());
  }
});

// Supplementary public component/API seam. Real Manager/SQLite/Core/TLS is separate.
it('saves explicit China cash configuration with independent password AK/SK and authenticated readback', async () => {
  let written: Record<string, unknown> | undefined;
  const requests: RequestInit[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_url: string, options: RequestInit) => {
      requests.push(options);
      if (options.method === 'PUT') {
        written = JSON.parse(String(options.body));
        return Response.json({});
      }
      return Response.json(
        written
          ? {
              ...emptyStatus,
              version: 1,
              adapter: protocol,
              endpoint: 'https://business.aliyuncs.com/',
              configured: true,
              state: 'configured',
              managerDirectQuery: true,
              aliyun: profile,
            }
          : emptyStatus
      );
    })
  );
  let renderer!: ReactTestRenderer;
  await act(async () => {
    renderer = create(
      <OfficialBalancePanel
        base="http://fixture"
        managementKey="SYNTHETIC-MANAGEMENT"
        binding={binding}
        compatibleCandidate
      />
    );
  });
  try {
    const input = (label: string) => renderer.root.findByProps({ 'aria-label': label });
    const button = () =>
      renderer.root.findAllByType('button').find((n) => n.children.join('') === '保存余额来源')!;
    const change = async (label: string, value: string) => {
      await act(async () => input(label).props.onChange({ target: { value } }));
    };
    const check = async (label: string) => {
      await act(async () => input(label).props.onChange({ target: { checked: true } }));
    };
    expect(input('官方余额协议').props.value).toBe('');
    expect(renderer.root.findAllByType('option').some((n) => n.props.value === protocol)).toBe(
      true
    );
    await change('官方余额协议', protocol);
    expect(button().props.disabled).toBe(true);
    expect(input('阿里云站点').props.value).toBe('');
    expect(renderer.root.findAllByProps({ 'aria-label': '只读查询密钥' })).toHaveLength(0);
    expect(renderer.root.findAllByProps({ 'aria-label': '允许的私网 IP' })).toHaveLength(0);
    await change('阿里云站点', 'china');
    await change('预期阿里云账户 ID', profile.accountID);
    await change('阿里来源账户关联证据', profile.proofReference);
    await check('阿里来源账户关联已确认');
    await change('阿里账务 AK', 'SYNTHETIC-AK');
    expect(button().props.disabled).toBe(true);
    await change('阿里账务 SK', 'SYNTHETIC-SK');
    for (const label of ['阿里账务 AK', '阿里账务 SK']) {
      expect(input(label).props.type).toBe('password');
      expect(input(label).props.autoComplete).toBe('new-password');
    }
    expect(input('查询 HTTPS 地址').props.value).toBe('https://business.aliyuncs.com/');
    expect(input('查询 HTTPS 地址').props.readOnly).toBe(true);
    expect(button().props.disabled).toBeFalsy();
    await act(async () => button().props.onClick());
    expect(written?.aliyun).toEqual(profile);
    expect(written?.secret).toBe('{"accessKeyID":"SYNTHETIC-AK","secretAccessKey":"SYNTHETIC-SK"}');
    expect(written?.scope).toEqual(binding.scope);
    expect(written?.targets).toEqual(binding.targets);
    expect(written?.version).toBe(0);
    expect(written?.privateTargets).toEqual([]);
    expect(written).not.toHaveProperty('currency');
    expect(written).not.toHaveProperty('volcengine');
    expect(input('阿里账务 AK').props.value).toBe('');
    expect(input('阿里账务 SK').props.value).toBe('');
    expect(requests[requests.length - 1]?.headers).toMatchObject({
      Authorization: 'Bearer SYNTHETIC-MANAGEMENT',
    });
    expect(requests[requests.length - 1]?.method).toBe('GET');
    expect(JSON.stringify(renderer.toJSON())).not.toContain('SYNTHETIC-AK');
    expect(JSON.stringify(renderer.toJSON())).not.toContain('SYNTHETIC-SK');
  } finally {
    await act(async () => renderer.unmount());
  }
});

it('preserves exact same-response cash currency and last-success time on stale failure without inventing wallet parts or key proof', async () => {
  const observation = {
    semantic: 'account_wallet',
    amountDecimal: '-9007199254740993.000000000000000001',
    currency: 'USD',
    queryStatus: 'success',
    observedAtMs: Date.UTC(2026, 10, 2, 8),
    aliyunAccount: {
      accountID: profile.accountID,
      identityType: 'RAMUser',
      proofReference: profile.proofReference,
      qualification:
        'caller_account_matched_operator_asserted_source_binding_not_key_or_payer_verified',
    },
  };
  const status = {
    ...emptyStatus,
    version: 1,
    adapter: protocol,
    endpoint: 'https://business.aliyuncs.com/',
    configured: true,
    state: 'success',
    managerDirectQuery: true,
    aliyun: profile,
    lastSuccess: observation,
  };
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_url: string, options: RequestInit) =>
      Response.json(
        options.method === 'POST'
          ? {
              id: 'controlled-job',
              state: 'complete',
              status: {
                ...status,
                state: 'auth_error',
                stale: true,
                attempt: { queryStatus: 'auth_error', observedAtMs: Date.UTC(2026, 10, 2, 9) },
              },
            }
          : status
      )
    )
  );
  let renderer!: ReactTestRenderer;
  await act(async () => {
    renderer = create(
      <OfficialBalancePanel
        base="http://fixture"
        managementKey="SYNTHETIC-MANAGEMENT"
        binding={binding}
        compatibleCandidate
      />
    );
  });
  try {
    const text = () => JSON.stringify(renderer.toJSON());
    expect(text()).toContain(observation.amountDecimal);
    expect(text()).toContain('USD');
    expect(text()).toContain('2026-11-02T08:00:00.000Z');
    expect(text()).toContain(observation.aliyunAccount.qualification);
    expect(text()).toContain('STS caller 账户已匹配');
    expect(text()).toContain('所选推理 Key 或最终 payer 未验证');
    await act(async () =>
      renderer.root
        .findAllByType('button')
        .find((n) => n.children.join('') === '手动查询余额')!
        .props.onClick()
    );
    expect(text()).toContain('stale');
    expect(text()).toContain('auth_error');
    expect(text()).toContain(observation.amountDecimal);
    expect(text()).toContain('2026-11-02T08:00:00.000Z');
    for (const invented of ['赠送部分', '充值部分', '平台可用标志', 'CNY', '已验证 Key 归属'])
      expect(text()).not.toContain(invented);
  } finally {
    await act(async () => renderer.unmount());
  }
});

it('keeps billing secrets until authenticated save readback, then clears them across adapter and authority context changes', async () => {
  let saved = false;
  let recover = false;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_url: string, options: RequestInit) => {
      if (options.method === 'PUT') {
        saved = true;
        return Response.json({});
      }
      if (saved && !recover) return Response.json({}, { status: 503 });
      return Response.json({
        ...emptyStatus,
        version: saved ? 2 : 1,
        adapter: protocol,
        endpoint: 'https://business.aliyuncs.com/',
        configured: true,
        state: 'configured',
        managerDirectQuery: true,
        aliyun: profile,
      });
    })
  );
  const panel = (authority: BalanceBinding, key = 'test') => (
    <OfficialBalancePanel
      base="http://fixture"
      managementKey={key}
      binding={authority}
      compatibleCandidate
    />
  );
  let renderer!: ReactTestRenderer;
  await act(async () => {
    renderer = create(panel(binding));
  });
  try {
    const input = (label: string) => renderer.root.findByProps({ 'aria-label': label });
    const button = (name: string) =>
      renderer.root.findAllByType('button').find((n) => n.children.join('') === name)!;
    const change = async (label: string, value: string) => {
      await act(async () => input(label).props.onChange({ target: { value } }));
    };
    const enter = async () => {
      await change('阿里账务 AK', 'SYNTHETIC-PENDING-AK');
      await change('阿里账务 SK', 'SYNTHETIC-PENDING-SK');
    };
    const empty = () => {
      expect(input('阿里账务 AK').props.value).toBe('');
      expect(input('阿里账务 SK').props.value).toBe('');
    };
    await enter();
    await act(async () => button('保存余额来源').props.onClick());
    expect(JSON.stringify(renderer.toJSON())).toContain('余额来源已保存，但回读失败');
    expect(input('阿里账务 AK').props.value).toBe('SYNTHETIC-PENDING-AK');
    expect(input('阿里账务 SK').props.value).toBe('SYNTHETIC-PENDING-SK');
    expect(renderer.root.findByType('fieldset').props.disabled).toBe(true);
    expect(button('保存余额来源').props.disabled).toBe(true);
    expect(button('手动查询余额').props.disabled).toBe(true);
    recover = true;
    await act(async () => button('重试回读').props.onClick());
    empty();
    await enter();
    await change('官方余额协议', 'deepinfra');
    expect(renderer.root.findAllByProps({ 'aria-label': '阿里账务 AK' })).toHaveLength(0);
    await change('官方余额协议', protocol);
    empty();
    await enter();
    const otherBinding = {
      ...binding,
      targets: [{ ...binding.targets[0], resource_id: 'other-issued-provider' }],
    };
    await act(async () => renderer.update(panel(otherBinding, 'other-authority')));
    empty();
    await act(async () => renderer.update(panel(binding)));
    empty();
    expect(JSON.stringify(renderer.toJSON())).not.toContain('SYNTHETIC-PENDING');
  } finally {
    await act(async () => renderer.unmount());
  }
});

it('marks retained cash stale after a query transport failure without changing its value or observation time', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_url: string, options: RequestInit) => {
      if (options.method === 'POST')
        throw new TypeError('synthetic upstream request details must stay private');
      return Response.json({
        ...emptyStatus,
        version: 1,
        adapter: protocol,
        endpoint: 'https://business.aliyuncs.com/',
        configured: true,
        state: 'success',
        managerDirectQuery: true,
        aliyun: profile,
        lastSuccess: {
          semantic: 'account_wallet',
          amountDecimal: '-0.000000000000000001',
          currency: 'JPY',
          queryStatus: 'success',
          observedAtMs: Date.UTC(2026, 10, 2, 8),
          aliyunAccount: {
            accountID: profile.accountID,
            identityType: 'RAMUser',
            proofReference: profile.proofReference,
            qualification:
              'caller_account_matched_operator_asserted_source_binding_not_key_or_payer_verified',
          },
        },
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
    expect(JSON.stringify(renderer.toJSON())).not.toContain('stale');
    await act(async () =>
      renderer.root
        .findAllByType('button')
        .find((n) => n.children.join('') === '手动查询余额')!
        .props.onClick()
    );
    const text = JSON.stringify(renderer.toJSON());
    expect(text).toContain('stale');
    expect(text).toContain('-0.000000000000000001');
    expect(text).toContain('JPY');
    expect(text).toContain('2026-11-02T08:00:00.000Z');
    expect(text).toContain('balance_request_failed_reload_identity');
    expect(text).not.toContain('synthetic upstream request details');
  } finally {
    await act(async () => renderer.unmount());
  }
});
