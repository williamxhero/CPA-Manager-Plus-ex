import { createHash } from 'node:crypto';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  FORK_BASE_SOURCE_COMMIT,
  FORK_REPOSITORY,
  FORK_WORKFLOW_PATH,
  REQUIRED_FORK_CHECKS,
  buildForkAssetRecords,
  buildForkReleaseInfo,
  forkArtifactName,
} from '../bin/release/fork-release-contract.mjs';
import {
  authorizeForkDryRun,
  createForkGitHubApi,
  forkAssetUploadUrl,
  publishForkRelease,
  runForkPublishCli,
} from '../bin/release/publish-fork-release.mjs';

const tag = 'v1.14.5-williamxhero.1';
const sourceCommit = 'a'.repeat(40);
const devCommit = 'b'.repeat(40);
const sourceTree = 'c'.repeat(40);
const sha256 = (bytes) => createHash('sha256').update(bytes).digest('hex');
const directories = [];
const options = () => ({
  repository: FORK_REPOSITORY,
  tag,
  sourceCommit,
  devCommit,
  dryRunId: '1001',
  dryRunAttempt: '2',
  receiptSha: 'd'.repeat(64),
  optIn: true,
  confirmPublish: true,
});
const ref = (name, sha) => ({ ref: `refs/${name}`, object: { type: 'commit', sha } });
const copy = (value) => structuredClone(value);

const makeServer = () => {
  const state = {
    run: {
      id: 1001,
      run_attempt: 2,
      path: FORK_WORKFLOW_PATH,
      repository: { full_name: FORK_REPOSITORY },
      head_repository: { full_name: FORK_REPOSITORY },
      head_sha: sourceCommit,
      head_branch: 'main',
      event: 'workflow_dispatch',
      status: 'completed',
      conclusion: 'success',
      display_title: `Fork native/web dry-run ${tag} @ ${sourceCommit}`,
    },
    main: ref('heads/main', sourceCommit),
    dev: ref('heads/dev', devCommit),
    commit: {
      sha: sourceCommit,
      tree: { sha: sourceTree },
      parents: [{ sha: 'e'.repeat(40) }, { sha: devCommit }],
    },
    devCommit: { sha: devCommit, tree: { sha: sourceTree } },
    artifact: {
      total_count: 1,
      artifacts: [
        {
          id: 9001,
          name: forkArtifactName(tag, sourceCommit, '2'),
          expired: false,
          workflow_run: { id: 1001, head_sha: sourceCommit },
        },
      ],
    },
    tag: null,
    release: null,
    releases: [],
    calls: [],
    uploads: [],
    before: null,
  };
  const api = async (apiPath, request = {}) => {
    const method = request.method || 'GET';
    state.calls.push({ path: apiPath, method, body: copy(request.body) });
    if (state.before) await state.before(apiPath, request, state);
    if (method === 'GET') {
      if (apiPath === '/actions/runs/1001') return copy(state.run);
      if (apiPath === '/git/ref/heads/main') return copy(state.main);
      if (apiPath === '/git/ref/heads/dev') return copy(state.dev);
      if (apiPath === `/git/commits/${sourceCommit}`) return copy(state.commit);
      if (apiPath === `/git/commits/${devCommit}`) return copy(state.devCommit);
      if (apiPath === '/actions/runs/1001/artifacts?per_page=100') return copy(state.artifact);
      if (apiPath === `/git/ref/tags/${tag}`) return copy(state.tag);
      if (apiPath === `/releases/tags/${tag}`) return copy(state.release);
      if (apiPath.startsWith('/releases?')) return copy(state.releases);
      if (apiPath === '/releases/5001') return copy(state.release);
    }
    if (method === 'POST' && apiPath === '/git/refs') {
      state.tag = ref(`tags/${tag}`, request.body.sha);
      return copy(state.tag);
    }
    if (method === 'POST' && apiPath === '/releases') {
      state.release = {
        ...request.body,
        id: 5001,
        published_at: null,
        assets: [],
        upload_url: `https://uploads.github.com/repos/${FORK_REPOSITORY}/releases/5001/assets{?name,label}`,
      };
      return copy(state.release);
    }
    if (method === 'PATCH' && apiPath === '/releases/5001') {
      Object.assign(state.release, request.body, { published_at: '2026-10-07T00:00:00Z' });
      return copy(state.release);
    }
    throw new Error(`Unexpected mock API request ${method} ${apiPath}`);
  };
  const upload = async (url, bytes, asset) => {
    state.uploads.push({ url, bytes: Buffer.from(bytes), name: asset.name });
    const result = {
      name: asset.name,
      size: bytes.length,
      digest: `sha256:${sha256(bytes)}`,
      state: 'uploaded',
    };
    state.release.assets.push(result);
    return copy(result);
  };
  return { state, api, upload };
};
const mutations = (state) => state.calls.filter(({ method }) => method !== 'GET');

