import { execFileSync } from 'node:child_process';
import { appendFileSync, lstatSync, readFileSync, readdirSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  FORK_REPOSITORY,
  FORK_WORKFLOW_PATH,
  assertForkRepository,
  buildForkAssetRecords,
  forkArtifactName,
  forkContentPaths,
  parseForkTag,
  sha256,
  validateForkReceipt,
  validateForkReleaseInfo,
} from './fork-release-contract.mjs';
import { verifyDraftRelease, verifyPublishedRelease } from './verify-published-release.mjs';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const fullSha = /^[0-9a-f]{40}$/;
const digestSha = /^[0-9a-f]{64}$/;
const positiveInteger = /^[1-9][0-9]*$/;
const fail = (message) => {
  throw new Error(message);
};
const hash = sha256;
const tagPath = (tag) => `/git/ref/tags/${encodeURIComponent(tag)}`;
const releasePath = (tag) => `/releases/tags/${encodeURIComponent(tag)}`;

const approvedOptions = (options) => {
  assertForkRepository(options.repository);
  parseForkTag(options.tag);
  if (options.optIn !== true || options.confirmPublish !== true) {
    fail('Gate 2 requires optIn === true and confirmPublish === true');
  }
  if (!fullSha.test(options.sourceCommit || '') || !fullSha.test(options.devCommit || '')) {
    fail('Gate 2 requires exact source and frozen dev commit SHAs');
  }
  if (
    !positiveInteger.test(String(options.dryRunId || '')) ||
    !positiveInteger.test(String(options.dryRunAttempt || ''))
  ) {
    fail('Gate 2 requires the exact approved dry-run ID and attempt');
  }
  if (!digestSha.test(options.receiptSha || '')) {
    fail('Gate 2 requires the approved receipt SHA-256');
  }
  return options;
};

const verifyRef = (ref, branch, sha) => {
  if (
    ref?.ref !== `refs/heads/${branch}` ||
    ref?.object?.type !== 'commit' ||
    ref?.object?.sha !== sha
  )
    fail(`Live fork ${branch} SHA drift`);
};

const verifyFrozenRefs = async ({ sourceCommit, devCommit }, api) => {
  verifyRef(await api('/git/ref/heads/main'), 'main', sourceCommit);
  verifyRef(await api('/git/ref/heads/dev'), 'dev', devCommit);
};

const verifyTag = (ref, { tag, sourceCommit }) => {
  if (
    ref?.ref !== `refs/tags/${tag}` ||
    ref?.object?.type !== 'commit' ||
    ref?.object?.sha !== sourceCommit
  )
    fail('Fork tag readback does not match the approved commit');
};

const inspectDryRun = async (options, api) => {
  const { repository, tag, sourceCommit, devCommit, dryRunId, dryRunAttempt } = options;
  const run = await api(`/actions/runs/${dryRunId}`);
  const workflowPath =
    typeof run?.path === 'string' && /^[^@]+(?:@[^@\r\n]+)?$/.test(run.path)
      ? run.path.split('@')[0]
      : '';
  if (
    String(run?.id) !== String(dryRunId) ||
    String(run?.run_attempt) !== String(dryRunAttempt) ||
    run?.repository?.full_name !== repository ||
    run?.head_repository?.full_name !== repository ||
    run?.head_sha !== sourceCommit ||
    run?.head_branch !== 'main' ||
    run?.event !== 'workflow_dispatch' ||
    run?.status !== 'completed' ||
    run?.conclusion !== 'success' ||
    workflowPath !== FORK_WORKFLOW_PATH ||
    run?.display_title !== `Fork native/web dry-run ${tag} @ ${sourceCommit}`
  ) {
    fail('Run is not the exact approved successful fork main dry-run and attempt');
  }
  await verifyFrozenRefs(options, api);
  const commit = await api(`/git/commits/${sourceCommit}`);
  if (
    commit?.sha !== sourceCommit ||
    !fullSha.test(commit?.tree?.sha || '') ||
    !Array.isArray(commit.parents) ||
    commit.parents.length !== 2 ||
    commit.parents[1]?.sha !== devCommit
  ) {
    fail('Git commit API did not return the approved main promotion commit, parents and tree');
  }
  const dev = await api(`/git/commits/${devCommit}`);
  if (dev?.sha !== devCommit || dev?.tree?.sha !== commit.tree.sha) {
    fail('Frozen dev and main promotion trees differ');
  }
  const expectedName = forkArtifactName(tag, sourceCommit, dryRunAttempt);
  const record = await api(`/actions/runs/${dryRunId}/artifacts?per_page=100`);
  if (
    record?.total_count !== 1 ||
    !Array.isArray(record?.artifacts) ||
    record.artifacts.length !== 1
  ) {
    fail('Dry-run must have exactly one immutable payload artifact; missing or extra artifacts');
  }
  const artifact = record.artifacts[0];
  if (
    artifact.name !== expectedName ||
    artifact.expired !== false ||
    !positiveInteger.test(String(artifact.id || '')) ||
    (artifact.expires_at !== undefined &&
      (!Number.isFinite(Date.parse(artifact.expires_at)) ||
        Date.parse(artifact.expires_at) <= Date.now())) ||
    (artifact.workflow_run !== undefined &&
      (String(artifact.workflow_run?.id) !== String(dryRunId) ||
        artifact.workflow_run?.head_sha !== sourceCommit))
  ) {
    fail('Dry-run payload artifact identity or expiration mismatch');
  }
  return {
    artifactId: String(artifact.id),
    artifactName: expectedName,
    sourceTree: commit.tree.sha,
  };
};

