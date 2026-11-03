import type { ProviderMoneyView } from './useProviderMoneySummaries';
import styles from './ProviderMoneySummary.module.scss';

const timeText = (ms?: number) => typeof ms === 'number' && Number.isFinite(ms) && ms > 0 && !Number.isNaN(new Date(ms).getTime())
  ? new Date(ms).toISOString() : '未知';
const moneyText = (currency: string, total: string) => /^[A-Z]{3}$/.test(currency) && typeof total === 'string' && /^-?\d+(?:\.\d+)?$/.test(total)
  ? `${currency} ${total}` : '金额未知';

export function ProviderMoneySummary({ view }: { view?: ProviderMoneyView }) {
  const data = view?.data;
  return <div className={styles.summary} aria-label="资金实时状态摘要" aria-live="polite">
    {view?.pending && <div role="status">正在核验 · 上次状态未确认</div>}
    {view?.failed && <div role="status">读取失败 · 上次状态未确认</div>}
    {!data ? <span>资金状态未知</span> : <>
      <div>{data.enabled ? '策略已启用' : '策略已关闭'} · {data.source === 'explicit' ? '显式设置' : '默认关闭'} · {data.locked ? '已锁定' : '未锁定'}</div>
      <div>{data.effective ? '策略可执行' : '策略未就绪'} · {data.ownPaused && data.applied ? '本策略已确认暂停' : data.applied ? '本策略已确认恢复' : '门禁结果未确认'}</div>
      <div>{data.manualDisabled ? '人工关闭' : '无人工关闭'} · {data.state} · {data.reason}</div>
      <div>设置时间：{timeText(data.updatedAtMs)}</div>
      {data.sources.length === 0 && <div>金额未知 · 来源未验证</div>}
      {data.sources.map(source => <div key={source.targets[0].resource_id}>
        <div>凭证 {source.targets[0].resource_id} · {source.state} · {source.stale === true ? '陈旧' : source.stale === false ? '当前' : '新鲜度未知'}</div>
        <div>lastSuccess：{timeText(source.lastSuccess?.observedAtMs)}</div>
        {source.lastSuccess?.balances?.length
          ? source.lastSuccess.balances.map((b, i) => <div key={i}>{moneyText(b.currency, b.total)}</div>)
          : <div>金额未知</div>}
      </div>)}
    </>}
  </div>;
}
