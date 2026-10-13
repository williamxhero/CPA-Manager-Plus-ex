import { afterEach, describe, expect, it } from 'vitest';
import { execFileSync } from 'node:child_process';
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {
  FORK_BASE_SOURCE_COMMIT,
  FORK_MANIFEST_SCHEMA,
  FORK_RECEIPT_SCHEMA,
  FORK_REPOSITORY,
  FORK_WORKFLOW_PATH,
  REQUIRED_FORK_CHECKS,
  assertForkRepository,
  buildForkAssetRecords,
  buildForkReleaseInfo,
  expectedForkAssetNames,
  forkArtifactName,
  forkContentPaths,
  nativeForkAssetNames,
  parseForkTag,
  sha256,
  validateForkMetadata,
  validateForkReceipt,
  validateForkReleaseContent,
  validateForkReleaseInfo,
  validateForkRemoteRefs,
  validateForkTopology,
  validateNativeChecksums,
  verifyBinaryEmbedding,
} from '../bin/release/fork-release-contract.mjs';
import {
  validateForkBuildInputs,
  validateForkBuildOptions,
  validateForkSourceScope,
  validateForkTagAbsent,
} from '../bin/release/build-fork-release.mjs';

const tag = 'v1.14.5-williamxhero.1';
const source = '1'.repeat(40);
const dev = '2'.repeat(40);
const tree = '3'.repeat(40);
const directories = [];
const temporary = () => {
  const root = mkdtempSync(path.join(os.tmpdir(), 'cpamp-fork-contract-'));
  directories.push(root);
  return root;
};
const metadata = () => ({
  summary: { zh: '本地功能整合', en: 'Local feature integration' },
  update: {
    breaking: false,
    migration_required: false,
    minimum_direct_upgrade_version: null,
    upgrade_guide_url: `https://github.com/${FORK_REPOSITORY}/releases/tag/${tag}`,
  },
  compatibility: { minimum_cpa_version: null },
});
const writeContent = (root, update = (value) => value) => {
  const paths = forkContentPaths(tag);
  const contents = update({
    chinese: `[English](https://github.com/${FORK_REPOSITORY}/blob/${tag}/${paths.english})\n<!-- cpamp-fork-release\n${JSON.stringify(metadata())}\n-->`,
    english: `[中文](https://github.com/${FORK_REPOSITORY}/blob/${tag}/${paths.chinese})\nUnofficial fork.`,
    post: '<b>Unofficial fork: manual announcement only</b>',
  });
  for (const [key, relative] of Object.entries(paths)) {
    const file = path.join(root, relative);
    mkdirSync(path.dirname(file), { recursive: true });
    writeFileSync(file, contents[key]);
  }
  return contents;
};
const payload = () => {
  const directory = temporary();
  for (const name of nativeForkAssetNames(tag))
    writeFileSync(path.join(directory, name), `native ${name}`);
  writeFileSync(path.join(directory, 'management.html'), '<html>combined panel</html>');
  writeFileSync(
    path.join(directory, 'release-info.json'),
    JSON.stringify(buildForkReleaseInfo({ tag, sourceCommit: source, metadata: metadata() }))
  );
  writeFileSync(
    path.join(directory, 'checksums.txt'),
    nativeForkAssetNames(tag)
      .map((name) => `${sha256(readFileSync(path.join(directory, name)))}  ./${name}\n`)
      .join('')
  );
  return directory;
};
const receipt = () => {
  const assets = buildForkAssetRecords(payload(), tag);
  return {
    schema: FORK_RECEIPT_SCHEMA,
    schema_version: 1,
    repository: FORK_REPOSITORY,
    workflow_path: FORK_WORKFLOW_PATH,
    mode: 'dry-run',
    tag,
    source_commit: source,
    source_tree: tree,
    base_source_commit: FORK_BASE_SOURCE_COMMIT,
    frozen_refs: { main: source, dev },
    run: { id: '123', attempt: '1' },
    content: Object.values(forkContentPaths(tag)).map((file) => ({
      path: file,
      sha256: 'a'.repeat(64),
    })),
    toolchain: { node: 'v22.23.3', npm: '10.9.9', go: 'go version go1.24.13 linux/amd64' },
    checks: Object.fromEntries(REQUIRED_FORK_CHECKS.map((name) => [name, 'passed'])),
    assets: assets.map(({ name, size, digest }) => ({ name, size, digest })),
  };
};
const topologyGit =
  (overrides = {}) =>
  (args) => {
    const key = args.join(' ');
    const responses = {
      'rev-parse HEAD': source,
      'rev-parse origin/main': source,
      'rev-parse origin/dev': dev,
      [`rev-list --parents -n 1 ${source}`]: `${source} ${'4'.repeat(40)} ${dev}`,
      [`rev-list --parents -n 1 ${dev}`]: `${dev} ${'5'.repeat(40)} ${'6'.repeat(40)}`,
      [`rev-parse ${source}^{tree}`]: tree,
      [`rev-parse ${dev}^{tree}`]: tree,
      [`diff --name-status --no-renames ${'5'.repeat(40)} ${dev}`]: Object.values(
        forkContentPaths(tag)
      )
        .map((file) => `A\t${file}`)
        .join('\n'),
      [`merge-base --is-ancestor ${FORK_BASE_SOURCE_COMMIT} ${source}`]: '',
      ...overrides,
    };
    if (!Object.hasOwn(responses, key)) throw new Error(`Unexpected Git request ${key}`);
    return responses[key];
  };

