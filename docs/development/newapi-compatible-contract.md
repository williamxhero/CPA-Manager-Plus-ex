# Explicit NewAPI compatible contract (#74 controlled native-quota increment)

This is a controlled native-quota increment, **not** a live-source readiness
assertion or completed ticket. After parent-authorized #73 integration, the
existing Manager dispatcher, authenticated configuration API, encrypted SQLite
jobs/status and provider drawer share `Observation.nativeQuota`. There is no
separate query service, scheduler, store or wrapper result schema. Confirmed
currency conversion is still unimplemented and rejected, not inferred.

## Protocol and authorization

The existing `balance.Querier.Query` dispatches an explicit `QueryTarget.NewAPI`
contract using the host credential resolver and shared `NetworkOptions`.
The adapter never discovers a
protocol, version, user ID, account, unit or secret from a hostname/provider
family. Required `protocolConfirmed` and nonempty `instanceVersion` represent
administrator confirmation of that instance contract, not remote verification.

| Explicit adapter ID | Exact GET path | Explicit auth profile | Semantic |
|---|---|---|---|
| `newapi-user-self-v1` | `/api/user/self` | `user-pat-with-user-id-v1` or `user-pat-v1` | account wallet in native quota |
| `newapi-token-usage-v1` | `/api/usage/token/` | `inference-key-readonly-v1` | key remaining in native quota |

Self requires a separately provisioned user PAT, explicit confirmed account
scope and positive canonical `userID`, checked against response `data.id`.
The with-user-id profile sends `New-Api-User`; the PAT-only profile does not.
Choose only the operator-confirmed auth variant; there is no fallback or retry
with other credentials. Token usage sends only the explicitly resolved relay
key. No cookies, login, sessions, arbitrary JSON paths, scripts or browser access.

Native account scope alone does not implement dedupe: the existing host must
retain complete source/instance/account identity, target and version fences.
Cross-provider shared-account single-flight belongs to later host scheduler
work, not a new adapter-owned mechanism in this increment.
Two keys at one domain do not prove a shared account. Read-only usage on depleted
or expired keys is possible upstream, but disabled tokens/users or IP restrictions
still reject it. An observed quota is not proof of inference eligibility.

## Native units are not money

The existing persisted `Observation` has a typed `nativeQuota`:

```json
{"semantic":"account_wallet","queryStatus":"success","nativeQuota":{"unit":"newapi_quota","state":"finite","remaining":"-9007199254740993"}}
```

This excerpt omits ordinary observation metadata. No `currency`, `amountDecimal`,
DeepSeek `balances` components, availability flag or quota window is fabricated.
Remaining quota accepts canonical JSON integer numbers or integer strings,
bounded to 120 digits (implementation resource limit, not vendor precision).
No float, exponent/fraction coercion, rounding, clamping or missing-as-zero.
Finite signed account debt is retained exactly. The account quota is not derived
from used quota or token limits.

Token `unlimited_quota=false` requires valid `total_available`; true emits
`state=unlimited` and no finite remaining even when a large placeholder exists.
Absent/null unlimited flag emits `state=unknown`, no finite remaining. Neither is
zero/exhaustion/recovery evidence. `total_used`, `total_granted` and optional
permission-gated `user_quota` never become an account wallet in this contract.

No conversion exists in this increment. Currency/rate fields in configuration
are rejected as unsupported input; direct monetary query requests return
`needs_configuration` before network I/O, not an assumed USD conversion. Later
money readiness needs confirmed instance version, account binding, exact native
denomination/conversion and evidence; use #73's shared decimal helper, not a
second converter. Never assume 500000 quota/USD.

## Shared safety and host responsibilities

The adapter reuses the production `secureClient`, `forbiddenIP`, TLS trust,
whole-DNS-answer validation, numeric pinning, exact private permits, metadata
refusal, disabled environment proxy and no redirects. Default timeout is 15s,
maximum 60s and body cap 2 MiB. No copied HTTP transport. Paths are exact with no
query, fragment, userinfo or escaped path. Invalid configuration never sends the
secret. Response duplicates/case aliases, excessive nesting, HTML, error
envelopes, absent/null/invalid quota and mismatched users fail without raw payload,
credential or upstream error echo. 401/403, 429 and other failures are classified.