const assertNoExistingPublication = async ({ tag }, api) => {
  if (await api(tagPath(tag), { allow404: true })) {
    fail('Fork tag already exists; even an exact match requires separately approved recovery');
  }
  if (await api(releasePath(tag), { allow404: true })) {
    fail('Fork release already exists; recovery is not authorized');
  }
  // The tag endpoint alone cannot prove that no draft with this tag exists.
  for (let page = 1; page <= 100; page += 1) {
    const releases = await api(`/releases?per_page=100&page=${page}`);
    if (!Array.isArray(releases)) fail('Invalid GitHub release list');
    if (releases.some((release) => release.tag_name === tag)) {
      fail('Fork draft or release already exists; recovery is not authorized');
    }
    if (releases.length < 100) return;
  }
  fail('Cannot prove absence of an existing fork release');
};

/** Read-only Gate 2 preflight. api(path, {method, body, allow404}) returns JSON;
 * allow404 must return null only for an actual HTTP 404, never for other errors. */
export const authorizeForkDryRun = async (options, api) => {
  approvedOptions(options);
  if (typeof api !== 'function') fail('A GitHub API client is required');
  const result = await inspectDryRun(options, api);
  await assertNoExistingPublication(options, api);
  return result;
};

const checkedContentPaths = (tag) => Object.values(forkContentPaths(tag));
const gitRead = (root, args, encoding) =>
  execFileSync('git', args, {
    cwd: root,
    encoding,
    stdio: ['ignore', 'pipe', 'pipe'],
  });

