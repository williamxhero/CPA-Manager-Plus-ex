# First-source official balance read (#71 / SPEC92)

This implements **DeepSeek `GET /user/balance` only**. It does not implement an
hourly scheduler, automatic policy, Core quota acquisition, or a Core gate ACK.
The former DeepInfra `/v1/billing` / `recent_usage` float scaffold is disabled;
DeepInfra capability remains unsupported pending #73. No inference credential is
read from Core or exported to the browser for this feature.

## Public management seam

All routes use panel administrator authentication and `Cache-Control: no-store`:

- `GET /usage-service/official-balance?binding=<URL-encoded Binding JSON>` reads
  configuration, the latest attempt and the last success. It performs current
  Core identity negotiation, **never a billing/platform request**.
- `PUT /usage-service/official-balance` accepts `scope`, `targets`, `version`
  (source configuration CAS; zero for creation), `adapter: "deepseek"`, a separate
  `endpoint`, optional `privateTargets` and `timeoutMs`, and write-only `secret`
  / `clearSecret`. The default endpoint is
  `https://api.deepseek.com/user/balance`. Empty/whitespace secret preserves the
  existing secret; explicit clear removes it. Setting and clearing together is
  rejected. Read responses contain only `configured`, not a secret, ciphertext,
  or secret reference. Source/authority replacement without a newly submitted
  secret clears the old secret. CAS and identity conflicts return 409.
