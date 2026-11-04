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

## OfficialBalance web source registration (PASS — corrected binding)

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
- Core binding (claude family, `base-url=https://api.layso.ai` from Core
  management config — resource IDs only, no secrets recorded):
  - provider `b341a945-0b64-40cd-ac80-3423e53211cf`
  - credential `14e6284d-b958-408f-947b-20df38076981`

Result: HTTP 200, `configured=true`, `version=1`,
`webSourceAuthority=78206120372d5529d13eca87c5654cf3ed8939c3f2af91fb01362a6fe31c593f`,
`managerDirectQuery=true`, GET status `state=ready`.

### Misbind correction

An earlier attempt incorrectly attached `layso_billing_web_v1` to the
openai-compatibility DeepInfra group (`068a7783…` / `27c51563…`). That was
replaced with `adapter=deepinfra` + `clearSecret=true` → `configured=false`
(version 2). No secrets were written in evidence.

Codex Layso twin (`b3452588…` / `9c6eea89…`, `https://api.layso.ai/v1`) also
registered: authority `be19953c…`; `POST` job `1b55357e…` → success `46.92` USD.

## Manager-driven collection (PASS via localhost bsk relay)

Earlier POSTs under bare `LocalService` failed with
`WEB_BROWSER_UNAVAILABLE` (service cannot see user-session BrowserSkill).

Host fix (runtime only, not committed as a product feature):

1. User-session agent `runtime/CPA-Manager-Plus/bsk-relay/bsk-relay-agent.ps1`
   listens on `127.0.0.1:18717` and runs real `bsk` (PID tracked at start).
2. `runtime/CPA-Manager-Plus/bin/bsk.exe` TCP-forwards argv to that agent.
3. WinSW `PATH` prefers that `bin` directory (XML backups under
   `runtime/CPA-Manager-Plus/deploy-backups/LocalCPAManagerPlus.xml.bak-*`).

`POST /usage-service/official-balance` on the corrected Layso binding then
returned job `60fa1e2d-…` with `queryStatus=success`, `amountDecimal=46.92`,
`currency=USD`, status `state=success`,
`verification=verified_browser`, authority `78206120…`.

## Non-claims

- Tickets #97/#95/#88 stay open.
- No issue close; no production secret dump; Core `api-keys` values never
  copied into this tree.

## Panel UI readback gap (same host)

BrowserSkill opened Claude/https://api.layso.ai provider detail. The 「官方余额」
region showed unsupported · 本票尚未交付此来源协议… and did **not** surface
Credits 46.92, even though Manager OfficialBalance API/full-refresh for that
Core group is success. Treat UI wiring/readback as still open under W05; API
path remains the verified collection evidence (ui-layso-official-balance-evidence.json).
