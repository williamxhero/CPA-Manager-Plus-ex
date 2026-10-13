# Fork 维护清单（CPAMP-EX）

本文件记录 `williamxhero/CPA-Manager-Plus-ex`（下称 CPAMP-EX）相对上游
`seakee/CPA-Manager-Plus` 的**自有定制**，供上游合并时逐项核对。只填写已核实
的信息；未落地项明确标注「待开发」，不当作已完成功能。

> **基准（2026-10-10 重建：只读盘点 + `git fetch origin fork`）**
> 取 refs `origin/main`、`fork/main`、`origin/dev`、`fork/dev`（盘点时 `fork/dev` = `223d3b24`，
> 已含 SPEC5-A/B/C/D）：
>
> - `merge-base(origin/main, fork/main) == 166262365d506157b99968105c890f480cc7c07c`
>   → fork 与上游**已分叉**，不再是无分叉纯 ahead。
> - 上游 `origin/main`（`dbf61ddf`）领先 `fork/main` **20** 个提交；`fork/main`（`203b0591`）
>   领先上游 **46** 个提交。
> - dev 线：上游 `origin/dev`（`2d83e14e`，含 v1.14.5 发布）领先 `fork/dev`（`223d3b24`）
>   **19** 个提交；`fork/dev` 领先 `origin/dev` **48** 个提交。
>
> 早期基线（`branding-plan.md`，2026-10-09，HEAD `41adc1e1`）记的 fork「纯 ahead、无分叉」已失效，
> 本文件以重建后的数字为准。下面各项都确属 fork 新增，而非上游功能。

## 分类说明

- **品牌偏好**：外观/文案/图标等偏好差异，不含功能行为。
- **新增功能**：上游没有的能力。
- **兼容修复**：为兼容旧配置/旧链接或修补集成问题而做的改动。

## 清单

