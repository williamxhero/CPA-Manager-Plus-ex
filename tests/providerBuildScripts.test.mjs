import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { afterAll, describe, expect, it } from 'vitest';

import {
  ALLOWED_SUBCOMMANDS,
  formatProviderGoSummary,
  loadProviderModules,
  repoRoot,
  runProviderGo,
  verifyQwenSnapshot,
} from '../bin/release/run-provider-go.mjs';
import {
  buildProviders,
  dllToolchainCandidates,
  findDllToolchain,
  formatBuildSummary,
  loadArtifacts,
} from '../bin/release/build-providers.mjs';

const manifest = JSON.parse(
  readFileSync(path.join(repoRoot, 'providers', 'import-boundary.json'), 'utf8')
);

const silentLogger = { log: () => {} };
const verifiedQwenSnapshot = () => {};
mkdirSync(path.join(repoRoot, 'bin/tmp'), { recursive: true });
const compatibleCoreSource = mkdtempSync(path.join(repoRoot, 'bin/tmp/provider-sdk-test-'));
mkdirSync(path.join(compatibleCoreSource, 'sdk/pluginapi'), { recursive: true });
writeFileSync(path.join(compatibleCoreSource, 'go.mod'), 'module github.com/router-for-me/CLIProxyAPI/v8\n');
afterAll(() => rmSync(compatibleCoreSource, { recursive: true, force: true }));

