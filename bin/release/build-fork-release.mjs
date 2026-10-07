import { execFileSync } from 'node:child_process';
import {
  copyFileSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  FORK_BASE_SOURCE_COMMIT,
  FORK_RECEIPT_SCHEMA,
  FORK_REPOSITORY,
  FORK_WORKFLOW_PATH,
  REQUIRED_FORK_CHECKS,
  assertForkRepository,
  buildForkAssetRecords,
  buildForkReleaseInfo,
  createForkGit,
  nativeForkAssetNames,
  parseForkTag,
  validateCommit,
  validateForkReceipt,
  validateForkReleaseContent,
  validateForkRemoteRefs,
  validateForkTopology,
  verifyBinaryEmbedding,
} from './fork-release-contract.mjs';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const fail = (message) => {
  throw new Error(message);
};
const approvedToolingPaths = [
  'bin/release/fork-release-contract.mjs',
  'bin/release/build-fork-release.mjs',
  'bin/release/publish-fork-release.mjs',
  '.github/workflows/fork-native-web-release.yml',
  'tests/forkReleaseContract.test.mjs',
  'tests/forkReleasePublish.test.mjs',
  'tests/forkReleaseWorkflow.test.mjs',
  'docs/fork-native-web-release.md',
  'apps/web/src/utils/quota/opencodeGoQuota.test.ts',
];

export const validateForkBuildOptions = (options) => {
  assertForkRepository(options.repository);
  parseForkTag(options.tag);
  validateCommit(options.sourceCommit, 'expected main SHA');
  validateCommit(options.devCommit, 'frozen dev SHA');
  if (options.optIn !== true) fail('Fork build requires explicit opt-in');
  if (!/^[1-9]\d*$/.test(options.runId) || !/^[1-9]\d*$/.test(options.runAttempt))
    fail('Dry-run requires numeric run ID and attempt');
  if (typeof options.output !== 'string' || !options.output.trim())
    fail('An output directory is required');
  return options;
};

export const validateForkBuildInputs = ({ git }) => {
  const inputs = ['docs', 'apps/manager-server', ':(glob).env*', ':(glob)apps/web/.env*'];
  for (const ignored of [false, true]) {
    const entries = git([
      'ls-files',
      '--others',
      ...(ignored ? ['--ignored'] : []),
      '--exclude-standard',
      '-z',
      '--',
      ...inputs,
    ])
      .split('\0')
      .filter(Boolean);
    if (entries.length)
      fail(`Untracked or ignored files may not enter fork build inputs: ${entries.join(', ')}`);
  }
};

export const validateForkTagAbsent = ({ git, tag }) => {
  parseForkTag(tag);
  if (git(['ls-remote', '--tags', 'origin', `refs/tags/${tag}`]))
    fail('Fork tag must not exist before dry-run');
};

export const validateForkSourceScope = ({ git, sourceCommit, contentPaths }) => {
  const permitted = new Set([...approvedToolingPaths, ...Object.values(contentPaths)]);
  const changed = git(['diff', '--name-only', FORK_BASE_SOURCE_COMMIT, sourceCommit])
    .split('\n')
    .filter(Boolean);
  const outsideScope = changed.filter((file) => !permitted.has(file));
  if (outsideScope.length)
    fail(`Source changes exceed the approved fork scope: ${outsideScope.join(', ')}`);
};

