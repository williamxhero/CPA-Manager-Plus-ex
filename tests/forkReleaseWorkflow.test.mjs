import { spawnSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { parse } from 'yaml';
import { describe, expect, it } from 'vitest';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const workflowText = readFileSync(
  path.join(repoRoot, '.github', 'workflows', 'fork-native-web-release.yml'),
  'utf8'
);
const workflow = parse(workflowText);
const { authorize, dry_run: dryRun, windows_smoke: windowsSmoke, publish } = workflow.jobs;
const stepNamed = (job, name) => {
  const step = job.steps.find((candidate) => candidate.name === name);
  if (!step) throw new Error(`Missing fork workflow step: ${name}`);
  return step;
};
const actionStep = (job, action) => job.steps.find((step) => step.uses?.startsWith(`${action}@`));
const scripts = (job) => job.steps.map((step) => step.run ?? '').join('\n');
const authorizeScript = authorize.steps[0].run;
const sourceSha = 'a'.repeat(40);
const frozenDevSha = 'b'.repeat(40);
const authorizedEnv = {
  ...process.env,
  GITHUB_EVENT_NAME: 'workflow_dispatch',
  GITHUB_REPOSITORY: 'williamxhero/CPA-Manager-Plus',
  GITHUB_REF: 'refs/heads/main',
  GITHUB_REF_TYPE: 'branch',
  GITHUB_SHA: sourceSha,
  EXPECTED_SHA: sourceSha,
  FROZEN_DEV_SHA: frozenDevSha,
  OPT_IN: 'true',
  MODE: 'dry-run',
  CONFIRM_PUBLISH: 'false',
  DRY_RUN_ID: '',
  DRY_RUN_ATTEMPT: '',
  RECEIPT_SHA: '',
};
const runAuthorization = (overrides = {}) => {
  const result = spawnSync('bash', ['--noprofile', '--norc', '-s'], {
    cwd: repoRoot,
    input: authorizeScript,
    encoding: 'utf8',
    env: { ...authorizedEnv, ...overrides },
    timeout: 10000,
  });
  if (result.error) throw result.error;
  return result;
};

describe('fork native/web release workflow', () => {
  it('is dispatch-only with explicit opt-in, exact source, and Gate 2 inputs', () => {
    expect(Object.keys(workflow.on)).toEqual(['workflow_dispatch']);
    expect(workflow['run-name']).toBe(
      'Fork native/web ${{ inputs.mode }} ${{ inputs.version }} @ ${{ inputs.expected_sha }}'
    );
    const inputs = workflow.on.workflow_dispatch.inputs;
    expect(Object.keys(inputs).sort()).toEqual([
      'confirm_publish',
      'dry_run_attempt',
      'dry_run_id',
      'expected_sha',
      'frozen_dev_sha',
      'mode',
      'opt_in',
      'receipt_sha',
      'version',
    ]);
    for (const name of ['opt_in', 'confirm_publish']) {
      expect(inputs[name]).toMatchObject({ required: true, type: 'boolean', default: false });
    }
    expect(inputs.mode).toMatchObject({
      required: true,
      type: 'choice',
      default: 'dry-run',
      options: ['dry-run', 'publish'],
    });
    expect(inputs.version).toMatchObject({ required: true, default: 'v1.14.5-williamxhero.1' });
    for (const name of ['expected_sha', 'frozen_dev_sha']) {
      expect(inputs[name]).toMatchObject({ required: true, type: 'string' });
    }
    for (const name of ['dry_run_id', 'dry_run_attempt', 'receipt_sha']) {
      expect(inputs[name]).toMatchObject({ required: false, type: 'string' });
    }
  });

  it('serializes each version without cancellation and grants write only to publish', () => {
    expect(workflow.concurrency).toEqual({
      group: 'fork-native-web-${{ inputs.version }}',
      'cancel-in-progress': false,
    });
    expect(workflow.permissions).toEqual({ contents: 'read', actions: 'read' });
    for (const [name, job] of Object.entries(workflow.jobs)) {
      if (name === 'publish') {
        expect(job.permissions).toEqual({ contents: 'write', actions: 'read' });
      } else {
        expect(job.permissions ?? workflow.permissions).toEqual({
          contents: 'read',
          actions: 'read',
        });
      }
    }
    expect(Object.keys(workflow.jobs)).toEqual([
      'authorize',
      'dry_run',
      'windows_smoke',
      'publish',
    ]);
  });

  it('runs authorization unconditionally and accepts only explicit main fork dry-runs', () => {
    expect(authorize.if).toBeUndefined();
    expect(authorize.needs).toBeUndefined();
    expect(authorize.steps[0].if).toBeUndefined();
    expect(runAuthorization().status).toBe(0);
  });

  it.each([
    { GITHUB_REPOSITORY: 'seakee/cpa-manager-plus' },
    { GITHUB_REPOSITORY: 'someone/CPA-Manager-Plus' },
    { GITHUB_EVENT_NAME: 'push' },
    { GITHUB_REF: 'refs/heads/dev' },
    { GITHUB_REF: 'refs/tags/main', GITHUB_REF_TYPE: 'tag' },
    { GITHUB_REF_TYPE: 'tag' },
    { GITHUB_SHA: frozenDevSha },
    { EXPECTED_SHA: '' },
    { EXPECTED_SHA: '$(touch should-not-exist)' },
    { FROZEN_DEV_SHA: '' },
    { OPT_IN: 'false' },
    { OPT_IN: '' },
    { MODE: '' },
    { MODE: 'tag' },
    { MODE: 'publish' },
    { MODE: 'publish', CONFIRM_PUBLISH: 'true' },
    { MODE: 'publish', CONFIRM_PUBLISH: 'true', DRY_RUN_ID: '123', DRY_RUN_ATTEMPT: '1' },
    { MODE: 'publish', CONFIRM_PUBLISH: 'true', DRY_RUN_ID: '123', RECEIPT_SHA: 'c'.repeat(64) },
    {
      MODE: 'publish',
      CONFIRM_PUBLISH: 'true',
      DRY_RUN_ID: '0',
      DRY_RUN_ATTEMPT: '1',
      RECEIPT_SHA: 'c'.repeat(64),
    },
  ])('fails rather than skips unauthorized dispatch %j', (env) => {
    const result = runAuthorization(env);
    expect(result.status).toBe(1);
    expect(result.stderr.trim()).not.toBe('');
  });

  it('requires the exact approved run, attempt, receipt and confirmation for publish', () => {
    expect(
      runAuthorization({
        MODE: 'publish',
        CONFIRM_PUBLISH: 'true',
        DRY_RUN_ID: '123',
        DRY_RUN_ATTEMPT: '2',
        RECEIPT_SHA: 'c'.repeat(64),
      }).status
    ).toBe(0);
    expect(publish.needs).toBe('authorize');
    expect(publish.if).toBe("inputs.mode == 'publish' && inputs.confirm_publish == true");
    expect(publish.env).toMatchObject({
      GITHUB_TOKEN: '${{ github.token }}',
      DRY_RUN_ID: '${{ inputs.dry_run_id }}',
      DRY_RUN_ATTEMPT: '${{ inputs.dry_run_attempt }}',
      RECEIPT_SHA: '${{ inputs.receipt_sha }}',
    });
  });

  it('checks out the exact SHA in every source job and passes inputs only through env to scripts', () => {
    expect(workflow.env).toEqual({
      VERSION: '${{ inputs.version }}',
      EXPECTED_SHA: '${{ inputs.expected_sha }}',
      FROZEN_DEV_SHA: '${{ inputs.frozen_dev_sha }}',
    });
    for (const job of [dryRun, windowsSmoke, publish]) {
      expect(actionStep(job, 'actions/checkout').with).toMatchObject({
        ref: '${{ inputs.expected_sha }}',
        'fetch-depth': 0,
        'persist-credentials': false,
      });
      expect(actionStep(job, 'actions/setup-node').with['node-version']).toBe('22');
    }
    for (const job of Object.values(workflow.jobs)) {
      expect(scripts(job)).not.toContain('${{');
    }
  });

  it('builds and validates only in dry-run, retaining the entire immutable root artifact', () => {
    expect(dryRun.needs).toBe('authorize');
    expect(dryRun.if).toBe("inputs.mode == 'dry-run'");
    expect(dryRun['runs-on']).toBe('ubuntu-latest');
    expect(actionStep(dryRun, 'actions/setup-go').with['go-version']).toBe('1.24.x');
    const runs = scripts(dryRun);
    expect(runs).toContain('+refs/heads/main:refs/remotes/origin/main');
    expect(runs).toContain('+refs/heads/dev:refs/remotes/origin/dev');
    expect(runs).toContain('npm ci');
    expect(runs).toContain('node bin/release/build-fork-release.mjs');
    for (const flag of [
      '--repository "$GITHUB_REPOSITORY"',
      '--tag "$VERSION"',
      '--sha "$EXPECTED_SHA"',
      '--dev-sha "$FROZEN_DEV_SHA"',
      '--output "$RUNNER_TEMP/fork-native-web"',
      '--run-id "$GITHUB_RUN_ID"',
      '--run-attempt "$GITHUB_RUN_ATTEMPT"',
      '--opt-in true',
    ])
      expect(runs).toContain(flag);
    const upload = actionStep(dryRun, 'actions/upload-artifact');
    expect(upload.uses).toMatch(/@[a-f0-9]{40}$/);
    expect(upload.with).toEqual({
      name: 'fork-native-web-${{ inputs.version }}-${{ inputs.expected_sha }}-attempt-${{ github.run_attempt }}',
      path: '${{ runner.temp }}/fork-native-web/',
      'if-no-files-found': 'error',
      overwrite: false,
      'retention-days': 30,
    });
    expect(dryRun.outputs.artifact_id).toBe('${{ steps.upload.outputs.artifact-id }}');
    const summary = stepNamed(dryRun, 'Record Gate 2 artifact and receipt identity');
    expect(summary.run).toContain('sha256File');
    expect(summary.run).toContain('GITHUB_STEP_SUMMARY');
    expect(summary.env).toEqual({
      ARTIFACT_ID: '${{ steps.upload.outputs.artifact-id }}',
      ARTIFACT_DIGEST: '${{ steps.upload.outputs.artifact-digest }}',
    });
  });

  it('preflights before downloading the exact approved artifact ID and never rebuilds in publish', () => {
    const preflight = publish.steps.find((step) => step.id === 'preflight');
    const download = actionStep(publish, 'actions/download-artifact');
    const fullPublish = publish.steps.at(-1);
    expect(publish.steps.indexOf(preflight)).toBeLessThan(publish.steps.indexOf(download));
    expect(publish.steps.indexOf(download)).toBeLessThan(publish.steps.indexOf(fullPublish));
    expect(download.with).toEqual({
      'github-token': '${{ github.token }}',
      'run-id': '${{ inputs.dry_run_id }}',
      'artifact-ids': '${{ steps.preflight.outputs.artifact_id }}',
      path: '${{ runner.temp }}/fork-native-web',
      'merge-multiple': true,
    });
    for (const run of [preflight.run, fullPublish.run]) {
      expect(run).toContain('node bin/release/publish-fork-release.mjs');
      for (const flag of [
        '--repository "$GITHUB_REPOSITORY"',
        '--tag "$VERSION"',
        '--sha "$EXPECTED_SHA"',
        '--dev-sha "$FROZEN_DEV_SHA"',
        '--dry-run-id "$DRY_RUN_ID"',
        '--dry-run-attempt "$DRY_RUN_ATTEMPT"',
        '--receipt-sha "$RECEIPT_SHA"',
        '--opt-in true',
        '--confirm-publish true',
      ])
        expect(run).toContain(flag);
    }
    expect(preflight.run).toContain('--preflight-only true');
    expect(fullPublish.run).not.toContain('--preflight-only');
    expect(fullPublish.run).toContain('--assets-dir "$RUNNER_TEMP/fork-native-web/payload"');
    expect(fullPublish.run).toContain('--receipt-file "$RUNNER_TEMP/fork-native-web/receipt.json"');
    expect(scripts(publish)).not.toMatch(
      /\bnpm\b|\bgo\s+(?:build|test)\b|build-fork-release|package-native|generate-release-info/
    );
    expect(actionStep(publish, 'actions/setup-go')).toBeUndefined();
    expect(actionStep(publish, 'actions/upload-artifact')).toBeUndefined();
  });

  it('scopes Windows smoke to the same-run artifact and temporary package, never an installer or service', () => {
    expect(windowsSmoke.needs).toEqual(['authorize', 'dry_run']);
    expect(windowsSmoke.if).toBe("inputs.mode == 'dry-run'");
    expect(windowsSmoke['runs-on']).toBe('windows-latest');
    expect(actionStep(windowsSmoke, 'actions/setup-go').with['go-version']).toBe('1.24.x');
    expect(actionStep(windowsSmoke, 'actions/download-artifact').with).toEqual({
      'artifact-ids': '${{ needs.dry_run.outputs.artifact_id }}',
      path: '${{ runner.temp }}/fork-native-web',
      'merge-multiple': true,
    });
    const runs = scripts(windowsSmoke);
    expect(runs).toContain('validateForkReceipt(receipt');
    expect(runs).toContain('buildForkAssetRecords');
    expect(runs).toContain("['payload', 'receipt.json']");
    expect(runs).toContain('runId: process.env.GITHUB_RUN_ID');
    expect(runs).toContain('runAttempt: process.env.GITHUB_RUN_ATTEMPT');
    expect(runs).toContain('devCommit: process.env.FROZEN_DEV_SHA');
    expect(runs).toContain(
      "sourceTree: createForkGit(process.cwd())(['rev-parse', 'HEAD^{tree}'])"
    );
    expect(runs).toContain("Join-Path $env:RUNNER_TEMP 'fork-native-web-smoke'");
    expect(runs).toContain('Expand-Archive -LiteralPath $archive -DestinationPath $destination');
    expect(runs).toContain("spawnSync(exe, ['--version']");
    expect(runs).toContain('assert.equal(result.stdout.trim(), VERSION)');
    expect(runs).toContain(
      "import { verifyBinaryEmbedding } from './bin/release/fork-release-contract.mjs'"
    );
    expect(runs).toContain('verifyBinaryEmbedding(binary, html, EXPECTED_SHA)');
    const sqliteGate = windowsSmoke.steps.at(-1);
    expect(sqliteGate['working-directory']).toBe('apps/manager-server');
    expect(sqliteGate.run).toBe('go test ./internal/repository/sqlite');
    expect(runs).not.toMatch(
      /cpa-manager-plusctl|install\.ps1|Start-Service|Stop-Service|sc\.exe|New-Service|-run\s|--serve/
    );
    expect(scripts(dryRun)).not.toMatch(/secrets\.|GITHUB_TOKEN/);
  });

  it('does not invoke upstream publication, registries, Telegram, update indexes, or tag pushes', () => {
    expect(workflowText).not.toMatch(
      /authorize-release-dispatch|validate-release\.mjs|verify-published-release|generate-release-info\.mjs|publish-update-index|send-telegram-release|docker\/|docker\s|ghcr\.io|docker\.io|login-action|softprops\/action-gh-release|git\s+push|secrets\./i
    );
    for (const job of Object.values(workflow.jobs)) {
      for (const step of job.steps.filter((candidate) => candidate.uses)) {
        expect(step.uses).toMatch(
          /^actions\/(?:checkout|setup-node|setup-go|upload-artifact|download-artifact)@[0-9a-f]{40}$/
        );
      }
    }
    expect(workflowText).toContain('prerelease=true/latest=false');
  });
});
