# SPEC #92 / reused #70 Manager identity consumer evidence

## Authority and baseline

Scope: Manager inventory/capability consumer, representative capability UI, public
HTTP/temporary-SQLite identity tests and docs. No Core, balance adapter, automatic
policy/gate, credential-switch redesign or live runtime/config change. The approved
seam is Manager HTTP -> temporary SQLite -> isolated real Core / controlled HTTP
upstream. Installed TDD SKILL.md, tests.md and mocking.md were read and followed;
there is no separate Skill tool. Work proceeded in individual RED -> GREEN slices.

Worktree: D:/WILL/AGENT/CPA/ops/implement-spec92-20261101/worktrees/identity
Branch: spec/92-identity, from spec/92-integration at
7115819e47689399bee1acf235725ce63e0224f1, initially clean.
Read the full SPEC.md, legacy-70.json, quota-exploration.md, gate-exploration.md and
EXECUTION.md in the project execution directory, plus the subsequently published
CORE-IDENTITY-CONTRACT.md. Current user write scope supersedes legacy delegation.

The main agent's separate original-checkout full Go baseline is **exit 1**:
2956 passing / 8 failing / 9 skipped test+subtest events; 59 passing / 7 failing /
25 skipped package events. Its Windows permissions/signals/replacement failures
are recorded in manager-baseline-summary.json. This is not an all-green global
suite. This branch's targeted baseline passed before implementation.

## Delivered consumer behavior

- Existing no-query support summary and policy shape unchanged, zero upstream.
- Authenticated ?inventory=1 adds complete secret-free v8 scope/target inventory,
  including disabled rows. No v0/config/auth-file-download inventory fallback.
- ?inventory=1&bindings=1 consumes the actual producer resource-bindings contract.
  Scopes, every target/version, primary runtime alias and proven projection
  counterparts must match the complete catalog. Unsupported/invalid/missing
  bindings never become successful row associations or an empty-coverage claim.
- Redirect refusal, TLS verification, bounded JSON, allowlisted opaque IDs/enums,
  missing/null manual-state rejection, generic errors and secret canaries.
- Late Core revisions and saved-connection changes fence completions. No new
  identity DB/cache, no locally invented IDs, no policy/gate ACK.
- Actual credential UI table/card uses host-issued association by current-runtime
  auth_index within the negotiated scope, displays full safe Scope/Targets, and
  distinguishes bound/unbound/unknown/unavailable. Source-list/loading/context/
  generation/unmount changes invalidate evidence. Native providers without a
  verified carried source marker remain explicitly unbound, not guessed from keys.
- All four locales explain identity-only vs protection. Manual controls,
  copying, row keyboard actions and legacy quota remain unchanged.

## RED -> GREEN ledger

All logs are under the project-owned external evidence directory:
D:/WILL/AGENT/CPA/ops/implement-spec92-20261101/identity-evidence

| Slice | Behavior | Logs |
| --- | --- | --- |
| 1 | Core unavailable mode, no empty-success inventory | slice1-red/green.txt |
| 2 | Complete disabled/versioned inventory projection | slice2-red/green.txt |
| 3 | Core scope revision changed before completion | slice3-red/green.txt |
| 4 | Saved connection replaced during blocked read via public Manager config API | slice4-red/green.txt |
| 5 | Unsafe IDs/enums/counters cannot escape projection | slice5-red/green.txt |
| 6 | UI consumes negotiated available/unavailable/unknown, requests opt-in | slice6-red/green.txt |
| 7 | Missing/null manual disabled state cannot become false | slice7-red/green.txt |
| 8 | UI explains actual unsupported_mode | slice8-red/green.txt |
| 9 | Incomplete parent/member graph rejected | slice9-red/green.txt |
| 10 | OpenCode Go family and full source-binding targets preserved | slice10-red/green.txt |
| 11 | Unproven zero-target row cannot leak arbitrary binding fields | slice11-red/green.txt |
| 12 | Actual producer runtime-binding contract consumed | slice12-red/green.txt |
| 13 | Representative bound/unbound credential UI with full versions | slice13-red/green.txt |

