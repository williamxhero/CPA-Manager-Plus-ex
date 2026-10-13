import { afterEach, describe, expect, it, vi } from 'vitest';
import type { TFunction } from 'i18next';
import type { AuthFileItem } from '@/types';
import { apiClient, createScopedApiRequestConfig } from '@/services/api/client';
import {
  fetchQwenQuota,
  isQwenQuotaFile,
  parseQwenQuota,
  QWEN_NATIVE_QUOTA_PATH,
  QWEN_QUOTA_PATH,
  type QwenQuotaState,
} from './qwenQuota';

const t = ((key: string) => key) as TFunction;
const file: AuthFileItem = { name: 'qwen.json', provider: 'qwen', auth_index: 'selected-index' };
const card = {
  auth_index: 'selected-index',
  label: 'Qwen',
  plan: 'Token Plan 个人版 Standard',
  planStatus: '生效中',
  planStart: '2026-09-17T18:11:24+08:00',
  planEnd: '2027-09-18T00:00:00+08:00',
  daysLeft: 345,
  observedAt: '2026-10-08T19:15:43+08:00',
  windows: [
    {
      window: '1month',
      usedPercent: 100,
      remainingPercent: 0,
      resetTime: '2026-10-18T00:00:00+08:00',
      resetsInDays: 10,
    },
  ],
  metrics: [
    {
      key: 'addon_remaining_credits',
      label: '加购包剩余额度',
      value: 0,
      unit: 'credits',
      format: 'number',
    },
  ],
  error: null,
};
const payload = { cards: [card] };
const nativePayload = {
  subscription: { plan: card.plan },
  groups: [
    {
      displayName: 'Qwen',
      buckets: [
        {
          window: '1month',
          remainingFraction: 0.25,
          resetTime: card.windows[0].resetTime,
        },
      ],
    },
  ],
  summary: card.metrics,
};

const parseCard = (overrides: Record<string, unknown>) =>
  parseQwenQuota({ cards: [{ ...card, ...overrides }] }, t, file.auth_index as string);

const deferred = () => {
  let resolve!: (value: unknown) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<unknown>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
};

afterEach(() => {
  vi.restoreAllMocks();
  vi.useRealTimers();
});

describe('isQwenQuotaFile', () => {
  it.each(['provider', 'quota_provider', 'type'])(
    'matches normalized %s independently',
    (field) => {
      expect(isQwenQuotaFile({ name: 'test.json', [field]: ' QwEn ' })).toBe(true);
      expect(isQwenQuotaFile({ name: 'test.json', provider: 'codex', [field]: 'qwen' })).toBe(true);
    }
  );

  it.each([
    'codex',
    'claude',
    'opencode-go',
    'qwen-cliproxyapi',
    'qwen_oauth',
    'qwen2',
    '',
    'unknown',
  ])('does not match %s', (provider) => {
    expect(
      isQwenQuotaFile({ name: 'qwen.json', provider, quota_provider: provider, type: provider })
    ).toBe(false);
  });

  it('does not infer Qwen from a filename, quota support, typo or non-string metadata', () => {
    expect(isQwenQuotaFile({ name: 'qwen.json', supports_quota: true, typo: 'qwen' })).toBe(false);
    expect(isQwenQuotaFile({ name: 'qwen.json', quota_provider: ['qwen'] })).toBe(false);
  });
});

