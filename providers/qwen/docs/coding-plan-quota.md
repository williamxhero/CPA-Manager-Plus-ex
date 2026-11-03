# Credential-management Coding Plan query contract

Updated 2026-11-03. SPEC92 Qwen CLI/cache component contract; **not deployed integration, full end-to-end acceptance or remote ownership verification**. This supersedes the earlier raw-token-only/helper-only description.

## Canonical classification and scope

CPA credential management means **Coding Plan**. The AI-provider entry means prepaid PAYG cash (out of scope here). Provider name, inference hostname and successful `/models` discovery cannot override this classification or prove ownership. Inference routing is unchanged by this patch.

This plugin's credential quota command accepts only the versioned Coding Plan helper protocol. Explicit legacy `bsk`, `cookie`, `bl`, Token Plan cache/check modes, and missing/default product configuration fail as `needs_configuration` **before subprocess execution**. Legacy standalone tooling remains for compatibility, not as an alternative credential query or optional enrichment. Do not configure it for this lane.

## Explicit CLI/cache reading chain

The plugin accepts these explicit helper arguments for the native CLI/cache lane (placeholders only; no live enrollment/config is created here):

```text
bailian-quota --protocol qwen-coding-plan-v1 --source coding-plan --bl-path <explicit-bl-executable-or-cmd> --cache-file <coding-plan-cache.json> --timeout 90s --json
bailian-quota --protocol qwen-coding-plan-v1 --source coding-plan --cache-only --cache-file <coding-plan-cache.json> --max-age 45m --json
```

Live CLI acquisition reuses the existing bounded BL runner and Windows private Job, invoking exactly `usage coding-plan --output json` once. This integration adds **no official API operation**, helper login/auth/refresh command, browser, retry or Token Plan request. An explicit `--bl-path` is required; `BAILIAN_BL_PATH` alone does not activate it. The supplied official CLI can internally refresh and write its own profile, so its execution is not guaranteed side-effect-free. Enrollment remains a separate, explicitly authorized operation.

The plugin CLI lane requires `--cache-file`; cache-only also requires explicit positive `--max-age`. The helper alone defaults `--max-age` to 45m. CLI transport cannot be combined with raw-token/region/site options. Cache-only reads only the supplied cache: no CLI, token-file, profile or network access, no receipt writes, and no new observation timestamp.

Flattened CLI JSON must supply each window's native `usedQuota`, `totalQuota` and integer-millisecond `resetTime`. `percentage` is informational and is not back-converted to counts. Native counts are decoded without adding float rounding; precision already lost inside an external CLI cannot be recovered. A CLI error or any nonzero exit, including the old Windows teardown-abort status, fails closed.

Successful helper acquisition can atomically save the strict `CodingPlanResult` via a same-directory temporary file and replacement. The old cache survives failed acquisition/save. Cache-only rejects stale/future/wrong-protocol/incomplete/malformed/over-4-MiB input and preserves the original `observedAt` and reset precision. Neither cache success nor reset passage proves ownership or restores policy quota locally.

Live cache producers also publish `<cache-file>.attempt.json`: `version:1`, `state`, RFC3339Nano `startedAt`, and terminal `completedAt`. A `running` receipt precedes CLI/token/HTTP I/O. A `success` receipt includes the original `observedAt` and lowercase64hex `cacheSHA256` of the exact saved bytes, including the JSON newline; `failed` includes neither observation nor hash. Failed refresh invalidates the receipt without replacing the old cache. Start-publication failure prevents acquisition; final-success-publication failure emits no success and attempts to publish failure. Receipts contain no diagnostics or secrets.

Cache and receipt are not a cross-file transaction. Same-path producers must be externally serialized; unwritable sidecars can prevent even failure publication. Core independently validates current source/resource enrollment, freshness, matching successful receipt/hash and exact normalized windows. The plugin emits candidate metadata only and does not duplicate these Core checks. Local administrator enrollment is not independent verification of remote key/account ownership.

### Existing raw-token compatibility

The existing raw-token query is retained, not added or expanded by this CLI/cache integration. It remains non-authoritative for native policy quota:

```text
bailian-quota --protocol qwen-coding-plan-v1 --source coding-plan --token-file <protected-raw-console-token-file> --console-region cn-beijing --console-site domestic --timeout 90s --json
```

The other allowed pair is `ap-southeast-1` / `international`. Unknown combinations fail; there is no hostname/default-region inference. The file must contain a raw, pre-enrolled console bearer token, not a CLI profile/login JSON. Protect it outside the repository, readable only by the intended query identity. Missing/expired tokens require separate enrollment outside the query path. This task neither performs nor authorizes enrollment.

