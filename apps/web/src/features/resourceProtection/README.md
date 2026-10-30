# Bounded resource capability support pass (issue 70)

This frontend consumes the manager-only read-only
`GET /usage-service/resource-capabilities` support summary. It does not consume
an inventory, resource/lifecycle identity, scope/version precondition, routing
status or enforcement ACK.

## Deliberate limits

- The only display states are **unknown** and **manager integration unavailable**.
- Unavailable requires the recognized schema-1 summary, including the explicit
  `independentGate: unavailable / consumer_contract_unconfigured` pair and valid
  identity, query and existing policy fields. It does not mean the deployed core
  is unsupported.
- Loading, unrecognized/malformed payloads, missing fields, legacy 404, transport
  errors and authentication errors remain unknown. A global `available` flag
  also remains unknown: it cannot prove protection for a particular resource.
- Policy configuration/locks, manual disabled state and wildcard exclusions are
  not independent protection evidence. Existing manual and quota behavior is
  unchanged. No raw API errors, credentials or response reasons reach the tooltip.
- Each representative page owns one loader; rows only render the same manager
  support explanation. Requests are fenced by API target, manager base, management
  key, usage-service revision and monotonically increasing request generation.
  Refresh invalidates previous evidence immediately. Cleanup invalidates late
  completions even when the transport ignores cancellation.
- There is no polling, persistent evidence cache, automation write, provider probe
  or quota probe. Demo support comes only from local simulated unknown/unavailable
  fixtures behind both demo guards.

The positive **normal/protected** path is intentionally deferred until verified
persistent instance/lifecycle identities and an independently owned, bound,
versioned gate/consumer contract exist. Inventory, binding/version logic, actual
routing/enforcement and full T01/Hermes acceptance remain open. Existing
`CredentialAutoStartStopToggle` remains a policy editor, not an ACK.

## Actual TDD and verification evidence (2026-10-25)

Commands below were run from the repository root. The standard Vite config could
not start in this restricted Windows environment (`spawn EPERM` in Vite's Windows
realpath helper); the runner-loader alternative also failed (`require is not defined`). A temporary native-loader configuration supplied the existing `@`
alias, test demo constants, worker threads and CSS-module proxies for Node tests.
It did not open a browser or listen as an application server. The temporary
configuration and raw logs were removed after verification.

Initial behavior reds, with the temporary configuration:

```powershell
npm --workspace apps/web run test -- src/services/api/usageService.resourceCapabilities.test.ts --config resource-capabilities.vitest.config.mjs --configLoader native
```

Result: **3 failed**, because `getResourceCapabilities` did not exist. After the
transport/fixture slice: **3 passed**. Further observed red slices were the missing
ProviderTable indicator (1 failure), missing Accounts indicators in table/grid
(2 failures), missing tooltip click support (1 failure), and malformed policy
metadata incorrectly displaying unavailable instead of unknown (1 failure).
Each was followed by implementation and passing focused tests.

Final green command:

```powershell
npm --workspace apps/web run test -- src/features/accounts/AccountsPage.test.tsx src/components/providers/ProviderTable/ProviderTable.test.tsx src/components/ui/InfoTooltip.test.tsx src/features/aiProviders/AiProvidersPage.test.tsx src/services/api/usageService.resourceCapabilities.test.ts src/features/accounts/components/CredentialAutoStartStopToggle.test.tsx src/features/accounts/components/credentialAutoStartStopI18n.test.ts src/features/resourceProtection --config resource-capabilities.vitest.config.mjs --configLoader native
```

Result: **8 files passed, 451 tests passed** (25.53 seconds). Coverage includes
loading/error/schema rejection, global-available rejection, explicit unconfigured
integration, A-to-B-to-A, overlapping refreshes, unmount/remount, every scope input,
secret-free descriptions, disabled rows, name copying, tooltip focus/click,
selection, keyboard/actions, manual-toggle compatibility and all four locales.

Additional checks:

- `npm --workspace apps/web run type-check`: passed (`tsc --noEmit`).
- ESLint on all changed TS/TSX files: exit 0, no errors; three existing
  `no-explicit-any` warnings in unchanged AccountsPage test code.
- Direct Sass compilation of the new indicator stylesheet: passed (163 bytes).
- `git diff --check -- apps/web`: passed.

No live manager/core acceptance, browser/Hermes acceptance, full production
bundle acceptance, backend testing, commit or push is claimed by the frontend
worker pass above.

## Independent coordinator verification (2026-10-25)

The coordinator reran the candidate using the repository's **standard Vite test
configuration**, without the temporary worker configuration or CSS proxies:

```bash
npm --workspace apps/web run test -- src/features/accounts/AccountsPage.test.tsx src/components/providers/ProviderTable/ProviderTable.test.tsx src/components/ui/InfoTooltip.test.tsx src/features/aiProviders/AiProvidersPage.test.tsx src/services/api/usageService.resourceCapabilities.test.ts src/features/accounts/components/CredentialAutoStartStopToggle.test.tsx src/features/accounts/components/credentialAutoStartStopI18n.test.ts src/features/resourceProtection
```

Result: **8 files passed, 451 tests passed** (30.74 seconds).

```bash
npm run test:web
```

Result: **269 files passed, 4,697 tests passed** (54.52 seconds).

Additional standard repository commands all passed:

```bash
npm run type-check
```

```bash
npm run lint
```

ESLint reported **0 errors and 6 existing warnings**: three `no-explicit-any`
warnings in unchanged AccountsPage test code and three existing fast-refresh
warnings in AccountHealthBadge / UsageMaintenanceCapabilityViews.

```bash
npm run build
```

```bash
npm run build:demo
```

```bash
npm run check:demo-isolation
```

```bash
npm run check:web-icons
```

The production bundle contains no Demo fixture markers. Both bundles built, and
the icon check found the root assets, marker probe and bound embedded fallbacks.
These are build checks, **not browser/rendered UI acceptance**. Browser tooling
remained blocked by its unavailable safety-classification service; no alternate
browser, permission change, live service or deployment was used.

The worker's temporary configuration and raw logs were absent from the final
worktree. No production dependency or generated runtime file was added.

## Independent review record

Two separate `gpt-6.1-sol` read-only reviewers checked the frozen staged candidate
against base `ce11c61def7fd01d85a7460eda50425e7fefee5e`, including all new files
(29 files, 1,501 additions, 6 deletions before this documentation-only update).
They did not review the empty pre-commit `base...HEAD` range.

- **Standards:** 0 documented-standard breaches, actionable heuristic smells or
  concrete correctness regressions. No blocking refactor/documentation finding.
- **Spec:** 0 concrete defects within the bounded slice and no scope creep.
  Persistent identities, full inventory/lifecycle isolation, positive bound gate
  evidence, real routing and Hermes acceptance remain expected outstanding work.

Both reviews were static; they did not independently reproduce tests or establish
runtime acceptance. The coordinator results above are the actual independent
execution evidence, not a claim that the reviewers ran those commands. Complete
T01, the child SPEC and the parent SPEC remain open.
