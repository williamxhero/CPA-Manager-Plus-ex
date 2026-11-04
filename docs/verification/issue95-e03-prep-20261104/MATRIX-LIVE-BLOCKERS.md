# Matrix live blockers on LocalCPAManagerPlus (2026-11-04)

## Green now

| Host | Adapter | Evidence |
|---|---|---|
| `api.layso.ai` / `layso.ai` | `layso_billing_web_v1` | Manager POST **success** / **USD 46.92** |
| `api.deepseek.com` | `deepseek` | POST **success**; wallet CNY **50.47** |
| `api.deepinfra.com` | `deepinfra` | POST **success**; signed prepaid **USD 0** |
| `wawapii.com` | `openai-compat-usage-v1` | POST **success**; **USD 53.71695596** |
| `api-top.com` | `newapi-browser-session-v1` | POST **success**; native quota **2280724** |
| `code28.ccwu.cc` | `newapi-browser-session-v1` | POST **success**; native quota **-13156** (job `38a200d9…`); userId `2822`; bound to kele520 Core row (no kele520 browser alias) |

## Still blocked

| Host | Adapter | Blocker |
|---|---|---|
| `api.liangrekui.com` | `newapi-user-self-v1` | needs operator login/`userID`+conversion (+ PAT); `/v1/usage` is **404** |
| Volcengine / Aliyun | vendor adapters | no AK/SK on host |
| `kele520.com` | — | **do not alias**; billing origin is `code28.ccwu.cc` |

Tickets stay open; no secrets in-repo.
