import { spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { afterAll, describe, expect, it } from 'vitest';
import { parse } from 'yaml';
import { classifyChangedFiles } from '../bin/ci/classify-pr-checks.mjs';

const root = new URL('../', import.meta.url);
const workflow = parse(readFileSync(new URL('.github/workflows/pr-check.yml', root), 'utf8'));
const job = workflow.jobs.providers;
const contract = JSON.parse(readFileSync(new URL('providers/import-boundary.json', root), 'utf8'));
const qwen = contract.providers.find((provider) => provider.id === 'qwen').consumerSnapshot;
const temporaryRoot = fileURLToPath(new URL('bin/tmp/', root));
mkdirSync(temporaryRoot, { recursive: true });
const temporary = mkdtempSync(path.join(temporaryRoot, 'provider-ci-pin-test-'));
afterAll(() => rmSync(temporary, { recursive: true, force: true }));

describe('provider CI uses explicit canonical sources', () => {
  it.each(['tests/providerSnapshotReceipt.test.mjs', 'tests/providerBuildWorkflow.test.mjs'])(
    'includes the provider job when %s changes alone', (file) => {
      expect(classifyChangedFiles([file]).providers).toBe(true);
    }
  );

  it('checks out the declared Qwen repository at the validated snapshot commit', () => {
    const source = job.steps.find((step) => step.name === 'Read Qwen canonical source pin');
    expect(source?.id).toBe('qwen-source');
    expect(source?.run).toContain('providers/import-boundary.json');
    expect(source?.run).toContain('williamxhero/CPA-Qwen');
    expect(source?.run).toContain('[0-9a-f]{40}');
    expect(qwen.commit).toMatch(/^[0-9a-f]{40}$/);
    const checkout = job.steps.find((step) => step.name === 'Checkout pinned CPA-Qwen');
    expect(checkout?.with).toMatchObject({
      repository: qwen.repository,
      ref: '${{ steps.qwen-source.outputs.commit }}',
      path: '.ci-sources/CPA-Qwen',
      'persist-credentials': false,
    });
    expect(job.steps.indexOf(checkout)).toBeGreaterThan(job.steps.indexOf(source));
  });

  it.each(['valid', 'missing', 'wrong-repository', 'short-commit', 'newline-commit'])(
    'executes the workflow pin reader with a %s source contract', (mode) => {
      const step = job.steps.find((entry) => entry.name === 'Read Qwen canonical source pin');
      const code = step.run.match(/<<'NODE'\n([\s\S]+)\nNODE\s*$/)?.[1];
      expect(code).toBeTruthy();
      const fixture = mkdtempSync(path.join(temporary, 'case-'));
      mkdirSync(path.join(fixture, 'providers'));
      const manifest = structuredClone(contract);
      const provider = manifest.providers.find((entry) => entry.id === 'qwen');
      if (mode === 'missing') delete provider.consumerSnapshot;
      if (mode === 'wrong-repository') provider.consumerSnapshot.repository = 'fixture/wrong';
      if (mode === 'short-commit') provider.consumerSnapshot.commit = qwen.commit.slice(0, 7);
      if (mode === 'newline-commit') provider.consumerSnapshot.commit = `${qwen.commit}\ninjected=1`;
      writeFileSync(path.join(fixture, 'providers/import-boundary.json'), JSON.stringify(manifest));
      const output = path.join(fixture, 'outputs.txt');
      const result = spawnSync(process.execPath, ['--input-type=module', '-e', code], {
        cwd: fixture, encoding: 'utf8', env: { ...process.env, GITHUB_OUTPUT: output },
      });
      expect(result.error).toBeUndefined();
      if (mode === 'valid') {
        expect(result.status, result.stderr).toBe(0);
        expect(readFileSync(output, 'utf8')).toBe(`commit=${qwen.commit}\n`);
      } else {
        expect(result.status).not.toBe(0);
        expect(result.stderr).toContain('qwen_snapshot_contract_invalid');
        expect(existsSync(output)).toBe(false);
      }
    }
  );

  it('uses an immutable compatible Core SDK instead of the old published module', () => {
    const checkout = job.steps.find((step) => step.name === 'Checkout compatible Core SDK');
    expect(checkout?.with).toMatchObject({
      repository: 'williamxhero/CPA-Core-ex',
      ref: 'e2f6cc33080ef5c0bbb45c711156520b126d4d6f',
      path: '.ci-sources/CPA-Core-ex',
      'persist-credentials': false,
    });
    expect(checkout.with.ref).toMatch(/^[0-9a-f]{40}$/);
    expect(job.env.CPA_COMPATIBLE_CORE_SOURCE).toBe('${{ github.workspace }}/.ci-sources/CPA-Core-ex');
    expect(job.env.CPA_QWEN_CANONICAL_SOURCE).toBe('${{ github.workspace }}/.ci-sources/CPA-Qwen');
  });

  it('keeps raw canonical and consumer checkout bytes reproducible without global Git edits', () => {
    expect(job.env).toMatchObject({
      GIT_CONFIG_COUNT: '1', GIT_CONFIG_KEY_0: 'core.autocrlf', GIT_CONFIG_VALUE_0: 'false',
    });
    expect(JSON.stringify(job)).not.toContain('git config --global');
  });

  it('sets up Node/Python and sources before every strict provider invocation', () => {
    const node = job.steps.findIndex((step) => step.name === 'Setup provider Node.js');
    const python = job.steps.findIndex((step) => step.name === 'Check provider Python');
    const canonical = job.steps.findIndex((step) => step.name === 'Checkout pinned CPA-Qwen');
    const sdk = job.steps.findIndex((step) => step.name === 'Checkout compatible Core SDK');
    expect(node).toBeGreaterThan(-1);
    expect(python).toBeGreaterThan(-1);
    expect(job.steps[python].run).toBe('python --version');
    const commands = job.steps.filter((step) => /node bin\/release\/(run-provider-go|build-providers)\.mjs/.test(step.run ?? ''));
    expect(commands).toHaveLength(3);
    for (const step of commands) {
      const index = job.steps.indexOf(step);
      for (const prerequisite of [node, python, canonical, sdk]) expect(index).toBeGreaterThan(prerequisite);
      expect(step['continue-on-error']).toBeUndefined();
    }
  });
});
