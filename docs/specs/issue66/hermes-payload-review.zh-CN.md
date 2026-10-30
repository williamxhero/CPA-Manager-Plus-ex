# #66 Hermes 安全 payload 审查清单

**LOCAL ONLY / AWAITING HERMES REVIEW。此清单、工作包交接和本地调查均不是待发布正文。** 本会话尚未收到对下列字节版本的审查批准；不得把“候选”“检查通过”解释为可以外发。

## 1. 本轮权限与唯一 writer

- 继续原 #66 规划；唯一 writer 为既有 `bdb38eb8-7a2b-40fe-881b-c5925885bd0a`，工作区 `D:/WILL/AGENT/CPA/cpamp-issue66-plan`，分支 `claude/issue66-plan`。用户说明意外 resume 副本 `2377cdb3` 已由 Hermes 停止；本会话未恢复它，也未启动新 worker／worktree。
- 本轮 Git／GitHub 仅只读核查。没有 stage／commit／push／PR／issue／label／关系写入，没有实施、live 访问、部署、重启、merge 或 close。新增文件仅本地；原调查文档不覆盖、不搬成公共稿。
- HEAD 仍 `88ef979bfda38410d1253ae2728f5d5b5d953e99`，历史含受限调查。**禁止 push、force-push、重写或以新提交删除文件来掩盖历史后推送；当前轮也不创建替代分支／worktree。** 公共稿获审不等于该历史可公开。
- 先交此清单及[四包交接](./hermes-workpackages.zh-CN.md)给 Hermes 审查，再决定实际外发；既有工具拒绝不因本地改稿自动解除。审查／授权后若工具仍拒绝，停止并记录，不换工具绕行。

## 2. 去重与公开来源

只读结果（2026-10-16）：所有 issue 列表返回 24 条，覆盖 OPEN／CLOSED，未触及 100 条上限；#66 相关只有父 #66、品牌 #68 及票 #70–#77。不存在 A／B／C 子 SPEC 或 T08／T09／T10／T12。精确 head 分支 PR 查询为空；远端无 `claude/issue66-plan`，`dev` 指向基线 `f3e078e6`。这些是本轮读取时的状态，实际获审后执行前必须再去重。