describe('parseQwenQuota rich cards', () => {
  it('retains the real rich fields, exhausted quota, and a genuine zero metric', () => {
    expect(parseQwenQuota(payload, t, 'selected-index')).toEqual({
      plan: card.plan,
      planStatus: card.planStatus,
      planStartMs: Date.parse(card.planStart),
      planEndMs: Date.parse(card.planEnd),
      daysLeft: 345,
      observedAtMs: Date.parse(card.observedAt),
      windows: [
        {
          id: '1month',
          label: 'accounts.detail_snapshot_window_monthly',
          usedPercent: 100,
          remainingPercent: 0,
          resetAtMs: Date.parse(card.windows[0].resetTime),
          resetsInDays: 10,
        },
      ],
      metrics: card.metrics,
    });
  });

  it('supports window-only cards with unknown plan and timestamps without inventing metadata', () => {
    expect(
      parseQwenQuota(
        {
          cards: [
            { auth_index: 'only', windows: [{ window: '5h', usedPercent: 25 }], error: null },
          ],
        },
        t
      )
    ).toEqual({
      plan: null,
      planStatus: null,
      planStartMs: null,
      planEndMs: null,
      daysLeft: null,
      observedAtMs: null,
      windows: [
        {
          id: '5h',
          label: 'accounts.detail_snapshot_window_five_hour',
          usedPercent: 25,
          remainingPercent: 75,
          resetAtMs: null,
          resetsInDays: null,
        },
      ],
      metrics: [],
    });
  });

  it('supports metrics-only cards and nullable optional metric metadata', () => {
    const result = parseCard({
      windows: [],
      metrics: [{ key: 'credits', label: 'Credits', value: 12 }],
    });
    expect(result.windows).toEqual([]);
    expect(result.metrics).toEqual([
      { key: 'credits', label: 'Credits', value: 12, unit: null, format: null },
    ]);
  });

  it.each([
    ['1month', 'monthly'],
    ['monthly', 'monthly'],
    ['5h', 'five_hour'],
    ['five_hour', 'five_hour'],
    ['weekly', 'weekly'],
    ['1week', 'weekly'],
    ['7d', 'weekly'],
  ])('translates %s using the existing %s label', (window, kind) => {
    expect(parseCard({ windows: [{ window, usedPercent: 10 }] }).windows[0].label).toBe(
      `accounts.detail_snapshot_window_${kind}`
    );
  });

  it('keeps unknown window labels and derives only a missing complementary percentage', () => {
    expect(
      parseCard({ windows: [{ window: 'custom quota', remainingPercent: 0 }] }).windows[0]
    ).toMatchObject({
      id: 'custom quota',
      label: 'custom quota',
      usedPercent: 100,
      remainingPercent: 0,
    });
  });

  it('computes missing countdowns from actual dates, not observation age', () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-10-08T16:00:00Z'));
    const result = parseCard({
      daysLeft: undefined,
      windows: [{ window: '1month', usedPercent: 30, resetTime: card.windows[0].resetTime }],
    });
    expect(result.daysLeft).toBe(344);
    expect(result.windows[0].resetsInDays).toBe(9);
    expect(result.observedAtMs).toBe(Date.parse(card.observedAt));
  });

  it('clamps expired explicit countdowns to zero without changing genuine quota zeros', () => {
    expect(
      parseCard({ daysLeft: -2, windows: [{ window: 'weekly', usedPercent: 0, resetsInDays: -2 }] })
    ).toMatchObject({
      daysLeft: 0,
      windows: [{ usedPercent: 0, remainingPercent: 100, resetsInDays: 0 }],
    });
  });

  it.each([
    ['2026-10-06T00:00:00Z', 0],
    ['2026-10-07T23:59:59Z', 0],
    ['2026-10-08T00:00:00Z', 0],
    ['2026-10-08T00:00:01Z', 1],
    ['2026-10-09T00:00:00Z', 1],
  ])('computes non-negative countdowns at the expiry boundary %s', (end, days) => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-10-08T00:00:00Z'));
    const result = parseCard({
      daysLeft: undefined,
      planEnd: end,
      windows: [{ window: 'weekly', usedPercent: 25, resetTime: end }],
    });
    expect(result.daysLeft).toBe(days);
    expect(result.windows[0].resetsInDays).toBe(days);
    expect(result.planEndMs).toBe(Date.parse(end));
    expect(result.windows[0].resetAtMs).toBe(Date.parse(end));
  });

  it('rejects duplicate window IDs and metric keys instead of creating ambiguous UI keys', () => {
    expect(() => parseCard({ windows: [card.windows[0], card.windows[0]] })).toThrow(/duplicate/);
    expect(() => parseCard({ metrics: [card.metrics[0], card.metrics[0]] })).toThrow(/duplicate/);
    expect(() =>
      parseQwenQuota({ groups: [...nativePayload.groups, ...nativePayload.groups] }, t)
    ).toThrow(/duplicate/);
  });

  it('uses native tierName only when plan is unavailable', () => {
    const result = parseQwenQuota(
      { subscription: { tierName: 'Standard' }, summary: card.metrics },
      t
    );
    expect(result.plan).toBe('Standard');
    expect(
      parseQwenQuota(
        { subscription: { plan: 'Token Plan', tierName: 'Standard' }, summary: card.metrics },
        t
      ).plan
    ).toBe('Token Plan');
  });

  it('selects only the requested identity even when another card appears first or has an error', () => {
    const other = { ...card, auth_index: 'someone-else', plan: 'Other plan', error: 'other error' };
    expect(parseQwenQuota({ cards: [other, card] }, t, 'selected-index').plan).toBe(card.plan);
    expect(parseQwenQuota({ cards: [{ ...card, auth_index: 0 }] }, t, 0).plan).toBe(card.plan);
  });

  it.each([
    { cards: [] },
    { cards: [{ ...card, auth_index: 'someone-else' }] },
    { cards: [card, card] },
    { cards: [{ ...card, auth_index: undefined }] },
    { ...card, auth_index: 'someone-else' },
  ])('rejects absent, mismatched or ambiguous selected identities %#', (response) => {
    expect(() => parseQwenQuota(response, t, 'selected-index')).toThrow(/identity/);
  });

  it('requires an identity when selecting a rich card and refuses an unscoped multi-card response', () => {
    expect(() => parseQwenQuota({ cards: [{ windows: card.windows }] }, t)).toThrow(/identity/);
    expect(() => parseQwenQuota({ cards: [card, { ...card, auth_index: 'other' }] }, t)).toThrow(
      /identity/
    );
  });

  it.each([
    { error: '  upstream quota failed  ' },
    { message: '  CLI login expired  ' },
    { error: { message: '  console unavailable  ' } },
  ])('preserves per-card upstream errors verbatim %#', (failure) => {
    const expected =
      typeof failure.error === 'string'
        ? failure.error
        : (failure.message ?? (failure.error as { message: string }).message);
    try {
      parseCard(failure);
      expect.fail('Expected upstream error');
    } catch (error) {
      expect((error as Error).message).toBe(expected);
    }
  });

  it('preserves top-level upstream errors even without cards', () => {
    expect(() => parseQwenQuota({ error: 'backend failed' }, t)).toThrow('backend failed');
    expect(() => parseQwenQuota({ message: 'CLI unavailable' }, t)).toThrow('CLI unavailable');
  });

  it.each([{}, { windows: [], metrics: [] }, { windows: null, metrics: null }])(
    'rejects empty quota rather than showing fabricated availability %#',
    (response) => {
      expect(() => parseQwenQuota(response, t)).toThrow(/no quota windows or metrics/);
    }
  );

  it.each([null, undefined, [], 'not JSON', 0, { cards: {} }, { cards: [null] }])(
    'rejects malformed envelopes %#',
    (response) => {
      expect(() => parseQwenQuota(response, t)).toThrow();
    }
  );

  it.each([
    { windows: {} },
    { metrics: {} },
    { windows: [null] },
    { metrics: [null] },
    { plan: {} },
    { planStatus: false },
    { planStart: 'invalid' },
    { planStart: '2026-02-30T00:00:00Z' },
    { planEnd: 0 },
    { planEnd: '2026-10-08T00:00:00' },
    { planEnd: '2026-10-08T00:00Z' },
    { observedAt: 'invalid' },
    { daysLeft: false },
    { daysLeft: '' },
    { daysLeft: 1.5 },
    { error: false },
    { error: {} },
    { message: [] },
  ])('rejects malformed card fields %#', (overrides) => {
    expect(() => parseCard(overrides)).toThrow(/invalid/);
  });

  it.each([
    {},
    { window: '' },
    { window: '1month' },
    { window: '1month', usedPercent: null },
    { window: '1month', usedPercent: false },
    { window: '1month', usedPercent: '' },
    { window: '1month', usedPercent: '50' },
    { window: '1month', usedPercent: NaN },
    { window: '1month', usedPercent: Infinity },
    { window: '1month', usedPercent: -1 },
    { window: '1month', usedPercent: 101 },
    { window: '1month', remainingPercent: false },
    { window: '1month', usedPercent: 25, remainingPercent: 25 },
    { window: '1month', usedPercent: 25, resetTime: 'not a date' },
    { window: '1month', usedPercent: 25, resetsInDays: '0' },
  ])('rejects malformed windows without synthesizing zeros %#', (window) => {
    expect(() => parseCard({ windows: [window] })).toThrow(/invalid/);
  });

  it.each([
    {},
    { key: '', label: 'Credits', value: 0 },
    { key: 'credits', label: '', value: 0 },
    { key: 'credits', label: 'Credits' },
    { key: 'credits', label: 'Credits', value: null },
    { key: 'credits', label: 'Credits', value: false },
    { key: 'credits', label: 'Credits', value: '0' },
    { key: 'credits', label: 'Credits', value: NaN },
    { key: 'credits', label: 'Credits', value: Infinity },
    { key: 'credits', label: 'Credits', value: 0, unit: 123 },
    { key: 'credits', label: 'Credits', value: 0, format: 'unsupported' },
  ])('rejects malformed metrics even when windows are valid %#', (metric) => {
    expect(() => parseCard({ metrics: [metric] })).toThrow(/invalid/);
  });

  it('whitelists only quota fields and supports credential lifecycle metadata in state', () => {
    const data = parseCard({ api_key: 'dummy-key', notes: ['not retained'] });
    const state: QwenQuotaState = {
      ...data,
      status: 'success',
      authFileKey: 'qwen.json::selected-index',
      authFileName: 'qwen.json',
      authIndex: 'selected-index',
      authFileIdentityVerified: true,
      fetchedAtMs: 123,
      failedAtMs: 122,
    };
    expect(state.fetchedAtMs).toBe(123);
    expect(data).not.toHaveProperty('api_key');
    expect(data).not.toHaveProperty('auth_index');
    expect(data).not.toHaveProperty('notes');
  });
});

