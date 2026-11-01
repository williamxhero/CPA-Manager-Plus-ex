# SPEC92 review blocker fixes

Date: 2026-11-01
Base: `1b3d4f069f750be6ba4ff94c202538440d36ef06`

## Blocker 1: legacy global quota authority removed

- `AutomationRuntime` no longer starts, enables, updates, or dispatches usage events to the legacy quota or Codex reset workers.
- `codexQuotaCooldownEnabled` remains accepted and persisted for API compatibility, but status reports `enabled=false`, `configured=false`, `source=legacy-inert`, and `disabledReason=per_credential_policy_only`.
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

No push, merge, deploy, runtime change, or issue closure was performed. Rendered UI acceptance remains a separate known SPEC frontier as stated by the review.