describe('provider Go runner (SPEC5-D)', () => {
  it('discovers every provider module that carries a go.mod', () => {
    const modules = loadProviderModules(repoRoot);

    expect(modules.map((module) => module.id).sort()).toEqual(['opencode-go', 'qwen']);
    for (const module of modules) {
      expect(module.dir).toMatch(/^providers\//);
    }
  });

  it('runs the go subcommand inside each provider module from the repo root', () => {
    const calls = [];
    const run = (command, args, { cwd }) => calls.push({ command, args, cwd });

    const results = runProviderGo({ subcommand: 'test', run, logger: silentLogger, verifySnapshot: verifiedQwenSnapshot, compatibleCoreSource });

    expect(results.every((result) => result.ok)).toBe(true);
    expect(calls).toHaveLength(4);
    const tests = calls.filter((call) => call.args[0] === 'test');
    expect(tests.map((call) => path.relative(repoRoot, call.cwd).replace(/\\/g, '/'))).toEqual([
      'providers/opencode-go',
      'providers/qwen',
    ]);
    for (const call of tests) {
      expect(call.command).toBe('go');
      expect(call.args).toContain('-mod=mod');
      expect(call.args.some((arg) => arg.startsWith('-modfile='))).toBe(true);
      expect(call.args.at(-1)).toBe('./...');
    }
  });

  it('forwards extra go flags before package paths for both Go commands', () => {
    const calls = [];
    const run = (_command, args) => calls.push(args);

    runProviderGo({
      subcommand: 'test',
      extraArgs: ['-run', 'TestFoo'],
      run,
      logger: silentLogger,
      verifySnapshot: verifiedQwenSnapshot,
      compatibleCoreSource,
    });

    expect(calls.filter((args) => args[0] === 'test').every((args) => args.slice(-3).join(' ') === '-run TestFoo ./...')).toBe(true);
  });

  it('attempts every module and reports failures without short-circuiting', () => {
    let seen = 0;
    const run = () => {
      seen += 1;
      if (seen === 1) throw new Error('boom');
    };

    const results = runProviderGo({ subcommand: 'test', run, logger: silentLogger, verifySnapshot: verifiedQwenSnapshot, compatibleCoreSource });

    expect(seen).toBe(3);
    expect(results.map((result) => result.ok)).toEqual([false, true]);
  });

  it('rejects an unsupported go subcommand', () => {
    expect(() => runProviderGo({ subcommand: 'run', run: () => {}, logger: silentLogger, verifySnapshot: verifiedQwenSnapshot })).toThrow(
      /unsupported go subcommand/
    );
    expect(ALLOWED_SUBCOMMANDS).toEqual(['test', 'vet', 'build']);
  });

  it('fails closed before any Go command when the Qwen consumer receipt is not verified', () => {
    const calls = [];
    expect(() => runProviderGo({
      subcommand: 'test',
      run: (...args) => calls.push(args),
      logger: silentLogger,
      qwenCanonicalSource: 'D:/independent/CPA-Qwen',
      verifySnapshot: () => {
        throw new Error('qwen_snapshot_mismatch:missing CPA_QWEN_SOURCE.json');
      },
    })).toThrow(/qwen_snapshot_mismatch/);
    expect(calls).toHaveLength(0);
  });

  it('summarises per-module results', () => {
    const summary = formatProviderGoSummary(
      [
        { id: 'opencode-go', dir: 'providers/opencode-go', ok: true },
        { id: 'qwen', dir: 'providers/qwen', ok: false },
      ],
      'test'
    );

    expect(summary).toContain('go test summary');
    expect(summary).toContain('ok   opencode-go');
    expect(summary).toContain('FAIL qwen');
  });

  it('requires an explicit compatible Core source before any Go call', () => {
    const calls = [];
    expect(() => runProviderGo({ compatibleCoreSource: '', verifySnapshot: verifiedQwenSnapshot, run: (...args) => calls.push(args) })).toThrow(/compatible_core_source_required/);
    expect(() => buildProviders({ compatibleCoreSource: '', verifySnapshot: verifiedQwenSnapshot, run: () => calls.push('go'), probe: () => calls.push('probe') })).toThrow(/compatible_core_source_required/);
    expect(() => buildProviders({ compatibleCoreSource: path.join(compatibleCoreSource, 'missing'), verifySnapshot: verifiedQwenSnapshot, run: () => calls.push('go'), probe: () => calls.push('probe') })).toThrow(/compatible_core_source_invalid/);
    expect(calls).toHaveLength(0);
  });

  it('does not allow forwarded Go flags to replace the selected source binding', () => {
    const calls = [];
    for (const flag of ['-modfile=other.mod', '--modfile=other.mod', '-mod=vendor', '--mod=vendor', '-overlay=other.json', '-C', '-o', '--o=other.exe']) {
      expect(() => runProviderGo({ extraArgs: [flag], verifySnapshot: verifiedQwenSnapshot, run: () => calls.push('go') })).toThrow(/source_override_forbidden/);
    }
    expect(calls).toEqual([]);
  });
});

describe('provider artifact builder (SPEC5-D)', () => {
  it('declares structured build env and args for every artifact', () => {
    const artifacts = loadArtifacts(repoRoot);

    expect(artifacts.map((artifact) => artifact.id).sort()).toEqual([
      'opencode-go-plugin',
      'qwen-cli',
      'qwen-compat-plugin',
      'qwen-plugin',
    ]);

    const cli = artifacts.find((artifact) => artifact.id === 'qwen-cli');
    expect(cli.kind).toBe('cli');
    expect(cli.env).toEqual({});
    expect(cli.args).toContain('./cmd/bailian-quota');

    const compat = artifacts.find((artifact) => artifact.id === 'qwen-compat-plugin');
    expect(compat.kind).toBe('plugin-c-shared');
    expect(compat.args).toEqual([
      'build',
      '-buildmode=c-shared',
      '-o',
      '../../bin/tmp/provider-build/qwen/aliyun-bss-quota.dll',
      './cmd/aliyun-bss-quota-plugin',
    ]);

    for (const artifact of artifacts.filter((entry) => entry.kind === 'plugin-c-shared')) {
      expect(artifact.env).toEqual({ CGO_ENABLED: '1', GOOS: 'windows', GOARCH: 'amd64' });
      expect(artifact.args).toContain('-buildmode=c-shared');
      // The declared args write to the file named by `output`.
      expect(artifact.args.some((arg) => arg.endsWith(path.basename(artifact.output)))).toBe(true);
    }
  });

  it('skips c-shared DLL builds when no Windows C toolchain is present, but still builds the CLI', () => {
    const calls = [];
    const run = (_compute, args, { env }) => calls.push({ args, env });

    const { results, toolchain } = buildProviders({
      probe: () => false,
      run,
      mkdir: () => {},
      logger: silentLogger,
      verifySnapshot: verifiedQwenSnapshot,
      compatibleCoreSource,
    });

    expect(toolchain).toBeNull();

    const byId = Object.fromEntries(results.map((result) => [result.id, result]));
    expect(byId['opencode-go-plugin'].status).toBe('skipped');
    expect(byId['qwen-plugin'].status).toBe('skipped');
    expect(byId['qwen-plugin'].localBuildCommand).toContain('node bin/release/build-providers.mjs --qwen-canonical-source');
    expect(byId['qwen-plugin'].localBuildCommand).toContain('--compatible-core-source');
    expect(byId['qwen-compat-plugin'].status).toBe('skipped');
    expect(byId['qwen-cli'].status).toBe('built');

    // Only the CLI was actually built.
    expect(calls).toHaveLength(2);
    expect(calls[1].args).toContain('./cmd/bailian-quota');
    expect(calls[1].env.GOOS).not.toBe('windows');
  });

  it('builds the DLLs with the windows cgo environment when a toolchain is available', () => {
    const calls = [];
    const run = (_compute, args, { env }) => calls.push({ args, env });

    const { results, toolchain } = buildProviders({
      platform: 'linux',
      probe: (bin) => bin === 'x86_64-w64-mingw32-gcc',
      run,
      mkdir: () => {},
      logger: silentLogger,
      verifySnapshot: verifiedQwenSnapshot,
      compatibleCoreSource,
    });

    expect(toolchain).toBe('x86_64-w64-mingw32-gcc');
    expect(results.every((result) => result.status === 'built')).toBe(true);
    expect(calls).toHaveLength(6);

    const dllCall = calls.find((call) => call.args.includes('-buildmode=c-shared'));
    expect(dllCall.env).toMatchObject({ CGO_ENABLED: '1', GOOS: 'windows', GOARCH: 'amd64' });
  });

  it('honours --skip-dll even when a toolchain is present', () => {
    const { results, toolchain } = buildProviders({
      probe: () => true,
      run: () => {},
      skipDll: true,
      mkdir: () => {},
      logger: silentLogger,
      verifySnapshot: verifiedQwenSnapshot,
      compatibleCoreSource,
    });

    expect(toolchain).toBeNull();
    expect(
      results.filter((result) => result.kind === 'plugin-c-shared').every((r) => r.status === 'skipped')
    ).toBe(true);
  });

  it('marks an attempted build as failed', () => {
    const run = () => {
      throw new Error('compiler exploded');
    };

    const { results } = buildProviders({
      probe: () => true,
      run,
      mkdir: () => {},
      logger: silentLogger,
      verifySnapshot: verifiedQwenSnapshot,
      compatibleCoreSource,
    });

    expect(results.some((result) => result.status === 'failed')).toBe(true);
  });

  it('exposes platform-appropriate toolchain candidates', () => {
    expect(dllToolchainCandidates('win32')).toContain('gcc');
    expect(dllToolchainCandidates('linux')).toEqual(['x86_64-w64-mingw32-gcc']);
  });

  it('does not probe a compiler or create output when provenance fails, even with --skip-dll', () => {
    const calls = [];
    expect(() => buildProviders({ skipDll: true, probe: () => calls.push('probe'), mkdir: () => calls.push('mkdir'), run: () => calls.push('go'), verifySnapshot: () => { throw new Error('receipt rejected'); } })).toThrow(/receipt rejected/);
    expect(calls).toEqual([]);
  });

  it('requires an explicit canonical source in the production verifier', () => {
    expect(() => verifyQwenSnapshot({ qwenCanonicalSource: '' })).toThrow(/qwen_canonical_source_required/);
  });

  it('summarises the build outcome', () => {
    const summary = formatBuildSummary({
      toolchain: null,
      results: [
        { id: 'qwen-plugin', status: 'skipped', output: 'providers/qwen/bin/qwen.dll', localBuildCommand: 'cd providers/qwen && go build' },
        { id: 'qwen-cli', status: 'built', output: 'providers/qwen/bin/bailian-quota.exe' },
      ],
    });

    expect(summary).toContain('dll toolchain: none');
    expect(summary).toContain('skipped qwen-plugin');
    expect(summary).toContain('built   qwen-cli');
  });
});
