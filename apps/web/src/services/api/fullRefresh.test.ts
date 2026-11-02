import { afterEach, expect, it, vi } from 'vitest';
import { refreshCredentialSource } from './fullRefresh';
afterEach(() => vi.unstubAllGlobals());
const binding = {
  scope: {
    instance_id: 'issued',
    instance_incarnation: 'incarnation',
    boot_epoch: 'boot',
    snapshot_revision: 1,
  },
  targets: [
    {
      resource_id: 'issued-target',
      incarnation: 'credential-1',
      identity_version: 2,
      credential_version: 3,
      config_version: 4,
      membership_version: 5,
    },
  ],
  authIndex: 'a',
  sourceId: 'issued-source',
  subjectId: 'issued-subject',
  queryVersion: 7,
  sourceVersion: 'v1',
};
it('uses only the common76 host-issued source/job/progress for explicit credential refresh', async () => {
  const source = {
    kind: 'credential_quota',
    credential: {
      auth_index: binding.authIndex,
      scope: binding.scope,
      targets: binding.targets,
      sourceId: binding.sourceId,
      subjectId: binding.subjectId,
      sourceVersion: binding.sourceVersion,
      version: binding.queryVersion,
    },
  };
  const calls: Array<[string, RequestInit | undefined]> = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init?: RequestInit) => {
      calls.push([url, init]);
      return Response.json(
        init?.method === 'POST'
          ? { id: 'common-job', state: 'complete', pending: 0, failed: 0, items: [] }
          : {
              scope: binding.scope,
              sources: [
                {
                  id: 'source',
                  request: source,
                  targets: source.credential.targets,
                  sourceId: binding.sourceId,
                  subjectId: binding.subjectId,
                  queryVersion: binding.queryVersion,
                  queryCapable: true,
                },
              ],
            }
      );
    })
  );
  await refreshCredentialSource('http://fixture', 'test', binding);
  expect(calls.map((c) => c[0])).toEqual([
    'http://fixture/usage-service/full-refresh',
    'http://fixture/usage-service/full-refresh',
  ]);
  expect(JSON.parse(String(calls[1][1]?.body))).toEqual({ purpose: 'manual', source });
  expect(
    calls.every((c) => c[1]?.headers && c[1]?.redirect === 'error' && c[1]?.cache === 'no-store')
  ).toBe(true);
});

it('rejects a same-auth-index replacement before POSTing the refresh job', async () => {
  const source = {
    kind: 'credential_quota',
    credential: {
      auth_index: 'a',
      scope: binding.scope,
      targets: [{ ...binding.targets[0], credential_version: 99 }],
      sourceId: 'replacement-source',
      subjectId: 'replacement-subject',
      sourceVersion: 'v2',
      version: 8,
    },
  };
  const calls: Array<[string, RequestInit | undefined]> = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init?: RequestInit) => {
      calls.push([url, init]);
      return Response.json({
        scope: binding.scope,
        sources: [
          {
            id: 'replacement',
            request: source,
            targets: source.credential.targets,
            sourceId: 'replacement-source',
            subjectId: 'replacement-subject',
            queryVersion: 8,
            queryCapable: true,
          },
        ],
      });
    })
  );
  await expect(refreshCredentialSource('http://fixture', 'test', binding)).rejects.toThrow(
    'current_credential_source_unavailable'
  );
  expect(calls).toHaveLength(1);
});

it('refreshes a never-queried source whose zero queryVersion is omitted by the host', async () => {
  const source = {
    kind: 'credential_quota',
    credential: {
      auth_index: binding.authIndex,
      scope: binding.scope,
      targets: binding.targets,
      sourceId: binding.sourceId,
      subjectId: binding.subjectId,
      sourceVersion: binding.sourceVersion,
      version: 0,
    },
  };
  const calls: Array<[string, RequestInit | undefined]> = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init?: RequestInit) => {
      calls.push([url, init]);
      return Response.json(
        init?.method === 'POST'
          ? { id: 'first-job', state: 'complete', pending: 0, failed: 0, items: [] }
          : {
              scope: binding.scope,
              sources: [
                {
                  request: source,
                  targets: binding.targets,
                  sourceId: binding.sourceId,
                  subjectId: binding.subjectId,
                  queryCapable: true,
                  // Go omitempty omits exactly the zero-valued inventory summary.
                },
              ],
            }
      );
    })
  );
  await refreshCredentialSource('http://fixture', 'test', { ...binding, queryVersion: 0 });
  expect(calls).toHaveLength(2);
  expect(JSON.parse(String(calls[1][1]?.body))).toEqual({ purpose: 'manual', source });
});

