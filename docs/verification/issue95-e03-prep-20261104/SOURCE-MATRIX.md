# #95 E01/E03 source matrix freeze (local, 2026-11-04)

Frozen against worktree `spec92/conter-balance-method-map-20261104` tip including
`c745df5a` / `a5904cd8`. **Not** a live inventory of a running production host.
Does not read production secrets.

| Host / site | CPA adapter | Protocol | Unit | Hourly path | Live success evidence this round | Gate |
|---|---|---|---|---|---|---|
| `api.deepinfra.com` | deepinfra prepaid | signed checklist | USD native | full-refresh when configured | #73 suite; no new live call here | ready if secret+Core |
| `api.liangrekui.com` | `newapi-user-self-v1` | Bearer + optional New-Api-User | NewAPI quota; conversion explicit | same | #74 contract; no new live call | needs operator conversion confirm |
| `wawapii.com` | `openai-compat-usage-v1` | `GET /v1/usage` Bearer | USD wallet | same | adapter+tests `2b59c7c2` | ready if secret |
| `api-top.com` | `newapi-browser-session-v1` | BrowserSkill same-origin `/api/user/self` | native NewAPI quota | same | adapter+tests `2b59c7c2` | needs enrolled browser instance |
| `kele520.com` | (unenrolled) | — | — | — | **blocked**: conter uses `code28.ccwu.cc`, not kele520 | do not alias |
| `layso.ai` | `layso_billing_web_v1` | BrowserSkill get-html `/dashboard/models` | USD Credits | same when Ready | live enrolled `46.92` (`c745df5a`); Manager `PUT official-balance` on Core claude/`api.layso.ai` (`b341a945…`, authority `78206120…`, GET ready); Manager `POST` → `WEB_BROWSER_UNAVAILABLE` under LocalService | host attestation OK; service account must reach bsk session |
| Volcengine / Aliyun | existing adapters | vendor APIs | vendor | same | suites exist; ActualCore often SKIP without pinned Core | Core pin |

## Explicit non-claims

- No production Manager process was queried for “latest success”.
- ×7 UI multipliers are not official observations.
- Closing E01 still requires Hermes/authorized live matrix on the install host.
