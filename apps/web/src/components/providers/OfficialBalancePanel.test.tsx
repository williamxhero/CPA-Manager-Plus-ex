import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { OfficialBalancePanel } from './OfficialBalancePanel';
import { ProviderDetailDrawer } from './ProviderDetailDrawer/ProviderDetailDrawer';
import { buildProviderRows } from './ProviderTable/rowData';

const fixtureDir = process.env.OFFICIAL_BALANCE_UI_FIXTURE;
it.skipIf(!fixtureDir)(
  'provider config -> exact wallet -> auth failure retains stale wallet through live Manager/SQLite/TLS',
  async () => {
    const f = JSON.parse(readFileSync(`${fixtureDir}/fixture.json`, 'utf8'));
    expect(f.binding.targets).toHaveLength(2);
    const row = buildProviderRows({
      gemini: [],
      codex: [],
      claude: [],
      vertex: [],
      openai: [
        {
          name: 'Synthetic DeepSeek',
          baseUrl: 'https://api.deepseek.com/v1',
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
    for (
      let i = 0;
      i < 100 && !JSON.stringify(renderer.toJSON()).includes('needs_configuration');
      i++
    ) {
      await act(async () => {
        await new Promise((r) => setTimeout(r, 10));
      });
    }
    expect(JSON.stringify(renderer.toJSON())).toContain('needs_configuration');
    const waitText = async (text: string) => {
      for (let i = 0; i < 200 && !JSON.stringify(renderer.toJSON()).includes(text); i++) {
        await act(async () => {
          await new Promise((r) => setTimeout(r, 10));
        });
      }
      expect(JSON.stringify(renderer.toJSON())).toContain(text);
    };
    const change = async (label: string, value: string) => {
      await act(async () => {
        renderer.root.findByProps({ 'aria-label': label }).props.onChange({ target: { value } });
      });
    };
    const click = async (label: string) => {
      await act(async () => {
        const button = renderer.root
          .findAllByType('button')
          .find((node) => node.children.join('') === label)!;
        expect(button.props.disabled).toBeFalsy();
        button.props.onClick();
      });
    };
    await change('只读查询密钥', 'synthetic-query-canary');
    await change('查询 HTTPS 地址', f.endpoint);
    await change('允许的私网 IP', '127.0.0.1');
    await click('保存余额来源');
    await waitText('configured');
    // The editor clears after encrypted server write plus authenticated readback.
    expect(renderer.root.findByProps({ 'aria-label': '只读查询密钥' }).props.value).toBe('');
    await click('手动查询余额');
    await waitText('-0.000000001');
    expect(JSON.stringify(renderer.toJSON())).toContain('account_wallet');
    expect(JSON.stringify(renderer.toJSON())).not.toContain('synthetic-query-canary');
    await fetch(f.control, { method: 'PUT', body: JSON.stringify({ response: 'status:401' }) });
    await click('手动查询余额');
    await waitText('auth_error');
    await waitText('stale');
    expect(JSON.stringify(renderer.toJSON())).toContain('-0.000000001');
    expect(JSON.stringify(renderer.toJSON())).toContain('最近成功');
    await change('允许的私网 IP', '');
    expect(renderer.root.findByProps({ 'aria-label': '允许的私网 IP' }).props.value).toBe('');
    await click('保存余额来源');
    for (
      let i = 0;
      i < 100 && renderer.root.findAllByType('button').some((node) => node.props.disabled);
      i++
    ) {
      await act(async () => {
        await new Promise((r) => setTimeout(r, 10));
      });
    }
    const readback = await fetch(
      `${f.base}/usage-service/official-balance?binding=${encodeURIComponent(JSON.stringify(f.binding))}`,
      { headers: { Authorization: `Bearer ${f.managementKey}` } }
    ).then((r) => r.json());
    expect(readback.privateTargets ?? []).toEqual([]);
    // Switching target must not render the previous target's secret or wallet.
    await act(async () => {
      renderer.update(
        <OfficialBalancePanel base={f.base} managementKey={f.managementKey} binding={undefined} />
      );
    });
    expect(JSON.stringify(renderer.toJSON())).toContain('identity_unavailable');
    expect(JSON.stringify(renderer.toJSON())).not.toContain('-0.000000001');

    await act(async () => {
      renderer.unmount();
    });
  }
);

it('keeps an unimplemented source honestly unsupported, without configuration or query controls', async () => {
  let renderer!: ReactTestRenderer;
  await act(async () => {
    renderer = create(<OfficialBalancePanel base="" managementKey="" supported={false} />);
  });
  expect(JSON.stringify(renderer.toJSON())).toContain('unsupported');
  expect(renderer.root.findAllByType('input')).toHaveLength(0);
  expect(renderer.root.findAllByType('button')).toHaveLength(0);
  await act(async () => {
    renderer.unmount();
  });
});