| # | 功能 / 用户目的 | 分类 | 涉及代码与资产（fork 内路径） | 上游来源 | 保留规则 | 重叠替换条件 | 配置/数据迁移注意 | 验收（测试 / 真实页面） | 状态 · 关联 |
|---|---|---|---|---|---|---|---|---|---|
| 1 | provider key 别名（凭证可用别名引用） | 新增功能 | manager-server `internal/{http/controller,repository,service}/providerkeyalias`、`internal/model/provider_key_alias.go`、`http/router/router.go`、`sqlite/migrate.go`；web `components/providers/ProviderKeyAliasEditor.tsx`、`ProviderEditDrawer/*`、`ProviderTable/rowData.ts`；`bin/release/reapply-provider-alias.sh`；`docs/provider-alias-upgrade.md` | 无（fork 新增） | 保留，直到上游提供等价能力并通过本 fork 验收 | 若上游实现别名且迁移兼容，移除重复实现、保留回归测试 | 升级后需 `reapply-provider-alias.sh` 回填别名 | 真实页面：Provider 编辑抽屉保存/回显别名 | 已实现（fork 主线，base `41adc1e1`） |
| 2 | OpenCode Go 额度查询 + 凭证行折叠 | 新增功能 | web `utils/quota/opencodeGoQuota.ts`、`features/authFiles/model/openCodeGoAuthFiles.ts`、`components/quota/quotaConfigs.ts` | 无 | 保留 | 上游覆盖且通过验收时移除重复实现 | 无 | `openCodeGoAuthFiles.test.ts` 等 | 已实现（fork 主线） |
| 3 | 额度单位分数显示 | 品牌偏好 | web `features/accounts/model/quotaUnitFraction.ts` | 无 | 保留 | 上游提供可配置单位时替换 | 无 | 单测 | 已实现（fork 主线） |
| 4 | OpenCode Go 计划剩余天数 + “Go” 标签 | 品牌偏好 | web `features/accounts/model/accountSubscriptionPresentation.ts` | 无 | 保留 | 上游覆盖时替换 | 无 | 单测 + 凭证卡页面 | 已实现（fork 主线） |
| 5 | OpenAI key 连通性测试（不消耗模型） | 新增功能 | web `components/providers/openAIKeyTest.ts` | 无 | 保留 | 上游提供等价探测时替换 | 无 | 单测 | 已实现（fork 主线） |
| 6 | 手写插件凭证表单（非浏览器 OAuth） | 新增功能 | web `features/oauth/PluginCredentialForm.tsx`、`pluginCredentialMetadata.ts` | 无 | 保留 | 上游支持手动凭证时替换 | 需要 host 下发 metadata 或回退 | `OAuthPage.credentials.test.tsx` | 已实现（fork 主线） |
| 7 | 统一「（计划）凭证」页 `/plan-credentials` | 品牌偏好 + 新增功能 | web `features/planCredentials/PlanCredentialsPage.tsx`、`features/plugins/planPlugins.ts`、`pages/PlanCredentialsPage.tsx`、`router/MainRoutes.tsx` | 无 | 保留 | 上游提供等价入口时替换 | route 保持 `/plan-credentials` 以免破坏旧链接 | `PlanCredentialsPage.test.tsx`、`planCredentialsWiring.test.ts` + 侧栏入口 | 已实现（fork 主线） |
| 8 | Qwen 额度 + 凭证卡渲染 | 新增功能 | web `utils/quota/qwenQuota.ts`、`features/accounts/model/accountQuota*`、`accountSubscriptionPresentation.ts` | 无 | 保留 | 上游覆盖时替换 | 无 | 单测 + 凭证卡页面 | 已实现（fork 主线） |
| 9 | 受保护的计划凭证编辑 | 新增功能 | web `services/api/authFiles.ts`（plan configuration）、`features/authFiles/hooks/useAuthFileConfigurationEditor.ts`、`accounts/components/accountDetail/AccountConfigurationTab.tsx`；`docs/plan-credential-editing.zh-CN.md` | 无 | 保留 | 上游覆盖时替换 | 无 | 单测 | 已实现（fork 主线） |
| 10 | provider source 解析改造 | 兼容修复 | web `utils/sourceResolver.ts`、`monitoring/model/sourceDisplay.ts`、`realtimeSourceDisplay.ts` 等 | 无 | 保留 | 上游覆盖时替换 | 无 | 单测 | 已实现（fork 主线） |
| 11 | CPAMP EX 品牌角标 + 主题 provider 图标 + 添加页改名 | 品牌偏好 | 品牌资产 `apps/web/src/assets/brand/*`（15 SVG + 15 PNG 全部加 EX 角标）、`apps/web/public/favicon.ico`、`apple-touch-icon.png` 及 manager-server 内嵌副本、`apps/web/index.html` 内联回退；生成脚本 `bin/branding/generate-cpamp-ex-assets.py`（见「品牌资产生成」）；provider 图标 `apps/web/src/assets/icons/opencode-{light,dark}.png` + `features/authFiles/constants.ts`；文案 `i18n/locales/*`、`features/plugins/planPlugins.ts`、`features/planCredentials/PlanCredentialsPage.tsx` | 无（fork 品牌偏好） | 保留；EX 仅加于 CPAMP 自有资产，**不加**第三方 provider 标志 | 上游若提供官方品牌变体，按需替换但保留 EX 语义 | 无 | `tests/cpampExBranding.test.mjs`、`repoSourceIntegrity.test.mjs`、`constants.test.ts`、`PlanCredentialsPage.test.tsx`、`planCredentialsWiring.test.ts` | 已并入 `fork/main` 与 `fork/dev`（PR #20，`26017f88`） |
| 12 | 插件源码单仓整合：OpenCode Go + Qwen 插件与 Qwen 额度 CLI 收入同仓 | 新增功能 | `providers/opencode-go/**`（git subtree）、`providers/qwen/**`（一次性导入）、`providers/README.md`、`providers/import-boundary.json`、`docs/providers-layout-contract.md`、根 `NOTICE`、`bin/release/{check-provider-import-boundary,run-provider-go,build-providers}.mjs`、`bin/ci/classify-pr-checks.mjs`、`.github/workflows/pr-check.yml`（`providers` job）、`package.json`（`check/lint/test/build:providers`） | 无（fork 新增整合；插件各自第三方上游见「provider 源码整合」） | 保留；每个 provider 保持独立 Go module，不并入 `apps/manager-server` | 上游若自行纳入等价插件，逐 provider 比对后再替换重复实现 | 无（面板运行时仍由 CPA 加载插件，配置不变） | `tests/{providerImportBoundary,providerBuildScripts,prCheckClassifier}.test.mjs`；`node bin/release/check-provider-import-boundary.mjs`；场景：新 clone 可构建面板 + 两个 DLL + Qwen CLI | 契约 SPEC5-A 已并入 `fork/main`/`fork/dev`；OpenCode SPEC5-B 已并入 `fork/dev`（`f73c059b`）；Qwen SPEC5-C（`19fa6c50`）与构建/CI SPEC5-D（`223d3b24`）均已并入 `fork/dev` |