const makePayload = () => {
  const directory = mkdtempSync(path.join(tmpdir(), 'cpamp-fork-publish-'));
  directories.push(directory);
  const assetsDir = path.join(directory, 'payload');
  const receiptFile = path.join(directory, 'receipt.json');
  mkdirSync(assetsDir);
  const nativeNames = [
    'darwin_amd64.tar.gz',
    'darwin_arm64.tar.gz',
    'linux_amd64.tar.gz',
    'linux_arm64.tar.gz',
    'windows_amd64.zip',
    'windows_arm64.zip',
  ].map((suffix) => `cpa-manager-plus_${tag}_${suffix}`);
  for (const name of nativeNames) writeFileSync(path.join(assetsDir, name), `native ${name}\n`);
  writeFileSync(path.join(assetsDir, 'management.html'), '<html>fork management</html>\n');
  writeFileSync(
    path.join(assetsDir, 'checksums.txt'),
    nativeNames
      .map((name) => `${sha256(readFileSync(path.join(assetsDir, name)))}  ${name}`)
      .join('\n') + '\n'
  );
  const info = buildForkReleaseInfo({
    tag,
    sourceCommit,
    metadata: {
      summary: { zh: '非官方 fork', en: 'Unofficial fork' },
      update: {
        breaking: false,
        migration_required: false,
        minimum_direct_upgrade_version: null,
        upgrade_guide_url: `https://github.com/${FORK_REPOSITORY}/releases/tag/${tag}`,
      },
      compatibility: { minimum_cpa_version: null },
    },
  });
  writeFileSync(path.join(assetsDir, 'release-info.json'), JSON.stringify(info));
  const content = {
    [`docs/fork-release-notes/${tag}-zh.md`]: Buffer.from('# 非官方 fork\n手动升级。\n'),
    [`docs/fork-release-notes/${tag}-en.md`]: Buffer.from(
      '# Unofficial fork\nManual upgrades only.\n'
    ),
    [`docs/fork-release-posts/${tag}-telegram.html`]: Buffer.from('<b>Manual post only</b>\n'),
  };
  const receipt = {
    schema: 'cpamp-fork-native-web-dry-run',
    schema_version: 1,
    repository: FORK_REPOSITORY,
    workflow_path: FORK_WORKFLOW_PATH,
    mode: 'dry-run',
    tag,
    source_commit: sourceCommit,
    source_tree: sourceTree,
    base_source_commit: FORK_BASE_SOURCE_COMMIT,
    frozen_refs: { main: sourceCommit, dev: devCommit },
    run: { id: '1001', attempt: '2' },
    content: Object.entries(content).map(([file, bytes]) => ({
      path: file,
      sha256: sha256(bytes),
    })),
    toolchain: { node: 'v22.0.0', npm: '10.0.0', go: 'go1.24.0' },
    checks: Object.fromEntries(REQUIRED_FORK_CHECKS.map((name) => [name, 'passed'])),
    assets: buildForkAssetRecords(assetsDir, tag).map(({ name, size, digest }) => ({
      name,
      size,
      digest,
    })),
  };
  const writeReceipt = () => {
    writeFileSync(receiptFile, JSON.stringify(receipt));
    return sha256(readFileSync(receiptFile));
  };
  const approved = { ...options(), assetsDir, receiptFile, receiptSha: writeReceipt() };
  const readers = {
    getSourceHead: () => sourceCommit,
    getSourceTree: () => sourceTree,
    getSourceStatus: () => '',
    getSourceContent: (file) => content[file],
  };
  return { approved, receipt, content, readers, writeReceipt };
};

afterEach(() => {
  vi.restoreAllMocks();
  for (const directory of directories.splice(0))
    rmSync(directory, { recursive: true, force: true });
});

