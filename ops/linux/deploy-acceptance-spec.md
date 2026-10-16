## #62 / #65 验收补充规格：部署失败闭合与可信回退

本补充仅完善原 #62/#65，不新建 SPEC，不关闭 issue。2026-10-16；隔离实现与自测进行中，独立验收由 Hermes 执行。规格与测试结果分别记录，**隔离测试不等于真实部署验收**。

## Problem Statement

运维需要部署失败时可证明恢复到原版本、原配置与原插件，并能区分部署失败但恢复成功和恢复本身失败。现有脚本不能提供足够证据：core 的指针文本备份与符号链接复制使用同一目标、备份与配置写入错误没有闭合、help 被当作配置检查、仅 HTTP 401/200 不能证明目标进程；Manager 的备份吞错、预演共享临时目录与宽泛进程清理、失败时缺少统一恢复、版本文本指针未随回退恢复。

## Solution

在保留现有默认调用兼容性的前提下，提供显式隔离参数、严格的备份/切换/恢复状态与本机 Docker 内失败注入测试。任何成功结论都必须同时满足磁盘状态、目标进程身份与健康恢复的契约；不以 fake systemctl 返回 0 代替健康证据。

## User Stories

1. As an operator, I want existing default invocations to remain supported, so that deployment automation stays compatible.
2. As an operator, I want explicit isolated root, service and health/rehearsal settings, so that tests cannot touch live instances.
3. As an operator, I want dry-run to avoid persistent changes and service restarts, so that inspection is safe.
4. As an operator, I want backup failure to abort before mutation, so that recovery is never assumed without a complete backup.
5. As an operator, I want the prior release pointer preserved independently of copied files, so that restoration never interprets a symlink as text.
6. As an operator, I want configuration installation errors to be detected, so that partial writes cannot be reported as success.
7. As an operator, I want the plugin backup and restore contract documented and tested, so that rehearsal or runtime cleanup cannot silently lose plugins.
8. As an operator, I want configuration validation to exercise real startup in isolation rather than help output, so that invalid configuration blocks deployment.
9. As an operator, I want health checks tied to the intended service process, so that another listener's 401/200 cannot falsely pass deployment.
10. As an operator, I want replacement, pointer switch, restart and health failures to trigger coherent recovery, so that failure paths are as reliable as the happy path.
11. As an operator, I want Manager app, panel and release-text pointers restored together, so that rollback does not leave inconsistent versions.
12. As an operator, I want rollback-only to work independently of new deployment artifacts, so that emergency restoration remains available.
13. As an operator, I want distinct machine-readable states for preflight failure, successful deployment, successful recovery and failed recovery, so that automation can act correctly.
14. As an operator, I want all logs and results to omit secrets and raw config, so that diagnostics are safe to share.
15. As an operator, I want cleanup to target only a process created by the current run, so that unrelated services and port owners remain untouched.
16. As an operator, I want the accepted core target to remain 8.0.15, so that the obsolete 8.0.13 wording cannot cause a downgrade.
17. As an acceptance reviewer, I want isolated and live evidence clearly separated, so that simulated recovery is never mistaken for production acceptance.

## Implementation Decisions