## provider 源码整合（SPEC5 A–D）

单仓把两个 CLIProxyAPI 插件源码与 Qwen 额度 CLI 收进来。运行时仍由 CPA 加载插件，
面板只是消费者；仓库托管整合不等于面板替代 CPA 执行器。

- 布局：`providers/opencode-go/`、`providers/qwen/`，每个 provider **保持独立 Go module**
  （`module opencode-go-cliproxyapi` / `module qwen-cliproxyapi`），不并入 `apps/manager-server`。
- 契约文件：机器可读 `providers/import-boundary.json`；人读 `docs/providers-layout-contract.md`；
  边界说明 `providers/README.md`；归属声明根 `NOTICE`。
- 边界校验：`node bin/release/check-provider-import-boundary.mjs`（`--json` 输出结构化结果）。

### OpenCode Go —— git subtree（SPEC5-B）

命名上游 remote：`opencode-upstream` = `massiveits/opencode-go-cliproxyapi`（branch `main`）。
在**独立更新分支**上执行（不在主干事分支直接操作）：

```bash
git remote add opencode-upstream https://github.com/massiveits/opencode-go-cliproxyapi.git
git fetch opencode-upstream
git subtree add  --prefix=providers/opencode-go opencode-upstream main --squash   # 首次导入
git subtree pull --prefix=providers/opencode-go opencode-upstream main --squash   # 后续同步
node bin/release/check-provider-import-boundary.mjs   # 必须通过；失败先移除被排除路径
```

**导入源决策（SPEC5-B 定案，记录于 `import-boundary.json` → `sourceDecision`）**：
采用「**上游作基 + 重新套用 17 个定制提交**」，即以上游 `massiveits/opencode-go-cliproxyapi@a4369b9b`
作 subtree 基（`--squash`），随后以本地提交重新套用本地 fork
`williamxhero/opencode-go-cliproxyapi-ex@f88846cc`（相对上游领先 17、落后 0）的定制。
**不采用“直接 `subtree add` fork 树”**：隔离仓库实测，直接 add fork 后执行
`git subtree pull opencode-upstream main` 会按反向 diff **删除全部定制**。因此上游仍是**唯一 sync 源**，
定制以本地叠加提交保留；未来同步冲突必须手动解决并测试，**不得强行覆盖**。

需保留的定制（凭证/额度/目录适配，逐条见 `import-boundary.json` → `preserveCustomizations`）：
凭证管理路由与脱敏（`504d2e85`、`d97568fc`、`f88846cc`、`86f6a27f`）、v7/v8 原生额度 provider
（`d41f0a24`、`0900bbec`、`8add1563`、`97548372`、`ae954849`、`74f9d0ab`）、目录适配
（`a7a6b85a`、`f5806103`）。构建产物提交 `495e197c`（c-shared 产物）**不得导入**。

排除（`git subtree` 无法按路径过滤，导入后须移除，且**每次同步后重跑校验**）：
`plugins/**`（含 `plugins/windows/amd64/*.dll` ≈11 MB 与生成的 `*.h`）、`*.dll/*.exe` 等二进制、
`*.log`、`worker*`（`worker8-err.log`、`worker8-result.json`）、`prompt8-credential-route.md`。

许可证：MIT，`Copyright (c) 2026 massiveits`；保留 `providers/opencode-go/LICENSE`。

状态：**已导入并并入 `fork/dev`**（`f73c059b`，PR #25）。

### Qwen —— 一次性目录导入（SPEC5-C）

Qwen 为**一次性目录导入**，不虚构 upstream 或 subtree remote，无同步机制；出现真实上游同步需求时
**另立 SPEC**。

