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

## Runtime after #95 E03 (2026-11-04)

- Installed exe SHA-256 `46A85F91…`; panel `087C71D3…`; service `LocalCPAManagerPlus` Running
- Host file `D:\WILL\AGENT\CPA\runtime\CPA-Manager-Plus\host-attestation.json` + WinSW env `CPAMP_WEB_BROWSER_ATTESTATION`
- Live enrolled BrowserSkill query rechecked this tick: Credits **46.92** PASS (`5cfdda27`)

## OfficialBalance API (same day, later)

- `GET /usage-service/resource-capabilities` → `core_contract_unverified` is **Status() stub only**; Inventory works (full-refresh shows live Core scope).
- `PUT /usage-service/official-balance` LAYSO web source **PASS** on claude/`api.layso.ai` group `b341a945…`+`14e6284d…` (`configured=true`, authority `78206120…`, GET `state=ready`). Details: `official-balance-api-20261104.md`
- Misbind to DeepInfra openai-compat group was corrected back to `adapter=deepinfra` / `configured=false`
- Manager `POST` collection **PASS** via localhost bsk relay (`60fa1e2d…`, Credits **46.92** USD, `verified_browser`). Relay is a host runtime bridge for LocalService→user `bsk`; keep agent alive while service runs.

## Still open before issue close

- Scheduled Task `CPA-BSK-Relay` registered AtLogOn for user `will` (agent already listening on `:18717`)
- Optional: second OfficialBalance row on codex Layso twin `b3452588…`/`9c6eea89…`
- Do not close #97/#95/#88; do not commit raw HTML dumps
