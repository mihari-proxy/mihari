# 连接详情纵向链路设计

状态：设计已收口。用户逐项确认 Q1–Q6 及后续字段合并、标签和配色修订，开始实现与验证。

## 范围

- 初始基于 origin/dev 的 b91499c，实施前已 rebase 至 a636792；使用 feat/conns-route-visualization 分支及同名 worktree。
- 调整现有单页 TUI 连接详情；保持英文界面与现有主题。
- 直接在对话中使用文字线框图沟通，不生成 HTML。
- 本文记录可逆的布局选择，不为此新增 ADR。领域词汇记录在根目录 CONTEXT.md。

## 已确认

1. 使用方案 B：以纵向连接链路作为页面主体。
2. 核心阶段为 Application → Routing → Outbound → Destination。Inbound 合并进 Routing，使用一行 `Inbound  {入口名称} · {入站类型} · {网络协议}`，例如 `Inbound  DEFAULT-TUN · TUN · TCP`；入口名称在最前，不再单独显示 Inbound 阶段或 Inbound name 行。入口名称缺失时省略名称及相邻分隔符，保留类型与协议。
3. 主要连接信息就近挂在对应阶段，不能移入独立的“补充连接信息”区。
4. Remote 放在 Outbound 内；Destination 作为核心链路终点，展示目标域名、Destination 地址及端口。
5. Remote 和 Destination 的 GeoIP 信息需分别关联自己的地址，不能混成一组无归属的地址资料。
6. 延续单页、长字段换行、滚动、返回原行、Paused 与 Closed observed 的现有行为。
7. 完整字段就近展开：Process path 放 Application，Inbound name 合入 Routing 的 Inbound 行，Inbound user 有值时在 Routing 独立显示，Sniff host 放 Destination；取消独立 Metadata 区。
8. 累计流量与速率放顶部；Started、Closed observed 和 Connection ID 放底部。
9. Routing 内的选择关系逐行逐级展开，不使用紧凑的单行箭头链。
10. 上传速率与上传累计量使用 Connections 列表的绿色，下载速率与下载累计量使用同一列表的蓝色；保留方向符号及文字标签，不能只靠颜色区分。Closed 仍标注最后观测速率。
11. 缺失信息仍保留四个主体阶段，核心字段使用 `—`，不推测地址；Process path、Inbound user、Sniff host 等可选字段为空时省略。
12. Routing 的规则类型与匹配内容合并为一行 `Rule Matched  {类型} · {payload}`，例如 `Rule Matched  RuleSet · Microsoft`；payload 为空时仅显示类型，例如 `Rule Matched  Match`，不显示空分隔符。规则类型缺失时仍使用核心字段占位 `—`。
13. REJECT 出站文字标红，通向 Destination 的连接线使用断线，Destination 节点变灰；保留请求目标资料，不暗示目标已连接。
14. 长节点名在本层换行；过深选择树限制缩进并标记层级序号，保留全部名称、顺序及层级，不新增横向滚动。
15. 面板居中，继续使用 88 个终端字符列的外宽上限，窄窗口随可用宽度收缩。

## 已核对事实

- `destinationIP` 是 mihomo 已知的目标地址，可能由入站、解析或 hosts 处理产生，也可能为空。
- `remoteDestination` 是本地出站的远端对端：TCP 优先从底层连接取得，失败时回退到 adapter 地址；UDP 来自 adapter 地址。字段为 host/IP，不带端口，不与目标端口拼接，也不命名为公网出口 IP。
- `chains` 原序为叶子出站 → 内层组 → 外层组；展示为反向的逐级选择关系，仅在视图中反转，保留原始观测不变。
- DIRECT、REJECT 也是出站选择；Remote 可以缺失。DIRECT 的 Remote 与 Destination 可相同，仍各自保留语义归属。
- dialer-proxy 的底层路径不保证完整反映在顶层 chains 中，因此不宣称展示完整网络拓扑；Outbound 下的 Remote 表示观测到的远端，而非保证是叶子名称对应服务器。
- 本地 `internal/mihomo/types.go` 与 `internal/control/server/runtime.go` 透传上述字段。

上游证据（mihomo commit `fbb674227d5cf5a3796a1dd1451849fa1872884a`）：

- [连接目标元数据](https://github.com/MetaCubeX/mihomo/blob/fbb674227d5cf5a3796a1dd1451849fa1872884a/constant/metadata.go#L193-L220)
- [RemoteDestination 与 chains 构造](https://github.com/MetaCubeX/mihomo/blob/fbb674227d5cf5a3796a1dd1451849fa1872884a/adapter/outbound/base.go#L225-L318)
- [Selector 追加自身的顺序](https://github.com/MetaCubeX/mihomo/blob/fbb674227d5cf5a3796a1dd1451849fa1872884a/adapter/outboundgroup/selector.go#L23-L38)
- [底层拨号代理](https://github.com/MetaCubeX/mihomo/blob/fbb674227d5cf5a3796a1dd1451849fa1872884a/component/proxydialer/proxydialer.go#L20-L50)

## 当前决策树

- 已定：纵向主体及字段就近归属。
  - 已定：长字段直接展开，速率和累计量在顶部，时间与 ID 在底部。
  - 已定：Routing 内逐行逐级展开选择树。
  - 已定：上传绿色、下载蓝色，速率和累计量遵循相同配色。
  - 已定：缺失信息保留阶段及核心字段占位，可选空字段省略。
- 已核对：字段和代理链语义，见上节。
- 已定：Q4 拒绝连接断线与目标灰色节点；Q5 深层链路限缩进并显示层级序号；Q6 保留 88 列上限。
- Frontier 为空；进入实现和验证，保留本次已确认范围。