afterEach(() => {
  directories.splice(0).forEach((root) => rmSync(root, { recursive: true, force: true }));
});

describe('fork release identity and content', () => {
  it('accepts only the explicit fork and fork-identifying SemVer', () => {
    expect(assertForkRepository(FORK_REPOSITORY)).toBe(FORK_REPOSITORY);
    expect(parseForkTag(tag)).toBe(tag);
    expect(forkArtifactName(tag, source, '2')).toBe(`fork-native-web-${tag}-${source}-attempt-2`);
  });
  it.each(['seakee/CPA-Manager-Plus', 'other/CPA-Manager-Plus', '', undefined])(
    'rejects repository %s',
    (repository) => {
      expect(() => assertForkRepository(repository)).toThrow();
    }
  );
  it.each([
    'v1.14.5',
    'v1.14.5-beta.1',
    'v1.14.5-williamxhero.01',
    'v1.14.5-williamxhero.0',
    'v01.14.5-williamxhero.1',
    '../v1.14.5-williamxhero.1',
    'v1.14.5-williamxhero.1+build',
  ])('rejects non-approved fork tag %s', (version) => {
    expect(() => parseForkTag(version)).toThrow();
  });
  it('validates exactly three reviewed files, metadata, reciprocal fork links and manual post', () => {
    const root = temporary();
    writeContent(root);
    const content = validateForkReleaseContent({ root, tag });
    expect(content.metadata).toEqual(metadata());
    expect(content.digests).toHaveLength(3);
    expect(content.digests.every((record) => /^[a-f0-9]{64}$/.test(record.sha256))).toBe(true);
  });
  it.each(['duplicate', 'upstream', 'missing-language-link', 'bad-post', 'bad-json'])(
    'rejects %s content',
    (scenario) => {
      const root = temporary();
      writeContent(root, (value) => {
        if (scenario === 'duplicate') value.chinese += value.chinese;
        if (scenario === 'upstream')
          value.chinese = value.chinese.replace('cpamp-fork-release', 'cpamp-update');
        if (scenario === 'missing-language-link') value.english = 'No link';
        if (scenario === 'bad-post') value.post = '<script>bad</script>';
        if (scenario === 'bad-json') value.chinese = value.chinese.replace('"summary"', 'summary');
        return value;
      });
      expect(() => validateForkReleaseContent({ root, tag })).toThrow();
    }
  );
  it.each([
    'https://github.com/seakee/CPA-Manager-Plus/releases/tag/v1.14.5',
    'https://example.com/williamxhero/CPA-Manager-Plus/',
    `http://github.com/${FORK_REPOSITORY}/`,
    `https://token@github.com/${FORK_REPOSITORY}/`,
    `https://github.com/${FORK_REPOSITORY}/../../seakee/CPA-Manager-Plus/`,
  ])('rejects unsafe or upstream upgrade guide %s', (url) => {
    const value = metadata();
    value.update.upgrade_guide_url = url;
    expect(() => validateForkMetadata(value)).toThrow();
  });
  it('requires explicit flags, bounded summaries, and no extra metadata sources', () => {
    const value = metadata();
    delete value.update.breaking;
    expect(() => validateForkMetadata(value)).toThrow();
    const oversized = metadata();
    oversized.summary.en = 'a'.repeat(4097);
    expect(() => validateForkMetadata(oversized)).toThrow();
    expect(() => validateForkMetadata({ ...metadata(), docker: {} })).toThrow();
  });
  it('never claims upstream updater or Docker compatibility', () => {
    const info = buildForkReleaseInfo({ tag, sourceCommit: source, metadata: metadata() });
    expect(info).toMatchObject({
      schema: FORK_MANIFEST_SCHEMA,
      updater_compatible: false,
      repository: FORK_REPOSITORY,
    });
    expect(validateForkReleaseInfo(info, tag, source)).toBe(info);
    info.distribution.docker = { image: 'seakee/cpa-manager-plus' };
    expect(() => validateForkReleaseInfo(info, tag, source)).toThrow();
  });
  it('rejects changed manifest identity, URLs, source, or compatibility', () => {
    for (const mutate of [
      (info) => {
        info.updater_compatible = true;
      },
      (info) => {
        info.repository = 'seakee/CPA-Manager-Plus';
      },
      (info) => {
        info.release.source_commit = dev;
      },
      (info) => {
        info.content.notes.zh = 'https://github.com/seakee/CPA-Manager-Plus/';
      },
    ]) {
      const info = buildForkReleaseInfo({ tag, sourceCommit: source, metadata: metadata() });
      mutate(info);
      expect(() => validateForkReleaseInfo(info, tag, source)).toThrow();
    }
  });
});

