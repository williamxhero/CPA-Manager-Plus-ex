# Coding Plan refresh wrapper

`refresh-qwen-quota.ps1` invokes the configured helper directly with:

```text
--json --source coding-plan --protocol qwen-coding-plan-v1 --bl-path <BlPath> --timeout <QueryTimeoutSeconds>s --cache-file <CacheFile>
```

`QueryTimeoutSeconds` defaults to 15; PowerShell rejects values outside 1–60
before the helper runs. This is the query budget, not the existing separate
90-second interactive recovery window. Paths remain native executable arguments,
not shell commands. Successful cache writes belong to the helper; the wrapper
does not replace a cache after a failed query. It preserves the existing helper
exit code and `refresh.out.log` / `refresh.err.log` filenames.

This source correction does not install/update the helper or script, change
scheduled cadence, logon identity, supplied cache/state directories, or the
default BL path. Existing `not_logged_in` recovery and one-notice-per-episode
behavior are unchanged. Enrollment/login is a separate operator action: the
offline tests do not launch a real login, browser, task, service or official CLI.
Official CLI internals may refresh their own profile; invocation alone is not a
no-write guarantee or selected-credential/account ownership proof.

Use a helper that supports this exact versioned Coding Plan CLI/cache contract;
an older helper can reject the new arguments. This change does not turn the
account-scoped result into per-key quota or enable policy authority. Explicit
profile/source mapping and integration acceptance remain separate gates.

The focused public test is `ops/tests/refresh-qwen-quota.test.ps1`. Run it with
Windows PowerShell 5.1, a new private `-ArtifactDirectory`, and `-Case Success`,
`UnrelatedError`, `SelfTest`, `BudgetMaximum`, `BudgetTooSmall` or `BudgetTooLarge`.
It compiles a tiny native .NET argument recorder locally, not a product helper.
All account/profile/cache/state/executable paths in the test are synthetic and
private; the existing script `-SelfTest` is invoked without changing its cwd
semantics. No additional dependency or external test framework is needed.