it.each([
  ['omitted nonzero summary', 7, undefined, 7],
  ['null zero summary', 0, null, 0],
  ['missing issued request version', 0, undefined, undefined],
  ['changed issued request version', 0, undefined, 8],
] as const)(
  'rejects %s without posting a refresh',
  async (_label, queryVersion, summaryVersion, requestVersion) => {
    const fetch = vi.fn(async () =>
      Response.json({
        scope: binding.scope,
        sources: [
          {
            queryCapable: true,
            sourceId: binding.sourceId,
            subjectId: binding.subjectId,
            queryVersion: summaryVersion,
            targets: binding.targets,
            request: {
              kind: 'credential_quota',
              credential: {
                auth_index: binding.authIndex,
                scope: binding.scope,
                targets: binding.targets,
                sourceId: binding.sourceId,
                subjectId: binding.subjectId,
                sourceVersion: binding.sourceVersion,
                version: requestVersion,
              },
            },
          },
        ],
      })
    );
    vi.stubGlobal('fetch', fetch);
    await expect(
      refreshCredentialSource('http://fixture', 'test', {
        ...binding,
        queryVersion,
      })
    ).rejects.toThrow('current_credential_source_unavailable');
    expect(fetch).toHaveBeenCalledTimes(1);
  }
);

it.each([
  ['source id', { sourceId: 'replacement-source' }],
  ['subject id', { subjectId: 'replacement-subject' }],
  ['query version', { queryVersion: binding.queryVersion + 1 }],
])('rejects a stale %s replacement before POSTing the refresh job', async (_label, change) => {
  const sourceId = 'sourceId' in change ? change.sourceId : binding.sourceId;
  const subjectId = 'subjectId' in change ? change.subjectId : binding.subjectId;
  const queryVersion = 'queryVersion' in change ? change.queryVersion : binding.queryVersion;
  const source = {
    kind: 'credential_quota',
    credential: {
      auth_index: binding.authIndex,
      scope: binding.scope,
      targets: binding.targets,
      sourceId,
      subjectId,
      sourceVersion: binding.sourceVersion,
      version: queryVersion,
    },
  };
  const calls: Array<[string, RequestInit | undefined]> = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init?: RequestInit) => {
      calls.push([url, init]);
      return Response.json({
        scope: binding.scope,
        sources: [
          {
            id: 'replacement',
            request: source,
            targets: binding.targets,
            sourceId: source.credential.sourceId,
            subjectId: source.credential.subjectId,
            queryVersion: source.credential.version,
            queryCapable: true,
          },
        ],
      });
    })
  );
  await expect(refreshCredentialSource('http://fixture', 'test', binding)).rejects.toThrow(
    'current_credential_source_unavailable'
  );
  expect(calls).toHaveLength(1);
  expect(calls.every(([, init]) => init?.method !== 'POST')).toBe(true);
});

it('rejects a stale manager scope even when auth_index and sourceVersion are reused', async () => {
  const source = {
    kind: 'credential_quota',
    credential: {
      auth_index: 'a',
      scope: { ...binding.scope, instance_incarnation: 'new-incarnation' },
      targets: binding.targets,
      sourceId: binding.sourceId,
      subjectId: binding.subjectId,
      sourceVersion: 'v1',
      version: binding.queryVersion,
    },
  };
  const calls: Array<[string, RequestInit | undefined]> = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init?: RequestInit) => {
      calls.push([url, init]);
      return Response.json({
        scope: source.credential.scope,
        sources: [
          {
            request: source,
            targets: source.credential.targets,
            sourceId: binding.sourceId,
            subjectId: binding.subjectId,
            queryVersion: binding.queryVersion,
            queryCapable: true,
          },
        ],
      });
    })
  );
  await expect(refreshCredentialSource('http://fixture', 'test', binding)).rejects.toThrow(
    'identity_or_source_changed'
  );
  expect(calls).toHaveLength(1);
});
