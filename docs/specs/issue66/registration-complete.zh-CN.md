# #66 登记完成与品牌阶段交接

日期：2026-10-16。LOCAL ONLY；不得连同旧调查历史 push。本文是此前规划／待审记录的最新状态补充，不删除或改写其历史证据。

## 实际登记结果

| 工作包 | 真实 SPEC | 主责票据 |
| --- | --- | --- |
| A：独立门禁消费、协调与迁移 | #79 | T01 #70、T07 #77、T09 #83 |
| B：安全查询、当前适配器与小时刷新 | #80 | T02 #71、T03 #73、T04 #74、T05 #75、T06 #76 |
| C：provider 策略、全部状态界面与最终验收 | #81 | T08 #82、T10 #84、T12 #85 |
| D：图形区域品牌 | #68 | T11 #72 |

仓库均为 https://github.com/williamxhero/CPA-Manager-Plus-ex 。父 #66、品牌 #68 正文未改，仍 OPEN。七份获审正文已发布为 #79–#85，逐目标回读正文与本地字节 SHA-256 完全一致，均 OPEN／ready-for-agent。

T09/T10 仅将 T08 占位替换为 #82；T12 仅将 T09/T10 占位替换为 #83/#84。最终 SHA-256：

- T09：0bf2bce5a56587045b07b0a4a85d4f1f85357f3c57fcf4f124268b50a354df5f
- T10：55c7ba72ff47a372daa87bb4d90d0552359163267cc6843d2bb6428430729266
- T12：0b861fd5c83f915bbcf55b57549a8825257fafe3d5e30c76786f23702761bbe4

与 HEAD 原批准内容及原始指纹比较，确认除此之外无语义改动。其余四份正文沿用 hermes-payload-review 原指纹。

## 已完成原生依赖

下列箭头为目标 ← blockers：

- #70 ← 无
- #71 ← #70
- #73 ← #71
- #74 ← #71
- #75 ← #71
- #76 ← #71、#75
- #77 ← #70、#75
- #82 ← #71、#77
- #83 ← #76、#77、#82
- #84 ← #77、#82
- #72 ← 无
- #85 ← #72、#73、#74、#76、#83、#84

21 条总边，20 条本轮新增；最终集合断言 present=21、missing=0。Hermes 另已独立回读并 assert 通过；不再重复读取或另造证据包。

工作包按里程碑推进：A 身份／能力 → B 查询 → A 门禁 → C 策略 → A 迁移 → C 最终验收，D 独立；不把 A/C 包整体互锁。四张 Kanban 已由 Hermes 建立，D=t_75519d08；本 worker 不建板、不启动第二 writer。

## 原树保留与下一阶段授权

本树 D:/WILL/AGENT/CPA/cpamp-issue66-plan 的既有提交、修改和未跟踪文件全部原地保留；不 reset、不 stash、不删除、不 push、不 cherry-pick 到品牌树。此前 planning、ticket-handoff、payload-review、workpackages 中“待审／未登记”的文字为历史快照，以本文真实映射为准。

唯一 writer/session：bdb38eb8-7a2b-40fe-881b-c5925885bd0a。

用户已解除仅品牌阶段的临时 planning-only 限制，授权在 Hermes 准备的 D:/WILL/AGENT/CPA/cpamp-brand-implementation、claude/issue68-brand-regions、基线 c0d5c952c274c8b9237aadcba9edda54e3fe720a 实现 #68/#72。进入前已只读确认该树干净、分支注册正确。下一步读取该树规则并实际切换 cwd，执行 implement-spec 或如实记录 3.0 允许的 phase-skill fallback。

范围仅品牌 generator/manifest、自有 SVG/PNG/ICO 每帧、server copies、三个 inline fallback、消费者去双 EX 与必要测试。必须保留几何字区保护、干净源幂等、generator/icons、完整前端回归/type/lint/build及页面渲染证据。不得改余额/core；不得部署、重启、访问 live8317／小电脑／其它仓库。仅品牌分支 commit/push 到 fork、PR base dev；不 merge、不 close。Hermes 独立集成验收尚未发生。
