# Linux deployment acceptance (#62 / #65)

These scripts default to the existing yosef-server layout. **Do not run the
production defaults to run tests.** This change has not been live deployed.
The accepted core target is **8.0.15**, not the historical 8.0.13 issue wording.

## Isolated acceptance

From the repository root, using local Docker Desktop/Linux:

```sh
docker build -t cpamp-deploy-tests:acceptance -f ops/linux/tests/Dockerfile ops/linux/tests
docker run --rm --network none \
  --mount "type=bind,source=$(pwd)/ops/linux,target=/src,readonly" \
  cpamp-deploy-tests:acceptance
```

On PowerShell, replace `$(pwd)/ops/linux` with the absolute worktree's Linux
scripts directory (for example `D:/WILL/AGENT/CPA/cpamp-deploy-acceptance/ops/linux`).
Image construction downloads development tools; test execution has **no network**
except the container's own loopback, no published host ports, and mounts **only
source/tests read-only**. Tests refuse to execute without the container guard.
No real configuration, credentials, host systemd, SSH service, CPA runtime, or
8317 host listener is used. All service ports are dynamically selected inside
the container. The test image pins its base image digest.

The public seam is each actual deployment CLI. Fake SSH/SCP execute the actual
remote commands in the container. Fake systemd starts real, distinct synthetic
ELF executables with PID, listening socket and HTTP responses. Filesystem
operations are real; command wrappers inject failures only at OS boundaries.
Passing assertions require restored bytes/pointers and an actually responding
old process, not just fake `systemctl` success. This is not real plugin ABI or
production-service acceptance.

Checks without writing bytecode into the source mount:

```sh
docker run --rm --network none \
  --mount "type=bind,source=$(pwd)/ops/linux,target=/src,readonly" \
  cpamp-deploy-tests:acceptance bash -c \
  'bash -n deploy-core-yosef.sh deploy-cpamp.sh && python -c '\''import ast,pathlib; [ast.parse(p.read_text()) for p in pathlib.Path(".").rglob("*.py")]'\'''
```

## Operational contract

Prerequisites: Bash, Linux `/proc`, Python **3** standard library, normal GNU
filesystem tools, user systemd, and core-side util-linux `flock`. Keep
`deploy-check.py` next to both shell scripts. Manager uses local Python3 and
passes the checker to remote Python3 through SSH; it needs no remote helper
installation. The service's **MainPID must be the foreground deployed binary**,
not a shell wrapper. Health URLs must be local HTTP loopback endpoints owned by
that process. Socket/executable ownership is checked before the response and
executable identity rechecked afterward. HTTP redirects and environment proxies
are disabled: success must come from that listener. Core accepts 200/401,
Manager requires 200 plus its service marker.

Core retains `-DryRun`, `-NoRestart`, `--dry-run`, `--no-restart` and its 8.0.15
default. Explicit isolation flags are documented in its `--help`: root, staged
config, service, OAuth directory, quota command, health URL, rehearsal port,
timeout and result path. No-restart returns `installed_not_restarted`, never a
claim of verified runtime health. No secrets/config fragments are logged; use
result `failurePhase` and `steps` for sanitized diagnostics.

Core rehearsal copies plugins into a private directory, redirects the supported
block-style legacy/new YAML listen/auth/plugin paths, disables quota helper
execution, and actually starts the new binary. The binary is the configuration
syntax validator; `--help` is not. Ambiguous port/plugin layouts (including
inline plugin directory mappings) fail closed. This is a deliberately narrow
adapter for existing configuration formats, **not a general YAML converter**.
Rehearsal proves startup/health, not equality of provider/credential semantics.
It preserves staged inline API/provider/management key values in private scratch;
it is **not a credential sanitizer**. Only synthetic configurations are used or
authorized for this task. Any later invocation with real staged credentials
requires its own authorization. Real binary/plugin compatibility and host-only
settings still require separate acceptance. Never point a rehearsal configuration at independently writable host
resources; extend the adapter and tests first for any new path-bearing schema.

