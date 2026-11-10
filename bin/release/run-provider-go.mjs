// SPEC5-D: unified Go entry point for the single-repo provider modules.
//
// providers/opencode-go and providers/qwen each keep their own go.mod, and
// providers/ itself is not a Go module, so a single `go test ./providers/...`
// pattern is not valid Go (the `providers` directory prefix is not in a module
// or workspace). This script is the supported unified entry point: it reads
// providers/import-boundary.json and runs `go <subcommand> ./...` inside every
// provider module that actually carries a go.mod.
//
// Usage:
//   node bin/release/run-provider-go.mjs test        # go test ./...
//   node bin/release/run-provider-go.mjs vet         # go vet ./...
//   node bin/release/run-provider-go.mjs build       # go build ./...
//   node bin/release/run-provider-go.mjs test -- -run TestFoo
// All forms require --qwen-canonical-source and --compatible-core-source (or the
// corresponding CPA_QWEN_CANONICAL_SOURCE / CPA_COMPATIBLE_CORE_SOURCE env vars).
//
// Qwen provenance must pass before ANY Go command; after that gate every module
// is attempted so a single run reports the full picture.
import { execFileSync } from 'node:child_process';
import { copyFileSync, existsSync, lstatSync, mkdirSync, mkdtempSync, readFileSync, realpathSync, rmSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

export const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
export const MANIFEST_PATH = 'providers/import-boundary.json';
export const ALLOWED_SUBCOMMANDS = ['test', 'vet', 'build'];

const defaultRun = (command, args, { cwd, env }) => {
  execFileSync(command, args, { cwd, env, stdio: 'inherit' });
};

const within = (parent, child) => {
  const relative = path.relative(parent, child);
  return relative === '' || (relative !== '..' && !relative.startsWith(`..${path.sep}`) && !path.isAbsolute(relative));
};

// lstat every existing component, including workspace ancestors and dangling
// links. Lexically external paths are not sufficient on Windows junctions.
const physicalWritePath = (target) => {
  const absolute = path.resolve(target);
  let nearest;
  for (let cursor = absolute; ; cursor = path.dirname(cursor)) {
    try {
      const entry = lstatSync(cursor);
      if (entry.isSymbolicLink() || (entry.isFile() && entry.nlink > 1)) {
        throw new Error(`provider_write_path_link: ${cursor}`);
      }
      nearest ??= cursor;
    } catch (error) {
      if (error.code !== 'ENOENT') throw error;
    }
    if (path.dirname(cursor) === cursor) break;
  }
  return path.resolve(realpathSync.native(nearest), path.relative(nearest, absolute));
};

const assertWritePaths = (paths, sources) => {
  const protectedSources = sources.filter(Boolean).flatMap((source) => {
    const absolute = path.resolve(source);
    return existsSync(absolute) ? [absolute, realpathSync.native(absolute)] : [absolute];
  });
  for (const target of paths) {
    const physical = physicalWritePath(target);
    if (protectedSources.some((source) => within(source, path.resolve(target)) || within(source, physical))) {
      throw new Error(`provider_write_path_source_overlap: ${target}`);
    }
  }
};

const artifactWritePaths = (root) => {
  const manifest = JSON.parse(readFileSync(path.join(root, MANIFEST_PATH), 'utf8'));
  return (manifest.buildInterface?.artifacts ?? []).flatMap((artifact) => {
    const outputIndex = artifact.args?.indexOf('-o') ?? -1;
    const declared = artifact.args?.[outputIndex + 1];
    const output = path.resolve(root, artifact.output);
    if (outputIndex < 0 || !declared || path.relative(output, path.resolve(root, artifact.workdir, declared)) !== '') {
      throw new Error(`provider_write_path_output_contract: ${artifact.id}`);
    }
    // c-shared also writes a header adjacent to its DLL, outside the Go -o arg.
    return artifact.kind === 'plugin-c-shared' ? [output, path.join(path.dirname(output), `${path.parse(output).name}.h`)] : [output];
  });
};

export const createProviderGoContext = ({
  root = repoRoot,
  compatibleCoreSource = process.env.CPA_COMPATIBLE_CORE_SOURCE,
  qwenCanonicalSource = process.env.CPA_QWEN_CANONICAL_SOURCE,
  goBin = 'go',
  run = defaultRun,
} = {}) => {
  if (!compatibleCoreSource) throw new Error('compatible_core_source_required: use --compatible-core-source or CPA_COMPATIBLE_CORE_SOURCE');
  const core = path.resolve(compatibleCoreSource);
  if (!existsSync(path.join(core, 'go.mod')) || !/^module\s+github\.com\/router-for-me\/CLIProxyAPI\/v8\s*$/m.test(readFileSync(path.join(core, 'go.mod'), 'utf8')) || !existsSync(path.join(core, 'sdk/pluginapi'))) {
    throw new Error('compatible_core_source_invalid');
  }
  const env = { ...process.env, GOWORK: 'off', GOENV: 'off', GOFLAGS: '', GOTOOLCHAIN: 'local' };
  delete env.GOROOT;
  const temporaryRoot = path.join(root, 'bin/tmp');
  const sources = [path.join(root, 'providers/qwen'), qwenCanonicalSource, core];
  const writes = [temporaryRoot, ...artifactWritePaths(root)];
  const assertBoundary = (extra = []) => assertWritePaths([...writes, ...extra], sources);
  // Validate ALL planned writes before creating a modfile or probing a compiler,
  // even when a DLL will later be skipped or the runner only tests/vets.
  assertBoundary();
  mkdirSync(temporaryRoot, { recursive: true });
  assertBoundary();
  const temporary = mkdtempSync(path.join(temporaryRoot, 'provider-modfiles-'));
  const prepared = new Map();
  return {
    env,
    assertBoundary,
    forModule(dir) {
      const source = path.join(root, dir);
      const modfile = path.join(temporary, `${path.basename(dir)}.mod`);
      assertBoundary([temporary, modfile, modfile.replace(/\.mod$/, '.sum')]);
      if (prepared.has(dir)) return prepared.get(dir);
      copyFileSync(path.join(source, 'go.mod'), modfile);
      if (existsSync(path.join(source, 'go.sum'))) copyFileSync(path.join(source, 'go.sum'), modfile.replace(/\.mod$/, '.sum'));
      run(goBin, ['mod', 'edit', `-modfile=${modfile}`, `-replace=github.com/router-for-me/CLIProxyAPI/v8=${core}`], { cwd: source, env });
      const flags = ['-mod=mod', `-modfile=${modfile}`];
      prepared.set(dir, flags);
      return flags;
    },
    cleanup() {
      assertWritePaths([temporary], sources);
      rmSync(temporary, { recursive: true, force: true });
    },
  };
};

// Execute the reviewed verifier from the pinned Git object, not a potentially
// edited working copy. This is read-only: never enroll or rewrite a receipt.
export const verifyQwenSnapshot = ({
  root = repoRoot,
  qwenCanonicalSource = process.env.CPA_QWEN_CANONICAL_SOURCE,
  pythonBin = process.env.CPA_QWEN_PYTHON || 'python',
} = {}) => {
  if (!qwenCanonicalSource) throw new Error('qwen_canonical_source_required: use --qwen-canonical-source or CPA_QWEN_CANONICAL_SOURCE');
  const manifest = JSON.parse(readFileSync(path.join(root, MANIFEST_PATH), 'utf8'));
  const contract = manifest.providers?.find((provider) => provider.id === 'qwen')?.consumerSnapshot;
  if (contract?.repository !== 'williamxhero/CPA-Qwen' || !/^[0-9a-f]{40}$/.test(contract.commit ?? '')) {
    throw new Error('qwen_snapshot_contract_invalid');
  }
  const canonical = path.resolve(qwenCanonicalSource);
  const git = (...args) => execFileSync('git', ['-C', canonical, ...args], { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] });
  if (git('rev-parse', 'HEAD').trim() !== contract.commit) throw new Error('qwen_canonical_commit_mismatch');
  const verifier = git('show', `${contract.commit}:scripts/verify-manager-snapshot.py`);
  try {
    const result = JSON.parse(execFileSync(pythonBin, [
      '-B', '-c', `__file__ = ${JSON.stringify(path.join(canonical, 'scripts/verify-manager-snapshot.py'))}\n${verifier}`, '--canonical-source', canonical,
      '--snapshot', path.join(root, 'providers/qwen'),
    ], { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }));
    if (result.status !== 'verified' || result.commit !== contract.commit || result.repository !== contract.repository) {
      throw new Error('invalid verifier result');
    }
    return result;
  } catch (error) {
    throw new Error(`qwen_snapshot_verification_failed: ${error.stderr?.toString().trim() || error.stdout?.toString().trim() || error.message}`);
  }
};

