# CPA-Qwen

A native CLIProxyAPI v8 credential provider for Qwen, with an explicit read-only **Coding Plan** credential-quota query. Inference support is separate from quota authority.

## Problem

A generic OpenAI-compatible upstream does not make Bailian plan keys first-class management credentials. Furthermore, plan keys cannot read console quota (`ConsoleNeedLogin`). Coding Plan's public `/models` response is not credential validation.

## Solution

Expose one provider, **`qwen`**, using CLIProxyAPI's credential scheduler, rotation and cooldowns. Discover models once, translate client protocols to upstream OpenAI Chat Completions, and invoke `bailian-quota.exe` for honest console-derived quota readings.

## Features

- Each configured key becomes a stable `qwen-key-<sha256>.json` auth file with an optional readable label, visible in the management credential page.
- Execution always uses the **credential selected by the host**, never the catalog's default key.
- OpenAI Chat Completions and Anthropic Messages requests, tool calls, image parts and streaming SSE translation, adapted from the MIT reference.
- Authenticated full catalog discovery, bare upstream model IDs by default, optional explicit prefix and last-good snapshot.
- Strict Coding Plan helper protocol: three exact request-count windows, no arbitrary shell command or helper auth retry. Native CLI/cache readings are candidates for Core validation, not selected-key ownership or policy authority.
- Plugin-registered quota status API retains per-credential selection, with independent query/authority statuses. Missing key/account/plan proof remains `needs_configuration`; the embedded quota page is not advertised as a panel menu.
- Standalone stdlib-only Go helper: explicit official Coding Plan CLI or existing pre-enrolled raw-token transport, plus a strict Coding Plan cache. The plugin accepts explicit CLI/cache arguments; legacy Token Plan BL/browser/cookie tooling remains separate and is forbidden in the credential quota lane.
- Same-module `aliyun-bss-quota` compatibility plugin: bounded command queries or read-only AK/SK account balance, optional monthly spend and Model Studio resource packages. Account money is not Coding Plan request quota.

## Requirements

- CLIProxyAPI v8 native plugin ABI **and the SPEC92 observation-metadata SDK/Core integration**. The published v8.0.0 SDK alone lacks the fields used by this integration.
- Windows AMD64; Go 1.26.10+ for the current integration Core and a GCC-compatible C compiler with `CGO_ENABLED=1` for the DLL.
- A selected inference key. The native CLI/cache quota lane also requires an explicitly configured Coding Plan CLI/cache source and current Core resource enrollment with a valid receipt. This is local administrator enrollment, not independent proof of remote key/account ownership. The existing raw-token query remains unbound compatibility behavior, not a policy-authority alternative.

## Build

```powershell
python -B scripts/build-artifacts.py `
  --core-source 'D:\path\to\CPA-Core-ex' `
  --go 'D:\path\to\go1.26.10\bin\go.exe' `
  --cc 'D:\path\to\mingw64\bin\gcc.exe' `
  --output 'D:\path\to\new-candidate'
```

The single build entry creates **both native plugins and the only `bailian-quota` CLI** from this module. Runtime identifiers and artifact basenames remain `qwen-cliproxyapi`, `aliyun-bss-quota`, and `bailian-quota`; the repository is `williamxhero/CPA-Qwen`. No old Aliyun checkout, directory junction or second module is required.

The Core SDK must be explicitly supplied; the published v8.0.0 dependency alone is insufficient. The script copies module inputs into the candidate, resolves that SDK there, and leaves both source checkouts' module files unchanged. Module downloads are disabled unless `--allow-module-downloads` is explicitly supplied. Python 3 and the selected Go/C toolchains must already be installed.

Existing candidate directories are never overwritten. Only all-success, unchanged-source builds receive `manifest.json`, with source/SDK fingerprints, resolved module hashes, toolchain settings and artifact SHA-256. A failed candidate retains logs and `FAILED.json`, not a success manifest. Building is **not deployment**: the script never installs, stops, restarts or rolls back a running service. Actual installation must use the separately authorized deployment transaction with backup, health checks and automatic rollback. See [compatibility and migration](docs/aliyun-quota-compatibility.md).

