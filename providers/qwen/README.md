# CPA-Qwen

CLIProxyAPI v8 的 Qwen 凭证插件。仓库是 [williamxhero/CPA-Qwen](https://github.com/williamxhero/CPA-Qwen)。运行标识是 `cpa-credential-qwen`，提供商是 `qwen`。

## 功能

- 每个密钥对应一个稳定的认证文件。推理使用宿主选中的那把密钥。
- 把 OpenAI Chat Completions 和 Anthropic Messages 转成上游 Chat Completions。
- Coding Plan 额度走 `bailian-quota`。三个请求窗口仍按严格协议校验。
- 缓存里如果只有一个真实的月度窗口，插件读出这一个窗口，不补 5 小时或周窗口。
- 同模块另有只读的 `aliyun-bss-quota`，查的是账户金额，不是 Coding Plan 请求额度。

## 构建

```powershell
python -B scripts/build-artifacts.py --core-source 'D:\path\to\CPA-Core-ex' --output 'D:\path\to\new-candidate'
```

需要 Go 1.26.10 或更高，以及带 `CGO_ENABLED=1` 的 GCC。产物是 `cpa-credential-qwen.dll`、`aliyun-bss-quota.dll` 和 `bailian-quota.exe`。
