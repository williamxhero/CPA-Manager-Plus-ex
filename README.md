<img align="right" src="./ex-badge.svg" alt="EX fork" width="76" />

# CPAMP-EX 主要能力

本页只列相对上游 seakee/CPA-Manager-Plus 的主要功能。上游面板适合管理 CPA 配置，但插件、凭证、配额和长期运营数据分散在不同工具里；本 fork 把它们收进同一条可验证的管理链路。

- **插件单仓与统一发布**：上游面板与插件源码分开，升级时容易出现版本和构建边界不一致。本 fork 将 OpenCode Go、Qwen 及 bailian-quota CLI 收入 providers/，由同一仓库构建，并用导入边界与 CI 约束来源和发布结果。对应 [issue #5](https://github.com/williamxhero/CPA-Manager-Plus-ex/issues/5)、[#6](https://github.com/williamxhero/CPA-Manager-Plus-ex/issues/6) 和 [#134](https://github.com/williamxhero/CPA-Manager-Plus-ex/issues/134)。
- **凭证、计划与身份管理**：上游缺少面向插件计划凭证的统一编辑入口，别名、掩码和宿主身份也难以保持一致。本 fork 提供统一的 /plan-credentials、Provider key 别名、插件凭证表单和受保护的凭证策略，让保存、展示、刷新都绑定到选定的真实凭证；同时修复别名修改不生效、凭证表单布局和保存回滚误报。对应 [issue #8](https://github.com/williamxhero/CPA-Manager-Plus-ex/issues/8)、[#56](https://github.com/williamxhero/CPA-Manager-Plus-ex/issues/56) 和 [#88](https://github.com/williamxhero/CPA-Manager-Plus-ex/issues/88)。
- **配额、余额与 Credits 观测**：上游主要展示 CPA 基础状态，无法把不同 Provider 的余额、预付费账户、Codex Credits 和凭证窗口放在同一套证据链里。本 fork 增加官方余额/预付费查询、按密钥缓存的余额、Codex Credits 与 Qwen 月度窗口，并在刷新间隔内稳定复用已确认的值；刷新失败会保留旧成功值并给出可操作状态。对应 [issue #38](https://github.com/williamxhero/CPA-Manager-Plus-ex/issues/38)、[#57](https://github.com/williamxhero/CPA-Manager-Plus-ex/issues/57)、[#80](https://github.com/williamxhero/CPA-Manager-Plus-ex/issues/80)、[#87](https://github.com/williamxhero/CPA-Manager-Plus-ex/issues/87) 和 [#92](https://github.com/williamxhero/CPA-Manager-Plus-ex/issues/92)。
- **请求、成本与排行分析**：上游的请求信息难以支撑长期成本核算和跨维度比较。本 fork 将请求历史、双成本、时间窗口、Provider/凭证/Key/渠道维度和排行统一到持久化分析中，保留可追溯的价格快照。
- **余额工具目录与有效余额门槛**：上游不同 Provider 的余额来源、安装方式和共享钱包口径分散，容易把单 Key 额度或不完整余额当成可恢复证据。本 fork 以余额工具目录统一安装和调用，并按 API Key 额度与全站余额的最小值确定有效余额。对应 [issue #133](https://github.com/williamxhero/CPA-Manager-Plus-ex/issues/133) 和 [issue #141](https://github.com/williamxhero/CPA-Manager-Plus-ex/issues/141)。
- **人工关闭与自动暂停分离**：上游共用 disabled 状态，额度保护可能覆盖人工意图，重启或回退还可能错误放行。本 fork 保存独立自动暂停、真实 Core 门禁、协调状态和统一 UI 展示，只有新鲜可信余额超过阈值才恢复自身策略；额度耗尽和 provider reset 复查失败时 fail-closed。对应 [issue #13](https://github.com/williamxhero/CPA-Manager-Plus-ex/issues/13)、[#27](https://github.com/williamxhero/CPA-Manager-Plus-ex/issues/27)、[#66](https://github.com/williamxhero/CPA-Manager-Plus-ex/issues/66)、[#79](https://github.com/williamxhero/CPA-Manager-Plus-ex/issues/79)、[#82](https://github.com/williamxhero/CPA-Manager-Plus-ex/issues/82)、[#83](https://github.com/williamxhero/CPA-Manager-Plus-ex/issues/83) 和 [#84](https://github.com/williamxhero/CPA-Manager-Plus-ex/issues/84)。
- **安全门禁与受控恢复**：上游缺少对 Provider 资金状态、凭证迁移和配额恢复的统一门禁。本 fork 在管理端加入资源能力、身份围栏、资金证据和恢复状态，先验证来源与主体，再允许刷新、自动启停或恢复动作。完整本机验收见 [issue #95](https://github.com/williamxhero/CPA-Manager-Plus-ex/issues/95)。
- **无 API 来源的只读网页额度闭环**：上游管理面通常只能读取 API/CLI 提供的额度，遇到没有公开接口的来源就无法形成可审计的刷新结果。本 fork 增加只读网页额度查询、缓存、失败分类和本机闭环验收。对应 [issue #97](https://github.com/williamxhero/CPA-Manager-Plus-ex/issues/97)。
- **Keeper 实时接入与 EX 品牌**：上游无法在 Keeper 订阅期间同时兼容 HTTP 采集，采集器断线后也可能长期停在无效的 HTTP 空拉路径，且没有清晰区分本 fork 的发布物。本 fork 保留订阅与 HTTP 双通道用量接入、自动重连和 EX 资产标识。对应 [issue #7](https://github.com/williamxhero/CPA-Manager-Plus-ex/issues/7)、[#17](https://github.com/williamxhero/CPA-Manager-Plus-ex/issues/17) 和 [#86](https://github.com/williamxhero/CPA-Manager-Plus-ex/issues/86)。
- **可回退部署与跨平台交付**：上游升级面板、Core 和插件时缺少统一的自动回退、Linux 无感部署与配置迁移入口，失败容易留下半升级状态。本 fork 提供独立部署脚本、失败自动回退、本机/远端配置同步和完整链路验收。对应 [issue #52](https://github.com/williamxhero/CPA-Manager-Plus-ex/issues/52)、[#62](https://github.com/williamxhero/CPA-Manager-Plus-ex/issues/62) 和 [#65](https://github.com/williamxhero/CPA-Manager-Plus-ex/issues/65)。

本页不替代上游使用文档；部署、配置和完整 API 说明仍以仓库其他文档为准。

> 完成 issue 归档更新时间：2026-11-09 08:34:40 SGT（UTC+08:00）。已列入上述条目的 issue 后续不再重复归档。