- Recommended defaults selected without waiting, as authorized: retain current service/root/port defaults and 8.0.15 target; add opt-in isolation overrides rather than changing production defaults.
- The final accepted core version is **8.0.15**, per the completed alignment record in #62 and the 2026-10-16 clarification in #65. The historical 8.0.13 issue wording is not the target.
- Limit implementation to the Linux deployment scripts and their tests/documentation. Do not touch #66 planning, business services or UI.
- Use one highest-level test seam: invoke each deployment CLI inside an isolated Linux container with disposable fixture roots and fake transport/service boundaries. Validate externally observable state, not helper function implementation.
- Backup metadata must be unambiguous and validated before mutation. Preserve configuration bytes, plugin snapshot/absence semantics, application and panel destinations, and release-text state where applicable.
- Stage configuration and pointer updates and check every operation. After any mutating failure, recovery must restore the whole documented state and explicitly check restart and health outcomes. Incomplete backup must prevent mutation.
- Real startup validation must use isolated config, writable data and independent plugin copies; help output alone is not validation. In this task, all inputs/processes use synthetic fixture configuration/credentials only, and no process shares writable live plugin trees. This is a restriction on task execution, not a claim that production rehearsal scrubs inline credentials from an explicitly staged configuration; doing that would alter the validation input and is not an implemented feature.
- Dry-run must not change the deployment tree, restart services, or leave remote artifacts. If temporary rehearsal is retained, use a unique disposable tree and only terminate its owned PID; document and test cleanup. Never use broad pkill patterns or kill a process solely because it owns the selected port.
- No-restart compatibility must be explicit: installing without restart is not a claim of verified running-service health.
- Health acceptance must combine expected response behavior with intended process identity/version. Fake service success alone and an unrelated endpoint's 401/200 are insufficient.
- Machine-readable statuses must distinguish failed deployment with verified restoration from rollback failure. Result serialization must handle supplied values safely, and logs must not include raw config or credentials.
- Local Docker is available (Linux/amd64 engine verified); use no published host ports and no privileged container, host PID namespace, host service manager or real config mounts. No SSH to yosef-server and no local live8317 access.

## Testing Decisions

Good tests invoke the actual CLI, inject failure at external command/service boundaries, and assert exit status, result JSON, complete file/pointer state and health/process evidence. All fixtures contain synthetic data only. Existing Windows deployment/result semantics are compatibility prior art; the Linux scripts currently have no colocated regression suite.

Required core cases:
- Success on synthetic old/new releases with target default 8.0.15.
- Dry-run leaves fixture content and service state unchanged.
- Original backup collision reproduced, then regression test passes with separate validated pointer metadata.
- Backup/config installation/pointer switch/restart/health failures detected; no false success.
- Verified restoration of old pointer, exact config bytes and plugin contents (including initially absent files) after failure.
- Rollback write/restart/health failure returns clear rollback_failed/nonzero state.
- Invalid configuration fails real isolated startup despite a successful help command.
- An unrelated 401/200 responder cannot pass the target identity check.
- No-restart result does not claim verified service health.

Required Manager cases:
- Success restores agreement among binary/panel destinations and release text.
- Dry-run has zero persistent tree/service side effects.
- Backup, transfer/replace, partial pointer switch, restart and health failure handling.
- Automatic and rollback-only recovery restore all pointers and release-text presence/content; rollback-only does not require new artifacts.
- Restoration failure is separately reported.
- Rehearsal cleanup does not terminate an unrelated listener/process, and runs do not share a fixed temporary tree.
- Synthetic credential canaries never appear in logs/results, including failed commands.

Run the smallest relevant regression suite first; demonstrate fail-before/pass-after for original defects where feasible; then shell syntax checks and the complete isolated suite. Preserve commands and results for independent Hermes acceptance. Container test success is not live deployment evidence.

## Out of Scope

Live deployment; connecting to or modifying local live8317 or yosef-server; reading real config/credentials; credential synchronization; Qwen console login/quota work; #66 planning; production infrastructure changes; merges, releases or issue closure. No downgrade to 8.0.13.

## Further Notes

CodeGraph is not initialized for this worktree, so source was read directly. Docker is available; WSL distributions were listed but not started. No real configuration, credentials or live service were accessed.

GitHub publication constraint: posting this supplement and adding `ready-for-agent` to #62/#65 was denied by the auto-mode classifier as an external write. No comments or labels were changed. This file is the preserved payload for Hermes; publish the same body to both existing issues and apply the existing label, without creating/closing issues. Permission denial is not retried through another tool/session.

