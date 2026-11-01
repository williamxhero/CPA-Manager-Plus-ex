# Resource capability and durable inventory consumer

Ticket #70 reused by SPEC #92. This Manager consumer reads the supported Core v8
catalog; it does **not** implement queries, policy writes, independent gates or
an applied routing ACK. Credential switch redesign belongs to #93, gates to #77.

## Public API and compatibility

`GET /usage-service/resource-capabilities` retains the existing schemaVersion=1
support summary and unchanged automation `policy` shape. Without a query it still
makes zero Core/provider requests and zero persistence writes. Existing policy
flags and environment locks are configuration, never protection evidence.

`GET /usage-service/resource-capabilities?inventory=1` adds `negotiated` to that
same summary. Existing fields retain their legacy meanings for old consumers;
new consumers must use `negotiated` for catalog observations, not reinterpret the
legacy `identity: unknown/core_contract_unverified` field as a negotiated result.
Older Managers may ignore the query and return only the legacy summary.

`?inventory=1&bindings=1` additionally consumes the actual producer's authenticated
`/v8/management/resource-bindings`. This is what the shared credential UI requests.
No successful catalog read alone is treated as a runtime-row binding.

Both variants require the existing Manager panel admin authentication, before
settings or Core reads; no setup bypass exists. GET/OPTIONS only, all responses
`Cache-Control: no-store`; OPTIONS has no auth/storage/Core work and other methods
return 405 (`Allow: GET, OPTIONS`). Core's management key is resolved server-side
using the existing saved Manager-config / legacy-setup / environment authority
rules. No request parameter supplies a Core URL, key or resource selector.

## Negotiated shape

- `contractVersion`: Core catalog contract version (1 when recognized; 0 if not
  negotiated), distinct from Manager schemaVersion, build version and revisions.
- `identity`: `available/durable_inventory` only after a complete validated
  snapshot bracketed by matching Core scope capabilities. This means the catalog
  mechanism is available, **not that every row is unambiguous or routable**.
- `snapshot` (only on success): the complete secret-free Core v1 projection:
  instance_id, instance_incarnation, boot_epoch, snapshot_revision, version,
  complete, resources. Each row retains resource_id, incarnation, identity_version,
  credential_version, config_version, membership_version, kind/family, parent_id,
  members, manual disabled/excluded/zero_weight, identity/catalog/runtime readiness,
  and advertised inventory/query/gate flags. Disabled rows are not filtered.
- Optional `source_bindings` carries full counterpart Target tuples and
  `binding_state` retains known Core plugin projection states. Qwen/OpenCode Go
  families are retained, not silently discarded or merged by names/keys.
- `bindingCapability` (when requested): available/host_issued_bindings only after
  validating all binding scopes and exact target tuples against the complete
  snapshot. `bindings` retains auth_index, runtime_alias, scope and targets.
  The current host's 16-hex-digit index is a scoped lookup selector, not durable
  identity. Empty/absent bindings never certify runtime coverage; missing/old/
  failed binding APIs produce unavailable and no associations, without falling
  back to filenames or secret hashes.
- `query`: unknown until a valid complete inventory, then
  unavailable/query_consumer_unconfigured. This consumer does not implement an
  official query adapter, even if a future Core advertises query capability.
- `independentGate`: always unavailable/consumer_contract_unconfigured.
- `policyLocked`: true: this consumer cannot enable/apply independent protection.
  Existing automation environment locks remain separately available in `policy`.

All Scope and Target fields form observation preconditions; IDs alone are
insufficient. Keep integer counters unchanged. Values outside JavaScript's safe
integer range are rejected, never rounded. No local IDs are minted, no identity
cache or policy/observation table is added, and no balance is associated here.

Unproven rows can retain empty target tuples with ambiguous/not_ready; those rows
are not action targets. Duplicate IDs, malformed parent/member graphs, mismatched
source-binding targets, unknown enum strings, missing/null manual-state fields,
invalid IDs/counters or unsafe sizes fail closed for the whole read. A partial
inventory is never relabeled complete or substituted with legacy credential lists.

## Read flow and fencing

Only these authenticated read-only Core endpoints are used:

1. `/v8/management/resource-capabilities`
2. `/v8/management/resources` if inventory AND durable are advertised
3. `/v8/management/resource-bindings` when requested (no v0 fallback)
4. capabilities again, matching instance, incarnation, boot and revision

Core v1 supplies one complete atomic inventory, not a paginated list. The consumer
requires `complete=true`; it does not assume a legacy auth-files response or a UI
page is complete. Re-resolving the connection after I/O fences changed connection
configuration/updatedAt, including negative results. No response is cached or
persisted; frontend request-generation/context/unmount fencing rejects older
completions. A read can naturally become stale after its last check: future
mutation consumers must revalidate the entire tuple atomically at Core. This read
is not such a mutation/admission primitive.

