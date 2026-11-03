import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, expect, it, vi } from 'vitest';
import { buildProviderRows } from '../ProviderTable/rowData';
import { ProviderDetailDrawer } from './ProviderDetailDrawer';
import type { BalanceBinding } from '@/services/api/officialBalance';

const binding: BalanceBinding = {
  scope: {
    instance_id: '11111111-1111-4111-8111-111111111111',
    instance_incarnation: '22222222-2222-4222-8222-222222222222',
    boot_epoch: '33333333-3333-4333-8333-333333333333',
    snapshot_revision: 1,
  },
  targets: [4, 5].map((v) => ({
    resource_id: `${v}${v}${v}${v}${v}${v}${v}${v}-4444-4444-8444-444444444444`,
    incarnation: '55555555-5555-4555-8555-555555555555',
    identity_version: 1,
    credential_version: 1,
    config_version: 1,
    membership_version: v === 4 ? 1 : 0,
  })),
};
const policy = {
  ...binding,
  providerId: binding.targets[0].resource_id,
  subjectId: 'safe-subject',
  owner: 'cpa-manager.provider-balance.v1/fixture',
  enabled: false,
  effective: false,
  generation: 2,
  source: 'explicit',
  updatedAtMs: 1793700000000,
  bindingCurrent: true,
  locked: true,
  manualDisabled: true,
  ownPaused: true,
  applied: true,
  state: 'historical_pause',
  reason: 'explicit_recovery_required',
  coordination: { receiptPersisted: true, state: 'paused' },
  sources: [
    {
      scope: binding.scope,
      targets: [binding.targets[1]],
      version: 1,
      state: 'network_error',
      stale: true,
      lastSuccess: {
        queryStatus: 'success',
        observedAtMs: 1793700000000,
        balances: [{ currency: 'USD', total: '0.0500' }],
      },
    },
  ],
};
afterEach(() => vi.unstubAllGlobals());

it('provider detail shows durable OFF/history, manual restriction and exact stale per-member money', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) =>
      Response.json(
        url.includes('/provider-policy')
          ? policy
          : { ...binding, version: 0, state: 'needs_configuration', configured: false }
      )
    )
  );
  const row = buildProviderRows({
    gemini: [],
    codex: [],
    claude: [],
    vertex: [],
    openai: [
      {
        name: 'Synthetic Provider',
        baseUrl: 'https://api.deepseek.com/v1',
        resourceId: policy.providerId,
        apiKeyEntries: [{ apiKey: 'SYNTHETIC', resourceId: binding.targets[1].resource_id }],
      },
    ],
    usageByProvider: new Map(),
  })[0];
  let view!: ReactTestRenderer;
  await act(async () => {
    view = create(
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
  try {
    const text = JSON.stringify(view.toJSON());
    expect(text).toContain('资金自动保护');
    expect(text).toContain('已关闭 · 保留历史暂停');
    expect(text).toContain('人工限制存在');
    expect(text).toContain('USD 0.0500');
    expect(text).toContain('陈旧');
    expect(text).toContain('命令回执已持久化');
  } finally {
    await act(async () => view.unmount());
  }
});