- `POST /usage-service/official-balance` accepts `scope`, `targets`, `version`
  and returns a 202 job with an opaque `id`. Same-source/version/**full operation
  context** requests join the active job. At most four jobs run; no unbounded
  queue is created (429 when full).
- `GET /usage-service/official-balance?job=<id>` reads that persisted job only.
  Interrupted jobs after host restart are explicit; no GET restarts them.
  Superseded source versions and stale Core scope cannot read an old job as the
  current target's result.

`Binding` carries the full verified Core `Scope` and all applicable `Target`s,
not labels, source positions, runtime indexes, or secret hashes. Provider bindings
include every member; verified source projections include the full projection
group. Target ordering has no authority. The backend revalidates the full scope
and every selected version before starting and before accepting completion.

Persistent credential authority consists of instance ID/incarnation and all
Target IDs/incarnations/identity/credential/config/membership versions. A freshly
verified new boot epoch or unrelated snapshot revision can rebind that unchanged
authority without reentering the billing secret. Previous successful evidence is
retained but stale until refreshed in the new scope. Old in-flight jobs retain
their original full operation context and cannot become new-boot results.
Real target versions, endpoint, adapter, incarnation or instance changes cannot
inherit usable old credentials/results. The CAS source version also fences
secret rotation and late completion.

`managerDirectQuery` describes Manager's independent adapter, not Core. The
separate `coreQuery` / `coreGate` flags remain false. No v0 mutation fallback,
source disablement, policy enrollment, or inference routing change is performed.

## Exact observations and freshness

DeepSeek `is_available` and each `balance_infos` entry are validated. Required
balances must be decimal **strings**, with at most 60 integer and 30 fractional
digits; negative debt is legitimate. Missing/null/numeric/nonfinite/exponent or
malformed values are parse failures, not zero. Each three-letter currency is
preserved separately, along with exact total, granted and topped-up strings.
There is no float conversion, rounding, cross-currency sum, or total-plus-
components double counting. No account equivalence is inferred from hostname.

Latest attempt and last success are separate SQLite values; a failed query
retains the prior successful wallet/time and marks it stale. Success is stale
after an hour or after a verified operation-scope rebind. Auth, rate limiting,
network, parse, needs-configuration and unsupported states remain explicit.
The accepted attempt and job are persisted atomically **before** upstream I/O.
If the hard management budget is exhausted during final identity verification,
no unverified platform value is accepted and no extra network budget is granted.
After fresh identity verification, GET can report the persisted expired attempt
as `MANAGEMENT_BUDGET_EXHAUSTED` while retaining the prior wallet. Completion also
requires the current persisted attempt ID, so an older operation cannot overwrite
a newer accepted attempt even at the same source configuration version.

## Transport and budgets

The production transport is always used, including in positive integration
tests. The only varied test dependencies are resolver/dial boundaries and trust
roots, not an arbitrary RoundTripper or fake querier.

- HTTPS only; TLS >=1.2; normal certificate/hostname validation; no insecure mode.
- No environment proxy, compression ambiguity, or credential-bearing redirect
  (all redirects are refused). Response body is bounded at 2 MiB plus one-byte
  overflow detection; headers are bounded too. Upstream error bodies and network
  diagnostics are not exposed or logged.
- Validate the entire DNS answer, then dial only approved numeric addresses.
  No second hostname resolution; mixed public/forbidden answers fail closed.
- Exact administrator IP permits are required for native private/loopback
  targets. Permits are not CIDRs/wildcards and cannot override metadata,
  link-local, multicast, unspecified, special/reserved, or unsupported transition
  classes. IPv4-mapped addresses are normalized before the same IPv4 ACL.
- IPv6 native GUA is conservatively limited to `2000::/3`, excluding special
  IETF/transition/documentation ranges. Native ULA/loopback still require exact
  private permits. NAT64 well-known/local-use prefixes, deprecated site-local,
  IPv4-compatible, 6to4, Teredo and ISATAP transition cases are unsupported, not
  blindly trusted because `IsGlobalUnicast` returned true. Arbitrary network-
  specific translators are not an asserted universal network-security guarantee;
  deployment egress policy remains a separate final-delivery responsibility.
- Default total job budget is 15 seconds, configured hard maximum 60 seconds,
  including initial identity verification. Part of that same budget is reserved
  for final identity verification and durable failure recording; diagnostics do
  not extend it. This affects management reads only, never established inference
  streams. Ordinary management status/config requests are bounded at 15 seconds.

Primary address-policy references: [RFC6052](https://www.rfc-editor.org/rfc/rfc6052.html),
[RFC8215](https://www.rfc-editor.org/rfc/rfc8215.html),
[IANA IPv6 special registry](https://www.iana.org/assignments/iana-ipv6-special-registry).
Protocol: [DeepSeek balance reference](https://api-docs.deepseek.com/api/get-user-balance/).

## Persistence and rollback

The migration is additive: `official_balance_sources` and
`official_balance_jobs`; no old usage/settings/source rows are dropped or
rewritten. Secrets use the existing server data-key protector and an opaque
server-owned reference, with source/version/reference checks on resolution.
Missing encryption is fail-closed. Startup refuses to generate a replacement
data key if this new table already contains encrypted secrets, including an
env-only Core connection deployment. This is scoped protection for the new
secret store, not the blocked legacy #95 security migration.

Before any later authorized deployment, back up the consistent SQLite database
**and its original data.key** using the project's supported backup procedure.
An old binary can ignore these additive tables; rollback must preserve the
original key and database backup. Do not delete keys/tables or copy only a live
WAL database file. No deployment, runtime backup, or real account query was
performed for this implementation.

## Verification

Run the `TestOfficialBalance*` HTTP tests in `apps/manager-server/internal/httpapi`.
They cross administrator APIs, real temporary SQLite, controlled Core HTTP and
real controlled TLS upstreams. Synthetic resolver/dial tests assert zero dial
calls for forbidden translated metadata, not merely a generic network error.

The opt-in `TestOfficialBalanceUIFixture` exposes the same isolated real stack
for `OfficialBalancePanel.test.tsx`. Set `OFFICIAL_BALANCE_UI_FIXTURE` to a fresh
temporary directory for both Go and Vitest; wait for `fixture.json`, run the UI
test, then create `stop` there. This is a test-only loopback fixture, excluded
from production. Without the environment variable, this one live UI test is
explicitly skipped; that skip is not evidence it passed. The delivery evidence
records its separate successful live execution.

For the highest producer/consumer chain, explicitly set
`CORE_OFFICIAL_QUERY_TEST_BINARY` to the independently built, tested Core artifact,
and set **both** `CORE_OFFICIAL_QUERY_EXPECTED_SHA256` and
`CORE_OFFICIAL_QUERY_EXPECTED_REVISION` from its independently trusted build
receipt. A supplied binary without valid expected provenance fails (not skips).
Do not derive expected values from the binary under test. Then run
`go test ./internal/httpapi -run '^TestOfficialBalanceActualCoreChain$' -count=1 -v -timeout 120s`.
The current acceptance used immutable `core-identity-final-f78e509f.exe`, expected
SHA256 `9fb3cb67dd879df1a32719c35e7ceed939e4c49956fbe97b3036466af50784e5` and
source `f78e509fc1b231fb071d1522a704b172c19dcad7` from the existing trusted receipt.
These are evidence of that combination, not goldens hardcoded into the test.
The parent must supply the **latest** independently verified Core/Manager
combination at full-release rerun; old compatibility evidence is not that gate.
The test verifies supplied provenance and `vcs.modified=false`, starts the actual producer with only synthetic config,
an owned directory and deny proxy, and uses actual Manager HTTP/temporary SQLite
and controlled TLS. It proves full native provider/member binding, exact balances,
failure retention, no billing queries from GET, actual Core restart without
resubmitting the secret, and no preboot in-flight overwrite beyond the entire
old operation budget. It captures Core capabilities/source/manual state before
and after billing operations and asserts they are unchanged; it does not require
future Core query/gate availability to remain false. Availability is not an
application ACK. It never performs inference or accepts a protection ACK.

Rendered browser/mobile/keyboard acceptance, full release/security gates,
real-account deployment/readback and other adapters remain parent-owned gates.
