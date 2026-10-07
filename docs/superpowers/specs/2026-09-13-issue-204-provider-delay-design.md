# Issue #204 provider 节点测速与 HTTP 诊断修复设计

日期：2026-09-13。
状态：用户已批准收缩后的设计，并授权执行计划、开发与测试。实施记录见 [执行计划](../plans/2026-09-13-issue-204-provider-delay.md)。
工作分支：investigate/204-provider-delay-20260913。

## 方案摘要

修复目标是让 provider-only 节点走正确的测速接口，并让所有 mihomo HTTP 失败留下可定位的原始诊断。已复现的直接原因是普通测速路由无法找到 provider-only 节点；同名节点是另一项名称歧义问题，不是触发该缺陷的必要条件。

| 领域 | 本轮交付 |
| --- | --- |
| 节点发现与测速 | daemon 读取全局节点与 provider 节点，统一补齐元数据并解析测速目标；provider-only 使用 provider healthcheck |
| 同名节点 | 按名称合并；全局普通节点优先，否则按 provider 名称排序取首个；TUI 每次启动最多提示一次 |
| Provider 读取故障 | 最多尝试 3 次，瞬时故障才重试；预算耗尽后返回可读的关键原因 |
| TUI 状态 | 保留旧列表并显示过期状态；首次失败显示错误空态；刷新恢复自动清除加载错误；全部产品文案为英文 |
| HTTP 诊断 | typed REST、gateway REST、WebSocket HTTP 握手均保留原始错误、真实上游状态与操作上下文；在日志边界脱敏和标识截断 |
| 验收 | fake controller 验证 provider 路由、固定候选、重试、状态恢复、原始诊断及公开输出安全边界 |

接口与时序细节见“完整实现方案”；按第 7 节分阶段实施。

## 修复前核对的事实

