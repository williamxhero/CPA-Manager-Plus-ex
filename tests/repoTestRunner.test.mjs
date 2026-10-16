import { spawnSync } from 'node:child_process';
import { copyFileSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';

let root;
let runner;

beforeEach(() => {
  root = mkdtempSync(path.join(tmpdir(), 'cpamp repo runner '));
  mkdirSync(path.join(root, 'bin'));
  mkdirSync(path.join(root, 'tests'));
  runner = path.join(root, 'bin/test-repo.mjs');
  copyFileSync(new URL('../bin/test-repo.mjs', import.meta.url), runner);
  const vitest = path.join(root, 'node_modules/vitest');
  mkdirSync(vitest, { recursive: true });
  writeFileSync(
    path.join(vitest, 'package.json'),
    JSON.stringify({ name: 'vitest', type: 'module' })
  );
  // Fake only the external test-engine boundary; execute the real runner CLI.
  writeFileSync(
    path.join(vitest, 'vitest.mjs'),
    `
console.log(JSON.stringify({ args: process.argv.slice(2), cwd: process.cwd() }));
process.exitCode = Number(process.env.FIXTURE_TEST_EXIT || 0);
`
  );
});

afterEach(() => rmSync(root, { recursive: true, force: true }));

const run = (args = [], env = {}) =>
  spawnSync(process.execPath, [runner, ...args], {
    cwd: tmpdir(),
    env: { ...process.env, FIXTURE_TEST_EXIT: '0', ...env },
    encoding: 'utf8',
  });

const testFile = (name) => writeFileSync(path.join(root, 'tests', name), '');

describe('repository test runner CLI', () => {
  it('enumerates only top-level test files, sorted, with the existing native-script exclusion', () => {
    testFile('z.test.mjs');
    testFile('a.test.mjs');
    testFile('nativeControlScripts.test.mjs');
    testFile('helper.mjs');
    mkdirSync(path.join(root, 'tests/nested'));
    writeFileSync(path.join(root, 'tests/nested/hidden.test.mjs'), '');
    mkdirSync(path.join(root, 'tests/not-a-file.test.mjs'));
    const result = run();
    expect(result.status).toBe(0);
    expect(JSON.parse(result.stdout)).toEqual({
      args: [
        'run',
        'tests/a.test.mjs',
        'tests/z.test.mjs',
        '--exclude',
        'tests/nativeControlScripts.test.mjs',
      ],
      cwd: root,
    });
  });

  it('forwards arguments containing spaces without shell interpretation', () => {
    testFile('a.test.mjs');
    const result = run(['--testNamePattern', 'missing required files']);
    expect(result.status).toBe(0);
    expect(JSON.parse(result.stdout).args.slice(-2)).toEqual([
      '--testNamePattern',
      'missing required files',
    ]);
  });

  it('propagates a failing test-engine exit code', () => {
    testFile('a.test.mjs');
    expect(run([], { FIXTURE_TEST_EXIT: '7' }).status).toBe(7);
  });

  it('fails closed when no eligible tests exist rather than running an unfiltered suite', () => {
    testFile('nativeControlScripts.test.mjs');
    const result = run();
    expect(result.status).toBe(1);
    expect(result.stderr).toContain('No repository test files found');
    expect(result.stdout).toBe('');
  });
});
