# Matrix live blockers on LocalCPAManagerPlus (2026-11-04)

Layso OfficialBalance Manager path is green (claude + codex twins, 46.92).
Other frozen hosts remain blocked for live Manager query as follows:

| Host | Adapter | Blocker |
|---|---|---|
| `api.deepinfra.com` | `deepinfra` | OfficialBalance `configured=false` after misbind cleanup; needs operator query credential (not pulled from Core config into evidence) |
| `api.deepseek.com` | `deepseek` | no OfficialBalance row; needs secret |
| Volcengine / Aliyun | vendor adapters | no row; needs vendor contract + secret |
| `wawapii.com` | `openai-compat-usage-v1` | needs Bearer secret |
| `api.liangrekui.com` | `newapi-user-self-v1` | needs secret + explicit conversion profile |
| `api-top.com` | `newapi-browser-session-v1` | BrowserSkill navigate `/dashboard` shows **Sign in** form (not enrolled session in Profile 1); user_id unknown |
| `kele520.com` | — | still blocked by host alias rule (conter uses `code28.ccwu.cc`) |

## Host bridges already in place for browser adapters

- `CPAMP_WEB_BROWSER_ATTESTATION` + host attestation file
- LocalService → `127.0.0.1:18717` bsk relay (`CPA-BSK-Relay` AtLogOn)

Tickets stay open; no secrets recorded here.
