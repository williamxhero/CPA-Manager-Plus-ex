import { execFileSync } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterAll, describe, expect, it } from 'vitest';

import {
  findSecretFindings,
  globToRegExp,
  isBinaryExtension,
  loadManifest,
  matchesGlob,
  redact,
  repoRoot,
  runBoundaryCheck,
  validateManifest,
} from '../bin/release/check-provider-import-boundary.mjs';

const tempRoots = [];

const makeTempRoot = () => {
  const root = mkdtempSync(path.join(os.tmpdir(), 'cpamp-providers-'));
  tempRoots.push(root);
  return root;
};

afterAll(() => {
  for (const root of tempRoots) rmSync(root, { recursive: true, force: true });
});

const write = (root, relativePath, content) => {
  const absolute = path.join(root, relativePath);
  mkdirSync(path.dirname(absolute), { recursive: true });
  writeFileSync(absolute, content);
};

const baseProvider = () => ({
  id: 'opencode-go',
  dir: 'providers/opencode-go',
  importStrategy: 'git-subtree',
  status: 'planned',
  sourceRepo: 'massiveits/opencode-go-cliproxyapi',
  license: { spdx: 'MIT', copyright: 'Copyright (c) 2026 massiveits' },
  excludeGlobs: ['plugins/**', '**/*.log', 'worker*'],
  requiredFiles: ['LICENSE', 'go.mod'],
  contentAllowlist: [],
});

const baseManifest = () => ({
  version: 1,
  spec: 'SPEC5-A',
  contractDoc: 'docs/providers-layout-contract.md',
  boundaryDoc: 'providers/README.md',
  attributionFile: 'NOTICE',
  globalForbidden: {
    binaryExtensions: ['.dll', '.exe', '.so'],
    pathGlobs: ['**/node_modules/**', '**/dist/**'],
    contentPatterns: [
      { id: 'private-key-block', pattern: '-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----', flags: '' },
      { id: 'openai-key', pattern: '\\bsk-[A-Za-z0-9_-]{20,}\\b', flags: '' },
    ],
  },
  providers: [baseProvider()],
  buildInterface: { status: 'contract-only' },
});

const withProviders = (...providers) => ({ ...baseManifest(), providers });

const noticeFor = (providers) =>
  providers
    .map((provider) => `${provider.sourceRepo}\n${provider.license.spdx}\n${provider.license.copyright}`)
    .join('\n');

const makeRepo = ({ manifest = baseManifest(), providerFiles = {} } = {}) => {
  const root = makeTempRoot();
  write(root, 'docs/providers-layout-contract.md', '');
  write(root, 'providers/README.md', '');
  write(root, 'NOTICE', noticeFor(manifest.providers));
  write(root, 'providers/import-boundary.json', JSON.stringify(manifest, null, 2));
  for (const [relativePath, content] of Object.entries(providerFiles)) {
    write(root, relativePath, content);
  }
  return root;
};

const FAKE_KEY = 'sk-abcdefghijklmnopqrstuvwxyz012345';

