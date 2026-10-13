# providers/ 导入边界（CPAMP-EX）

本目录是 CPAMP 单仓中**插件源码**的落点。它由 SPEC5-A 定义边界，只固定契约，
不含任何已导入的插件源码；`providers/opencode-go` 与 `providers/qwen` 分别由
SPEC5-B、SPEC5-C 落地。

- 机器可读契约：`providers/import-boundary.json`
- 人读契约：`docs/providers-layout-contract.md`
- 归属声明：根目录 `NOTICE`
- 契约校验：`node bin/release/check-provider-import-boundary.mjs`（加 `--json` 输出结构化结果）

## 目录约定

```
providers/
  README.md               # 本文件
  import-boundary.json    # 机器可读契约
  opencode-go/            # SPEC5-B：OpenCode Go 插件（git subtree）
  qwen/                   # SPEC5-C：Qwen 插件 + bailian-quota CLI（一次性导入）
```

每个 provider 子目录保持**独立 Go module**（各自的 `go.mod`），不并入
`apps/manager-server` module。

## 硬性规则（校验脚本会强制）

1. **禁止二进制**：`.dll` `.exe` `.so` `.dylib` `.pdb` `.lib` `.o` `.a` `.obj` `.bin`
   `.class` `.jar` `.node` `.wasm` `.msi` `.zip` `.7z` `.tar` `.gz` 不得入库；构建产物
   放在各自 `bin/`（已被 `.gitignore` 忽略）。
2. **禁止密钥**：不得提交真实凭据。校验脚本按 `import-boundary.json` 的
   `globalForbidden.contentPatterns` 扫描私钥块、`sk-*`、`AKIA*`、`AIza*`、Slack token
   及通用 `key/secret/token/password = "..."` 赋值；命中即失败，且输出对匹配值脱敏。
3. **禁止缓存 / 工作区垃圾**：`node_modules/`、`.cache/`、`__pycache__/`、`.pytest_cache/`、
   `dist/`、`go-build/`、`.DS_Store`、`Thumbs.db` 不得入库。
4. **OpenCode 额外排除**：`plugins/**`（含 `plugins/windows/amd64/*.dll|*.h`）、`*.log`、
   `worker*`（`worker8-err.log` / `worker8-result.json`）、`prompt8-credential-route.md`。
   这些来自 fork 树的构建/工作区垃圾，`git subtree` 无法过滤，须在导入后的维护提交中移除，
   且每次同步后重跑校验。
5. **许可证与归属**：每个 provider 必须带 `LICENSE`；Qwen 还必须带 `NOTICE.md`；根 `NOTICE`
   必须覆盖每个 provider 的来源仓库、SPDX 与版权行（校验脚本比对 `import-boundary.json`）。

## OpenCode 同步入口（SPEC5-B 执行）

```bash
git remote add opencode-upstream https://github.com/massiveits/opencode-go-cliproxyapi.git
git fetch opencode-upstream
# 首次导入
git subtree add  --prefix=providers/opencode-go opencode-upstream main --squash
# 后续同步
git subtree pull --prefix=providers/opencode-go opencode-upstream main --squash
node bin/release/check-provider-import-boundary.mjs   # 必须通过；失败则先移除被排除路径
```

> `git subtree` 不能按路径过滤，导入源与本地定制（本地 fork `williamxhero/
> opencode-go-cliproxyapi-ex` 领先上游 17 个提交）的取舍由 SPEC5-B 决定并记录；
> 冲突不得强行覆盖，必须报告并测试。

## Qwen 导入（SPEC5-C 执行）

Qwen 为**一次性目录导入**（`williamxhero/qwen-cliproxyapi`，MIT），无 subtree 上游；
出现真实上游同步需求时另立 SPEC。导入后同样受上述硬性规则约束。
