import type { TFunction } from 'i18next';
import type { AuthFileItem, CredentialScopedQuotaState } from '@/types';
import {
  apiClient,
  createScopedApiRequestConfig,
  type ApiClientRequestScope,
} from '@/services/api/client';
import { normalizeAuthIndex } from '@/utils/authIndex';
import { isRecord } from '@/utils/helpers';
import type { QuotaFetchContext } from './metaQuota';

export const QWEN_QUOTA_PATH = '/plugins/qwen-cliproxyapi/quota-usage';
export const QWEN_NATIVE_QUOTA_PATH = '/quota/fetch';

export interface QwenQuotaWindow {
  id: string;
  label: string;
  usedPercent: number;
  remainingPercent: number;
  resetAtMs: number | null;
  resetsInDays: number | null;
}

export interface QwenQuotaMetric {
  key: string;
  label: string;
  value: number;
  unit: string | null;
  format: string | null;
}

export interface QwenQuotaData {
  plan: string | null;
  planStatus: string | null;
  planStartMs: number | null;
  planEndMs: number | null;
  daysLeft: number | null;
  observedAtMs: number | null;
  windows: QwenQuotaWindow[];
  metrics: QwenQuotaMetric[];
}

export interface QwenQuotaState extends CredentialScopedQuotaState, QwenQuotaData {
  status: 'idle' | 'loading' | 'success' | 'error';
  error?: string;
  errorStatus?: number;
}

export const isQwenQuotaFile = (file: AuthFileItem): boolean =>
  [file.provider, file.quota_provider, file.type].some(
    (value) => typeof value === 'string' && value.trim().toLowerCase().replace(/_/g, '-') === 'qwen'
  );

const invalid = (field: string): never => {
  throw new Error(`Qwen returned invalid ${field} quota data`);
};

const assertNoError = (body: Record<string, unknown>) => {
  const error = body.error;
  if (typeof error === 'string' && error.trim()) throw new Error(error);
  if (isRecord(error) && typeof error.message === 'string' && error.message.trim()) {
    throw new Error(error.message);
  }
  if (error != null && error !== '') invalid('error');
  if (typeof body.message === 'string' && body.message.trim()) throw new Error(body.message);
  if (body.message != null && body.message !== '') invalid('message');
};

const optionalText = (value: unknown, field: string): string | null => {
  if (value == null || value === '') return null;
  if (typeof value !== 'string') return invalid(field);
  return value.trim() || null;
};

const requiredText = (value: unknown, field: string): string =>
  optionalText(value, field) ?? invalid(field);

const finiteNumber = (value: unknown, field: string, max?: number): number => {
  if (
    typeof value !== 'number' ||
    !Number.isFinite(value) ||
    (max !== undefined && (value < 0 || value > max))
  ) {
    return invalid(field);
  }
  return value;
};

const timestamp = (value: unknown, field: string): number | null => {
  if (value == null || value === '') return null;
  if (typeof value !== 'string') return invalid(field);
  const parts = /^(\d{4})-(\d{2})-(\d{2})T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/.exec(
    value
  );
  if (!parts) return invalid(field);
  // Date.parse rolls impossible dates such as February 30 into the next month.
  const calendar = new Date(0);
  calendar.setUTCFullYear(Number(parts[1]), Number(parts[2]) - 1, Number(parts[3]));
  if (calendar.getUTCMonth() !== Number(parts[2]) - 1 || calendar.getUTCDate() !== Number(parts[3]))
    return invalid(field);
  const ms = Date.parse(value);
  return Number.isFinite(ms) ? ms : invalid(field);
};

const countdown = (value: unknown, endMs: number | null, field: string): number | null => {
  if (value != null) {
    const days = finiteNumber(value, field);
    return Number.isInteger(days) ? Math.max(0, days) : invalid(field);
  }
  return endMs === null ? null : Math.max(0, Math.ceil((endMs - Date.now()) / 86_400_000));
};

const array = (value: unknown, field: string): unknown[] => {
  if (value == null) return [];
  return Array.isArray(value) ? value : invalid(field);
};

const windowLabel = (id: string, t: TFunction): string => {
  const normalized = id.trim().toLowerCase().replace(/_/g, '-');
  const kind = ['1month', 'monthly', 'month'].includes(normalized)
    ? 'monthly'
    : ['5h', '5hour', '5hours', 'five-hour', 'rolling'].includes(normalized)
      ? 'five_hour'
      : ['1week', '7d', 'weekly', 'week'].includes(normalized)
        ? 'weekly'
        : null;
  return kind ? t(`accounts.detail_snapshot_window_${kind}`) : id;
};

const parseWindow = (raw: unknown, t: TFunction, native: boolean): QwenQuotaWindow => {
  if (!isRecord(raw)) return invalid('window');
  const id = requiredText(raw.window, 'window');
  let usedPercent: number;
  let remainingPercent: number;
  if (native) {
    remainingPercent = finiteNumber(raw.remainingFraction, 'remainingFraction', 1) * 100;
    usedPercent = 100 - remainingPercent;
  } else {
    if (raw.usedPercent == null && raw.remainingPercent == null) return invalid('window percent');
    usedPercent =
      raw.usedPercent == null
        ? 100 - finiteNumber(raw.remainingPercent, 'remainingPercent', 100)
        : finiteNumber(raw.usedPercent, 'usedPercent', 100);
    remainingPercent =
      raw.remainingPercent == null
        ? 100 - usedPercent
        : finiteNumber(raw.remainingPercent, 'remainingPercent', 100);
    if (Math.abs(usedPercent + remainingPercent - 100) > 0.000001) return invalid('window percent');
  }
  const resetAtMs = timestamp(raw.resetTime, 'resetTime');
  return {
    id,
    label: windowLabel(id, t),
    usedPercent,
    remainingPercent,
    resetAtMs,
    resetsInDays: countdown(native ? null : raw.resetsInDays, resetAtMs, 'resetsInDays'),
  };
};

