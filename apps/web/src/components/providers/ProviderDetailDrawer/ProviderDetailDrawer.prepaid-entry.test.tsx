import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, expect, it, vi } from 'vitest';
import type { BalanceBinding, OfficialBalanceStatus } from '@/services/api/officialBalance';
import { buildProviderRows } from '../ProviderTable/rowData';
import { ProviderDetailDrawer } from './ProviderDetailDrawer';

afterEach(() => vi.unstubAllGlobals());

const binding: BalanceBinding = {
  scope: {
    instance_id: 'synthetic-instance',
    instance_incarnation: 'synthetic-incarnation',
    boot_epoch: 'synthetic-boot',
    snapshot_revision: 1,
  },
  targets: [
    {
      resource_id: 'synthetic-provider',
      incarnation: 'synthetic-provider-incarnation',
      identity_version: 1,
      credential_version: 1,
      config_version: 1,
      membership_version: 1,
    },
  ],
};

const emptyStatus: OfficialBalanceStatus = {
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

async function renderProvider(baseUrl: string) {
  const row = buildProviderRows({
    gemini: [],
    codex: [],
    claude: [],
    vertex: [],
    openai: [
      {
        name: 'Synthetic AI Provider',
        baseUrl,
        resourceId: binding.targets[0].resource_id,
        apiKeyEntries: [{ apiKey: 'SYNTHETIC-INFERENCE-NOT-BILLING', resourceId: 'synthetic-key' }],
      },
    ],
    usageByProvider: new Map(),
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
        officialBalance={{
          base: 'http://synthetic-manager',
          managementKey: 'synthetic-admin',
          binding,
        }}
      />
    );
  });
  return renderer;
}

it('exposes a blank prepaid protocol editor for the saved Alibaba AI Provider entry without inferring billing identity', async () => {
  const fetch = vi.fn(async () => Response.json(emptyStatus));
  vi.stubGlobal('fetch', fetch);
  const renderer = await renderProvider('https://token-plan.cn-beijing.maas.aliyuncs.com/v1');
  try {
    const input = (label: string) => renderer.root.findByProps({ 'aria-label': label });
    expect(input('官方余额协议').props.value).toBe('');
    expect(input('查询 HTTPS 地址').props.value).toBe('');
    expect(input('只读查询密钥').props.value).toBe('');
    expect(JSON.stringify(renderer.toJSON())).toContain('不会按域名自动选择');
    expect(
      renderer.root
        .findAllByType('option')
        .map((option) => option.children.join(''))
        .join(' ')
    ).not.toMatch(/Coding Plan|Token Plan/i);
    await act(async () =>
      input('官方余额协议').props.onChange({ target: { value: 'aliyun-prepaid-cash-v2017-12-14' } })
    );
    for (const label of [
      '阿里云站点',
      '预期阿里云账户 ID',
      '阿里来源账户关联证据',
      '阿里账务 AK',
      '阿里账务 SK',
    ])
      expect(input(label).props.value).toBe('');
    expect(input('阿里来源账户关联已确认').props.checked).toBe(false);
    expect(renderer.root.findAllByProps({ 'aria-label': '换算币种' })).toHaveLength(0);
    expect(JSON.stringify(renderer.toJSON())).toContain('关联是管理员声明');
    expect(JSON.stringify(renderer.toJSON())).toContain('不证明所选推理 Key 归属');
    expect(
      renderer.root
        .findAllByType('button')
        .find((button) => button.children.join('') === '保存余额来源')?.props.disabled
    ).toBe(true);
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(fetch.mock.calls[0]).toEqual([
      expect.stringContaining('http://synthetic-manager/usage-service/official-balance?binding='),
      expect.objectContaining({ method: 'GET', body: undefined }),
    ]);
    expect(JSON.stringify(fetch.mock.calls)).not.toContain('SYNTHETIC-INFERENCE-NOT-BILLING');
  } finally {
    await act(async () => renderer.unmount());
  }
});

it.each([
  'http://token-plan.cn-beijing.maas.aliyuncs.com/v1',
  'https://token-plan.cn-beijing.maas.aliyuncs.com.untrusted.example/v1',
  'https://unsupported.example/v1',
  'not-a-url',
])(
  'keeps an unsupported AI Provider entry closed without probing or exposing billing controls: %s',
  async (baseUrl) => {
    const fetch = vi.fn(async () => Response.json(emptyStatus));
    vi.stubGlobal('fetch', fetch);
    const renderer = await renderProvider(baseUrl);
    try {
      expect(JSON.stringify(renderer.toJSON())).toContain('unsupported');
      expect(renderer.root.findAllByProps({ 'aria-label': '官方余额协议' })).toHaveLength(0);
      expect(renderer.root.findAllByProps({ 'aria-label': '阿里账务 AK' })).toHaveLength(0);
      expect(fetch).not.toHaveBeenCalled();
    } finally {
      await act(async () => renderer.unmount());
    }
  }
);
