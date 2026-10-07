# Unofficial fork native/web release

This is the opt-in native-package and management-panel release path for
**`williamxhero/CPA-Manager-Plus` only**. It does not impersonate the upstream
project or remove any upstream release rejection guards. The first candidate is
`v1.14.5-williamxhero.1`, an **unofficial fork** GitHub prerelease with
`prerelease=true` and `latest=false`.

**The built-in updater remains pointed at upstream. Fork releases require manual
upgrade.** The fork `release-info.json` uses the distinct
`cpamp-fork-native-web-release` schema, declares `updater_compatible=false`, and
uses fork version/download links. It is not an upstream updater manifest and has
no Docker or channel identity. There is no Docker publication, registry login,
Telegram delivery, or upstream update-index modification in this path.

Implementing this tooling does not authorize running a workflow, creating a tag,
publishing a release, or changing a running service. Gate 1 and Gate 2 are
separate operator approvals; the publish checkbox is not a substitute for review
of the exact Gate 2 identity.

## Approved scope: exactly 11 new files

The approved Gate 1 implementation adds only:

1. `bin/release/fork-release-contract.mjs`
2. `bin/release/build-fork-release.mjs`
3. `bin/release/publish-fork-release.mjs`
4. `.github/workflows/fork-native-web-release.yml`
5. `tests/forkReleaseContract.test.mjs`
6. `tests/forkReleasePublish.test.mjs`
7. `tests/forkReleaseWorkflow.test.mjs`
8. `docs/fork-native-web-release.md`
9. `docs/fork-release-notes/v1.14.5-williamxhero.1-zh.md`
10. `docs/fork-release-notes/v1.14.5-williamxhero.1-en.md`
11. `docs/fork-release-posts/v1.14.5-williamxhero.1-telegram.html`

The last file is retained release copy, **not permission to send an announcement**.
Existing upstream workflows, release schema/validators, and
`bin/release/package-native.sh` stay unchanged. No real configuration, SQLite
files or sidecars, `data.key`, administrator keys, credentials, or runtime data
may enter the build tree, artifact, or published payload.

## Provenance and branch phases

The frozen feature baseline is
`233a225fe9e13375670e1bb93d9ad76a04b99c84`: provider aliases, OpenCode Go quota,
credential deduplication, and OpenAI key connectivity checks without model usage.
Integrate the test-only follow-up `da20f829` as well (collapsed OpenCode Go account
refresh coverage). The receipt records the feature baseline as
`base_source_commit`; it is **not** the final tag target.

The earlier verification packages built from `233a225f` with `VERSION=dev` are
verification-only. They are not publish payloads and cannot stand in for an
approved final-main dry-run.

After Gate 1 approval, follow these phases without a preliminary main promotion:

1. `feat/fork-native-web-release` → fork `dev`: integrate the frozen feature and
   test baseline and the eight tooling/test/process files. Preserve existing
   branches and dirty developer workspaces; do not force-push, reset, or clean
   them. Read current remote refs before each authorized operation, not just the
   historical SHAs in the plan.
2. `release/v1.14.5-williamxhero.1` → fork `dev`: add **exactly the three versioned
   note/post files** above, then merge the release PR with a merge commit (not
   squash or rebase). Record and freeze that exact resulting dev SHA.
3. **Once only**, promote frozen fork `dev` → fork `main` using a same-repository
   PR and merge commit. Record the previous main, frozen dev, full promotion
   range, related integration/release PRs, reviewed head/base SHAs, and resulting
   main SHA. Stop for ref drift, unmapped PRs, failed required checks, or enlarged
   scope; do not overwrite another contributor's work.
4. From a clean final `main`, run an explicitly authorized dry-run for the exact
   final main SHA and frozen dev SHA. Main must still point at the expected
   source, and dev must still be frozen. Use the app's PR status/monitoring tools
   for PR checks rather than periodic polling.
5. Only after the entire dry-run succeeds, seek Gate 2 approval for the exact
   tag → final main SHA, frozen dev SHA, dry-run run ID **and attempt**, receipt
   SHA256, artifact ID/digest, and all nine asset names/sizes/digests.

Tooling, versioned notes, and the promotion merge change the final source SHA.
Binaries, fork manifest, receipt, and tag must bind to that final main commit
and source tree, not merely to the original feature baseline.

## Dispatch contract

The workflow is **`workflow_dispatch` only**. It has no push/tag, schedule,
workflow-run, or pull-request release trigger. Its run name is:

```text
Fork native/web <mode> <version> @ <expected_sha>
```

Dispatch from `main` in `williamxhero/CPA-Manager-Plus`. The authorization job
always executes and fails invalid dispatches rather than silently skipping them.
It rejects a wrong repository, non-main branch/ref, an actual `GITHUB_SHA` that
differs from `expected_sha`, absent explicit opt-in, unsupported mode, or missing
publish confirmation/approved receipt identity. Scripts consume inputs through
environment variables and quoted arguments, never interpolated shell source.

| Input             | Dry-run                              | Publish                                 |
| ----------------- | ------------------------------------ | --------------------------------------- |
| `opt_in`          | Explicit `true`; default `false`     | Explicit `true`; default `false`        |
| `mode`            | `dry-run` (default)                  | `publish`                               |
| `version`         | `v1.14.5-williamxhero.1` by default  | Exact approved dry-run tag              |
| `expected_sha`    | Required full final main SHA         | Same approved full main SHA             |
| `frozen_dev_sha`  | Required full frozen release dev SHA | Same approved dev SHA                   |
| `dry_run_id`      | Leave empty                          | Required approved successful dry-run ID |
| `dry_run_attempt` | Leave empty                          | Required approved exact attempt         |
| `receipt_sha`     | Leave empty                          | Required raw lowercase receipt SHA256   |
| `confirm_publish` | `false` (default)                    | Explicit `true` after Gate 2            |

