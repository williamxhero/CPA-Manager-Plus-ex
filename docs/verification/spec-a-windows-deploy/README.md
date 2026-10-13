# SPEC-A isolated Windows verification — 2026-10-13

Specification: [Refs #52](https://github.com/williamxhero/CPA-Manager-Plus-ex/issues/52).

Environment: Windows 11, Windows PowerShell 5.1; terminal **not elevated**. All runtime files belonged to `tests/windows/.runs/893eaa3686e842f48dc9144fbfd539ba` inside the assigned worktree. Fixtures are small .NET loopback TCP servers, not Manager/core production binaries. Service/admin doubles are scoped to the test process. No SCM service was installed, stopped or started; no real credentials or production configuration were read; no request was sent to 8317.

Final verification command:

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File tests/windows/deploy-cpamp.tests.ps1
# process exit: 0
```

The harness executes an actual, separate CLI process (not a mocked entrypoint):

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File $entry -DryRun `
  -ServiceName FakeCPAMP-SpecA -AppDir $app -PanelPath $panel `
  -ArtifactDir $artifact -BackupDir $backup `
  -HealthUrl $common.HealthUrl -ExpectMarker fixture-healthy -TimeoutSeconds 3 `
  -RehearsalDataDir (Join-Path $OutputDir rehearsal-cli) `
  -LogPath (Join-Path $logs dryrun-cli.log) -ResultPath (Join-Path $logs dryrun-cli.json)
```

These variables resolve to the worktree-owned fixture paths above and a randomly selected, non-8317 loopback port. Actual stdout and exit code are retained **unchanged** in [dryrun-cli.stdout.txt](dryrun-cli.stdout.txt). The result explicitly reports `dryRun=true`, `serviceChanged=false`.

## Actual evidence

| Scenario | Exit | Evidence |
| --- | --- | --- |
| Standalone `-File ... -DryRun` | 0 | [JSON](dryrun-cli.json), [stdout](dryrun-cli.stdout.txt) |
| All files under isolated `site` unchanged after DryRun | — | [Before/after SHA256 lists](dryrun-hashes.json): `equal=true` for exe, panel, and test config |
| Successful deployment | 0 | [deployed.json](deployed.json): service double Running, new exe/panel installed, HTTP 200 + marker |
| Damaged live-mode artifact → automatic rollback | 1 | [rolled-back.json](rolled-back.json): restored executable/panel hashes and HTTP 200 + marker |
| Restoration startup intentionally fails | 3 | [rollback-failed.json](rollback-failed.json): manual recovery step with absolute backup paths |
| All additional regression scenarios | Expected per case | [Complete stdout](test-output.txt), ending with `PASS: all isolated assertions` and process exit 0 |

The fault-injected `broken` executable is intentionally valid in the fresh rehearsal directory, but exits immediately when run from the installed `app` directory. This exercises the **post-rehearsal deployment failure** branch. A separately supplied non-PE executable is correctly rejected during rehearsal without any service/file changes; it is **not** misrepresented as an automatic rollback test.

Recorded rollback hash equality ([hash-proof.txt](hash-proof.txt)):

```text
backup=BAB3A570ED56480AD006E90FF66EF7995B83384D821C9712A6B95911D6F82A6D
restored=BAB3A570ED56480AD006E90FF66EF7995B83384D821C9712A6B95911D6F82A6D
siteDryRunAllHashesUnchanged=true
```

A separate smoke invocation of `ops/windows/deploy-cpamp.cmd -DryRun` against the same disposable fixture also passed (exit 0, `RESULT: deployed; exit=0; dryRun=True`); this checked argument forwarding and wrapper exit-code propagation without service control.

Additional assertions cover specific-manifest recovery, latest-manifest recovery with a missing executable, originally absent panel restoration, invalid executable cleanup, wrong marker, HTTP 503 with the right body marker, explicit core rehearsal on a fake port/config, manager 8317 rejection before probing, conflicting switches, locked-log and result-write-failure recovery, missing backup hash, damaged backup bytes, output/manifest overlap, panel/config overlap, running dependent services, non-admin guidance, and no dummy inherited environment/child-output leakage.

During development, new assertions exposed PS5.1 exception wrapping that lost non-2xx status codes, and a null-dependent-list handling issue; both were fixed before this final successful run. Focused review also reproduced the original logging-recovery and unstarted-process cleanup defects in memory; regression assertions now pass with the fixes.

## Boundaries / limitations

- This is **not** evidence of a real elevated WinSW or production Manager/core deployment. Production operations were explicitly prohibited and were not attempted.
- No web/Go build, UI screenshot, Docker run, production health probe, database or authentication test was performed. They are outside this script-only isolated acceptance scope.
- Recorded versions are the test executable resource versions, not application release versions.
- JSON/stdout files above are exact historical outputs, not synthetic expected results. The referenced temporary binaries/directories can be removed after retaining this evidence.
- Re-run the harness to recreate the scenarios; compiled fixture hashes vary per compilation. See [operator documentation](../../../ops/windows/README.md) for exit codes, DryRun status semantics, migration limitations, and manual recovery.
