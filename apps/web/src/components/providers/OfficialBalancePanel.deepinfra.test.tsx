import { act, create, type ReactTestInstance, type ReactTestRenderer } from 'react-test-renderer';
import { expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { ProviderDetailDrawer } from './ProviderDetailDrawer/ProviderDetailDrawer';
import { buildProviderRows } from './ProviderTable/rowData';
const dir = process.env.DEEPINFRA_BALANCE_UI_FIXTURE;
it.skipIf(!dir)(
  'DeepInfra explicit protocol -> exact signed prepaid debt -> stale failures, no fabricated wallet parts',
  async () => {
    const f = JSON.parse(readFileSync(`${dir}/fixture.json`, 'utf8'));
    let renderer!: ReactTestRenderer;
    expect(f.binding.targets).toHaveLength(2);
    const row = buildProviderRows({
      gemini: [],
      codex: [],
      claude: [],
      vertex: [],
      openai: [
        {
          name: 'Synthetic DeepInfra',
          baseUrl: 'https://api.deepinfra.com/v1/openai',
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
      node.children.map((child) => (typeof child === 'string' ? child : textOf(child))).join('');
    const text = () => textOf(renderer.root.findByProps({ 'aria-label': '官方余额' }));
    const waitText = async (value: string) => {
      for (
        let i = 0;
        i < 200 && renderer.root.findByProps({ 'aria-label': '官方余额' }).props['aria-busy'];
        i++
      )
        await act(async () => {
          await new Promise((r) => setTimeout(r, 10));
        });
      for (let i = 0; i < 200 && !text().includes(value); i++)
        await act(async () => {
          await new Promise((r) => setTimeout(r, 10));
        });
      expect(text()).toContain(value);
    };
    const change = async (label: string, value: string) => {
      await act(async () => {
        renderer.root.findByProps({ 'aria-label': label }).props.onChange({ target: { value } });
      });
    };
    const click = async (label: string) => {
      const button = () =>
        renderer.root.findAllByType('button').find((n) => n.children.join('') === label)!;
      for (let i = 0; i < 200 && button().props.disabled; i++)
        await act(async () => {
          await new Promise((r) => setTimeout(r, 10));
        });
      await act(async () => {
        expect(button().props.disabled).toBeFalsy();
        button().props.onClick();
      });
    };
    await waitText('needs_configuration');
    const initial = await fetch(
      `${f.base}/usage-service/official-balance?binding=${encodeURIComponent(JSON.stringify(f.binding))}`,
      { headers: { Authorization: `Bearer ${f.managementKey}` } }
    ).then((r) => r.json());
    expect(initial.configured).toBe(false);
    expect(initial.lastSuccess).toBeUndefined();
    expect(initial.attempt).toBeUndefined();
    const partial = await fetch(`${f.base}/usage-service/official-balance`, {
      method: 'PUT',
      headers: { Authorization: `Bearer ${f.managementKey}`, 'Content-Type': 'application/json' },
      body: JSON.stringify({
        ...f.binding,
        targets: [f.binding.targets[0]],
        version: 0,
        adapter: 'deepinfra',
        endpoint: f.endpoint,
      }),
    });
    expect(partial.status).toBe(409);
    expect(renderer.root.findByProps({ 'aria-label': '官方余额协议' }).props.value).toBe(
      'deepinfra'
    );
    await change('官方余额协议', 'deepinfra');
    expect(renderer.root.findByProps({ 'aria-label': '查询 HTTPS 地址' }).props.value).toBe(
      'https://api.deepinfra.com/payment/checklist'
    );
    await change('只读查询密钥', 'synthetic-query-canary');
    await change('查询 HTTPS 地址', f.endpoint);
    await change('允许的私网 IP', '127.0.0.1');
    await click('保存余额来源');
    await waitText('configured');
    expect(renderer.root.findByProps({ 'aria-label': '只读查询密钥' }).props.value).toBe('');
    expect(renderer.root.findByProps({ 'aria-label': '官方余额协议' }).props.value).toBe(
      'deepinfra'
    );
    await click('手动查询余额');
    await waitText('-1.5');
    expect(text()).toContain('signed_prepaid_net');
    expect(text()).toContain('USD');
    expect(text()).toContain('欠款');
    expect(text()).not.toContain('赠送部分');
    expect(text()).not.toContain('充值部分');
    expect(text()).not.toContain('平台可用标志');
    expect(text()).not.toContain('synthetic-query-canary');
    const read = () =>
      fetch(
        `${f.base}/usage-service/official-balance?binding=${encodeURIComponent(JSON.stringify(f.binding))}`,
        { headers: { Authorization: `Bearer ${f.managementKey}` } }
      ).then((r) => r.json());
    const persisted = await read();
    expect(persisted.lastSuccess.amountDecimal).toBe('-1.5');
    expect(persisted.targets).toEqual(f.binding.targets);
    expect(persisted.scope).toEqual(f.binding.scope);
    expect(persisted.managerDirectQuery).toBe(true);
    expect(persisted.coreGate).toBe(false);
    expect(persisted.coreQuery).toBe(false);
    for (const [response, state] of [
      ['status:401', 'auth_error'],
      ['status:429', 'rate_limited'],
      ['<html>secret</html>', 'parse_error'],
    ]) {
      await fetch(f.control, { method: 'PUT', body: JSON.stringify({ response }) });
      await click('手动查询余额');
      await waitText(state);
      await waitText('stale');
      expect(text()).toContain('-1.5');
      expect(text()).toContain('最近成功');
    }
    await fetch(f.control, {
      method: 'PUT',
      body: JSON.stringify({
        response: '{"stripe_balance":-12345678901234567890.000000001,"recent":0.0000000001}',
      }),
    });
    await click('手动查询余额');
    await waitText('12345678901234567890.0000000009');
    // Empty preserves the explicit encrypted credential, clear is separately requested.
    await click('保存余额来源');
    for (
      let i = 0;
      i < 200 && renderer.root.findAllByType('button').some((n) => n.props.disabled);
      i++
    )
      await act(async () => {
        await new Promise((r) => setTimeout(r, 10));
      });
    expect((await read()).configured).toBe(true);
    await act(async () => {
      renderer.root
        .findByProps({ 'aria-label': '明确清除查询密钥' })
        .props.onChange({ target: { checked: true } });
    });
    await click('保存余额来源');
    await waitText('needs_configuration');
    expect((await read()).configured).toBe(false);
    await change('只读查询密钥', 'synthetic-query-canary');
    await click('保存余额来源');
    await waitText('configured');
    expect((await read()).configured).toBe(true);
    // Changing protocol must not carry credentials or old source results.
    await change('官方余额协议', 'deepseek');
    expect(renderer.root.findByProps({ 'aria-label': '查询 HTTPS 地址' }).props.value).toBe(
      'https://api.deepseek.com/user/balance'
    );
    await click('保存余额来源');
    await waitText('needs_configuration');
    const replaced = await read();
    expect(replaced.adapter).toBe('deepseek');
    expect(replaced.configured).toBe(false);
    expect(replaced.lastSuccess).toBeUndefined();
    expect(text()).not.toContain('12345678901234567890.0000000009');
    await act(async () => {
      renderer.unmount();
    });
  }
);