const readPayload = (options, dependencies, authorized) => {
  const {
    assetsDir,
    receiptFile,
    tag,
    sourceCommit,
    devCommit,
    dryRunId,
    dryRunAttempt,
    receiptSha,
  } = options;
  if (
    !assetsDir ||
    !receiptFile ||
    path.basename(path.resolve(assetsDir)) !== 'payload' ||
    path.resolve(receiptFile) !== path.join(path.dirname(path.resolve(assetsDir)), 'receipt.json')
  ) {
    fail('Expected flat payload directory and sibling receipt.json');
  }
  const entries = readdirSync(path.dirname(path.resolve(assetsDir))).sort();
  if (
    entries.length !== 2 ||
    entries[0] !== 'payload' ||
    entries[1] !== 'receipt.json' ||
    !lstatSync(assetsDir).isDirectory() ||
    !lstatSync(receiptFile).isFile()
  ) {
    fail('Downloaded artifact must contain only a regular receipt.json and payload directory');
  }
  const root = dependencies.sourceRoot || repoRoot;
  const read = dependencies.readFile || readFileSync;
  const getHead =
    dependencies.getSourceHead || (() => gitRead(root, ['rev-parse', 'HEAD'], 'utf8').trim());
  const getTree =
    dependencies.getSourceTree ||
    (() => gitRead(root, ['rev-parse', `${sourceCommit}^{tree}`], 'utf8').trim());
  const getStatus =
    dependencies.getSourceStatus ||
    (() => gitRead(root, ['status', '--porcelain', '--untracked-files=all'], 'utf8').trim());
  if (getStatus() !== '') fail('Fork publication requires a clean checked source checkout');
  const getContent =
    dependencies.getSourceContent || ((file) => gitRead(root, ['show', `${sourceCommit}:${file}`]));
  if (getHead() !== sourceCommit || getTree() !== authorized.sourceTree) {
    fail('Checked source HEAD or tree does not match the approved dry-run');
  }
  const receiptBytes = read(receiptFile);
  if (hash(receiptBytes) !== receiptSha) fail('Approved receipt SHA-256 mismatch');
  const receipt = JSON.parse(receiptBytes.toString('utf8'));
  const assets = buildForkAssetRecords(assetsDir, tag);
  const content = checkedContentPaths(tag).map((file) => ({ path: file, bytes: getContent(file) }));
  const contentDigests = content.map(({ path: file, bytes }) => ({
    path: file,
    sha256: hash(bytes),
  }));
  validateForkReceipt(receipt, {
    tag,
    sourceCommit,
    runId: String(dryRunId),
    runAttempt: String(dryRunAttempt),
    assets,
    contentDigests,
    devCommit,
    sourceTree: authorized.sourceTree,
  });
  // Freeze verified bytes in memory: uploads must not read mutable files after authorization.
  const frozenAssets = assets.map((asset) => {
    const bytes = read(asset.filePath);
    if (bytes.length !== asset.size || `sha256:${hash(bytes)}` !== asset.digest) {
      fail('Payload changed while preparing immutable uploads');
    }
    return { ...asset, bytes };
  });
  validateForkReleaseInfo(
    JSON.parse(
      frozenAssets.find(({ name }) => name === 'release-info.json').bytes.toString('utf8')
    ),
    tag,
    sourceCommit
  );
  return { receipt, assets: frozenAssets, body: content[0].bytes.toString('utf8') };
};

const verifyAuthorizationUnchanged = (before, after) => {
  if (
    before.artifactId !== after.artifactId ||
    before.artifactName !== after.artifactName ||
    before.sourceTree !== after.sourceTree
  )
    fail('Dry-run artifact or source tree changed');
};

export const forkAssetUploadUrl = (uploadUrl, releaseId, name) => {
  if (!positiveInteger.test(String(releaseId || ''))) fail('Invalid fork release ID');
  const expected = `https://uploads.github.com/repos/${FORK_REPOSITORY}/releases/${releaseId}/assets`;
  if (uploadUrl !== `${expected}{?name,label}` && uploadUrl !== expected) {
    fail('Unsafe fork asset upload URL');
  }
  return `${expected}?name=${encodeURIComponent(name)}`;
};

const verifyReleaseTarget = (release, id, options) => {
  if (
    String(release?.id) !== String(id) ||
    release?.target_commitish !== options.sourceCommit ||
    (release.make_latest !== undefined &&
      release.make_latest !== 'false' &&
      release.make_latest !== false)
  ) {
    fail('Fork release ID, target commit or latest policy mismatch');
  }
};

/** Publish only the approved prebuilt payload. Source readers are synchronous;
 * upload(url, bytes, asset) must return GitHub upload metadata without redirects.
 * Failures deliberately leave partial state for separately approved recovery. */
