import { readdirSync, readFileSync, statSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const sourceRoot = path.join(repoRoot, 'apps/web/src');
const checkedRoots = ['features', 'components'];
const sourceExtensions = new Set(['.ts', '.tsx']);

const walkFiles = (dir) =>
  readdirSync(dir).flatMap((entry) => {
    const filePath = path.join(dir, entry);
    const stat = statSync(filePath);
    if (stat.isDirectory()) return walkFiles(filePath);
    return [filePath];
  });

// Detect real cross-layer page imports only: static `import`/`export ... from`
// declarations (including side-effect and type-only forms), dynamic `import()`,
// and `require()` calls. The match is anchored to the statement keyword, so a
// bare `@/pages` substring trapped inside a string literal or comment (for
// example a wiring test asserting against page source text) is not a real
// import and must not be reported as a boundary violation.
const pageImportPatterns = [
  /(?:^|[\r\n])\s*import\s+(?:type\s+)?(?:[^'"]*?\sfrom\s*)?['"]@\/pages(?:\/|['"])/,
  /(?:^|[\r\n])\s*export\s+(?:type\s+)?[^'"]*?\sfrom\s*['"]@\/pages(?:\/|['"])/,
  /\bimport\s*\(\s*['"]@\/pages(?:\/|['"])/,
  /\brequire\s*\(\s*['"]@\/pages(?:\/|['"])/,
];

const importsPages = (source) => pageImportPatterns.some((pattern) => pattern.test(source));

describe('frontend architecture boundaries', () => {
  it('keeps feature and business component code from importing pages', () => {
    const offenders = checkedRoots
      .flatMap((root) => walkFiles(path.join(sourceRoot, root)))
      .filter((filePath) => sourceExtensions.has(path.extname(filePath)))
      .filter((filePath) => importsPages(readFileSync(filePath, 'utf8')))
      .map((filePath) => path.relative(repoRoot, filePath));

    expect(offenders).toEqual([]);
  });

  it('still flags genuine cross-layer page imports', () => {
    const offenders = [
      "import { PlanCredentialsPage } from '@/pages/PlanCredentialsPage';",
      "import type { PageProps } from '@/pages/types';",
      "import '@/pages/PlanCredentialsPage';",
      "export { PlanCredentialsPage } from '@/pages/PlanCredentialsPage';",
      "const load = () => import('@/pages/PlanCredentialsPage');",
      "const page = require('@/pages/PlanCredentialsPage');",
    ];

    for (const source of offenders) {
      expect(importsPages(source), source).toBe(true);
    }
  });

  it('ignores page paths that only appear inside string literals or comments', () => {
    const safe = [
      // The wiring test asserts on page source text without importing it.
      `expect(routesSource).toContain("import { PlanCredentialsPage } from '@/pages/PlanCredentialsPage';");`,
      "// see '@/pages/PlanCredentialsPage' for the route definition",
      "const docsLink = 'https://example.test/@/pages/PlanCredentialsPage';",
    ];

    for (const source of safe) {
      expect(importsPages(source), source).toBe(false);
    }
  });
});
