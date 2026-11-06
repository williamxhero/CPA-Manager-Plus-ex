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
> cpa-credential-opencode-ex` 领先上游 17 个提交）的取舍由 SPEC5-B 决定并记录；
> 冲突不得强行覆盖，必须报告并测试。

## Qwen 导入历史（SPEC5-C）

Qwen 为**一次性目录导入**（`williamxhero/qwen-cliproxyapi`，MIT），无 subtree 上游；
导入后同样受上述硬性规则约束。该记录及根 `NOTICE` 是历史归属，不是当前维护或构建源。

## Qwen 受控消费快照（Q8）

唯一维护源为 `williamxhero/CPA-Qwen`。`providers/qwen` 只消费规范源的完整快照，
不得在其中独立修复、生成构建产物或修改 receipt 来隐藏差异。当前规范 pin 为
`76413510fe5add216968d6b5967cc5c3f7aa9113`，记录在 `import-boundary.json` 的
`consumerSnapshot`；升级时先审查规范源新提交，再由独立同步流程更新快照和 receipt。
构建脚本**不负责同步或 enroll**，也不会调用 `--write-receipt`。

生产 build/test/vet 统一要求两个显式独立源码路径：

```powershell
$env:CPA_QWEN_CANONICAL_SOURCE = 'D:/reviewed/CPA-Qwen'
$env:CPA_COMPATIBLE_CORE_SOURCE = 'D:/reviewed/CPA-Core-ex'
npm run test:providers
npm run lint:providers
npm run build:providers
```

或通过 CLI 参数提供（runner 参数须在 `--` 之前）：

```text
node bin/release/run-provider-go.mjs test --qwen-canonical-source D:/reviewed/CPA-Qwen --compatible-core-source D:/reviewed/CPA-Core-ex -- -run TestFoo
node bin/release/build-providers.mjs --qwen-canonical-source D:/reviewed/CPA-Qwen --compatible-core-source D:/reviewed/CPA-Core-ex --json
```

需要 Python（默认 `python`，可用 `CPA_QWEN_PYTHON` 指定）和本地 Git。入口从已固定的
canonical Git object 读取规范 `scripts/verify-manager-snapshot.py`，只读校验真实
`CPA_QWEN_SOURCE.json`：规范来源、commit、tree、完整每文件 SHA-256，缺/多/变文件、
链接/junction/reparse、dirty canonical 等均拒绝。校验失败时，在任何 provider Go 调用、
C 编译器探测或 artifact 输出之前退出；`--skip-dll` 不能跳过来源门禁。

Core 必须包含 SPEC92 观察字段的兼容 SDK；published `v8.0.0` 不足以构建新 Qwen。
所有 provider 的 Go 调用均使用快照外的临时 `.mod/.sum`，显式
`-replace=github.com/router-for-me/CLIProxyAPI/v8=<selected Core>` 与 `-modfile/-mod=mod`；
不修改消费 `go.mod/go.sum`。缺少 Core 路径即失败，不回退到 published SDK。
运行时关闭 GOWORK/GOENV，清理 GOFLAGS/GOROOT，限制 GOTOOLCHAIN=local；额外 Go 参数
不能覆盖 modfile/mod/overlay/工作目录。编译器仍负责检验所选 SDK 的实际 API 兼容性。

Qwen 三产物写到仓库根的 `bin/tmp/provider-build/qwen/`（**快照之外**）：

- `cpa-credential-qwen-windows-amd64.dll`：原 Qwen 插件；
- `aliyun-bss-quota.dll`：同仓 quota-only 兼容入口，不是第二份源码或 CLI；
- `bailian-quota.exe`：唯一 CLI。

manifest 的 `args/build` 是构建模板，不是绕过门禁、直接在 module 内构建的操作指南。
所有本地重试应使用上述统一入口。无 Windows C 工具链时保留原有 DLL skip 行为，
CLI 仍构建；skip **不等于三产物验收成功**。重复 build/test 不应向快照留下额外文件，
校验不忽略整个 `bin/` 或任何源码子树。构建不启用插件、不部署兼容 DLL、不依赖旧 Aliyun 仓库。

物理写入边界同样是前置门禁：runner/build 在任何 Go 调用或 C 编译器探测之前，
逐级 `lstat` 检查 `bin/tmp`、全部 artifact 目标（含 c-shared 生成的 `.h`）及其所有
现有祖先，拒绝 symlink/junction、悬空链接和多硬链接的目标文件；按最近已存在祖先的
`realpath` 投影未创建路径，拒绝实际或词法落入消费 Qwen/canonical Qwen/Core source。
manifest output 与实际 `-o` 路径必须一致。临时 modfile 写入和后续 Go 调用前再次检查，
不通过目录 ignore 隐藏污染；已有普通产物可以正常重复构建。runner 不接受额外 `-o`
覆盖受控输出。此检查不是对恶意并发文件系统替换的 OS 沙箱保证，也不沙箱化 Go 测试代码。

Windows 完整栈入口使用独立 CPA-Qwen/Core 显式 pin，并保留兼容 DLL 为 candidate-only；
详见 `ops/windows/BUILD-LOCAL-STACK.md`。
