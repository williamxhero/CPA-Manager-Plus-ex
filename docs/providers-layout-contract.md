# CPAMP 单仓 providers 布局、许可证与导入边界契约（SPEC5-A）

- 状态：契约已定义，校验脚本与测试已落地；**插件源码尚未导入**（`providers/opencode-go`
  与 `providers/qwen` 分别由 SPEC5-B、SPEC5-C 落地）。
- 关联：Issue #5、现状调查 comment 6080840163、协调者默认选择 comment 6080913010。
- 范围：**只定义仓库布局契约**。本 SPEC 不移动、不复制任何插件代码，不读真实密钥，
  不碰线上，不修改共享 CPAMP 主树，也不修改 `package.json` / `.github/workflows`
  （构建与 CI 落地在 SPEC5-D）。

## 1. 目标布局

```
providers/
  README.md               # providers 树边界说明
  import-boundary.json    # 机器可读契约（目录 / 许可证 / 排除 / 构建接口）
  opencode-go/            # OpenCode Go 插件源码（SPEC5-B，git subtree）
  qwen/                   # Qwen 插件源码 + bailian-quota CLI（SPEC5-C，一次性导入）
```

- 每个 provider 保持**独立 Go module**，不并入 `apps/manager-server` module。
- 面板（`apps/web` / `apps/manager-server`）仍是插件的**消费者**：运行时由 CPA 加载
  插件，面板不声称替代 CPA 执行器。

## 2. providers/opencode-go 边界

| 项 | 值 |
|---|---|
| 插件 ID | `opencode-go-cliproxyapi` |
| 导入方式 | `git subtree` |
| 命名上游 remote | `opencode-upstream` → `https://github.com/massiveits/opencode-go-cliproxyapi.git`，branch `main` |
| 首次导入 | `git subtree add --prefix=providers/opencode-go opencode-upstream main --squash` |
| 同步入口 | `git subtree pull --prefix=providers/opencode-go opencode-upstream main --squash` |
| 许可证 | MIT，`Copyright (c) 2026 massiveits`；须保留 `LICENSE` |
| 必需文件 | `LICENSE`、`go.mod`、`main.go` |

**排除清单**（subtree 无法过滤，导入后须移除并在每次同步后重跑校验）：

- `plugins/**`（构建/工作区目录，含 `plugins/windows/amd64/*.dll` 约 11 MB 与生成的 `*.h`）
- `*.dll` `*.exe` `*.so` `*.dylib` `*.pdb` `*.lib` `*.o` `*.a`
- `*.log`、`worker*`（`worker8-err.log`、`worker8-result.json`）、`prompt8-credential-route.md`
- 缓存/工作区垃圾：`node_modules/**` `.cache/**` `__pycache__/**` `dist/**`

**需保留的本地定制**（本地 fork `williamxhero/opencode-go-cliproxyapi-ex` 领先上游 17 个
提交，SPEC5-B 负责在导入中保留、冲突必须报告不覆盖）：

- 凭证管理路由与脱敏：`504d2e85`、`d97568fc`（label=alias，否则脱敏 api key，文件名不含 key 片段）、
  `f88846cc`、`86f6a27f`（host 相对 auth 路径解析；相对/不可读路径不得中断注册）。
- 额度：`d41f0a24`、`0900bbec`（v7/v8 原生额度 provider）、`8add1563`、`97548372`（5h 滚动窗口）、
  `ae954849`、`74f9d0ab`。
- 目录适配：`a7a6b85a`、`f5806103`。
- 构建产物提交 `495e197c`（c-shared 产物）**不得导入**（见排除清单）。

> **导入源未定**：上游 `massiveits/...` 干净但落后本地 fork 17 个提交。SPEC5-B 决定
> “上游 + 重新套用定制” 还是 “fork 树 + 排除产物”，并在其 SPEC 记录中留痕。本契约只固定
> 目录、排除清单、remote 名与同步命令形态。

## 3. providers/qwen 边界

| 项 | 值 |
|---|---|
| 插件 ID | `qwen-cliproxyapi` |
| 导入方式 | **一次性目录导入**（不虚构 upstream 或 subtree remote） |
| 来源 | `williamxhero/qwen-cliproxyapi`，默认分支 `claude/qwen-provider` |
| CLI | 内嵌 `cmd/bailian-quota`（+ `internal/bailianquota/*`），`go build -o bin/bailian-quota.exe ./cmd/bailian-quota` |
| 许可证 | MIT，`Copyright (c) 2026 williamxhero`；须保留 `LICENSE` 与 `NOTICE.md` |
| 必需文件 | `LICENSE`、`NOTICE.md`、`go.mod`、`main.go`、`cmd/bailian-quota/main.go` |

- 出现真实上游同步需求时**另立 SPEC**，不在此虚构同步机制。
- Qwen 组件改编自 `massiveits/opencode-go-cliproxyapi`（MIT）；`providers/qwen/NOTICE.md`
  必须原样保留。