Workflow record: `/to-spec` was dispatched. `/implement-spec` dispatch was rejected (`disable-model-invocation`). At the next safe boundary the user explicitly supplied dev-workflow-rules 3.0.0, superseding conflicting old workflow rules and directing continuation in this same session/worktree. Its fallback requires reading the phase skill and following its procedure without claiming slash dispatch. The implementation phase skill was read and followed with the explicit single-writer/no-new-worktree/no-merge/no-close overrides. `tdd` was dispatched. No additional writer was started.

## Implementation and isolated evidence

- Core now fails closed on backup/install/switch/restart errors, keeps pointer metadata separate from copied release/config/plugin snapshots, restores the complete plugin tree (including absence of newly introduced files), verifies state plus restart and target-owned health, and distinguishes rolled_back (3) from rollback_failed (4).
- Manager now checks backup/transfer/switch/restart operations, uses immutable fresh version labels, atomically updates individual pointers, restores app/panel/release-text state together, selects completed backups for rollback-only without requiring new artifacts, and verifies restoration rather than accepting systemctl success alone.
- Both use private unique rehearsal trees and own-child PID cleanup, never pkill/fuser or kill-by-port. Health requires the expected executable as systemd MainPID plus ownership of the actual listening socket and an appropriate response. A healthy foreign 200/401 is rejected.
- Python3 standard library is used for the shared checker and JSON serialization (Manager already depended on Python for checksums); no third-party production package was added. Core additionally uses util-linux flock on its existing root inode, with no dry-run lockfile.
- Intentional fail-closed compatibility changes: Manager requires a fresh release label rather than overwriting recoverable/running version directories; rollback-only refuses legacy/incomplete backups missing the new completeness/release-text contract; core no-restart reports installed_not_restarted, not runtime-verified deployed. Existing default roots/services/ports, core 8.0.15 and legacy core switches remain.
- Baseline red evidence: original core returned exit 0 and `RESULT: deployed` after injected systemctl restart exit 1 and an arbitrary 401. After gating restart, original recovery still falsely reported healthy restoration with restart failure. Original Manager health-failure test restored app/panel but asserted `new\\n != old\\n` for release text.
- Isolation: local Linux/amd64 Docker container, `--network none`, read-only mount of only Linux deployment source/tests; no host ports, service sockets, privileged flags, host PID namespace or real data mounts. Fake SSH and systemd are boundaries only; synthetic compiled ELF services actually bind/respond, expose distinct executable/PID identity and exercise recovery.
- Current isolated suite: **26 tests passed**. Covers success, both dry-runs, complete backup, backup/config-write/transfer/pointer/restart/health failures, plugin deletion/new-file absence restoration, rollback write/restart/health failure, rollback-only with missing artifacts/incomplete backup, absent release text, invalid config despite successful help, foreign 200/401, occupied rehearsal port and secret-canary/JSON serialization checks. See the colocated README for repeatable commands.
- Review: Standards axis found two safety violations (plugin-link aliases and health redirects); both have red/green CLI regressions and fixes. Spec axis also found rollback-only depending on current artifacts and silenced inherited ERR-trap output; both have red/green regressions and fixes. A fifth spec finding concerned retained inline credentials: the initial supplement overgeneralized the task's prohibition into a production credential-sanitizer promise. The contract is clarified above to match the original task: no real configs/credentials are used in task execution; future production rehearsal explicitly loads its operator-staged values in private scratch and is not a sanitizer. No credential-reading permission is expanded, no synthetic-only test requirement is relaxed, and no unimplemented redaction is claimed.
- Publication handoff: local implementation commit exists. Fork push was denied by the classifier; fork `dev` is also absent (404; branch list contains main only), while repository rules prohibit feature PRs to main. No branch/PR/issue state was changed remotely. The issue supplement and PR body/blocked-action details are preserved in the colocated handoff for Hermes's authorized delivery; no alternate-tool/session retry is made.
- Still required: final regression on the final candidate, authorized publication and Hermes independent acceptance. Real core/plugin ABI startup, actual host systemd identity, real live failure/recovery and no-user-visible-interruption evidence are **not** established by these fixtures. No deployment is authorized in this task.
