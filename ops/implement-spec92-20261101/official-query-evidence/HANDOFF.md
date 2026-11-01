# Ticket #71 first-source handoff (SPEC92 frontier only)

## Scope and authority

- Worktree: `D:/WILL/AGENT/CPA/ops/implement-spec92-20261101/worktrees/official-query`.
- Branch: `spec/92-official-query`. Before edits, branch and clean status verified;
  tested integration `38af4658a0a38efe3fc0244066838fd279ffd9c1` was an ancestor (exit 0).
- Loaded all requested frontier inputs, CONTRIBUTING, TDD/tests/mocking and
  codebase-design installed skills. No real runtime/config/DB/account/provider
  was inspected. Existing authorized DeepSeek probe200 is supplied evidence,
  not a query repeated by this implementation.
- DeepSeek first-source configuration -> authenticated job -> real SQLite
  attempt/last-success -> status -> provider detail UI is implemented. The API
  contract and rollback notes are in `docs/development/official-balance-first-source.md`.
- Server-owned opaque refs and existing server encryption protect explicit query
  credentials. Empty preserves, explicit clear removes; no Core secret export.
- All full Targets are checked (including provider membership/projections).
  Persistent authority is distinct from boot/revision operation context.
  Verified rebind preserves the secret, but stale in-flight work is rejected.
- DeepInfra's invented endpoint/float/recent_usage query was removed from the
  executable path. Its capability is unsupported; no unsafe billing call is made.
- No other adapter, hourly scheduler, policy/gate, switch redesign, or legacy
  security migration was implemented. The small missing-key guard extension is
  solely for the NEW encrypted source table, not the blocked legacy migration.
- Core query/gate flags remain false. Manager direct-query readiness is separate.

## Ordered behavior RED -> GREEN evidence

Each row is one behavior slice. Later regression cases that passed immediately
are not misrepresented as RED. All test data, money and credentials are synthetic.

| Behavior | RED | GREEN |
|---|---|---|
| Authenticated explicit config CAS | 01-config-red.log (404) | 01-config-green.log |
| Exact query/job/SQLite/TLS/restart readback | 02-query-red.log (405) | 02-query-green.log |
| Timeout attempt retains last success | 03-timeout-red.log (discarded) | 03-timeout-green.log |
| Terminal job read race under protocol regression | 04-protocol-fencing-regression.log | 04-protocol-fencing-green.log |
| Azure metadata cannot be privately permitted | 05-metadata-red.log (200) | 05-metadata-green.log (400) |
| Full source-projection target group | 07-projection-red.log (subset accepted) | 07-projection-green.log |
| Translated metadata must never reach dial | 09-translated-metadata-red.log (dial=1, NO actual connection) | 09-translated-metadata-green.log (dial=0) |
| Initial UI persisted state | 08-ui-red.log (null UI) | 08-ui-green-initial.log |
| Verified boot/revision rebind keeps secret | 10-rebind-red.log (configured=false) | 10-rebind-green.log |
| New secret table prevents replacement data.key | 12-key-guard-red.log | 12-key-guard-green.log |
| Target-version fencing / JSON slice aliasing | 14-authority-regression.log | 14-authority-green.log |
| UI can revoke all exact private permits | 15-ui-permit-red.log | 15-ui-permit-green.log |
| Exhausted final-verification budget retains a durable attempt | 21-durable-attempt-red.log | 21-durable-attempt-green.log |

`08-ui-green.log` is a retained FAILED harness attempt, not a green: its act()
wait ran before effects were flushed. The corrected harness and actual live
UI runs are explicitly named above and in `11-ui-full-regression.log`.
An early command used the wrong working-directory prefix and ran no tests;
that is not a behavior RED and is not acceptance evidence.

## Highest actual-producer proof

`22-actual-core-chain.log` explicitly enables `TestOfficialBalanceActualCoreChain`:

- Immutable binary SHA256 `9fb3cb67dd879df1a32719c35e7ceed939e4c49956fbe97b3036466af50784e5`.
- Embedded source `f78e509fc1b231fb071d1522a704b172c19dcad7`, modified=false.
- Actual Core process, synthetic config/own auth/catalog/home dir, deny proxy;
  actual Manager HTTP server + temporary SQLite + controlled TLS upstream.
- Actual native provider and member issued Targets, including disabled source.
- Exact CNY debt + USD strings; independent total/granted/topped-up fields.
- Auth failure retains last-success/stale; status GET causes zero billing calls.
- Actual Core restart preserves all durable Target versions but changes boot;
  read/requery succeeds with ZERO billing-secret resubmissions.
