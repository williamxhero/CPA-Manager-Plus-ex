import { useEffect, useMemo, useRef, useState } from 'react';
import {
  providerBindingKey,
  providerPolicyRequest,
  type ProviderPolicyStatus,
} from '@/services/api/providerPolicy';
import type { OfficialBalancePanelProps } from './OfficialBalancePanel';
import type { BalanceBinding } from '@/services/api/officialBalance';
import styles from './ProviderDetailDrawer/ProviderDetailDrawer.module.scss';

const timeText = (ms?: number) =>
  typeof ms === 'number' && Number.isFinite(ms) && ms > 0 && !Number.isNaN(new Date(ms).getTime())
    ? new Date(ms).toISOString()
    : '未知';
const moneyText = (currency: string, total: string) =>
  typeof currency === 'string' &&
  /^[A-Z]{3}$/.test(currency) &&
  typeof total === 'string' &&
  /^-?\d+(?:\.\d+)?$/.test(total)
    ? `${currency} ${total}`
    : '金额未知';

export function ProviderMoneyPanel({
  base,
  managementKey,
  binding,
  providerId,
  disabled = false,
}: OfficialBalancePanelProps & { providerId: string }) {
  const bindingKey = binding ? providerBindingKey(binding) : '';
  // Auth/capability changes create a fresh monotonic context even after A->B->A.
  const context = useMemo(
    () => ({ base, managementKey, bindingKey, providerId, disabled }),
    [base, managementKey, bindingKey, providerId, disabled]
  );
  // Keep the actual issued DTO separate from its comparison key.
  const bindingJSON = JSON.stringify(binding);
  const issued = useMemo(
    () => (bindingJSON ? (JSON.parse(bindingJSON) as BalanceBinding) : undefined),
    [bindingJSON]
  );
  const current = useRef(context);
  const serial = useRef(0);
  const active = useRef<AbortController | null>(null);
  const [view, setView] = useState<{
    context: typeof context;
    data?: ProviderPolicyStatus;
    error?: string;
    busy?: boolean;
    verified?: boolean;
  }>({ context });
  const visible = view.context === context ? view : { context };
  const usable = Boolean(issued && base && managementKey && !disabled);

  useEffect(() => {
    current.current = context;
    let mounted = true;
    const load = async () => {
      if (!usable || !issued || active.current) return;
      const controller = new AbortController();
      active.current = controller;
      const id = ++serial.current;
      setView((v) => ({ ...(v.context === context ? v : {}), context, busy: true }));
      try {
        const data = await providerPolicyRequest(
          base,
          managementKey,
          issued,
          providerId,
          controller.signal
        );
        if (mounted && current.current === context && serial.current === id)
          setView({ context, data, verified: true });
      } catch {
        if (mounted && current.current === context && serial.current === id)
          setView((v) => ({
            ...v,
            error: '读取失败，显示上次状态；请刷新确认',
            busy: false,
            verified: false,
          }));
      } finally {
        if (active.current === controller) active.current = null;
      }
    };
    void load();
    const timer = setInterval(() => {
      void load();
    }, 15000);
    return () => {
      mounted = false;
      active.current?.abort();
      active.current = null;
      clearInterval(timer);
    };
  }, [context, usable, issued, base, managementKey, providerId]);

  const request = async (enabled?: boolean) => {
    if (
      current.current !== context ||
      !usable ||
      !issued ||
      active.current ||
      (enabled !== undefined && (!visible.data || !visible.verified))
    )
      return;
    const controller = new AbortController();
    active.current = controller;
    const id = ++serial.current;
    const valid = () =>
      current.current === context && serial.current === id && !controller.signal.aborted;
    setView((v) => ({ ...v, busy: true, error: undefined }));
    let writeFailed = false;
    try {
      let data: ProviderPolicyStatus;
      if (enabled !== undefined && visible.data) {
        try {
          data = await providerPolicyRequest(
            base,
            managementKey,
            issued,
            providerId,
            controller.signal,
            { enabled, expectedGeneration: visible.data.generation }
          );
        } catch {
          if (!valid()) return;
          writeFailed = true;
          data = await providerPolicyRequest(
            base,
            managementKey,
            issued,
            providerId,
            controller.signal
          );
        }
      } else
        data = await providerPolicyRequest(
          base,
          managementKey,
          issued,
          providerId,
          controller.signal
        );
      if (valid())
        setView({
          context,
          data,
          verified: true,
          error: writeFailed ? '保存未确认，已回读持久状态' : undefined,
        });
    } catch {
      if (valid())
        setView((v) => ({
          ...v,
          busy: false,
          verified: false,
          error: '请求失败，显示上次状态；请刷新确认',
        }));
    } finally {
      if (active.current === controller) active.current = null;
    }
  };
  const data = visible.data;
  return (
    <section className={styles.section} aria-label="资金自动保护">
      <h4 className={styles.sectionTitle}>资金自动保护</h4>
      <p>
        逐凭证余额不高于 0.1 原币时暂停；新的可信余额高于阈值时恢复本策略暂停。关闭后保留历史暂停。
      </p>
      {!usable && <p>需要当前完整资源身份与可用管理服务</p>}
      {usable && !data && <p>{visible.busy ? '读取资金保护状态…' : '状态未知'}</p>}
      {data && (
        <>
          <p>{data.enabled ? '已启用' : data.ownPaused ? '已关闭 · 保留历史暂停' : '已关闭'}</p>
          <p>
            {data.effective ? '策略可执行' : '策略未就绪'} ·{' '}
            {data.applied ? (data.ownPaused ? '已确认暂停' : '已确认恢复') : '门禁结果未确认'}
          </p>
          <p>
            {data.manualDisabled ? '人工限制存在' : '无人工限制'} ·{' '}
            {data.coordination?.receiptPersisted ? '命令回执已持久化' : '命令回执未持久化'}
          </p>
          <p>
            状态：{data.state} · {data.reason} · 保存时间：{timeText(data.updatedAtMs)}
          </p>
          {data.sources.map((s, i) => (
            <div key={s.targets?.[0]?.resource_id ?? i}>
              <p>
                凭证 {s.targets?.[0]?.resource_id ?? '未知'} · {s.state} ·{' '}
                {s.stale ? '陈旧' : '当前'}
              </p>
              <p>lastSuccess：{timeText(s.lastSuccess?.observedAtMs)}</p>
              {s.lastSuccess?.balances?.length ? (
                s.lastSuccess.balances.map((b, j) => (
                  <p key={j}>{moneyText(b.currency, b.total)}</p>
                ))
              ) : (
                <p>金额未知</p>
              )}
            </div>
          ))}
        </>
      )}
      {visible.error && <p role="status">{visible.error}</p>}
      <button
        type="button"
        disabled={!usable || visible.busy}
        onClick={() => {
          void request();
        }}
      >
        刷新资金保护状态
      </button>{' '}
      <button
        type="button"
        disabled={!usable || !data || !visible.verified || visible.busy}
        onClick={() => {
          void request(!data?.enabled);
        }}
      >
        {data?.enabled ? '关闭资金自动保护' : '启用资金自动保护'}
      </button>
    </section>
  );
}