export const publishForkRelease = async (options, dependencies = {}) => {
  const api = dependencies.api || createForkGitHubApi({ repository: options.repository });
  const upload = dependencies.upload || api.upload;
  const authorized = await authorizeForkDryRun(options, api);
  const payload = readPayload(options, dependencies, authorized);
  if (typeof upload !== 'function') fail('A guarded GitHub asset uploader is required');
  // Re-read server identity and both frozen refs immediately before the first mutation.
  verifyAuthorizationUnchanged(authorized, await authorizeForkDryRun(options, api));
  await verifyFrozenRefs(options, api);
  const createdTag = await api('/git/refs', {
    method: 'POST',
    body: { ref: `refs/tags/${options.tag}`, sha: options.sourceCommit },
  });
  verifyTag(createdTag, options);
  verifyTag(await api(tagPath(options.tag)), options);
  const draft = await api('/releases', {
    method: 'POST',
    body: {
      tag_name: options.tag,
      target_commitish: options.sourceCommit,
      name: options.tag,
      body: payload.body,
      draft: true,
      prerelease: true,
      make_latest: 'false',
    },
  });
  if (!positiveInteger.test(String(draft?.id || ''))) fail('Invalid fork draft release ID');
  verifyReleaseTarget(draft, draft.id, options);
  verifyDraftRelease({
    release: draft,
    tag: options.tag,
    prerelease: true,
    body: payload.body,
    assets: [],
  });
  for (const asset of payload.assets) {
    const url = forkAssetUploadUrl(draft.upload_url, draft.id, asset.name);
    const uploaded = await upload(url, asset.bytes, asset);
    if (
      uploaded?.name !== asset.name ||
      uploaded?.state !== 'uploaded' ||
      uploaded?.size !== asset.size ||
      uploaded?.digest !== asset.digest
    ) {
      fail(`Fork uploaded asset metadata mismatch: ${asset.name}`);
    }
  }
  const release = await api(`/releases/${draft.id}`);
  verifyReleaseTarget(release, draft.id, options);
  verifyDraftRelease({
    release,
    tag: options.tag,
    prerelease: true,
    body: payload.body,
    assets: payload.assets,
  });
  // No rebuilding or repair. Any input/server drift leaves the draft unpublished.
  const finalAuthorization = await inspectDryRun(options, api);
  verifyAuthorizationUnchanged(authorized, finalAuthorization);
  readPayload(options, dependencies, finalAuthorization);
  verifyTag(await api(tagPath(options.tag)), options);
  const finalDraft = await api(`/releases/${draft.id}`);
  verifyReleaseTarget(finalDraft, draft.id, options);
  verifyDraftRelease({
    release: finalDraft,
    tag: options.tag,
    prerelease: true,
    body: payload.body,
    assets: payload.assets,
  });
  await verifyFrozenRefs(options, api);
  const published = await api(`/releases/${draft.id}`, {
    method: 'PATCH',
    body: { draft: false, prerelease: true, make_latest: 'false' },
  });
  verifyReleaseTarget(published, draft.id, options);
  verifyPublishedRelease({
    release: published,
    tag: options.tag,
    prerelease: true,
    body: payload.body,
    assets: payload.assets,
  });
  const readback = await api(`/releases/${draft.id}`);
  verifyReleaseTarget(readback, draft.id, options);
  verifyPublishedRelease({
    release: readback,
    tag: options.tag,
    prerelease: true,
    body: payload.body,
    assets: payload.assets,
  });
  verifyTag(await api(tagPath(options.tag)), options);
  return {
    ...authorized,
    releaseId: String(draft.id),
    tag: options.tag,
    sourceCommit: options.sourceCommit,
    prerelease: true,
    makeLatest: false,
    publishedAssets: payload.assets.length,
  };
};

