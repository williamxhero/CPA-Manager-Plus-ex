# #95 E03 prep (2026-11-04) — build only, no install

This folder records a **clean build** of the ordinary untagged manager from
`spec92/conter-balance-method-map-20261104` commit `c745df5a`. It does **not**
replace a running service, touch production SQLite, or claim E03 complete.

## Build summary

See `BUILD-SUMMARY.txt` and `go-version-m.txt` (regenerated locally; `.exe` is
gitignored).

Expected fields when rebuilt from the same commit:

- path: `cmd/cpa-manager-plus`
- module: `github.com/seakee/cpa-manager-plus/apps/manager-server (devel)`
- SHA-256 of this build: `AE3060D3E6D1B00378AD7D906DC338D268E5F4CCB9A9D56B3B41C1E13F477040`

## Gate before authorized install

1. Operator explicitly authorizes local ex deploy with backup + rollback.
2. Copy `docs/verification/issue97-live-20261104/host-attestation.example.json`
   to the host attestation path and set `CPAMP_WEB_BROWSER_ATTESTATION` (or call
   `AttestWebBrowserHost` into that SQLite) with the live Edge instance id.
3. Configure LAYSO web source: accountId `h7w8`, tenant/role `Personal`,
   executionUser `will`, profile `Profile 1`, parsingRule
   `layso-dashboard-models-account-balance-v1`.
4. Smoke: official-balance DirectQuery returns Credits without HTTP self-certify.
5. Record capability readback + rollback drill — only then mark E03.

## Explicitly not done here

- No service stop/start
- No data-dir overwrite
- No push / PR / issue close
- #88 S03/S04 still need production panel binary with embedded UI hash match
