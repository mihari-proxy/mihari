# Issue #204：Zashboard 与 MetaCubeXD 的 provider 测速实现对照

检索日期：2026-09-13。本文是固定源码版本的静态调查，不代表用户现场安装的面板版本，也未执行真实内核或网络测速。

## 问题与方法

确认面板如何发现仅存在于 provider 的节点、选择单节点/组测速接口，以及处理同名节点。用于解释 Mihari TUI 的 provider-only 404 缺陷，并界定可借鉴的修复方式。

关键词：provider 节点、测速、同名、healthcheck、proxy-provider latency、provider-scoped endpoint、duplicate node names。

当前环境未找到 searxng-search，使用会话 web 搜索和 GitHub 官方仓库源码交叉核对。克隆两个仓库到 repos/，未安装依赖、执行仓库脚本或运行面板测试。网页 main 内容可能变化；以下结论以本地固定 commit 的完整源码为准。

| 项目 | 固定 commit | 角色 |
| --- | --- | --- |
| [Zashboard](https://github.com/Zephyruso/zashboard) | b31d05f42702b121e0b17eab1e3697d5f1d6db8d | 用户指定的主要参照 |
| [MetaCubeXD](https://github.com/MetaCubeX/metacubexd) | 8bbc8f58fef71148a94fb5c0ff808f79b057337d | Mihari 另一个内置面板，交叉参照 |
| [mihomo](https://github.com/MetaCubeX/mihomo/tree/v1.19.30) | v1.19.30 | 上一轮核对的接口语义基线，不等同于用户内核版本 |

## Zashboard：先合并节点数据，再选择接口

源码：[src/assembly/proxies/clash.ts](https://github.com/Zephyruso/zashboard/blob/b31d05f42702b121e0b17eab1e3697d5f1d6db8d/src/assembly/proxies/clash.ts)。

1. fetchProxies 同时请求 /proxies 与 /providers/proxies。过滤 default 和 vehicleType=Compatible 的 provider。
2. 遍历 provider 的节点，缺少 provider-name 时填入所属 provider 名，再合并进 proxyMap。合并顺序使全局 /proxies 对象覆盖同名 provider 对象。
3. getProviderNameByProxy 优先使用节点 provider-name（且检查 provider 是否仍在列表），否则扫描 provider 列表，取第一个包含同名节点的 provider。
4. fetchNodeLatency 能确定 provider 时调用 provider 专用单节点接口，否则调用普通单节点接口。IPv6 探测也复用这层选择。
5. proxyLatencyTest 最终重新获取节点状态；面板逐节点批量测速使用 pLimit(5)，结束后也尝试刷新。

API 定义：[src/api/clash.ts](https://github.com/Zephyruso/zashboard/blob/b31d05f42702b121e0b17eab1e3697d5f1d6db8d/src/api/clash.ts)。

| 动作 | 接口 |
| --- | --- |
| 普通单节点测速 | GET /proxies/{name}/delay |
| provider 单节点测速 | GET /providers/proxies/{provider}/{name}/healthcheck |
| 组测速 | GET /group/{group}/delay |
| 整个 provider 健康检查 | GET /providers/proxies/{provider}/healthcheck |

组测速并非永远调用组接口：当 speedtestMode=DASHBOARD 且组类型为 Selector/LoadBalance/Smart 时，拆成逐节点任务，并通过上述 provider 路由选择；其他分支调用组接口。全量测速在独立测速模式下遍历组，否则遍历合并后的非组、可测速节点。

provider 页面健康检查按钮调用整个 provider 的 healthcheck，然后重新获取状态。参见 [ProxyProvider.vue](https://github.com/Zephyruso/zashboard/blob/b31d05f42702b121e0b17eab1e3697d5f1d6db8d/src/components/proxies/ProxyProvider.vue#L140)。

### 同名处理的局限（源码推断，未做运行复现）

- allProviderProxies 仍以 proxy.name 为键，多个 provider 同名会被后遍历者覆盖。
- 全局同名对象覆盖 provider 对象；若全局对象没有 provider-name，后续 provider 扫描仍可能选中同名 provider。因此不能把合并顺序理解为所有操作都严格优先全局节点。
- fallback 的 find 取第一个匹配 provider，没有显式拒绝歧义。
- 节点卡片只向测速动作传名称与测试 URL，未将当前 provider 身份贯穿请求与结果缓存。

所以它解决了常见的 provider-only 节点 404，但不是完整的多 provider 同名身份模型。

## MetaCubeXD：显式传 provider 的请求函数

源码：[useApi.ts](https://github.com/MetaCubeX/metacubexd/blob/8bbc8f58fef71148a94fb5c0ff808f79b057337d/packages/ui/composables/useApi.ts#L386)、[stores/proxies.ts](https://github.com/MetaCubeX/metacubexd/blob/8bbc8f58fef71148a94fb5c0ff808f79b057337d/packages/ui/stores/proxies.ts)。

- fetchProxiesImpl 同时获取普通节点和 provider 数据。对未出现在全局映射中的 provider 节点，补充 provider 字段并加入节点元数据。
- proxyLatencyTestAPI(proxyName, provider, url, timeout) 在 provider 非空时使用 provider 单节点 healthcheck；否则使用普通节点 delay。路由选择是请求发出前完成，不是先请求普通接口再依据 404 重试。
- 节点卡片经 useProxyNode 从节点元数据读取 provider 后传入测速动作。参见 [useProxyNode.ts](https://github.com/MetaCubeX/metacubexd/blob/8bbc8f58fef71148a94fb5c0ff808f79b057337d/packages/ui/composables/useProxyNode.ts#L77)。
- proxyGroupLatencyTest 使用组测速接口。
- provider 批量测速使用 provider 的测试 URL/timeout，按 10 个一批调用 provider 专用单节点接口，并记录返回值；因此可以传入测试参数并逐节点处理结果。
- 单节点 HTTP 客户端超时设置为 max(20000, timeout+10000)，给内核测速预算留出传输余量。这个做法解决另一类过早取消问题，不能解释本次隔离复现中的立即 404。

### 同名与其他入口的局限

- setProxiesInfo、延迟状态仍按节点名建索引，不按 provider+name 分离；多个 provider 同名仍可能互相覆盖。
- 对组成员 all 进行 Set 去重，并有对应测试；去重只减少重复行，不解决 provider 身份歧义。参见 [proxies.spec.ts](https://github.com/MetaCubeX/metacubexd/blob/8bbc8f58fef71148a94fb5c0ff808f79b057337d/packages/ui/stores/__tests__/proxies.spec.ts#L81)。
- useProxyNode 的测速 provider 来自按名称查找的元数据，providerName 选项用于测试中状态判断，不能视为所有卡片操作都按当前 provider 隔离。
- [useBatchLatencyTest.ts](https://github.com/MetaCubeX/metacubexd/blob/8bbc8f58fef71148a94fb5c0ff808f79b057337d/packages/ui/composables/useBatchLatencyTest.ts#L52) 的通用 testSingleNode 仍固定调用普通节点接口。本次 batchTestNodes 调用点搜索仅发现测试代码，不能据此宣称当前用户主流程一定会触发该缺口，也不能宣称所有入口都支持 provider。

## 错误展示不能替代 daemon 诊断

Zashboard 的单节点测速失败被捕获并展示统一 testFailedTip；逐节点批量失败主要计数、写失败状态，最后汇总。其他用户操作可以走 notifyRequestError 展示请求错误。参见 [requestError.ts](https://github.com/Zephyruso/zashboard/blob/b31d05f42702b121e0b17eab1e3697d5f1d6db8d/src/helper/requestError.ts)。

MetaCubeXD 的单节点测速 catch 保留此前延迟值，追加失败历史；catch 没有逐项分析 HTTP 状态。以上是界面策略，不证明其持久化诊断日志完整，也不适合据此推导节点一定不通。

Mihari 仍需要独立解决 daemon 日志没有输出上游状态与操作类别的问题。

## 对 Mihari 的建议（设计判断，尚未实现）

1. 同时发现普通与 provider 节点，修复 provider-only 节点元数据为空的问题。
2. 由 daemon/Manager 所有的用例组织节点身份解析，mihomo 适配层封装具体 REST 路径；TUI 不直接连接 controller。
3. 在单节点测速发出前明确身份；provider 节点使用专用单节点接口，保留组测速作为不同的操作语义。不要简单把所有 TUI 逐节点操作改为组测速。
4. 不照搬按名称覆盖或取第一个 provider 的行为。需要明确普通/provider 同名、跨 provider 同名，以及刷新后身份变化的处理；仅有名称且无法唯一定位时不能猜测。
5. 列表状态、测速任务去重、结果缓存都要服从同一身份规则，避免只修路由而结果仍写到错误节点。
6. 诊断以安全字段保留上游状态和固定操作类别，不输出完整 URL、secret 或响应正文。若需要新增公开 DTO/CLI 字段，先说明契约影响并取得确认。

这些对照进一步支持已有缺陷定位，但没有补齐用户现场 338 条错误的 HTTP 状态证据。

## 本次工作区与验证

仅新增本研究目录及忽略的源码克隆，未修改原调查报告、复现脚本或生产代码。已读取固定源码及相关测试文本，未运行上游测试、浏览器或真实环境验证；未创建 commit、推送或发布 Issue 评论。
