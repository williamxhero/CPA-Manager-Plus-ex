import { spawnSync } from 'node:child_process';
import { readdirSync } from 'node:fs';
import { createRequire } from 'node:module';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('../', import.meta.url));
const excluded = 'nativeControlScripts.test.mjs';
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
    [cli, 'run', ...files, '--exclude', `tests/${excluded}`, ...process.argv.slice(2)],
    { cwd: root, stdio: 'inherit' }
  );
  if (result.error) console.error(result.error.message);
  process.exitCode = result.status ?? 1;
}
