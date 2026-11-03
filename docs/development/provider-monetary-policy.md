# Provider monetary policy

This development candidate adds an independent, default-OFF provider policy.
Saving a switch is not an inference acknowledgement. Administrative provider
pauses, credential restrictions, manual disabled state and monetary ownership
remain orthogonal. This document is not deployment or whole-SPEC acceptance.

## Current proof boundary

The first supported funding source is the official DeepSeek balance adapter.
The configured query credential must independently match each claimed native
OpenAI-compatible runtime member through the Core v8 online HMAC challenge.
Only the exact official DeepSeek inference origin qualifies. The challenge
returns runtime-key equality only: no account identity, shared wallet identity,
stable key fingerprint, money or transport attestation is invented.

A full issued provider group query can cover only members whose actual key
matches its query credential. Other keys require explicitly configured
per-member official sources. Complete current issued membership and native
HTTP/SSE/WS eligibility are preconditions, not substitutes for funding proof.
NewAPI wallet labels, provider display names and same-origin endpoints cannot
establish provider-wide monetary authority in this slice.

## Intent and observation

- GET `/usage-service/provider-policy?providerId=<issued UUID>` is read-only.
- PUT `/usage-service/provider-policy` requires the complete current parent-first
  scope/targets, providerId, expectedGeneration and explicit enabled boolean.
- No record means OFF. A saved explicit intent, generation and persistent owner
  survive Manager/SQLite restart. CAS or identity conflicts are not successful
  saves. OFF may be saved when a previously ON source becomes unavailable.
- Only new successful queries captured under the current enabling generation
  may authorize an automatic decision. Saving ON, cached reads, reset times,
  failures, source replacement and status GETs do not manufacture new evidence.
- The distinct owner is `cpa-manager.provider-balance.v1/<installation>`.
  OFF does not clear this owner's existing pause and never changes manual state.

## Native-currency decision

Balances are bounded exact decimals, not floating-point estimates or a sum.
Every currently covered routing member must have a fresh, supported official
wallet observation; each reported native currency must be <= 0.1 to pause.
Missing, NULL, non-finite, malformed, stale or unknown sources prevent an
all-provider low decision. Negative debt and explicit zero remain real values.
A new verified high (> 0.1) source can clear only this policy's restriction.
Consecutive low observations do not repeatedly pause or oscillate the gate.

Source completions are deduplicated by durable attempt identity. Independent
source start order is not a provider-wide sequence watermark: A-start,
B-start/B-finish, A-finish must preserve A's current valid observation.

## Durable recovery

A lease-fenced policy decision and its evidence snapshot are durable before
an external Core operation. The separate gate journal records the immutable
opId/body before POST. A saved ON or attempted command is never displayed as
an applied acknowledgement. Read-only receipt and current-ledger confirmation
can recover an accepted operation whose response was lost.

A retry which could first-apply an unaccepted command must revalidate current
policy generation, exact runtime/source binding and current monetary evidence.
An old high clear is refused when newer low, failed, expired or reconfigured
source evidence is durable before its final local pre-send validation.
Genuine unresolved external outcomes remain
pending, not fabricated success. A provably unattempted or definitively obsolete
missing operation can retire its local decision under CAS without removing any
owned pause; the decision journal retains its original evidence and outcome.

Both initial monetary dispatch and immutable replay require a local evidence
guard AFTER the final authenticated remote preparation read. It checks the
lease, exact intent generation/document, current source versions, successful
observation identities and freshly recomputed monetary validity against the
closed issued inventory. A new journal also receives a second check immediately
before POST; failure preserves its immutable unknown state, never an invented
receipt. Confirming an already accepted receipt does not require old money to
remain fresh. Manual gate paths do not acquire monetary authority through this
guard. There is no additional network proof or persisted proof cache.

This closes changes during the remote preparation chain. It does not serialize
a later source publication with a command already sent to Core or cancel an
already admitted exchange. Unknown-state terminal recovery and any stronger
cross-service ordering guarantee remain separately identified boundaries.

Provider-local unavailability does not veto unrelated refresh admission or
independent policies. Pending decisions and source completions share one durable
frontier; failed items rotate behind unattempted work across both kinds.
The scheduler observer uses at most five seconds or one third of its caller's
remaining budget, reserving time for source acquisition. Waiting for a concurrent
policy operation is context-bounded too. Wall-clock rollback cannot reset the
durable queue priority. Recovery selects persisted
terminal jobs, so abandoned running captures cannot hide a later completed job.
No failed/interrupted job is reclassified as a successful balance read.

Completion callbacks only persist the durable inbox and wake an owned monetary
worker. They do not perform authenticated Core proof/dispatch inside the refresh
coordinator's shared five-second notification deadline. The worker has a separate
15-second pre-lock lifecycle budget, recovers on startup and periodic wakes, and
is cancelled and joined by the same host lifecycle before SQLite closes. One
proof/dispatch chain can use the remaining lifecycle budget; durable rotation,
not a repeatedly expiring five-second chain, provides cross-item fairness.
After a slow or failed pass, a mandatory five-second post-release backoff cannot
be bypassed by queued wakes. This leaves the shared lease available to credential
policy instead of consuming buffered ticks in a provider-only retry loop.

## Development evidence and remaining boundaries

Public Manager API -> temporary SQLite -> pinned ordinary Core tests use only
controlled official-host TLS upstreams and workspace-local synthetic identities.
They are not real account, deployment or browser-profile verification. The
representative native provider list/detail UI has a non-sliding name-prefix
mark, independent manual state, explicit readback, and stale callback fencing;
all #84 consumers and real browser visual acceptance remain separate work.
An accepted switch save followed by failed readback is shown separately and
remains write-locked until a fresh GET. A management-identity rejection revokes
both mounted same-authority list/detail marks and captured mutation handlers,
without triggering automatic auth retries or replaying the user's intent.

Refer to the external `ops/needs-forever-all-specs-20261103` evidence records for
exact commits, source hashes, RED/GREEN runs, current review findings and pending
gates. Historical green bundles do not include new candidate changes.
