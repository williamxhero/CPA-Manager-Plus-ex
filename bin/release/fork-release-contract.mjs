import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { lstatSync, readFileSync, readdirSync } from 'node:fs';
import path from 'node:path';
import { parseReleaseTag, validateTelegramHtml } from './validate-release.mjs';

export const FORK_REPOSITORY = 'williamxhero/CPA-Manager-Plus';
export const FORK_BASE_SOURCE_COMMIT = '233a225fe9e13375670e1bb93d9ad76a04b99c84';
export const FORK_WORKFLOW_PATH = '.github/workflows/fork-native-web-release.yml';
export const FORK_MANIFEST_SCHEMA = 'cpamp-fork-native-web-release';
export const FORK_RECEIPT_SCHEMA = 'cpamp-fork-native-web-dry-run';
export const REQUIRED_FORK_CHECKS = [
  'type-check',
  'web-lint',
  'web-tests',
  'repository-tests',
  'backend-tests',
  'backend-build',
  'web-build',
  'demo-isolation',
  'web-icons',
  'native-packaging',
  'native-checksums',
  'native-embedding',
];

const commitPattern = /^[a-f0-9]{40}$/;
const digestPattern = /^[a-f0-9]{64}$/;
const forkTagPattern = /^v(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)-williamxhero\.[1-9]\d*$/;
const positiveIntegerPattern = /^[1-9]\d*$/;
const targets = [
  'darwin_amd64',
  'darwin_arm64',
  'linux_amd64',
  'linux_arm64',
  'windows_amd64',
  'windows_arm64',
];
const fail = (message) => {
  throw new Error(message);
};
const sameList = (left, right) =>
  left.length === right.length && left.every((value, index) => value === right[index]);
const sorted = (values) => [...values].sort((left, right) => left.localeCompare(right));
const object = (value, label) => {
  if (!value || typeof value !== 'object' || Array.isArray(value))
    fail(`${label} must be an object`);
  return value;
};
const keys = (value, names, label) => {
  object(value, label);
  if (!sameList(sorted(Object.keys(value)), sorted(names)))
    fail(`${label} has missing or unexpected fields`);
};
const text = (value, label, maximumBytes = 4096) => {
  if (typeof value !== 'string' || !value.trim() || Buffer.byteLength(value) > maximumBytes)
    fail(`${label} must be non-empty text (at most ${maximumBytes} bytes)`);
  return value;
};
export const validateCommit = (value, label = 'commit') => {
  if (typeof value !== 'string' || !commitPattern.test(value))
    fail(`${label} must be a full lowercase commit SHA`);
  return value;
};
export const validateDigest = (value, label = 'digest') => {
  if (typeof value !== 'string' || !digestPattern.test(value))
    fail(`${label} must be a lowercase SHA-256`);
  return value;
};
export const assertForkRepository = (repository) => {
  if (repository !== FORK_REPOSITORY)
    fail(`Only ${FORK_REPOSITORY} is authorized for fork releases`);
  return repository;
};
export const parseForkTag = (tag) => {
  parseReleaseTag(tag);
  if (!forkTagPattern.test(tag))
    fail('Fork version must use the -williamxhero.<positive integer> suffix');
  return tag;
};
export const sha256 = (value) => createHash('sha256').update(value).digest('hex');
export const sha256File = (filePath) => sha256(readFileSync(filePath));
export const forkArtifactName = (tag, sourceSha, attempt) => {
  parseForkTag(tag);
  validateCommit(sourceSha);
  if (!positiveIntegerPattern.test(String(attempt))) fail('Run attempt must be a positive integer');
  return `fork-native-web-${tag}-${sourceSha}-attempt-${attempt}`;
};
export const forkContentPaths = (tag) => {
  parseForkTag(tag);
  return {
    chinese: `docs/fork-release-notes/${tag}-zh.md`,
    english: `docs/fork-release-notes/${tag}-en.md`,
    post: `docs/fork-release-posts/${tag}-telegram.html`,
  };
};
export const nativeForkAssetNames = (tag) => {
  parseForkTag(tag);
  return targets.map(
    (target) =>
      `cpa-manager-plus_${tag}_${target}.${target.startsWith('windows') ? 'zip' : 'tar.gz'}`
  );
};
export const expectedForkAssetNames = (tag) =>
  sorted([...nativeForkAssetNames(tag), 'checksums.txt', 'management.html', 'release-info.json']);
