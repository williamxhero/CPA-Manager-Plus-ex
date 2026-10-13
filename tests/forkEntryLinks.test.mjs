import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const read = (rel) => readFileSync(path.resolve(repoRoot, rel), 'utf8');

const FORK_SLUG = 'williamxhero/CPA-Manager-Plus-ex';
const UPSTREAM_SLUG = 'seakee/CPA-Manager-Plus';

// User-facing project-entry surfaces that must point at the fork (refs SPEC #6).
const SWITCHED_FILES = [
  'README.md',
  'README_CN.md',
  'apps/web/src/components/layout/MainLayout.tsx',
  'apps/web/src/features/system/SystemPage.tsx',
  'apps/web/src/features/dashboard/versionReleaseLinks.ts',
  'apps/web/src/features/demo/demoFixtures.ts',
  'apps/docs/.vitepress/config.ts',
  'apps/docs/deployment/cpa-panel.md',
  'apps/docs/en/deployment/cpa-panel.md',
  'apps/docs/reference/releases.md',
  'apps/docs/en/reference/releases.md',
];

describe('fork project-entry links (SPEC #6)', () => {
  it.each(SWITCHED_FILES)('%s points project entries at the fork, not upstream', (rel) => {
    const text = read(rel);
    expect(text).toContain(FORK_SLUG);
    expect(text).not.toContain(`github.com/${UPSTREAM_SLUG}`);
  });

  it('keeps the original author/LICENSE attribution', () => {
    expect(read('LICENSE')).toContain('Seakee');
    expect(read('README.md')).toContain('Copyright 2026 Seakee');
  });

  it('keeps the Go module identity on the upstream path', () => {
    expect(read('apps/manager-server/go.mod')).toContain(
      'module github.com/seakee/cpa-manager-plus/apps/manager-server'
    );
  });

  it('keeps the explicit upstream sync remote in CONTRIBUTING', () => {
    expect(read('CONTRIBUTING.md')).toContain(
      'git remote add upstream https://github.com/seakee/CPA-Manager-Plus.git'
    );
  });

  it('leaves historical release notes referencing their original upstream URLs', () => {
    expect(read('docs/release-notes/v1.14.3-en.md')).toContain(UPSTREAM_SLUG);
  });
});