## Configuration

Set the key in the **host process environment** rather than storing it in this repository:

```yaml
plugins:
  enabled: true
  dir: "D:\\path\\to\\candidate\\plugins"
  configs:
    qwen-cliproxyapi:
      enabled: true
      base-url: "https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1"
      api-keys:
        - value: "${QWEN_API_KEY}"
          name: "Qwen"
      model-prefix: { enabled: false, value: "qwen" }
      catalog: { refresh-interval: "30m", stale-while-unavailable: true }
      protocols: { chat-completions: true, messages: true }
      request-timeout: "5m"
      max-response-bytes: 67108864
      allow-http: false
      quota-source: command
      command: "D:\\WILL\\AGENT\\CPA\\qwen-cliproxyapi\\bin\\bailian-quota.exe"
      command-args: ["--protocol", "qwen-coding-plan-v1", "--source", "coding-plan", "--bl-path", "C:/path/to/bl.cmd", "--cache-file", "C:/path/to/qwen-coding-plan-cache.json", "--json"]
      command-timeout: "90s"
      display-name: "阿里云百炼额度（CLI）"
      quota-resource-bindings:
        - resource-id: "<current-CPA-resource-id>"
          identity-version: 1
          credential-version: 1
          config-version: 1
          membership-version: 1
```

### Configuration table

| Option | Default | Meaning |
|---|---|---|
| `api-keys` | Required | Objects containing `value` and optional `name`; `${ENV_VAR}` expansion; empty/duplicate keys rejected. |
| `base-url` | Token Plan CN URL above | HTTPS upstream without userinfo, query or fragment. |
| `model-prefix.enabled` / `.value` | `false` / `qwen` | Publish the bare upstream ID by default; when enabled, publish `<value>/<id>`. |
| `catalog.refresh-interval` | `30m` | Discovery cadence; minimum `1m`. |
| `catalog.stale-while-unavailable` | `true` | Retain last successful catalog on failure. |
| `protocols.chat-completions` | `true` | Allow OpenAI Chat Completions clients. |
| `protocols.messages` | `true` | Allow Anthropic Messages clients, translated to Chat Completions. |
| `request-timeout` | `5m` | Upstream request deadline. |
| `max-response-bytes` | `67108864` | Non-streaming response limit (64 MiB). |
| `allow-http` | `false` | HTTP only for local mocks when explicitly enabled. |
| `quota-source` | `command` | External CLI is the only quota source. |
| `command` | Required | Executable path; **not** a shell command. |
| `command-args` | Must be explicit for quota | Require `--protocol qwen-coding-plan-v1 --source coding-plan` with `--bl-path` + `--cache-file` for refresh, or `--cache-only` + `--cache-file` + positive `--max-age`; no legacy defaults, shell, pipes or interpolation. |
| `quota-resource-bindings` | Required for policy use | Explicit CPA resource target generations enrolled for this CLI/cache source. Each entry must match the resource ID plus identity, credential, config and membership versions; Core rejects stale or un-enrolled credentials and never assigns one CLI account to every key automatically. |
| `command-timeout` | `90s` | Hard quota command deadline. |
| `display-name` | `阿里云百炼额度（CLI）` | Quota group display name. |

Without an explicit key name labels are `Qwen 1`, `Qwen 2`, etc. IDs depend on the key hash, not label or ordering. Removed configuration keys are not automatically deleted from the host auth directory: the ABI has no deletion callback. Manage stale credentials explicitly in the host.

For Coding Plan use `https://coding.dashscope.aliyuncs.com/v1` (CN) or `https://coding-intl.dashscope.aliyuncs.com/v1` (international). Coding Plan keys typically start with `sk-sp-`. All execution goes to `{base-url}/chat/completions`; no fallback to another provider or credential is performed by the plugin.

## Panel API-key form

Registration displays **QWen Plan API Key** with an original embedded Q monogram at `/v0/resource/plugins/qwen-cliproxyapi/logo.svg`. Panel-created credentials are included in the quota API; credential cards and the standalone page retain the same explicit query/authority status, not unverified per-key numbers.

