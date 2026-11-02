import { act, create, type ReactTestInstance, type ReactTestRenderer } from 'react-test-renderer';
import { expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { OfficialBalancePanel } from './OfficialBalancePanel';
import { ProviderDetailDrawer } from './ProviderDetailDrawer/ProviderDetailDrawer';
import { buildProviderRows } from './ProviderTable/rowData';
import type { OfficialBalanceStatus } from '@/services/api/officialBalance';

const directory = process.env.ALIYUN_BALANCE_UI_FIXTURE;

// Opt-in real public service seam, never a mock or real-account billing request.
it.skipIf(!directory)(
  'real Alibaba provider UI -> Manager/SQLite/Core/TLS cash, caller qualification and stale business failure',
  async () => {
    const fixture = JSON.parse(readFileSync(`${directory}/fixture.json`, 'utf8'));
    expect(fixture.synthetic).toBe(true);
    expect(fixture.adapter).toBe('aliyun-prepaid-cash-v2017-12-14');
    const credentials = JSON.parse(fixture.secret);
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
      const section = () => renderer.root.findByProps({ 'aria-label': '官方余额' });
      const text = () => textOf(section());
      const input = (label: string) => renderer.root.findByProps({ 'aria-label': label });
      const wait = async (value: string) => {
        for (let i = 0; i < 600 && (!text().includes(value) || section().props['aria-busy']); i++) {
          await act(async () => {
            await new Promise((resolve) => setTimeout(resolve, 25));
          });
        }
        expect(text()).toContain(value);
        expect(section().props['aria-busy']).toBeFalsy();
      };
      const change = async (label: string, value: string) => {
        await act(async () => input(label).props.onChange({ target: { value } }));
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
      const read = async (): Promise<OfficialBalanceStatus> => {
        const response = await fetch(
          `${fixture.base}/usage-service/official-balance?binding=${encodeURIComponent(JSON.stringify(fixture.binding))}`,
          {
            headers: { Authorization: `Bearer ${fixture.managementKey}` },
          }
        );
        expect(response.status).toBe(200);
        return response.json();
      };
      const control = async (value: unknown) => {
        const response = await fetch(fixture.control, {
          method: 'PUT',
          body: JSON.stringify(value),
        });
        expect(response.status).toBe(204);
      };
      const noSecrets = (value: string) => {
        expect(value).not.toContain(credentials.accessKeyID);
        expect(value).not.toContain(credentials.secretAccessKey);
        expect(value).not.toContain('SYNTHETIC-INFERENCE-NOT-BILLING');
      };

      await wait('needs_configuration');
      expect(input('官方余额协议').props.value).toBe('');
      await change('官方余额协议', fixture.adapter);
      expect(input('阿里云站点').props.value).toBe('');
      expect(input('预期阿里云账户 ID').props.value).toBe('');
      await change('阿里云站点', fixture.aliyun.site);
      await change('预期阿里云账户 ID', fixture.aliyun.accountID);
      await change('阿里来源账户关联证据', fixture.aliyun.proofReference);
      await act(async () =>
        input('阿里来源账户关联已确认').props.onChange({ target: { checked: true } })
      );
      await change('阿里账务 AK', credentials.accessKeyID);
      await change('阿里账务 SK', credentials.secretAccessKey);
      await click('保存余额来源');
      await wait('configured');
      expect(input('阿里账务 AK').props.value).toBe('');
      expect(input('阿里账务 SK').props.value).toBe('');
      const configured = await read();
      expect(configured.aliyun).toEqual(fixture.aliyun);
      expect(configured.configured).toBe(true);
      expect(configured.coreQuery).toBe(false);
      expect(configured.coreGate).toBe(false);
      noSecrets(JSON.stringify(configured));
      // Empty inputs preserve only the unchanged authority's encrypted credentials.
      await click('保存余额来源');
      await wait('configured');
      await click('手动查询余额');
      await wait('9007199254740993.000000000000000001');
      expect(text()).toContain('CNY');
      expect(text()).not.toContain('9007199254741093.000000000000000001');
      expect(text()).toContain('STS caller 账户已匹配');
      expect(text()).toContain('所选推理 Key 或最终 payer 未验证');
      const initial = await read();
      expect(initial.lastSuccess?.aliyunAccount).toEqual({
        accountID: fixture.aliyun.accountID,
        identityType: 'RAMUser',
        proofReference: fixture.aliyun.proofReference,
        qualification:
          'caller_account_matched_operator_asserted_source_binding_not_key_or_payer_verified',
      });

      await control({
        Status: 0,
        Response:
          '{"Code":"200","Success":true,"Data":{"AvailableCashAmount":"-0.000000000000000001","AvailableAmount":"99.00","Currency":"USD"}}',
      });
      await click('手动查询余额');
      await wait('-0.000000000000000001');
      expect(text()).toContain('USD');
      const before = await read();
      expect(before.lastSuccess?.amountDecimal).toBe('-0.000000000000000001');
      expect(before.lastSuccess?.currency).toBe('USD');

      await control({
        Status: 200,
        Response:
          '{"Code":"NoPermission","Success":false,"Message":"synthetic-denied-private-message"}',
      });
      await click('手动查询余额');
      await wait('auth_error');
      expect(text()).toContain('stale');
      expect(text()).toContain('-0.000000000000000001');
      expect(text()).toContain(new Date(before.lastSuccess!.observedAtMs).toISOString());
      for (const invented of [
        '赠送部分',
        '充值部分',
        '平台可用标志',
        'synthetic-denied-private-message',
      ]) {
        expect(text()).not.toContain(invented);
      }
      const failed = await read();
      expect(failed.stale).toBe(true);
      expect(failed.lastSuccess).toEqual(before.lastSuccess);
      noSecrets(JSON.stringify(failed));
      noSecrets(text());

      await control({
        IdentityStatus: 0,
        IdentityResponse: '{"AccountId":"1968132000123457","IdentityType":"RAMUser"}',
      });
      await click('手动查询余额');
      await wait('parse_error');
      const mismatched = await read();
      expect(mismatched.attempt?.errorCode).toBe('ALIYUN_ACCOUNT_MISMATCH');
      expect(mismatched.stale).toBe(true);
      expect(mismatched.lastSuccess).toEqual(before.lastSuccess);
      expect(text()).toContain('-0.000000000000000001');

      await change('阿里账务 AK', 'SYNTHETIC-UNSAVED-AK');
      await change('阿里账务 SK', 'SYNTHETIC-UNSAVED-SK');
      await act(async () =>
        renderer.update(<OfficialBalancePanel base="" managementKey="" compatibleCandidate />)
      );
      expect(JSON.stringify(renderer.toJSON())).toContain('identity_unavailable');
      expect(JSON.stringify(renderer.toJSON())).not.toContain('SYNTHETIC-UNSAVED');
      expect(JSON.stringify(renderer.toJSON())).not.toContain('-0.000000000000000001');
    } finally {
      await act(async () => renderer.unmount());
    }
  },
  90000
);
