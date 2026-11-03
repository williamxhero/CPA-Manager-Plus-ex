import { readFileSync, writeFileSync } from 'node:fs';
import { act, create } from 'react-test-renderer';
import { expect, it } from 'vitest';
import { ProviderMoneyPanel } from './ProviderMoneyPanel';

// This opt-in seam uses real fetch to the ordinary built Manager main; no
// in-process API replacement, mocked service or private SQLite read is used.
it.skipIf(!process.env.PROVIDER82_LIVE_UI)(
  'ordinary main exposes persisted provider state to the production panel',
  async () => {
    const fixture = process.env.PROVIDER82_LIVE_UI!;
    const props = JSON.parse(readFileSync(fixture, 'utf8'));
    let view!: ReturnType<typeof create>;
    try {
      await act(async () => {
        view = create(<ProviderMoneyPanel {...props} />);
      });
      for (let i = 0; i < 100 && !JSON.stringify(view.toJSON()).includes('保留历史暂停'); i++)
        await act(async () => {
          await new Promise((resolve) => setTimeout(resolve, 30));
        });
      const rendered = JSON.stringify(view.toJSON(), null, 2);
      expect(rendered).toContain('已关闭 · 保留历史暂停');
      expect(rendered).toContain('已确认暂停');
      expect(rendered).toContain('命令回执已持久化');
      expect(rendered).toContain('USD 0.0500');
      expect(rendered).toContain('陈旧');
      expect(rendered).toContain('lastSuccess');
      writeFileSync(fixture + '.render.json', rendered);
    } finally {
      if (view) await act(async () => view.unmount());
      writeFileSync(fixture + '.stop', 'stop owned fixture');
    }
  }
);
