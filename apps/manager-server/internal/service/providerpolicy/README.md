# Provider monetary intent and current funding proof

This integration consumes the committed intent boundary from
`572ebbbf9827128d13c39f0e7702824333ed1eb7`. It does not consume uncommitted
monetary code from another worktree.

The public `GET/PUT /usage-service/provider-policy` endpoint persists explicit
OFF intent with a generation compare-and-swap and the existing fenced SQLite
lease. Its `cpa-manager.provider-balance.v1/` namespace is separate from the
administrative provider gate. Saving OFF never clears a gate or changes native
manual state.

`POST /usage-service/provider-funding-proof` accepts the issued full provider
Binding (`scope`, `targets`, `providerId`), not consumer-asserted evidence. It
reads each independently configured credential source from encrypted SQLite,
requires a current successful official DeepSeek balance observation, validates
complete actual Core routing coverage, and challenges every current runtime
member using its source credential. The nonce-bound challenge and response are
checked against the full current group, source version and runtime locator.
Source and Core identity/connection checks close the read transaction logically;
a concurrent change, incomplete group, stale balance, wrong key or mismatched
response fails closed. Verification never performs another balance collection.

The response is a request-local proof of independently queried per-member
balances. It returns original currency/decimal strings, source versions and
success times; it never infers account identity, wallet equivalence, summed
money, a durable gate permit or an applied policy acknowledgement. Reopening
SQLite preserves the source evidence; the next proof revalidates actual Core.
The first adapter is the exact official DeepSeek balance endpoint. Unsupported
or shared/group query sources do not establish all-member monetary coverage.

The API can return a successful funding proof while policy is still default
OFF. The automatic monetary engine is not integrated: policy status remains
locked, `effective` and `applied` are false, and ON returns unavailable. Native
decimal thresholds, automatic gate consumption, OFF with a historical monetary
pause, refresh subscription, full lifecycle recovery and UI remain unfinished.
The administrative `/usage-service/provider-gate` retains its explicit operator
semantics and does not acquire a funding prerequisite.