// Every manifest provider whose directory contains a go.mod, in manifest order.
export const loadProviderModules = (root = repoRoot) => {
  const manifest = JSON.parse(readFileSync(path.join(root, MANIFEST_PATH), 'utf8'));
  return (manifest.providers ?? [])
    .filter((provider) => provider.dir && provider.dir.startsWith('providers/'))
    .map((provider) => ({ id: provider.id, dir: provider.dir }))
    .filter(({ dir }) => existsSync(path.join(root, dir, 'go.mod')));
};

export const runProviderGo = ({
  root = repoRoot,
  subcommand = 'test',
  extraArgs = [],
  goBin = 'go',
  run = defaultRun,
  logger = console,
  qwenCanonicalSource,
  verifySnapshot = verifyQwenSnapshot,
  compatibleCoreSource,
} = {}) => {
  if (!ALLOWED_SUBCOMMANDS.includes(subcommand)) {
    throw new Error(
      `unsupported go subcommand '${subcommand}' (expected one of: ${ALLOWED_SUBCOMMANDS.join(', ')})`
    );
  }
  if (extraArgs.some((arg) => /^--?(?:modfile|mod|overlay|C|o)(?:=|$)/.test(arg))) {
    throw new Error('provider_go_source_override_forbidden');
  }

  verifySnapshot({ root, qwenCanonicalSource });
  const modules = loadProviderModules(root);
  const results = [];
  const context = createProviderGoContext({ root, compatibleCoreSource, qwenCanonicalSource, goBin, run });
  try {
    for (const module of modules) {
      try {
        const flags = context.forModule(module.dir);
        const args = [subcommand, ...flags, ...extraArgs, './...'];
        logger.log(`\n[providers] ${module.id}: ${[goBin, ...args].join(' ')}`);
        context.assertBoundary();
        run(goBin, args, { cwd: path.join(root, module.dir), env: context.env });
        results.push({ ...module, ok: true });
      } catch {
        results.push({ ...module, ok: false });
      }
    }
  } finally {
    context.cleanup();
  }

  return results;
};

