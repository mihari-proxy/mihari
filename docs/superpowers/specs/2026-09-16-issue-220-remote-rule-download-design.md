# Issue #259：订阅刷新超时与 AUTO 文案（grilling 草稿）

> 本文件仅保留历史范围调整与访谈记录，其中“待定”“尚未实现”描述访谈结束时的状态。当前方案及实施状态见 [Issue #259 修复设计](2026-09-16-issue-259-subscription-timeout-design.md)，具体预算、取消所有权、恢复与布局以该文档为准。

状态：用户要求关闭 #220，已以 NOT_PLANNED 关闭并关联后续议题。新建 #259 跟踪主订阅刷新超时修复及 AUTO 显示文案；具体预算与改名仍待定，无功能实现。

- 当前 Issue：https://github.com/mihari-proxy/mihari/issues/259
- 已关闭的原始讨论：https://github.com/mihari-proxy/mihari/issues/220
- 后续 provider override：https://github.com/mihari-proxy/mihari/issues/258
- 基线：`origin/dev`，`6bd40d18903cc796e2d2d808aa6c0774a81bbf7f`
- 分支：`feat/220-remote-rule-download`
- Worktree：`.worktrees/feat-220-remote-rule-download`
- 分支名和文档文件名保留已有名称；实际范围以 #259 修复设计为准。
- 已完成工作：创建工作区、只读调查、设计访谈、创建 #258 / #259、关闭并关联 #220；尚无功能代码变更或提交。

## 1. 当前范围与已确认语义

用户最新决定：sub 订阅下载模式与 provider 的 proxy override 应分开。目前阶段仅完成 sub 的代理模式，proxy-provider override 拆到 #258。

本阶段的对象是 Mihari 根据订阅 URL 下载的主 YAML，沿用现有每条订阅的 Mode：

- direct：使用直连下载路径。
- proxy：通过当前 mihomo 的常规代理入口，遵从当前核心模式、路由规则及节点选择；不让用户额外指定 selector/node，也不承诺最终出口必定是代理节点。失败不由 Mihari 自动改为直连。
- proxy w fallback：先尝试当前 mihomo 代理入口，代理尝试超时后对同一订阅 URL 直连重试。其他网络错误是否也触发回退、各阶段预算仍待讨论。

主 YAML 下载或校验失败时保留最后有效订阅缓存与运行配置。保持既有后续配置校验、应用与回滚边界，不在本阶段新增跨 provider 资源事务。

provider 的原生字段、下载渠道、后台 interval 以及由 mihomo 执行更新的当前职责保持现状；不让本阶段的 sub Mode 覆盖 provider.proxy。用户原先接受的多资源候选收集与整次资源事务，随统一 scope 撤回，不转化为本阶段实现要求。

## 2. 决策记录

| 编号 | 最新状态 |
| --- | --- |
| Q1：主订阅与全部 provider 统一下载 | 已撤回；仅主 YAML 下载进入当前范围 |
| Q2：Inherit / 配置覆盖 | 本阶段不新增 provider 覆盖状态，无需为此引入 Inherit |
| Q3：proxy 出口 | 保持：当前 mihomo 常规路由，不手动指定出口 |
| Q4：超时 | 待讨论；任何数值或全局客户端调整均未定稿 |
| Q5：一个 sub Mode | 保持：沿用现有 Mode，仅作用于主订阅下载，不扩展至 provider |
| Q6：provider 自主更新是否继承 Mode | 从本阶段移除；保持原生行为，后续 provider override 见 #258 |
| Q7：逐资源回退 | 收窄为同一个主 YAML 请求的代理→直连重试，不再涉及多资源下载 |
| Q8：整次失败 | 主订阅刷新失败保留旧有效状态；不扩成 provider 缓存集合的原子事务 |

## 3. 当前代码事实

- `internal/subscription/model.go:7`：已经存在 direct（内部零值）、proxy、auto，保存在订阅 Profile；`service.go:139` 将 Mode 传给订阅主 URL 下载器。
- `internal/subscription/downloader.go:57`：代理下载客户端连接 mihomo mixed-port；直连与代理客户端各有 30 秒 HTTP 超时。proxy 不自动直连；auto 对特定网络错误尝试直连。
- 当前 auto 的网络回退包括超时、连接拒绝和连接重置；HTTP 非成功状态不属于该回退路径。整体调用 context 已取消/超时会提前返回，不进行有效直连尝试。
- `internal/control/client/client.go:63` 与 `internal/mihomo/client.go:30` 分别默认 10 秒；TUI refresh 的 30 秒 context 不会覆盖客户端内置的 10 秒限制。时间预算须区分 URL 下载、IPC 等待及后续配置应用，不能把所有超时统称为代理超时。
- `internal/runtime/subscription.go:120`：先准备主订阅和候选配置，再提交缓存/catalog；活跃订阅 apply 失败有 receipt 补偿。现有边界不证明 provider 原生缓存也能一起回滚。
- `internal/subscription/generator.go:18` 保留原生 provider 字段；`internal/core/provider_native_integration_test.go:23` 明确 provider 网络工作归 core executor。
- 非活跃订阅 refresh 仍进行配置验证；live reload 只针对活跃订阅。校验/重载阶段可能涉及核心的 provider 行为，不属于本阶段主 YAML 下载模式控制范围。

## 4. Provider 调查结论（后续议题参考）

- 主配置订阅的周期刷新由 Mihari 调度；provider 内容下载及其独立 `interval` 更新由 mihomo 执行。
- HTTP rule-provider 与 proxy-provider 各自支持 `interval`，单位秒；`health-check.interval` 是健康检查周期，不是节点列表下载周期。
- 对运行中的核心，未指定 provider.proxy 时按当前核心 mode/rules 选择出口；显式 DIRECT 或代理组/节点优先使用指定对象。组内仍可选中 DIRECT。
- 核心初始化时内部 tunnel 未建立，HTTP 下载器存在底层直连分支；不能据此声称运行中的代理请求超时会自动直连重试。
- provider 的单个 proxy 字段或 fallback 代理组，不能直接证明具备按下载错误类型重试直连的语义。

这些事实已通过 Context7 查询官方文档，并以项目固定的 mihomo v1.19.30 源码核对：

- [规则 provider](https://wiki.metacubex.one/en/config/rule-providers/)
- [节点 provider](https://wiki.metacubex.one/en/config/proxy-providers/)
- [HTTPVehicle](https://github.com/MetaCubeX/mihomo/blob/v1.19.30/component/resource/vehicle.go)
- [Fetcher / 定时更新](https://github.com/MetaCubeX/mihomo/blob/v1.19.30/component/resource/fetcher.go)
- [HTTP 出站](https://github.com/MetaCubeX/mihomo/blob/v1.19.30/component/http/http.go)
- [内部入口](https://github.com/MetaCubeX/mihomo/blob/v1.19.30/listener/inner/tcp.go)
- [核心出口选择](https://github.com/MetaCubeX/mihomo/blob/v1.19.30/tunnel/tunnel.go)

## 5. 当前待定事项

- 现有 DIRECT/PROXY/AUTO 的显示命名与说明：用户要求在 #259 讨论是否将 AUTO 显示为 `PROXY w Fallback to DIRECT`；仅针对 Subs 的下载 Mode，不改 Proxies 页的 Routing Mode。CLI/JSON/catalog 原有值建议保持兼容。
- 回退仅限代理尝试超时，还是保留现有连接拒绝/重置也回退；DNS、TLS、EOF、HTTP、内容校验和取消的精确矩阵。
- 单次代理尝试、直连尝试及整个刷新操作的预算，以及 IPC/TUI 等待如何配合；避免代理超时后整体 deadline 已耗尽。
- 初始化添加、手动刷新与自动刷新的共用语义及安全失败提示。
- 方案明确后记录测试验收和契约影响，再完成 grilling 定稿确认。

## 6. 治理约束

沿用 daemon 单写入者与统一 mutation 路径。新增依赖、持久化、公开 CLI/JSON 或安全边界的具体变更，需依仓库规范先说明并确认。保留原工作区未提交改动，不修改 CHANGELOG。

日志遵循用户最新整体替换后的 AGENTS：文件日志保留原始 cause（包括错误自带的 URL 等），执行既有大小上限和导出提示；普通 DTO、事件、状态与 CLI 输出保护凭据。

规范引用的旧 `2026-08-03-mihari-architecture-design.md` 在当前分支不存在，采用现行 `docs/architecture.md`；历史 root provider 托管设计已声明移除，不作为本阶段的现行实现依据。