With management authorization, submit `POST /v0/management/plugins/qwen-cliproxyapi/credentials` with `base_url`, `api_key`, and optional `name`. The default label is `Qwen`. Unknown fields are ignored. URLs must be HTTP/HTTPS with a hostname and without userinfo, query, or fragment; explicit HTTP panel credentials are accepted independently of the config-only `allow-http` switch. Prefer HTTPS outside local tests. Empty/whitespace keys and control characters are rejected. Successful responses contain only `ok`, the `qwen-key-<sha256>` ID, and the label; keys are never returned or logged. A label containing the submitted key is redacted. Duplicate key + normalized base URL returns 409, including configured and imported credentials. Unreadable existing Qwen storage fails closed with 502.

The host saves the same top-level `type`, `id`, `label`, and `api_key` schema as configured credentials, plus `base_url`. Panel IDs hash the key and URL together, so the same key can target distinct URLs without overwriting a credential. Existing configured-key IDs remain unchanged. Panel parsing uses the host's filename-based runtime ID (`<returned id>.json`) so watcher parsing updates the initially saved record instead of creating a duplicate runtime credential. Execution resolves `base_url` from current persisted storage, then attributes, then the plugin endpoint default; both streaming and non-streaming use that result. Parsing/reloading and refresh preserve the URL and panel label. Model discovery still uses the configured key/base URL, not a separate catalogue per panel credential; at least one configured key remains required.

`auth.login.start` returns the manual form specification in `Metadata` (`auth_kind: manual_api_key`, the credential submit path, **添加凭证**, and Base URL/API Key/凭证名称 fields). An opaque state satisfies the host's login-start validation; it is not an OAuth flow, and the form does not need polling. Empirical isolated-core verification at 8399 returned HTTP 200 with only `state`, `status: "ok"`, and `url: ""`: **the host does not forward Metadata**. The panel must use its allowlist fallback; the metadata form path is not functional through this host's management response.

**Persistence limitation:** the v8 `host.auth.save` implementation writes directly to the destination before runtime registration, without a transactional rollback/delete callback. The plugin serializes its own duplicate checks/submissions, creates no local credential state before save confirmation, and returns a safe 502 on host rejection. It cannot guarantee that a failing host write leaves no partial file, or that a timed-out callback cannot later complete. Full failure atomicity requires a host-side transactional save; do not interpret a 502 as proof that no credential exists. No host source or live deployment is modified here.

## Model catalogue

The plugin fetches `GET {base-url}/models` using a configured key. Successful discovery replaces the model list; prefixing changes only public IDs, not upstream IDs. Discovery uses the first configured key, while requests use the host-selected credential.

Before the first successful upstream discovery, the model list is empty rather than a hardcoded partial catalogue. After discovery, every valid upstream model is published, including IDs that do not start with `qwen`. With stale policy enabled an outage retains the last successful catalog. Disable stale policy for fail-closed discovery.

Token Plan `/models` is auth-protected. Coding Plan `/models` is a static public list: **never interpret successful discovery as successful credential validation**.

## Quota via CLI

**Canonical:** credential management queries Coding Plan; AI-provider resources query prepaid PAYG balance (not implemented here). No provider/hostname-based product switching is allowed. A UI type is not ownership proof.

The plugin accepts the explicit CLI/cache Coding Plan arguments shown in the configuration example. It reuses the existing helper and bounded CLI reading chain; this integration adds **no official API operation**. Native `FetchQuota` returns normalized candidate readings with `accountProvenance:configured_cli_profile`; Core, not the plugin or legacy view, must validate enrollment and receipt evidence before accepting them for policy. Existing raw-token mode remains available as a non-authoritative query, without helper login, refresh, profile writes, browser or Token Plan/BSS queries.

### Helper CLI transport and cache

These invocations are accepted by the helper and as the plugin's explicit `command-args` (paths are placeholders, not installed configuration):

```text
bailian-quota --protocol qwen-coding-plan-v1 --source coding-plan --bl-path <explicit-bl-executable-or-cmd> --cache-file <coding-plan-cache.json> --timeout 90s
bailian-quota --protocol qwen-coding-plan-v1 --source coding-plan --cache-only --cache-file <coding-plan-cache.json> --max-age 45m
```