export const formatProviderGoSummary = (results, subcommand) => {
  const lines = [`[providers] go ${subcommand} summary:`];
  if (results.length === 0) lines.push('  (no provider go modules found)');
  for (const result of results) {
    lines.push(`  ${result.ok ? 'ok  ' : 'FAIL'} ${result.id} (${result.dir})`);
  }
  return lines.join('\n');
};

const runCli = () => {
  const argv = process.argv.slice(2);
  const separatorIndex = argv.indexOf('--');
  const head = separatorIndex === -1 ? argv : argv.slice(0, separatorIndex);
  const extraArgs = separatorIndex === -1 ? [] : argv.slice(separatorIndex + 1);
  const [subcommand = 'test', ...options] = head;
  let qwenCanonicalSource;
  let compatibleCoreSource;
  const unknown = [];
  for (let i = 0; i < options.length; i += 1) {
    if (options[i] === '--qwen-canonical-source' && options[i + 1] && !options[i + 1].startsWith('--')) {
      qwenCanonicalSource = options[++i];
    } else if (options[i] === '--compatible-core-source' && options[i + 1] && !options[i + 1].startsWith('--')) {
      compatibleCoreSource = options[++i];
    } else unknown.push(options[i]);
  }

  if (unknown.length > 0) {
    console.error(`Unknown arguments: ${unknown.join(', ')}`);
    process.exitCode = 1;
    return;
  }

  let results;
  try {
    results = runProviderGo({ subcommand, extraArgs, qwenCanonicalSource, compatibleCoreSource });
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
    return;
  }

  console.log(`\n${formatProviderGoSummary(results, subcommand)}`);
  if (results.length === 0 || results.some((result) => !result.ok)) {
    process.exitCode = 1;
  }
};

const entryPoint = process.argv[1] ? pathToFileURL(path.resolve(process.argv[1])).href : '';
if (entryPoint === import.meta.url) runCli();
