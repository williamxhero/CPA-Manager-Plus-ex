import fs from 'node:fs';
import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { expect, it, vi } from 'vitest';
import { ProviderTable } from './ProviderTable';
import { buildProviderRows } from './rowData';
import type { ProviderPolicyStatus } from '@/services/api/providerPolicy';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (s: string) =>
      ({
        'ai_providers.money_summary_aria': '资金实时状态摘要',
        'ai_providers.money_summary_explicit': '显式设置',
        'ai_providers.money_summary_stale': '陈旧',
        'ai_providers.money_summary_last_success': 'lastSuccess：{{time}}',
        'ai_providers.money_summary_amount_unknown': '金额未知',
        'ai_providers.money_summary_default_off': '默认关闭',
      } as Record<string, string>)[s] ?? s,
  }),
}));
const fixture = process.env.PROVIDER82_ROWS_LIVE_UI;
it.skipIf(!fixture)(
  'renders every issued row through real fetch to ordinary main and preserves A/B amount isolation',
  async () => {
    const f: { base: string; managementKey: string; states: ProviderPolicyStatus[] } = JSON.parse(
      fs.readFileSync(fixture!, 'utf8')
    );
    const rows = buildProviderRows({
      gemini: [],
      codex: [],
      claude: [],
      vertex: [],
      usageByProvider: new Map(),
      openai: f.states.map((s, i) => ({
        name: `controlled-row-${i}`,
        baseUrl: 'https://synthetic.invalid',
        resourceId: s.providerId,
        apiKeyEntries: s.targets
          .filter((t) => t.resource_id !== s.providerId)
          .map((t) => ({ resourceId: t.resource_id, apiKey: 'synthetic-display-only' })),
      })),
    });
    let view!: ReactTestRenderer;
    try {
      await act(async () => {
        view = create(
          <ProviderTable
            rows={rows}
            loading={false}
            actionsDisabled={false}
            toggleDisabled={false}
            resolvedTheme="light"
            emptyState={null}
            onShowDetail={() => {
              throw new Error('no detail open expected');
            }}
            onEdit={() => {}}
            onDelete={() => {}}
            onToggle={() => {
              throw new Error('read-only');
            }}
            onPriorityChange={() => {}}
            moneyStatus={{
              base: f.base,
              managementKey: f.managementKey,
              bindingForRow: (r) => f.states.find((s) => s.providerId === r.raw.resourceId),
            }}
          />
        );
      });
      for (
        const deadline = Date.now() + 20000;
        !JSON.stringify(view.toJSON()).includes('USD 0.123456789012345678');

      ) {
        if (Date.now() > deadline) throw new Error('ordinary main summary unavailable');
        await act(async () => {
          await new Promise((r) => setTimeout(r, 50));
        });
      }
      const summaries = view.root.findAll(
        (n) => n.type === 'div' && n.props['aria-label'] === '资金实时状态摘要'
      );
      expect(summaries).toHaveLength(2);
      const words = (i: number) =>
        summaries[i]
          .findAll((n) => n.type === 'div')
          .flatMap((n) => n.children.filter((c) => typeof c === 'string'))
          .join(' ');
      expect(words(0)).toContain('USD 0.123456789012345678');
      expect(words(0)).toContain('陈旧');
      expect(words(0)).toContain('lastSuccess');
      expect(words(1)).toContain('金额未知');
      expect(words(1)).not.toContain('USD');
      expect(words(0)).toContain('显式设置');
      expect(words(1)).toContain('显式设置');
    } finally {
      if (view) await act(async () => view.unmount());
    }
  }
);
