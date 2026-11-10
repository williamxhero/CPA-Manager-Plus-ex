import { spawnSync } from 'node:child_process';
import { readdirSync } from 'node:fs';
import { createRequire } from 'node:module';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('../', import.meta.url));
const excluded = 'nativeControlScripts.test.mjs';
// The installer fixtures assert POSIX permissions, paths and process ownership.
// Run the complete repository suite in local WSL on Windows; Git Bash cannot
// reproduce those contracts. WSL must have Node and Linux native dependencies.
if (process.platform === 'win32') {
  const distro = process.env.CPAMP_TEST_WSL_DISTRO;
  const prefix = distro ? ['--distribution', distro] : [];
  const mapped = spawnSync('wsl.exe', [...prefix, '--exec', 'wslpath', '-u', root], { encoding: 'utf8' });
  if (mapped.error || mapped.status !== 0) {
    console.error('Repository tests on Windows require local WSL with Node.js.');
    process.exit(mapped.status || 1);
  }
  const script = path.posix.join(mapped.stdout.trim(), 'bin/test-repo.mjs');
  const result = spawnSync('wsl.exe', [
    ...prefix, '--exec', 'bash', '-lc', 'exec node "$@"', 'cpamp-repo-tests',
    script, ...process.argv.slice(2),
  ], { cwd: root, stdio: 'inherit' });
  if (result.error) console.error(result.error.message);
  process.exit(result.status ?? 1);
}

// npm uses cmd.exe on Windows, which does not expand shell wildcard arguments.
const files = readdirSync(path.join(root, 'tests'), { withFileTypes: true })
  .filter((entry) => entry.isFile() && entry.name.endsWith('.test.mjs') && entry.name !== excluded)
  .map((entry) => `tests/${entry.name}`)
  .sort();

if (files.length === 0) {
  console.error('No repository test files found');
  process.exitCode = 1;
} else {
  const require = createRequire(import.meta.url);
  const cli = path.join(path.dirname(require.resolve('vitest/package.json')), 'vitest.mjs');
  const result = spawnSync(
    process.execPath,
    [cli, 'run', ...files, '--testTimeout', '30000', '--exclude', `tests/${excluded}`, ...process.argv.slice(2)],
    { cwd: root, env: { ...process.env, CPA_QWEN_PYTHON: process.env.CPA_QWEN_PYTHON || 'python3' }, stdio: 'inherit' }
  );
  if (result.error) console.error(result.error.message);
  process.exitCode = result.status ?? 1;
}