Transport failure, auth/method, reorder/ambiguity, read lifecycle and stale binding
regressions were added at public boundaries; already-passing behavior is recorded
as regression, not fabricated RED. No internal service mocks/private assertions.

## Test commands and results

Go commands below run from the worktree's apps/manager-server directory.
Frontend commands run from the worktree root with this exact supported toolchain:

```powershell
$nodeDir='C:/Users/will/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/bin'
$env:PATH="$nodeDir;$env:PATH"
& "$nodeDir/node.exe" --version # v24.22.1; global v24.19.0 not used for tests/builds
& "$nodeDir/node.exe" 'C:/Program Files/nodejs/node_modules/npm/bin/npm-cli.js' ci --ignore-scripts
```

- Baseline: `go test ./internal/httpapi ./internal/service/resourcecapability ./internal/service/managerconfig ./internal/app` -> PASS (baseline-focused.txt).
- Per-slice exact commands/exit codes and log names: external COMMANDS.md.
- `go test ./internal/httpapi -run '^TestResource(Capabilities|Inventory)' -count=1 -v` -> PASS (backend-with-bindings.txt; optional real-process test skipped unless explicitly enabled).
- `go test -race ./internal/httpapi -run '^TestResource(Capabilities|Inventory)' -count=1` -> PASS (backend-race-with-bindings.txt; optional process exercise not enabled by this command).
- `go vet ./...` -> PASS (go-vet-with-bindings.txt).
- Final focused HTTP/app/settings/store tests PASS (final-focused-with-bindings.txt); entrypoint compilation PASS with no tests run (entrypoint-compile.txt).
- Frontend: `npm --workspace apps/web run test -- src/services/api/usageService.resourceCapabilities.test.ts src/features/resourceProtection src/features/accounts/AccountsPage.test.tsx src/components/providers/ProviderTable/ProviderTable.test.tsx src/features/aiProviders/AiProvidersPage.test.tsx` (invoked via Node/npm-cli above) -> 5 files / 460 tests PASS (frontend-with-bindings.txt).
- `npm run type-check` -> PASS (type-check-bindings.txt).
- `npm run lint` -> PASS, 0 errors / 7 existing warnings (lint-with-bindings.txt).
- `npm run build` -> PASS (build-with-bindings.txt).
- Earlier pre-binding increment demo build and demo isolation passed (demo-build.txt / demo-isolation.txt); they are not evidence of a post-binding demo rebuild.

Vite's existing native-config warning / Windows git-describe shell diagnostics,
React test renderer deprecation and existing lint warnings are not concealed.
Initial setup errors (wrong Python cwd/path, Windows default encoding, wrong
npm-cli location) were corrected before the actual RED tests. One Core regression
initially assumed rename preserved config_version: corrected to identity/credential
continuity because configuration may legitimately advance. A second initially
counted controlled-proxy-denied startup traffic as provider inventory traffic;
separate counters now distinguish them (eight denied startup/background attempts,
zero provider inventory calls in the baseline-Core run). These are test-fixture
corrections, not claims of product fixes. Earlier failure logs are preserved.

## Real Core evidence and remaining dependency

Original isolated Core was independently built from
00b7f8d7b245cef7675be7de832f37b661559ee8, SHA256
64A098CF526769F47D34717A1F15E24A3A14E8AC08977AF0DF7BB948509B8726.
Synthetic configs/auth dirs/SQLite only, sanitized child environment,
-local-model, disabled management panel/update, controlled denial proxy.
Actual Manager -> SQLite -> Core HTTP passed restart/rename/key-change scope and
target continuity, manual-disabled inclusion, plugin-mode negotiation honesty;
no provider calls. See final-identity-with-real-core.txt and earlier failure logs.
**This original binary does not prove the new runtime-binding producer.**

The same public regression now has an explicit strict producer mode:

