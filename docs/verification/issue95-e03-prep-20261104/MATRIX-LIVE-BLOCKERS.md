# Matrix live blockers on LocalCPAManagerPlus (2026-11-04)

## Green now

| Host | Adapter | Evidence |
|---|---|---|
| `api.layso.ai` / `layso.ai` | `layso_billing_web_v1` | Manager POST **success** / **USD 46.92** (job `1411b927…`) |
| `api.deepseek.com` | `deepseek` | POST **success**; wallet CNY **50.47** |
| `api.deepinfra.com` | `deepinfra` | POST **success**; signed prepaid **USD 0** |
| `wawapii.com` | `openai-compat-usage-v1` | POST **success**; **USD 53.71695596** |

E03 elevated deploy PASS (`deploy-solve-all-e03.json`). Panel SHA `DCB4CF76…`.
bsk relay `:18717` + attestation remain required for Layso web path.

## Still blocked

| Host | Adapter | Blocker |
|---|---|---|
| `api.liangrekui.com` | needs `newapi-user-self-v1` | hosts OK; `/api/status` **200**; bare `GET /v1/usage` → **404** (explains OfficialBalance `UPSTREAM_STATUS`); `/api/user/self` → **401** without New-Api-User |
| `api-top.com` | `newapi-browser-session-v1` | Profile 1 **Sign in**; bare `GET /v1/usage` → **404**; `/api/user/self` → **401** |
| `code28.ccwu.cc` | browser | Profile 1 **登录**; `/api/status` **200** |
| Volcengine / Aliyun | vendor adapters | no AK/SK on host |
| `kele520.com` | — | **do not alias**; use `code28.ccwu.cc` |

## Operator unblockers

1. Log into `api-top.com` and `code28.ccwu.cc` under Edge Profile 1 (pass CF), then register `newapi-browser-session-v1` OfficialBalance.
2. For 凉热葵: confirm NewAPI `userID` + conversion proof (do not treat openai-compat `/v1/usage` as authorized).
3. Provide Volc/Aliyun billing AK/SK (never commit) + hosts/DNS for their APIs.

Tickets stay open; no secrets in-repo.