Concurrency is serialized per version and does not cancel an in-progress run.
Default token permissions are `contents: read` and `actions: read`; **only** the
publish job receives `contents: write`. No runtime secrets are required.

### Dry-run: build once and review

The Ubuntu job checks out the exact expected SHA with complete history, fetches
main/dev, and uses Node 22 and Go 1.24. After `npm ci`, the fork builder runs the
type/lint, web and repository tests, full backend tests, and CGO-disabled build
checks. It builds a fresh management panel, verifies isolation and icons, and
uses the unchanged native packager with a temporary source tree containing the
fresh embedded HTML/icons.

The immutable Actions artifact is named:

```text
fork-native-web-<version>-<expected_sha>-attempt-<GITHUB_RUN_ATTEMPT>
```

It is uploaded with artifact v4, `overwrite: false`, and 30-day retention. Its
root contains exactly `payload/` and `receipt.json`. The payload contains exactly
nine publishable assets:

- `cpa-manager-plus_<tag>_linux_amd64.tar.gz`
- `cpa-manager-plus_<tag>_linux_arm64.tar.gz`
- `cpa-manager-plus_<tag>_darwin_amd64.tar.gz`
- `cpa-manager-plus_<tag>_darwin_arm64.tar.gz`
- `cpa-manager-plus_<tag>_windows_amd64.zip`
- `cpa-manager-plus_<tag>_windows_arm64.zip`
- `checksums.txt`
- `management.html`
- `release-info.json`

`receipt.json` is **not** a tenth release asset. It binds the repository, fork
schema, dry-run mode, tag, final source commit/tree, base feature SHA, frozen
main/dev refs, run ID/attempt, content digests, completed checks, toolchain, and
all nine asset sizes and `sha256:<hex>` digests. The job summary records the
receipt's raw SHA256 and the Actions artifact ID/digest. These are review
identities, not approval to publish.

The dependent Windows job downloads the exact artifact ID from this same run,
validates its root layout, receipt, and payload with the fork contract helper,
and expands only the Windows amd64 archive into `RUNNER_TEMP`. It runs only
`cpa-manager-plus.exe --version`, requiring the exact tag, and checks the binary
contains the exact published `management.html` bytes and source SHA. It then
runs the existing `go test ./internal/repository/sqlite` compatibility gate.
There is no server start, installer, service registration/control, real-host
configuration access, or production data migration.

A successful Ubuntu builder alone is not a successful dry-run: Windows smoke
must pass too. Windows failures must be compared with the clean upstream
`166262365d506157b99968105c890f480cc7c07c` baseline and documented, not hidden by
weakening, skipping, or deleting tests. The scoped CI gate complements, rather
than replaces, Gate 1's isolated Linux backend/repository verification and
review of embedded UI behavior and existing-data compatibility.

### Gate 2: reuse the approved artifact, never rebuild

Submit this exact mapping and evidence to the operator:

```text
refs/tags/v1.14.5-williamxhero.1 -> <validated final main SHA>
frozen dev: <exact release dev SHA>
dry-run: <run ID>, attempt <attempt>
receipt SHA256: <raw hex>
artifact: <ID>, <name>, <digest>
assets: <all nine names, sizes, SHA256 digests>
```

Only after this approval, dispatch `mode=publish` from that same main SHA with
all approved fields and both opt-in and publish confirmation enabled.

The publisher first performs a **read-only** preflight. It checks the expected
workflow path, repository/source, successful `dry-run` mode, exact run attempt,
and matching artifact, and returns `artifact_id`/`artifact_name` through
`GITHUB_OUTPUT`. The workflow downloads **that exact artifact ID from that exact
approved run**, not the newest artifact or a name-only approximation.

The full publisher receives the downloaded `payload/` and root `receipt.json`.
It does not install workspace dependencies, rebuild the panel, or repackage
binaries. It revalidates the approved receipt hash, all payload hashes and
source/frozen-ref drift, and rejects existing tag/release conflicts before
mutation. It creates and rereads the exact lightweight tag, uploads the nine
assets to a draft, and verifies the draft uploads before publishing with
`prerelease=true` and `latest=false`. No upstream release workflow is invoked.

## Failure and recovery boundary

Stop on provenance mismatch, expired/missing artifacts, SHA or frozen-ref drift,
failed gates, asset mismatch, or tag/release conflicts. **Do not automatically
rerun publish, delete a draft/release/tag, move or overwrite a tag, or reconstruct
an expired artifact under the old approval.** An existing tag or release requires
separate operator review and explicit recovery approval. A new dry-run or attempt
has a new receipt/artifact identity and requires renewed Gate 2 approval.

Record completed mutations and leave failed drafts/tags intact for review.
This workflow is not the upstream recovery path and does not authorize using
upstream registry/Telegram/update-index recovery commands for fork artifacts.

Manual backend installation is a separate approved operation. Preserve and back
up configuration, SQLite/sidecars, `data.key`, and administrator keys before any
approved upgrade. Local service changes must be confined to the approved
`LocalCPAManagerPlus` instance, with normal user authorization for Windows
elevation. Do not terminate protected processes, change security settings, touch
production containers, or operate yosef-server as part of this release workflow.
The already-deployed static-panel hotfix does not establish completion of the
final backend release.
