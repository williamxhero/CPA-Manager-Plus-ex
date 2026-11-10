# CPA-Qwen

CLIProxyAPI v8 的 Qwen 凭证插件。仓库是 [williamxhero/CPA-Qwen](https://github.com/williamxhero/CPA-Qwen)。运行标识是 `cpa-credential-qwen`，提供商是 `qwen`。

## 组成

| 产物 | 用途 |
| --- | --- |
| `cpa-credential-qwen` | 推理凭证插件与额度查询 |
| `aliyun-bss-quota` | 只读账户金额查询插件 |
| `bailian-quota` | Coding Plan 用量 CLI |

## 功能

- 每个密钥对应一个稳定的认证文件。推理使用宿主选中的那把密钥。
- 把 OpenAI Chat Completions 和 Anthropic Messages 转成上游 Chat Completions。
- Coding Plan 额度走 `bailian-quota`。三个请求窗口仍按严格协议校验；源库依赖浏览器会话时，后台刷新会受浏览器常开和登录状态影响，本 fork 改用官方 `bl` CLI、缓存和严格窗口语义。对应 [issue #1](https://github.com/williamxhero/CPA-Qwen/issues/1)。
- 缓存里如果只有一个真实的月度窗口，插件读出这一个窗口，不补 5 小时或周窗口，沿用上方已归档的严格窗口语义，避免把不存在的窗口伪造成零额度。
- 同模块另有只读的 `aliyun-bss-quota`，查的是账户金额，不是 Coding Plan 请求额度；旧的额度仓库能力已合并到本仓库，避免两份实现分叉。对应 [issue #8](https://github.com/williamxhero/CPA-Qwen/issues/8)。
- **完整上游模型目录**：源库在发现失败时用三个硬编码模型和 `qwen/` 前缀冒充完整目录，容易漏模型并误导路由。本 fork 发布上游返回的完整原始模型 ID，发现失败时保持空目录并等待刷新，不伪造候选。对应 [issue #3](https://github.com/williamxhero/CPA-Qwen/issues/3)。
- **百炼登录失效可恢复**：源库把官方 CLI 的登录过期误报为 `invalid_response`，用户看不到真正原因，也不会被引导重新登录。本 fork 识别官方错误码，提示并打开控制台登录页，登录恢复后继续刷新额度。对应 [issue #4](https://github.com/williamxhero/CPA-Qwen/issues/4) 和 [issue #5](https://github.com/williamxhero/CPA-Qwen/issues/5)。
- **凭证保存回滚一致性**：源库生成 Qwen 凭证时遗漏 `base_url`，面板保存后的运行时校验把正常保存误判为失败并回滚。本 fork 持久化完整字段并让保存前后校验与运行时文件一致。对应 [issue #6](https://github.com/williamxhero/CPA-Qwen/issues/6)。

## 验证

本插件依赖 CPA-Core-ex 的扩展 SDK；检查时显式选择宿主源码。脚本使用临时模块文件，不修改两个仓库的 `go.mod` 或 `go.sum`。

```powershell
python -B scripts/check-go.py --core-source '..\CPA-Core-ex'
python -B scripts/check-go.py --core-source '..\CPA-Core-ex' --mode vet
```

仓库中的 `internal/bailianquota` 包含 CLI 生命周期、取消和清理测试，不需要真实账户或额度请求。

## 构建

```powershell
python -B scripts/build-artifacts.py --core-source 'D:\path\to\CPA-Core-ex' --output 'D:\path\to\new-candidate'
```

需要 Go 1.26.10 或更高，以及带 `CGO_ENABLED=1` 的 GCC。产物是 `cpa-credential-qwen.dll`、`aliyun-bss-quota.dll` 和 `bailian-quota.exe`。

> 完成 issue 归档更新时间：2026-11-09 08:34:40 SGT（UTC+08:00）。已列入上述条目的 issue 后续不再重复归档。