`--bl-path` is an alternative to `--token-file`/region/site, never a fallback or combination. `BAILIAN_BL_PATH` alone does not activate this Coding Plan transport. It invokes exactly `usage coding-plan --output json` once through the existing bounded BL runner and Windows private Job. No helper login/auth/refresh command, browser, retry or Token Plan request is made. **The supplied official CLI may internally refresh and write its own profile on an auth failure**; this adapter cannot promise otherwise. Existing raw-token mode avoids that behavior but remains unbound. No live CLI/profile/account is used by the component tests.

Flattened CLI JSON must contain all three windows' native `usedQuota`, `totalQuota` and integer-millisecond `resetTime`; `percentage` is informational, never a substitute for counts. Counts are decoded without adding float rounding (the adapter cannot recover precision already lost inside an external CLI). Any CLI error/nonzero exit, including the legacy Windows teardown-abort status, fails closed. Legacy `--source bl` keeps its original monthly Token Plan behavior; its cache is never reinterpreted as Coding Plan.

At the standalone helper boundary, both explicit Coding Plan transports may opt into `--cache-file`; the plugin's native CLI lane requires a cache file, and cache-only additionally requires explicit positive `--max-age`. A complete successful result is saved via same-directory temporary write/close/replace, with failed refresh/save preserving the old cache. Cache-only performs no CLI, token-file, profile or network read. It validates the same strict `CodingPlanResult`, rejects stale/future/wrong-protocol/partial/malformed/over-4-MiB input, and preserves the original `observedAt` and reset precision. The helper's `--max-age` default is 45m; cached success alone remains non-authoritative. The raw-token transport's no-cache default is unchanged.

Live Coding Plan cache production also atomically publishes `<cache-file>.attempt.json` with `version:1`, `state` and RFC3339Nano `startedAt`. `running` is written before CLI/token/HTTP I/O. A terminal `failed` adds `completedAt` but no observation/hash; a terminal `success` adds `completedAt`, the cache's original `observedAt`, and lowercase64hex `cacheSHA256` of the exact saved bytes (including the JSON newline). A failed refresh invalidates the receipt without changing the previous cache. Start-publication failure stops before querying; final-success-publication failure emits no success and attempts a failed receipt. No diagnostic/error/secret is stored in this fixed small receipt. Cache-only neither reads nor writes the sidecar and retains its existing helper contract.

Cache and receipt are two files, not a cross-file transaction: after cache commit, receipt-publication failure can leave new cache bytes without a valid matching success receipt. If the sidecar is entirely unwritable, even failed publication cannot be guaranteed; the command returns failure. Same-path producers must be externally serialized. Host-owned Core acceptance must independently require an enrolled, matching successful receipt and hash; these helper receipts are freshness evidence, never remote selected-key/account/plan proof. Core's SPEC92 integration owns these checks; this Qwen fix does not modify Core or Manager.

Domestic: `cn-beijing` / `domestic`; international: `ap-southeast-1` / `international`. No unknown-region fallback. Native counts are exact decimal strings with unit `requests` in `5h`, `week`, `billing_month` windows. Missing/null data, wrong units, unsafe numbers/times and nested errors do not become zero, unlimited quota or partial recovery evidence.

**Query success is not authority:** the helper emits `queryStatus:success`, `authorityStatus:needs_configuration`, `reason:account_authority_unverified`. For explicitly configured CLI/cache sources, native `FetchQuota` preserves all three window buckets (in one quota group), the Coding Plan subscription, observation metadata derived from the original timestamp, protocol and `configured_cli_profile` provenance as candidate evidence. Core must match `quota-resource-bindings` on resource ID plus identity, credential, config and membership versions, and validate source/cache/receipt freshness and matching windows before policy admission. Old `quota-resource-ids` is not a substitute for these bindings. Raw-token native queries still return empty quota plus `needs_configuration`.

