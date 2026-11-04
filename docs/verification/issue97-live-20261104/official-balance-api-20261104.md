# #97 OfficialBalance API on LocalCPAManagerPlus (2026-11-04)

## Clarification: `core_contract_unverified` is not the Inventory probe

`GET /usage-service/resource-capabilities` always returns
`identity.state=unknown` / `reason=core_contract_unverified` by design
(`resourcecapability.Service.Status`). Real Core negotiation is
`Inventory()`, used by OfficialBalance / full-refresh.

Evidence that Inventory works on this host:

- `GET /usage-service/full-refresh` returns live Core scope
  `instance_id=396cba32-…`, `snapshot_revision≥11`, `resourceCount=51`
- Core `GET /v8/management/resource-capabilities` with
  `data/cpa-management-key.txt` → 200, `inventory/durable=true`
- Manager `PUT /usage-service/config` re-aligned
  `cpaBaseUrl=http://127.0.0.1:8317` + management key from the same file

## OfficialBalance web source registration (PASS)

```http
PUT /usage-service/official-balance
Authorization: Bearer <admin>
```

Payload (binding + web contract):

- adapter: `layso_billing_web_v1`
- endpoint (host-fixed): `https://layso.ai/dashboard/models`
- web: site=`layso`, accountId=`h7w8`, tenantId=`Personal`, role=`Personal`,
  executionUser=`will`, browser family=`microsoft-edge` profile=`Profile 1`
  instanceId=`5cfdda27`, currency=`USD`,
  parsingRule=`layso-dashboard-models-account-balance-v1`
- Core binding (complete openai-compatibility provider+credential group used
  for this registration):
  - provider `068a7783-00cf-40e3-a900-ced04834afbe`
  - credential `27c51563-3dcd-424b-b591-21d93637e27f`
  - scope revision 11

Result: HTTP 200, `configured=true`, `version=1`,
`webSourceAuthority=1829ec0e2595c450353cc6ba404a86b3336e8ba34f54cebc31c41f2a4055059d`,
`managerDirectQuery=true`, SQLite `official_balance_sources` row present.

`GET …/web-enrollment` → `collectionAuthorized=true`, `parserSupported=true`,
`state=not_registered` (HTTP registration stays pending; host attestation
already authorizes collection).

## Manager-driven collection (BLOCKED on service account)

`POST /usage-service/official-balance` → 202 jobs
`4ff2badb-…` and `e9b30659-…` complete with
`queryStatus=unavailable`, `errorCode=WEB_BROWSER_UNAVAILABLE`.

Root cause on this machine:

- Service `LocalCPAManagerPlus` runs as `NT AUTHORITY\LocalService`
- BrowserSkill instance `5cfdda27` is connected in the interactive user
  session (`will`); `bsk browsers --json` as that user lists it
- Manager invokes `exec.CommandContext(..., "bsk", …)`; LocalService cannot
  see the user-session BrowserSkill daemon even after WinSW `PATH` includes
  `C:\Users\will\.local\bin` (XML backup under
  `runtime/CPA-Manager-Plus/deploy-backups/LocalCPAManagerPlus.xml.bak-*`)

Workaround already proven outside the service process: package-level live
enrolled query (`TestLiveLaysoEnrolledDashboardModelsQuery`) as user `will`
→ Credits `46.92`. Closing Manager-path collection needs the service to run
in an identity that can reach BrowserSkill (or an equivalent host relay) —
not attempted here without an explicit password/account change.

## Non-claims

- The Core resource group above is an openai-compatibility inventory binding
  used to satisfy OfficialBalance identity fences; host display-name mapping
  to “Layso” among the four openai-compat providers is not yet proven from
  Core catalog labels (Core resources API has no display name field).
- Tickets #97/#95/#88 stay open.
- No issue close; no production secret dump.
