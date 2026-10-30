# Capability support pass: backend verification

Date: 2026-10-25. Branch: `claude/issue70-capability-status`.
Base: `ce11c61def7fd01d85a7460eda50425e7fefee5e`.

Scope: the manager HTTP/router/auth/controller/service seam with `testutil`
temporary SQLite persistence. This is not full
[T01](https://github.com/williamxhero/CPA-Manager-Plus-ex/issues/70) or
[parent SPEC](https://github.com/williamxhero/CPA-Manager-Plus-ex/issues/66)
acceptance, core routing verification, deployment verification, or a frontend test report.
See [the API contract and remaining requirements](../resource-capabilities.md).

## Red / green record

All commands below run from `apps/manager-server` in PowerShell with `-count=1`.
After a default Go-cache access-denied build attempt, subsequent commands used:

```powershell
$env:GOCACHE = Join-Path $env:TEMP 'cpamp-t70-go-cache'
```

That cache-only retry is not counted as a behavior-test red. An earlier incorrect
file path produced `[no tests to run]`; that is likewise not counted as evidence.

Each behavior test below was written and executed before its corresponding fix;
commands were rerun unchanged after implementing the minimum fix.

| Exact command | Red | Green |
| --- | --- | --- |
| `go test ./internal/httpapi -run '^TestResourceCapabilitiesTruthfulSupport$' -count=1` | FAIL: missing route returned 404, expected 200. | PASS after adding service, authenticated controller, app construction and route. |
| `go test ./internal/httpapi -run '^TestResourceCapabilitiesAuthorizationBoundary$' -count=1` | FAIL: synthetic private auth-storage detail appeared in logs. | PASS after removing raw verifier errors from the shared auth logger; authorization decisions/responses unchanged. |
| `go test ./internal/httpapi -run '^TestResourceCapabilitiesReadOnlyWithSavedConnection$' -count=1` | FAIL: rejected methods did not advertise `Allow: GET, OPTIONS`. | PASS after adding that header; all non-GET/OPTIONS methods still reject with 405. |

Additional regression guards were added and passed without claiming additional
behavior-red cycles:

```powershell
go test ./internal/httpapi -run '^TestResourceCapabilities' -count=1
```

These cover exact successful shape (no extra binding/routing fields), unchanged
legacy policy JSON, persisted policy after reopening SQLite, environment locks
and dependency semantics without gate ACK, subsequent legacy PATCH behavior,
no unauthenticated setup exception, generic auth/storage errors without decoys in
responses or logs, no stale success after a storage failure, and no-store on
success/error/method/preflight paths.

The saved-connection test uses only a temporary deny-all `httptest` upstream
fixture, verifies its request count remains **zero**, checks unchanged persisted
policy/manual cooldown/action/connection data, and observes SQLite `data_version`
on a fixed separate connection to detect any committed manager write. GET,
OPTIONS, rejected mutations, missing auth and the synthetic CPA key all leave
that version unchanged. No application server, browser, live runtime or real
configuration is used; the test fixture closes during cleanup.

## Relevant regression scope

```powershell
go test ./internal/httpapi ./internal/service/automation ./internal/app ./internal/http/router ./internal/http/middleware ./internal/store ./internal/service/resourcecapability ./internal/http/controller/resourcecapability -count=1
```

Result: **PASS** for `httpapi`, `service/automation`, `app`, `http/middleware`,
and `store`. `http/router`, `service/resourcecapability`, and
`http/controller/resourcecapability` report `[no test files]`; their integration
is exercised through the real HTTP-seam tests. No existing tests were modified.

Additional focused regression/static checks (PASS):

```powershell
go test ./internal/worker -run 'Automation' -count=1
go vet ./internal/service/resourcecapability ./internal/http/controller/resourcecapability ./internal/httpapi ./internal/http/middleware ./internal/app ./internal/http/router
```

The capability-only command above was rerun after the final saved-connection
authentication assertions and passed. `git diff --check -- apps/manager-server docs`
also passed. All test processes and temporary test fixtures have exited.
The requester reported that the pre-implementation `npm run manager-server:test`
baseline already failed on Windows (unsupported signals, POSIX-mode assertions,
and open-file operations), while relevant httpapi/app/store/automation packages
passed. This pass does not fix, skip, or weaken those unrelated tests and does
not claim the full suite is green. Live core identity, full inventory/lifecycle
isolation, independent gate application and real routing/Hermes acceptance remain
outstanding.

## Coordinator verification

The coordinator independently ran the following against the completed backend
candidate on Windows; both passed:

```bash
go -C apps/manager-server test -race ./internal/httpapi ./internal/http/middleware ./internal/service/automation ./internal/app ./internal/store ./internal/service/resourcecapability ./internal/http/controller/resourcecapability
```

```bash
go -C apps/manager-server vet ./...
```

The manager entrypoint (`./cmd/cpa-manager-plus`) also built successfully to a
26,161,664-byte executable in a temporary directory. The executable was not run,
and the temporary build output was removed. These checks do not override the
pre-existing Windows full-suite failures or establish live/core routing behavior.
