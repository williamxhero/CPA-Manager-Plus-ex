# #62 / #65 review handoff

## Publication state

Implementation and isolated tests exist on local branch
`claude/deploy-acceptance-fixes` in `cpamp-deploy-acceptance`. No live deployment,
merge, release or issue closure has occurred. Hermes remains the independent
acceptance/integration owner.

- Commenting on original issues and adding `ready-for-agent` was rejected by the
  auto-mode classifier as an external write. No comments/labels were changed.
- Pushing `git push -u fork claude/deploy-acceptance-fixes` was rejected by the
  classifier as creating a public surface. No push was performed. Do not retry
  this through another tool/session to bypass that denial.
- The verified `fork` remote is
  `https://github.com/williamxhero/CPA-Manager-Plus-ex.git`.
- `CONTRIBUTING.md` requires PRs to the fork's `dev`, but the GitHub branches API
  lists only `main`; `dev` lookup returns 404. No integration branch was created,
  no PR was opened against main, and no PR was opened to the unrelated upstream.
  Restore/choose an authorized valid integration target before creating a PR.

The full issue supplement payload is `deploy-acceptance-spec.md`. Publish it to
**existing** issues #62 and #65 and use the existing `ready-for-agent` label
through an authorized channel; do not create unrelated SPECs or close issues.
The candidate is for **request_review**, not an independent-acceptance or
production-complete claim. Read the exact final commit and run the full Docker
suite before integration; no issue closure keywords are needed in the PR.

## Proposed draft PR payload

Title: `fix(deploy): verify Linux deployment failures and complete rollback`

### Summary

Harden Linux Manager/core deployment and provide isolated failure-injection
proof for existing SPEC #62/#65. Backups, configuration, plugin trees and all
version pointers are checked through deployment and recovery. Health requires
the intended process and listening socket, not a successful fake restart or an
unrelated 401/200 endpoint.

### Scope

- [ ] Frontend panel
- [ ] Manager Server
- [ ] CPA panel mode
- [ ] Full Docker mode
- [x] Native packages / release — Linux deployment scripts only, no release
- [x] Docs / Wiki — colocated deployment specification/evidence
- [x] CI / build / tooling — isolated test image/harness only

### Changes

- Fail closed on backup/install/replace/switch/restart failures; distinguish
  verified rollback from rollback failure.
- Restore core pointer/config/full plugins and Manager app/panel/release-text
  state, including original absence. Completed-backup rollback-only works
  without deployment artifacts.
- Private startup rehearsal, own-child cleanup, process/socket-owned health,
  explicit isolation flags, safe JSON and sanitized error outcomes.
- Preserve the accepted **8.0.15** target and legacy core dry-run/no-restart flags.

### User Impact

No application/UI changes. Deployment scripts now reject unsafe or ambiguous
success/recovery cases rather than silently accepting them.

### Compatibility / Runtime Notes

- CPA panel mode: unchanged.
- Manager Server mode: unchanged application, stronger deployment checks.
- Native packages: scripts require colocated `deploy-check.py`, Python3,
  foreground systemd MainPID, loopback HTTP health and core-side flock.
- Manager needs a fresh immutable version label and a completed new-format
  backup for rollback-only. Existing default layout/service/port syntax stays.
- Core no-restart reports `installed_not_restarted`, not verified runtime health.
- No new production third-party package; Python helper uses only stdlib.

### Data / Security Notes

No real configurations/credentials were read. Tests use synthetic canaries.
Container tests have no published ports/network, mount only source read-only,
and use fake SSH/systemd boundaries with real synthetic ELF listeners. They do
not validate real plugin ABI or actual host credentials. No pkill/fuser,
kill-by-port, deployment, database writes or production infrastructure changes.

### Risk / Rollback

Risk level: Medium — deployment control/recovery paths. Backups and partial
releases are retained; originals must still match their backup for trusted
restoration. Inspect leftovers after SIGKILL/power loss rather than auto-delete.
Hermes independently verifies the final candidate; live rollout needs separate
authorization and evidence.

### Verification

- [x] Tests — full isolated CLI regression suite (see specification/results)
- [x] Shell/Python syntax checks
- [ ] Full frontend/Go test suites — not run; application code unchanged
- [ ] Manual UI check — not applicable
- [ ] Live deployment/failure-injection — explicitly forbidden in this task

```text
docker build -t cpamp-deploy-tests:acceptance -f ops/linux/tests/Dockerfile ops/linux/tests
docker run --rm --network none --mount type=bind,source=<worktree>/ops/linux,target=/src,readonly cpamp-deploy-tests:acceptance
```

Red evidence includes original core restart failure reported as deployed and
original Manager rollback leaving new release text behind. Subsequent tests
exercise failed recovery, foreign 200/401 responders, occupied rehearsal port,
plugin cleanup, missing artifacts, dry-run snapshots and sanitized diagnostics.

### Screenshots / Recordings

N/A — no UI change.

### Docs

- [x] Colocated Linux deployment README/specification/evidence updated
- [ ] Main README/manual/release notes — not needed for scoped ops-only change

### Related

Refs #62 and #65. Do not close either issue on this implementation's evidence.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
