import { afterEach, expect, it, vi } from 'vitest';
import {
  autoStateDigest,
  getProviderAutoState,
  indexProviderAutoStates,
  parseProviderAutoState,
} from './providerAutoState';

afterEach(() => vi.unstubAllGlobals());

const projection = {
  version: 1,
  ready: true,
  revision: 12,
  providers: [
    {
      resourceId: 'rid-group',
      state: 'partially_auto_paused',
      memberCount: 2,
      pausedCount: 1,
      reasons: [
        {
          owner: 'core.upstream-balance.v1',
          reason: 'upstream_balance_exhausted',
          observedAtMs: 1763970360000,
          resourceId: 'rid-member-one',
        },
      ],
      members: [
        {
          resourceId: 'rid-member-one',
          state: 'paused',
          reasons: [
            {
              owner: 'core.upstream-balance.v1',
              reason: 'upstream_balance_exhausted',
              observedAtMs: 1763970360000,
              resourceId: 'rid-member-one',
            },
          ],
        },
        { resourceId: 'rid-member-two', state: 'available', reasons: [] },
      ],
    },
  ],
};

it('reads the read-only projection with the panel management key and no cache', async () => {
  const fetcher = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify(projection)));
  vi.stubGlobal('fetch', fetcher);

  await expect(getProviderAutoState('http://manager.invalid/', 'panel-key')).resolves.toEqual(
    projection
  );
  expect(fetcher).toHaveBeenCalledWith(
    'http://manager.invalid/usage-service/provider-auto-state',
    expect.objectContaining({
      method: 'GET',
      headers: { Authorization: 'Bearer panel-key' },
      cache: 'no-store',
    })
  );
});

it('resolves to null instead of throwing when the projection is unavailable', async () => {
  const unavailable = vi.fn<typeof fetch>().mockResolvedValue(
    new Response(JSON.stringify({ error: 'projection unavailable', reason: 'core_unreachable' }), {
      status: 503,
    })
  );
  vi.stubGlobal('fetch', unavailable);
  await expect(getProviderAutoState('http://manager.invalid', 'panel-key')).resolves.toBeNull();

  const rejected = vi.fn<typeof fetch>().mockRejectedValue(new Error('network down'));
  vi.stubGlobal('fetch', rejected);
  await expect(getProviderAutoState('http://manager.invalid', 'panel-key')).resolves.toBeNull();

  const malformed = vi.fn<typeof fetch>().mockResolvedValue(new Response('not json'));
  vi.stubGlobal('fetch', malformed);
  await expect(getProviderAutoState('http://manager.invalid', 'panel-key')).resolves.toBeNull();
});

it('drops malformed projection entries instead of failing the whole read', () => {
  expect(
    parseProviderAutoState({
      version: 1,
      ready: true,
      revision: 3,
      providers: [
        { resourceId: 'rid-kept', state: 'auto_paused', reasons: [], members: [] },
        { resourceId: 'rid-unknown-state', state: 'exploded' },
        { state: 'available' },
        'not-an-entry',
      ],
    })
  ).toEqual({
    version: 1,
    ready: true,
    revision: 3,
    providers: [
      {
        resourceId: 'rid-kept',
        state: 'auto_paused',
        memberCount: undefined,
        pausedCount: undefined,
        reasons: [],
        members: [],
      },
    ],
  });

  expect(parseProviderAutoState(null)).toBeNull();
  expect(parseProviderAutoState({ providers: 'nope' })).toBeNull();
});

it('indexes the projection by provider resource id', () => {
  const index = indexProviderAutoStates(parseProviderAutoState(projection));
  expect(Object.keys(index)).toEqual(['rid-group']);
  expect(index['rid-group'].state).toBe('partially_auto_paused');
  expect(indexProviderAutoStates(null)).toEqual({});
});

it('digests the projected content, not the revision Core happened to stamp', () => {
  const sameContentLaterRevision = { ...projection, revision: 99 };
  expect(autoStateDigest(parseProviderAutoState(sameContentLaterRevision))).toBe(
    autoStateDigest(parseProviderAutoState(projection))
  );

  const resumed = {
    ...projection,
    providers: [
      {
        ...projection.providers[0],
        state: 'available',
        pausedCount: 0,
        reasons: [],
        members: [
          { resourceId: 'rid-member-one', state: 'available', reasons: [] },
          { resourceId: 'rid-member-two', state: 'available', reasons: [] },
        ],
      },
    ],
  };
  expect(autoStateDigest(parseProviderAutoState(resumed))).not.toBe(
    autoStateDigest(parseProviderAutoState(projection))
  );
});

it('digests content independently of key order and irrelevant fields', () => {
  const reordered = {
    providers: [
      {
        state: 'partially_auto_paused',
        pausedCount: 1,
        memberCount: 2,
        resourceId: 'rid-group',
        ignoredUpstreamField: 'noise',
        members: [
          {
            reasons: [
              {
                resourceId: 'rid-member-one',
                observedAtMs: 1763970360000,
                reason: 'upstream_balance_exhausted',
                owner: 'core.upstream-balance.v1',
              },
            ],
            state: 'paused',
            resourceId: 'rid-member-one',
          },
          { reasons: [], state: 'available', resourceId: 'rid-member-two' },
        ],
        reasons: [
          {
            resourceId: 'rid-member-one',
            observedAtMs: 1763970360000,
            reason: 'upstream_balance_exhausted',
            owner: 'core.upstream-balance.v1',
          },
        ],
      },
    ],
    revision: 12,
    ready: true,
    version: 1,
  };
  expect(autoStateDigest(parseProviderAutoState(reordered))).toBe(
    autoStateDigest(parseProviderAutoState(projection))
  );
});

it('digests an unreadable projection as empty so a failure never looks like a clear state', () => {
  expect(autoStateDigest(null)).toBe(autoStateDigest({ version: 1, ready: true, revision: 1, providers: [] }));
});
