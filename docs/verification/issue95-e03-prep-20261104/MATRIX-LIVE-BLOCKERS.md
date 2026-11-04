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
| `api.liangrekui.com` | `newapi-browser-session-v1` | POST **success**; native quota **172900** → CNY **0.3458** (job `8547ebe1…`) |
| `billing.volcengineapi.com` (Doubao Core bind) | `volcengine-account-balance-v2022-01-01` | POST **success**; CNY **122.35** (job `dbf1d73d…`); hosts-pinned off Clash fake-IP |

## Still blocked

| Host | Adapter | Blocker |
|---|---|---|
| Aliyun / Qwen-Compat | `aliyun-prepaid-cash-v2017-12-14` | `billing-aksk.local.json` Aliyun section empty — need AK/SK + accountID |
| `kele520.com` | — | **do not alias**; billing origin is `code28.ccwu.cc` |

Tickets stay open; no secrets in-repo.
