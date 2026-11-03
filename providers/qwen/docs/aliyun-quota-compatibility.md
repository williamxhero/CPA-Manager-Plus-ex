# Aliyun quota compatibility in CPA-Qwen

CPA-Qwen is the canonical maintenance/build source. The source tree has one Go
module, one `internal/bailianquota` implementation and one `cmd/bailian-quota`
CLI. `cmd/aliyun-bss-quota-plugin` is a quota-only native entrypoint, **not a
second repository or a second CLI**. See `NOTICE.md` for migration provenance.

## Three separate products

| Entry | Product and authority |
| --- | --- |
| Native `qwen-cliproxyapi` | Selected-credential inference; strict Coding Plan CLI/cache candidate quota, subject to Core enrollment/receipt validation. |
| Compatibility `aliyun-bss-quota`, `quota-source: command` | Existing account-level console/Token Plan CLI readings. No inference, auth provider or model catalogue. |
| Compatibility `aliyun-bss-quota`, `quota-source: bss` | Signed AK/SK account money, optional current-month spend and actual Model Studio resource packages. Not Coding Plan request usage or key ownership. |

Both DLLs may be built and loaded by an explicitly configured isolated host.
Do not enable both for the same purpose by assumption. Provider matching and a
successful account read do **not** establish an API key's account ownership,
nor do these compatibility readings become automatic-pause policy evidence.
The native Qwen credential lane continues rejecting browser/cookie/Token Plan
commands; migration is not a fallback from that lane into the compatibility DLL.

## Build

Run the repository's `scripts/build-artifacts.py` with an explicit compatible
Core source, Go 1.26.10+ and C compiler. It produces `qwen-cliproxyapi.dll`,
`aliyun-bss-quota.dll` and `bailian-quota.exe` on Windows, plus generated native
headers and an all-success manifest. Linux/macOS filenames use their native
suffixes; Windows execution evidence is not evidence of other platforms working.

The build takes no dependency on the old Aliyun checkout. It does not install,
restart, switch runtime configuration or delete anything. Failed builds retain
diagnostic candidates without a success manifest; previous candidates remain
untouched. Deployment remains a separate backup/health-check/automatic-rollback
transaction and requires its own authorization and evidence.

## Configuration (illustrative, never installed automatically)

```yaml
plugins:
  configs:
    aliyun-bss-quota:
      enabled: true
      quota-source: command
      command: 'D:/path/to/candidate/bailian-quota.exe'
      command-args: ['--json']
      command-timeout: 90s
      command-workdir: 'D:/path/to/isolated-working-directory'
      supported-providers: ['openai-compatibility']
      debug-log-requests: false
```

Command arguments are an array, never a shell string. `command-workdir` is
retained. Commands have bounded stdout/stderr, a positive deadline, sanitized
diagnostics and fail on error JSON even when a subprocess exits zero. Missing or
null readings are omitted; real zero is retained. Non-finite/out-of-range
percentages, malformed times, and a response containing neither usable windows
nor usable metrics fail instead of yielding a fabricated successful reading.
Both compatibility command mapping and legacy Qwen normalization use
`internal/quotawire`; native Coding Plan retains its stricter protocol validator.
Known credential diagnostics also redact mixed-case percent escapes and common
second-encoding forms. Native decode failures retain bounded, sanitized helper
stderr classifications without treating ordinary warnings as failed readings.
Compatibility requests with metadata/storage nesting beyond the bounded secret
walk are rejected before launching the helper, never partially redacted.

For BSS, replace the source with the following example:

```yaml
quota-source: bss
ak-file: 'D:/private/operator-supplied-ak.json'
region: cn-beijing
endpoint: https://business.aliyuncs.com/
request-timeout: 20s
include-bill: true
include-resource-packages: false
supported-providers: ['openai-compatibility']
debug-log-requests: false
```

No real AK file is provided or read by the build/tests. If configured by the
operator, its JSON keys are `access_key_id`, `access_key_secret` and optionally
`region`. Per-field precedence remains literal configuration, then AK file,
then expanded/fallback environment (`ALIYUN_AK_ID` / `ALIYUN_AK_SECRET`, then
`ALIBABA_CLOUD_ACCESS_KEY_ID` / `ALIBABA_CLOUD_ACCESS_KEY_SECRET`). An unreadable
or malformed explicitly selected AK file is an error. Invalid reconfiguration
keeps the previous valid state; shutdown clears it.

The legacy `debug-log-requests: true` compatibility behavior accepts any incoming
provider while describing the configured supported-provider list. It now logs
**fixed counts/flags only**, never request IDs, provider values, attribute names,
credentials or signed URLs. Set it to `false` for strict configured-provider
matching. This compatibility flag is not an authorization bypass in the host.

## BSS data and network boundaries

`QueryAccountBalance` is required. `QueryAccountBill` and
`QueryResourcePackageInstances` are individually optional: their failure omits
only the corresponding enrichment. Incomplete pagination is rejected, not
presented as a full monthly/package total. Current billing month uses the China
account calendar. Available/cash/credit/monthly-spend metrics and resource-package
fractions remain distinct; absence is never zero. Existing BSS `float64` public
types are retained for compatibility, not advertised as exact-decimal policy
evidence. Real-account currency/amounts still need authorized acceptance.

Production reads require HTTPS with normal certificate verification and public
destination IPs. DNS answers are validated before dialing a checked address;
redirects and environment proxies are not used. `allow-http: true` exists only
for explicit HTTP loopback fixtures, not private-network access. There is no new
automatic login, browser storage reading, API-key forwarding to console CLI or
silent API-to-web fallback.

## Migration and deletion gate

The legacy source identity is `williamxhero/CPA-Aliyun-Quota`, recorded historical
HEAD `25fcdfe21577a742377c51b909b2d5627b5e5f3f`. Its four historical commits are
listed in `NOTICE.md`. Historical references are provenance, not build inputs.

Do not delete or archive the old source as a shortcut to acceptance. The original
SPEC requires full behavior/test/consumer review, clean-checkout builds,
isolated-host acceptance, separately authorized mainline/push and runtime
switching, identity verification and post-delete rebuilding before final
completion. Development candidates alone satisfy none of the destructive gates.
Manager's copied Qwen tree must become a verified canonical-source snapshot;
until that switch is actually verified it is an outstanding migration item.
Real-account/browser tests, deployed service behavior, remote synchronization
and deletion must be reported separately from controlled local tests.

### Consumer provenance verification

After independently reconciling consumer-specific fixes into canonical Qwen,
review and commit the canonical source, and update the Manager copy deliberately.
Use `scripts/verify-manager-snapshot.py --canonical-source <clean-CPA-Qwen>
--snapshot <Manager/providers/qwen> --write-receipt` only for initial enrollment.
This writes `CPA_QWEN_SOURCE.json` only when every source file already matches,
including the full canonical commit/tree and per-file SHA-256. An existing receipt
is never overwritten. For subsequent read-only checks omit `--write-receipt`.

The verifier rejects missing, extra or differing files, nested Git metadata,
symlinks, stale receipts, dirty canonical source and a nested consumer presented
as canonical. It never synchronizes/deletes consumer files or picks a conflict
winner. Updating a receipt requires separately reviewing the new snapshot; do not
edit its commit number to conceal differences. Manager-local product fixes must
first return to CPA-Qwen rather than create an independent maintenance branch
inside `providers/qwen`.
