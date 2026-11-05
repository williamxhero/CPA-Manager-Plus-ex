# Host bsk relay for LocalCPAManagerPlus (2026-11-04)

`LocalCPAManagerPlus` runs as `NT AUTHORITY\LocalService` and cannot see the
interactive BrowserSkill instance. OfficialBalance web adapters need `bsk`.

## Layout (runtime only)

| Path | Role |
|---|---|
| `runtime/CPA-Manager-Plus/bsk-relay/bsk-relay-agent.ps1` | User-session TCP agent on `127.0.0.1:18717` |
| `runtime/CPA-Manager-Plus/bsk-relay/bsk-relay-client.ps1` | Optional PowerShell client |
| `runtime/CPA-Manager-Plus/bin/bsk.exe` | Go forwarder preferred by WinSW `PATH` |
| WinSW env `PATH` | `…\CPA-Manager-Plus\bin;…\.local\bin;…` |
| Scheduled Task `CPA-BSK-Relay` | `AtLogOn` → agent for user `will` |

XML backups: `runtime/CPA-Manager-Plus/deploy-backups/LocalCPAManagerPlus.xml.bak-*`.

## Verified

Manager `POST /usage-service/official-balance` Layso → Credits `46.92` USD
(`60fa1e2d…` / recheck `0908f04a…`, `verified_browser`). Codex twin
`1b55357e…` also `46.92`.

`bin/bsk.exe` SHA-256 `CF6A4821292AAABA36717A2D6E34FAC68539BDD4AC2EEE8196BC4138E4395CFA`
(recorded 2026-11-04).

Controlled `go test ./internal/httpapi/ -run OfficialBalanceWeb|Layso|BrowserSession`
→ `ok` in 39.209s (2026-11-04T17:03+08).

## Cold-start note (2026-11-05, post S03 exe)

- Scheduled Task `CPA-BSK-Relay` (`AtLogOn` / user `will`) is **Ready** but
  `LastTaskResult=267011` (never ran this boot).
- Live listener `127.0.0.1:18717` is the interactive agent
  `bsk-relay-agent.ps1` (observed PID 50560).
- Layso POST on exe `A8946628…` still **success USD 46.92** while that agent is up
  (`issue95-e03-prep-20261104/evidence/e03-s03-capability-readback-20261105.json`).
- If the user-session agent dies and AtLogOn has not re-fired, LocalService sees
  `WEB_BROWSER_UNAVAILABLE`. Keep the agent running after login (or start the task).
