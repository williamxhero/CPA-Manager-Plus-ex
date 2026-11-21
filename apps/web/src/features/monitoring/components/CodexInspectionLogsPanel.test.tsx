import { createRef } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import {
  type InspectionLogViewEntry,
  toLocalInspectionLogViewEntry,
  toServerInspectionLogViewEntry,
} from '@/features/monitoring/model/codexInspectionPresentation';
import { CodexInspectionLogsPanel } from './CodexInspectionLogsPanel';

const logs: InspectionLogViewEntry[] = [
  {
    id: 'info-log',
    level: 'info',
    message: '正在加载认证文件，目标类型：Codex + xAI',
    timestamp: Date.UTC(2026, 6, 23, 12, 0, 0),
  },
  {
    id: 'error-log',
    level: 'error',
    message: 'xai@example.com -> 禁用（真实推理检查：消费额度或积分已用尽）',
    detail: '{"provider":"xai","inspectionMode":"真实推理检查"}',
    timestamp: Date.UTC(2026, 6, 23, 12, 0, 1),
  },
];

const renderPanel = ({
  logsCollapsed = false,
  levelFilter = 'all',
  entries = logs,
}: {
  logsCollapsed?: boolean;
  levelFilter?: 'all' | 'info' | 'success' | 'warning' | 'error';
  entries?: InspectionLogViewEntry[];
} = {}) =>
  renderToStaticMarkup(
    <CodexInspectionLogsPanel
      logs={entries}
      logsCollapsed={logsCollapsed}
      levelFilter={levelFilter}
      logListRef={createRef<HTMLDivElement>()}
      locale="zh-CN"
      t={i18n.getFixedT('zh-CN')}
      onLevelFilterChange={vi.fn()}
      onToggleCollapsed={vi.fn()}
    />
  );

describe('CodexInspectionLogsPanel', () => {
  it('renders concise summaries and keeps structured data behind details', () => {
    const markup = renderPanel();

    expect(markup).toContain('正在加载认证文件，目标类型：Codex + xAI');
    expect(markup).toContain('xai@example.com -&gt; 禁用（真实推理检查：消费额度或积分已用尽）');
    expect(markup).toContain('<details');
    expect(markup).toContain('详情');
    expect(markup).toContain('inspectionMode');
  });

  it('applies the same level filter to local and server log entries', () => {
    const markup = renderPanel({ levelFilter: 'error' });

    expect(markup).not.toContain('正在加载认证文件，目标类型：Codex + xAI');
    expect(markup).toContain('xai@example.com -&gt; 禁用（真实推理检查：消费额度或积分已用尽）');
  });

  it('distinguishes an empty level filter from an inspection that has not started', () => {
    const markup = renderPanel({ levelFilter: 'warning' });

    expect(markup).toContain('当前级别筛选下没有日志');
    expect(markup).not.toContain('尚未开始巡检');
  });

  it('uses the shared collapsed presentation without rendering log details', () => {
    const markup = renderPanel({ logsCollapsed: true });

    expect(markup).toContain('巡检日志已折叠，共 2 条。');
    expect(markup).not.toContain('<details');
    expect(markup).not.toContain('xai@example.com');
  });

  it('updates a persisted server review hint in the summary and details without changing diagnostics', () => {
    const reason = '同一认证文件下存在多个不同建议动作，文件级处理已阻止，请到凭证管理中手动处理';
    const diagnostic = 'upstream rejected the request: 凭证管理 [HTTP 503]';
    const stored = {
      id: 1,
      runId: 3,
      level: 'warning',
      message: '手动处理账号跳过',
      detail: {
        displayAccount: 'review@example.com',
        action: 'disable',
        status: 'needs_review',
        reason,
        error: diagnostic,
      },
      createdAtMs: Date.UTC(2026, 6, 23, 12),
    };
    const entry = toServerInspectionLogViewEntry(stored, null, i18n.getFixedT('zh-CN'));
    const markup = renderPanel({ entries: [entry] });
    const newReason =
      '同一认证文件下存在多个不同建议动作，文件级处理已阻止，请到额度接入管理中手动处理';

    expect(markup).toContain(`review@example.com 禁用需人工复核：${newReason}`);
    expect(markup).toContain(`&quot;reason&quot;:&quot;${newReason}&quot;`);
    expect(markup).toContain(`&quot;error&quot;:&quot;${diagnostic}&quot;`);
    expect(markup).not.toContain(reason);
    expect(stored.detail.reason).toBe(reason);
  });

  it('preserves the interpolated rollback failure while updating a saved local restore hint', () => {
    const reason = 'service unavailable [HTTP 503]';
    const historical = `自动禁用所有权保存失败，且禁用回滚失败：${reason}；请到凭证管理中手动恢复`;
    const entry = toLocalInspectionLogViewEntry(
      {
        id: 'local-restore',
        level: 'error',
        message: `restore@example.com 禁用失败：${historical}`,
        detail: { error: historical },
        timestamp: Date.UTC(2026, 6, 23, 12),
      },
      i18n.getFixedT('zh-CN')
    );
    const markup = renderPanel({ entries: [entry] });

    expect(markup).toContain(
      `restore@example.com 禁用失败：自动禁用所有权保存失败，且禁用回滚失败：${reason}；请到额度接入管理中手动恢复`
    );
    expect(markup).not.toContain(historical);
  });
});