describe('fork publication read-only authorization', () => {
  it('returns the exact artifact ID/name without touching local payload or mutating GitHub', async () => {
    const { api, state } = makeServer();
    expect(await authorizeForkDryRun(options(), api)).toEqual({
      artifactId: '9001',
      artifactName: forkArtifactName(tag, sourceCommit, '2'),
      sourceTree,
    });
    expect(mutations(state)).toEqual([]);
    expect(state.uploads).toEqual([]);
  });

  it.each([
    ['wrong repository', { repository: 'seakee/CPA-Manager-Plus' }],
    ['official version', { tag: 'v1.14.5' }],
    ['missing opt-in', { optIn: false }],
    ['string opt-in', { optIn: 'true' }],
    ['missing confirmation', { confirmPublish: false }],
    ['string confirmation', { confirmPublish: 'true' }],
    ['short source SHA', { sourceCommit: 'abcdef' }],
    ['missing dev SHA', { devCommit: '' }],
    ['missing run', { dryRunId: '' }],
    ['invalid attempt', { dryRunAttempt: '0' }],
    ['missing approved receipt SHA', { receiptSha: '' }],
    ['prefixed receipt digest', { receiptSha: `sha256:${'d'.repeat(64)}` }],
  ])('rejects %s before any API call', async (_name, patch) => {
    const { api, state } = makeServer();
    await expect(authorizeForkDryRun({ ...options(), ...patch }, api)).rejects.toThrow();
    expect(state.calls).toEqual([]);
  });

  it.each([
    ['failed run', { conclusion: 'failure' }],
    ['incomplete run', { status: 'in_progress' }],
    ['wrong branch', { head_branch: 'dev' }],
    ['wrong event', { event: 'push' }],
    ['wrong source', { head_sha: devCommit }],
    ['wrong attempt', { run_attempt: 3 }],
    ['wrong run ID', { id: 1002 }],
    ['wrong workflow', { path: '.github/workflows/release.yml' }],
    ['wrong mode', { display_title: `Fork native/web publish ${tag} @ ${sourceCommit}` }],
    ['wrong source repo', { head_repository: { full_name: 'attacker/CPA-Manager-Plus' } }],
    ['wrong run repo', { repository: { full_name: 'seakee/CPA-Manager-Plus' } }],
  ])('rejects %s without mutations', async (_name, patch) => {
    const { api, state } = makeServer();
    Object.assign(state.run, patch);
    await expect(authorizeForkDryRun(options(), api)).rejects.toThrow(/dry-run/);
    expect(mutations(state)).toEqual([]);
  });

  it('normalizes only the workflow path @ suffix', async () => {
    const { api, state } = makeServer();
    state.run.path += '@refs/heads/main';
    await expect(authorizeForkDryRun(options(), api)).resolves.toMatchObject({
      artifactId: '9001',
    });
    state.run.path = `${FORK_WORKFLOW_PATH}.evil@refs/heads/main`;
    await expect(authorizeForkDryRun(options(), api)).rejects.toThrow();
  });

  it.each(['main', 'dev', 'tree', 'parents', 'dev-tree'])('rejects live %s drift', async (part) => {
    const { api, state } = makeServer();
    if (part === 'tree') state.commit.sha = devCommit;
    else if (part === 'parents') state.commit.parents[1].sha = sourceCommit;
    else if (part === 'dev-tree') state.devCommit.tree.sha = devCommit;
    else state[part].object.sha = sourceTree;
    await expect(authorizeForkDryRun(options(), api)).rejects.toThrow();
    expect(mutations(state)).toEqual([]);
  });

  it.each(['missing', 'extra', 'expired', 'expired-time', 'wrong-name', 'wrong-sha', 'bad-id'])(
    'rejects %s artifacts',
    async (kind) => {
      const { api, state } = makeServer();
      const artifact = state.artifact.artifacts[0];
      if (kind === 'missing') state.artifact = { total_count: 0, artifacts: [] };
      if (kind === 'extra')
        state.artifact = { total_count: 2, artifacts: [artifact, copy(artifact)] };
      if (kind === 'expired') artifact.expired = true;
      if (kind === 'expired-time') artifact.expires_at = '2000-01-01T00:00:00Z';
      if (kind === 'wrong-name') artifact.name = forkArtifactName(tag, sourceCommit, '1');
      if (kind === 'wrong-sha') artifact.workflow_run.head_sha = devCommit;
      if (kind === 'bad-id') artifact.id = '../evil';
      await expect(authorizeForkDryRun(options(), api)).rejects.toThrow(/artifact/);
      expect(mutations(state)).toEqual([]);
    }
  );

  it.each(['tag', 'release', 'draft'])(
    'rejects an existing matching %s instead of resuming it',
    async (kind) => {
      const { api, state } = makeServer();
      if (kind === 'tag') state.tag = ref(`tags/${tag}`, sourceCommit);
      if (kind === 'release') state.release = { tag_name: tag };
      if (kind === 'draft') state.releases = [{ tag_name: tag, draft: true }];
      await expect(authorizeForkDryRun(options(), api)).rejects.toThrow(/recovery/);
      expect(mutations(state)).toEqual([]);
    }
  );

  it('fails on tag lookup errors and never treats them as absence', async () => {
    const { api, state } = makeServer();
    state.before = (requestPath) => {
      if (requestPath.includes('/git/ref/tags/')) throw new Error('HTTP 403');
    };
    await expect(authorizeForkDryRun(options(), api)).rejects.toThrow('HTTP 403');
    expect(mutations(state)).toEqual([]);
  });
});

