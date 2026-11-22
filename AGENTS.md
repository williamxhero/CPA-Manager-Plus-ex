## Agent skills

### Issue tracker

Issues and specs live in this repository's GitHub Issues. See `docs/agents/issue-tracker.md`.

### Triage labels

Use `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, and `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

This is a multi-context repository. See `docs/agents/domain.md`.

### Local deployment

For every Manager or panel change, deploy only to the local Windows CPA stack. Build the candidate first, then run `ops/windows/deploy-cpamp.ps1` (or the full-stack wrapper when the stack is rebuilt) with automatic rollback and health verification. Treat a nonzero result as not deployed; preserve the receipt and investigate before retrying. The yosef-server/small-computer environment is outside this repository's deployment scope.

### Credential inference gates

Automatic recover and authenticated resume must clear owned `quota_windows_exhausted` rows even when Core reports `reconciliation_required` (lifecycle drift) or projection membership changed. Match the parent credential resource id (`targets[0]`), not full target DeepEqual.
## Version

- `VERSION` holds the composed identity `{source}+{ex}`, for example `8.0.76+1.0.0`: the newest tag from seakee/CPA-Manager-Plus without its `v`, plus this fork's own semantic version.
- Every requirement that changes this repository ships a version change: run `D:\WILL\AGENT\CPA\ops\update-versions.ps1 -BumpChanged` before committing, or `-BumpEx minor|major` / `-ExVersion x.y.z` when a patch is too small. Merging the source repository refreshes the source part with a plain run. The script rewrites `VERSION` and the generated Go constant that ordinary `go build` compiles.
- `D:\WILL\AGENT\CPA\ops\update-versions.ps1 -Check` must pass before the task is reported finished.
- Never edit the `version_generated.go` file by hand. A test rejects an identity that is not `{source}+{ex}`.
- `-ldflags -X` still overrides the compiled version, but local deployments must rely on the checked-in identity so the running stack reports what was merged.

## 自有 fork 首页与 GitHub About

仅适用于自有远端仓库名以 `-ex` 结尾的 fork。根 README.md 用中文说明相对上游已交付的改进、解决的问题、使用位置和方法，不复述通用安装配置。外部项目首次出现时链接项目主页；集合中的具体组件直接链接其 README 或目录。上游英文原文单独保存在 README-source.md，移动后修正并检查相对链接。首页只留一个上游基础用法入口：优先中文版，否则链接可用版本并标明语言。

合并上游时，将上游 README 的变化同步到归档；即使无冲突，提交前也要将 fork 首页与合并前版本比较，除非用户另有要求，保持首页不变。交付大功能后先更新首页，再从全部已交付功能中按用户价值重选三项，用具体、简短的中文收益更新 GitHub About 并回读确认。三项限制不适用于 README。
