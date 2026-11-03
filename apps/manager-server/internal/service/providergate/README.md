# Explicit provider gate

`GET /usage-service/provider-gate?binding=<JSON>` reads a current issued group.
`PUT /usage-service/provider-gate` accepts `version: 1`, that complete binding
(`scope`, `targets`, `providerId`), `sourceVersion`, `expectedGeneration` and an
explicit `pause` or `clear` action. Panel authorization is required. Unknown
fields are rejected. There is no automatic policy switch in this API.

The local provider selector is verified against current inventory. The Core
adapter sends the canonical parent-first complete group, without `providerId`,
to `/v8/management/inference-gates/providers/eligibility`. Every actual member
must have a matching issued runtime binding and all three supported routes.
Only `authority: routing_only` is accepted. This proves no funding ownership,
wallet completeness, currency threshold or permission for automatic action.

SQLite owns a separate installation identity, generation CAS, immutable command
history and receipts. The command is durable before any Core write. Applied
means its operation receipt and the current ledger agree; a historical HTTP 200
alone is insufficient. A fresh issued binding may confirm the same persisted
restriction after Core restart. An explicit new command always requires the
current route proof and source version. `clear` removes only this installation's
provider gate; it does not change manual disabled flags or other owners.

Lost ACK recovery reads the immutable operation and the current ledger without
replaying it. An absent operation is conclusively expired only when the current
Core revision has advanced beyond its immutable expected CAS revision. At the
same revision the outcome stays pending and further writes fail closed. There
is no automatic retry/reconciliation scheduler in this slice. Complete monetary
policy, durable reconciliation UI, funding verification and live acceptance
remain separate work.

The opt-in `TestProviderGateActualCorePublicPersistenceAndIsolation` runs the
public API against temporary SQLite and a clean ordinary Core artifact with
loopback inference and billing dependencies. Its process metadata, raw logs and
SHA256 bind the fixture to the source commit. Owned forced cleanup exit codes
are recorded separately from the Go test result; this is not real-account or
graceful-shutdown acceptance.
