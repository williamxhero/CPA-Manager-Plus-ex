# Matrix live blockers on LocalCPAManagerPlus (2026-11-05)

Freeze after #88 S03 night elevated deploy.

- exe SHA-256: `A89466285946D5DBCE60FB7058DFA4B22EB84C613D28FDD37CA0C67C74255987`
- panel SHA-256: `AF171E9BFBC045041043E71B7C589EE9BCA726C0A74FF55E504323A7162385DA`
- Evidence: `evidence/e03-s03-capability-readback-20261105.json`
- Aliyun: **DEFERRED** (operator skip for go-live)

## Green on this SHA (Manager POST OfficialBalance)

| Host / adapter | Result |
|---|---|
| Layso `layso_billing_web_v1` (×2 Claude/Codex) | **success** USD **46.92** (jobs `74b95068…`, `43b7d4c4…`) |
| DeepInfra `deepinfra` | **success** USD **0** (`17fdd16e…`) |
| DeepSeek `deepseek` | **success** (`c49eb158…`; amount field null in job status, queryStatus success) |
| Volcengine Doubao `volcengine-account-balance-v2022-01-01` | **success** CNY **122.35** |
| WawAPI `openai-compat-usage-v1` | **success** USD **53.66854619** after hosts re-pin (`5faf1a1a…`) |
| api-top / code28 / 凉热葵 `newapi-browser-session-v1` | prior-night live POST green; not in this tick's queryCapable official set |

## Deferred

| Host | Note |
|---|---|
| Aliyun / Bailian | **SKIP** until after go-live |

## DNS

Elevated hosts re-pinned (Clash `198.18` bypass): `wawapii.com`, `api.deepseek.com`, `api.deepinfra.com`, `api.liangrekui.com`, `api-top.com`, `code28.ccwu.cc`, plus existing Volc entries. Clash Verge Merge `fake-ip-filter` still not applied — hosts remain the operational fix.

## E03 readback

- `GET /usage-service/full-refresh` → 200, `resourceCount=51`, round `complete`
- `CPA-BSK-Relay` AtLogOn task Ready but **never ran this boot** (`LastTaskResult=267011`); live `:18717` = user-session `bsk-relay-agent.ps1` PID 50560 (Layso POST still works)

Tickets stay open; no issue close.
