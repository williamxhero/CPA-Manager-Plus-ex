# SPEC #8 Aliyun quota consolidation (development, not deployed)

Build the native Qwen provider, quota-only `aliyun-bss-quota` compatibility
provider and the single `bailian-quota` CLI from CPA-Qwen's one Go module.
Migrate BSS signing/balance/monthly-spend/resource-package behavior and preserve
legacy command/configuration semantics with bounded execution and strict
missing/null/numeric/error handling. Money is separate from plan request windows
and does not certify credential ownership or automatic-pause authority.

`scripts/build-artifacts.py` requires an explicit compatible Core SDK checkout;
successful candidates carry source/toolchain/artifact manifests, failed ones
carry diagnostic logs without a success manifest. It never deploys or deletes.
Manager snapshot switching, full isolated-host/clean-checkout acceptance,
mainline/remote delivery, runtime switching and old-repository deletion remain
separate uncompleted gates until actually evidenced.

# SPEC92 / #77 selected inference scope

Preserve each host-issued invocation callback ID in HTTP and SSE executor
callbacks. Advertise anonymous additive `inference_scoped_http` JSON metadata
while retaining the normal published SDK v8.0.0 pin and native C ABI.

The host owns durable gates, exact source eligibility and per-attempt admission.
Quota account ownership remains a separate unsupported/needs-configuration
contract; this change does not certify quota authority or change money policy.
No new inference timeouts, runtime configuration, or management quota path.

Offline callback-wire tests and opt-in isolated actual Core/DLL live smoke cover
both streaming and nonstreaming. The controlled smoke protocol mirrors the
canonical OpenCode gate fixture. No default runtime/provider calls.