```powershell
# Build from the delivered producer worktree/commit (not original Core):
go build -o D:/WILL/AGENT/CPA/ops/implement-spec92-20261101/identity-evidence/delivered-core.exe ./cmd/server
# In identity/apps/manager-server:
$env:CORE_IDENTITY_TEST_BINARY='D:/WILL/AGENT/CPA/ops/implement-spec92-20261101/identity-evidence/delivered-core.exe'
$env:CORE_IDENTITY_REQUIRE_BINDINGS='1'
go test ./internal/httpapi -run '^TestResourceInventoryRealCoreLifecycle$' -count=1 -v -timeout 120s
```

Strict mode creates a disabled native file credential and independently obtains its
actual runtime selector through the supported public Core `/v8/management/credentials` metadata API, then
requires Manager's producer-consumed Scope/Targets to identify that same source.
The name only locates a synthetic fixture; it is not identity. This mode must be
run against the delivered Core producer and recorded before integration acceptance.
At this handoff the producer is still in progress: this proof remains pending.

## Explicit acceptance gaps / handoff

Do NOT mark full #70 or #92 complete from this commit:

1. Delivered-producer Manager HTTP/SQLite runtime-binding exercise and known Qwen/
   OpenCode plus native source completeness/lifecycle evidence must be combined.
   Synthetic protocol/UI fixtures alone do not discharge this dependency.
2. Supported native provider source-marker association is implemented in the
   follow-up below. Sources without issued/current/unambiguous proof stay unbound;
   that is not a missing mapping for a currently supported issued source.
3. Rendered-browser layouts/keyboard/mobile checks and independent full-ticket
   review remain pending. React renderer tests are not browser screenshots.
4. Queries, balances, switches, policy/gate ACK/enforcement, live inventory
   enrollment, migration/deployment and runtime mutations are not owned here.

No main merge/push, issue closure, deployment, deletion or live mutation occurred.
Migration/rollback is additive and Manager-schema-free; see resource-capabilities.md.


## Native-provider follow-up (2026-11-01; after a7458730)

Public source API -> real normalizers -> real capability hook/ProviderTable tests
cover Gemini, Interactions, Codex, xAI, Meta, Claude, Vertex and OpenAI. Issued
resource-id markers are carried unchanged (not invented from raw key/position
matches); they become display evidence only after exact current catalog matching.
OpenAI group/member targets remain distinct, including membership versions.
Duplicate source/member/catalog markers, malformed markers, family/kind mismatch,
ambiguous/not-ready rows and missing member proof do not become associations.
Late source-list completions cannot restore invalidated evidence. Tooltip canaries
prove no source keys, URLs or management key enter capability identity output.

Real OpenAI drawer and routed layout public outlet edit/save tests demonstrate
that both group and member markers survive renamed form payloads. The routed
layout test uses its exported child-route context, not a browser-rendered portal.
Public API tests cover raw-config unavailable saves, same-name/reordered edits,
key edits, marker non-invention and refusal of ambiguous marked updates. Existing
legacy manual selectors are not used to establish catalog authority. A browser
numeric timer-ref typing correction in Modal is required because the Node HTTP
fixtures bring Node timer overloads into test type checking; runtime is unchanged.

The strict process test now uses the actual credentials route and waits for full
Manager inventory/bindings negotiation within its existing 20-second startup
context. HTTP 200/not_ready is never ready; persistent unavailability still fails.
Auth/schema errors fail immediately. Explicit plugin unsupported states retain
their separate negative assertions. Scope, target/version and per-fixture binding
assertions have not been relaxed. Parent's early strict failure remains failure
(parent-cross-contract-wip.log); producer diagnostic23-debug.log identified the
publication-readiness transition. Parent owns the final committed/integrated Core
run; this follow-up does not duplicate or claim it. Browser fixture success is
not rendered-browser acceptance; parent reported host-policy rejection before
navigation and cleaned its owned fixture/session. Browser acceptance is pending.

Follow-up raw logs, exact commands and results:
D:/WILL/AGENT/CPA/ops/implement-spec92-20261101/identity-evidence/native-provider/.
See REPORT.md there for the terminal verification receipt and RED/GREEN ledger.
Full original Go baseline remains exit 1 with the eight known Windows failures;
no all-green global claim. No Core, gate, live config or deployment changes.
