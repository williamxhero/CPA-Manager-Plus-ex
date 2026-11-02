# SPEC92 review blocker fixes

Date: 2026-11-01
Base: `1b3d4f069f750be6ba4ff94c202538440d36ef06`

## Blocker 1: legacy global quota authority removed

- `AutomationRuntime` no longer starts, enables, updates, or dispatches usage events to the legacy quota or Codex reset workers.
- `codexQuotaCooldownEnabled` is no longer accepted by the account-processing-policy API and is no longer persisted or used as a runtime control. The per-credential policy is the only automatic quota authority.
- Auth-health account actions remain wired and gated independently.
- The existing #76/#77 full-refresh scheduler/service was not deleted or changed into the legacy path.
- Focused tests prove a global PATCH cannot activate legacy quota handling and that account actions still operate.

## Blocker 2: full-refresh source binding

- `refreshCredentialSource` now requires the issued scope, exact targets, auth index, and sourceVersion.
- Inventory scope and the credential source request are checked before the POST; a same-index replacement, changed target/version, changed sourceVersion, or stale Manager incarnation/source reuse is rejected without starting a job.
- Credential policy toggle refresh, credential quota panel refresh, and AccountsPage row refresh all pass the current issued binding. AccountsPage also cross-checks the current host-issued protection binding before POST.
- Focused RED/GREEN coverage covers same-auth-index replacement and stale manager incarnation/source reuse.

## Verification

- `go test ./internal/service/automation ./internal/httpapi ./internal/worker ./internal/service/fullrefresh` — PASS
- `npm --workspace apps/web exec vitest run src/services/api/fullRefresh.test.ts src/features/accounts/components/CredentialAutoStartStopToggle.test.tsx` — PASS (10 tests)
- `npm run type-check` — PASS
- `npm run lint` — PASS with pre-existing warnings only
- `npm run build` — PASS
- `npm --workspace apps/web exec vitest run src/features/accounts/AccountsPage.test.tsx -t 'ticket93 capable refresh uses common76'` — PASS (the repository test invocation matched the suite; 409 tests passed)

Postfix verification:

- `git diff --check` — PASS
- Prettier check for all changed web files — PASS
- Final Manager artifact was built from source head `98ae82a177d9ae0db42028d691b6df396216958a`
- The automated local deployment script completed successfully with backup and
  rollback receipt; Manager, CPA, and Usage Keeper health checks passed.
- The deployed Manager executable SHA-256 is
  `302d30e4f3ceb295f7764157f5ae60e49adf7e12812ca1b35d63963cc755371`.

Known non-blocking regression baseline:

- Web full suite: 5103 passed, 1 failed, 4 skipped. The single failure is the
  existing DST fall-back duplicate-local-hour baseline in
  `src/features/usage-analytics/usageAnalyticsModel.test.ts`.
- Full Go suite still contains Windows/environment baseline failures
  (`not supported by windows`, SQLite temporary-file locking, file-mode
  expectations, and snapshot/archive access denied). These are recorded rather
  than represented as feature regressions.

No push, merge, or issue closure was performed. The local runtime deployment is
complete; rendered post-login UI acceptance remains a separate SPEC frontier
because the local management key was not entered into the browser session.