- 父 [#66](https://github.com/williamxhero/CPA-Manager-Plus-ex/issues/66)，OPEN，updatedAt `2026-10-16T03:13:27Z`；body UTF-8 SHA-256：`985faf23e29a81a7b8a5a8e572f364f5d16a48b30604c21557347d1562211045`。
- 品牌 [#68](https://github.com/williamxhero/CPA-Manager-Plus-ex/issues/68)，OPEN，updatedAt `2026-10-16T06:55:35Z`；body UTF-8 SHA-256：`202371a055f87d5c25e1353d8cff9eba414b1c7d5c25c849c7f0083e9ba1496f`。**复用，不新建 D，不更新其正文。**
- 父 body 与 comments 均已完整读取；评论仅作上下文，不当成新授权或要求重新创建 issue。公共候选仅派生 body 的需求与测试，不转载评论中的工作路径／会话信息。
- 8 张已发布票 #70–#77 均 OPEN、仅 `ready-for-agent`，逐份正文与既有本地票据一致。全部复用，不因补 SPEC 再建一遍，也不在本轮改父 #66／#68。

## 3. 待审正文白名单（仅这七份）

路径均相对当前 worktree。字节数及 SHA-256 针对文件原始 UTF-8 字节；这是精确审查候选，不是 Git 仓库／历史的外发许可。

| 项 | 拟目的地／动作（待批准） | 本地正文 | 字节数 | SHA-256 |
|---|---|---|---:|---|
| 66-A | fork 新建 `[SPEC 66-A] 独立自动暂停消费契约、协调与安全迁移` | [A](./public-review/66-a-consumer-contract.md) | 8226 | `3413b94bd89703f44b32792d9116fe162890b37aedc1531f4d7b8fe821c5b755` |
| 66-B | fork 新建 `[SPEC 66-B] 统一安全额度查询与全量小时刷新` | [B](./public-review/66-b-query-refresh.md) | 9848 | `ff117b43235ba64099bee1c0607bf892cf341430b0ff37bf88ab242bfb32c94b` |
| 66-C | fork 新建 `[SPEC 66-C] 正交自动策略、统一状态展示与完整链路验收` | [C](./public-review/66-c-policy-status.md) | 9232 | `7088b677085fcda9c56ce6e5f71930e6133f0de2822c69c6ddef746a699697e2` |
| T08 | fork 新建 `[TICKET #66 T08] provider 精确余额暂停与恢复闭环` | [T08](./tickets/08-provider-policy.md) | 2534 | `ee62950f00c5f7e1e7313773a1ead2218d2f25f63c42bbe106331347515f8e30` |
| T09 | fork 新建 `[TICKET #66 T09] 历史迁移、重启协调与安全回退`，须实号替换 | [T09](./tickets/09-migration-reconciliation.md) | 2333 | `894ac2fa652585eb6c69fdacbde26d379c433e330a6a810b451be8f9d02252f0` |
| T10 | fork 新建 `[TICKET #66 T10] 所有渲染点的一致状态展示`，须实号替换 | [T10](./tickets/10-all-status-surfaces.md) | 2284 | `32bcb7edaba1dcdf6a8fec7bbf07e783d9a5245d3daeff710629a147c872c0ea` |
| T12 | fork 新建 `[TICKET #66 T12] 本地完整链路与独立验收交接`，须实号替换 | [T12](./tickets/12-independent-acceptance.md) | 3262 | `9a847c671787ba8b2354b3fc6053858511d8913129f7bea89cd21c83140f4a13` |

唯一拟目的 repo 为 `williamxhero/CPA-Manager-Plus-ex`；拟标签仅 `ready-for-agent`。没有其他仓库、工单评论、PR 或站点目的地。没有待发送附件、源码、日志、原始运行响应、credentials 或整个目录。此表尚未授权执行这些动作。

### 公共稿的来源与排除边界

| 稿 | 仅引用的父 #66 需求 | 新提案的性质 | 明确不携带 |
|---|---|---|---|
| A | Implementation Decisions 5–7／11；Testing 1–3／11–13／16–18 | 版本前置条件、幂等操作、状态确认、归属隔离及启动恢复是消费端需要的外部语义，标为提案；URL／字段拼写／实际能力由目标 owner 核实 | 独立 core 的 revision、源码路径／行号、内部选择入口、API 家族版本、已有／缺失实现的调查断言、运行模式内部结论 |
| B | Implementation Decisions 2–5／8–9／11；Testing 6–11／16–18 | Go 深模块、标准观测、精确金额、来源去重、任务／状态及安全边界，预算照父要求 | 本地实现路径、特定客户端安全缺陷调查、特定 provider 现有前端实现差异、历史配置或真实库存／账户／秘密 |
| C | Implementation Decisions 5–6／9–11；Testing 1–6／8／11–18 | 独立策略状态、余额边界、门禁确认与 UI 消费／最终验收语义 | 源码渲染定位、现有私有字段布局、目标 core 缺能力断言、真实运行截图／响应／账户信息 |

A/B/C 使用“待核验能力”“消费约束提案”，不把过去调查结论换成事实口吻重新发布。阈值、完整钱包范围、真实路由、当前清单逐项、迁移回退与 Hermes 独立验收均保留，未以脱敏名义削弱需求。

### 待 Hermes 审批的受限替换规则

T09／T10／T12 当前正文含未发布 T 引用，**不能原样发成最终票**。建议 Hermes 同时审查并明确批准：只允许把 T08／T09／T10 的“尚未发布”引用替换为实际创建并回读过的本 repo issue 号／URL，保留标题、验收与其他语义不变；A/C 的相应“待登记”描述可按同一映射更新为实际票号。记录替换前后 hash 和真实映射，发前检查无未解决 T 占位。未批准此规则，则每次替换后的正文重新交审，不自行扩大批准。

候选语义、来源、权限、门槛或目的地一旦改变，需重新审查；禁止由“批准一个文件”推断允许发布其目录或旧历史。

## 4. 待审批的原生依赖写清单

只读回查确认仅 **#71 blocked_by #70** 已存在。下列 20 条是完整原计划尚缺关系，尚未写入；无 parent subissue／父正文变更：

- 已存在票需补 7 条：#73←#71；#74←#71；#75←#71；#76←#71、#75；#77←#70、#75。
- T08 需 2 条：←#71、#77。
- T09 需 3 条：←#76、#77、T08 实号。
- T10 需 2 条：←#77、T08 实号。
- T12 需 6 条：←#73、#74、#76、T09 实号、T10 实号、#72。

若批准外发，实际创建前再次搜索标题／T 编号；原生边先读取再仅补缺项。不得重建已有 8 票或重加已存在边。先取得真实 issue 数据库 ID，未发布票不猜序号。实际写后回读正文、标签、OPEN、父引用及精确 blockers；不能用本地清单代替外部结果。

## 5. 明确不在白名单内

- 原 `core-gate-contract.zh-CN.md`、`balance-refresh.zh-CN.md`、`status-policy-ui.zh-CN.md`、`planning.zh-CN.md`：保留本地调查，不作为 issue body／附件／PR 内容。
- 本清单、`hermes-workpackages.zh-CN.md`、旧交接记录、原始 tool 输出和 job tmp 检查脚本：内部审查材料，不发布。
- 受限提交 `6ebc6997`、后续 `88ef979b` 及其祖先包：不 push／上传／打补丁公开；公开文件的批准不解除历史限制。
- D #68、已有 #70–#77、父 #66：只复用；此次审查不包含更新或关闭它们的权限。
- 不向独立 core 仓库写 issue／代码，不启动实现任务；需要的跨项目执行由 Hermes 通过目标 owner 另行安排。

## 6. 检查与审查请求

- 通过：3 份 SPEC 模板／各 ≥14 条用户故事，7 份候选的父引用／链接目的域／无私有路径及调查特征、简单秘密前缀检查；逐条人工核对父需求与提案性质，哈希如上。另验证七份清单字节数／hash 完全一致、本目录 23 份 Markdown 的 45 个相对链接／围栏／空白、四包对 12 票唯一 primary 归属、21 条直接依赖无环（1 条已建立、20 条待补），`git diff --check` 通过。
- 检查脚本首次把 HTTPS 中 `s:/` 误判为盘符，已定位并修正为带边界的盘符匹配后通过；没有修改候选以掩盖问题。这些静态检查不构成无泄露的自动保证，仍需 Hermes 逐正文审查。
- 未运行业务、真实查询、路由、UI／品牌、构建、CI；未访问运行服务。当前清单、认证与 core 当前能力仍是后续实证门槛。
- **请求 Hermes 审查输出**：逐项 ACCEPT／REVISE／HOLD（附所审 hash）、是否批准上面的受限实号替换及 20 条关系动作、允许的实际外发阶段。未收到批准前保持本地，不以推荐默认代替该发布前置条件。
- 四包 workspace／owner／输入／交付／里程碑依赖／验收／退出门槛见[Hermes 工作包交接](./hermes-workpackages.zh-CN.md)。Hermes 建 4 张 SPEC 卡，worker 不建板、不加卡，不另启 writer。