The host, not this adapter, must own encrypted write-only secret references, CAS
and authority-change clearing, accepted attempts, durable jobs/SQLite, last-success
retention/stale display, late-result fences, scheduling, dedupe and public UI.
No read-only observation is a policy/gate acknowledgement. Public host tests,
DeepSeek/DeepInfra regressions and independently-provenanced actual Core tests
exercise this chain; fixture success does not establish live readiness.

## Persisted configuration, authority and UI

`PUT /usage-service/official-balance` retains existing full `scope` and issued
`targets`, CAS `version`, explicit `secret`/`clearSecret`, `endpoint`,
`privateTargets` and management timeout. NewAPI additionally requires:

```json
{"adapter":"newapi-user-self-v1","newAPI":{"protocolConfirmed":true,"instanceVersion":"operator-confirmed-deployed-revision","auth":"user-pat-with-user-id-v1","userID":"42","accountScope":"independently-confirmed-account-reference","nativeUnit":"newapi_quota"}}
```

This excerpt is not a complete request or a real deployment mapping. Token
usage requires `inference-key-readonly-v1`, no `userID` and no implicit wallet.
Native unit must be `newapi_quota`, not USD or CNY. Currency/rate fields remain
unsupported and rejected while confirmed conversion is incomplete.

All persisted source-contract fields (account, user ID, auth profile, confirmed
instance version, protocol/unit and network policy) are authority. Empty secret
preserves only unchanged authority; changes clear the old PAT unless a new
explicit secret establishes the new authority through CAS. Config saves clear
old observations and source-version fences reject late results. Verified pure
boot/revision rebind still preserves durable authority and encrypted secrets.

Authenticated GET returns the nonsecret contract from SQLite, never secret
body echo, and performs no quota request. Same-source failures retain stale
last-success. Provider UI shows saved contract fields, exact native integers and
finite/unlimited/unknown states without invented currency or wallet components.
LiangReKui branding exposes only a **manual** editor with initially empty
protocol/endpoint; it never selects NewAPI or confirms authorization. Other
unresolved protocols are not auto-enabled. UI marks currency conversion
unimplemented and current live authorization/version/units unverified.

## Primary evidence and actual-source gaps

Upstream source was pinned on 2026-11-01 to
[`ea343c4a81151bfd537d33b66527c25fa21a1112`](https://github.com/QuantumNous/new-api/tree/ea343c4a81151bfd537d33b66527c25fa21a1112):
[router](https://github.com/QuantumNous/new-api/blob/ea343c4a81151bfd537d33b66527c25fa21a1112/router/api-router.go),
[user response](https://github.com/QuantumNous/new-api/blob/ea343c4a81151bfd537d33b66527c25fa21a1112/controller/user.go),
[token response](https://github.com/QuantumNous/new-api/blob/ea343c4a81151bfd537d33b66527c25fa21a1112/controller/token.go),
[auth](https://github.com/QuantumNous/new-api/blob/ea343c4a81151bfd537d33b66527c25fa21a1112/middleware/auth.go).
Published [management auth](https://docs.newapi.pro/en/llms.mdx/api/management/auth)
and [token usage](https://doc.newapi.pro/en/api/token-usage/) corroborate the
distinction. IDs version this parser contract, **not** a claimed deployment.

LiangReKui remains a NewAPI-branded candidate with unknown deployed version,
authorization, account binding and units. Waw/Top/Kele protocols remain unknown.
LAYSO's published docs provide no public balance REST. No operator contract for
original OneAPI/custom JSON or independently authorized local session connector
was supplied: none is implemented or claimed complete. No new platforms, real
site/account/billing/inference probes or runtime changes are part of this stage.