describe('fork immutable payload publisher', () => {
  it('creates/readbacks a lightweight tag, uploads exactly nine verified bytes and publishes a non-latest prerelease', async () => {
    const { api, upload, state } = makeServer();
    const fixture = makePayload();
    const result = await publishForkRelease(fixture.approved, { api, upload, ...fixture.readers });
    expect(result).toMatchObject({
      artifactId: '9001',
      sourceCommit,
      prerelease: true,
      makeLatest: false,
      publishedAssets: 9,
      releaseId: '5001',
    });
    expect(mutations(state)).toEqual([
      { path: '/git/refs', method: 'POST', body: { ref: `refs/tags/${tag}`, sha: sourceCommit } },
      {
        path: '/releases',
        method: 'POST',
        body: {
          tag_name: tag,
          target_commitish: sourceCommit,
          name: tag,
          body: fixture.content[`docs/fork-release-notes/${tag}-zh.md`].toString(),
          draft: true,
          prerelease: true,
          make_latest: 'false',
        },
      },
      {
        path: '/releases/5001',
        method: 'PATCH',
        body: { draft: false, prerelease: true, make_latest: 'false' },
      },
    ]);
    expect(state.uploads).toHaveLength(9);
    expect(new Set(state.uploads.map(({ name }) => name)).size).toBe(9);
    for (const { name, bytes } of state.uploads) {
      expect(bytes.equals(readFileSync(path.join(fixture.approved.assetsDir, name)))).toBe(true);
    }
    expect(
      state.calls.filter(({ path: requestPath }) => requestPath === '/git/ref/heads/main')
    ).toHaveLength(5);
    expect(
      state.calls.some(({ path: requestPath }) =>
        /docker|telegram|channel|update-index/.test(requestPath)
      )
    ).toBe(false);
  });

  it.each([
    'receipt-hash',
    'asset-bytes',
    'extra-file',
    'missing-file',
    'content-hash',
    'receipt-mode',
    'receipt-run',
    'receipt-tree',
    'receipt-dev',
    'receipt-asset',
    'checkout-head',
    'checkout-tree',
    'dirty-checkout',
    'manifest-source',
    'extra-artifact-file',
  ])('rejects %s tampering before tag creation', async (kind) => {
    const { api, upload, state } = makeServer();
    const fixture = makePayload();
    if (kind === 'receipt-hash') writeFileSync(fixture.approved.receiptFile, '{}');
    if (kind === 'asset-bytes')
      writeFileSync(path.join(fixture.approved.assetsDir, 'management.html'), 'tampered');
    if (kind === 'extra-file')
      writeFileSync(path.join(fixture.approved.assetsDir, 'config.yaml'), 'private');
    if (kind === 'extra-artifact-file')
      writeFileSync(path.join(path.dirname(fixture.approved.assetsDir), 'config.yaml'), 'private');
    if (kind === 'missing-file') rmSync(path.join(fixture.approved.assetsDir, 'management.html'));
    if (kind === 'content-hash')
      fixture.content[`docs/fork-release-notes/${tag}-zh.md`] = Buffer.from('tampered');
    if (kind === 'receipt-mode') fixture.receipt.mode = 'publish';
    if (kind === 'receipt-run') fixture.receipt.run.attempt = '1';
    if (kind === 'receipt-tree') fixture.receipt.source_tree = devCommit;
    if (kind === 'receipt-dev') fixture.receipt.frozen_refs.dev = sourceCommit;
    if (kind === 'receipt-asset') fixture.receipt.assets[0].digest = `sha256:${'0'.repeat(64)}`;
    if (kind.startsWith('receipt-') && kind !== 'receipt-hash')
      fixture.approved.receiptSha = fixture.writeReceipt();
    if (kind === 'checkout-head') fixture.readers.getSourceHead = () => devCommit;
    if (kind === 'checkout-tree') fixture.readers.getSourceTree = () => devCommit;
    if (kind === 'dirty-checkout') fixture.readers.getSourceStatus = () => '?? config.yaml';
    if (kind === 'manifest-source') {
      const infoPath = path.join(fixture.approved.assetsDir, 'release-info.json');
      const info = JSON.parse(readFileSync(infoPath, 'utf8'));
      info.release.source_commit = devCommit;
      writeFileSync(infoPath, JSON.stringify(info));
      fixture.receipt.assets = buildForkAssetRecords(fixture.approved.assetsDir, tag).map(
        ({ name, size, digest }) => ({ name, size, digest })
      );
      fixture.approved.receiptSha = fixture.writeReceipt();
    }
    await expect(
      publishForkRelease(fixture.approved, { api, upload, ...fixture.readers })
    ).rejects.toThrow();
    expect(mutations(state)).toEqual([]);
    expect(state.uploads).toEqual([]);
  });

  it('rechecks frozen refs after reading payload, before the first mutation', async () => {
    const { api, upload, state } = makeServer();
    const fixture = makePayload();
    let reads = 0;
    state.before = (requestPath) => {
      if (requestPath === '/git/ref/heads/main' && ++reads === 2) state.main.object.sha = devCommit;
    };
    await expect(
      publishForkRelease(fixture.approved, { api, upload, ...fixture.readers })
    ).rejects.toThrow(/drift/);
    expect(mutations(state)).toEqual([]);
  });

  it('stops if lightweight tag creation readback points to another SHA', async () => {
    const { api, upload, state } = makeServer();
    const fixture = makePayload();
    state.before = (requestPath) => {
      if (requestPath === `/git/ref/tags/${tag}` && state.tag) state.tag.object.sha = devCommit;
    };
    await expect(
      publishForkRelease(fixture.approved, { api, upload, ...fixture.readers })
    ).rejects.toThrow(/tag readback/);
    expect(mutations(state)).toHaveLength(1);
    expect(state.uploads).toEqual([]);
  });

  it.each(['main', 'dev', 'tag', 'artifact', 'receipt', 'payload', 'draft-digest', 'draft-extra'])(
    'leaves the draft unpublished on final %s drift',
    async (kind) => {
      const { api, upload, state } = makeServer();
      const fixture = makePayload();
      const guardedUpload = async (...args) => {
        const uploaded = await upload(...args);
        if (state.uploads.length === 9) {
          if (kind === 'main') state.main.object.sha = devCommit;
          if (kind === 'dev') state.dev.object.sha = sourceCommit;
          if (kind === 'tag') state.tag.object.type = 'tag';
          if (kind === 'artifact') state.artifact.artifacts[0].id = 9002;
          if (kind === 'receipt') writeFileSync(fixture.approved.receiptFile, '{}');
          if (kind === 'payload')
            writeFileSync(path.join(fixture.approved.assetsDir, 'management.html'), 'tampered');
          if (kind === 'draft-digest') state.release.assets[0].digest = `sha256:${'0'.repeat(64)}`;
          if (kind === 'draft-extra') state.release.assets.push({ name: 'extra' });
        }
        return uploaded;
      };
      await expect(
        publishForkRelease(fixture.approved, { api, upload: guardedUpload, ...fixture.readers })
      ).rejects.toThrow();
      expect(state.calls.some(({ method }) => method === 'PATCH')).toBe(false);
      expect(state.release.draft).toBe(true);
    }
  );

  it('rechecks refs after final local payload reads, immediately before publication', async () => {
    const { api, upload, state } = makeServer();
    const fixture = makePayload();
    let contentReads = 0;
    const getSourceContent = (file) => {
      if (++contentReads === 6) state.main.object.sha = devCommit;
      return fixture.content[file];
    };
    await expect(
      publishForkRelease(fixture.approved, { api, upload, ...fixture.readers, getSourceContent })
    ).rejects.toThrow(/drift/);
    expect(state.calls.some(({ method }) => method === 'PATCH')).toBe(false);
  });

  it('uploads frozen bytes even if the local files change mid-upload, then refuses publication', async () => {
    const { api, upload, state } = makeServer();
    const fixture = makePayload();
    const originalHtml = readFileSync(path.join(fixture.approved.assetsDir, 'management.html'));
    const guardedUpload = async (...args) => {
      const result = await upload(...args);
      if (state.uploads.length === 1) {
        writeFileSync(
          path.join(fixture.approved.assetsDir, 'management.html'),
          'changed during upload'
        );
      }
      return result;
    };
    await expect(
      publishForkRelease(fixture.approved, { api, upload: guardedUpload, ...fixture.readers })
    ).rejects.toThrow();
    expect(
      state.uploads.find(({ name }) => name === 'management.html').bytes.equals(originalHtml)
    ).toBe(true);
    expect(state.calls.some(({ method }) => method === 'PATCH')).toBe(false);
  });

  it.each(['name', 'size', 'digest', 'state'])(
    'stops on mismatched uploaded asset %s',
    async (field) => {
      const { api, upload, state } = makeServer();
      const fixture = makePayload();
      await expect(
        publishForkRelease(fixture.approved, {
          api,
          ...fixture.readers,
          upload: async (...args) => ({
            ...(await upload(...args)),
            [field]: field === 'size' ? 1 : 'invalid',
          }),
        })
      ).rejects.toThrow(/uploaded asset metadata/);
      expect(state.uploads).toHaveLength(1);
      expect(state.calls.some(({ method }) => method === 'PATCH')).toBe(false);
    }
  );

  it('rejects an unsafe upload endpoint without sending payload bytes', async () => {
    const { api, upload, state } = makeServer();
    const fixture = makePayload();
    const wrapped = async (requestPath, request) => {
      const result = await api(requestPath, request);
      if (requestPath === '/releases' && request?.method === 'POST')
        result.upload_url = 'https://evil.test/upload';
      return result;
    };
    await expect(
      publishForkRelease(fixture.approved, { api: wrapped, upload, ...fixture.readers })
    ).rejects.toThrow(/Unsafe/);
    expect(state.uploads).toEqual([]);
    expect(state.calls.some(({ method }) => method === 'PATCH')).toBe(false);
  });

  it('detects published readback tampering without performing repairs', async () => {
    const { api, upload, state } = makeServer();
    const fixture = makePayload();
    state.before = (requestPath, request) => {
      if (requestPath === '/releases/5001' && !request.method && state.release?.draft === false) {
        state.release.assets[0].size += 1;
      }
    };
    await expect(
      publishForkRelease(fixture.approved, { api, upload, ...fixture.readers })
    ).rejects.toThrow(/size mismatch/);
    expect(mutations(state)).toHaveLength(3);
  });
});