- 当前代码按节点名请求普通单节点接口，provider-only 节点在隔离复现中返回 404。
- provider 节点列表元数据也受只查询全局节点映射的假设影响。
- 现有 ProxyNode 仅含 Name/Type/UDP/XUDP；DelayTestRequest 没有 provider 字段，DelayResult 按节点名返回。TUI 的队列、去重、在飞任务和延迟状态也以节点名为键，跨组同名只测一次已有测试约束。
- 当前 Controller/Manager 没有 proxy-provider 读取或 provider 单节点测速方法；已有 RuleProviders 指规则提供者，不能替代 proxy provider。仅适配层已有的 UpdateProxyProvider 也不提供节点发现能力。
- 在保持现有公开字段的条件下，可以修复 provider-only 名称并补元数据；本轮保留按名称操作，采用已确认的全局普通节点优先规则，不实现完整跨来源身份协议。
- 上游非成功 HTTP 响应的状态保存在 APIError.Details.status；诊断格式化器只输出错误码。
- 2026-09-10 的 URL 回落和 TUI 限流设计已进入当前代码。此次缺陷不能通过重复调整默认 URL 解决；该旧文档对现场根因的描述属于当时判断，不代替 2026-09-13 的新证据。
- Zashboard/MetaCubeXD 都采用 provider 节点发现与专用测速路径，但仍有按名称合并的歧义局限。参见 [面板对照（历史版本）](https://github.com/mihari-proxy/mihari/blob/8661e5bd2f1eedb0dd5dff3ef4c8fd27b089b95d/projects/research-204-panels-20260913/report.md)。
- 现场日志缺少上游状态，不能断言其中 338 条简短错误全部为 404。参见 [调查记录](../../investigations/2026-09-13-issue-204-new-logs.md)。
- mihomo v1.19.30 的 Selector 列表只序列化成员名称，选择接口也只接收节点名；实际选择为组当前遍历顺序中第一个同名对象。provider 单节点测速则可以明确指定来源。两者不能被描述成同一种能力。参见 [Selector 源码](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/adapter/outboundgroup/selector.go#L50-L104)。
- AGENTS.md 指向的 2026-08-03 架构规格在当前 worktree 和原工作区均不存在；现行参照为 AGENTS.md、[docs/architecture.md](../../architecture.md) 与已有相关设计。既有诊断设计也记录了该旧路径缺失。

## 既有约束

- daemon/Manager 拥有业务编排，TUI/CLI 经本地控制协议访问；mihomo 适配层封装上游接口。
- 不改写用户订阅或配置以消除同名，不通过连接真实环境验证方案。
- 保留稳定 /v1 语义、JSON envelope 和 CLI 退出码；需要调整公开字段时，先形成具体兼容设计并取得用户确认。
- 日志保留原始报错与错误链，使用既有脱敏与长度限制；内部错误不进入用户输出，避免直接转储含凭据的请求/响应。

## 设计树

- Q1/Q3 修复范围（已决定，替代此前完整同名支持提案）
  - 本轮补齐 provider 节点发现、元数据与正确测速路由，不新增 provider 浏览页、完整跨 provider 同名操作能力或精确出口切换功能。
  - 已复现的 404 不依赖同名：单个 provider 的唯一名称节点，只要不在全局 /proxies 中，仍会因错误测速路由失败。
  - 跨来源同名节点概念上不同，本轮按 Q5/Q7 告警后合并处理。
- Q2 诊断范围（已决定）
  - 用户要求：本轮统一完善全部 mihomo HTTP 错误诊断。
  - 用户进一步要求：日志直接保留全部报错，不做摘要整理；只有面向用户的输出需要整理。
  - 日志保留原始错误文本及错误链，不能继续只记录 API 错误码。操作类别、上游状态（实际存在时）等可辅助诊断，但不能替代实际报错。
  - 保留既有凭据/完整订阅 URL 脱敏及长度限制；不在公开 Message/Details 中泄露内部 cause。错误链重复文本的处理、响应错误文本提取和截断标记需在具体设计中说明。
  - 覆盖 HTTP 非成功响应、传输、读取和解码等错误；具体字段、owner 与边界见完整实现方案第 6 节。
  - 不只修测速日志。WebSocket 握手、Web gateway 透传等与 HTTP 的交界需明确范围，不能假定全部由 Client.do 覆盖。
- Q4 版本兼容要求（已决定）：CLI/TUI 与 daemon 配套升级。不为旧客户端增加新功能兼容层；是否需要新增版本匹配校验、具体协议字段及版本策略另行设计，不能据此直接改变稳定契约。
- Q5 同名处理（已决定，替代跳过歧义项）：每次 TUI 启动检测重名；存在重名则弹窗告知服务可以继续运行，但无法保证同名节点选择来源的一致性。界面、任务与结果按名称合并，一次测速选择其中一个候选，不因为重名直接拒绝。实际候选选择规则见 Q7。
  - 用户表述中的“随机”不能写成对内核算法的事实描述：mihomo 取当前组顺序中的首个同名节点。告警应说明选中来源不可区分，以及测速来源可能与实际出口不同。
  - 检测基于 daemon 获取的内核节点数据；TUI 不读取订阅配置文件。文案称“当前加载的节点中存在重名”，不宣称扫描了磁盘配置。
  - 同一节点出现在多个组不等于多个来源的同名冲突，不能据组成员重复次数告警。
  - 此功能只合并 Mihari 的展示与操作，不修改内核配置，也不承诺内核把底层节点合并。
- Q6 provider 读取失败（已决定）：先重试，继续失败后向用户展示关键报错原因，日志保留完整原始错误及错误链；不只给笼统的“检查未完成”提示。重试与页面策略见 Q8/Q9。该要求针对读取失败，不自动扩展到测速、选择、更新等其他操作的重试。
- Q7 同名测速候选规则（已决定）：全局普通节点存在时优先测它；否则按 provider 名称排序，选择第一个包含目标名称的 provider。不得使用随机抽取或 Go map 遍历顺序作为选择依据。相同节点数据得到相同候选，但不承诺与代理组实际出口一致。
- Q8 重试策略（已决定）：最多 3 次（含首次），有限退避，设置每次及整个过程的时间上限；连接失败、超时等瞬时错误重试，认证拒绝或接口不支持直接反馈原因，取消立即停止。总预算耗尽时不强行补足 3 次。具体错误分类与时间参数在最终方案中列明。
- Q9 最终失败展示（已决定）：保留最后成功快照并标记过期，在 Proxies 页显示关键原因；首次失败显示错误空态。恢复后清除错误，不随轮询重复弹窗。
- 具体协议、刷新、时间预算、日志 owner 与验收见下文“完整实现方案”，一并交用户整体确认。

## 记录规则

术语见根 [CONTEXT.md](../../../CONTEXT.md)。修复范围见 [ADR 0003](../../adr/0003-provider-delay-repair-scope.md)，原 [ADR 0001](../../adr/0001-provider-node-identity.md) 已被替代；日志要求见 [ADR 0002](../../adr/0002-mihomo-http-error-diagnostics.md)。具体传输字段提案见完整实现方案第 4 节；范围确认不等同于对公开协议修改的确认。

同名告警与按名称合并的决定见 [ADR 0004](../../adr/0004-duplicate-node-name-warning.md)。

## 英文界面与启动告警文案

界面语言已确认统一为英文，覆盖页面标题、状态、错误摘要、时间说明、空态、弹窗、按钮和快捷键提示；本文中文用于设计说明，不直接作为产品文案。用户配置中的节点名、组名和 provider 名保留原文，不做翻译或改名；HTML 示意使用英文示例名称。

标题：Duplicate node names detected

正文：The loaded nodes contain duplicate names. Mihari displays and tests one entry per name. The tested node may differ from the node selected by a proxy group. The service can continue running.

确认按钮：Continue

具体弹窗触发时机与优先级见完整实现方案；不新增退出或阻止 daemon 运行的流程。

## Provider 读取失败反馈

用户已确认先重试、持续失败后报错。日志中的原始失败信息与用户关键提示分开：用户需要看到具体类别，例如连接失败、请求超时、认证失败、HTTP 状态或响应解析失败；内部堆叠文本、凭据和完整 URL 不直接进入提示。

一次读取失败不能被解释为“没有 provider”或“没有同名节点”。重试成功后继续正常处理；重试耗尽后按 Q9 保留旧快照并标记过期，后续成功刷新清除错误。

修复前核对：session 的初次和周期快照调用会丢弃 pollStatus 错误，ProxyGroups 失败不会产生携带错误的事件；root 仅在 EventProxies 成功时 SetGroups，因此最终失败当前没有用户可见原因。首次失败会落入无代理组空态，已有数据则停留在旧值。本轮需为代理快照最终失败接通错误反馈，不能只增加 daemon 日志。

session 默认每 3 秒发起轮询，单个 session 内串行执行快照请求；同一 session 不会因慢读取而并发启动另一次轮询，但慢读取会拖延后续快照和事件处理。control HTTP client 与 daemon mihomo HTTP client 默认都是 10 秒，Client.do 没有应用级重试。新重试必须纳入原有请求预算，不能每次另开一套不受取消控制的 10 秒等待。

## HTTP 诊断覆盖盘点

| 路径 | 修复前情况 | 设计需要处理的事项 |
| --- | --- | --- |
| internal/mihomo.Client.do | typed REST 调用的共用出口 | 非成功状态与响应报错；请求构造、传输、读取、解码和超限错误的原始原因；close 失败的处理责任 |
| internal/mihomo/stream.go | 独立 WebSocket 握手，绕过 do | 握手 HTTP 状态和原始失败；升级后的帧读取/关闭与 HTTP 范围区分 |
| internal/web/proxy.go | 浏览器 REST 通过 ReverseProxy，绕过 typed client | 传输失败有 ErrorHandler，普通上游 4xx/5xx 不会触发它；需覆盖后者，保留转发语义 |
| internal/web/server.go | WebSocket 使用自己的握手与 relay；部分写操作进入 Manager | 避免与 Manager/typed client 双重记录同一次失败；明确握手与 relay 边界 |

公开控制响应当前允许未知字段，常规请求使用 DisallowUnknownFields。配套升级要求减少新旧功能协商负担，但不自动允许破坏 /v1 契约。

## 实现职责

| 模块 | 责任 |
| --- | --- |
| internal/mihomo | 读取 provider 模型、执行 provider 单节点 healthcheck、保留 HTTP 原始失败 |
| internal/runtime | 组织目录读取与有界重试、解析固定候选、计算重名集合、编排测速 |
| internal/control/protocol、server、client | 定义安全响应增量，适配 Manager 结果，传播成功快照或失败 |
| internal/tui/session | 发布代理快照加载状态和最终错误，继续本轮其他页面的数据读取 |
| internal/tui root、pages/proxies | 一次性同名告警、旧快照保留、过期/空态/恢复、名称去重与英文文案 |
| internal/diagnostics、logging | 保留并输出受控原始原因，复用失败记录归属，完成脱敏与可识别截断 |
| internal/web | 覆盖绕过 typed client 的 REST/握手诊断，保持转发行为 |

- 在 mihomo 适配层增加 proxy-provider 读取模型和单节点 healthcheck 请求，复用现有受限 HTTP 客户端；内部接口按需要扩展，不让 TUI 接触 controller。
- 普通节点与 provider 节点使用同一套固定候选规则补齐元数据和选择测速目标，避免列表采用一个来源而测速采用另一个。
- 保持现有组测速、URL 回落、5 个在飞任务及按名称去重的行为；读取失败重试只作用于 provider 发现，不能自动重试有副作用的操作。
- 同名告警所需信息由 daemon 检测后返回。现有 ProxyGroups 没有适用字段，添加可选 duplicate_names 字段；具体 DTO 及缺省语义见第 4 节，已随本方案获批。
- TUI 会周期刷新代理快照；启动告警需要进程内待显示/已显示状态，不能每次刷新弹窗，也不能覆盖正在使用的确认框或安装提示。首次读取失败不消耗一次性同名告警机会。
- 不将“重试成功”与“检测无重名”混为一谈：只有完整、成功的数据读取才能产生可靠检查结果。

## 回归验收矩阵

| 场景 | 必须验证的结果 |
| --- | --- |
| 唯一 provider-only 节点 | 名称能显示，类型/UDP/XUDP 来自真实候选；测速请求进入 provider 专用接口 |
| 普通节点及普通/provider 同名 | 保持已确认的全局普通节点优先规则 |
| 多个 provider 同名 | provider 输入/map 顺序变化不改变排序后的选择；界面和测速任务只保留一个名称项 |
| 同一节点跨组出现 | 不误报为跨来源同名，保持原去重语义 |
| provider 读取先失败后成功 | 有限重试后恢复正常，不留下失败状态或错误的无重名结论 |
| provider 读取持续失败 | 用户看到关键原因，日志保留各次所需原始原因；次数/级别/owner按最终重试规则断言 |
| TUI 启动、轮询、重连与已有弹窗 | 同名提示按最终一次性规则出现，既有交互不被覆盖 |
| HTTP 状态、传输、读取、解析失败 | typed client 和 gateway 两条路径均保留真实诊断，公开响应不泄露内部原因 |
| 取消、超长错误、敏感错误文本 | 请求及时停止，长度限制可识别，凭据及完整URL脱敏；日志失败不递归写日志 |

行为变更按 Red–Green–Refactor 实施，优先目标包与 fake controller 集成；不执行真实环境测试。范围确定后分别验证日志通路、provider路由和TUI，再运行相关全仓检查。

## 完整实现方案

### 1. 节点读取与固定解析

Manager 组织一次节点目录读取，分别调用 mihomo 的普通节点与 proxy-provider 读取接口。成功时生成一次请求内使用的只读目录：原始全局映射、provider 候选、按名称解析后的展示元数据和重名名称集合。不把补充后的 provider 节点伪装成存在于原始全局映射中，否则将再次错误选择普通测速接口。

provider 模型只解析当前所需字段（provider 名称、vehicleType、节点列表及节点元数据），不读取或持久化用户 YAML。内核为内联节点/组生成的 Compatible provider 不作为独立来源重复计数；需要专门 fixture 验证同一节点的重复投影、同一节点跨组复用均不触发误报。

同名候选规则：

1. 名称存在于原始全局普通节点映射：选择该普通目标。
2. 否则按 provider 名称的字节字典序升序（不做 locale、大小写折叠），选择首个含同名节点的 provider。
3. 同一 provider 内若出现重复名称，遵循上游专用接口的首个匹配语义，并纳入重名提示；不宣称可以区分这些重复项。
4. 确认不存在时，返回既有类别的安全上游失败，不伪造 0ms、不遍历所有候选反复测速。

Type/UDP/XUDP 与测速使用同一解析规则。数据未变化时不受 map 遍历顺序影响。组顺序、组选择及原始组成员信息仍保留既有语义；TUI 的组内重复卡片和全量测速队列按名称去重，跨组共享同名结果，不将整个组结构折叠成一张全局列表。

### 2. 测速流程与刷新边界

单节点测速由 Manager 组织当前目标解析与请求执行：普通目标调用 /proxies/{name}/delay，provider 目标调用 /providers/proxies/{provider}/{name}/healthcheck。两段路径分别转义，不把 provider/name 字符串拼成未转义路径。

默认 URL 解析保留既有实现与优先级，不把额外重构列为交付条件：显式 URL → 全局目标自身 testUrl → 原有组遍历顺序中的第一个非空父组 URL → Mihari 默认 URL。不额外插入 provider health-check URL 作为新的优先级。默认内核 timeout 仍为 5000ms，既有显式 timeout 校验及组测速入口保持。

只在实际需要时复用读取结果，不为本轮增加缓存层。显式 URL 不再意味着可以省略目标发现：provider 路由仍需要当前目标信息。普通目标已确定时无需等待 provider 列表即可执行其测速；/v1/proxies 的完整目录刷新则必须读取两份数据后才发布新快照。

不引入跨请求长期候选缓存。同一次测速确定候选后只请求该候选：返回 404 时记录真实错误，不临时换到另一个同名 provider，下一次新请求再重新发现。两个上游读取不是原子快照，且内核不提供被本方案使用的配置 revision；本轮不能保证恰好在内核刷新期间的同名对象仍是之前的底层实例。

TUI 保留名称作为任务键、最多 5 个在飞测速、同名不并发。按当前名称合并语义显示结果，不扩展节点删除后的迟到结果处理或跨内核刷新身份追踪。

### 3. Provider 读取重试预算

采用内部时间预算，重试等待器可在测试中注入，不增加 settings：

| 参数 | 提案 |
| --- | --- |
| 应用级尝试上限 | 3 次，含首次 |
| provider 单次尝试预算 | 最多 1 秒，且不能超过剩余总预算 |
| 两次退避 | 100ms、200ms |
| 单次 provider 读取总预算 | 最多 4 秒，受调用方更早 deadline 约束；不收紧既有普通节点查询 |

发现与测速仍受既有请求总 deadline 控制；默认情况下给内核 5 秒测速留出空间。预算耗尽或取消后不补足次数，退避使用可取消等待，不扩大两个默认 10 秒 HTTP client 超时，不承诺显式 60 秒测速可突破既有外层请求限制。

重试连接拒绝/重置、每次尝试超时、EOF/截断读取、408、429、500、502、503、504。429 若带 Retry-After，在剩余预算内遵守；等待超过预算则直接返回最终失败，不在限流窗口内抢跑。父 context 取消/耗尽、401/403、404/405/501 等不支持接口、构造/编码错误、稳定的格式错误、超过体积上限不重试。错误分类基于类型/状态，不按任意错误字符串猜测。

这个重试只覆盖新增 provider 读取，不放入通用 Client.do 形成所有 GET/写操作隐式重试。一次完整读取内部的重试完成后，后续既有轮询仍可发起新的读取，不新增第二个后台重试循环。

### 4. 最小公开协议增量

在成功的 ProxyGroups 响应增加：

```go
DuplicateNames []string `json:"duplicate_names,omitempty"`
```

示例：

```json
{"schema":"mihari/v1","groups":[],"duplicate_names":["香港 01"]}
```

duplicate_names 是完整目录中重名名称的去重、有序集合；无重名时省略。读取失败继续返回既有错误 envelope，不发布缺少 provider 数据的新“成功”快照，因此省略字段不会被用于掩盖检查失败。集合只含已可向本地用户展示的节点名称，不含订阅 URL、controller 信息或内部错误。

上例仅展示新增字段形状；实际 groups 继续携带原有组 DTO，重名检测范围为完整节点目录，不依赖该节点是否出现在某个组。

不增加 provider 请求参数、复合 node ID、新端点、持久化字段或新错误码；DelayTestRequest 和 DelayResult 的名称语义保留。CLI/TUI 与 daemon 配套升级，不增加独立新旧功能协商层或强制版本比较。旧响应解码器可忽略新增响应字段，但不以此承诺旧 TUI 具备新告警能力。

现有公开错误码、CLI 退出码和外层 HTTP 映射保持；用户关键原因使用经审核的安全短文案。已存在的 Details.status 可直接用于提示上游 HTTP 状态，原始响应错误文本仅在内部诊断保留。duplicate_names 字段与安全文案变化已随收缩后的方案获批。

唯一公开 JSON 增量是成功响应的可选 `duplicate_names`。不增加 `Details.attempts`；实际尝试过程记录在日志，页面只显示最终失败原因。

### 5. TUI 过期状态与启动告警

session 只发布完整成功快照或带 Err 的最终失败事件；不新增刷新开始事件或逐次重试推送。HTML 中的重试次数仅为此前讨论示意，收缩后的生产页面只显示失败原因。

最终状态处理：

- 有成功快照：保留该快照，显示 `Stale data` 及英文安全失败原因。
- 无成功快照：显示错误空态，不能显示“没有代理组”。
- 后续成功：替换快照并清除加载错误/过期标记；不能顺便清除不相关的选择失败状态。
- 页面加载错误不按 3 秒轮询反复弹框。该次代理错误也不能阻止本轮后续 Rules 等无关快照尝试；只处理这个错误传播缺口，不泛化重构整个轮询系统。

旧快照仍允许用户按名称发起操作，由 daemon 对每次操作重新核对当前目标；界面不能把旧数据声明为实时状态。失败请求照常反馈，不使用旧 provider 映射绕过读取失败。

页面维护最后成功快照、成功接收时间和最终加载错误；这些都是 TUI 进程内展示状态，不持久化。最后成功时间仅在成功事件到达时更新；距今时长由本地时钟计算，不能拿最后一次请求时间冒充成功时间。已有失败之后的新刷新继续保留旧失败说明，直到收到成功结果再清除。

代理快照接收时间由 Proxies 页面独立维护；session 的 EventProxies 不填写流事件的 ObservedAt，避免轮询覆盖壳层最后一个 daemon 流样本的时间。流断开后的 stale footer 继续描述实际流样本。

| 状态 | 页面英文文案 | 数据行为 |
| --- | --- | --- |
| 成功 | `Up to date`、`Last updated` | 展示成功快照 |
| 失败且有旧数据 | `Stale data`、`Refresh failed`、`Last selected` | 保留旧列表与已有测速值 |
| 首次失败 | `Load failed`、`Unable to load proxy groups` | 错误空态 |
| 恢复 | `Up to date` | 替换列表、更新成功时间、清除加载错误；测速时间不变 |

过期标记的视觉方案已获用户确认，并按后续要求统一使用英文：页头显示 `Stale data`，下面用 `Last updated` 显示本 TUI 最近一次成功接收快照的时间与距今时长；分区错误区域给出失败类别，例如 `Refresh failed` 与 `Failed to load provider nodes: mihomo returned HTTP 503.`。列表保留，选中状态称 `Last selected`，历史测速值保持且用 `Last tested` 注明测速时间，不因列表刷新失败改成 `Failed`，也不在恢复列表时伪造新的测速结果。`Daemon connected` 与节点快照时效分别显示。采用默认分区提示与组级历史说明，不默认逐节点追加标签；预览中的紧凑提示和逐节点 `Previous` 标签仅为备选。交互示意见 [历史预览](https://github.com/mihari-proxy/mihari/blob/8661e5bd2f1eedb0dd5dff3ef4c8fd27b089b95d/projects/research-204-panels-20260913/proxies-stale-preview.html)，涵盖正常、重试、耗尽、首次失败及恢复五种模拟状态；不是生产界面。

同名告警以启动后的首次成功完整目录作为本进程检查结果。失败不消耗检查机会；成功无重名后结束本次启动检查，后续轮询不再触发新告警。成功发现重名时进入 pending，直到现有 modal/安装提示关闭再显示一次。重新启动 TUI 后重新检查。

文案使用前述英文同名告警，列出有界数量的名称及剩余数量，按终端宽度换行。控制字符净化复用 UI 安全渲染，不能让外部节点名注入终端控制序列。确认仅关闭提示，不停止服务或修改配置。

### 6. 全部 mihomo HTTP 错误诊断

覆盖 typed REST、Web gateway 的 REST 透传，以及两条 WebSocket 路径的 HTTP 握手。WebSocket 升级成功后的帧语义不扩展为本次新功能；已有握手/relay 报错应避免因共用机制而丢失原始原因或产生重复记录。

HTTP 边界保存有界的原始报错文本、错误链、实际响应状态（无响应时不伪造）、固定操作名称与失败阶段。错误包装仍只通过 Error() 暴露安全 API 信息；日志出口显式遍历内部 cause，不能只调用外层 Error() 后再次得到通用摘要。

对非成功响应，保留错误响应中的实际报错，不用固定“mihomo request failed”替代。成功响应解析失败保留解码错误本身，不转储整个成功配置/连接响应。未使用的 headers、请求 body、认证信息不整包写入日志。日志格式化保留原有错误细节，只做必要脱敏与有界处理；超出既有 4096 字节诊断限制时追加可识别截断标记，不能静默伪称完整。

原始文本输出路径须验证 JSON/文本中的 token、secret、密码、Authorization、Cookie、已注册凭据和完整 URL 的脱敏；短凭据和 JSON 带引号键也需要回归。内部错误对象不保存 request、response、context、logger 或无限增长 body。用户提示不读原始正文生成文案。

嵌入错误文本的 JSON 成员名按 JSON 转义语义识别，例如 `to\u006ben` 与 `token` 具有相同脱敏规则。只规范化已识别的敏感键并隐藏其值，保留其他文本的原始排版和既有非 JSON 回退；不重新序列化整份诊断正文。

记录责任：

- typed client 负责保留诊断，不在每层重复打印。Manager 的实际 mutation owner 保留最终失败唯一记录；未由 owner 处理的普通读取/测速由控制层最终出口记录。
- provider 读取中已决定继续重试的失败，在重试边界记录一次 WARN，保留该次原始原因；最后一次失败交最终出口记录 ERROR。成功恢复不再把前面的失败汇总打印一次。主动取消不制造 ERROR。
- gateway 直接透传的请求没有 typed client owner，由 gateway 记录最终 HTTP 或传输失败；进入 Manager 的请求遵守既有 AlreadyReported 标记。
- 非成功响应状态和 body 读取/关闭同时失败时保留各原因，不让关闭错误替换主因。业务已成功后的资源关闭问题只记录诊断，不把已完成的操作改为失败。

成功后的资源收尾诊断由实际持有 HTTP body 的边界记录 WARN，这一窄场景不能依赖不存在的失败返回路径触发上层 reporter。普通 HTTP 失败仍按上述 owner 分工处理。

typed client 与 gateway 的关闭阶段同样使用 FailureLevel 的取消分类：owner 已取消/截止且仅有取消原因时不记录；真实关闭故障仍为 WARN，不能因为请求已取消就丢弃与取消混合的实际错误，也不改写成功返回值。

ReverseProxy 的 4xx/5xx 不会自动走 ErrorHandler，需要独立观察。错误 body 通过有界的透传包装捕获，原字节仍原样交给下游；正常 EOF/关闭或中途失败后由唯一完成边界记录。不得为日志无限等待完整 body、吞掉已读前缀、修改 HTTP 状态、破坏流式读取或压缩响应。测试必须覆盖截断、慢 body、下游取消和透传字节一致性。

### 7. 实施顺序与完成条件

1. HTTP 诊断基础：先以错误状态/原始原因回归证明当前摘要缺口，再接通 typed client 与 gateway/握手覆盖，验证公开安全边界及唯一 owner。
2. Provider 发现与测速：先将现有 issue204 脚本触发条件写成正式单元/集成回归，再实现读取、有限重试、固定候选、元数据及专用路由。脚本当前以缺陷存在为成功条件，需要同步调整或明确保留为历史复现，不能把旧脚本成功当作修复验收。
3. 协议与 TUI：增加 duplicate_names、错误事件、过期/空态反馈和不覆盖既有 modal 的一次性同名提示；补齐名称去重和恢复测试。
4. 文档与审查：更新 README/中文说明、架构说明、既有测速设计的现状链接和相关界面说明；不修改 CHANGELOG。检查所有差异仅涉及本次范围。

每阶段按 TDD 先确认目标行为测试失败，再做最小实现。目标包包括 mihomo、diagnostics/logging、runtime、control/server、control/client、control/protocol、web、tui/session、tui/pages/proxies 与 tui root；跨包用 internal/integration 的 fake controller，不访问公网。

合并前执行相关包和集成测试、go test ./...、go vet ./...、格式及 diff 检查；日志共享状态/重试/TUI 生命周期有并发变更，需执行 race 检查。无法在当前 Windows 环境运行 race 时记录原因并由受支持 CI 验证，不能宣称通过。涉及公共装配的改动同时验证 Windows/Linux/macOS 的无 CGO 构建，目标遵守现有六平台发布矩阵。

用户已授权本设计对应的开发与测试；具体执行结果记录在执行计划。提交、推送、PR/合并与真实环境操作不在本次授权范围。

## 已批准的范围收缩

- 删除 `Details.attempts` 和专用刷新开始事件，页面只显示最终关键原因。
- 同名告警限定启动首次成功检查，不扩展运行期间新增重名提示。
- 不另修节点删除后的迟到测速结果；不把 URL 解析重构作为交付条件。
- 重试总预算只作用于 provider 读取，不改变普通节点查询的既有超时。