const parseMetric = (raw: unknown): QwenQuotaMetric => {
  if (!isRecord(raw)) return invalid('metric');
  const format = optionalText(raw.format, 'metric format');
  if (format !== null && format !== 'number' && format !== 'currency')
    return invalid('metric format');
  return {
    key: requiredText(raw.key, 'metric key'),
    label: requiredText(raw.label, 'metric label'),
    value: finiteNumber(raw.value, 'metric value'),
    unit: optionalText(raw.unit, 'metric unit'),
    format,
  };
};

export const parseQwenQuota = (
  payload: unknown,
  t: TFunction,
  authIndex?: string | number | null
): QwenQuotaData => {
  if (!isRecord(payload)) return invalid('response');
  assertNoError(payload);
  let body = payload;
  const expectedIndex = normalizeAuthIndex(authIndex);
  if ('cards' in payload) {
    if (!Array.isArray(payload.cards)) return invalid('cards');
    const matches =
      expectedIndex === null
        ? payload.cards
        : payload.cards.filter(
            (card) => isRecord(card) && normalizeAuthIndex(card.auth_index) === expectedIndex
          );
    // Never use the first card when the selected credential is absent or ambiguous.
    if (matches.length !== 1 || !isRecord(matches[0])) return invalid('credential identity');
    body = matches[0];
    if (!normalizeAuthIndex(body.auth_index)) return invalid('credential identity');
  }
  if (
    expectedIndex !== null &&
    'auth_index' in body &&
    normalizeAuthIndex(body.auth_index) !== expectedIndex
  )
    return invalid('credential identity');
  assertNoError(body);

  const native =
    !('cards' in payload) && ('groups' in body || 'summary' in body || 'subscription' in body);
  let windows: QwenQuotaWindow[];
  if (native) {
    windows = array(body.groups, 'groups').flatMap((group) => {
      if (!isRecord(group)) return invalid('group');
      return array(group.buckets, 'buckets').map((bucket) => parseWindow(bucket, t, true));
    });
  } else {
    windows = array(body.windows, 'windows').map((window) => parseWindow(window, t, false));
  }
  const metrics = array(native ? body.summary : body.metrics, 'metrics').map(parseMetric);
  if (!windows.length && !metrics.length)
    throw new Error('Qwen returned no quota windows or metrics');
  if (new Set(windows.map((window) => window.id)).size !== windows.length)
    return invalid('duplicate window');
  if (new Set(metrics.map((metric) => metric.key)).size !== metrics.length)
    return invalid('duplicate metric');

  if (native && body.subscription != null && !isRecord(body.subscription))
    return invalid('subscription');
  const subscription = native && isRecord(body.subscription) ? body.subscription : null;
  const plan = native
    ? (optionalText(subscription?.plan, 'plan') ?? optionalText(subscription?.tierName, 'tierName'))
    : optionalText(body.plan, 'plan');
  const planEndMs = native ? null : timestamp(body.planEnd, 'planEnd');
  return {
    plan,
    planStatus: native ? null : optionalText(body.planStatus, 'planStatus'),
    planStartMs: native ? null : timestamp(body.planStart, 'planStart'),
    planEndMs,
    daysLeft: native ? null : countdown(body.daysLeft, planEndMs, 'daysLeft'),
    observedAtMs: native ? null : timestamp(body.observedAt, 'observedAt'),
    windows,
    metrics,
  };
};

export const fetchQwenQuota = async (
  file: AuthFileItem,
  t: TFunction,
  requestScope?: ApiClientRequestScope,
  context?: QuotaFetchContext
): Promise<QwenQuotaData> => {
  const assertCurrent = () => {
    if (context?.isCurrent && !context.isCurrent()) throw new Error('Quota request superseded');
  };
  assertCurrent();
  const authIndex = normalizeAuthIndex(file.authIndex ?? file.auth_index);
  if (authIndex === null) throw new Error('Qwen credential is missing auth_index');
  const config = requestScope ? createScopedApiRequestConfig(requestScope) : undefined;
  let response: unknown;
  try {
    response = await apiClient.post<unknown>(QWEN_QUOTA_PATH, { auth_index: authIndex }, config);
  } catch (error: unknown) {
    assertCurrent();
    const status =
      isRecord(error) && typeof error.status === 'number'
        ? error.status
        : isRecord(error) && isRecord(error.response)
          ? error.response.status
          : undefined;
    if (status !== 404) throw error;
    try {
      response = await apiClient.post<unknown>(
        QWEN_NATIVE_QUOTA_PATH,
        { auth_index: authIndex },
        config
      );
    } catch (fallbackError: unknown) {
      assertCurrent();
      throw fallbackError;
    }
  }
  assertCurrent();
  return parseQwenQuota(response, t, authIndex);
};
