# #97 live LAYSO wallet observation (2026-11-04)

## Remaining credits (confirmed)

On logged-in `https://layso.ai/dashboard/models` (same chrome as dashboard home):

- **Account balance → Credits:46.92**
- Subtext: Available for all model calls
- Evidence: viewport PNG + page HTML `aria-label="Credits:46.92"` (visible text is `aria-hidden`, so VOM `observe` does **not** see 46.92 and may show unrelated `Credits:0`)

## Not remaining balance

| URL | What it shows |
|---|---|
| `/billing` | Recharge **history** (Credits received per order) |
| `/dashboard/usage` | **Total spend** (e.g. Credits:168.04), not wallet remaining |
| Dashboard “Cumulative usage” | Credits:317.68 cumulative, not remaining |

## Identity visible on wallet surface

- Avatar `alt="h7w8"` (display name)
- Sidebar section `Personal`
- User ID `21086` appears on Personal Center, **not** on the models wallet surface

## CPA contract update

- Fixed page → `https://layso.ai/dashboard/models`
- Parsing rule → `layso-dashboard-models-account-balance-v1` via read-only `get-html`
- Suggested web binding: accountId=`h7w8`, tenantId=`Personal`, role=`Personal`, currency=`USD`

## Still open for close

- BrowserSkill Profile 1 ↔ instance `5cfdda27` proof (Copy profile instructions)
- Host Enrollment callback marking Profile/execution/parser verified
- Do not commit raw HTML dumps (may contain account chrome); PNG kept as local operator evidence only if redacted policy allows