export const buildForkRelease = (
  options,
  {
    root = repoRoot,
    git = createForkGit(root),
    execute = (command, args, config = {}) =>
      execFileSync(command, args, {
        cwd: root,
        stdio: 'inherit',
        ...config,
      }),
  } = {}
) => {
  validateForkBuildOptions(options);
  if (process.platform !== 'linux')
    fail('Use the isolated Linux builder for fork release packaging');
  if (process.env.GITHUB_REPOSITORY) {
    assertForkRepository(process.env.GITHUB_REPOSITORY);
    if (
      process.env.GITHUB_REF !== 'refs/heads/main' ||
      process.env.GITHUB_SHA !== options.sourceCommit
    )
      fail('Workflow must run from the exact reviewed fork main SHA');
  }
  if (git(['status', '--porcelain', '--untracked-files=all']))
    fail('Fork release source must be clean');
  validateForkBuildInputs({ git });
  validateForkTagAbsent({ git, tag: options.tag });
  validateForkRemoteRefs({ sourceCommit: options.sourceCommit, devCommit: options.devCommit, git });
  const topology = validateForkTopology({
    tag: options.tag,
    sourceCommit: options.sourceCommit,
    devCommit: options.devCommit,
    git,
  });
  const content = validateForkReleaseContent({ root, tag: options.tag });
  validateForkSourceScope({ git, sourceCommit: options.sourceCommit, contentPaths: content.paths });
  const output = path.resolve(options.output);
  if (existsSync(output)) fail('Output already exists; never overwrite a dry-run payload');

  const capture = (command, args) =>
    String(execute(command, args, { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] })).trim();
  const toolchain = {
    node: process.version,
    npm: capture('npm', ['--version']),
    go: capture('go', ['version']),
  };
  if (!/^go version go1\.24\./.test(toolchain.go))
    fail('Fork release requires the reviewed Go 1.24 toolchain');
  mkdirSync(output, { recursive: true });
  const temporaryParent = path.join(root, 'bin/tmp/release');
  mkdirSync(temporaryParent, { recursive: true });
  const temporary = mkdtempSync(path.join(temporaryParent, 'fork-native-'));
  const payload = path.join(output, 'payload');
  const checks = {};
  const check = (name, command, args, config) => {
    execute(command, args, config);
    checks[name] = 'passed';
  };

  try {
    check('type-check', 'npm', ['run', 'type-check']);
    check('web-lint', 'npm', ['run', 'lint']);
    check('web-tests', 'npm', ['run', 'test:web']);
    check('repository-tests', 'npm', ['run', 'test:repo']);
    check('backend-tests', 'go', ['test', '-count=1', './...'], {
      cwd: path.join(root, 'apps/manager-server'),
    });
    check('backend-build', 'go', ['build', './...'], {
      cwd: path.join(root, 'apps/manager-server'),
      env: { ...process.env, CGO_ENABLED: '0' },
    });
    check('web-build', 'npm', ['run', 'build']);
    check('demo-isolation', 'npm', ['run', 'check:demo-isolation']);
    check('web-icons', 'npm', ['run', 'check:web-icons']);
    const html = readFileSync(path.join(root, 'apps/web/dist/index.html'));
    const panel = html.toString('utf8');
    for (const marker of [
      'provider-key-aliases',
      'opencodeGoQuota',
      'setOpenCodeGoQuota',
      '/plugins/opencode-go-cliproxyapi/quota-usage',
      'OpenAI-compatible base URL is required',
    ]) {
      if (!panel.includes(marker)) fail(`Combined panel is missing an approved feature: ${marker}`);
    }
    const nativeDir = path.join(temporary, 'native');
    validateForkBuildInputs({ git });
    check('native-packaging', 'bash', ['bin/release/package-native.sh'], {
      env: {
        ...process.env,
        VERSION: options.tag,
        OUT_DIR: nativeDir,
        WEB_HTML: path.join(root, 'apps/web/dist/index.html'),
        WEB_FAVICON: path.join(root, 'apps/web/dist/favicon.ico'),
        WEB_APPLE_TOUCH_ICON: path.join(root, 'apps/web/dist/apple-touch-icon.png'),
      },
    });
    check('native-checksums', 'sha256sum', ['-c', 'checksums.txt'], { cwd: nativeDir });
    for (const name of nativeForkAssetNames(options.tag)) {
      const windows = name.endsWith('.zip');
      const packageName = name.replace(/\.(?:tar\.gz|zip)$/, '');
      const binaryPath = `${packageName}/cpa-manager-plus${windows ? '.exe' : ''}`;
      const binary = execute(
        windows ? 'unzip' : 'tar',
        windows
          ? ['-p', path.join(nativeDir, name), binaryPath]
          : ['-xOzf', path.join(nativeDir, name), binaryPath],
        {
          stdio: ['ignore', 'pipe', 'pipe'],
          encoding: null,
          maxBuffer: 128 * 1024 * 1024,
        }
      );
      verifyBinaryEmbedding(binary, html, options.sourceCommit);
    }
    checks['native-embedding'] = 'passed';
    // Recheck the source after every build command, before sealing the receipt.
    if (git(['status', '--porcelain', '--untracked-files=all']))
      fail('Build changed tracked source or left untracked inputs');
    validateForkBuildInputs({ git });
    validateForkTagAbsent({ git, tag: options.tag });
    validateForkRemoteRefs({
      sourceCommit: options.sourceCommit,
      devCommit: options.devCommit,
      git,
    });
    validateForkTopology({
      tag: options.tag,
      sourceCommit: options.sourceCommit,
      devCommit: options.devCommit,
      git,
    });
    mkdirSync(payload);
    for (const name of [...nativeForkAssetNames(options.tag), 'checksums.txt'])
      copyFileSync(path.join(nativeDir, name), path.join(payload, name));
    copyFileSync(
      path.join(root, 'apps/web/dist/index.html'),
      path.join(payload, 'management.html')
    );
    writeFileSync(
      path.join(payload, 'release-info.json'),
      `${JSON.stringify(buildForkReleaseInfo({ tag: options.tag, sourceCommit: options.sourceCommit, metadata: content.metadata }), null, 2)}\n`
    );
    const assets = buildForkAssetRecords(payload, options.tag);
    const receipt = {
      schema: FORK_RECEIPT_SCHEMA,
      schema_version: 1,
      repository: FORK_REPOSITORY,
      workflow_path: FORK_WORKFLOW_PATH,
      mode: 'dry-run',
      tag: options.tag,
      source_commit: options.sourceCommit,
      source_tree: topology.sourceTree,
      base_source_commit: FORK_BASE_SOURCE_COMMIT,
      frozen_refs: { main: options.sourceCommit, dev: options.devCommit },
      run: { id: options.runId, attempt: options.runAttempt },
      content: content.digests,
      toolchain,
      checks,
      assets: assets.map(({ name, size, digest }) => ({ name, size, digest })),
    };
    if (Object.keys(checks).length !== REQUIRED_FORK_CHECKS.length)
      fail('Incomplete dry-run check set');
    validateForkReceipt(receipt, {
      tag: options.tag,
      sourceCommit: options.sourceCommit,
      sourceTree: topology.sourceTree,
      devCommit: options.devCommit,
      assets,
      contentDigests: content.digests,
    });
    writeFileSync(path.join(output, 'receipt.json'), `${JSON.stringify(receipt, null, 2)}\n`);
    return { payload, receiptFile: path.join(output, 'receipt.json'), receipt };
  } catch (error) {
    // This directory was created by this invocation and never existed beforehand.
    rmSync(output, { recursive: true, force: true });
    throw error;
  } finally {
    rmSync(temporary, { recursive: true, force: true });
  }
};

const parseArguments = (args) => {
  const allowed = new Set([
    'repository',
    'tag',
    'sha',
    'dev-sha',
    'output',
    'run-id',
    'run-attempt',
    'opt-in',
  ]);
  const values = {};
  for (let index = 0; index < args.length; index += 2) {
    const key = args[index]?.slice(2);
    if (
      !args[index]?.startsWith('--') ||
      !allowed.has(key) ||
      args[index + 1] === undefined ||
      values[key] !== undefined
    )
      fail('Expected unique approved --name value build arguments');
    values[key] = args[index + 1];
  }
  return {
    repository: values.repository,
    tag: values.tag,
    sourceCommit: values.sha,
    devCommit: values['dev-sha'],
    output: values.output,
    runId: values['run-id'],
    runAttempt: values['run-attempt'],
    optIn: values['opt-in'] === 'true',
  };
};

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const result = buildForkRelease(parseArguments(process.argv.slice(2)));
    console.log(`Fork dry-run sealed: ${result.receiptFile}`);
  } catch (error) {
    console.error(error instanceof Error ? error.message : String(error));
    process.exitCode = 1;
  }
}
