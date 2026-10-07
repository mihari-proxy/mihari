# Issue #291 出口网卡选择设计访谈

状态：Q1–Q24 产品决定已收敛；B 精修版交互、CLI、持久化及本地接口扩展已获用户批准。用户已授权并完成生产实现，验证及 PR 交付进度见[实施计划](../plans/2026-09-21-egress-interface-implementation.md)。本文保留原访谈决定与原型验收记录，真实网络验证仍需单独进行。

关联：[Issue #291](https://github.com/mihari-proxy/mihari/issues/291)。

## 已确认

- 用户场景：两个代理软件同时开启全局 TUN；本功能仅考虑流量出口，不设计流量入口的接管与分配。
- 2026-09-21：用户宣布访谈前的建议设计全部作废，由 grill 逐项重新决定。此前的 UI 位置、配置优先级、失效策略等建议均不自动成为需求。
- Q1：手动选择网卡后，目标是 mihomo 的全部出口流量均使用该网卡；范围不限于连接代理节点的流量。这是待实现及验证的产品要求，不代表现有内核配置已经能够完整保证。
- Q2：出口选择属于整个 Mihari 实例，切换订阅不改变选择；不按订阅、节点或代理组分别保存。
- Q3：自动模式就是当前没有配置 Mihari 出口网卡时的状态，恢复既有配置行为，不新增“跟随配置 / 真正自动 / 手动”的三模式划分。
- Q4：手动选定的网卡失效后坚持原选择，不自动切换其他网卡；TUI 必须显示原来选择的网卡及其不可用状态。判据和生命周期由 Q9/Q11 确定，原生绑定保证边界由 Q8 确定。
- Q5：切换网卡时采用立即切换语义，允许断开旧连接，重建连接走新网卡。仅修改后续连接的默认接口不满足此要求。
- Q6：允许选择其他软件的 VPN/TUN 等虚拟网卡；并非只允许物理网卡。自身 TUN 禁选见 Q16，本机通信的原生语义见 Q8。
- Q7：手动选择优先，生成运行配置时统一覆盖冲突的网卡设置，保留原订阅，切回自动恢复既有行为；不因覆盖网卡而顺带改变代理节点、代理链或 DNS 服务器的选择。
- Q9：不可用以网卡自身状态判断，例如不存在、禁用、链路断开；不增加定期访问外部目标的联网探测。网卡状态正常时的访问失败通过具体连接诊断表达，不自动等同网卡不可用。
- Q8：沿用 mihomo 原生出口绑定语义及其例外，Mihari 负责配置与管理，不重新实现 socket 绑定、路由或额外网络隔离。此决定界定 Q1 的“全部出口”承诺：覆盖受管出口配置，同时保留内核本身的通信语义；不能宣称所有 socket 均无例外地被强制绑定。见 [ADR 0006](../../adr/0006-native-egress-binding.md)。
- Q10：默认热应用，通过原生配置重载及活动连接关闭完成切换，沿用 mihomo 对后台连接及复用会话的处理，不自动重启核心。
- Q11：所选网卡失效时保持核心运行，让 mihomo 按保存的绑定处理连接；不因失效主动停止核心。不改变原选择、不自动回退，并显示不可用状态。
- Q12：TUI 入口位于 System → Network；配置在独立弹窗中完成，用户明确要求美观的设计。最终布局与交互见 Q15。
- Q13：先在弹窗中选择，再通过固定底部的 Apply 提交，浏览列表不改变运行设置；Cancel 取消。应用成功后关闭，失败时保留弹窗和候选选择并显示原因。
- Q14：允许选择并保存当前不可用的网卡，例如 VPN 尚未启动时先保存该选择；接受暂时无法连接，不自动回退。应用与展示需区分设置已保存、网卡不可用和真正的配置应用失败。
- Q15：用户明确“设计方案通过”，批准 B 精修版（左侧列表、右侧详情）及补充的方向键滚动交互。以此作为生产 TUI 的布局与交互依据；A/C 仅保留为原型对照。
- Q15 补充：用户认可 B 第一轮精修，要求左侧网卡列表支持方向键滚动。打开时焦点进入已保存选择，↑/↓ 移动候选并同步右侧详情，超出可见区域时列表跟随滚动；浏览不应用配置，仍需 Apply。
- Q16 A：Mihari 自己创建的 TUN 在列表中显示但禁止选择，并注明原因；其他软件的 TUN 仍可选择。具体自身身份识别需覆盖核心运行与停止状态。
- Q17 A：TUI 与 CLI 同时支持列出网卡、查看和修改实例出口选择，共用 daemon 控制接口；具体命令语法、DTO 和持久化变更仍待明确。
- Q18 A：按接口名称保存选择；名称改变后原选择显示不可用，同名接口重新出现后继续使用，即使是重新创建的设备。不承诺跟随设备身份跨重命名迁移。
- Q19：核心停止时允许保存，保存成功后关闭弹窗，下次启动核心时应用；不主动启动核心。用户明确要求只显示 `Saved`，不显示 `Pending`。保存成功不能被内部状态或协议误报为已经向运行核心应用。
- Q20 澄清：用户指出选择出口与开启自身 TUN 是两种独立操作，质疑为何需要再次确认。出口 Apply 不触发双 TUN 确认，不将现有 EnableTun 的冲突确认串入出口选择流程。本功能仍按仅管理出口的既定范围推进，不将该答复扩大解释为要求删除现有 EnableTun 检查。
- Q21 B：TUI 与 CLI 均只允许选择当前枚举到的网卡，或当前已保存但已消失的网卡；不允许手工预设从未出现且未保存的名称。自身 TUN 的禁选规则仍优先适用。不据此新增所有历史选择的网卡收藏库。
- Q22 A：真正的配置校验或重载失败时，回滚设置与运行配置；弹窗保留候选和原始错误以便重试。网卡暂时不可用与配置应用失败分开处理。回滚不代表已关闭连接能够恢复，回滚本身失败也必须如实报告。
- Q23 OK：采用 `mihari egress list|status|set <interface-name>|auto`；支持 `--json`，修改命令支持 `--if-revision`。
- Q24 OK：批准可选 `egress-interface` 持久化字段及 `/v1/egress` 本地 IPC 扩展，接受旧版严格配置读取的降级影响；自动模式移除字段，无字段的旧配置维持既有行为。

## 设计树与实施核对项

- 范围：仅出口、全部 mihomo 出口流量、实例级选择（已确认）。
  - 自动模式恢复当前未配置出口网卡时的行为；无字段兼容旧配置，切回自动移除字段（已确认）。
  - 手动选择覆盖冲突的网卡设置，原订阅保留，自动模式恢复既有行为（已确认）；各配置路径的可靠覆盖方法（待设计）。
  - 可选择物理及其他软件的虚拟网卡、沿用内核本机通信语义，自身 TUN 显示但禁止选择（已确认）；自身身份识别方法（待设计）。
  - 按接口名称保存；重命名使原选择不可用，同名重建继续使用（已确认）。
- 生命周期：
  - 所选网卡失效后不回退，TUI 展示原选择及不可用状态；按网卡自身状态判断，不增加公网探测，保持核心运行（已确认）。
  - 切换网卡时允许断开旧连接，立即采用新出口（已确认）；连接与底层复用会话清理方法（待设计）。
  - 核心停止时保存且只显示 Saved，下次启动应用，不主动启动核心（已确认）。
  - 不允许预设尚未枚举到且未保存的名称；同名接口恢复后继续按原名称拨号，沿用内核原生缓存与复用语义，不自动新增 reload/restart（Q8/Q10/Q18 的组合约束；源码事实见下）。
  - 真正应用失败回滚设置与运行配置，保留候选和错误（已确认）。
- 应用与展示：
  - 保证受管配置覆盖及原生绑定语义，不承诺无例外的 socket 隔离、所有池化会话即时迁移或互联网连通；实施按测试矩阵验证。
  - TUI 入口 System → Network，使用 B 精修版独立弹窗及方向键滚动；CLI 同样提供列出、查看和修改能力。具体 CLI、协议扩展与持久化方案获 Q23/Q24 批准。
  - 出口 Apply 与开启自身 TUN 独立，不触发双 TUN 确认；不扩大修改现有 TUN 开启流程（Q20 澄清）。

## 剩余边界的仓库事实

### CLI 与持久化 / 本地 API 扩展（Q23/Q24 已批准）

- Q23：新增 `mihari egress list`、`mihari egress status`、`mihari egress set <interface-name>`、`mihari egress auto`。沿用全局 `--json`；set/auto 支持 `--if-revision`。名称使用列表返回的精确接口名，包含空格时由 shell 引号包围；独立 auto 子命令避免与名为 auto 的真实接口冲突。set 的候选约束由 daemon 执行，与 TUI 一致。
- Q24：settings 增加可选字符串 `egress-interface`，手动模式保存接口名称；自动模式删除字段，旧配置缺省仍按当前行为运行，不改写原订阅。此字段不放入 TUN 配置块。
- 新增认证本地 IPC 的 `GET /v1/egress` 与 `PATCH /v1/egress`，共用既有 mutation coordinator。GET 返回 schema、revision、保存的选择、运行配置应用状态及候选列表；PATCH 使用 operation_id、可选 if_revision、明确的 mode（automatic/manual）与手动模式的 interface_name。新增 capability `egress-interface-v1`，旧 daemon 不支持时客户端清楚提示，不退化为直写文件。保持现有 envelope、错误码与退出码语义，不映射到浏览器 gateway。
- 应用状态只表达配置是否已应用，不表示访问互联网成功；停核保存的界面文案仅为 Saved。运行核心无法读取时不可假称已应用；原选择缺失仍作为候选列出并标注不可用。候选列表至少提供精确名称、可用状态、可选性及不可选原因；地址与类型用于已批准的详情布局，无法识别的值如实呈现未知。
- 兼容性影响：新版本读取无字段的旧配置时保持自动；现有旧版本因严格未知字段校验，可能拒绝包含该字段的配置。降级前应先用新版本切回自动并确认字段已移除。用户已接受该影响；新增契约尚未实现。

- 显式开启 TUN 时，Manager 对检测到的其他 TUN 执行冲突检查；CLI 可使用 `tun enable --force`，TUI 有对应的再次确认。这是开启自身 TUN 的流程，与出口 Apply 无关。参见 `internal/runtime/tun.go:52`、`internal/cli/tun.go:53`、`internal/tui/pages/system/model.go:2208`。启动时已有的 TUN 配置不走相同检查，不应把现有行为描述为统一启动门槛。
- 核心停止时，routing/logging 有保存设置并返回 pending、等待下次启动的先例；现有 TUN mutation 则要求访问运行核心，并非所有设置都采用相同语义。参见 `internal/runtime/routing.go:167`、`internal/runtime/logging_transaction.go:18`。出口选择采用 Q19 的停核保存语义，但不沿用 Pending 界面文案。
- 当前没有通用出口网卡枚举和身份契约。`internal/tundetect` 只枚举疑似 TUN，Windows 还会过滤 Down 接口，因此不能直接用作完整候选列表。
- settings 使用严格未知字段校验，新增持久化字段会影响旧版读取；具体字段及降级影响须在实施前明确。参见 `internal/config/settings.go:182`。CLI/API 的精确新增契约在网卡身份与生命周期决定后再定稿。

## 上游事实：mihomo v1.19.30

以下为固定提交 ac017cdd246ce8bd547653d927e7bf77d7ee73d5 的源码调查，并非三平台实际网络验收；其他核心版本的能力需单独检查。

- 标准 TCP/UDP 拨号优先取节点显式接口，其次全局接口，再取 TUN 自动发现接口。provider override 可覆写节点接口，dialer-proxy 使用承载代理的拨号路径。因此仅设置全局 interface-name 不能满足全部出口统一的目标。[公共拨号](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/component/dialer/dialer.go#L143-L160)、[provider 覆写](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/adapter/provider/override.go#L39-L74)、[承载代理](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/adapter/outbound/base.go#L193-L218)。
- DNS 可指定代理或接口；名称先匹配代理，未找到才作为接口。不能直接删掉所有 DNS URL fragment，否则会误删代理选择等语义。[DNS 拨号](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/tunnel/dns_dialer.go#L56-L84)。
- TCP 与 connected UDP 的绑定回调接收远端地址，非 global-unicast 地址可跳过实际绑定，包括回环、链路本地和组播。RFC1918 私网地址不属于该豁免。Windows/macOS 在回调前仍会解析接口名，因此失效接口可能先导致失败。[Windows](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/component/dialer/bind_windows.go#L39-L96)、[Linux](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/component/dialer/bind_linux.go#L12-L30)、[macOS](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/component/dialer/bind_darwin.go#L14-L56)。
- unconnected UDP 的 ListenPacket 回调接收本地监听地址，不能套用上述远端分类。常规空地址最终是 :0，ParseAddrPort 失败后继续绑定，因此普通 UDP 没有因 wildcard 地址漏绑；远端 loopback 在 mihomo 公共层有明确清空接口的路径。普通 UDP DNS 使用 connected UDP，DoQ/DoH3 则使用 PacketConn，需分别验证。[Go ListenPacket 控制回调](https://github.com/golang/go/blob/go1.26.5/src/net/sock_posix.go#L181-L218)、[mihomo UDP 拨号](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/component/dialer/dialer.go#L79-L111)。
- 修改全局接口不关闭已有连接。完整配置 reload 也不统一关闭全部连接，但 HTTP/file provider 初始化会关闭所属活动连接；需要为立即切换明确连接及底层会话处理。[配置更新](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/hub/route/configs.go#L320-L388)、[provider 初始化](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/adapter/provider/provider.go#L147-L176)。
- Q10 补充调查：原生配置热更新/重载及 DELETE /connections 可用于切换配置并关闭统计管理器中的活动连接，不要求进程重启。受 Q7 全配置覆盖要求影响，不能仅 PATCH 全局 interface-name；具体使用完整配置应用链路。活动连接关闭不等于所有后台连接、DNS 池或底层复用会话同步销毁，完整重载清理面更广但不承诺进程内瞬间不存在旧 socket。用户已接受此原生热应用语义，不自动将每次切换升级为核心重启。
- 网卡存在、处于启用状态、能到达目标是不同条件；仅成功加载接口名不能证明网络可用。网卡移除后的已有连接表现、特殊协议复用会话、系统 DNS 与所选网卡的可达性仍需隔离验证。
- 缺失/Down 的接口名称不在配置解析阶段触发 OS 接口查找；全局、节点及 provider override 保存名称，DNS 指定接口在实际查询时解析。实际 socket 拨号才可能失败。因此允许保存不可用接口与真正 reload 失败回滚不冲突；不能把 provider 初始化或健康检查失败等同为配置提交失败。[全局配置解析](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/config/config.go#L755-L781)、[配置应用](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/hub/route/configs.go#L391-L439)、[provider 初始化](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/hub/executor/executor.go#L318-L352)。
- Windows/macOS 的名称到接口 index 解析使用约 20 秒 TTL 的接口表缓存；同名重建后的新物理拨号可能在缓存过期前仍使用旧 index。Linux 显式绑定直接使用名称。完整 reload 清缓存，单独 PATCH 不清缓存。按 Q8/Q10/Q18 沿用该原生行为：同名恢复不要求重新选择，也不新增检测后自动 reload/restart；不承诺恢复立即生效或已有复用会话迁移。[接口缓存](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/component/iface/iface.go#L29-L119)、[reload 清缓存](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/hub/executor/executor.go#L393-L418)。若旧 index 被其他设备复用，原生实现也不提供设备身份隔离；Mihari 不主动回退的产品规则不能被描述为 OS 级防泄漏。

## 状态与验收结果

| 场景 | 预期结果 |
| --- | --- |
| 自动模式 | 不施加 Mihari 出口 override，移除新增 settings 字段 |
| 运行时改选 | 生成、校验并事务应用候选配置，关闭活动连接，成功保存并关闭弹窗 |
| 核心停止时改选 | 保存并关闭弹窗，只显示 Saved；下次核心启动使用，不主动启动 |
| 当前或已保存网卡不可用 | 允许保存；保留名称、显示不可用，不回退、不因不可用停核心 |
| 网卡改名 | 原名称不可用，需要用户重新选择新名称 |
| 同名网卡恢复/重建 | 后续物理拨号继续尝试原名称，接受原生缓存及池化行为 |
| 当前没有且不是已保存的名称 | CLI/API 拒绝，TUI 不提供手工输入 |
| 自身 TUN | 展示但不可选，daemon 对 CLI/API 同样拒绝 |
| 浏览、取消、无变化 Apply | 浏览仅改草稿，取消保持设置；无变化不重载、不关闭连接 |
| 校验、应用或提交真正失败 | 补偿恢复之前设置和运行配置；保留草稿及诊断；恢复失败如实报告并沿用既有 degraded 机制 |
| settings 已提交后的同步 warning | 按仓库提交点规则保持成功与 revision，不冒充失败回滚 |

实施工作拆分与验证矩阵见 [实施计划](../plans/2026-09-21-egress-interface-implementation.md)。以上均为设计验收条件，不能表述为生产测试已经通过。

## 弹窗布局（B 精修版已通过）

交互原型位于 [prototype-egress](../../../internal/tui/pages/system/prototype-egress/README.md)，只使用模拟数据，不调用核心或网卡。三套方案共享已确认的业务规则：

| 方案 | 布局 | 取舍 |
| --- | --- | --- |
| A 紧凑列表 | 单行网卡列表，选中项信息放在下方 | 高度较小，操作直接；详细信息较少 |
| B 列表与详情 | 左侧网卡列表、右侧详情，窄窗口上下排列 | 更容易对照网卡身份与状态；宽窗口效果更好 |
| C 分步选择 | 先选网卡，再在第二步核对并 Apply | 每一步内容集中；多一次 Next/Back 操作 |

已在浏览器检查方案切换、不可用网卡保存、C 的确认步骤、应用失败保留候选、Cancel 保留已保存值，以及窄窗口布局。另已验证 B 的方向键滚动、首尾边界、Tab/Shift+Tab 与取消保留设置。该检查仅验证原型交互，不是生产代码或真实网络验证。用户已批准 B 精修版及滚动交互。

已批准的 B 精修版：左侧双行名称与状态、右侧详情，Saved 与 Not applied 分开展示；当前配置不被候选高亮冒充。打开时选中已保存网卡，无变化时 Apply 不可用；Tab 从列表进入操作区。底部合并变更与影响提示，不重复堆叠不可用信息。不可用候选仍可保存，失效、取消与应用失败语义遵循已确认规则。

方向键滚动原型：Automatic 固定在网卡滚动区上方，但仍参与 ↑/↓ 顺序选择；列表到首尾停止，不循环跳转。选中项始终可见，右侧详情及底部操作不随列表滚动；Tab 进入 Cancel，Shift+Tab 返回当前候选。该滚动区域按用户明确要求实现。

沿用现有 TUI 的圆角边框、主色标题和状态配色；居中显示，网卡列表可滚动，底部操作固定。候选高亮与已保存选择分开表达。所有内置界面文案保持英文，设备原始名称正常显示。

布局以已批准的 B 原型为准：顶部 Saved 名称与可用状态，左侧固定 Automatic 和可滚动网卡列表，右侧候选详情，底部变更摘要与 Apply / Cancel。原型 A/C 仅作历史对照。

- Available 只表达网卡自身状态，不表示通过互联网连通性探测。
- 上述名称与地址均为示意数据，未读取用户真实网络信息。
- 原选择失效时仍显示在 Saved 区域，并以醒目的不可用状态说明；不从列表中悄然消失。
- 紧凑终端保留 Saved、候选列表及固定操作，详情随空间收缩或滚动；长网卡名称可在详情区完整查看。
- 列表允许选择并保存不可用网卡，最终布局已通过；恢复行为按上述原生语义。

## 调查与文档规则

- 术语记录在根目录 CONTEXT.md；本文记录用户决定、未决问题及后续设计。
- 上游能力由代理只读调查；不要求用户回答可从源码或文档查明的事实。
- 事实核对、用户期望与已经实现的能力分别记录。不能把全局默认接口直接等同于全部流量的强制绑定。
- 本阶段产出文档与模拟原型，不修改运行配置、不运行真实核心或服务。Q1–Q24 已收敛，生产实施按关联计划开展；真实环境验证仍需另行授权。