- Old in-flight TLS response is released after new-boot success. The test waits
  past its whole operation deadline and proves it cannot replace the new amount.
- Four denied startup/background requests, four synthetic billing calls, ZERO
  inference calls. Core query/gate remain false.
- `23-final-race-actual-core.log`: the entire official-balance seam INCLUDING
  that explicitly enabled actual-process chain passed under race detection.
  Earlier opt-in skips are not counted as this acceptance.

## Other verification and failures (no hidden baseline reclassification)

- Production transport positives exercise real TLS and private permits. Negative
  resolver/dial cases assert zero dial/upstream calls for NAT64 well-known and
  local-use, site-local, 6to4, Teredo, compatible/mapped metadata and ISATAP cases.
  Mapped loopback with an exact native IPv4 permit still succeeds. Mixed DNS,
  pinning, cert validation, HTTP refusal, redirects, response cap, missing/null/
  numeric/NaN/Inf/HTML, 401/403/429 and connection refusal have seam coverage.
- Address classification is conservative; arbitrary network-specific translators
  are not claimed universally detectable. Deployment egress controls remain a
  separate final gate. Primary RFC6052/RFC8215/IANA references are in the docs.
- Successful/failed attempts and jobs are durably recorded. Beginning an accepted
  attempt and job is atomic before upstream I/O. Expired management work is
  reported from this persisted attempt after fresh identity verification, without
  accepting unverified platform data or extending the hard network budget.
- Full frontend: 5080 passed / 1 skipped (the opt-in live test). Its separate
  live React -> real Manager/SQLite/TLS execution PASSED: config/readback, cleared
  editor, exact wallet, auth/stale retention, target switch, permit revocation.
- Type-check, scoped lint, frontend build and Go build passed. Full lint had no
  errors and seven existing warnings. Bundled Node24.22.1 / npm CLI11.17.0 used;
  unsupported global Node24.19 was not used. No dependencies/lock/audit settings
  were changed; the supplied 13-moderate audit baseline is not remediated here.
- The first selected backend packages passed (`backend-regression.log`). The broad
  full Go run was NOT GREEN: the known eight Windows signal/mode/file-replacement
  cases failed, plus six packages timed out during overlapping validation. Those
  timeouts are NOT falsely declared a proven baseline. `17-budget-final-seam.log`
  and `backend-race.log` retain concurrent-load timeouts; sequential seam and
  race retries passed WITHOUT relaxing the 60s/120s test deadlines.
- The later owner run `20-final-owner-regression.log` still caught a real expired
  attempt loss (not merely a baseline). A deterministic public-seam reproduction
  drove the 21 RED/GREEN fix. Final owner/race results are separate newer receipts.
- Repository Node tests were NOT GREEN: default Windows WSL bash invocation had
  137 failures / 432 passes; explicit Git Bash invocation had 70 installer failures
  / 499 passes. Scripts/tests were unchanged. These platform failures are NOT
  claimed as freshly proven baseline; parent final release validation remains open.
- No existing test or timeout was weakened. No audit suppression or dependency
  update was used to hide failures.

## Resource hygiene and true remaining gates

`own-orphan-cleanup.json` records ONLY two helpers (PIDs 5048,15484) of this
terminal Go full run. Exact binary path, args, creation times, terminal parent
and unique embedded worktree source proof were checked. Both were removed;
remaining exact helpers=0. No live service/other-agent process was touched.
No worktree, repository, backup, runtime data or unrelated directory was cleaned.

Parent still owns rendered browser/mobile/keyboard acceptance, independent full
release/security review, broad platform validation, other-source readiness,
scheduler/gate/policy/switch tickets, main/dev promotion, pushes, PRs, closure,
cleanup and deployment/hash/readback/rollback. This is NOT full SPEC92 completion.

Commit and latest integration merge readback are recorded after the scoped commit
in the parent evidence `DELIVERY.json`; the parent merger owns further integration.

## Final harness freeze / focused results

- `25-missing-provenance-rejected.log`: supplied binary with missing independent
  expected SHA/revision fails immediately (expected exit 1), never skips.
- `25-provenance-current-core-chain.log`: the CURRENT trusted f78 manifest was
  independently supplied through `CORE_OFFICIAL_QUERY_EXPECTED_SHA256` and
  `CORE_OFFICIAL_QUERY_EXPECTED_REVISION`; final harness PASSED (19.48s test /
  20.369s package). No golden hash/revision remains in the test. Before/after
  capability and full source/manual state are unchanged; future Core availability
  may legitimately be true. No protection application ACK is accepted.
