// Real local Git + the canonical Python verifier; only the compiler is synthetic.
// Fixture commits/receipts are generated from matching files, never product provenance.
import { execFileSync } from 'node:child_process';
import { copyFileSync, linkSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, realpathSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { afterAll, describe, expect, it } from 'vitest';
import { repoRoot, runProviderGo, verifyQwenSnapshot } from '../bin/release/run-provider-go.mjs';
import { buildProviders } from '../bin/release/build-providers.mjs';

mkdirSync(path.join(repoRoot, 'bin/tmp'), { recursive: true });
const temporary = mkdtempSync(path.join(repoRoot, 'bin/tmp/qwen-receipt-test-'));
afterAll(() => rmSync(temporary, { recursive: true, force: true }));
const canonical = path.join(temporary, 'canonical');
mkdirSync(path.join(canonical, 'scripts'), { recursive: true });
const git = (...args) => execFileSync('git', ['-C', canonical, ...args], { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }).trim();
git('init', '-q');
git('config', 'user.email', 'fixture@example.invalid');
git('config', 'user.name', 'Receipt fixture');
git('config', 'core.autocrlf', 'false');
copyFileSync(path.join(repoRoot, 'providers/qwen/scripts/verify-manager-snapshot.py'), path.join(canonical, 'scripts/verify-manager-snapshot.py'));
writeFileSync(path.join(canonical, 'go.mod'), 'module cpa-credential-qwen\n');
writeFileSync(path.join(canonical, 'go.sum'), '');
git('add', '.');
git('-c', 'commit.gpgsign=false', 'commit', '-qm', 'controlled verifier fixture');
const previousCommit = git('rev-parse', 'HEAD');
writeFileSync(path.join(canonical, 'main.go'), 'package main\nfunc main() {}\n');
git('add', '.');
git('-c', 'commit.gpgsign=false', 'commit', '-qm', 'controlled payload fixture');
const commit = git('rev-parse', 'HEAD');
const names = git('ls-files', '-z').split('\0').filter(Boolean);
const python = process.env.CPA_QWEN_PYTHON || 'python';
const baseManifest = JSON.parse(readFileSync(path.join(repoRoot, 'providers/import-boundary.json'), 'utf8'));
const compatibleCoreSource = path.join(temporary, 'core');
mkdirSync(path.join(compatibleCoreSource, 'sdk/pluginapi'), { recursive: true });
writeFileSync(path.join(compatibleCoreSource, 'go.mod'), 'module github.com/router-for-me/CLIProxyAPI/v8\n');
const logger = { log() {} };

function consumer() {
  const root = mkdtempSync(path.join(temporary, 'manager-'));
  const snapshot = path.join(root, 'providers/qwen');
  for (const name of names) {
    mkdirSync(path.dirname(path.join(snapshot, name)), { recursive: true });
    copyFileSync(path.join(canonical, name), path.join(snapshot, name));
  }
  execFileSync(python, ['-B', path.join(canonical, 'scripts/verify-manager-snapshot.py'), '--canonical-source', canonical, '--snapshot', snapshot, '--write-receipt'], { stdio: 'pipe' });
  const manifest = structuredClone(baseManifest);
  manifest.providers = manifest.providers.filter((provider) => provider.id === 'qwen');
  manifest.providers[0].consumerSnapshot.commit = commit;
  manifest.buildInterface.artifacts = manifest.buildInterface.artifacts.filter((artifact) => artifact.provider === 'qwen');
  writeFileSync(path.join(root, 'providers/import-boundary.json'), JSON.stringify(manifest));
  return { root, snapshot, manifest, qwenCanonicalSource: canonical, compatibleCoreSource, logger };
}

describe('Qwen consumer receipt production gate', () => {
  for (const linkedPath of ['bin', 'bin/tmp', 'bin/tmp/provider-build', 'bin/tmp/provider-build/qwen']) {
    it(`rejects output ancestor ${linkedPath} junction before any Go/probe without polluting the verified snapshot`, () => {
      const fixture = consumer();
      const target = path.join(fixture.root, linkedPath);
      mkdirSync(path.dirname(target), { recursive: true });
      symlinkSync(fixture.snapshot, target, process.platform === 'win32' ? 'junction' : 'dir');
      const before = readdirSync(fixture.snapshot, { recursive: true }).sort();
      expect(verifyQwenSnapshot(fixture).status).toBe('verified');
      const calls = [];
      const run = (_command, args, { cwd }) => {
        calls.push('go');
        if (args.includes('-o')) writeFileSync(path.resolve(cwd, args[args.indexOf('-o') + 1]), 'synthetic pollution\n');
      };
      expect(() => buildProviders({ ...fixture, run, probe: () => { calls.push('probe'); return true; } })).toThrow(/provider_write_path/);
      expect(() => buildProviders({ ...fixture, skipDll: true, run, probe: () => calls.push('probe') })).toThrow(/provider_write_path/);
      for (const subcommand of ['test', 'vet', 'build']) {
        expect(() => runProviderGo({ ...fixture, subcommand, run })).toThrow(/provider_write_path/);
      }
      expect(calls).toEqual([]);
      expect(readdirSync(fixture.snapshot, { recursive: true }).sort()).toEqual(before);
      expect(verifyQwenSnapshot(fixture).status).toBe('verified');
    }, 30000);
  }

  for (const kind of ['file-symlink', 'dangling-file-symlink', 'output-junction', 'header-symlink']) {
    it(`rejects ${kind} at an existing artifact target before any Go/probe`, () => {
      const fixture = consumer();
      const output = path.join(fixture.root, 'bin/tmp/provider-build/qwen', kind === 'header-symlink' ? 'aliyun-bss-quota.h' : 'aliyun-bss-quota.dll');
      mkdirSync(path.dirname(output), { recursive: true });
      const target = kind === 'output-junction' ? fixture.snapshot : path.join(fixture.snapshot, kind === 'dangling-file-symlink' ? 'must-not-be-created.go' : 'main.go');
      symlinkSync(target, output, kind === 'output-junction' ? (process.platform === 'win32' ? 'junction' : 'dir') : 'file');
      const calls = [];
      const before = readdirSync(fixture.snapshot, { recursive: true }).sort();
      expect(verifyQwenSnapshot(fixture).status).toBe('verified');
      expect(() => buildProviders({ ...fixture, run: () => calls.push('go'), probe: () => { calls.push('probe'); return true; } })).toThrow(/provider_write_path_link/);
      expect(() => runProviderGo({ ...fixture, run: () => calls.push('go') })).toThrow(/provider_write_path_link/);
      expect(calls).toEqual([]);
      expect(readdirSync(fixture.snapshot, { recursive: true }).sort()).toEqual(before);
      expect(verifyQwenSnapshot(fixture).status).toBe('verified');
    }, 30000);
  }

  it('rejects a linked workspace ancestor even when its snapshot itself verifies', () => {
    const fixture = consumer();
    const alias = `${fixture.root}-alias`;
    symlinkSync(fixture.root, alias, process.platform === 'win32' ? 'junction' : 'dir');
    const linked = { ...fixture, root: alias };
    const calls = [];
    expect(verifyQwenSnapshot(linked).status).toBe('verified');
    expect(() => buildProviders({ ...linked, run: () => calls.push('go'), probe: () => { calls.push('probe'); return true; } })).toThrow(/provider_write_path_link/);
    expect(calls).toEqual([]);
    expect(verifyQwenSnapshot(fixture).status).toBe('verified');
  }, 30000);

  for (const filename of ['cpa-credential-qwen-windows-amd64.dll', 'aliyun-bss-quota.dll', 'bailian-quota.exe', 'aliyun-bss-quota.h']) {
    it(`rejects existing output file link ${filename} before Go/probe and preserves source`, () => {
      const fixture = consumer();
      const output = path.join(fixture.root, 'bin/tmp/provider-build/qwen', filename);
      mkdirSync(path.dirname(output), { recursive: true });
      const source = path.join(fixture.snapshot, 'main.go');
      // A hardlink is also a write alias; unlike a Windows file symlink it needs
      // no elevation, so this regression never silently skips on Windows.
      linkSync(source, output);
      const before = readFileSync(source);
      const calls = [];
      expect(verifyQwenSnapshot(fixture).status).toBe('verified');
      expect(() => buildProviders({ ...fixture, run: () => calls.push('go'), probe: () => { calls.push('probe'); return true; } })).toThrow(/provider_write_path/);
      expect(() => runProviderGo({ ...fixture, run: () => calls.push('go') })).toThrow(/provider_write_path/);
      expect(calls).toEqual([]);
      expect(readFileSync(source)).toEqual(before);
      expect(verifyQwenSnapshot(fixture).status).toBe('verified');
    }, 30000);
  }

  it('rejects an artifact output placed directly inside source without any link', () => {
    const fixture = consumer();
    const artifact = fixture.manifest.buildInterface.artifacts[0];
    artifact.output = 'providers/qwen/main.go';
    artifact.args[artifact.args.indexOf('-o') + 1] = 'main.go';
    writeFileSync(path.join(fixture.root, 'providers/import-boundary.json'), JSON.stringify(fixture.manifest));
    const calls = [];
    expect(() => buildProviders({ ...fixture, run: () => calls.push('go'), probe: () => { calls.push('probe'); return true; } })).toThrow(/provider_write_path/);
    expect(calls).toEqual([]);
    expect(verifyQwenSnapshot(fixture).status).toBe('verified');
  }, 30000);

  it('accepts a fully matching receipt enrolled by the canonical verifier', () => {
    expect(verifyQwenSnapshot(consumer())).toMatchObject({ status: 'verified', commit, files: names.length });
  });

  for (const mutation of ['missing', 'extra', 'changed', 'missing-receipt', 'repository', 'commit', 'tree', 'file-hash', 'file-set']) {
    it(`rejects ${mutation} before any production build/test/vet Go command`, () => {
      const fixture = consumer();
      const receiptPath = path.join(fixture.snapshot, 'CPA_QWEN_SOURCE.json');
      if (mutation === 'missing') rmSync(path.join(fixture.snapshot, 'main.go'));
      else if (mutation === 'extra') writeFileSync(path.join(fixture.snapshot, 'injected.go'), 'package main\n');
      else if (mutation === 'changed') writeFileSync(path.join(fixture.snapshot, 'main.go'), 'package changed\n');
      else if (mutation === 'missing-receipt') rmSync(receiptPath);
      else {
        const receipt = JSON.parse(readFileSync(receiptPath, 'utf8'));
        if (mutation === 'repository') receipt.canonical.repository = 'fixture/not-canonical';
        if (mutation === 'commit') receipt.canonical.commit = previousCommit;
        if (mutation === 'tree') receipt.canonical.tree = git('rev-parse', 'HEAD~1^{tree}');
        if (mutation === 'file-hash') receipt.canonical.files['main.go'] = receipt.canonical.files['go.mod'];
        if (mutation === 'file-set') delete receipt.canonical.files['main.go'];
        writeFileSync(receiptPath, JSON.stringify(receipt));
      }
      const calls = [];
      const diagnostic = ['missing', 'extra', 'changed'].includes(mutation) ? /snapshot_mismatch/
        : mutation === 'missing-receipt' ? /No such file|cannot find/i : /receipt does not match/;
      for (const subcommand of ['test', 'vet', 'build']) {
        expect(() => runProviderGo({ ...fixture, subcommand, run: () => calls.push('go') })).toThrow(diagnostic);
      }
      expect(() => buildProviders({ ...fixture, skipDll: true, run: () => calls.push('go'), probe: () => calls.push('probe') })).toThrow(diagnostic);
      expect(calls).toEqual([]);
    }, 30000);
  }

  it('rejects an unexpected canonical pin even when the copy and receipt match HEAD', () => {
    const fixture = consumer();
    fixture.manifest.providers[0].consumerSnapshot.commit = previousCommit;
    writeFileSync(path.join(fixture.root, 'providers/import-boundary.json'), JSON.stringify(fixture.manifest));
    expect(() => verifyQwenSnapshot(fixture)).toThrow(/qwen_canonical_commit_mismatch/);
  });

  it('rejects linked directories and a linked snapshot root (Windows junction included)', () => {
    const nested = consumer();
    symlinkSync(compatibleCoreSource, path.join(nested.snapshot, 'linked'), process.platform === 'win32' ? 'junction' : 'dir');
    expect(() => verifyQwenSnapshot(nested)).toThrow(/linked directory/);
    const rootLink = consumer();
    const original = path.join(rootLink.root, 'retained-source');
    // Only a fixture is moved; no product snapshot is modified.
    execFileSync(python, ['-B', '-c', 'import os,sys;os.rename(sys.argv[1],sys.argv[2])', rootLink.snapshot, original]);
    symlinkSync(original, rootLink.snapshot, process.platform === 'win32' ? 'junction' : 'dir');
    expect(() => verifyQwenSnapshot(rootLink)).toThrow(/links or junctions/);
  });

  it('supports two synthetic builds plus test/vet and repeated strict verification without changing source or receipt', () => {
    const fixture = consumer();
    const inputs = [...names, 'CPA_QWEN_SOURCE.json'];
    const before = inputs.map((name) => readFileSync(path.join(fixture.snapshot, name)));
    const calls = [];
    const run = (command, args, { cwd, env }) => {
      calls.push(args);
      expect(env.GOWORK).toBe('off');
      expect(env.GOENV).toBe('off');
      expect(env.GOTOOLCHAIN).toBe('local');
      expect(env.GOFLAGS).toBe('');
      expect(env).not.toHaveProperty('GOROOT');
      if (args[0] === 'mod') {
        expect(args).toContain(`-replace=github.com/router-for-me/CLIProxyAPI/v8=${compatibleCoreSource}`);
        return;
      }
      expect(args).toContain('-mod=mod');
      const modfile = args.find((arg) => arg.startsWith('-modfile=')).slice('-modfile='.length);
      expect(path.relative(fixture.snapshot, modfile).startsWith('..')).toBe(true);
      expect(path.relative(realpathSync.native(fixture.snapshot), realpathSync.native(modfile)).startsWith('..')).toBe(true);
      if (args.includes('-o')) {
        const output = path.resolve(cwd, args[args.indexOf('-o') + 1]);
        expect(path.relative(fixture.snapshot, output).startsWith('..')).toBe(true);
        writeFileSync(output, 'synthetic compiler boundary only; NOT a compiled artifact\n');
        expect(path.relative(realpathSync.native(fixture.snapshot), realpathSync.native(output)).startsWith('..')).toBe(true);
      }
    };
    for (let i = 0; i < 2; i += 1) {
      expect(verifyQwenSnapshot(fixture).status).toBe('verified');
      const result = buildProviders({ ...fixture, run, platform: 'linux', probe: () => true });
      expect(result.results.map((entry) => entry.status)).toEqual(['built', 'built', 'built']);
      expect(verifyQwenSnapshot(fixture).status).toBe('verified');
    }
    for (const subcommand of ['test', 'vet']) expect(runProviderGo({ ...fixture, run, subcommand })[0].ok).toBe(true);
    expect(verifyQwenSnapshot(fixture).status).toBe('verified');
    expect(inputs.map((name) => readFileSync(path.join(fixture.snapshot, name)))).toEqual(before);
    expect(calls.filter((args) => args.at(-1) === './cmd/bailian-quota')).toHaveLength(2);
    expect(calls.filter((args) => args.at(-1) === './cmd/aliyun-bss-quota-plugin')).toHaveLength(2);
  }, 60000);
});
