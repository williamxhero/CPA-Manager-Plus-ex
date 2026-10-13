# Windows deployment with automatic rollback (SPEC-A)

Specification: [#52](https://github.com/williamxhero/CPA-Manager-Plus-ex/issues/52).

`deploy-cpamp.ps1` is a standalone Windows PowerShell **5.1** script using only Windows/.NET APIs. It does not call an Agent, Go, Docker, a package manager, a proxy, or any build tool. Prepare artifacts separately; there is intentionally no implicit build/download step.

## Before running

- The existing Windows service must already be configured to run the executable in `AppDir` and use the selected `PanelPath`. This script **does not install/reconfigure WinSW or read/edit live configuration or credentials**. Check these paths yourself before production use.
- Default target/service: `LocalCPAManagerPlus`; executable: `cpa-manager-plus.exe`; app: `D:\WILL\AGENT\CPA\CPA-Manager-Plus\app`; panel: `AppDir\management.html`; health: `http://127.0.0.1:18317/health`; expected marker: `"ok":true`.
- Put `cpa-manager-plus.exe` and, optionally, `management.html` in `ops\windows\artifacts`, or specify `-ArtifactDir`. Omitting the panel preserves the installed panel (or its absence).
- Logs default to the app parent's `deploy-logs` directory; backups to its `deploy-backups` directory. The default JSON filename is the log filename with `.json` instead of `.log`.
- Real deployment/recovery requires **Run as Administrator**. `DryRun` can run unelevated/offline without an installed SCM service; it does not prove service configuration, permissions, or the live database's migration compatibility.
- Choose a stable `ExpectMarker` present in **both** the new and old versions. A version-specific new marker would prevent successful health validation after restoring an old version. Use a panel URL/marker when panel rendering is part of your acceptance criteria.

## Running

Double-click `deploy-cpamp.cmd` for default parameters (it pauses to keep the result visible). It does not auto-elevate: an unelevated real deployment aborts with clear instructions. Right-click the `.cmd` and select **Run as Administrator** when ready. For Task Scheduler, run `powershell.exe -NoProfile -ExecutionPolicy Bypass -File "<absolute-path>\deploy-cpamp.ps1" ...`, with highest privileges and the appropriate explicit paths.

A rehearsal-only example, using an **operator-created isolated installation**, not a live service:

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\ops\windows\deploy-cpamp.ps1 `
  -DryRun -ServiceName FakeCPAMP `
  -AppDir C:\cpamp-sandbox\site\app `
  -PanelPath C:\cpamp-sandbox\site\app\management.html `
  -ArtifactDir C:\cpamp-sandbox\artifact `
  -BackupDir C:\cpamp-sandbox\backups `
  -HealthUrl http://127.0.0.1:28318/health `
  -ExpectMarker '"ok":true' `
  -LogPath C:\cpamp-sandbox\logs\dryrun.log `
  -ResultPath C:\cpamp-sandbox\logs\dryrun.json
```

For deployment, remove `-DryRun` and use the actual, independently verified service/path/port parameters. Never use a fake service name expecting it to work: only the test harness supplies service doubles.

Independent recovery (no artifact or rehearsal required):

```powershell
# Latest matching timestamped manifest in BackupDir:
.\ops\windows\deploy-cpamp.ps1 -RollbackOnly -AppDir <app> -PanelPath <panel> -BackupDir <backups>
# Or a specific manifest:
.\ops\windows\deploy-cpamp.ps1 -RollbackOnly -AppDir <app> -PanelPath <panel> `
  -BackupDir <backups> -BackupManifest <absolute-path-to-timestamp.manifest.json>
```

Include `ServiceName`, `HealthUrl`, `ExpectMarker` overrides when the installation uses nondefaults. A backup is accepted only when its target, service, destinations, SHA256 hashes, and contained backup files match. A missing installed executable can be repaired by `RollbackOnly`. If the panel originally did not exist, rollback removes the panel introduced by deployment.

### Explicit core opt-in

`-Target LocalCPA` is mandatory for the core service or port 8317. Its executable/artifact name is `cli-proxy-api.exe`; conventional default app is `D:\WILL\AGENT\CPA\CLIProxyAPI\app`, health is `http://127.0.0.1:8317/`, marker is `CLI Proxy API`. **Override AppDir/PanelPath to match your own core installation**; this repository does not supply a core WinSW configuration.

Core rehearsal uses CLIProxyAPI's `-config` CLI contract with a freshly generated configuration, temporary auth directory, panel downloads disabled, and no inherited `.env`. Manager mode rejects the known core installation paths, core service name, and port 8317. Rehearsals never use port 8317, including core rehearsals. No real core binary/service was exercised during this change's verification.

## Parameters and constraints

| Parameter | Meaning |
| --- | --- |
| `Target` | `LocalCPAManagerPlus` (default), or explicit `LocalCPA` |
| `ServiceName` | Defaults to target; real deployment requires an existing Running service |
| `AppDir`, `PanelPath` | Executable directory and `management.html` destination (not another runtime/config file); panel parent must exist |
| `ArtifactDir` | Prepared executable and optional `management.html` |
| `BackupDir` | Timestamped binary/panel backups and matching manifests |
| `BackupManifest` | Optional specific manifest for `RollbackOnly` |
| `HealthUrl` | Loopback HTTP(S), no user information, query, or fragment; redirects/proxies/default credentials disabled |
| `ExpectMarker` | Case-sensitive literal substring, required together with HTTP **200**; never logged |
| `LogPath`, `ResultPath` | `.log` and `.json` outputs outside runtime/artifact/backup/rehearsal paths |
| `TimeoutSeconds` | Default 60; range 1–3600; applies separately to service state waits and each health phase |
| `RehearsalPort` | Default 0: select an available loopback port; cannot equal live port or 8317 |
| `RehearsalDataDir` | Must not exist; otherwise a new UUID temporary directory is used and removed |
| `DryRun` | Only preflight + rehearsal; no backup snapshot, service control, or deployment |
| `RollbackOnly` | Restore only; cannot be combined with `DryRun` |

Paths through symlinks/junctions, overlapping output/input destinations, and running dependent services are rejected. Calls targeting the same installed executable are serialized through a stable exclusive lock file (the zero-content lock file remains for safe reuse). Normal deploy also verifies the installation has not changed since backup, rechecks backup hashes before stopping, and verifies replacement hashes.

## Isolation, logging and result semantics

Flow: **preflight → backup → rehearsal → stop → replace → start → health → automatic recovery if needed → result**. Backup/rehearsal failures abort before service control. Any deployment failure, including log/result persistence failure, attempts recovery. Logging failures cannot short-circuit recovery.

Manager rehearsal copies the executable/panel into a fresh, current-user-only directory, uses an **environment allowlist**, explicitly sets separate config/database/data-key paths, directs secret-file lookups to nonexistent paths, disables update checks/automated account actions, and has no live core connection. Startup stdout/stderr are drained and **discarded**, because fresh Manager startup can generate/print a new local admin key. Rehearsal state is removed after terminating the owned process. If termination cannot be confirmed, deployment aborts and the temporary directory is left for manual investigation.

No HTTP body, expected marker, response header, URL, inherited environment value, service/process exception text, real credential, cookie, token or management key is logged. Fixed error codes become readable fixed explanations; unknown diagnostics are suppressed. Result JSON contains timestamps, duration, target/service/status, `before`/`after` paths/hashes/version, `steps`, `health`, backup metadata, and mode flags. Version is read from the executable's Windows file resource **without executing it**; it is `null` when the artifact has no version resource.

The status vocabulary follows SPEC-A. Successful rehearsal-only runs use `status: "deployed"` **with `dryRun: true` and `serviceChanged: false`**; this means validation passed, **not** that deployment happened. Consumers must check `dryRun` before interpreting `deployed`.

| Exit | Result |
| --- | --- |
| 0 | Healthy deployment, successful DryRun, or successful requested RollbackOnly |
| 1 | Deployment failed; automatic rollback restored hashes and health |
| 2 | Preflight/rehearsal aborted without service changes (also unrecoverable result-output failure) |
| 3 | Rollback failed; manual guidance includes selected service, destination and absolute backup paths |

Unsafe output paths are rejected **without writing to them**; invalid/unwritable output locations can therefore prevent result JSON creation. Disk exhaustion/permissions can also prevent final result/log persistence, but recovery is still attempted and instructions/result are printed to the console. Persisted backup manifests are the fallback for manual recovery.

This is executable/panel rollback, **not a database/auth/config rollback**. The script does not read/copy real credentials or database files. New binaries can still migrate a live database when the real service starts; review release compatibility and follow your separate data-backup procedure. A fresh-data rehearsal cannot prove old-data compatibility. Hard process termination/power loss during deployment cannot run in-process recovery; use `RollbackOnly` or the printed/manual backup manifest procedure afterward. Keep backup directories protected from concurrent external modification.

## Verification (no production operations)

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File tests\windows\deploy-cpamp.tests.ps1
```

The pure-script assertion harness compiles small C# TCP fixtures using Windows/.NET `Add-Type` (test-only; no Go/Docker/Pester dependency), imports entrypoint **function ASTs without its CLI exit**, and replaces administrator/service APIs only in its own test process. No Windows service is installed/stopped/started. Each call asserts the fake service name and a randomly selected non-8317 port. The actual standalone `-File ... -DryRun` command also runs without these mocks.

The harness checks PS5.1 parsing, actual DryRun output/exit and **every file hash in the isolated site tree**, deployment/panel replacement, independent latest/specific rollback, damaged live-mode artifact auto-recovery, invalid executable rehearsal abort, marker mismatch, rollback failure/manual instructions, core-port block, contradictory modes, log/result I/O failure recovery, malformed/corrupted backup rejection, output/manifest overlap, missing executable recovery, originally absent panel restoration, and non-admin guidance. It also verifies inherited dummy sentinels and process output never appear in logs/results. Evidence is under `tests/windows/.runs/<uuid>/logs` (git-ignored).