Unsupported/off modes are unavailable with the Core allowlisted reason:
not_enabled, unsupported_mode, unsupported_storage, unsupported_filesystem,
not_ready. Transport/auth/version/malformed/stale cases are unknown with safe
reason codes (core_unavailable, core_auth_error, unsupported_contract,
invalid_contract, stale_inventory, connection_changed, connection_unavailable,
connection_unconfigured, inventory_unavailable). Failure has no successful stale
snapshot. Core disabled mode stays disabled: probing never enables enrollment or
changes plugins. A newer Core may support known plugins under v1; mode support is
negotiated, not inferred from historic configuration or hardcoded plugin exclusion.

## Security and side effects

Core transport preserves TLS verification, refuses every redirect, bounds each
read to 15 seconds and 2 MiB, and returns no raw response/error text or URL. Unknown
JSON fields are discarded; string fields are allowlisted opaque IDs/enums. Only
the trusted admin-configured connection is used (including intentional loopback
connections); this is not an arbitrary-provider billing transport or an SSRF/DNS
policy implementation for later balance adapters. No Core config/auth-file download,
provider enumeration containing keys, management api-call, legacy disabled PATCH,
model exclusion, routing mutation, platform quota call or live enrollment occurs.

## Representative UI

The shared page-level capability loader opts in once, not per row. Existing
credential table/card and provider indicators explain:

- unknown: unverified/failed negotiation;
- unavailable: missing integration/catalog support;
- unsupported_mode: the actual Core mode blocker, not a healthy/protected label;
- identity_only: verified runtime binding or issued native-source marker, but query/gate consumers unavailable;
- unbound: source inventory is available but this row has no verified runtime
  association or issued native source marker.

All four locales have accessible tooltip text. A credential's current auth_index
is looked up only in the current negotiated scope. Its displayed identity comes
from producer-issued runtime_alias + full Target tuples, never from that index,
name, position or key. The tooltip exposes only the sanitized Scope/Targets.
Source list changes/loading, connection/revision changes, refresh and unmount
invalidate old completions. Native source APIs retain issued resource-id markers
through all supported provider normalizers and edit/save serialization. The UI
resolves only unique markers against ready catalog rows with matching kind/family;
OpenAI groups additionally require the exact complete member marker set and parent
relations. No labels, key hashes, array slots or auth indices establish source
identity. Unmarked/ambiguous/mismatched rows remain unbound. Native source evidence
is not proof of runtime eligibility or runtime-binding coverage. The tooltip
contains only the actual current Scope and full Target versions. Marked edits
match the issued source marker (including reordered same-name OpenAI groups);
ambiguous marked edits fail rather than attaching the marker to another row.
Unmarked legacy manual editing remains compatible, but never creates trusted IDs.
Manual switches, copying, row keyboard actions, legacy quota and settings remain
unchanged. No automatic switch is enabled by catalog availability.

## Migration and rollback

No Manager DB schema/data migration: the only SQLite use is existing auth, policy
and saved connection resolution. Core remains the identity authority. Durable
catalog enrollment is a separate explicit Core operation with its own source
marker/backups/rollback requirements; this Manager read never performs it.
Reverting this additive consumer/UI returns to the legacy summary without touching
saved connection, manual intent, policies or balances. Do not delete Core identity
state or reassign IDs as a rollback shortcut.

## Acceptance limits

HTTP/temporary-SQLite tests cover supported/unsupported modes, disabled rows,
version projection, rename/key/recreate/instance read isolation, late Core revision
and connection completions, authorization, secret canaries and transport failures.
An opt-in real Core process test independently exercises restart, rename and key
replacement using synthetic source files, temporary SQLite, a controlled denial
proxy and no live services. The Core worker separately owns known-plugin identity
and source lifecycle proofs; its evidence is not implied by Manager test success.

Remaining #70 acceptance: run the opt-in actual-runtime association exercise
against the **delivered producer commit**, not just the original Core binary;
rendered-browser layout/keyboard verification and independent full-ticket review.
Supported native provider marker consumption is implemented; rows without current
proof intentionally remain unbound. The
producer pointer is ops/implement-spec92-20261101/CORE-IDENTITY-CONTRACT.md at the
project root; its in-progress status is not acceptance evidence.
No full-ticket closure or SPEC #92 completion is claimed. Real query/gate ACK,
automatic intent/switch behavior, routing and deployment are later tickets, not
capabilities conferred by this read. See verification/issue70-spec92-identity.md.
