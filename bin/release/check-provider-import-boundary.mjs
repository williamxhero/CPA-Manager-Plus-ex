// SPEC5-A: enforce the CPAMP single-repo providers layout, licence and import boundary.
//
// Contract source of truth: providers/import-boundary.json
// Human-readable contract:    docs/providers-layout-contract.md
//
// This script only inspects the repository. It never reads the user's real
// credentials, never touches the network, and never mutates tracked files.
// It is imported by tests/providerImportBoundary.test.mjs and can also be run
// directly:
//   node bin/release/check-provider-import-boundary.mjs [--json]
import { execFileSync } from 'node:child_process';
import { existsSync, readFileSync, readdirSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

export const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
export const MANIFEST_PATH = 'providers/import-boundary.json';
export const PROVIDERS_ROOT = 'providers';
const TEXT_SCAN_MAX_BYTES = 2 * 1024 * 1024;
// SPEC5-D: provider trees must stay source-only. Anything larger than this cap
// is almost certainly a committed build artifact, vendored blob or stray export.
export const DEFAULT_PROVIDER_MAX_FILE_BYTES = 1024 * 1024;
const IGNORED_DIRECTORIES = new Set(['.git', 'node_modules']);

// Minimal fallback used when a caller does not supply manifest patterns.
// The manifest (globalForbidden.contentPatterns) is the authoritative set.
export const FALLBACK_SECRET_PATTERNS = [
  { id: 'private-key-block', pattern: '-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----', flags: '' },
  { id: 'openai-key', pattern: '\\bsk-[A-Za-z0-9_-]{20,}\\b', flags: '' },
];

export const escapeRegExp = (value) => value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

// Glob -> RegExp supporting `**`, `*` and `?` against POSIX-style relative paths.
export const globToRegExp = (glob) => {
  const pattern = String(glob).replace(/\\/g, '/');
  let source = '^';

  for (let index = 0; index < pattern.length; index += 1) {
    const char = pattern[index];
    if (char === '*') {
      if (pattern[index + 1] === '*') {
        index += 1;
        if (pattern[index + 1] === '/') {
          index += 1;
          source += '(?:.*/)?';
        } else {
          source += '.*';
        }
      } else {
        source += '[^/]*';
      }
    } else if (char === '?') {
      source += '[^/]';
    } else {
      source += escapeRegExp(char);
    }
  }

  return new RegExp(`${source}$`);
};

export const matchesGlob = (relativePath, glob) =>
  globToRegExp(glob).test(String(relativePath).replace(/\\/g, '/'));

export const isBinaryExtension = (relativePath, extensions = []) =>
  extensions.includes(path.extname(String(relativePath)).toLowerCase());

export const redact = (matched) => `${String(matched).slice(0, 4)}***`;

export const findSecretFindings = (
  relativePath,
  text,
  { patterns = FALLBACK_SECRET_PATTERNS, allowlist = [], displayPath = relativePath } = {}
) => {
  const findings = [];

  for (const { id, pattern, flags = '' } of patterns) {
    const suppressed = allowlist.some(
      (entry) => entry.patternId === id && matchesGlob(relativePath, entry.glob)
    );
    if (suppressed) continue;

    const regex = new RegExp(pattern, flags.includes('g') ? flags : `${flags}g`);
    for (const match of text.matchAll(regex)) {
      const line = text.slice(0, match.index).split('\n').length;
      findings.push(
        `${displayPath}:${line} matches forbidden secret pattern '${id}' (${redact(match[0])})`
      );
    }
  }

  return findings;
};

export const listFiles = (root, relativeDir = '.') => {
  const results = [];
  const start = path.join(root, relativeDir);
  if (!existsSync(start)) return results;
  const prefix = relativeDir === '.' ? '' : `${relativeDir.replace(/\\/g, '/')}/`;

  // SPEC5: when the tree is a git worktree, only consider files git would
  // track (tracked + untracked-but-not-ignored). Local build artifacts under
  // providers/*/bin are gitignored and must not fail the boundary contract.
  try {
    const output = execFileSync(
      'git',
      ['ls-files', '-z', '--cached', '--others', '--exclude-standard', '--', relativeDir],
      { cwd: root, encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'] }
    );
    return output
      .split('\0')
      .filter(Boolean)
      .map((entry) => entry.replace(/\\/g, '/'))
      .filter((entry) => !prefix || entry.startsWith(prefix))
      .map((entry) => entry.slice(prefix.length))
      .sort();
  } catch {
    // Not a git worktree (e.g. a temp fixture): fall back to a filesystem walk.
  }

  const visit = (directory) => {
    for (const entry of readdirSync(directory, { withFileTypes: true })) {
      if (entry.isDirectory()) {
        if (IGNORED_DIRECTORIES.has(entry.name)) continue;
        visit(path.join(directory, entry.name));
      } else if (entry.isFile()) {
        results.push(path.relative(start, path.join(directory, entry.name)).replace(/\\/g, '/'));
      }
    }
  };

  visit(start);
  return results.sort();
};

export const listTrackedFiles = (root, relativeDir) => {
  try {
    const output = execFileSync('git', ['ls-files', '-z', '--', relativeDir], {
      cwd: root,
      encoding: 'utf8',
      stdio: ['ignore', 'pipe', 'ignore'],
    });
    return output
      .split('\0')
      .filter(Boolean)
      .map((entry) => entry.replace(/\\/g, '/'));
  } catch {
    return [];
  }
};

export const loadManifest = (root = repoRoot) =>
  JSON.parse(readFileSync(path.join(root, MANIFEST_PATH), 'utf8'));

export const validateManifest = (manifest) => {
  const findings = [];
  if (!manifest || typeof manifest !== 'object') return ['manifest is not an object'];

  for (const key of ['contractDoc', 'boundaryDoc', 'attributionFile']) {
    if (!manifest[key]) findings.push(`manifest.${key} is required`);
  }

  if (!Array.isArray(manifest.providers) || manifest.providers.length === 0) {
    findings.push('manifest.providers must be a non-empty array');
  }

  const ids = new Set();
  const dirs = new Set();
  for (const provider of manifest.providers ?? []) {
    const label = provider.id ?? '(missing id)';
    if (!provider.id) findings.push('a provider entry is missing an id');
    if (ids.has(provider.id)) findings.push(`duplicate provider id: ${provider.id}`);
    ids.add(provider.id);

    if (!provider.dir || !String(provider.dir).startsWith('providers/')) {
      findings.push(`provider ${label}: dir must live under providers/`);
    }
    if (dirs.has(provider.dir)) findings.push(`duplicate provider dir: ${provider.dir}`);
    dirs.add(provider.dir);

    if (!['planned', 'imported'].includes(provider.status)) {
      findings.push(`provider ${label}: status must be planned|imported`);
    }
    if (!provider.sourceRepo) findings.push(`provider ${label}: sourceRepo is required`);
    if (!provider.license?.spdx || !provider.license?.copyright) {
      findings.push(`provider ${label}: license.spdx and license.copyright are required`);
    }
    if (!Array.isArray(provider.requiredFiles) || provider.requiredFiles.length === 0) {
      findings.push(`provider ${label}: requiredFiles must be a non-empty array`);
    }
  }

  if (!manifest.buildInterface || manifest.buildInterface.status !== 'contract-only') {
    findings.push('manifest.buildInterface.status must be "contract-only" for SPEC5-A');
  }

  return findings;
};

export const checkAttribution = (root, manifest) => {
  const findings = [];
  const attributionPath = path.join(root, manifest.attributionFile);
  if (!existsSync(attributionPath)) {
    return [`missing attribution file: ${manifest.attributionFile}`];
  }

  const text = readFileSync(attributionPath, 'utf8');
  for (const provider of manifest.providers ?? []) {
    const tokens = [
      provider.sourceRepo,
      provider.license?.spdx,
      provider.license?.copyright,
    ].filter(Boolean);

    for (const token of tokens) {
      if (!text.includes(token)) {
        findings.push(
          `${manifest.attributionFile} must reference '${token}' (provider ${provider.id})`
        );
      }
    }
  }

  return findings;
};

export const scanProvider = (root, provider, manifest) => {
  const findings = [];
  const binaryExtensions = manifest.globalForbidden?.binaryExtensions ?? [];
  const pathGlobs = [
    ...(manifest.globalForbidden?.pathGlobs ?? []),
    ...(provider.excludeGlobs ?? []),
  ];
  const patterns = manifest.globalForbidden?.contentPatterns ?? FALLBACK_SECRET_PATTERNS;
  const maxFileBytes = manifest.globalForbidden?.maxFileBytes ?? DEFAULT_PROVIDER_MAX_FILE_BYTES;
  const allowlist = provider.contentAllowlist ?? [];
  const dir = path.join(root, provider.dir);

  if (!existsSync(dir)) {
    if (provider.status === 'imported') {
      findings.push(`provider ${provider.id} is marked imported but ${provider.dir} is missing`);
    }
    return { findings, imported: false };
  }

  for (const relativePath of listFiles(root, provider.dir)) {
    if (isBinaryExtension(relativePath, binaryExtensions)) {
      findings.push(`${provider.dir}/${relativePath} has a forbidden binary extension`);
      continue;
    }

    const matchedGlob = pathGlobs.find((glob) => matchesGlob(relativePath, glob));
    if (matchedGlob) {
      findings.push(`${provider.dir}/${relativePath} matches excluded glob '${matchedGlob}'`);
      continue;
    }

    let buffer;
    try {
      buffer = readFileSync(path.join(dir, relativePath));
    } catch {
      continue;
    }

    if (buffer.length > maxFileBytes) {
      findings.push(
        `${provider.dir}/${relativePath} is ${buffer.length} bytes, over the ${maxFileBytes}-byte provider file cap (large/committed artifacts must not be imported)`
      );
      continue;
    }

    if (buffer.includes(0) || buffer.length > TEXT_SCAN_MAX_BYTES) continue;

    findings.push(
      ...findSecretFindings(relativePath, buffer.toString('utf8'), {
        patterns,
        allowlist,
        displayPath: `${provider.dir}/${relativePath}`,
      })
    );
  }

  for (const required of provider.requiredFiles ?? []) {
    if (!existsSync(path.join(dir, required))) {
      findings.push(`provider ${provider.id} is missing required file: ${provider.dir}/${required}`);
    }
  }

  return { findings, imported: true };
};

export const runBoundaryCheck = ({ root = repoRoot, manifest } = {}) => {
  const findings = [];
  const info = [];
  const resolved = manifest ?? loadManifest(root);

  for (const relativePath of [
    resolved.contractDoc,
    resolved.boundaryDoc,
    resolved.attributionFile,
  ]) {
    if (!existsSync(path.join(root, relativePath))) {
      findings.push(`missing contract file: ${relativePath}`);
    }
  }

  findings.push(...validateManifest(resolved));
  findings.push(...checkAttribution(root, resolved));

  for (const provider of resolved.providers ?? []) {
    const { findings: providerFindings, imported } = scanProvider(root, provider, resolved);
    findings.push(...providerFindings);
    info.push(
      imported
        ? `provider ${provider.id} present (status=${provider.status})`
        : `provider ${provider.id} not imported yet (status=${provider.status})`
    );
  }

  const binaryExtensions = resolved.globalForbidden?.binaryExtensions ?? [];
  for (const tracked of listTrackedFiles(root, PROVIDERS_ROOT)) {
    const providerRelative = tracked.startsWith(`${PROVIDERS_ROOT}/`)
      ? tracked.slice(PROVIDERS_ROOT.length + 1)
      : tracked;
    if (isBinaryExtension(providerRelative, binaryExtensions)) {
      findings.push(`tracked provider binary in git index: ${tracked}`);
    }
  }

  return { ok: findings.length === 0, findings, info };
};

const runCli = () => {
  const args = process.argv.slice(2);
  const unknown = args.filter((argument) => argument !== '--json');
  if (unknown.length > 0) {
    console.error(`Unknown arguments: ${unknown.join(', ')}`);
    process.exitCode = 2;
    return;
  }

  const { ok, findings, info } = runBoundaryCheck({ root: repoRoot });

  if (args.includes('--json')) {
    console.log(JSON.stringify({ ok, findings, info }, null, 2));
    process.exitCode = ok ? 0 : 1;
    return;
  }

  for (const line of info) console.log(`info: ${line}`);
  if (ok) {
    console.log('providers import boundary contract OK');
    return;
  }

  console.error('providers import boundary violations:');
  for (const finding of findings) console.error(`- ${finding}`);
  process.exitCode = 1;
};

const entryPoint = process.argv[1] ? pathToFileURL(path.resolve(process.argv[1])).href : '';
if (entryPoint === import.meta.url) runCli();