const forkUrl = (value, label) => {
  text(value, label);
  let url;
  try {
    url = new URL(value);
  } catch {
    fail(`${label} must be an absolute fork HTTPS URL`);
  }
  if (
    url.protocol !== 'https:' ||
    url.hostname !== 'github.com' ||
    url.port ||
    url.username ||
    url.password ||
    !url.pathname.startsWith(`/${FORK_REPOSITORY}/`)
  )
    fail(`${label} must point to ${FORK_REPOSITORY}`);
  return value;
};
const nullableTag = (value, label) => {
  if (value !== null) {
    try {
      parseReleaseTag(value);
    } catch {
      fail(`${label} must be a SemVer tag or null`);
    }
  }
};
export const validateForkMetadata = (metadata) => {
  keys(metadata, ['summary', 'update', 'compatibility'], 'fork metadata');
  keys(metadata.summary, ['zh', 'en'], 'summary');
  text(metadata.summary.zh, 'summary.zh');
  text(metadata.summary.en, 'summary.en');
  keys(
    metadata.update,
    ['breaking', 'migration_required', 'minimum_direct_upgrade_version', 'upgrade_guide_url'],
    'update'
  );
  if (
    typeof metadata.update.breaking !== 'boolean' ||
    typeof metadata.update.migration_required !== 'boolean'
  )
    fail('Upgrade flags must be explicit booleans');
  nullableTag(metadata.update.minimum_direct_upgrade_version, 'minimum_direct_upgrade_version');
  forkUrl(metadata.update.upgrade_guide_url, 'upgrade_guide_url');
  keys(metadata.compatibility, ['minimum_cpa_version'], 'compatibility');
  nullableTag(metadata.compatibility.minimum_cpa_version, 'minimum_cpa_version');
  return metadata;
};
export const validateForkReleaseContent = ({ root, tag, readFile = readFileSync }) => {
  const paths = forkContentPaths(tag);
  const contents = Object.fromEntries(
    Object.entries(paths).map(([key, relative]) => [
      key,
      readFile(path.join(root, relative), 'utf8'),
    ])
  );
  Object.entries(contents).forEach(([key, value]) => text(value, `release ${key}`, 64 * 1024));
  const matches = [...contents.chinese.matchAll(/<!--\s*cpamp-fork-release\s+([\s\S]*?)-->/g)];
  if (matches.length !== 1 || contents.chinese.includes('cpamp-update'))
    fail('Chinese notes require exactly one cpamp-fork-release block, not upstream metadata');
  let metadata;
  try {
    metadata = JSON.parse(matches[0][1]);
  } catch {
    fail('Fork metadata block is not valid JSON');
  }
  validateForkMetadata(metadata);
  const noteUrl = (language) =>
    `https://github.com/${FORK_REPOSITORY}/blob/${tag}/docs/fork-release-notes/${tag}-${language}.md`;
  if (!contents.chinese.includes(noteUrl('en')) || !contents.english.includes(noteUrl('zh')))
    fail('Fork notes require reciprocal tag-pinned fork language links');
  validateTelegramHtml(contents.post);
  return {
    paths,
    contents,
    metadata,
    digests: Object.values(paths).map((relative, index) => ({
      path: relative,
      sha256: sha256(Object.values(contents)[index]),
    })),
  };
};
export const buildForkReleaseInfo = ({ tag, sourceCommit, metadata }) => {
  parseForkTag(tag);
  validateCommit(sourceCommit, 'source_commit');
  validateForkMetadata(metadata);
  return {
    schema: FORK_MANIFEST_SCHEMA,
    schema_version: 1,
    repository: FORK_REPOSITORY,
    updater_compatible: false,
    release: {
      version: tag,
      stage: 'fork',
      source_commit: sourceCommit,
      base_source_commit: FORK_BASE_SOURCE_COMMIT,
    },
    content: {
      summary: metadata.summary,
      notes: Object.fromEntries(
        ['zh', 'en'].map((language) => [
          language,
          `https://github.com/${FORK_REPOSITORY}/blob/${tag}/docs/fork-release-notes/${tag}-${language}.md`,
        ])
      ),
    },
    update: metadata.update,
    compatibility: metadata.compatibility,
    distribution: {
      web: { asset: 'management.html' },
      native: { assets: nativeForkAssetNames(tag) },
    },
  };
};
export const validateForkReleaseInfo = (info, tag, sourceCommit) => {
  keys(
    info,
    [
      'schema',
      'schema_version',
      'repository',
      'updater_compatible',
      'release',
      'content',
      'update',
      'compatibility',
      'distribution',
    ],
    'fork release-info'
  );
  assertForkRepository(info.repository);
  if (
    info.schema !== FORK_MANIFEST_SCHEMA ||
    info.schema_version !== 1 ||
    info.updater_compatible !== false
  )
    fail('Invalid fork manifest schema or updater compatibility');
  keys(info.release, ['version', 'stage', 'source_commit', 'base_source_commit'], 'release');
  parseForkTag(info.release.version);
  if (
    info.release.version !== tag ||
    info.release.stage !== 'fork' ||
    info.release.base_source_commit !== FORK_BASE_SOURCE_COMMIT
  )
    fail('Fork manifest release identity mismatch');
  validateCommit(info.release.source_commit);
  if (sourceCommit && info.release.source_commit !== sourceCommit)
    fail('Fork manifest source SHA mismatch');
  keys(info.content, ['summary', 'notes'], 'content');
  keys(info.content.notes, ['zh', 'en'], 'notes');
  for (const language of ['zh', 'en']) {
    if (
      info.content.notes[language] !==
      `https://github.com/${FORK_REPOSITORY}/blob/${tag}/docs/fork-release-notes/${tag}-${language}.md`
    )
      fail('Fork note URL mismatch');
  }
  validateForkMetadata({
    summary: info.content.summary,
    update: info.update,
    compatibility: info.compatibility,
  });
  keys(info.distribution, ['web', 'native'], 'distribution');
  keys(info.distribution.web, ['asset'], 'web');
  keys(info.distribution.native, ['assets'], 'native');
  if (
    info.distribution.web.asset !== 'management.html' ||
    !Array.isArray(info.distribution.native.assets) ||
    !sameList(sorted(info.distribution.native.assets), sorted(nativeForkAssetNames(tag)))
  )
    fail('Fork distribution asset mismatch');
  return info;
};
export const validateNativeChecksums = (body, tag, assets) => {
  const expected = new Set(nativeForkAssetNames(tag));
  const records = new Map(assets.map((asset) => [asset.name, asset]));
  const seen = new Set();
  const lines = String(body).trimEnd().split(/\r?\n/);
  for (const line of lines) {
    const match = /^([a-f0-9]{64})[ \t]+\*?(?:\.\/)?([^/\\\s]+)$/.exec(line);
    if (!match || !expected.has(match[2]) || seen.has(match[2]))
      fail('Native checksums contain an unexpected, duplicate, or unsafe entry');
    seen.add(match[2]);
    if (records.get(match[2])?.digest !== `sha256:${match[1]}`)
      fail(`Native checksum mismatch: ${match[2]}`);
  }
  if (seen.size !== expected.size) fail('Native checksums must cover exactly six archives');
};
export const buildForkAssetRecords = (assetsDir, tag) => {
  const names = sorted(readdirSync(assetsDir));
  if (!sameList(names, expectedForkAssetNames(tag)))
    fail('Fork payload must contain exactly nine expected assets');
  const assets = names.map((name) => {
    const filePath = path.join(assetsDir, name);
    const stats = lstatSync(filePath);
    if (!stats.isFile() || stats.isSymbolicLink() || stats.size <= 0)
      fail(`Asset must be a non-empty regular file: ${name}`);
    return { name, size: stats.size, digest: `sha256:${sha256File(filePath)}`, filePath };
  });
  validateForkReleaseInfo(
    JSON.parse(readFileSync(path.join(assetsDir, 'release-info.json'), 'utf8')),
    tag
  );
  validateNativeChecksums(readFileSync(path.join(assetsDir, 'checksums.txt'), 'utf8'), tag, assets);
  return assets;
};
export const verifyBinaryEmbedding = (binary, html, sourceCommit) => {
  validateCommit(sourceCommit);
  if (
    !Buffer.isBuffer(binary) ||
    !Buffer.isBuffer(html) ||
    html.length === 0 ||
    !binary.includes(html) ||
    !binary.includes(Buffer.from(sourceCommit))
  )
    fail('Native binary must contain exact panel bytes and source SHA');
};
export const validateForkReceipt = (receipt, expected = {}) => {
  keys(
    receipt,
    [
      'schema',
      'schema_version',
      'repository',
      'workflow_path',
      'mode',
      'tag',
      'source_commit',
      'source_tree',
      'base_source_commit',
      'frozen_refs',
      'run',
      'content',
      'toolchain',
      'checks',
      'assets',
    ],
    'receipt'
  );
  assertForkRepository(receipt.repository);
  if (
    receipt.schema !== FORK_RECEIPT_SCHEMA ||
    receipt.schema_version !== 1 ||
    receipt.workflow_path !== FORK_WORKFLOW_PATH ||
    receipt.mode !== 'dry-run'
  )
    fail('Invalid dry-run receipt identity');
  parseForkTag(receipt.tag);
  validateCommit(receipt.source_commit, 'receipt source');
  validateCommit(receipt.source_tree, 'receipt tree');
  if (receipt.base_source_commit !== FORK_BASE_SOURCE_COMMIT) fail('Receipt feature base mismatch');
  keys(receipt.frozen_refs, ['main', 'dev'], 'frozen refs');
  validateCommit(receipt.frozen_refs.main);
  validateCommit(receipt.frozen_refs.dev);
  if (receipt.frozen_refs.main !== receipt.source_commit) fail('Receipt main source mismatch');
  keys(receipt.run, ['id', 'attempt'], 'run');
  if (
    typeof receipt.run.id !== 'string' ||
    typeof receipt.run.attempt !== 'string' ||
    !positiveIntegerPattern.test(receipt.run.id) ||
    !positiveIntegerPattern.test(receipt.run.attempt)
  )
    fail('Receipt requires positive numeric run ID and attempt');
  const comparisons = {
    tag: receipt.tag,
    sourceCommit: receipt.source_commit,
    sourceTree: receipt.source_tree,
    devCommit: receipt.frozen_refs.dev,
    runId: receipt.run.id,
    runAttempt: receipt.run.attempt,
  };
  for (const [key, value] of Object.entries(comparisons)) {
    if (expected[key] !== undefined && String(expected[key]) !== value)
      fail(`Receipt ${key} mismatch`);
  }
  keys(receipt.toolchain, ['node', 'npm', 'go'], 'toolchain');
  Object.entries(receipt.toolchain).forEach(([key, value]) => text(value, `toolchain.${key}`));
  keys(receipt.checks, REQUIRED_FORK_CHECKS, 'checks');
  if (Object.values(receipt.checks).some((status) => status !== 'passed'))
    fail('Receipt checks must all pass');
  const paths = Object.values(forkContentPaths(receipt.tag));
  if (
    !Array.isArray(receipt.content) ||
    !sameList(sorted(receipt.content.map((record) => record.path)), sorted(paths))
  )
    fail('Receipt content set mismatch');
  receipt.content.forEach((record) => {
    keys(record, ['path', 'sha256'], 'content digest');
    validateDigest(record.sha256);
  });
  if (expected.contentDigests) {
    for (const record of receipt.content) {
      if (
        expected.contentDigests.find((item) => item.path === record.path)?.sha256 !== record.sha256
      )
        fail(`Receipt content digest mismatch: ${record.path}`);
    }
  }
  if (
    !Array.isArray(receipt.assets) ||
    !sameList(
      sorted(receipt.assets.map((asset) => asset.name)),
      expectedForkAssetNames(receipt.tag)
    )
  )
    fail('Receipt asset set mismatch');
  receipt.assets.forEach((asset) => {
    keys(asset, ['name', 'size', 'digest'], 'receipt asset');
    if (
      !Number.isSafeInteger(asset.size) ||
      asset.size <= 0 ||
      !/^sha256:[a-f0-9]{64}$/.test(asset.digest)
    )
      fail('Invalid receipt asset metadata');
    if (expected.assets) {
      const actual = expected.assets.find((record) => record.name === asset.name);
      if (!actual || actual.size !== asset.size || actual.digest !== asset.digest)
        fail(`Receipt asset digest/size mismatch: ${asset.name}`);
    }
  });
  return receipt;
};
export const validateForkRemoteRefs = ({ sourceCommit, devCommit, git }) => {
  const origin = git(['remote', 'get-url', 'origin']);
  if (
    ![
      `https://github.com/${FORK_REPOSITORY}`,
      `https://github.com/${FORK_REPOSITORY}.git`,
      `git@github.com:${FORK_REPOSITORY}.git`,
    ].includes(origin)
  )
    fail('Fork builder origin must point to the authorized fork');
  const lines = git(['ls-remote', 'origin', 'refs/heads/main', 'refs/heads/dev'])
    .split('\n')
    .filter(Boolean);
  const refs = new Map(
    lines.map((line) => {
      const [sha, ref] = line.split(/\s+/);
      validateCommit(sha);
      return [ref, sha];
    })
  );
  if (
    lines.length !== 2 ||
    refs.size !== 2 ||
    refs.get('refs/heads/main') !== sourceCommit ||
    refs.get('refs/heads/dev') !== devCommit
  )
    fail('Live fork main/dev refs drifted');
};
export const validateForkTopology = ({ tag, sourceCommit, devCommit, git }) => {
  parseForkTag(tag);
  validateCommit(sourceCommit);
  validateCommit(devCommit);
  if (
    git(['rev-parse', 'HEAD']) !== sourceCommit ||
    git(['rev-parse', 'origin/main']) !== sourceCommit ||
    git(['rev-parse', 'origin/dev']) !== devCommit
  )
    fail('Frozen fork main/dev refs drifted');
  const parents = (sha) => {
    const values = git(['rev-list', '--parents', '-n', '1', sha]).split(/\s+/);
    if (values.shift() !== sha) fail('Git returned an unexpected commit identity');
    values.forEach((value) => validateCommit(value));
    return values;
  };
  const mainParents = parents(sourceCommit);
  const devParents = parents(devCommit);
  if (mainParents.length !== 2 || mainParents[1] !== devCommit || devParents.length !== 2)
    fail('Fork release requires release-PR merge followed by one dev-to-main promotion merge');
  const tree = (sha) => git(['rev-parse', `${sha}^{tree}`]);
  if (tree(sourceCommit) !== tree(devCommit))
    fail('Main tree must exactly match the frozen release dev tree');
  const changes = git(['diff', '--name-status', '--no-renames', devParents[0], devCommit])
    .split('\n')
    .filter(Boolean)
    .map((line) => line.split('\t'));
  if (
    changes.some(([status]) => status !== 'A') ||
    !sameList(sorted(changes.map(([, file]) => file)), sorted(Object.values(forkContentPaths(tag))))
  )
    fail('Release PR must add exactly three fork versioned files');
  git(['merge-base', '--is-ancestor', FORK_BASE_SOURCE_COMMIT, sourceCommit]);
  return { sourceCommit, devCommit, sourceTree: tree(sourceCommit) };
};
export const createForkGit = (root) => (args) =>
  execFileSync('git', args, {
    cwd: root,
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'pipe'],
  }).trim();