Manager retains the existing SSH/artifact/version flags and adds a configurable
health timeout. Rehearsal uses a unique private tree, and cleanup kills/waits
only for its child PID. It never kills a port owner or uses `pkill`/`fuser`.
Manager requires a **fresh version label**: existing app/panel directories are
not overwritten, protecting running releases and rollback targets. Retry using
a new label or inspect the retained partial release; do not automatically delete
an old release. A per-root lock rejects overlapping mutating Manager runs; core
locks the existing root inode. Stale Manager locks after a controller crash
require operator inspection, not automatic deletion.

Backups are private, per-run directories. Symlinked plugin directories/files
are rejected before core rehearsal/backup, so copied plugins cannot alias a live
external tree. Core snapshots old release/config and the entire plugin tree, then restores pointer/config/plugins and verifies them
before restart/health. A failed plugin tree is retained under a run-specific
name for diagnosis; it is not treated as the restored tree. Manager snapshots
app/panel destinations plus exact release-text content or its original absence.
Each pointer replacement is atomic; the multi-pointer operation is not a single
filesystem transaction, so partial-switch errors trigger complete restoration.
Manager rollback-only needs no deployment artifacts and selects only completed
backups. Legacy backups without the new completeness/release-text contract are
not eligible for trusted rollback-only. Originals must still match their backup
copies; unexpected changes fail closed rather than overwriting unknown data.

`--dry-run` performs real isolated startup and leaves no persistent deployment
or service changes. Its unique temporary tree is removed. A requested result
file is the deliberate diagnostic output; place it **outside** the deployment
root when comparing tree snapshots. SIGKILL/power failure is not recoverable by
shell traps; private scratch/locks may remain and need inspection. Partial
backups/releases are retained rather than destructively cleaned up.

Statuses / exits:

| Status | Exit | Meaning |
|---|---:|---|
| `deployed` | 0 | Target-owned health verified; with `dryRun=true`, rehearsal only |
| `installed_not_restarted` | 0 | Core files installed; running service not verified |
| `preflight_failed` | 2 | Preflight/rehearsal/backup aborted before mutation |
| `rolled_back` | 3 | Deployment failed; old state and service health verified |
| `rolled_back` (explicit Manager rollback-only) | 0 | Requested restoration verified |
| `rollback_failed` | 4 | Recovery write/restart/health verification failed |
| `result_write_failed` | 5 | Requested result output could not be written |

Machine-readable result JSON includes times/duration, target/service/status,
version, before/after pointer state, steps, health and backup path. Core also
retains `stamp` and `configSha`. Input-derived strings are JSON-encoded rather
than interpolated into JSON text. Result write failure is explicit, not a
successful deployment report.

## Evidence and independent review

The supplement in `deploy-acceptance-spec.md` is the payload for **existing**
issues #62/#65. GitHub comment/label publication was rejected by the classifier;
Hermes should publish the payload through its authorized channel, not create a
new SPEC. `/to-spec` was dispatched; implementation followed the phase skill
read under user-supplied workflow 3.0.0 after slash dispatch was unavailable.
All work stays in this worktree and source writer.

Observed red regressions before the fixes:

- Original core: injected restart exit 1 + arbitrary HTTP 401 still yielded
  `RESULT: deployed`, exit 0. Initial restart gating then exposed false recovery
  success and the overwritten pointer-metadata path.
- Original Manager: health-failure recovery restored executable/panel pointers
  but release-text assertion failed (`new\n` versus expected `old\n`).
- Further failure injection caught run-specific temporary symlinks blocking
  recovery after a failed move, and unsanitized command stderr. Those paths are
  covered in the retained regression suite.

The current suite covers positive, failure, recovery-failure and isolation
contracts. Hermes owns **independent acceptance**, integration and any separately
authorized live deployment. This task performs no live deployment, merge,
release or issue closure, and proves no real no-interruption window.
