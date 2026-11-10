# Issue #97: web source registration and controlled observation slices

These slices add authenticated registration, a read-only BrowserSkill driver
boundary, and durable observation readback for a declared LAYSO web balance
source. Controlled fixtures verify the orchestration; no real browser, login,
account or LAYSO page has been observed. Wallet membership and key ownership
are never inferred from a page balance.

Use the existing panel-authenticated `PUT /usage-service/official-balance` with
the current complete Core `scope` and `targets`, the expected configuration
`version` (zero for a new source), and these additional fields:

```json
{
  "adapter": "layso_billing_web_v1",
  "web": {
    "site": "layso",
    "accountId": "expected-account-id",
    "tenantId": "expected-tenant-id",
    "role": "expected-billing-role",
    "executionUser": "expected-operator-id",
    "browser": {
      "family": "microsoft-edge",
      "profile": "Profile 1",
      "instanceId": "expected-extension-instance-id"
    },
    "currency": "USD",
    "parsingRule": "unverified"
  }
}
```

The example omits `scope`, `targets`, and `version`; it is not a complete request
or real account/browser mapping evidence. Identity fields must be explicit,
nonempty, at most 128 ASCII characters from letters, digits, `-_.:@`. They are
opaque expected identifiers, never paths, browser secrets, or executable code.
Only the shown site, browser family/profile and currency are accepted. Parsing
rule `unverified` registers a declaration but cannot collect. Other safe rule IDs
are accepted only when installed in the trusted host registry. That registry is
empty by default. A real LAYSO rule still requires an observed normal page; no
DOM selectors or guessed page labels are defined here.

The host fixes `endpoint` to `https://layso.ai/dashboard/models`; callers may omit it or
send that exact value. Other URLs, private IP permits, nonempty secrets, mixed API
contracts, and unknown JSON fields (including scripts/selectors) are rejected.
Existing API adapters retain their existing HTTPS and SSRF validation path.

The existing SQLite `official_balance_sources.config` document stores the entire
typed contract and complete Core resource group. No schema migration or dummy
credential is needed. Web `configured: true` means a valid declaration was saved;
`state: unavailable`, `reason: web_source_verification_required`, and false query
and gate flags describe its default capability. Without trusted enrollment,
manual POST is rejected before creating a job or observation. GET with the
JSON-encoded `binding` query parameter reads persisted state without collection
or recording a new success time.

Source updates use the existing version CAS. All account, tenant, role, execution
user, browser instance, and source-contract fields are included in the persisted
authority. An accepted update increments the source configuration version and
clears prior attempts/successes; it clears API credentials when changing to web.
Every bound Core identity, credential, configuration, and membership version is
retained, including peer/source targets. A changed tuple requires explicit fresh
registration; stale writers and old jobs cannot restore old authority or results.

`internal/httpapi/official_balance_web_test.go` exercises the production public
controller with temporary SQLite and controlled Core/HTTPS dependencies. It
reopens SQLite for readback, tests validation/authentication, account/role/CAS and
complete source-version isolation, and fences a late API response after a web
replacement. Synthetic identities prove only those isolated code paths.

The host startup dependency `OfficialBalanceService.WebReader` accepts a
`balance.NewBrowserSkill` reader. Its `Enrollment` callback must bind the exact
`webSourceAuthority` returned by the management API and the entire typed source.
The authority includes the source configuration version and complete current
Core scope/target versions. Enrollment must independently verify the execution
user, Edge Profile 1 mapping and installed parser, and supply a proof reference.
HTTP input cannot install a rule or assert any verification flag. Enrollment is
checked before each operation and after cleanup. Re-registration or Core scope
changes require fresh enrollment; prior observations cannot cross the boundary.

The driver uses only `bsk browsers --json`, explicitly bound own-session start,
navigation to the fixed normal billing URL, two `observe` calls, and own-session
stop. Both observations must match the expected visible account/tenant/role and
URL. Missing/unverified identities, changed enrollment, malformed CLI output,
unknown/non-finite/abbreviated amounts, unexpected currency and failed cleanup
cannot publish success. The exact decimal lexeme is preserved without float
conversion. The driver shares the management deadline, reserves cleanup time,
caps output at 2 MiB, serializes its own reads, and never persists raw page text,
CLI stderr or errors. No evaluate, private API, storage access, login or account
switching is used.

Verified reads enter the existing SQLite job/source-version transaction as
`semantic: web_account_balance`. Success has a local capture time and redacted
`webEvidence`; failures have no amount/capture time/receipt and retain the exact
last success marked stale. The fixture receipt explicitly says
`verification: controlled_fixture`. It is not real account acceptance. API
adapters stay on their own query branch even after 401/429/5xx or parse errors.

`internal/httpapi/official_balance_web_observation_test.go` exercises this path
through the authenticated management API, real temporary SQLite and controlled
external CLI/Core/HTTPS dependencies. It includes SQLite close/reopen, exact
negative decimal persistence, identity/role/enrollment and in-flight source/Core
version fences, timeout/cleanup/output failures and API no-fallback behavior.

Real BrowserSkill execution, verified Profile 1/execution-user enrollment,
observed LAYSO parsing, hour-scheduler integration, UI, independent review,
integration-branch acceptance, deployment and rollback remain pending. The
default production reader intentionally fails closed. Issue #97 remains open.
