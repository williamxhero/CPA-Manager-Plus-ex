import { readFileSync, writeFileSync } from 'node:fs';
import { act, create } from 'react-test-renderer';
import { expect, it } from 'vitest';
import { ProviderMoneyPanel } from './ProviderMoneyPanel';

// Actual ordinary main, authenticated API and temporary SQLite; no fetch stub.
it.skipIf(!process.env.PROVIDER82_RECOVERY_LIVE_UI)(
  'explicitly restores only this policy through the production panel and main',
  async () => {
    const fixture = process.env.PROVIDER82_RECOVERY_LIVE_UI!;
    const props = JSON.parse(readFileSync(fixture, 'utf8'));
    let view!: ReturnType<typeof create>;
    const text = () => JSON.stringify(view.toJSON());
    const waitFor = async (condition: () => boolean) => {
      for (let i = 0; i < 400 && !condition(); i++)
        await act(async () => { await new Promise((resolve) => setTimeout(resolve, 50)); });
      expect(condition()).toBe(true);
    };
    const click = async (label: string) => {
      await act(async () => {
        const button = view.root.findAllByType('button').find((b) => b.children.join('') === label)!;
        expect(button.props.disabled).not.toBe(true);
        button.props.onClick();
      });
    };
    try {
      await act(async () => { view = create(<ProviderMoneyPanel {...props} />); });
      await waitFor(() => text().includes('已关闭 · 保留历史暂停'));
      expect(text()).toContain('USD 0.0500');
      await click('恢复本策略历史暂停');
      expect(view.root.findAllByProps({ role: 'alertdialog' })).toHaveLength(1);
      expect(text()).toContain('人工关闭和其他策略限制将保留');
      await click('确认仅恢复本策略暂停');
      await waitFor(() => text().includes('已显式恢复本策略暂停'));
      expect(text()).toContain('策略保持关闭');
      expect(text()).toContain('命令回执已持久化');
      writeFileSync(fixture + '.render.json', JSON.stringify(view.toJSON(), null, 2));
    } finally {
      if (view) await act(async () => view.unmount());
      writeFileSync(fixture + '.stop', 'stop owned fixture');
    }
  },
  45000
);
