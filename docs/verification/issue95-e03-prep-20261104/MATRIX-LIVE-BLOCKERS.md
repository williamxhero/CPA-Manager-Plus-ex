# Matrix live blockers on LocalCPAManagerPlus (2026-11-04)

## Green now

| Host | Adapter | Evidence |
|---|---|---|
| `api.layso.ai` / `layso.ai` | `layso_billing_web_v1` | Manager POST **success** / **USD 46.92** |
| `api.deepseek.com` | `deepseek` | POST **success**; wallet CNY **50.47** |
| `api.deepinfra.com` | `deepinfra` | POST **success**; signed prepaid **USD 0** |
| `wawapii.com` | `openai-compat-usage-v1` | POST **success**; **USD 53.71695596** |
| `api-top.com` | `newapi-browser-session-v1` | POST **success**; native quota **2280724** |
| `code28.ccwu.cc` | `newapi-browser-session-v1` | POST **success**; native quota **-13156**; userId `2822`; kele520 Core bind (no kele alias) |
| `api.liangrekui.com` | `newapi-browser-session-v1` | POST **success**; quota **172900** → CNY **0.3458** |
| `billing.volcengineapi.com` (Doubao) | `volcengine-account-balance-v2022-01-01` | POST **success**; CNY **122.35** |

## Aliyun = local CLI (not OfficialBalance AK/SK)

| Path | Status |
|---|---|
| `bailian-quota.exe --json --source bsk` | **keep this form**; Token Plan / console reading via BrowserSkill |
| OfficialBalance `aliyun-prepaid-cash-v2017-12-14` | **do not pursue** for this matrix (operator: continue CLI) |

Live gate now: Edge+BrowserSkill connected (`5cfdda27`), but Bailian console shows **登录** — operator must sign in on `https://bailian.console.aliyun.com/` in that profile, then re-run CLI.

## Other

| Host | Note |
|---|---|
| `kele520.com` | **do not alias**; billing origin is `code28.ccwu.cc` |

Tickets stay open; no secrets in-repo.
