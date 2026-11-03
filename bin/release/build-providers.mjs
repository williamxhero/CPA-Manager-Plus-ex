// SPEC5-D: build the provider artifacts declared in providers/import-boundary.json.
//
// Provider CLI artifacts always build. c-shared DLL artifacts need a Windows
// C toolchain (cgo): on Windows a mingw-w64 `gcc` on PATH, cross-compiling
// from Linux/macOS a `x86_64-w64-mingw32-gcc`. CI runners ship neither, so the
// DLL builds are SKIPPED there with the gated local build command printed (CI
// never force-compiles the DLLs). A skipped DLL build is not a failure; a
// build that was attempted and failed is.
//
// Usage:
//   node bin/release/build-providers.mjs            # CLI always, DLL when toolchain present
//   node bin/release/build-providers.mjs --skip-dll # never attempt DLL builds
//   node bin/release/build-providers.mjs --json     # machine-readable summary
// All forms require explicit canonical Qwen and compatible Core source paths;
// see providers/README.md. Outputs must not contaminate the Qwen snapshot.
import { execFileSync } from 'node:child_process';
import { mkdirSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { createProviderGoContext, verifyQwenSnapshot } from './run-provider-go.mjs';

export const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
export const MANIFEST_PATH = 'providers/import-boundary.json';

export const loadArtifacts = (root = repoRoot) => {
  const manifest = JSON.parse(readFileSync(path.join(root, MANIFEST_PATH), 'utf8'));
  return manifest.buildInterface?.artifacts ?? [];
};

// Windows C toolchains for `GOOS=windows GOARCH=amd64 -buildmode=c-shared`.
export const dllToolchainCandidates = (platform = process.platform) =>
  platform === 'win32'
    ? ['x86_64-w64-mingw32-gcc', 'gcc', 'clang']
    : ['x86_64-w64-mingw32-gcc'];

export const findDllToolchain = ({ platform = process.platform, probe } = {}) => {
  const check =
    probe ??
    ((bin) => {
      try {
        execFileSync(bin, ['--version'], { stdio: 'ignore' });
        return true;
      } catch {
        return false;
      }
    });

  for (const candidate of dllToolchainCandidates(platform)) {
    if (check(candidate)) return candidate;
  }
  return null;
};

const defaultRun = (command, args, { cwd, env }) => {
  execFileSync(command, args, { cwd, env, stdio: 'inherit' });
};

export const buildProviders = ({
  root = repoRoot,
  platform = process.platform,
  probe,
  run = defaultRun,
  goBin = 'go',
  skipDll = false,
  mkdir = mkdirSync,
  logger = console,
  qwenCanonicalSource,
  verifySnapshot = verifyQwenSnapshot,
  compatibleCoreSource,
} = {}) => {
  verifySnapshot({ root, qwenCanonicalSource });
  const artifacts = loadArtifacts(root);
  const context = createProviderGoContext({ root, compatibleCoreSource, qwenCanonicalSource, goBin, run });
  try {
    const toolchain = skipDll ? null : findDllToolchain({ platform, probe });
    const results = [];

    for (const artifact of artifacts) {
      const workdir = path.join(root, artifact.workdir);
      const outputDir = path.dirname(path.join(root, artifact.output));
      const entry = { id: artifact.id, kind: artifact.kind, output: artifact.output, status: 'built' };

      if (artifact.kind === 'plugin-c-shared' && !toolchain) {
        entry.status = 'skipped';
        entry.reason = 'no Windows C toolchain (mingw-w64) available';
        const canonical = qwenCanonicalSource || process.env.CPA_QWEN_CANONICAL_SOURCE;
        const core = compatibleCoreSource || process.env.CPA_COMPATIBLE_CORE_SOURCE;
        entry.localBuildCommand = `node bin/release/build-providers.mjs --qwen-canonical-source ${JSON.stringify(canonical)} --compatible-core-source ${JSON.stringify(core)}`;
        logger.log(
          `[providers] skip ${artifact.id} (${entry.reason}); local build:\n    ${entry.localBuildCommand}`
        );
        results.push(entry);
        continue;
      }

      try {
        context.assertBoundary();
        mkdir(outputDir, { recursive: true });
        const flags = context.forModule(artifact.workdir);
        const args = [artifact.args[0], ...flags, ...artifact.args.slice(1)];
        logger.log(`[providers] build ${artifact.id}: ${[goBin, ...args].join(' ')}`);
        context.assertBoundary();
        run(goBin, args, {
          cwd: workdir,
          env: { ...context.env, ...(artifact.env ?? {}), ...(artifact.kind === 'plugin-c-shared' ? { CC: toolchain } : {}) },
        });
        results.push(entry);
      } catch {
        entry.status = 'failed';
        results.push(entry);
      }
    }

    return { results, toolchain };
  } finally {
    context.cleanup();
  }
};

export const formatBuildSummary = ({ results, toolchain }) => {
  const lines = [
    `[providers] build summary (dll toolchain: ${toolchain ?? 'none'}, DLL builds ${
      toolchain ? 'enabled' : 'skipped'
    }):`,
  ];
  for (const result of results) {
    lines.push(`  ${result.status.padEnd(7)} ${result.id} -> ${result.output}`);
    if (result.localBuildCommand) lines.push(`          local: ${result.localBuildCommand}`);
  }
  return lines.join('\n');
};

const runCli = () => {
  const argv = process.argv.slice(2);
  const known = new Set(['--skip-dll', '--json']);
  let qwenCanonicalSource;
  let compatibleCoreSource;
  const unknown = [];
  for (let i = 0; i < argv.length; i += 1) {
    if (argv[i] === '--qwen-canonical-source' && argv[i + 1] && !argv[i + 1].startsWith('--')) {
      qwenCanonicalSource = argv[++i];
    } else if (argv[i] === '--compatible-core-source' && argv[i + 1] && !argv[i + 1].startsWith('--')) {
      compatibleCoreSource = argv[++i];
    } else if (!known.has(argv[i])) unknown.push(argv[i]);
  }
  if (unknown.length > 0) {
    console.error(`Unknown arguments: ${unknown.join(', ')}`);
    process.exitCode = 1;
    return;
  }

  let summary;
  try {
    summary = buildProviders({ skipDll: argv.includes('--skip-dll'), qwenCanonicalSource, compatibleCoreSource });
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
    return;
  }

  if (argv.includes('--json')) {
    console.log(JSON.stringify(summary, null, 2));
  } else {
    console.log(`\n${formatBuildSummary(summary)}`);
  }

  if (summary.results.some((result) => result.status === 'failed')) {
    process.exitCode = 1;
  }
};

const entryPoint = process.argv[1] ? pathToFileURL(path.resolve(process.argv[1])).href : '';
if (entryPoint === import.meta.url) runCli();