describe('parseQwenQuota native v8 fallback', () => {
  it('converts real groups/buckets and summary without inventing missing rich metadata', () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-10-08T16:00:00Z'));
    expect(parseQwenQuota(nativePayload, t, 'selected-index')).toEqual({
      plan: card.plan,
      planStatus: null,
      planStartMs: null,
      planEndMs: null,
      daysLeft: null,
      observedAtMs: null,
      windows: [
        {
          id: '1month',
          label: 'accounts.detail_snapshot_window_monthly',
          usedPercent: 75,
          remainingPercent: 25,
          resetAtMs: Date.parse(card.windows[0].resetTime),
          resetsInDays: 9,
        },
      ],
      metrics: card.metrics,
    });
  });

  it('reads all native groups and genuine zero remaining fractions', () => {
    const result = parseQwenQuota(
      {
        groups: [
          { buckets: [{ window: '5h', remainingFraction: 0 }] },
          { buckets: [{ window: 'weekly', remainingFraction: 1 }] },
        ],
      },
      t
    );
    expect(
      result.windows.map((window) => [window.id, window.usedPercent, window.remainingPercent])
    ).toEqual([
      ['5h', 100, 0],
      ['weekly', 0, 100],
    ]);
  });

  it('supports native summary-only responses', () => {
    const result = parseQwenQuota(
      {
        summary: [{ key: 'balance', label: 'Balance', value: 0, format: 'currency', unit: 'USD' }],
      },
      t
    );
    expect(result.windows).toEqual([]);
    expect(result.metrics[0]).toMatchObject({ value: 0, format: 'currency', unit: 'USD' });
  });

  it.each([
    { subscription: { plan: 'Standard' } },
    { groups: [] },
    { groups: [{ buckets: [] }], summary: [] },
  ])('rejects native empty inventories %#', (response) => {
    expect(() => parseQwenQuota(response, t)).toThrow(/no quota windows or metrics/);
  });

  it.each([
    { groups: {} },
    { groups: [null] },
    { groups: [{ buckets: {} }] },
    { groups: [{ buckets: [{ window: '1month' }] }] },
    { groups: [{ buckets: [{ window: '1month', remainingFraction: false }] }] },
    { groups: [{ buckets: [{ window: '1month', remainingFraction: -0.1 }] }] },
    { groups: [{ buckets: [{ window: '1month', remainingFraction: 1.1 }] }] },
    { groups: [{ buckets: [{ window: '1month', remainingFraction: NaN }] }] },
    { groups: [{ buckets: [{ window: '', remainingFraction: 0 }] }] },
    { subscription: [], summary: card.metrics },
    { summary: [{ key: 'credits', label: 'Credits' }] },
  ])('rejects malformed native quota %#', (response) => {
    expect(() => parseQwenQuota(response, t)).toThrow(/invalid/);
  });
});

