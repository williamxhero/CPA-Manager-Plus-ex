## 本 fork 的功能

相对上游 `seakee/CPA-Manager-Plus`，这条主干增加：

- 同一仓库托管 OpenCode Go 与 Qwen 插件源码，运行标识是 `cpa-credential-opencode` 和 `cpa-credential-qwen`。
- 提供商密钥别名、计划凭证页，以及 OpenCode、Qwen 的凭证与额度。
- AI 提供商余额按各自密钥保存，两位小数；间隔未到时直接用缓存。
- Codex Credits 列，自动启停会把剩余 Credits 算进去。
- Keeper 正在订阅用量时，请求监控仍能通过 HTTP 收到新记录。
- Qwen 额度可以显示缓存里真实的月度窗口。