The legacy plugin management `quota-usage` view has no Core enrollment/receipt context. **Both bulk and single-credential queries** retain labels/indexes and successful query status but always report `authorityStatus:needs_configuration` and `account_authority_unverified`, without windows, metrics, plan or observation attribution. It does not copy native candidate readings onto configured or panel-added keys. Login JSON, hand-entered account, helper `verified:true` and a successful public models list do not prove selected-key/account/plan ownership.

Legacy standalone bsk/cookie/`--source bl` and Token Plan caches remain rejected by the credential quota command; they are not optional fallback/enrichment. A missing protocol/source also fails before plugin execution. Existing inference routing and configuration defaults are not changed by this quota slice; an inference hostname must not select the quota product.

See [Coding Plan query and authority contract](docs/coding-plan-quota.md) for the current CLI/cache/native-versus-legacy contract and pinned first-party source lines. [Old BL deployment notes](docs/bl-quota-deployment.md) are historical standalone Token Plan records, not current credential-lane guidance.

### Credential naming and selection

Credential labels preserve explicit aliases; otherwise keys are masked for display. `quota-usage` accepts `{}` for listed Qwen credentials or `{"auth_index":"<host credential index>"}` for one; unknown indexes return 404. No key enters helper argv. The embedded same-origin page still requires management authorization; it shows the explicit error rather than fabricated quota. `/quota-info` remains available.

This is supplementary Qwen helper/plugin evidence only, **not Manager -> SQLite -> actual Core end-to-end acceptance, live ownership verification or full functionality**. No Core/Manager source, real credentials/config/provider access or deployment is changed by this Qwen fix.

## Testing

```powershell
$mod = 'D:\path\to\candidate\build-inputs\source.mod'
$env:CGO_ENABLED = '1'
go test "-modfile=$mod" -mod=readonly ./... -cover
go vet "-modfile=$mod" -mod=readonly ./...
```

Tests use dummy keys and mocked HTTP/CLI processes. Live acceptance must use an isolated core on **8399**, a scratch directory outside every repository, its own auth/config, and the built DLL. Do not change/restart the live **8317** instance. Remove scratch credentials and stop the isolated process afterward.

## Known limits

- CLIProxyAPI core v8.0.15's management `/plugins` response does not expose `auth_provider` or `executor` capability booleans, and its `quota_provider` field is the provider identifier string. The native registration envelope declares the booleans; credential/model/execution/quota endpoints demonstrate behavior. This is a host response-schema limitation, not a reason to invent endpoint fields.
- No OAuth flow or refresh token: these are manually configured plan keys. Refresh preserves the credential rather than inventing a token.
- Exhausted inference quota produces the upstream error, not a successful completion. Historical account readings are not current quota or ownership evidence.
- Native CLI/cache candidates require current local Core enrollment and matching receipt evidence; `configured_cli_profile` alone never authorizes policy. This does not independently verify the remote selected-key -> exact Coding Plan/account -> query-principal relationship. Legacy views and raw-token native queries remain `needs_configuration`.
- The plugin accepts explicit CLI/cache configuration but never implicitly enrolls it. Neither helper transport permits legacy browser/cookie/Token Plan fallback. Separate pre-enrollment and future live acceptance need explicit authorization; CLI-internal refresh is the explicit caveat above.
- Coding Plan is the sole credential quota product, not optional enrichment. Successful public model discovery does not validate keys.
- Removed keys may leave host-managed credential files; remove/disable those via the host management flow. A credential file deleted while the host is running is **not** recreated until the next host restart — the plugin materialises credentials from config at registration, so delete + restart if you need a clean single credential.
- No models are advertised before the first successful catalogue discovery. Client formats supported here are Chat Completions and Messages, not Responses.

## Attribution

Structure, native CGO ABI glue, provider lifecycle/credentials, host bridge, executor, protocol adapters, catalog/config/error/thinking utilities, tests and documentation organization are adapted from [massiveits/opencode-go-cliproxyapi](https://github.com/massiveits/opencode-go-cliproxyapi), MIT, Copyright (c) 2026 massiveits. See [NOTICE.md](NOTICE.md) for the file/area inventory and reproduced upstream license. New work is MIT, Copyright (c) 2026 williamxhero; see [LICENSE](LICENSE).
