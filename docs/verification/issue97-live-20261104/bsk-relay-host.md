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
(`60fa1e2d…`, `verified_browser`).
