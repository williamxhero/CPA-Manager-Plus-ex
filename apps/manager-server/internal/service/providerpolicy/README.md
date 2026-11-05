# Provider monetary policy and current funding proof

This integration consumes the committed intent boundary from
`572ebbbf9827128d13c39f0e7702824333ed1eb7`. It does not consume uncommitted
monetary code from another worktree.

The public `GET/PUT /usage-service/provider-policy` endpoint persists explicit
ON/OFF intent with a generation compare-and-swap and the existing fenced SQLite
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
OFF. Explicit ON requires current full funding proof and valid finite amounts.
It pins source/runtime versions without consuming cached values. The existing
refresh coordinator captures each query's enabled generation before upstream
I/O and notifies policy after durable completion. Complete post-enable fresh
evidence at every member/currency <= 0.1 pauses the entire current group; a new
post-enable success > 0.1 at a verified routed member clears this owner alone.
Exact bounded decimal rationals are compared without floats, rounding or sums.
Unknown, failed, stale, wrong-key, replaced-source/identity/generation evidence
cannot authorize a change; a failed peer does not prevent a new high member's
verified recovery. OFF retains an existing restriction as `historical_pause`.

SQLite stores query fences, one-use decision consumption and immutable Core
commands before RPC. Both save and send validate the shared fenced lease and
current intent. Final source/connection checks precede the send; Core validates
full route/source/CAS authority independently. Applied status requires its real
durable receipt and ledger. Pending/rejected commands are unavailable, never
optimistic success. Public `evaluation` reports the latest sanitized per-query
pending/evaluated/rejected outcome, generation and consumption without secrets.
Both the public funding proof and final send close all source versions and
successful attempt IDs in one SQLite snapshot after network reads; a source
replaced while another is checked cannot authorize a write. A journal known
to have failed authorization before its sole RPC is marked unsent, so future
new evidence can progress; a transport-unknown outcome remains pending.
Downstream proof work has a separate maximum20s inline callback budget, uses
the existing four query slots and is canceled/waited at coordinator shutdown.

Reopening SQLite preserves intent, fences and confirmed Core restrictions; a
new successful query may decide against that durable state. Interrupted or
unknown commands are NOT blindly retried. A production lifecycle worker now
rotates through saved subjects with at most one 15-second attempt per tick. It
only reads and CAS-persists a verified receipt for an existing immutable command,
under current complete routing/identity/source authority; an unavailable receipt
never authorizes replay. Cancellation joins the worker before SQLite closes.
The provider detail and name-leading entrance expose explicit ON/OFF intent,
effective/applied/manual state, persisted receipt and exact per-member native
amounts/lastSuccess/stale through the public management API. Failed reloads keep
the snapshot and lock writes until authoritative readback; auth/binding changes
hide the old scope. Ordinary builds exclude fixture DNS/socket/TLS overrides.
`POST /usage-service/provider-policy/recover` is a separate explicit admin
override for an OFF historical monetary pause. It requires current complete
issued binding/routing/connection proof, policy and gate generations and the
current route source version. It never needs a new high balance or changes
ON/OFF/manual intent. The fenced OFF row is validated independently of automatic
money evaluation's mandatory source fences. Only the installation's monetary
owner is selected server-side; administrative and other owner gates remain.
The UI requires confirmation tied to the current auth/binding context, policy
generation, gate generation and source version. Mutation errors cause GET
readback, never automatic POST retries or optimistic success.

An unknown dispatch stays pending across retries/restarts; only read-only
receipt reconciliation may confirm it. A journal proven canceled before its
sole RPC permits a new confirmation and fresh immutable command under restored
authority. Manual disable can remove Core runtime route authority: recovery
then fails closed and never enables native configuration to fabricate proof.
Unknown missing-command disposition, all-row live summaries, additional
adapter/funding coverage, visual/browser and
operational acceptance remain unfinished.
Only the independent worker branch implements this slice; any other candidate
requires explicit reconciliation before integration. Fixtures are not real
account acceptance and the entire #82 SPEC is not complete.
The administrative `/usage-service/provider-gate` retains its explicit operator
semantics and does not acquire a funding prerequisite.