In raw-token mode the helper does not execute `bl`, `bsk`, login, token refresh, key creation, a browser or a default-profile lookup. It does not write token/config files and writes no cache unless explicitly opted in at the helper boundary. Browser/cookie/check flags and alternate products remain incompatible. No raw token is supplied in argv, diagnostics or output; the selected inference key is never passed to either helper transport.

This pre-existing compatibility transport performs one fixed HTTPS POST query, with system certificate verification and no redirect; the new CLI path does not call it or fall back to it:

| Region / site | Origin | Action | Commodity |
| --- | --- | --- | --- |
| cn-beijing / domestic | `https://bailian-cs.console.aliyun.com` | `BroadScopeAspnGateway` | `sfm_codingplan_public_cn` |
| ap-southeast-1 / international | `https://bailian-singapore-cs.alibabacloud.com` | `IntlBroadScopeAspnGateway` | `sfm_codingplan_public_intl` |

Path `/cli/api.json`; query `product=sfm_bailian`, `action=<above>`, `api=zeldaEasy.broadscope-bailian.codingPlan.queryCodingPlanInstanceInfoV2`. Form body contains only `params=<JSON>` and `region=<explicit region>`. Header `Authorization: Bearer <explicit console token>`; no inference bearer/cookie or account-switch selector.

`params` is `{Api:<operation>,V:"1.0",Data:{queryCodingPlanInstanceInfoRequest:{commodityCode:<above>,onlyLatestOne:true},cornerstoneParam:{protocol:"V2",console:"ONE_CONSOLE",productCode:"p_efm",switchUserType:3,consoleSite:"BAILIAN_ALIYUN"}}}`. No arbitrary URL/operation/body extension is accepted.

## Query result versus selected-key authority

Our helper protocol is **not the official CLI JSON**. Synthetic example (all three windows are required; these dates are fixture data, not a fresh policy reading):

```json
{
  "protocol":"qwen-coding-plan-v1",
  "product":"coding-plan",
  "queryStatus":"success",
  "authorityStatus":"needs_configuration",
  "reason":"account_authority_unverified",
  "observedAt":"2026-11-02T12:00:00Z",
  "instanceType":"pro",
  "windows":[
    {"window":"5h","unit":"requests","used":"38","total":"100","remaining":"62","resetTime":"2026-08-06T07:06:40Z"},
    {"window":"week","unit":"requests","used":"500","total":"1000","remaining":"500","resetTime":"2026-08-07T10:53:20Z"},
    {"window":"billing_month","unit":"requests","used":"950","total":"1000","remaining":"50","resetTime":"2026-08-08T14:40:00Z"}
  ]
}
```

- Select the first `status == VALID` instance, as the frozen first-party source does. `instanceType` is a tier, **not a unique plan ID**. No VALID instance fails without guessing expired/absent/mismatched-account status.
- Native counts are **model requests**, not tokens, money or percentages. Require all three windows. Used >= 0, total > 0, used <= total; no clamp, rounding, unlimited/zero inference, percentage back-calculation or partial policy reading.
- Decode upstream JSON numbers without float conversion; accept only unsigned integer lexical form, bounded to 64 decimal digits. Serialize exact counts as decimal strings. Missing/null/strings/fractions/exponents or contradictory remaining counts fail closed. The 64-digit bound is an adapter resource limit, not a claimed official maximum.
- Millisecond epochs must be integers in [946684800000, 253402300799999]. Preserve milliseconds in RFC3339Nano; reject seconds/fractions rather than converting units. Billing-month reset is not computed as 30 days. Reset passage does not locally restore quota.
- `observedAt` is helper receive/parse time, not an official server sample timestamp or ownership proof. Cache-only preserves that original time; it does not re-stamp stale data. Cache age and latest-attempt receipt checks supply bounded freshness evidence, not remote identity verification.
- A single complete JSON value, bounded response and output, gateway/business/nested success/error checks, nonzero-exit rejection, existing command deadline and diagnostic redaction remain mandatory. New code does not inherit the legacy BL Windows teardown-abort exception.
- The plugin independently validates the versioned result, counts, unit and three-window identity. Unknown fields, helper `verified` claims or a changed authority state are rejected. Neither selected-key attributes nor hand-entered account/login JSON can bypass the guard.

The public surfaces have deliberately different authority contracts:

| Surface | Successful query result | Authority |
| --- | --- | --- |
| Native `Manager.FetchQuota`, explicit CLI/cache | Coding Plan subscription, one group with three normalized window buckets, `observedAtMs` derived from the original observation, `protocol:qwen-coding-plan-v1`, `timestampProvenance:local_cli_acquisition_time`, `accountProvenance:configured_cli_profile`; no summary | Candidate evidence only; Core must validate enrollment/receipt before policy admission. |
| Native `Manager.FetchQuota`, raw-token | Empty quota plus `needs_configuration: account_authority_unverified` | No policy authority. |
| Legacy plugin management `quota-usage`, **bulk or single** | Credential labels/indexes retained, `queryStatus:success`, `authorityStatus:needs_configuration`, error `account_authority_unverified`; no windows, metrics, plan/period or observation attribution | No Core binding/receipt context, hence no authority even when native CLI parsing succeeded. Configured and panel-added keys obey the same rule. |

One query's status may appear on several legacy cards, but its account quota is never copied to those keys. Invalid/nonzero/stale helper results remain failed queries, not success. No Core guard is removed, no Core/Manager endpoint is modified, and no new API is introduced by this Qwen fix.

Core enrollment uses `quota-resource-bindings` entries containing **all five** current fields: `resource-id`, `identity-version`, `credential-version`, `config-version`, `membership-version`. Resource ID alone or old `quota-resource-ids` cannot substitute for a generation binding. Core owns source revision, cache/receipt matching and freshness validation; `configured_cli_profile` is neither an admission acknowledgment nor an official account identifier.

## Public protocol evidence

Frozen source: first-party `modelstudioai/cli@a7cb810c68f434c2c7330d8c0775614f7e699c8d`, package 2.7.2. These are documentation/source references, **not installed or executed dependencies**:

- `packages/commands/src/commands/usage/coding-plan.ts:5-11,49-67,107-112`: operation, commodity, first VALID instance, `onlyLatestOne`.
- `packages/core/src/console/gateway.ts:17-41,90-112,127-161`: fixed origins/action, form envelope and bearer header.
- `packages/commands/tests/coding-plan-usage.test.ts:68-78,169-221`: three-window fixture, incomplete field semantics. Independent expected remaining counts: 62 / 500 / 50.
- `packages/core/src/client/client.ts:252-290` and `packages/core/src/auth/refresh-token.ts:124-156`: why directly invoking official CLI cannot be asserted strictly read-only.

Public copies/manifests and the canonical research contract are under `D:/WILL/AGENT/CPA/ops/implement-spec92-20261102/development-completion/token-plan-public` and `D:/WILL/AGENT/CPA/ops/implement-spec92-20261102/development-completion/token-plan-contract.md`.

## Evidence and finite outstanding work

Tests exercise exported `bailianquota.RunCLI`, plugin `Manager.FetchQuota` and plugin `Manager.HandleManagement` with real synthetic helper subprocesses and a controlled HTTP boundary. Wrong Token Plan + optional Coding Plan mixed requests were observed in the original RED. The legacy regression `TestManagementLegacyQuotaBulkAndSingleNeverAttributeCLIReading` covers CLI and cache-only, each with bulk and both single-key selections: reverting only the production projection fragment reproduces the erroneous authority/windows; the fail-closed fix passes. Native CLI/cache tests independently retain all three fixture fractions and observation/provenance metadata.

This is **supplementary Qwen component evidence**, not Manager -> SQLite -> actual Core end-to-end evidence or real account verification. The local Core checkout is used only as an external test-module replacement for the SPEC92 metadata SDK; Core/Manager sources are not modified by this task. The published v8.0.0 SDK alone lacks the observation fields; use a pinned integration Core and matching Go requirement, keeping temporary mod/sum files outside this repository.

Evidence and exact counts/terminal receipts: `D:/WILL/AGENT/CPA/ops/implement-spec92-20261102/development-completion/qwen-coding-implementation`.

Legacy authority RED/GREEN logs and external modfile/SDK pin: `D:/WILL/AGENT/CPA/ops/implement-spec92-20261103/qwen-legacy-authority-20261103-7d4c`.

Core's SPEC92 local enrollment/receipt integration is the policy gate, not a missing plugin verifier and not a helper boolean. Still outside this Qwen component evidence: independent remote selected inference key -> exact account/Coding Plan instance -> query-principal verification, the agreed Manager/SQLite/real-Core controlled-upstream acceptance seam, and separately authorized live enrollment/acceptance. Local generation enrollment must not be described as proof of remote ownership. BSS balance, deployment and UI work are not part of this patch.