describe('fork payload and immutable receipt', () => {
  it('validates nine exact assets and six native checksums', () => {
    const assets = buildForkAssetRecords(payload(), tag);
    expect(assets.map((record) => record.name)).toEqual(expectedForkAssetNames(tag));
    expect(assets).toHaveLength(9);
  });
  it.each(['extra', 'missing', 'empty', 'directory', 'checksum'])(
    'rejects %s payload',
    (scenario) => {
      const root = payload();
      const name = nativeForkAssetNames(tag)[0];
      if (scenario === 'extra') writeFileSync(path.join(root, 'unexpected.json'), '{}');
      if (scenario === 'missing') rmSync(path.join(root, name));
      if (scenario === 'empty') writeFileSync(path.join(root, name), '');
      if (scenario === 'directory') {
        rmSync(path.join(root, name));
        mkdirSync(path.join(root, name));
      }
      if (scenario === 'checksum') writeFileSync(path.join(root, name), 'changed');
      expect(() => buildForkAssetRecords(root, tag)).toThrow();
    }
  );
  it.each(['../archive.tar.gz', '/archive.tar.gz', 'folder/archive.tar.gz', 'folder\\archive.zip'])(
    'rejects checksum path %s',
    (name) => {
      expect(() => validateNativeChecksums(`${'a'.repeat(64)}  ${name}\n`, tag, [])).toThrow();
    }
  );
  it('rejects duplicate or missing native checksum entries', () => {
    const root = payload();
    const assets = buildForkAssetRecords(root, tag);
    const body = readFileSync(path.join(root, 'checksums.txt'), 'utf8');
    expect(() => validateNativeChecksums(body + body, tag, assets)).toThrow();
    expect(() =>
      validateNativeChecksums(body.split('\n').slice(1).join('\n'), tag, assets)
    ).toThrow();
  });
  it('binds every asset, source, tree, dev ref and reviewed content digest', () => {
    const value = receipt();
    expect(
      validateForkReceipt(value, {
        tag,
        sourceCommit: source,
        sourceTree: tree,
        devCommit: dev,
        runId: '123',
        runAttempt: '1',
        assets: value.assets,
        contentDigests: value.content,
      })
    ).toBe(value);
  });
  it.each(['sourceCommit', 'sourceTree', 'devCommit', 'runId', 'runAttempt', 'tag'])(
    'rejects drifted %s',
    (key) => {
      expect(() => validateForkReceipt(receipt(), { [key]: 'different' })).toThrow();
    }
  );
  it('rejects changed asset bytes, content hashes, failed gates and unknown receipt fields', () => {
    const value = receipt();
    const assets = structuredClone(value.assets);
    assets[0].digest = `sha256:${'f'.repeat(64)}`;
    expect(() => validateForkReceipt(value, { assets })).toThrow();
    const content = structuredClone(value.content);
    content[0].sha256 = 'f'.repeat(64);
    expect(() => validateForkReceipt(value, { contentDigests: content })).toThrow();
    value.checks['web-tests'] = 'skipped';
    expect(() => validateForkReceipt(value)).toThrow();
    expect(() => validateForkReceipt({ ...receipt(), allow_upstream: true })).toThrow();
  });
  it('rejects duplicate asset names, missing checks and non-string run identifiers', () => {
    const value = receipt();
    value.assets[0].name = value.assets[1].name;
    expect(() => validateForkReceipt(value)).toThrow();
    const missing = receipt();
    delete missing.checks['backend-tests'];
    expect(() => validateForkReceipt(missing)).toThrow();
    const numeric = receipt();
    numeric.run.id = 123;
    expect(() => validateForkReceipt(numeric)).toThrow();
  });
  it('requires exact embedded HTML and source SHA', () => {
    const html = Buffer.from('<html>joint features</html>');
    verifyBinaryEmbedding(Buffer.concat([Buffer.from(source), html]), html, source);
    expect(() => verifyBinaryEmbedding(Buffer.from(source), html, source)).toThrow();
    expect(() => verifyBinaryEmbedding(html, html, source)).toThrow();
  });
});