- 来源：`williamxhero/qwen-cliproxyapi`，默认分支 `claude/qwen-provider`。
- 权威快照 revision：`main@595320999cb5b17a7e7754e70ce740f9b578c212`（`main` 领先默认分支 4 个提交），
  导入内容与该快照**逐字节一致**（61 文件）。
- 内嵌额度 CLI：`cmd/bailian-quota`（+ `internal/bailianquota/*`），构建命令
  `go build -o bin/bailian-quota.exe ./cmd/bailian-quota`。
- 许可证：MIT，`Copyright (c) 2026 williamxhero`；保留 `providers/qwen/LICENSE` 与
  `providers/qwen/NOTICE.md`（`NOTICE.md` **必须原样保留**）。组件改编自
  `massiveits/opencode-go-cliproxyapi`（MIT），归属见根 `NOTICE`。

状态：**已导入并并入 `fork/dev`**（`19fa6c50`，PR #26）；`import-boundary.json` 中 `qwen.status`
已为 `imported`。

### 构建与 CI 入口（SPEC5-D）

- `package.json` 脚本：`check:providers` / `lint:providers` / `test:providers` / `build:providers`。
- 统一 Go 入口 `bin/release/run-provider-go.mjs`：逐 module 跑 `go test|vet|build ./...`
  （仓库根 `go test ./providers/...` 不是合法 Go pattern）。
- 产物构建 `bin/release/build-providers.mjs`：**始终**构建 Qwen CLI；**仅当存在 Windows C 工具链时**
  构建 c-shared DLL，否则**跳过并打印本地构建命令**（CI 不强制编译 DLL；无 mingw 时跳过）。
- 产物（写入各自 `providers/*/bin/`，被 `.gitignore` 忽略，**不入库**）：

  | id | 产物 |
  |---|---|
  | `opencode-go-plugin` | `providers/opencode-go/bin/opencode-go-cliproxyapi-windows-amd64.dll` |
  | `qwen-plugin` | `providers/qwen/bin/qwen-cliproxyapi-windows-amd64.dll` |
  | `qwen-cli` | `providers/qwen/bin/bailian-quota.exe` |

- CI：`.github/workflows/pr-check.yml` 新增 `providers` job，矩阵 `os ∈ {ubuntu-latest, windows-latest} × go ∈ {1.26.x, 1.27.x}`，
  顺序执行 (1) 边界守卫 (2) `go vet` (3) `go test` (4) artifact 构建，并接入 `required` 聚合；
  `bin/ci/classify-pr-checks.mjs` 新增 `providers` 分类（触发：`providers/**`、provider release 脚本、
  `NOTICE`、`package.json`、`go.work`）。
- 守卫（由 `check-provider-import-boundary.mjs` 强制，规则见 `import-boundary.json` → `globalForbidden`）：
  1. **禁止二进制**：`.dll .exe .so .dylib .pdb .lib .o .a .obj .bin .class .jar .node .wasm .msi .zip .7z .tar .gz`。
  2. **禁止密钥**：私钥 PEM 块、`sk-*`、`sk-ant-*`、`AKIA*`、`AIza*`、`xox[baprs]-*`，以及通用
     `key/secret/token/password = "..."` 赋值；命中即失败且输出**脱敏**，占位/测试样例须在 provider
     `contentAllowlist` 显式登记。
  3. **禁止缓存 / 工作区垃圾**：`node_modules/`、`.cache/`、`__pycache__/`、`.pytest_cache/`、`dist/`、`go-build/` 等。
  4. **单文件上限 1 MiB**：`globalForbidden.maxFileBytes`。
  5. **必需文件与 `NOTICE` 归属比对**：每个 provider 须齐 `requiredFiles`，根 `NOTICE` 必须覆盖其
     `sourceRepo` / `license.spdx` / `license.copyright`。

状态：**已并入 `fork/dev`**（`223d3b24`，PR #28）；`fork/dev` 已含 `.github/workflows/pr-check.yml` 的
`providers` job、`bin/ci/classify-pr-checks.mjs` 的 `providers` 分类，以及 `package.json` 的
`check:providers` / `lint:providers` / `test:providers` / `build:providers` 脚本。

## 上游合并检查表