describe('provider import boundary contract', () => {
  it('matches **, * and ? globs against provider-relative paths', () => {
    expect(matchesGlob('plugins/windows/amd64/x.dll', 'plugins/**')).toBe(true);
    expect(matchesGlob('plugins', 'plugins/**')).toBe(false);
    expect(matchesGlob('worker8-result.json', 'worker*')).toBe(true);
    expect(matchesGlob('nested/worker8.log', 'worker*')).toBe(false);
    expect(matchesGlob('a/b/c.log', '**/*.log')).toBe(true);
    expect(matchesGlob('c.log', '**/*.log')).toBe(true);
    expect(matchesGlob('node_modules/x.js', '**/node_modules/**')).toBe(true);
    expect(matchesGlob('ab', 'a?')).toBe(true);
    expect(matchesGlob('abc', 'a?')).toBe(false);
    expect(matchesGlob('axlog', '*.log')).toBe(false);
    expect(globToRegExp('**/*.dll').test('x/y/z.dll')).toBe(true);
  });

  it('classifies binary extensions case-insensitively', () => {
    expect(isBinaryExtension('a/b.dll', ['.dll'])).toBe(true);
    expect(isBinaryExtension('a/b.DLL', ['.dll'])).toBe(true);
    expect(isBinaryExtension('a/b.go', ['.dll'])).toBe(false);
  });

  it('redacts matched secret previews', () => {
    expect(redact(FAKE_KEY)).toBe('sk-a***');
  });

  it('detects secrets and never echoes the raw value', () => {
    const findings = findSecretFindings('a.go', `key := "${FAKE_KEY}"\n`);

    expect(findings).toHaveLength(1);
    expect(findings[0]).toContain('openai-key');
    expect(findings[0]).not.toContain(FAKE_KEY);
  });

  it('detects private key blocks', () => {
    const findings = findSecretFindings('id_rsa', '-----BEGIN RSA PRIVATE KEY-----\nMIIB...');

    expect(findings.some((finding) => finding.includes('private-key-block'))).toBe(true);
  });

  it('respects the per-provider content allowlist', () => {
    const findings = findSecretFindings('fixtures/placeholder.go', `"${FAKE_KEY}"`, {
      allowlist: [{ glob: 'fixtures/**', patternId: 'openai-key', reason: 'placeholder' }],
    });

    expect(findings).toEqual([]);
  });

  it('rejects an invalid manifest shape', () => {
    expect(validateManifest({ ...baseManifest(), buildInterface: { status: 'done' } })).toContain(
      'manifest.buildInterface.status must be "contract-only" for SPEC5-A'
    );
  });

  it('rejects duplicate provider directories', () => {
    const findings = validateManifest(withProviders(baseProvider(), { ...baseProvider(), id: 'other' }));

    expect(findings).toContain('duplicate provider dir: providers/opencode-go');
  });

  it('accepts the committed manifest', () => {
    expect(validateManifest(loadManifest(repoRoot))).toEqual([]);
  });

  it('passes on the current repository for every manifest provider state', () => {
    const manifest = loadManifest(repoRoot);
    const { ok, findings, info } = runBoundaryCheck({ root: repoRoot });

    expect(findings).toEqual([]);
    expect(ok).toBe(true);
    // Providers land in separate SPECs, so this must hold both before and after an import.
    for (const provider of manifest.providers) {
      const present = existsSync(path.join(repoRoot, provider.dir));
      expect(
        info.some((line) => line.includes(provider.id) && line.includes('not imported yet'))
      ).toBe(!present);
    }
  });

  it('reports both imported providers as decided in the committed manifest', () => {
    const manifest = loadManifest(repoRoot);
    const byId = Object.fromEntries(manifest.providers.map((provider) => [provider.id, provider]));

    expect(byId['opencode-go'].status).toBe('imported');
    expect(byId['opencode-go'].sourceDecision.status).toBe('decided');
    expect(byId.qwen.status).toBe('imported');
  });

  it('treats a planned provider with no directory as not-yet-imported', () => {
    const root = makeRepo();

    expect(runBoundaryCheck({ root }).ok).toBe(true);
  });

  it('ignores gitignored local build artifacts inside an imported provider tree', () => {
    const manifest = withProviders({ ...baseProvider(), status: 'imported' });
    const root = makeRepo({
      manifest,
      providerFiles: {
        'providers/opencode-go/LICENSE': 'MIT',
        'providers/opencode-go/go.mod': 'module x\n',
        'providers/opencode-go/bin/opencode-go-cliproxyapi-windows-amd64.dll': 'MZ',
        'providers/opencode-go/.gitignore': 'bin/\n',
      },
    });
    execFileSync('git', ['init'], { cwd: root, stdio: 'ignore' });

    const { ok, findings } = runBoundaryCheck({ root });

    expect(findings).toEqual([]);
    expect(ok).toBe(true);
  });

  it('still rejects an unignored provider binary in a git repository', () => {
    const manifest = withProviders({ ...baseProvider(), status: 'imported' });
    const root = makeRepo({
      manifest,
      providerFiles: {
        'providers/opencode-go/LICENSE': 'MIT',
        'providers/opencode-go/go.mod': 'module x\n',
        'providers/opencode-go/bin/opencode-go-cliproxyapi-windows-amd64.dll': 'MZ',
      },
    });
    execFileSync('git', ['init'], { cwd: root, stdio: 'ignore' });

    const { ok, findings } = runBoundaryCheck({ root });

    expect(ok).toBe(false);
    expect(findings.some((finding) => finding.includes('opencode-go-cliproxyapi-windows-amd64.dll'))).toBe(
      true
    );
  });

  it('accepts a clean imported provider tree', () => {
    const manifest = withProviders({ ...baseProvider(), status: 'imported' });
    const root = makeRepo({
      manifest,
      providerFiles: {
        'providers/opencode-go/LICENSE': 'MIT',
        'providers/opencode-go/go.mod': 'module opencode-go-cliproxyapi\n',
        'providers/opencode-go/main.go': 'package main\n',
      },
    });

    expect(runBoundaryCheck({ root }).ok).toBe(true);
  });

  it('rejects a provider tree that ships a binary', () => {
    const manifest = withProviders({ ...baseProvider(), status: 'imported' });
    const root = makeRepo({
      manifest,
      providerFiles: {
        'providers/opencode-go/LICENSE': 'MIT',
        'providers/opencode-go/go.mod': 'module x\n',
        'providers/opencode-go/plugins/windows/amd64/opencode-go-cliproxyapi.dll': 'MZ',
      },
    });

    const { ok, findings } = runBoundaryCheck({ root });

    expect(ok).toBe(false);
    expect(findings.some((finding) => finding.includes('opencode-go-cliproxyapi.dll'))).toBe(true);
  });

  it('rejects excluded workspace junk after a subtree import', () => {
    const manifest = withProviders({ ...baseProvider(), status: 'imported' });
    const root = makeRepo({
      manifest,
      providerFiles: {
        'providers/opencode-go/LICENSE': 'MIT',
        'providers/opencode-go/go.mod': 'module x\n',
        'providers/opencode-go/worker8-result.json': '{}',
      },
    });

    const { ok, findings } = runBoundaryCheck({ root });

    expect(ok).toBe(false);
    expect(
      findings.some((finding) => finding.includes('worker8-result.json') && finding.includes('worker*'))
    ).toBe(true);
  });

  it('rejects a secret committed inside a provider and redacts it', () => {
    const manifest = withProviders({ ...baseProvider(), status: 'imported' });
    const root = makeRepo({
      manifest,
      providerFiles: {
        'providers/opencode-go/LICENSE': 'MIT',
        'providers/opencode-go/go.mod': 'module x\n',
        'providers/opencode-go/leak.go': `package main\n\nvar k = "${FAKE_KEY}"\n`,
      },
    });

    const { ok, findings } = runBoundaryCheck({ root });

    expect(ok).toBe(false);
    expect(findings.some((finding) => finding.includes('leak.go') && finding.includes('openai-key'))).toBe(
      true
    );
    expect(findings.join('\n')).not.toContain(FAKE_KEY);
  });

  it('rejects a provider file larger than the configured cap', () => {
    const base = withProviders({ ...baseProvider(), status: 'imported' });
    const manifest = {
      ...base,
      globalForbidden: { ...base.globalForbidden, maxFileBytes: 32 },
    };
    const root = makeRepo({
      manifest,
      providerFiles: {
        'providers/opencode-go/LICENSE': 'MIT',
        'providers/opencode-go/go.mod': 'module x\n',
        'providers/opencode-go/blob.txt': 'x'.repeat(64),
      },
    });

    const { ok, findings } = runBoundaryCheck({ root });

    expect(ok).toBe(false);
    expect(
      findings.some(
        (finding) => finding.includes('blob.txt') && finding.includes('byte provider file cap')
      )
    ).toBe(true);
  });

  it('fails when an imported provider is missing a required file', () => {
    const manifest = withProviders({ ...baseProvider(), status: 'imported' });
    const root = makeRepo({
      manifest,
      providerFiles: { 'providers/opencode-go/go.mod': 'module x\n' },
    });

    const { ok, findings } = runBoundaryCheck({ root });

    expect(ok).toBe(false);
    expect(
      findings.some((finding) => finding.includes('missing required file') && finding.includes('LICENSE'))
    ).toBe(true);
  });

  it('fails when an imported provider has no directory', () => {
    const manifest = withProviders({ ...baseProvider(), status: 'imported' });
    const root = makeRepo({ manifest });

    const { ok, findings } = runBoundaryCheck({ root });

    expect(ok).toBe(false);
    expect(findings.some((finding) => finding.includes('marked imported'))).toBe(true);
  });

  it('fails when NOTICE omits a provider attribution', () => {
    const root = makeRepo();
    write(root, 'NOTICE', 'no attribution registered here');

    const { ok, findings } = runBoundaryCheck({ root });

    expect(ok).toBe(false);
    expect(findings.some((finding) => finding.includes('massiveits/opencode-go-cliproxyapi'))).toBe(true);
  });
});