describe('frozen source and promotion gates', () => {
  it('rejects ignored private docs even when ordinary Git status is clean', () => {
    const root = temporary();
    const git = (args) =>
      execFileSync('git', args, {
        cwd: root,
        encoding: 'utf8',
        stdio: ['ignore', 'pipe', 'pipe'],
      }).trim();
    git(['init', '--quiet']);
    writeFileSync(path.join(root, '.gitignore'), '*.local\n');
    mkdirSync(path.join(root, 'docs'));
    writeFileSync(path.join(root, 'docs', 'README.md'), 'Tracked public documentation');
    git(['add', '.gitignore', 'docs/README.md']);
    git([
      '-c',
      'user.name=Fixture',
      '-c',
      'user.email=fixture@example.test',
      'commit',
      '--quiet',
      '-m',
      'fixture',
    ]);
    validateForkBuildInputs({ git });
    writeFileSync(path.join(root, 'docs', 'private.local'), 'must not enter packages');
    expect(git(['status', '--porcelain', '--untracked-files=all'])).toBe('');
    expect(() => validateForkBuildInputs({ git })).toThrow(/ignored files/);
  });
  it('rejects an already-created fork tag before dry-run', () => {
    validateForkTagAbsent({ git: () => '', tag });
    expect(() => validateForkTagAbsent({ git: () => `${source}\\trefs/tags/${tag}`, tag })).toThrow(
      /must not exist/
    );
  });
  it('accepts one promotion merge and a three-file release merge', () => {
    expect(
      validateForkTopology({ tag, sourceCommit: source, devCommit: dev, git: topologyGit() })
    ).toEqual({ sourceCommit: source, devCommit: dev, sourceTree: tree });
  });
  it.each(['head', 'main-ref', 'dev-ref', 'promotion', 'release-merge', 'tree', 'release-scope'])(
    'rejects %s drift',
    (scenario) => {
      const overrides = {};
      if (scenario === 'head') overrides['rev-parse HEAD'] = dev;
      if (scenario === 'main-ref') overrides['rev-parse origin/main'] = dev;
      if (scenario === 'dev-ref') overrides['rev-parse origin/dev'] = source;
      if (scenario === 'promotion')
        overrides[`rev-list --parents -n 1 ${source}`] = `${source} ${dev}`;
      if (scenario === 'release-merge')
        overrides[`rev-list --parents -n 1 ${dev}`] = `${dev} ${tree}`;
      if (scenario === 'tree') overrides[`rev-parse ${dev}^{tree}`] = dev;
      if (scenario === 'release-scope')
        overrides[`diff --name-status --no-renames ${'5'.repeat(40)} ${dev}`] = 'M\tREADME.md';
      expect(() =>
        validateForkTopology({
          tag,
          sourceCommit: source,
          devCommit: dev,
          git: topologyGit(overrides),
        })
      ).toThrow();
    }
  );
  it('checks live fork refs without accepting the upstream origin', () => {
    const git = (args) =>
      args[0] === 'remote'
        ? `https://github.com/${FORK_REPOSITORY}.git`
        : `${dev}\trefs/heads/dev\n${source}\trefs/heads/main`;
    validateForkRemoteRefs({ sourceCommit: source, devCommit: dev, git });
    expect(() =>
      validateForkRemoteRefs({
        sourceCommit: source,
        devCommit: dev,
        git: () => 'https://github.com/seakee/CPA-Manager-Plus.git',
      })
    ).toThrow();
    expect(() => validateForkRemoteRefs({ sourceCommit: tree, devCommit: dev, git })).toThrow();
  });
  it('allows only approved fork tooling, three content files and supplemental quota test changes', () => {
    const contentPaths = forkContentPaths(tag);
    const git = () =>
      `bin/release/fork-release-contract.mjs\n${contentPaths.chinese}\napps/web/src/utils/quota/opencodeGoQuota.test.ts`;
    validateForkSourceScope({ git, sourceCommit: source, contentPaths });
    expect(() =>
      validateForkSourceScope({
        git: () => '.github/workflows/release.yml',
        sourceCommit: source,
        contentPaths,
      })
    ).toThrow();
    expect(() =>
      validateForkSourceScope({
        git: () => 'apps/web/src/stores/useQuotaStore.ts',
        sourceCommit: source,
        contentPaths,
      })
    ).toThrow();
  });
  it('requires explicit opt-in and positive run identity for builds', () => {
    const value = {
      repository: FORK_REPOSITORY,
      tag,
      sourceCommit: source,
      devCommit: dev,
      output: '/tmp/fork',
      runId: '123',
      runAttempt: '1',
      optIn: true,
    };
    expect(validateForkBuildOptions(value)).toBe(value);
    expect(() => validateForkBuildOptions({ ...value, optIn: false })).toThrow();
    expect(() => validateForkBuildOptions({ ...value, runId: 'local' })).toThrow();
    expect(() => validateForkBuildOptions({ ...value, sourceCommit: 'main' })).toThrow();
  });
});
