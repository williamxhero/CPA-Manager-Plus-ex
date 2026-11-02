import { act, create, type ReactTestInstance, type ReactTestRenderer } from 'react-test-renderer';
import { expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { OfficialBalancePanel } from './OfficialBalancePanel';
import { ProviderDetailDrawer } from './ProviderDetailDrawer/ProviderDetailDrawer';
import { buildProviderRows } from './ProviderTable/rowData';

const directory = process.env.VOLCENGINE_BALANCE_UI_FIXTURE;
it.skipIf(!directory)(
  'real UI -> Manager/SQLite/Core/TLS prepaid config, exact debt and stale authentication failure',
  async () => {
    const fixture = JSON.parse(readFileSync(`${directory}/fixture.json`, 'utf8'));
    expect(fixture.synthetic).toBe(true);
    const credentials = JSON.parse(fixture.secret);
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
          resourceId: fixture.binding.targets[0].resource_id,
          apiKeyEntries: [
            {
              apiKey: 'SYNTHETIC-INFERENCE-NOT-BILLING',
              resourceId: fixture.binding.targets[1].resource_id,
            },
          ],
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
          officialBalance={{
            base: fixture.base,
            managementKey: fixture.managementKey,
            binding: fixture.binding,
          }}
        />
      );
    });
    try {
      const textOf = (node: ReactTestInstance): string =>
        node.children.map((child) => (typeof child === 'string' ? child : textOf(child))).join('');
      const text = () => textOf(renderer.root.findByProps({ 'aria-label': '官方余额' }));
      const wait = async (value: string) => {
        for (
          let i = 0;
          i < 600 &&
          (!text().includes(value) ||
            renderer.root.findByProps({ 'aria-label': '官方余额' }).props['aria-busy']);
          i++
        )
          await act(async () => {
            await new Promise((resolve) => setTimeout(resolve, 25));
          });
        expect(text()).toContain(value);
        expect(
          renderer.root.findByProps({ 'aria-label': '官方余额' }).props['aria-busy']
        ).toBeFalsy();
      };
      const change = async (label: string, value: string) => {
        await act(async () =>
          renderer.root.findByProps({ 'aria-label': label }).props.onChange({ target: { value } })
        );
      };
      const check = async (label: string) => {
        await act(async () =>
          renderer.root
            .findByProps({ 'aria-label': label })
            .props.onChange({ target: { checked: true } })
        );
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
      const read = async () => {
        const response = await fetch(
          `${fixture.base}/usage-service/official-balance?binding=${encodeURIComponent(JSON.stringify(fixture.binding))}`,
          { headers: { Authorization: `Bearer ${fixture.managementKey}` } }
        );
        expect(response.status).toBe(200);
        return response.json();
      };
      await wait('needs_configuration');
      expect(renderer.root.findByProps({ 'aria-label': '官方余额协议' }).props.value).toBe('');
      await change('官方余额协议', fixture.adapter);
      await change('资金账户 ID', fixture.volcengine.accountID);
      await change('火山账务币种', 'CNY');
      await change('来源账户关联证据', fixture.volcengine.proofReference);
      await check('币种与主单位已确认');
      await check('来源账户关联已确认');
      await change('火山账务 AK', credentials.accessKeyID);
      await change('火山账务 SK', credentials.secretAccessKey);
      await click('保存余额来源');
      await wait('configured');
      expect(renderer.root.findByProps({ 'aria-label': '火山账务 AK' }).props.value).toBe('');
      expect(renderer.root.findByProps({ 'aria-label': '火山账务 SK' }).props.value).toBe('');
      await click('手动查询余额');
      await wait('77.01');
      const debt = '-0.000000000000000001';
      const controlled = await fetch(fixture.control, {
        method: 'PUT',
        body: JSON.stringify({
          Status: 0,
          Response: `{"ResponseMetadata":{},"Result":{"AccountID":${fixture.volcengine.accountID},"AvailableBalance":"${debt}"}}`,
        }),
      });
      expect(controlled.ok).toBe(true);
      await click('手动查询余额');
      await wait(debt);
      const before = await read();
      expect(before.lastSuccess.amountDecimal).toBe(debt);
      expect(before.lastSuccess.currency).toBe('CNY');
      expect(before.lastSuccess.volcengineAccount.accountID).toBe(fixture.volcengine.accountID);
      const failure = await fetch(fixture.control, {
        method: 'PUT',
        body: JSON.stringify({ Status: 403, Response: '{"error":"synthetic-denied"}' }),
      });
      expect(failure.ok).toBe(true);
      await click('手动查询余额');
      await wait('auth_error');
      expect(text()).toContain('stale');
      expect(text()).toContain(debt);
      expect(text()).not.toContain('赠送部分');
      expect(text()).not.toContain('充值部分');
      expect(text()).not.toContain('平台可用标志');
      const after = await read();
      expect(after.lastSuccess.observedAtMs).toBe(before.lastSuccess.observedAtMs);
      expect(JSON.stringify(after)).not.toContain(credentials.accessKeyID);
      expect(JSON.stringify(after)).not.toContain(credentials.secretAccessKey);
      await act(async () => {
        renderer.update(
          <OfficialBalancePanel base="" managementKey="" binding={undefined} compatibleCandidate />
        );
      });
      expect(JSON.stringify(renderer.toJSON())).toContain('identity_unavailable');
      expect(JSON.stringify(renderer.toJSON())).not.toContain(debt);
    } finally {
      await act(async () => renderer.unmount());
    }
  },
  90000
);