1. 建独立更新分支（不要直接在主干事分支上合并）。
2. 合并上游来源（按改动来源选择）：CPAMP 上游经 `origin` 合并到本 fork 主干；OpenCode 经
   `opencode-upstream` 用 `git subtree pull` 合并到专属子目录（命令见「provider 源码整合」）。
   两者都允许手动解决冲突，解决后跑测试与构建，**不自动覆盖定制**。
3. 逐项核对上表定制点，确认上游是否已覆盖。
4. 处理重叠/迁移（配置、数据、路由、资产）。
5. 跑测试与构建：`npm run test`、`npm run type-check`、`npm run lint`、`npm run build`；Go 侧
   Manager Server `go test ./...`，provider 侧 `npm run test:providers`。
6. 真实页面验收（侧栏入口、`/plan-credentials` 两模块标题+图标、系统信息链接、favicon）。
7. 若本轮含插件：按「provider 源码整合」的入口同步 `providers/opencode-go` 或重新一次性导入
   `providers/qwen`，移除被排除路径，`node bin/release/check-provider-import-boundary.mjs` 通过，
   再 `npm run build:providers`（有 Windows C 工具链时产出 DLL，否则跳过并打印本地命令）。
8. 合并主干（由 Hermes 验收后进行）。

> 上游已覆盖、且通过本 fork 验收的能力：移除重复实现，保留有价值的回归测试。
> **不得**无条件 `ours`/`theirs` 合并。

## 品牌资产生成（EX 角标）

品牌资产的 EX 角标由脚本统一生成，**committed 资产是脚本输出，不手工编辑**。

- 生成脚本：`bin/branding/generate-cpamp-ex-assets.py`
- 依赖：Python 3.8+ 与 Pillow（`pip install Pillow`）；读取 git 源时另需 `git`。
- 源：`--source-ref`（默认 `DEFAULT_SOURCE_REF`，即角标落地前的最后一个提交）里的
  **未加角标** 资产。每次运行都从干净源重建，因此**不会累加/叠印**角标。
- 产出（35 项）：15 品牌 SVG + 15 品牌 PNG + `apps/web/public/favicon.ico` +
  `apple-touch-icon.png`（各自与 manager-server 内嵌副本逐字节一致），并重算
  `apps/web/index.html` 的三处内联 base64 回退。
- 幂等校验：`python bin/branding/generate-cpamp-ex-assets.py --check`
  （重建到内存后与工作树逐字节比较，漂移即非零退出）。
- 16px favicon 帧：抗锯齿的 Arial「EX」在 16px 会糊成一片，故 16px 帧改用定制
  像素字形——3×5 的 `E` + 1px 间隙 + 3×5 的 `X`（7×5 白色像素，蓝底，1px 留白），
  只落在右下角（9×7，占比 < 半幅，不遮主体）。32/48 帧保持原可读构图。
- 回归守卫：`tests/cpampExBranding.test.mjs` 直接解码 ICO 内 16px 帧的 PNG 像素，
  断言字形逐像素（白色 20 个：E 11 + X 9），而非匹配标记字符串。

## 面向用户链接切 fork（SPEC #6）

已核实（只读）：fork `williamxhero/CPA-Manager-Plus-ex` 是上游的 fork，**仓库页/tag 存在**，但
**无 GitHub Release（API `/releases` 为空）、无 release 资产、无 `update-channel` 分支、无 GHCR/Docker
Hub 镜像（`williamxhero/cpa-manager-plus` 不存在）、未启用 GitHub Pages**。据此分类处理：

**已切到 fork（受 `tests/forkEntryLinks.test.mjs` 守卫）**

- 仓库/项目入口：README(+CN) 徽章（release/license/stars）、LICENSE 文件链接、`SystemPage.tsx` 仓库
  快捷入口、`MainLayout.tsx` 页头/侧栏仓库入口、docs vitepress editLink 与 socialLinks、`apps/docs/**`
  的项目与 Release 浏览链接、`bin/install-cpamp.sh` 文档中安装脚本 raw 链接。
- 版本/发布浏览链接：`versionReleaseLinks.ts` 的 manager 目标改为 fork（`/releases/tag/<tag>` 对 fork
  已存在的 tag 返回 200；fork 自发布版本必然带同名 tag）。
- 演示 fixture：`demoFixtures.ts` 的示例 release 链接。

