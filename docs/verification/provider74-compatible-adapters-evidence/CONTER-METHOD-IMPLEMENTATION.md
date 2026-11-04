# conter 方法落地（2026-11-04）

参考：`docs/verification/conter-balance-method-map.md`（只读对照，无密钥）。

## 已实现

| CPA adapter | 对应 conter | 覆盖 host | 语义 |
|---|---|---|---|
| `openai-compat-usage-v1` | `openai_new_api` → `/v1/usage` | `wawapii.com`（endpoint 可配任意 `https://…/v1/usage`） | 精确 USD `account_wallet` |
| `newapi-browser-session-v1` | `browser_session` | **仅** `https://api-top.com` enrollment | 原生 `newapi_quota`；cookie 不离浏览器 |
| `deepinfra` | `deepinfra_billing` | `api.deepinfra.com` | 已有 signed prepaid（#73） |
| `newapi-user-self-v1` | `openai_new_api` → `/api/user/self` | 凉热葵等候选 | 已有；须显式 operator 合同 |

## 明确未 enrollment

- `code28.ccwu.cc` / `kele520.com`：主机不一致，browser session origin 拒绝。
- LAYSO：继续 `layso_billing_web_v1` observe-only；不得用本会话连接器的 evaluate。

## 验证

- `go test ./internal/balance/ -count=1`（含 OpenAICompatUsage / BrowserSession / capabilities）。
