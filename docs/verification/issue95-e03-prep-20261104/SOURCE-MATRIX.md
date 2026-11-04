# #95 E01/E03 source matrix freeze (local, 2026-11-04)

Frozen against worktree `spec92/conter-balance-method-map-20261104` tip including
`40b2cb08`. **Not** a live inventory of a running production host.
Does not read production secrets.

| Host / site | CPA adapter | Protocol | Unit | Hourly path | Live success evidence this round | Gate |
|---|---|---|---|---|---|---|
| `api.deepinfra.com` | deepinfra prepaid | signed checklist | USD native | full-refresh when configured | #73 suite; no new live call here | ready if secret+Core |
| `api.liangrekui.com` | `newapi-browser-session-v1` | BrowserSkill same-origin `/api/user/self` + refresh Bearer | NewAPI quota → CNY | same | live POST success quota 172900 / CNY 0.3458 (user 920); atob expression `98c59096` | green |
| `wawapii.com` | `openai-compat-usage-v1` | `GET /v1/usage` Bearer | USD wallet | same | adapter+tests `2b59c7c2` | ready if secret |
| `api-top.com` | `newapi-browser-session-v1` | BrowserSkill same-origin `/api/user/self` | native NewAPI quota | same | adapter+tests `2b59c7c2`; live `/dashboard` shows Sign-in (Profile 1 not logged in) | needs enrolled browser session + user_id |
| `kele520.com` | (unenrolled) | — | — | — | **blocked**: conter uses `code28.ccwu.cc`, not kele520 | do not alias |
| `layso.ai` / `api.layso.ai` | `layso_billing_web_v1` | BrowserSkill get-html `/dashboard/models` | USD Credits | same when Ready | live enrolled `46.92`; Manager PUT/POST success; **UI readback PASS** Claude+Codex (`bd880e9a`, panel `DCB4CF76…`) | host attestation + runtime bsk relay |
| Volcengine | `volcengine-account-balance-v2022-01-01` | billing AK/SK JSON secret | CNY major | same | live POST CNY 122.35 on Doubao bind | green |
| Aliyun / Bailian Token Plan | `bailian-quota` CLI (`--source bsk`) | local BrowserSkill console read | Token Plan windows/credits | credential-quota / CLI | operator: continue CLI; not OB AK/SK; needs Bailian login in bsk Edge | waiting login |

## Explicit non-claims

- No production Manager process was queried for “latest success”.
- ×7 UI multipliers are not official observations.
- Closing E01 still requires Hermes/authorized live matrix on the install host.