- 早期本地仓库 `aliyun-bss-quota-cliproxyapi` 仅作诊断对照，不纳入。

## 4. 许可证与归属策略

- 本仓库自身：MIT（`LICENSE`，`Copyright (c) 2026 Seakee`，fork 修改由 williamxhero 维护）。
- 根 `NOTICE`：列出每个 provider 的来源仓库、SPDX、版权行；**先登记后导入**，使导入无法
  在缺少归属的情况下落地。
- 每个 provider 子目录保留其 `LICENSE`；Qwen 额外保留 `NOTICE.md`。
- 校验脚本比对 `NOTICE` 是否覆盖 `import-boundary.json` 中每个 provider 的
  `sourceRepo` / `license.spdx` / `license.copyright`。

## 5. 全局禁止项

由 `import-boundary.json` 的 `globalForbidden` 定义并在校验中强制：

- **二进制**：`.dll .exe .so .dylib .pdb .lib .o .a .obj .bin .class .jar .node .wasm .msi .zip .7z .tar .gz`。
- **缓存/工作区垃圾**：`.git/ node_modules/ .cache/ __pycache__/ .pytest_cache/ dist/ go-build/ .DS_Store Thumbs.db`。
- **密钥**：私钥 PEM 块、`sk-*`、`sk-ant-*`、`AKIA*`、`AIza*`、`xox[baprs]-*`，以及
  `key|secret|token|password = "..."` 形式的赋值。命中即失败，输出**脱敏**（不含原文）。
  确属占位/测试样例的，须在 provider 的 `contentAllowlist` 中按 `{glob, patternId, reason}` 显式登记。

## 6. build / CI 接口（契约，SPEC5-D 落地）

本 SPEC 只固定接口，**不落地**——`package.json` 与 `.github/workflows` 均不在本 SPEC 修改。

- Go：`1.26.7`。
- 产物：

  | id | provider | 产物 | 构建 |
  |---|---|---|---|
  | `opencode-go-plugin` | opencode-go | `providers/opencode-go/bin/opencode-go-cliproxyapi-windows-amd64.dll` | `CGO_ENABLED=1 GOOS=windows GOARCH=amd64 go build -buildmode=c-shared -o bin/...dll .` |
  | `qwen-plugin` | qwen | `providers/qwen/bin/qwen-cliproxyapi-windows-amd64.dll` | `CGO_ENABLED=1 GOOS=windows GOARCH=amd64 go build -buildmode=c-shared -o bin/...dll .` |
  | `qwen-cli` | qwen | `providers/qwen/bin/bailian-quota.exe` | `go build -o bin/bailian-quota.exe ./cmd/bailian-quota` |

- module 边界：各 provider 保持独立 `go.mod`（`module opencode-go-cliproxyapi` /
  `module qwen-cliproxyapi`），不并入 `apps/manager-server`。
- CI hook（SPEC5-D）：在 `.github/workflows/pr-check.yml` 增加 `providers` job，至少运行
  `bin/release/check-provider-import-boundary.mjs`；发布工作流按需增加三产物构建。

## 7. 校验方式

- 脚本：`node bin/release/check-provider-import-boundary.mjs`（`--json` 输出结构化）。
  读取 `providers/import-boundary.json`，校验：契约文件存在、清单结构合法、`NOTICE` 归属完整、
  provider 目录内无被排除路径/无二进制/无密钥、`requiredFiles` 齐备、git 索引内无 provider 二进制。
- 测试：`tests/providerImportBoundary.test.mjs`，由既有 `npm run test:repo` 收集
  （本 SPEC 不改 `package.json`）。测试用合成 fixture 演练“未来导入”会被正确拦截。
- 未导入的 provider（`status=planned` 且目录缺失）不判失败，仅记录 info；`status=imported`
  且目录缺失或必需文件缺失则失败。

## 8. 同步 / 变更流程

1. 建独立更新分支（不在主干事分支上直接 subtree/merge）。
2. `git subtree pull ...`（OpenCode）或重新一次性导入（Qwen，若来源更新）。
3. 移除被排除路径；逐项核对本地定制是否保留。
4. 冲突不得强行覆盖，必须报告并测试。
5. `node bin/release/check-provider-import-boundary.mjs` 通过。
6. 跑相关全量测试与构建（SPEC5-D 落地后含 provider 构建）。
7. 合并主干（由 Hermes 验收后进行）。

## 9. 非目标

- 不导入插件源码、不演练 subtree、不构建 DLL/CLI（SPEC5-B/C/D）。
- 不修改 `package.json` / `.github/workflows`。
- 不改 README / FORK-MAINTENANCE 基线（SPEC5-E）。
- 不改动旧独立插件仓库（不删除、不归档、不改重定向）。
