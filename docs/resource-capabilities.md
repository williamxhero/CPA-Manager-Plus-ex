# Resource capability support summary

This is a **support-only, negative/unknown first vertical pass** for
[T01](https://github.com/williamxhero/CPA-Manager-Plus-ex/issues/70), under
[SPEC 66-A](https://github.com/williamxhero/CPA-Manager-Plus-ex/issues/79) /
[parent SPEC](https://github.com/williamxhero/CPA-Manager-Plus-ex/issues/66).
It does not complete those requirements or enable independent automatic protection.

## Manager-owned API

`GET /usage-service/resource-capabilities`

The manager handles this route directly; it is never proxied to CPA core.
GET requires the existing manager panel authentication (`Authorization: Bearer
<manager-admin-key>`, verified by `middleware.AuthorizePanel`). There is **no
setup or unconfigured-manager authorization exception**. Authorization completes
before policy settings are read. Authentication itself reads the existing admin
credential; it does not contact core.

The exact successful response contract is:

```json
{
  "schemaVersion": 1,
  "identity": {"state": "unknown", "reason": "core_contract_unverified"},
  "query": {"state": "unknown", "reason": "query_contract_unverified"},
  "independentGate": {"state": "unavailable", "reason": "consumer_contract_unconfigured"},
  "policy": {
    "source": "startup",
    "codexQuotaCooldown": {
      "enabled": false,
      "configured": false,
      "source": "startup",
      "locked": false,
      "envKey": "USAGE_QUOTA_COOLDOWN_ENABLED",
      "configFileKey": "quotaCooldownEnabled"
    },
    "authIssueQueue": {
      "enabled": false,
      "configured": false,
      "source": "startup",
      "locked": false,
      "envKey": "USAGE_ACCOUNT_ACTIONS_ENABLED",
      "configFileKey": "accountActionsEnabled"
    },
    "authIssueAutoDisable": {
      "enabled": false,
      "configured": false,
      "source": "startup",
      "locked": false,
      "envKey": "USAGE_ACCOUNT_ACTIONS_AUTO_DISABLE",
      "configFileKey": "accountActionsAutoDisable",
      "dependsOn": "authIssueQueue"
    }
  }
}
```

The `policy` above illustrates startup settings with all flags false. In actual
responses it is the **existing `automation.Status` JSON shape, unchanged**:
current effective/configured flags, `source`, environment locks and field keys,
plus `updatedAtMs` when present. Sources remain `startup`, `database`, or `env`,
with existing precedence and dependency behavior. These are policy settings only,
not capability negotiation or an application acknowledgement (ACK). A true
policy flag never changes `independentGate` to `available`.

`schemaVersion: 1` versions this manager response shape only. It is not a core
version, resource version, inventory version, or enforcement version.

## State meanings and UI boundaries

| Field | State / reason in this pass | Meaning |
| --- | --- | --- |
| `identity` | `unknown` / `core_contract_unverified` | A persistent core instance/resource identity contract is not verified by this consumer. |
| `query` | `unknown` / `query_contract_unverified` | The new query capability contract is unverified; existing legacy quota functionality is not being declared unsupported. |
| `independentGate` | `unavailable` / `consumer_contract_unconfigured` | This manager has no implemented independent-gate consumer contract. This is a consumer limitation, **not proof that deployed core is unsupported**. |

This is a **manager-wide support summary**, not a bound per-resource inventory,
resource routing state, or evidence that a particular resource is protected. It
contains no resource/scope IDs, persistent UUIDs, protected/routable booleans, or
fabricated core versions. Even an unexpected future global `available` value
must never be interpreted as a per-resource positive ACK.

The `query: unknown` value must not hide or disable currently supported legacy
quota UI. Manual disabled state, excluded models, cooldown success, and policy
flags are not independent gate application ACKs. This pass neither reads core
inventory nor changes those manual/policy semantics.

## Methods, errors, and side effects

- GET is the only data operation. OPTIONS follows existing CORS conventions:
  `204`, empty body, no authentication or policy-storage read.
- Every other method (including HEAD, POST, PUT, PATCH and DELETE) returns `405`
  with `Allow: GET, OPTIONS`; request bodies are not applied.
- **All handler paths**, including preflight, authentication failure, storage
  failure and method rejection, return `Cache-Control: no-store`.
- Invalid/missing credentials return the existing `401` error shape, normally
  `{"error":"invalid admin key","code":"invalid_admin_key"}`. An uninitialized
  admin credential or auth-storage error follows existing middleware semantics:
  `500`, `{"error":"authorization failed","code":"request_failed"}`; it never
  permits a setup bypass.
- A policy-storage failure returns `500`,
  `{"error":"resource capabilities unavailable","code":"request_failed"}`.
  No stale/default/partial successful summary is returned. Raw storage/auth
  errors, URLs, keys and private details are not returned or logged by this path;
  the shared auth-failure logger now records only generic authorization failures.
- The endpoint makes **zero upstream/provider/core HTTP requests and zero
  persistence writes**, even with a saved CPA connection. It only uses existing
  authentication and policy reads. It does not configure gates, migrate data,
  schedule jobs, or apply pause/resume operations.
- The existing `/usage-service/account-processing-policy` API and legacy quota
  APIs retain their existing behavior and environment locks.

## Outstanding T01 and parent requirements

This pass leaves the following work outstanding; its tests do not replace any
of these acceptance requirements:

1. Verify an actual, owned core contract for persistent instance/resource
   identity and independent automatic-policy application acknowledgements.
2. Implement a complete resource inventory, **including manually disabled
   resources**, with versioning and lifecycle isolation across instance changes,
   reorder, key/account changes, delete/recreate, and provider member changes.
   Names, positions and display keys cannot become durable identities.
3. Bind capability and actual gate state to verified resources/versions. Keep
   manual intent, policy configuration, persisted intent and actual application
   ACK separate, including conflicts, unknown outcomes and recovery behavior.
4. Deliver and verify the real core integration and real routing acceptance,
   including isolation of resources, new attempts/retries/failover, manual and
   independent gate interaction, quota queries, failure injection and restart
   behavior. No enforcement adapter or transport is asserted by this pass.
5. Complete the remaining safety/migration/rollback requirements in the broader
   tickets and obtain real routing evidence and independent Hermes acceptance
   for the fixed manager/core versions before declaring completion.

## Data compatibility and rollback

This is an additive read-only API over existing policy storage. **No new DB
schema or data migration is needed.** Rolling back this support-only pass removes
its route and UI display/integration; existing persisted policy, manual state and
connection data remain unchanged. This does not prescribe a rollback procedure
for any future implementation that actually persists or enforces new gate state.

See [backend verification notes](verification/issue70-resource-capability-support-pass.md)
for the bounded HTTP-seam checks and known test-scope limits, and the
[frontend verification and independent review record](../apps/web/src/features/resourceProtection/README.md)
for standard frontend tests, builds and remaining browser/acceptance limits.