**保留上游引用（逐一理由，不得盲目字符串替换）**

| 引用 | 位置 | 保留理由 |
|---|---|---|
| `Copyright (c) 2026 Seakee` / `Copyright 2026 Seakee` | `LICENSE`、README、docs footer | 法律要求的原作者署名 |
| `git remote add upstream .../seakee/CPA-Manager-Plus.git` | `CONTRIBUTING.md` | 明确命名的上游同步 remote，用于未来合并 seakee 更新 |
| Go module `github.com/seakee/cpa-manager-plus/...` | `go.mod`、`Dockerfile.manager-server`、`package-native.sh` ldflags | 内部 import identity，改动会牵动全部 import，非用户可见，独立评估 |
| 历史 release notes/posts | `docs/release-notes/**`、`docs/release-posts/**` | 历史记录，保留原样 |
| 旧版 `seakee/cpa-manager` 镜像 | migration 文档 | 指向更早的另一个项目，非本 fork |
| 镜像 `seakee/cpa-manager-plus`（docker.io + ghcr.io） | README、docs、`UsageMaintenanceCapabilityViews.tsx`、`bin/install-cpamp.sh` | **fork 未发布自有镜像**；改指 fork 会使 `docker pull` 404，下载请求失效 |
| `update-channel/update-index.json`（raw） | `version.ts`、`updatecheck/metadata.go`、`docs/update-check.md` | **fork 无 `update-channel` 分支**；改指 fork 会 404、更新检查失效 |
| `panel-github-repository` 示例值 | docs（getting-started/cpa-panel/faq/update） | 已切 fork 作为推荐值；但需 fork 先发布 Releases 方能真正下载 `management.html`（见阻塞） |
| release 流水线仓库常量/镜像名/source label | `bin/release/*.mjs`、`.github/workflows/release*.yml` | 绑定发布身份与镜像仓库；fork 尚无发布器与命名空间，改指会 fail-closed |

> **provider 第三方归属（不属 seakee 上游，独立位置）**：`providers/opencode-go/LICENSE`
> （MIT，`Copyright (c) 2026 massiveits`）、`providers/qwen/LICENSE` 与 `providers/qwen/NOTICE.md`
> （MIT，`Copyright (c) 2026 williamxhero`），以及根 `NOTICE` 的 “Bundled provider components”
> 段落（预登记 `sourceRepo` / `license.spdx` / `license.copyright`）。这些必须随导入**原样保留**，
> 由 `node bin/release/check-provider-import-boundary.mjs` 比对，不得删除或改写版权行。

**阻塞（需人工/发布后处理，不声称完成）**

1. fork 尚未发布任何 GitHub Release/资产 → 一键安装脚本的 native 下载、`panel-github-repository` 的
   `management.html` 拉取、端口版本卡片的 release 页仍可能落空。需在 fork 运行发布流水线后复核。
2. fork 未启用 GitHub Pages，`demo-pages.yml` 未在 fork main 运行 → README/界面/文档站的在线演示与
   在线文档链接仍指向上游 `seakee.github.io/CPA-Manager-Plus/`（唯一可用站点）。
3. fork 未发布镜像（docker.io/ghcr.io）→ 安装脚本与 README 的镜像拉取仍走上游镜像。
4. fork 无 `update-channel` 分支 → 管理器自动更新索引仍指向上游。

## 待开发 / 后续

- Go module path 改名：属独立评估，不在本轮字符串替换范围。
- 桌面端/托盘/安装器图标：本仓无独立资产，如引入需单独补 EX。
- 旧独立插件仓库（`williamxhero/opencode-go-cliproxyapi-ex`、`williamxhero/qwen-cliproxyapi`，
  以及本地 `aliyun-bss-quota-cliproxyapi`）在验收前**不删除、不归档、不改重定向**；后续是否归档单独确认。
- provider 本地构建产物：`npm run build:providers` 会把产物写入 `providers/*/bin/`（已 gitignore）；
  若在构建**之后**再跑边界守卫，会命中 `.dll/.exe` 规则而失败——CI 的顺序是**先守卫、后构建**，不受影响。
  （SPEC5-D 已记录此 known limitation；如要消除，可让守卫忽略已声明的产物输出目录。）
