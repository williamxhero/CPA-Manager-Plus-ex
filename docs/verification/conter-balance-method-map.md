# conter_proj UI 余额方法 → CPA 清单对照

参考只读：`d:\WILL\TOOLS\conter_proj\main.py` → `RelayQuotaService`（`relay_quota.py` / `browser_balance.py` / `relay_sites.json`）。
**不复制、不入库任何 Token/Key。** 展示倍率（×7）是 UI 口径，不是 CPA 原生金额合同。

## UI 实际采用的三条适配器

| adapter | 认证与路径 | 金额语义 |
|---|---|---|
| `openai_new_api` | `GET {base}{path}`，`Authorization: Bearer`；可选 `New-Api-User: {user_id}` | JSON 点分字段；NewAPI quota 用 `balance_divisor`（常见 `500000`）换展示币种 |
| `deepinfra_billing` | `GET https://api.deepinfra.com/payment/checklist?compute_owed=true` Bearer | 可用额 = `-(stripe_balance + recent)`；欠款拒绝当可用 |
| `browser_session` | 固定 BrowserSkill `browser_instance`；导航同源 `dashboard`；页内 `fetch('/api/user/self', credentials:include, New-Api-User)` | 校验 `success` + `data.id==user_id`；取 `data.quota` / divisor；**不导出 cookie** |

## 与冻结清单 host 对齐

| conter 站点 | base | 方法 | CPA 冻结 host | CPA 落点 |
|---|---|---|---|---|
| WawAPI | `https://wawapii.com` | `openai_new_api` → `/v1/usage`，字段 `balance`，divisor 1 | `wawapii.com` | **已落** `openai-compat-usage-v1`：`GET /v1/usage` → 精确 USD `account_wallet`（非 NewAPI quota / 非 ×7） |
| Top-API | `https://api-top.com` | `browser_session` → `/dashboard` + `/api/user/self` + user_id | `api-top.com` | **已落** `newapi-browser-session-v1`：同站会话只读原生 `newapi_quota`（不导出 cookie；×7 不进观测） |
| 可乐AI | `https://code28.ccwu.cc`（**非** kele520） | 同上 browser_session | 清单有 `kele520.com` | **主机不一致**：origin 未 enrollment；禁止把 code28 当 kele520 |
| 凉热葵 | `https://api.liangrekui.com` | `openai_new_api` → `/api/user/self` + `New-Api-User`，`data.quota`/500000 | `api.liangrekui.com` | #74 已有 `newapi-user-self-v1` 路径；转换须显式 operator 合同 |
| DeepInfra | `https://api.deepinfra.com` | `deepinfra_billing` checklist | `api.deepinfra.com` | #73 已交付 signed prepaid（原生 USD；×7 不进观测） |
| （无） | — | — | `api.layso.ai` / `layso.ai` | #97 只读 `/billing`：2026-11-04 真页 observe 为**未登录**营销+登录表；parser/Profile1 门禁仍开（见 `issue97-layso-live-observe-20261104.md`） |

## CPA 约束（相对 conter）

- 原生观测保留精确原币/原单位；不把 UI ×7 写成官方余额。
- NewAPI 禁止默认 `500000` 猜倍率；须 `protocolConfirmed` + 显式 conversion profile。
- #97 BrowserSkill：只读普通页；LAYSO 固定 billing 页。conter 的同站 `fetch /api/user/self` 属 **#74 已登录会话连接器** 证据，不能未经授权扩成任意站点私有 API。
- 清单主机以 CPA 冻结矩阵为准；conter 仅作方法参考。
