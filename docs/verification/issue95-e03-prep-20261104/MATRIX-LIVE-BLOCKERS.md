# Matrix live blockers on LocalCPAManagerPlus (2026-11-04)

Layso OfficialBalance Manager path is green (claude + codex twins, 46.92),
including panel UI readback (`configured` / `USD 46.92`) after `bd880e9a`.
Other frozen hosts remain blocked for live Manager query as follows:

| Host | Adapter | Blocker |
|---|---|---|
| `api.deepinfra.com` | `deepinfra` | PUT `configured=true`; POST blocked: DNS fake-IP `198.18.2.52` refused by `secureClient` (user-session checklist **200**) — `MATRIX-DEEPINFRA-DEEPSEEK-20261104.md` |
| `api.deepseek.com` | `deepseek` | PUT `configured=true`; POST blocked: DNS fake-IP `198.18.2.53` refused by `secureClient` (user-session **50.50 CNY**) — same note |
| Volcengine / Aliyun | vendor adapters | no row; needs vendor contract + secret |
| `wawapii.com` | `openai-compat-usage-v1` | needs Bearer secret |
| `api.liangrekui.com` | `newapi-user-self-v1` | needs secret + explicit conversion profile |
| `api-top.com` | `newapi-browser-session-v1` | BrowserSkill navigate `/dashboard` shows **Sign in** form (not enrolled session in Profile 1); user_id unknown |
| `kele520.com` | — | still blocked by host alias rule (conter uses `code28.ccwu.cc`) |

## Host bridges already in place for browser adapters

- `CPAMP_WEB_BROWSER_ATTESTATION` + host attestation file
- LocalService → `127.0.0.1:18717` bsk relay (`CPA-BSK-Relay` AtLogOn)

## Operator unblockers (no secrets in-repo)

1. DeepInfra / DeepSeek: PUT already green on this host; for POST, turn off Clash
   fake-IP for those hostnames (or real DNS for LocalService). Do **not** permit
   `198.18.0.0/15` in OfficialBalance. WawAPI / 凉热葵 still need secret + same DNS.
2. WawAPI / 凉热葵: `PUT /usage-service/official-balance` with the correct Core
   complete source group + adapter + `secret` (never paste into git); same DNS
   constraint as above once configured.
3. api-top.com: log into `https://api-top.com/dashboard` under Edge Profile 1, then
   register `newapi-browser-session-v1` with that origin’s `user_id` and instance `5cfdda27`.
4. Keep Scheduled Task `CPA-BSK-Relay` / agent on `:18717` while LocalService Manager runs.

Tickets stay open; no secrets recorded here.
