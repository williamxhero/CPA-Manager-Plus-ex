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

- Avatar `alt="h7w8"` (display name); site chrome also has `alt="Logo"` — parser skips Logo
- Sidebar section `Personal`
- User ID `21086` appears on Personal Center, **not** on the models wallet surface

## CPA contract update

- Fixed page → `https://layso.ai/dashboard/models`
- Parsing rule → `layso-dashboard-models-account-balance-v1` via read-only `get-html`
- Suggested web binding: accountId=`h7w8`, tenantId=`Personal`, role=`Personal`, currency=`USD`, executionUser=`will`

## Host Enrollment (wired)

- Management HTTP registration stays `pending_verification` only (never self-certifies).
- Durable host table `official_balance_web_browser_attestations` + `Service.AttestWebBrowserHost` grants `verified_browser` when Profile=`Profile 1`, family/instance/executionUser match the web source, and the parser rule is installed.
- `collectionAuthorized` / `managerDirectQuery` follow `WebReader.Ready`, not the registration request alone.

## Live enrolled query (PASS)

```text
CPA_LIVE_LAYSO=1 CPA_LIVE_LAYSO_INSTANCE=5cfdda27 CPA_LIVE_LAYSO_EXECUTION_USER=will \
  go test -mod=readonly -count=1 -timeout=120s -v ./internal/balance \
  -run TestLiveLaysoEnrolledDashboardModelsQuery
```

Observed: `QueryStatus=success`, `AmountDecimal=46.92`, `Currency=USD`, `Verification=verified_browser`.

## Profile 1 host facts (this machine)

- Edge `Local State` `profile.last_used` = `Profile 1`
- Profile 1 info_cache: name `Hinh`, `user_name` `h7w8@qq.com` (matches wallet avatar `h7w8`)
- BrowserSkill connected instance during live query: `5cfdda27`
- OS execution user used in contract: `will`
- Example attestation payload: `host-attestation.example.json` (host-only; not an HTTP body)

## Still open before issue close / deploy

- Persist host attestation into the manager process SQLite on the intended runtime (not only the live test Enrollment stub)
- #95 E03 deploy evidence and #88 S03/S04 remain blocked until authorized deploy + this attestation is on that host
- Do not commit raw HTML dumps; keep them gitignored
