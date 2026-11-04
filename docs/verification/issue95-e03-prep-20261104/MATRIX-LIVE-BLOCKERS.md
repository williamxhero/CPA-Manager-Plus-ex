# Matrix live blockers on LocalCPAManagerPlus (2026-11-04)

## Green now

| Host | Adapter | Evidence |
|---|---|---|
| `api.layso.ai` / `layso.ai` | `layso_billing_web_v1` | Manager POST **success** / **USD 46.92** |
| `api.deepseek.com` | `deepseek` | POST **success**; wallet CNY **50.47** |
| `api.deepinfra.com` | `deepinfra` | POST **success**; signed prepaid **USD 0** |
| `wawapii.com` | `openai-compat-usage-v1` | POST **success**; **USD 53.71695596** |
| `api-top.com` | `newapi-browser-session-v1` | POST **success**; native quota **2280724** (job `d41c45a5…`); userId `2648`; exe `B26E39A8…` |

E03 elevated redeploy PASS after BrowserSkill cooperate-sync evaluate fix.
Panel SHA `DCB4CF76…`. bsk relay `:18717` required.

## Still blocked

| Host | Adapter | Blocker |
|---|---|---|
| `api.liangrekui.com` | `newapi-user-self-v1` | needs operator `userID`+conversion (+ PAT); openai-compat `/v1/usage` is **404** |
| `code28.ccwu.cc` | browser | Profile login; origin **not** enrolled for browser-session (do not alias kele520) |
| Volcengine / Aliyun | vendor adapters | no AK/SK on host |
| `kele520.com` | — | **do not alias**; use `code28.ccwu.cc` |

## Operator unblockers

1. Log into `code28.ccwu.cc` under Edge Profile 1 (pass CF). Browser-session enrollment for code28 needs a separate origin allowlist decision.
2. For 凉热葵: confirm NewAPI `userID` + conversion proof + PAT (not openai-compat `/v1/usage`).
3. Provide Volc/Aliyun billing AK/SK (never commit) + hosts/DNS for their APIs.

Tickets stay open; no secrets in-repo.
