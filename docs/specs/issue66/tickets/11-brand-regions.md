# T11：EX 图形区域生成与全资产验证

## Parent

- 直接父 SPEC：[#68](https://github.com/williamxhero/CPA-Manager-Plus-ex/issues/68)。总需求：[#66](https://github.com/williamxhero/CPA-Manager-Plus-ex/issues/66)。工作包 D，仅依据这两份公开需求。

### Use Case

用户在 splash、登录、侧栏及各尺寸图标看到 CPAMP EX 时，需要文字完整且角标只出现一次。

## What to build

从干净原图可复现生成全部自有品牌资产，按图形区域右下角定位 EX，保护文字；贯通生成源、全部格式／副本／回退与实际页面消费者，而不是只修单张输出。

## Acceptance criteria

- [ ] 资产类别记录图形区域、文字保护区及角标锚点；竖排／横排图文均不按整图右下角替代图形锚点；纯 wordmark 不覆盖加标。
- [ ] SVG、各档 PNG、ICO 每帧、touch icon、服务端副本、内联 fallback 同步；重复生成幂等，不从已加标输出继续叠印；第三方 provider 商标不变。
- [ ] splash、登录卡、侧栏展开／折叠、wordmark、favicon／touch 和 fallback 在明暗、窄屏及不同分辨率无文字覆盖、无裁切、无双 EX。
- [ ] 几何不相交与实际渲染检查通过；保留有效的像素、资源副本和完整性测试。仅替换已被新需求改写的“所有 wordmark 强制 EX”等旧断言，不跳过有效回归。
- [ ] 生成器检查、相关仓库测试、前端类型／构建及图标检查通过并记录准确候选版本；源、生成器与所有输出可一起回退。
- [ ] Hermes 独立验收和后续授权发布后才可宣称部署；本轮只规划，不生成或修改业务资产、不部署、不关闭父 issue。

## Blocked by

None (can start immediately)。不依赖余额或 core gate。
