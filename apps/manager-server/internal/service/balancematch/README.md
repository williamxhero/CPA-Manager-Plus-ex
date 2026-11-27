# Official balance automatic matching (#105)

`POST /usage-service/official-balance/match` uses the same Manager admin auth as
`/usage-service/official-balance`. Send `{"binding":{"scope":{...},"targets":[...]},"apply":false}`
with a current Core-issued binding. Omit `apply` to preview. The server reads the
bound Core v8 API-key group; the caller cannot supply a URL, key, or private
address exception. Use the current binding again to replace a stale automatic
registration. Existing registrations without automatic provenance are manual.

A successful HTTP response has `status` (`matched` or `unmatched`), `confidence`
(0–100, zero on `unmatched`), `candidates`, and `applied`. A match also has
`adapter`, `endpoint` and `evidence`. `reason` explains an unmatched result;
`applyReason` explains a match that an explicit apply could not register (e.g.
`manual_configuration`). Stale Core bindings or concurrent registration writes
return HTTP 409 (`identity_changed` or `configuration_conflict`).

Each candidate has `adapter` and `evidence`: `source`, `host`, `adapter`, `url`,
HTTP `status` (0 if no HTTP response), `matchedFields`, UTC `timestamp`, `score`,
and optional `reason`. Fingerprints produce evidence without any vendor request
and have no probe candidates. URLs contain only the HTTPS origin and fixed
adapter path. Neither response bodies, response headers, nor field values are
returned as evidence. `matchedFields` describes the contract, not balances.

Probes are ordered DeepSeek, DeepInfra, then OpenAI-compatible monetary usage
(WawAPI). DeepInfra and compatible usage specify USD in their existing contract,
not in a response field. HTTP 200 scores 20, valid adapter response adds 60,
and JSON content type adds 20 to a valid response. The sole leader must score
at least 90 after all candidates receive their verdicts. The default budget is
four concurrent match calls, 250 ms minimum between probe starts per normalized
hostname, and ten seconds overall. No redirects, retries, or pause actions.

An access with multiple distinct keys may be fingerprinted but is not probed
or auto-registered: its binding does not identify one balance credential. OAuth
and plugin credentials and protocols requiring NewAPI confirmation, signed cloud
billing, or browser/session enrollment remain outside automatic matching.
