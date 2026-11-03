# SPEC97 real-main shutdown acceptance

The ordinary binary and the explicit `cpa_entry_fixture` build run the same
`cmd/cpa-manager-plus/main.go`. The tagged build replaces only external
BrowserSkill/enrollment/page dependencies and scheduler time with authenticated
loopback fixtures. It is not a release artifact or real-account acceptance.
Ordinary builds cannot enable these fixtures through environment variables.

On shutdown, main explicitly calls the outer coordinated service's `Shutdown`
and waits for `Run`, both within its existing ten-second shutdown context. This
covers an initialization error followed by later manual work. A coordinator lock
or uncooperative dependency cannot make the main wait unbounded: expiration is
logged before proceeding to SQLite close. That fallback does **not** promise all
hosts joined or cancellation was persisted. Startup recovery fences interrupted
jobs; the previous successful observation remains available and stale.

The opt-in Windows Python runner builds real executables offline, starts only
owned processes with isolated SQLite and loopback Core/BrowserSkill fixtures,
and sends CTRL_BREAK only to those process groups. It requires an installed Go
toolchain and cached dependencies. All keys and account identities are synthetic;
no logged-in browser, real account, remote service, or deployment is used.

From the repository root, choose an output directory that does not exist:

```powershell
python -X utf8 -B docs/verification/spec97-main-recovery/acceptance.py D:/scratch/spec97-shutdown `
  test_run_initialization_failure_still_drains_later_manual_host `
  test_slow_core_finalization_reports_bounded_shutdown `
  test_graceful_stop_persists_cancelled_attempt `
  test_ordinary_build_rejects_fixture_environment
```

`CPAMP_ENTRY_GO` optionally selects the local Go executable;
`CPAMP_ENTRY_GOCACHE` selects an existing local build cache. `GOPROXY`, `GOSUMDB`,
and telemetry are disabled. The output contains source fingerprints, build
commands and SHA256, module metadata, raw test and process logs, exact exit codes,
and the runner snapshot. The four selected scenarios are four top-level tests;
process counts and parameter cases are reported separately. The retained hourly
and identity/version scenarios can be selected by name when those paths change.

Default-product enrollment/parser integration, observed real LAYSO page mapping,
visual/browser acceptance, SPEC integration gates, and installation/deployment/
rollback remain incomplete. This local evidence does not close #97.
