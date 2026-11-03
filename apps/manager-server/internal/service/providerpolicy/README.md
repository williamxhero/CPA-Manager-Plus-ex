# Provider monetary intent: default-off guard

This integration consumes the committed intent boundary from
`572ebbbf9827128d13c39f0e7702824333ed1eb7`. It does not consume uncommitted
monetary code from another worktree.

The public `GET/PUT /usage-service/provider-policy` endpoint persists explicit
OFF intent with a generation compare-and-swap and the existing fenced SQLite
lease. Its `cpa-manager.provider-balance.v1/` namespace is separate from the
administrative provider gate. Saving OFF never clears a gate or changes native
manual state.

Funding verification is not connected in this revision. Consequently status
is locked, `effective` and `applied` are false, and ON returns unavailable.
The missing-proof integration test checks real Core ledger invariance and
durable public readback after reopening temporary SQLite. It does not establish
that stale or mismatched funding evidence is rejected by a monetary engine.

The next required integration must exercise a successful, current per-member
official balance/key relationship as well as stale, mismatched and incomplete
proofs. Runtime key equality alone is not authenticated account identity or a
wallet total. Native decimal thresholds, automatic gate consumption, OFF with
a historical monetary pause, full lifecycle recovery and UI remain unfinished.
The administrative `/usage-service/provider-gate` retains its explicit operator
semantics and does not acquire a funding prerequisite.
