import { act, create, type ReactTestInstance, type ReactTestRenderer } from 'react-test-renderer';
import { expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { OfficialBalancePanel } from './OfficialBalancePanel';
import { CommonQuotaInventoryPanel } from '@/components/quota/CommonQuotaInventoryPanel';
import { ProviderDetailDrawer } from './ProviderDetailDrawer/ProviderDetailDrawer';
import { buildProviderRows } from './ProviderTable/rowData';
import type { OfficialBalanceStatus } from '@/services/api/officialBalance';

const directory = process.env.WEB_BALANCE_UI_FIXTURE;

it.skipIf(!directory)(
  'web source UI -> public Manager API/SQLite/controlled BrowserSkill preserves exact stale captures',
  async () => {
    const f = JSON.parse(readFileSync(`${directory}/fixture.json`, 'utf8'));
    expect(f.synthetic).toBe(true);
    const binding = { scope: f.config.scope, targets: f.config.targets };
    const row = buildProviderRows({
      gemini: [],
      codex: [],
      claude: [],
      vertex: [],
      usageByProvider: new Map(),
      openai: [
        {
          name: 'Synthetic LAYSO',
          baseUrl: 'https://layso.ai/v1',
          resourceId: binding.targets[0].resource_id,
          apiKeyEntries: [],
        },
      ],
    })[0];
    let renderer!: ReactTestRenderer;
    let inventoryRenderer: ReactTestRenderer | undefined;
    const textOf = (node: ReactTestInstance): string =>
      node.children.map((c) => (typeof c === 'string' ? c : textOf(c))).join('');
    const section = () => renderer.root.findByProps({ 'aria-label': '官方余额' });
    const text = () => textOf(section());
    const button = (label: string) =>
      section()
        .findAllByType('button')
        .find((n) => n.children.join('') === label)!;
    const wait = async (value: string) => {
      for (let i = 0; i < 400 && (!text().includes(value) || section().props['aria-busy']); i++) {
        await act(async () => {
          await new Promise((resolve) => setTimeout(resolve, 10));
        });
      }
      expect(text()).toContain(value);
      expect(section().props['aria-busy']).toBeFalsy();
    };
    const change = async (label: string, value: string) => {
      await act(async () =>
        renderer.root.findByProps({ 'aria-label': label }).props.onChange({ target: { value } })
      );
    };
    const click = async (label: string) => {
      await act(async () => {
        expect(button(label).props.disabled).toBeFalsy();
        button(label).props.onClick();
      });
    };
    const read = async (): Promise<OfficialBalanceStatus> => {
      const r = await fetch(
        `${f.base}/usage-service/official-balance?binding=${encodeURIComponent(JSON.stringify(binding))}`,
        { headers: { Authorization: `Bearer ${f.managementKey}` } }
      );
      expect(r.status).toBe(200);
      return r.json();
    };
    const control = async (value: unknown) => {
      expect(
        (
          await fetch(f.control, {
            method: 'PUT',
            headers: { Authorization: `Bearer ${f.managementKey}` },
            body: JSON.stringify(value),
          })
        ).status
      ).toBe(204);
    };
    const exact = '-12345678901234567890.00000000100';
    try {
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
            officialBalance={{ base: f.base, managementKey: f.managementKey, binding }}
          />
        );
      });
      await wait('needs_configuration');
      await change('官方余额协议', 'layso_billing_web_v1');
      for (const [label, value] of [
        ['网页账户 ID', f.config.web.accountId],
        ['网页租户 ID', f.config.web.tenantId],
        ['网页角色', f.config.web.role],
        ['网页执行用户', f.config.web.executionUser],
        ['Edge 实例 ID', f.config.web.browser.instanceId],
        ['网页解析规则', f.config.web.parsingRule],
      ])
        await change(label, value);
      expect(renderer.root.findAllByProps({ 'aria-label': '只读查询密钥' })).toHaveLength(0);
      expect(renderer.root.findAllByProps({ 'aria-label': '允许的私网 IP' })).toHaveLength(0);
      expect(renderer.root.findByProps({ 'aria-label': '查询 HTTPS 地址' }).props.readOnly).toBe(
        true
      );
      await click('保存余额来源');
      await wait('web_source_verification_required');
      expect(button('手动查询余额').props.disabled).toBe(true);
      expect(text()).toContain('暂无成功观测');
      expect(text()).not.toContain('USD 0');
      const registered = await read();
      expect(registered.lastSuccess).toBeUndefined();
      expect(registered.configured).toBe(true);
      expect(JSON.stringify(registered)).not.toContain('secret');
      // The synthetic host dependency is enrolled outside the product's UI/API.
      await control({ enroll: true });
      await act(async () =>
        renderer.update(
          <OfficialBalancePanel
            base={f.base}
            managementKey={f.managementKey}
            binding={binding}
            compatibleCandidate
          />
        )
      );
      await wait('ready');
      await click('手动查询余额');
      await wait(exact);
      expect(text()).toContain('USD');
      expect(text()).toContain('网页账户余额');
      expect(text()).toContain('https://layso.ai/dashboard/models');
      expect(text()).toContain(f.config.web.accountId);
      expect(text()).toContain('controlled_fixture');
      expect(text()).not.toContain('平台可用标志');
      const first = await read();
      expect(first.lastSuccess?.amountDecimal).toBe(exact);
      const captured = first.lastSuccess;
      // Keep the exact capture mounted during a slow, then failed browser query.
      await control({ delayMs: 300, missing: true });
      await click('手动查询余额');
      expect(section().props['aria-busy']).toBe(true);
      expect(text()).toContain(exact);
      await wait('WEB_INVALID_AMOUNT');
      expect(text()).toContain('stale');
      expect(text()).toContain(exact);
      expect(captured?.observedAtMs).toBeTypeOf('number');
      expect(text()).toContain(new Date(captured!.observedAtMs!).toISOString());
      expect(text()).toContain('未产生采集时间');
      expect((await read()).lastSuccess).toEqual(captured);
      for (const amount of ['NaN', 'Infinity']) {
        await control({ amount });
        await click('手动查询余额');
        await wait('WEB_INVALID_AMOUNT');
        expect((await read()).lastSuccess).toEqual(captured);
        expect(text()).toContain(exact);
        expect(text()).not.toContain('USD 0');
      }
      await control({ stage: 'browser-closed' });
      await click('手动查询余额');
      await wait('WEB_BROWSER_UNAVAILABLE');
      expect(text()).toContain('stale');
      expect((await read()).lastSuccess).toEqual(captured);
      expect(text()).not.toContain('RAW-PAGE-CANARY');
      // The shared inventory must show the same persisted failed attempt and
      // precise historical value, not only the provider's private drawer.
      await act(async () => {
        inventoryRenderer = create(
          <CommonQuotaInventoryPanel base={f.base} managementKey={f.managementKey} />
        );
      });
      for (let i = 0; i < 300 && !textOf(inventoryRenderer!.root).includes(exact); i++) {
        await act(async () => {
          await new Promise((resolve) => setTimeout(resolve, 10));
        });
      }
      expect(textOf(inventoryRenderer!.root)).toContain(exact);
      expect(textOf(inventoryRenderer!.root)).toContain('WEB_BROWSER_UNAVAILABLE');
      expect(textOf(inventoryRenderer!.root)).toContain('stale');
      // Real zero is distinct from unknown and retains the page's trailing zeros.
      await control({ stage: '', amount: '0.00000' });
      await click('手动查询余额');
      await wait('0.00000');
      expect((await read()).stale).toBe(false);
      // Changing declared account invalidates the old successful capture/enrollment.
      await change('网页账户 ID', 'replacement-account');
      await click('保存余额来源');
      await wait('web_source_verification_required');
      expect(text()).toContain('暂无成功观测');
      expect(text()).not.toContain(exact);
      expect(text()).not.toContain('0.00000');
      expect(button('手动查询余额').props.disabled).toBe(true);
      await act(async () =>
        renderer.update(<OfficialBalancePanel base="" managementKey="" compatibleCandidate />)
      );
      expect(text()).toContain('identity_unavailable');
      expect(text()).not.toContain('replacement-account');
    } finally {
      if (inventoryRenderer) await act(async () => inventoryRenderer!.unmount());
      if (renderer) await act(async () => renderer.unmount());
    }
  },
  45000
);