describe('fork publisher HTTP and CLI boundaries', () => {
  it.each([
    'http://uploads.github.com/repos/williamxhero/CPA-Manager-Plus/releases/5001/assets{?name,label}',
    'https://evil.test/repos/williamxhero/CPA-Manager-Plus/releases/5001/assets{?name,label}',
    'https://uploads.github.com@evil.test/repos/williamxhero/CPA-Manager-Plus/releases/5001/assets{?name,label}',
    'https://uploads.github.com/repos/seakee/CPA-Manager-Plus/releases/5001/assets{?name,label}',
    'https://uploads.github.com/repos/williamxhero/CPA-Manager-Plus/releases/5002/assets{?name,label}',
    'https://uploads.github.com/repos/williamxhero/CPA-Manager-Plus/releases/5001/assets?evil=true',
  ])('rejects unsafe upload URL %s', (url) => {
    expect(() => forkAssetUploadUrl(url, 5001, 'management.html')).toThrow(/Unsafe/);
  });

  it('treats only an actual 404 as absence and disables redirects on authenticated requests', async () => {
    const fetchImpl = vi
      .fn()
      .mockResolvedValueOnce({ status: 404, ok: false })
      .mockResolvedValueOnce({ status: 403, ok: false });
    const api = createForkGitHubApi({
      repository: FORK_REPOSITORY,
      env: { GITHUB_TOKEN: 'private-test-token' },
      fetchImpl,
    });
    await expect(api(`/git/ref/tags/${tag}`, { allow404: true })).resolves.toBeNull();
    await expect(api(`/git/ref/tags/${tag}`, { allow404: true })).rejects.toThrow('HTTP 403');
    expect(fetchImpl.mock.calls.every(([, request]) => request.redirect === 'error')).toBe(true);
  });

  it('does not include credentials in API errors or output and never accepts external API paths', async () => {
    const fetchImpl = vi.fn().mockResolvedValue({ status: 500, ok: false });
    const api = createForkGitHubApi({
      repository: FORK_REPOSITORY,
      env: { GITHUB_TOKEN: 'private-test-token' },
      fetchImpl,
    });
    await expect(api('/releases')).rejects.toThrow('HTTP 500');
    try {
      await api('/releases');
    } catch (error) {
      expect(error.message).not.toContain('private-test-token');
    }
    expect(() => api('//evil.test/path')).toThrow(/Unsafe/);
    expect(() => api('https://evil.test/path')).toThrow(/Unsafe/);
  });

  it('preflight CLI writes exact artifact outputs without requiring a local payload', async () => {
    const directory = mkdtempSync(path.join(tmpdir(), 'cpamp-fork-preflight-'));
    directories.push(directory);
    const output = path.join(directory, 'output');
    const { api, state } = makeServer();
    // The HTTP mock returns 404, not JSON null, for absent tag/releases.
    vi.stubGlobal('fetch', async (url) => {
      const result = await api(url.replace(`https://api.github.com/repos/${FORK_REPOSITORY}`, ''));
      return { ok: result !== null, status: result === null ? 404 : 200, json: async () => result };
    });
    try {
      await runForkPublishCli(cliArguments(), {
        GITHUB_TOKEN: 'private-test-token',
        GITHUB_OUTPUT: output,
        GITHUB_REPOSITORY: FORK_REPOSITORY,
        GITHUB_WORKFLOW_REF: `${FORK_REPOSITORY}/${FORK_WORKFLOW_PATH}@refs/heads/main`,
      });
      expect(readFileSync(output, 'utf8')).toBe(
        `artifact_id=9001\nartifact_name=${forkArtifactName(tag, sourceCommit, '2')}\n`
      );
      expect(mutations(state)).toEqual([]);
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it.each([
    { GITHUB_REPOSITORY: 'seakee/CPA-Manager-Plus' },
    { GITHUB_WORKFLOW_REF: `${FORK_REPOSITORY}/${FORK_WORKFLOW_PATH}@refs/heads/dev` },
    { GITHUB_REF: 'refs/heads/dev' },
    { GITHUB_SHA: devCommit },
    { GITHUB_EVENT_NAME: 'push' },
  ])('rejects unsafe workflow environment %j before networking', async (environment) => {
    const fetchImpl = vi.fn();
    vi.stubGlobal('fetch', fetchImpl);
    try {
      await expect(
        runForkPublishCli(cliArguments(), { GITHUB_TOKEN: 'private-test-token', ...environment })
      ).rejects.toThrow(/workflow/);
      expect(fetchImpl).not.toHaveBeenCalled();
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it('rejects token flags, unknown flags and duplicate approval arguments', async () => {
    for (const extra of [
      ['--token', 'secret'],
      ['--unexpected', 'true'],
      ['--opt-in', 'true'],
    ]) {
      await expect(runForkPublishCli([...cliArguments(), ...extra], {})).rejects.toThrow(
        /argument/
      );
    }
  });
});

const cliArguments = () => [
  '--repository',
  FORK_REPOSITORY,
  '--tag',
  tag,
  '--sha',
  sourceCommit,
  '--dev-sha',
  devCommit,
  '--dry-run-id',
  '1001',
  '--dry-run-attempt',
  '2',
  '--receipt-sha',
  'd'.repeat(64),
  '--opt-in',
  'true',
  '--confirm-publish',
  'true',
  '--preflight-only',
  'true',
];
