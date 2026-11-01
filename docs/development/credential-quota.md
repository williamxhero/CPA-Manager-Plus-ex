# Scoped credential quota (SPEC92 / reused #75)

This module hosts credential **windows**, not wallets, inference admission or an
independent gate. Generic inventory `query=false` is intentionally unchanged:
that historic flag does not version this new operation. `gate=false` and
`policy_locked=true` remain unchanged. Neither a ready source nor a successful
quota observation acknowledges policy application.

## Version negotiation

Core resource-capabilities may add `credential_query`:
`{"version":1,"sources":true,"scoped_query":true}`. The authenticated read-only
`GET /v8/management/credential-quota/sources` must ALSO return
`{"version":1,"sources":[...]}` with a valid current scope/target roster.
Manager negotiates both reads, exposes `negotiated.credentialQuery` separately
from generic `query` and `independentGate`, and does not enable the operation for
old Core 404, absent/unversioned/malformed contracts or unknown versions. Query
availability is operation support, not universal current-source authorization.
The credential status/panel uses that same verified capability and the source's
own support state; it does not interpret generic query=false as a veto.

## Authenticated Manager interface

- `GET /usage-service/credential-quota`: current source roster including unbound
  and unsupported descriptors. This and every GET perform NO upstream quota read.
- `GET ...?authIndex=<current-selector>`: current verified full binding, protocol,
  source support/reason, distinct account provenance, manual-disabled fact,
  persisted source version, latest attempt and last success with timestamp/stale.
- `POST /usage-service/credential-quota`: `{scope,targets,auth_index,sourceVersion,version,
  timeoutMs?}`. Carries exactly all current producer targets, not a subset.
  Returns 202 and `{id,state,deadlineMs,query,requestedVersion}`. This is the shared
  manual/recovery/scheduler entry, with bounded single-flight and four slots.
- `GET .../jobs/<id>`: durable job/current identity readback. A host restart or
  management deadline cannot silently turn a persisted running attempt into a
  success. An interrupted/expired job remains diagnosable.

Missing/changed authority is 409, invalid request 400, occupied slots 429, generic
host failures 500. Panel authorization is required. Error bodies never reflect
Core/provider bodies or submitted secrets. OPTIONS does not read storage.

## Actual persistence and authority

New `credential_quota_sources` and `credential_quota_jobs` SQLite tables contain
NO tokens. Start atomically persists source CAS, accepted latest attempt and job
before upstream I/O; completion is fenced by source version and accepted job ID.
This follows #71's real atomic persistence pattern, not the unimplemented legacy
balance.Repository. Wallet/balance tables, adapters and services are separate.

Storage identity uses issued instance/resource/incarnation IDs, never labels,
filenames, ordering or secret hashes. Every target version, protocol and account
provenance participates in persistent authority. Scope boot/revision and current
runtime selector participate in operation authority. Verified rebind can retain
history, but old-boot history is stale until requery; in-flight old operations
cannot replace a newer accepted attempt. Source/target changes cannot reuse an
old success. Account provenance is NOT a credential ID or a cross-key wallet ID.

`balance.Observation` is reused with semantic `quota_windows`. Its `windowsJson`
contains the versioned Core observation: full binding, protocol/version,
timestamp provenance, received-at, upstream observed-at, complete flag, distinct
account provenance and typed windows (name/model, percentage, remaining fraction,
reset time, original source status). No monetary conversion is applied.

## Protocol and transport constraints

Native selected Codex OAuth uses ONLY fixed first-party wham. Intermediary/API
keys (including mixed file metadata) never use wham. Unknown native account
authority never supplies positive recovery evidence. Known OpenCode quota goes
through the host-selected plugin exactly once; the invocation-scoped bridge
requires exact selected key, GET, origin/path, bounded response and no duplicate
callback. `scoped_http` capability is required before invoking compiled plugins;
anonymous legacy callbacks cannot safely inherit quota operation context.

Original OpenCode raw status/completeness is parsed from that SAME response, not
from its older lossy normalized QuotaBucket. `rate-limited` below 100% remains
exhausted; missing windows remain partial. Qwen console/key ownership is still
an external gap: needs_configuration/account_authority_unverified, no automatic
recovery promise and no console command from this path. Other current sources
remain explicitly unavailable, never guessed from hostname/provider branding.

Management budgets are 15s default, 60s hard maximum, four concurrent jobs and
2MiB responses. TLS validation, entire DNS-answer checking, numeric pinning,
metadata/transition-IP refusal, no environment proxy and no redirect apply to
quota I/O only. There is no change to established inference stream timeouts.
Success timestamps come from validated upstream HTTP Date, with explicit
`upstream_http_date` provenance; Core receipt time is separate. A missing, old or
future origin timestamp is not fresh evidence. Partial/missing/expired windows,
429/auth/error/reset-at alone are never positive recovery evidence.

## Extension owners

- #76: use Sources/Status/Start/Job unchanged for browser-independent scheduling;
  include unsupported, manual-disabled and paused identities in progress.
- #77: independently consume fresh identity/source/version-bound authoritative
  observations, and implement/verify a real Core admission ACK. Do not interpret
  `recoveryEligible`, source-ready, query capability or SQLite state as that ACK.
- #93: the representative panel is mounted in account quota detail using only
  the current row auth_index. Move/reuse it for redesign; never reconstruct
  durable identity from labels or download Core credentials.

Compatibility endpoints and legacy UI remain separate historical surfaces; this
change does not migrate their global policy or claim a completed #93 redesign.
Rendered UI, scheduler/gate, full release/security regression, main promotion,
deployment and tracker closure remain parent-owned gates.

## Selected plugin wire and source revision

Every bound source and query carries `sourceVersion`, a secret-free query-semantics
revision distinct from credential identity and SQLite CAS `version`. It participates
in before/after checks, persistence and async fences. #76/#77/#93 must retain it.
The plugin adds `scoped_http` and `default_quota_base_url` on the JSON describe
wire using an anonymous struct embedding the published SDK response. No ABI or
module-version change is required. Canonical source/hash provenance for the small
packaging mirror is in `providers/opencode-go/QUOTA-MIRROR.json`.

Core resolves explicit selected-auth endpoint, then config, then the actual
selected plugin's described default; it never substitutes a guessed origin.
Canonical and older vendored native defaults differ and are not silently unified.
If the existing identity catalog cannot bind explicit file/default configuration,
its descriptors remain `runtime_binding_unavailable` and no query is made. A
bound group whose effective endpoints disagree is also refused, not repaired by
guessing identity. Qwen ownership remains an external unresolved source gap.

The representative panel invalidates persisted display on credential mutation,
even when a runtime selector is reused, and fences late async results. Ordinary
refresh failures retain last success and its original timestamp/staleness.
