import { act, create, type ReactTestInstance, type ReactTestRenderer } from 'react-test-renderer';
import { expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { ProviderDetailDrawer } from './ProviderDetailDrawer/ProviderDetailDrawer';
import { buildProviderRows } from './ProviderTable/rowData';

const dir = process.env.NEWAPI_BALANCE_UI_FIXTURE;
it.skipIf(!dir)(
  'explicit confirmed NewAPI config -> real HTTP/SQLite/TLS native debt and source-authority clearing',
  async () => {
    const f = JSON.parse(readFileSync(`${dir}/fixture.json`, 'utf8'));
    expect(f.binding.targets).toHaveLength(2);
    const row = buildProviderRows({
      gemini: [],
      codex: [],
      claude: [],
      vertex: [],
      openai: [
        {
          name: 'Synthetic compatible candidate',
          baseUrl: 'https://api.liangrekui.com/v1',
          resourceId: f.binding.targets[0].resource_id,
          apiKeyEntries: [
            {
              apiKey: 'SYNTHETIC-INFERENCE-NOT-BILLING',
              resourceId: f.binding.targets[1].resource_id,
            },
          ],
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
          officialBalance={{ base: f.base, managementKey: f.managementKey, binding: f.binding }}
        />
      );
    });
    const textOf = (node: ReactTestInstance): string =>
      node.children.map((c) => (typeof c === 'string' ? c : textOf(c))).join('');
    const text = () => textOf(renderer.root.findByProps({ 'aria-label': '官方余额' }));
    const wait = async (want: string) => {
      for (
        let i = 0;
        i < 250 &&
        (!text().includes(want) ||
          renderer.root.findByProps({ 'aria-label': '官方余额' }).props['aria-busy']);
        i++
      )
        await act(async () => {
          await new Promise((r) => setTimeout(r, 10));
        });
      expect(text()).toContain(want);
    };
    const change = async (label: string, value: string) => {
      await act(async () => {
        renderer.root.findByProps({ 'aria-label': label }).props.onChange({ target: { value } });
      });
    };
    const check = async (label: string, checked: boolean) => {
      await act(async () => {
        renderer.root.findByProps({ 'aria-label': label }).props.onChange({ target: { checked } });
      });
    };
    const click = async (name: string) => {
      await act(async () => {
        const b = renderer.root.findAllByType('button').find((n) => n.children.join('') === name)!;
        expect(b.props.disabled).toBeFalsy();
        b.props.onClick();
      });
    };
    const read = () =>
      fetch(
        `${f.base}/usage-service/official-balance?binding=${encodeURIComponent(JSON.stringify(f.binding))}`,
        { headers: { Authorization: `Bearer ${f.managementKey}` } }
      ).then((r) => r.json());
    await wait('needs_configuration');
    // Branding only makes a manual configuration entry available, never selects a protocol.
    expect(renderer.root.findByProps({ 'aria-label': '官方余额协议' }).props.value).toBe('');
    expect(renderer.root.findByProps({ 'aria-label': '查询 HTTPS 地址' }).props.value).toBe('');
    await change('官方余额协议', 'newapi-user-self-v1');
    await change('实例协议版本', 'operator-confirmed-synthetic-v1');
    await change('查询账户范围', 'synthetic-account-A');
    await change('NewAPI 用户 ID', '42');
    await check('实例协议已确认', true);
    await change('只读查询密钥', 'synthetic-query-canary');
    await change('查询 HTTPS 地址', f.endpoint);
    await change('允许的私网 IP', '127.0.0.1');
    await click('保存余额来源');
    await wait('configured');
    expect(renderer.root.findByProps({ 'aria-label': '只读查询密钥' }).props.value).toBe('');
    expect((await read()).newAPI).toEqual({
      protocolConfirmed: true,
      instanceVersion: 'operator-confirmed-synthetic-v1',
      auth: 'user-pat-with-user-id-v1',
      userID: '42',
      accountScope: 'synthetic-account-A',
      nativeUnit: 'newapi_quota',
    });
    await click('手动查询余额');
    await wait('-9007199254740993');
    expect(text()).toContain('newapi_quota');
    expect(text()).toContain('不是货币');
    expect(text()).not.toContain('赠送部分');
    expect(text()).not.toContain('平台可用标志');
    expect(text()).not.toContain('synthetic-query-canary');
    await fetch(f.control, { method: 'PUT', body: JSON.stringify({ response: 'status:401' }) });
    await click('手动查询余额');
    await wait('auth_error');
    await wait('stale');
    expect(text()).toContain('-9007199254740993');
    await change('查询账户范围', 'synthetic-account-B');
    await click('保存余额来源');
    await wait('needs_configuration');
    const changed = await read();
    expect(changed.configured).toBe(false);
    expect(changed.lastSuccess).toBeUndefined();
    expect(changed.newAPI.accountScope).toBe('synthetic-account-B');
    expect(text()).not.toContain('-9007199254740993');
    await change('官方余额协议', 'newapi-token-usage-v1');
    await change('实例协议版本', 'operator-confirmed-token-v1');
    await check('实例协议已确认', true);
    await change('查询 HTTPS 地址', f.tokenEndpoint);
    await change('只读查询密钥', 'synthetic-query-canary');
    await click('保存余额来源');
    await wait('configured');
    for (const [response, state] of [
      [
        '{"code":true,"data":{"object":"token_usage","unlimited_quota":true,"total_available":100000000}}',
        'unlimited',
      ],
      ['{"code":true,"data":{"object":"token_usage","total_available":0}}', 'unknown'],
    ]) {
      await fetch(f.control, { method: 'PUT', body: JSON.stringify({ response }) });
      await click('手动查询余额');
      await wait(`原生额度（不是货币）：${state}`);
      const observed = (await read()).lastSuccess;
      expect(observed.semantic).toBe('key_remaining');
      expect(observed.nativeQuota.state).toBe(state);
      expect(observed.nativeQuota.remaining).toBeUndefined();
      expect(observed.currency).toBeUndefined();
      expect(observed.amountDecimal).toBeUndefined();
      expect(text()).not.toContain('100000000');
    }
    await act(async () => {
      renderer.unmount();
    });
  }
);
