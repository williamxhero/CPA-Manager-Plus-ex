# Matrix live blockers on LocalCPAManagerPlus (2026-11-04)

## Green now

| Host | Adapter | Evidence |
|---|---|---|
| `api.layso.ai` / `layso.ai` | `layso_billing_web_v1` | Manager POST **success** / **USD 46.92** |
| `api.deepseek.com` | `deepseek` | POST **success**; wallet CNY **50.47** |
| `api.deepinfra.com` | `deepinfra` | POST **success**; signed prepaid **USD 0** |
| `wawapii.com` | `openai-compat-usage-v1` | POST **success**; **USD 53.71695596** |
| `api-top.com` | `newapi-browser-session-v1` | POST **success**; native quota **2280724** |
| `code28.ccwu.cc` | `newapi-browser-session-v1` | POST **success**; native quota **-13156**; kele520 Core bind |
| `api.liangrekui.com` | `newapi-browser-session-v1` | POST **success**; quota **172900** → CNY **0.3458** |
| `billing.volcengineapi.com` (Doubao) | `volcengine-account-balance-v2022-01-01` | POST **success**; CNY **122.35** |

## Deferred (operator 2026-11-04 ~23:36)

| Item | Decision |
|---|---|
| Aliyun / Bailian Token Plan via `bailian-quota --source bsk` | **SKIP for now** — Bailian console login / bsk Edge flaky; resume after CPA go-live |
| OfficialBalance Aliyun AK/SK | **not pursued** (operator: keep CLI form later) |

## Other

| Host | Note |
|---|---|
| `kele520.com` | **do not alias**; billing origin is `code28.ccwu.cc` |

Tickets stay open; no secrets in-repo. Priority shifts to remaining SPECs for CPA online deploy.
