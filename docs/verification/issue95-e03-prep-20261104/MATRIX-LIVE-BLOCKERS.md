# Matrix live blockers on LocalCPAManagerPlus (2026-11-04)

## Green now

| Host | Adapter | Evidence |
|---|---|---|
| `api.layso.ai` / `layso.ai` | `layso_billing_web_v1` | Manager POST + UI **USD 46.92** |
| `api.deepseek.com` | `deepseek` | POST **success**; wallet CNY **50.47** |
| `api.deepinfra.com` | `deepinfra` | POST **success**; signed prepaid **USD 0** |
| `wawapii.com` | `openai-compat-usage-v1` | POST **success**; **USD 53.72267888** |

E03 elevated deploy PASS (`deploy-solve-all-e03.json`). Panel SHA `DCB4CF76…`.
bsk relay `:18717` + attestation remain required for Layso web path.

## Still blocked

| Host | Adapter | Blocker |
|---|---|---|
| `api.liangrekui.com` | NewAPI / usage | CF **1010** via hosts-pinned edge; NewAPI user-self needs operator `userID`+conversion |
| `api-top.com` | browser / usage | Profile 1 **Sign in**; CF 1010 on HTTP adapters |
| Volcengine / Aliyun | vendor adapters | no AK/SK on host |
| `kele520.com` | — | **do not alias**; use `code28.ccwu.cc` (also Sign in + CF) |

## Operator unblockers

1. Log into `api-top.com` and `code28.ccwu.cc` under Edge Profile 1 (pass CF), then register browser-session OfficialBalance.
2. For 凉热葵: confirm NewAPI `userID` + optional conversion proof, or allowlist Manager egress past CF.
3. Provide Volc/Aliyun billing AK/SK (never commit) + hosts/DNS for their APIs.

Tickets stay open; no secrets in-repo.