- Preserve 22/23 as original compatibility evidence, not a substitute for latest
  full-release combo. Parent must supply the newest independently trusted binary
  plus manifest and explicitly enable test25 after merging. Never derive expected
  provenance from the binary being tested, and never silently keep old Core.
- `23-final-race-actual-core.log`: 22 top-level official-balance tests passed
  (including actual Core), one UI-fixture launcher intentionally skipped because
  that launcher is separately enabled. Race run: 38.422s, zero race reports.
- `24-final-owner-actual-core.log`: complete httpapi, balance and app packages
  passed: 41.474s / 0.769s / 1.776s. This supersedes the real expired-attempt failure
  in 20; no timeout was relaxed. Store/SQLite packages passed in 20 (26.010s /
  66.230s); the earlier complete selected package run also passed.
- `26-ui-unsupported-red.log` -> `26-ui-unsupported-green.log`: final honest
  unsupported-source presentation. Native source hostname selects UI protocol
  availability only, NEVER identity. Other protocols get no fake configuration
  or query controls; backend still owns identity/authorization.
- `27-final-ui.log`: 2 files / 16 tests passed, including the explicitly enabled
  live Manager/SQLite/TLS UI workflow and existing provider detail regression.
  This final targeted run includes the new unsupported UI test; no broad suite
  was restarted. Historical full frontend result remains 5080 passes / 1 opt-in
  skip, measured before that last added UI test. Do not invent a new full count.

## Extension pointers (no additional implementation authorized here)

All paths below are repository-relative to the Manager worktree.

### #73 / #74 protocol adapter workers

- `apps/manager-server/internal/balance/balance.go`: Querier, QueryTarget,
  Observation/CurrencyBalance and CredentialResolver. Money is exact strings;
  multiple currencies and total/components remain independent. Binding JSON in
  persisted observations is host-owned; protocol adapters do not invent identity.
- `.../balance/querier.go`: capability truth, explicit protocol dispatch, resolver
  handling and bounded query context. DeepInfra is deliberately unsupported.
  Replace no global transports and do not silently fall back to another protocol.
- `.../balance/adapter_deepseek.go`: bounded protocol parse / safe classification
  example. `adapter_deepinfra.go` documents why the old scaffold is not executable.
- `.../balance/source.go` and `transport.go`: shared safety transport / exact-IP
  permits / entire-answer validation / numeric pin / TLS / no redirects. The
  current source path validator is intentionally DeepSeek-specific `/user/balance`;
  a newly verified adapter needs its own EXPLICIT path policy, not a blanket
  weakening of HTTPS/target/redirect/metadata restrictions.
- `.../service/officialbalance/service.go`: Config whitelist/source validation and
  explicit credentials; extend supported source config/capability only after the
  actual protocol is established. Do not revive `/v1/billing` / `recent_usage` or
  float normalization from third-party assumptions.
- Test seam: `.../httpapi/official_balance_test.go`. Only use NetworkOptions
  RootCAs/LookupIP/DialContext at external dependencies; preserve the production
  transport and actual HTTP/TLS/SQLite/controller chain, not internal fake queriers.

### #75 credential host owner

- `.../service/officialbalance/{service.go,query.go}`: reusable Configure/Start/
  Status/Job host. Binding = full Scope + full Targets; source CAS, operation
  context and accepted attempt ID are distinct fences. Start provides bounded
  single-flight; no scheduler or policy is implemented. GET never queries billing.
- `.../store/official_balance.go`: encrypted server ref resolution and real SQLite
  atomic begin-attempt/job + fenced completion; latest attempt / last-success are
  separate. Do not substitute the old unimplemented balance.Repository model for
  this real persistence. No Core secret export or identity inferred from labels.
- `.../service/resourcecapability/{contract.go,inventory.go}`: use producer-issued
  current runtime binding/targets for credential rows and the complete source
  projection group. Unbound/unavailable is not an empty successful inventory.
- `.../http/controller/officialbalance/handler.go` and router: authenticated public
  GET/PUT/POST seam `/usage-service/official-balance`; see contract documentation.
- `apps/web/src/services/api/officialBalance.ts` +
  `components/providers/OfficialBalancePanel.tsx`: exact persisted display and
  explicit write-only credential UI; provider detail page is its current host.
- Credential query observations are NOT gate ACKs. Any later policy/executor
  requires its own real Core admission/ACK contract and fresh matching identity,
  source version and observation. Do not clear independent/manual pause owners or
  infer an account/wallet association from domain or unrelated provider labels.
- Final producer/consumer rerun: `official_balance_core_test.go`, with explicit
  latest Core binary + independent SHA/revision. `RECEIPT.json` records the current
  acceptance manifest; parent must update its run inputs, not edit test goldens.
