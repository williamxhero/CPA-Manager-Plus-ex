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
//
// Exit code is non-zero if any provider module fails; every module is always
// attempted so a single run reports the full picture.
import { execFileSync } from 'node:child_process';
import { existsSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

export const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
export const MANIFEST_PATH = 'providers/import-boundary.json';
export const ALLOWED_SUBCOMMANDS = ['test', 'vet', 'build'];

const defaultRun = (command, args, { cwd }) => {
  execFileSync(command, args, { cwd, stdio: 'inherit' });
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
} = {}) => {
  if (!ALLOWED_SUBCOMMANDS.includes(subcommand)) {
    throw new Error(
      `unsupported go subcommand '${subcommand}' (expected one of: ${ALLOWED_SUBCOMMANDS.join(', ')})`
    );
  }

  const modules = loadProviderModules(root);
  const results = [];

  for (const module of modules) {
    const command = [goBin, subcommand, './...', ...extraArgs].join(' ');
    logger.log(`\n[providers] ${module.id}: ${command}`);
    try {
      run(goBin, [subcommand, './...', ...extraArgs], { cwd: path.join(root, module.dir) });
      results.push({ ...module, ok: true });
    } catch {
      results.push({ ...module, ok: false });
    }
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
  const [subcommand = 'test', ...unknown] = head;

  if (unknown.length > 0) {
    console.error(`Unknown arguments: ${unknown.join(', ')}`);
    process.exitCode = 1;
    return;
  }

  let results;
  try {
    results = runProviderGo({ subcommand, extraArgs });
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
