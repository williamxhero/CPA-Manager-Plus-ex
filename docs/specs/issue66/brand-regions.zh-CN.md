# [SPEC 66-D] EX 图形区域定位与可复现品牌资产

- 父规格：[#66](https://github.com/williamxhero/CPA-Manager-Plus-ex/issues/66)。
- 状态：规划，未实现、未测试、未部署。GitHub 子 SPEC：[#68](https://github.com/williamxhero/CPA-Manager-Plus-ex/issues/68)。
- 依赖：无余额或 core 门禁依赖，可独立实施与验收；仍归父规格最终完成门槛。

### Use Case

用户在启动页、登录页、侧栏及不同尺寸图标看到 CPAMP EX 品牌时，需要完整读清图文组合中的文字，且角标只出现一次。

## Problem Statement

现有生成器按整张图片边界放 EX，竖排／横排图文资源的文字可能被覆盖。纯 wordmark 也被无差别加标，侧栏又在已有角标的图形上加 DOM EX，形成双角标。只修单张 PNG 或登录页盖层会留下 SVG、图标、副本和回退资源不一致。

## Solution

从未加标的干净源可重复生成，按每类资产的图形区域右下角定位，保护文字区域。纯 wordmark 不覆盖文字加标。同步所有输出、内联回退和运行消费者，自动几何断言加渲染检查证明无文字覆盖、无裁切、无重复。

## User Stories

1. 作为用户，我希望 splash 的 EX 在图形角落，以便读清下方品牌文字。
2. 作为用户，我希望登录页横排与竖排资源都不遮字，以便一致识别品牌。
3. 作为用户，我希望侧栏只有一个 EX，以免标签叠印。
4. 作为用户，我希望纯文字 wordmark 不被角标覆盖，以便保留可读性。
5. 作为用户，我希望明暗主题与窄屏一致，以免只修一种布局。
6. 作为用户，我希望小 favicon／touch icon 无裁切且可辨认，以便不同设备都正常。
7. 作为维护者，我希望同一源生成所有 SVG／PNG／ICO 与副本，以便不会手工漂移。
8. 作为维护者，我希望内联 fallback 与发布副本同步，以免加载失败时出现旧遮挡。
9. 作为维护者，我希望从干净源重复生成不会叠加角标，以便安全再生成。
10. 作为维护者，我希望测试证明文字保护而不是只检查 EX 字符串，以免断言通过仍遮字。

## Implementation Decisions

- 保留现有 generator 与 pristine git source 的可复现边界；所检查默认源 ref 为 `419c65fc`，实现时先核实其可解析且为未加标源，不改成当前已加标图。此为源身份选择，不依赖 live。
- 推荐资产类别 manifest 记录 canvas、symbol bounds、protected text bounds、badge policy、anchor 与尺寸规则；显式维护元数据，而非对像素内容做不稳定自动识别。
- symbol-only：图形区域右下角；竖排／横排图文：各自 symbol bounds 右下角；pure wordmark：不加 EX。角标需位于 canvas 内，不能与 protected text bounds 相交；尺寸按图形而非整图计算。
- 只覆盖 CPAMP 自有品牌，不修改第三方 provider 标志。保留每档图标的可读性策略，不用缩放大图替代 16px 像素字形验证。
- 同一次生成同步 SVG、PNG、ICO 每帧、touch icon、manager-server 副本及三处内联 fallback；枚举旧的 15 SVG／15 PNG 与 icon／副本全覆盖。可改变按类别是否加标，但不能遗漏输出。
- 侧栏删除重复表达的视觉角标；若需要保留可访问 EX 品牌文字，可用不叠印的语义表达，不再要求可见 DOM badge。splash、登录各 logo、侧栏 symbol／wordmark、favicon／touch 和 HTML fallback 都检查真实消费者。
- 当前测试要求所有 wordmark 都有 EX，并强制侧栏 DOM tag；这些是已被父规格改写的旧行为断言。实现时替换为分类规则、几何边界、无重复和可读性断言，不删除／跳过仍有效的字节副本、fallback、ICO 像素等检查。
- `--check` 仍仅比较重建结果，不修改工作树；生成器无“只修输出”的旁路，重复生成幂等。

## Testing Decisions

复用仓库品牌生成完整性测试和 generator check；不为此引入新的验证框架。原来“有 EX 标记”不足以证明布局正确，需增加显式区域几何及实际渲染检查。

验收覆盖：

1. 竖排／横排图文 EX 均锚定图形右下角，badge 与文字保护区无交集，canvas 不裁切。
2. pure wordmark 无覆盖标记；symbol 正确且只有一个可见 EX；第三方标志不变。
3. 所有尺寸、主题资源及 ICO 各帧像素／可读性通过，保留适用的 16px 字形检查。
4. SVG／PNG／server copies／touch／三处 inline fallback 同步，副本一致，坏资源不能靠 fallback 掩盖。
5. 从同一干净源连续生成两次无差异；`--check` 通过且不改文件；缺源或元数据应明确失败而非拿输出再叠标。
6. splash、登录竖横 logo、侧栏展开／折叠、wordmark、favicon／touch、回退在明暗主题及窄屏渲染无文字遮挡、无双角标；记录实际构建 head 的截图／几何证据。

## Out of Scope

不整站重设计，不动第三方商标，不改余额与 gate，不在本轮生成或修改任何业务／资产输出，不发布或重启服务。

## Further Notes

- 部署门槛：generator check、品牌及 repo source integrity 测试通过，前端类型／构建与 icon 检查、实际页面渲染通过；Hermes 独立验收后再授权发布。仅修改源文件或截图不等于部署完成。
- 回退：源元数据、生成器及全部输出作为同一版本回退，不能留下部分新 PNG 与旧内联 fallback。涉及线上缓存时按既有包版本策略验证，不在本轮触碰 live。
- 本包独立，不应因 core 缺能力而阻塞；父 #66 不能因品牌先完成就被关闭。