describe('fetchQwenQuota', () => {
  const scope = { apiBase: 'http://localhost:18317', managementKey: 'dummy-management-key' };

  it('posts only the chosen credential identity with the captured connection scope', async () => {
    const post = vi
      .spyOn(apiClient, 'post')
      .mockResolvedValue({ cards: [{ ...card, auth_index: 'other' }, card] });
    expect((await fetchQwenQuota(file, t, scope)).plan).toBe(card.plan);
    expect(post).toHaveBeenCalledExactlyOnceWith(
      QWEN_QUOTA_PATH,
      { auth_index: 'selected-index' },
      createScopedApiRequestConfig(scope)
    );
  });

  it('supports camelCase and numeric auth indexes without downloading credentials', async () => {
    const post = vi
      .spyOn(apiClient, 'post')
      .mockResolvedValue({ cards: [{ ...card, auth_index: '0' }] });
    await fetchQwenQuota({ ...file, authIndex: 0 }, t);
    expect(post).toHaveBeenCalledExactlyOnceWith(QWEN_QUOTA_PATH, { auth_index: '0' }, undefined);
  });

  it.each([undefined, null, '', '  ', false, Infinity])(
    'rejects missing/invalid auth indexes before network I/O %#',
    async (auth_index) => {
      const post = vi.spyOn(apiClient, 'post');
      await expect(fetchQwenQuota({ ...file, auth_index }, t)).rejects.toThrow(
        'missing auth_index'
      );
      expect(post).not.toHaveBeenCalled();
    }
  );

  it.each([{ status: 404 }, { response: { status: 404 } }])(
    'falls back only on HTTP 404 using the same scope and identity %#',
    async (error) => {
      const post = vi
        .spyOn(apiClient, 'post')
        .mockRejectedValueOnce(error)
        .mockResolvedValueOnce(nativePayload);
      expect((await fetchQwenQuota(file, t, scope)).windows[0].remainingPercent).toBe(25);
      expect(post).toHaveBeenCalledTimes(2);
      expect(post).toHaveBeenNthCalledWith(
        1,
        QWEN_QUOTA_PATH,
        { auth_index: 'selected-index' },
        createScopedApiRequestConfig(scope)
      );
      expect(post).toHaveBeenNthCalledWith(
        2,
        QWEN_NATIVE_QUOTA_PATH,
        { auth_index: 'selected-index' },
        createScopedApiRequestConfig(scope)
      );
    }
  );

  it.each([400, 401, 403, 429, 500, 502, undefined])(
    'does not fall back on status %s or mask its original error',
    async (status) => {
      const error = Object.assign(new Error('  original upstream error  '), { status });
      const post = vi.spyOn(apiClient, 'post').mockRejectedValue(error);
      await expect(fetchQwenQuota(file, t)).rejects.toBe(error);
      expect(post).toHaveBeenCalledTimes(1);
    }
  );

  it.each([
    { cards: [{ ...card, error: 'console login expired' }] },
    { cards: [{ ...card, windows: [], metrics: [] }] },
    { cards: [{ ...card, auth_index: 'other' }] },
    { cards: [{ ...card, windows: [{}] }] },
  ])(
    'does not fall back for successful HTTP responses containing failed/invalid readings %#',
    async (response) => {
      const post = vi.spyOn(apiClient, 'post').mockResolvedValue(response);
      await expect(fetchQwenQuota(file, t)).rejects.toThrow();
      expect(post).toHaveBeenCalledTimes(1);
    }
  );

  it('preserves fallback failures without trying another request', async () => {
    const error = new Error('native quota unavailable');
    const post = vi
      .spyOn(apiClient, 'post')
      .mockRejectedValueOnce({ status: 404 })
      .mockRejectedValueOnce(error);
    await expect(fetchQwenQuota(file, t)).rejects.toBe(error);
    expect(post).toHaveBeenCalledTimes(2);
  });

  it('does not start a superseded request', async () => {
    const post = vi.spyOn(apiClient, 'post');
    await expect(fetchQwenQuota(file, t, scope, { isCurrent: () => false })).rejects.toThrow(
      'superseded'
    );
    expect(post).not.toHaveBeenCalled();
  });

  it.each(['resolve', 'reject'] as const)(
    'discards a superseded plugin %s and never starts fallback',
    async (settle) => {
      const pending = deferred();
      const post = vi.spyOn(apiClient, 'post').mockReturnValueOnce(pending.promise);
      let current = true;
      const result = fetchQwenQuota(file, t, scope, { isCurrent: () => current });
      current = false;
      if (settle === 'resolve') pending.resolve(payload);
      else pending.reject({ status: 404 });
      await expect(result).rejects.toThrow('superseded');
      expect(post).toHaveBeenCalledTimes(1);
    }
  );

  it.each(['resolve', 'reject'] as const)(
    'discards a superseded native fallback %s',
    async (settle) => {
      const pending = deferred();
      let current = true;
      const post = vi
        .spyOn(apiClient, 'post')
        .mockRejectedValueOnce({ status: 404 })
        .mockImplementationOnce(() => {
          current = false;
          return pending.promise;
        });
      const result = fetchQwenQuota(file, t, scope, { isCurrent: () => current });
      if (settle === 'resolve') pending.resolve(nativePayload);
      else pending.reject(new Error('late failure'));
      await expect(result).rejects.toThrow('superseded');
      expect(post).toHaveBeenCalledTimes(2);
    }
  );
});