/** Token is read only from the environment, never a CLI option or returned value. */
export const createForkGitHubApi = ({ repository, fetchImpl = fetch, env = process.env }) => {
  assertForkRepository(repository);
  const token = env.GITHUB_TOKEN;
  if (!token) fail('Missing GITHUB_TOKEN');
  const request = async (url, options) => {
    const response = await fetchImpl(url, {
      ...options,
      redirect: 'error',
      headers: {
        Accept: 'application/vnd.github+json',
        Authorization: `Bearer ${token}`,
        'X-GitHub-Api-Version': '2022-11-28',
        ...options.headers,
      },
      signal: AbortSignal.timeout(60_000),
    });
    if (options.allow404 && response.status === 404) return null;
    if (!response.ok)
      fail(`GitHub ${options.method || 'GET'} request failed: HTTP ${response.status}`);
    return response.json();
  };
  const api = (apiPath, options = {}) => {
    if (
      typeof apiPath !== 'string' ||
      !apiPath.startsWith('/') ||
      apiPath.startsWith('//') ||
      apiPath.includes('\\') ||
      apiPath.includes('..')
    )
      fail('Unsafe GitHub API path');
    return request(`https://api.github.com/repos/${repository}${apiPath}`, {
      method: options.method || 'GET',
      allow404: options.allow404 === true,
      headers: { 'Content-Type': 'application/json' },
      body: options.body === undefined ? undefined : JSON.stringify(options.body),
    });
  };
  api.upload = (url, bytes, asset) => {
    const match =
      /^https:\/\/uploads\.github\.com\/repos\/williamxhero\/CPA-Manager-Plus\/releases\/([1-9][0-9]*)\/assets\?name=([^&]+)$/.exec(
        url
      );
    if (
      !match ||
      decodeURIComponent(match[2]) !== asset.name ||
      forkAssetUploadUrl(url.split('?')[0], match[1], asset.name) !== url
    ) {
      fail('Unsafe fork asset upload URL');
    }
    return request(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/octet-stream' },
      body: bytes,
    });
  };
  return api;
};

const parseArguments = (args) => {
  const names = {
    repository: 'repository',
    tag: 'tag',
    sha: 'sourceCommit',
    'dev-sha': 'devCommit',
    'dry-run-id': 'dryRunId',
    'dry-run-attempt': 'dryRunAttempt',
    'receipt-sha': 'receiptSha',
    'assets-dir': 'assetsDir',
    'receipt-file': 'receiptFile',
    'opt-in': 'optIn',
    'confirm-publish': 'confirmPublish',
    'preflight-only': 'preflightOnly',
  };
  const options = {};
  for (let i = 0; i < args.length; i += 2) {
    const name = args[i]?.slice(2);
    if (
      !args[i]?.startsWith('--') ||
      !Object.hasOwn(names, name) ||
      args[i + 1] === undefined ||
      Object.hasOwn(options, names[name])
    )
      fail('Unknown, missing or duplicate fork publish argument');
    let value = args[i + 1];
    if (['opt-in', 'confirm-publish', 'preflight-only'].includes(name)) {
      if (!['true', 'false'].includes(value)) fail('Confirmation arguments must be true or false');
      value = value === 'true';
    }
    options[names[name]] = value;
  }
  return options;
};

export const runForkPublishCli = async (args = process.argv.slice(2), env = process.env) => {
  const options = approvedOptions(parseArguments(args));
  if (
    (env.GITHUB_REPOSITORY && env.GITHUB_REPOSITORY !== FORK_REPOSITORY) ||
    (env.GITHUB_WORKFLOW_REF &&
      env.GITHUB_WORKFLOW_REF !== `${FORK_REPOSITORY}/${FORK_WORKFLOW_PATH}@refs/heads/main`) ||
    (env.GITHUB_REF && env.GITHUB_REF !== 'refs/heads/main') ||
    (env.GITHUB_SHA && env.GITHUB_SHA !== options.sourceCommit) ||
    (env.GITHUB_EVENT_NAME && env.GITHUB_EVENT_NAME !== 'workflow_dispatch')
  ) {
    fail('Fork publisher must run from the approved fork workflow on main');
  }
  const api = createForkGitHubApi({ repository: options.repository, env });
  const result =
    options.preflightOnly === true
      ? await authorizeForkDryRun(options, api)
      : await publishForkRelease(options, { api });
  if (options.preflightOnly === true && env.GITHUB_OUTPUT) {
    appendFileSync(
      env.GITHUB_OUTPUT,
      `artifact_id=${result.artifactId}\nartifact_name=${result.artifactName}\n`
    );
  }
  return result;
};

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    console.log(JSON.stringify({ ok: true, ...(await runForkPublishCli()) }));
  } catch (error) {
    const message = error instanceof Error ? error.message : 'Fork publication failed';
    console.error(
      process.env.GITHUB_TOKEN
        ? message.replaceAll(process.env.GITHUB_TOKEN, '[redacted]')
        : message
    );
    process.exitCode = 1;
  }
}
