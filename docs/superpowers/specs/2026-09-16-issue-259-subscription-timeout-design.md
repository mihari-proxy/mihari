# Issue #259：订阅刷新超时与 AUTO 文案修复设计

状态：用户于 2026-09-16 授权按设计和执行计划开发并提交 PR。本文预算、正文超时补齐和 TUI 文案已实施；验证和 PR 状态见执行计划末尾记录。

- Issue：https://github.com/mihari-proxy/mihari/issues/259
- 基线：`origin/dev` / `6bd40d18903cc796e2d2d808aa6c0774a81bbf7f`
- 工作区：`.worktrees/feat-220-remote-rule-download`；分支：`feat/220-remote-rule-download`。沿用已创建的隔离工作区。
- 前序范围记录：[设计访谈](2026-09-16-issue-220-remote-rule-download-design.md)。#220 已关闭；provider override 另见 [#258](https://github.com/mihari-proxy/mihari/issues/258)。
- 执行拆分及验证记录：[TDD 执行计划](../plans/2026-09-16-issue-259-subscription-timeout.md)。

## 1. 问题与目标行为

当前订阅主 YAML 下载已有 direct/proxy/auto。代理与直连 HTTP client 各允许 30 秒，但普通控制客户端固定 10 秒，TUI 刷新还套有 30 秒 context。用户在 Subs 选择 AUTO 后，如果代理迟迟不返回，交互请求可能在 10 秒时结束，daemon 随请求取消，无法等到代理自己的 30 秒超时并回退。

目标示例：代理尝试在第 30 秒超时，随后直连在第 35 秒成功，CLI/TUI 应继续等待校验与必要的重载，最终显示真实结果。后台自动刷新采用同样的下载和错误分类。用户主动取消或更短的显式调用方 deadline 仍优先。

本 Issue 只修复主 YAML 下载链路与相关等待/恢复路径。provider 字段、下载归属、下载出口、后台 interval 和原生缓存语义保持当前行为；不建立跨 provider 更新事务。不新增模式、依赖、配置字段、协议字段、CLI flag 或后台任务 API。

## 2. 现状证据

| 位置 | 当前事实 | 对方案的影响 |
| --- | --- | --- |
| `internal/subscription/downloader.go` / `newHTTPClient`, `Fetch`, `do` | 每次 30 秒；auto 对 transport 超时/拒绝/重置回退；成功响应的 body 读取错误直接包装为公开 APIError | 除 IPC 限制外，还须补齐 body 读取超时的同类处理 |
| `internal/control/client/client.go` / `New`, `requestHTTP` | 默认 10 秒；credential 模式仅复制 client 并禁用重定向 | 必须按具体长操作选择预算，不能原地修改共享 client |
| `internal/control/client/provider_client_unix.go` | Unix 已验证 IPC 入口也固定 10 秒 | Windows 与 Unix 构造路径都需覆盖 |
| `internal/control/client/runtime.go` / `doRuntimeOutcome` | credential 在派发前读取；派发后的失败有 dispatched/unknown 分类 | 新预算必须贯穿 token 读取、发出请求和读完响应，同时保持结果分类 |
| `internal/control/server/server.go` | 仅 ReadHeaderTimeout=5 秒；没有统一 handler WriteTimeout | 不用全局服务器超时改造处理本缺陷 |
| `internal/runtime/subscription.go` / `AddSubscription`, `RefreshSubscription` | Add 先持久注册，再隐式 Refresh；Refresh 准备后提交，活跃订阅 reload；自动刷新同样调用 Manager | daemon 边界统一封顶，覆盖隐式首次刷新与 scheduler |
| `internal/tui/pages/subscriptions/model.go` | Add 15 秒，单次 refresh 30 秒，批量使用条数乘 30 秒；均从 Background 派生 | 调整预算的同时为行刷新/批量补齐退出取消所有权 |
| `internal/mihomo/client.go` | 控制核心的 HTTP client 默认 10 秒 | 与主 YAML 下载不同；本阶段保留该请求预算 |
| `internal/runtime/subscription.go` / rollback 路径 | 普通路径复用原 ctx；trusted 路径使用 WithoutCancel；外层 routing 另有 15 秒恢复 | 明确内外两层恢复预算，不能声称只有一个 10 秒恢复阶段 |

Go 文档确认：`http.Client.Timeout` 包含连接、重定向和读取响应正文；request context 同样覆盖请求完整生命周期，较早 deadline 生效。因此只扩大其中一层不够。[Client](https://pkg.go.dev/net/http#Client) · [NewRequestWithContext](https://pkg.go.dev/net/http#NewRequestWithContext) · [WithTimeout](https://pkg.go.dev/context#WithTimeout)

文档检索遵循 Context7：先 resolve Go，再查询 `/golang/go/go1.26.0`；工具返回的源码片段指向官方主干，另查官方 API 文档核对稳定的 timeout/context 语义。实现继续使用项目固定 toolchain，不调整 Go 版本。

## 3. 推荐预算

| 层级 | 推荐值 | 范围 / 责任 |
| --- | --- | --- |
| 主 YAML 单次代理尝试 | 30 秒 | 保留当前完整 HTTP 请求预算，包括 body |
| 主 YAML 单次直连尝试 | 30 秒 | 单独开始计时；AUTO 最多两次尝试 |
| daemon 一次 Add / Refresh | 120 秒 | 注册（Add）、下载、准备、等 mutation、提交及首次 reload 共用上限 |
| 运行配置字节恢复 / 补偿 reload | 10 秒 | 只允许已开始事务的补偿，独立于被取消的请求；不是新下载预算 |
| 已有外层 routing 恢复 | 15 秒 | 保留现有上限；若与上述恢复依次执行，合计最多按 25 秒设计等待余量 |
| Add / Refresh 的本地控制请求 | 180 秒 | 留给 daemon 120 秒、两层恢复最多 25 秒及响应传输的余量 |
| TUI 单条 Add / Refresh | 同一个 180 秒策略值 | 生命周期可取消，替换原 15/30 秒；fake/替代 Client 也获得明确 deadline |
| 普通控制请求 / mihomo 控制请求 | 保持现状 | 不将所有请求统一放大至 180 秒 |

30+30 保留既有下载器行为，120 秒给最坏 60 秒下载之后的配置准备/应用留出空间；180 秒覆盖现有内外两层补偿并留出响应余量，避免客户端与 daemon 在同一时刻竞相超时。这些是推荐默认值，不新增面向用户的超时设置。

剩余时间按实际消费计算，不给各步骤自动重置整次预算。例如 Add 注册等待已消耗时间，内部 Refresh 继承 Add 的更早 deadline；调用方只给 5 秒时，不强行保证完整的 30+30。配置校验、锁等待也可能耗尽 120 秒，随后终止正常执行，不再发起直连或新 mutation。

时间上限针对可取消的网络/进程/等待工作；不能将普通文件系统调用描述为可在任意时刻强制打断的硬实时操作。恢复也应使用 context-aware 边界，不通过遗弃 goroutine 伪造超时。

## 4. 实现结构

### 4.1 Typed control client：只给 Add / Refresh 使用长预算

在 `AddSubscription` 与 `RefreshSubscription` 的类型化方法进入时建立默认 180 秒的子 context；更早的父 deadline 保留。由明确的私有请求选项把本次预算传入既有 doRuntime 链路，不依赖日志 operation 名推断，也不通过宽泛 URL 前缀放大其他接口。

本次请求从 `requestHTTP()` 取得 client 后再创建局部副本，选择匹配的 Timeout；保留 Transport、Jar、CheckRedirect 和现有认证行为。禁止修改共享 `c.http.Timeout`、Transport 字段或 provider/token。普通查询、设置、Use、流式请求继续走已有路径。

对 `NewHTTP` / 注入 transport 的客户端沿用同一类型化预算规则，测试用私有预算注入缩短等待；调用方要缩短单次等待使用 ctx deadline。自定义 transport 自身的更短超时仍有效，客户端不能承诺覆盖它。

必须保持：每个逻辑请求只读取一次 credential；Unix 不跟随重定向；mutation 不自动重放；派发前失败和派发后结果未知的判断不变；context 的 cancel 在响应 body 已完整读完并关闭后释放。不要增加自动重试 Add/Refresh 的 HTTP 请求层，下载器的直连回退不是重放 mutation。

### 4.2 Manager：所有刷新入口共用上限

在 `Manager.AddSubscription` / `RefreshSubscription` 进入执行链时设置 120 秒封顶，并在返回时 cancel。预算通过现有 context 传播到下载、验证、mutation 锁和 reload，不存储 context 到结构体。

Add → Refresh 的父子 deadline 取更早值，不能从子调用重新获得 120 秒。现有 operation ID / `-fetch` 子操作、执行去重和等待者独立取消保持；重复等待者不能替执行 owner 延时或取消 owner。

scheduler 通过相同 Manager 方法获得上限；不需要新 provider 定时器。完整刷新超时作为该次失败交给现有 scheduler 退避，daemon 生命周期未结束时不能误退出整个 scheduler。

预算常量放在各自 owner 附近，测试允许缩短；客户端可导出 internal 包内的 `SubscriptionMutationTimeout` 供 TUI 复用 180 秒，runtime 保有自己的执行/恢复上限。不新建通用 utils 或把时间策略塞进 `/v1` DTO。集成测试核对默认客户端预算大于 daemon 加两层恢复预算，防止再次漂移。

### 4.3 TUI / CLI / Setup / 批量刷新

- Subs 注入由 Run owner 提供的 context 工厂，创建从运行 ctx 派生的 cancel-only owner scope；页面存工厂函数和 CancelFunc，不新增保存 context 的结构体字段。单条 Add/Refresh 再显式套同一 180 秒子 deadline，以覆盖 fake/替代 Client。旧 `New` 的测试默认 owner 从 Background 派生，但每条请求仍套预算；批次 owner 不套 180 秒。其他编辑、查询、Use 的 timeout 不顺手改动。
- Add 复用现有 saveOperation/saveCancel；行 Refresh 可并发，按 operation ID 保存 CancelFunc；批量按 base ID 保存 owner cancel。所有 ctx 在返回 tea.Cmd 前创建并登记，正常结束释放，结果消息清理记录；Stop 幂等取消全部，排队但尚未开始的命令也不能逃过取消。
- `newModelWithClientContext` 将运行 owner 的工厂接给 Subs。`Run` 在 `program.Run()` 返回后、关闭 session/logger 前调用 Subs Stop，不能仅依赖当前晚于显式资源 cleanup 的 defer。复用现有关闭流程并测试取消传播，不建立新的通用任务框架。
- CLI 的 `sub add` 与 `sub refresh`：通过同一客户端自然获得长预算，保留 Ctrl+C / command context；不新增 flag。
- Setup 的订阅添加及失败后重试：已通过 beginExecution 从页面生命周期派生可取消 ctx，没有单独 30 秒限制；自然获得类型化客户端上限，保留取消后 15 秒 settlement。Setup 目前只有 Name/URL、默认 DIRECT，不增加 Mode 控件。其他 core/geo/setup 步骤不改变预算。
- 批量刷新维持当前顺序与首次错误即停止；批次 owner 只有生命周期取消，每条 item 新建 180 秒子 scope 并及时 cancel，不给整批套一个 180 秒上限，也不再用 `N × 30 秒` 截断。取消后不启动剩余条目，保留 operation ID 与 revision 递进。
- 不把传输断开或客户端 deadline 当作“服务器未保存”。现有 typed OutcomeUnknown 只覆盖 Add/Set；Setup 有通用 settlement，而 Subs 普通/批量 Refresh 与 CLI 当前仅报错。本阶段保留这些区别，不声称 Refresh 已具备自动结果核对，不增加持久结果协议或自动 mutation 重放。`/v1/operations` 的 finished 只表示 handler 结束，不证明业务成功。

### 4.4 下载器：正文超时也属于同一次代理尝试

保留请求前/请求中已有 timeout、connection refused、connection reset 分类。成功 HTTP 响应的正文若出现可识别的 timeout，且总 ctx 仍有效，AUTO 同样可以回退；保留 read 阶段的内部 HTTP cause 与静态公开错误边界。实现应最小补齐这一缺口，不直接将所有 body 读取错误标为可回退。

| 情况 | AUTO 是否直连重试 |
| --- | --- |
| 单次代理连接/等响应头超时，父 ctx 有效 | 是，保留现状 |
| 代理成功响应的 body 读取超时，父 ctx 有效 | 是，本次补齐 |
| 请求 transport 的 connection refused/reset | 是，保留现状 |
| body 的普通 EOF / UnexpectedEOF / 非 timeout 错误 | 否，不扩大范围 |
| HTTP 401/403/404/429/5xx，包括读取其错误正文时的超时 | 否；以已收到的 HTTP 失败为准 |
| 证书验证失败、非超时 DNS 错误、非法 URL、重定向策略拒绝 | 否，保持分类 |
| YAML 无效、超过 16 MiB、缓存校验失败 | 否 |
| 用户取消、整次操作 deadline 已到 | 否，结束本次执行 |
| PROXY 模式任意失败 | 不执行直连 |

每次请求分别关闭 body；失败正文/部分成功正文不能作为完整候选提交。直连成功才设置既有 `FellBack`，沿用诊断 owner 去重和恢复日志。不增加错误 DTO 字段、phase 状态协议或全量响应转储。

### 4.5 提交与恢复

主 YAML 下载、解析、候选校验失败发生在提交前，保留旧 catalog/cache/runtime 配置。延长客户端等待不会把原来失败改写为成功，也不会掩盖 mihomo reload 的独立失败。

Add 的注册已经持久化后，首次刷新失败仍按现有语义返回已注册 profile 与 last_error；不删除该条目或对用户诱导重复 Add。这是现存明确语义，不能误套成“所有 Add 步骤原子回滚”。

若已经替换运行配置而首次 reload 失败/取消，普通与 trusted 字节恢复/补偿 reload 使用 `WithTimeout(WithoutCancel(ctx), 10 秒)`，保留 operation 诊断关联，完成后 cancel。仅恢复先前有效状态；不能借恢复 context 继续下载、提交新版本或延长正常操作。缓存/catalog receipt 的回滚继续原路径；恢复无法确认则沿用 degraded 处理，不声称已恢复。

`routing_config.go` 的外层恢复已独立使用 15 秒；本次保留它，按内层 10 + 外层 15 的最坏组合预留客户端时间，不重构成共享恢复事务。覆盖 Routing 设置存在/不存在、普通/trusted 四种分支的取消回归，证明最终状态和上限。

这项补偿调整只覆盖本缺陷触及的订阅运行配置恢复路径。trusted 文件内部的既有同步恢复与其他业务事务不借机重构；若必要测试发现其会阻塞本边界，先把具体缺口补进设计，而不扩大成全仓取消治理。

## 5. TUI 文案方案

推荐只改订阅下载 Mode 的展示：

| 内部值 | 展示文案 |
| --- | --- |
| 空值 / direct | `DIRECT` |
| proxy | `PROXY` |
| auto | `PROXY w Fallback to DIRECT` |

完整文案 26 个 ASCII 字符。表单/详情/相关帮助使用完整文案；Mode 当前只有 6 列宽，不能只替换字符串后让它被截为 PROXY，造成模式无法区分。

列表在存在 AUTO 项时按完整 Mode 文案计算列宽，继续使用既有列优先级：先隐藏更新时间/流量等低优先级列，必要时整个隐藏 Mode 列。隐藏后仍可通过 Enter 在详情中查看/修改完整文案。列表不引入第二种缩写，也不让 Mode 的加宽挤掉更高优先级的 Name/InUse/Enabled/Status。

已按现有布局公式核算：72×22 时页面宽 58、列表预算 50，完整 Mode 需要前四列加间隔合计 69，因此整列隐藏；100×28 的列表预算 76，可显示完整 Mode，但流量/时间列隐藏。表单在 72×22 时文本宽 44，单行字段需要 46，会超出两格；这里将标签和值分成两行，沿用字段行号/滚动逻辑，完整值不能截成 PROXY。实际行为仍需实施时的布局测试验证。更小终端沿用已有最小尺寸处理。

聚焦 Mode 时显示简短说明：`Download subscription YAML via proxy; retry DIRECT on eligible network errors.` 保持 Enter next/Save、Esc、方向键/Space、列表 p 的原有行为。

CLI 仍接受 `--proxy direct|proxy|auto`，人类输出保持 `auto`；可补充帮助解释 auto。JSON `proxy_mode`、catalog `proxy-mode`、枚举值和默认 direct 全部不变。Proxies 页 `Rule / Global / Direct`、节点自动选择及 Auto refresh 开关不改名。

## 6. 测试与实施顺序

按 Red–Green–Refactor 分为五步，每步先证明行为缺失，再最小实现：

1. **IPC 预算回归**：客户端目标包先覆盖 Add/Refresh 比旧短预算长的等待、普通请求保持短预算、响应 body 全程受控、显式更短 deadline 与主动取消。覆盖静态 token 与 credential-provider 两种构造、禁止重定向/重放、同一 client 并发普通与长请求无污染。
2. **入口一致性**：runtime/TUI/CLI/Setup 测试覆盖手动、Add 首次刷新、重试、批量、scheduler。观察传给 fake 的 deadline/取消与调用次数；Add 子 Refresh 不重置父预算，单条超时不导致整个 scheduler 退出。
3. **完整下载尝试回退**：用隔离代理/源站或注入 transport，在响应头已成功后阻塞 body 直到单次超时，验证直连一次、失败 body 关闭、`FellBack=true`；同时覆盖父取消禁止重试和错误矩阵，不能仅制造连接超时代替 body 超时。
4. **集成与恢复**：本地控制 → Manager → downloader → fake 校验/reload 的跨包场景，证明代理超时后直连成功能返回客户端；两次失败不改变有效状态；提交后取消的恢复仍有独立上限；恢复失败仍 degraded；Add 已注册但拉取失败不会重复创建。
5. **文案与布局**：Subs 列表/新增/编辑/帮助的 AUTO 展示、72×22 / 常用宽屏布局、窄屏列隐藏、焦点和草稿保存；CLI flag、JSON 和 catalog round-trip 保持旧值。

测试使用可注入的小预算与事件/channel 控制服务器响应，不实际等待 30/60/180 秒，不使用固定 Sleep 维持时序；timer/context deadline 只用于要验证的超时行为与测试防挂。布局测试验证文案、可见宽度与交互结果，不只更新大快照。

实施时先目标包，再相关集成；随后 `go test ./...`、适用的 race、vet、修改文件 gofmt。公共客户端与 Unix 构造有改动时验证当前平台并进行受影响 CGO_ENABLED=0 跨平台编译。工具链、权限限制若使检查无法执行，按实际记录。

原设计轮仅产出方案；后续实施的实际测试命令、结果和测试组织调整记录在执行计划中。

## 7. 风险、兼容与定稿项

- 长等待延后的是无响应时的最终提示；TUI 事件循环保持响应，退出/生命周期取消和 Setup 的现有取消动作保持有效，Subs 保存中的 Esc 禁用规则不变，不新增单次刷新取消按钮。CLI Ctrl+C 保持有效。显示使用现有 Fetching/Working，不新增全链路进度协议。
- 120 秒总体预算是新推荐上限，极慢 provider 引发的候选校验仍可能超时；本 Issue 不承诺修复 provider 网络策略或其 10 秒 reload 请求问题。
- daemon 自设 deadline 若在下载之外以裸 DeadlineExceeded 返回，现有 server 会映射为 `internal` / `internal error`。本阶段保留既有公开分类、完整 cause 进入文件诊断，不新增 timeout code 或阶段 DTO；不能承诺所有超时都显示为 network_failure 或专用阶段提示。若要统一公开超时文案，应作为定稿追加项明确具体映射。
- 当前 direct client 克隆 DefaultTransport 时保留环境代理逻辑。此为邻近出站语义问题，本 Issue 不顺手改变；隔离测试显式控制环境，不能宣称已验证系统代理/TUN 下的物理直连。
- 无新增 schema/DTO/enum/退出码，不要求数据迁移。混用新旧客户端时旧客户端仍可能有短超时，文档建议 CLI/TUI 与 daemon 同步升级；不引入能力协商。
- 日志按最新 AGENTS 保留原始 cause，普通 API/状态/事件不带凭据；恢复与最终失败继续由既有 owner 记录。
- 本次采用：**30+30 / daemon 120 / 内层恢复 10 + 已有外层恢复 15 / IPC 与 TUI 单条 180 秒**；body 读取超时纳入回退；完整 TUI 文案及窄列表必要时隐藏 Mode。

后续用户已明确授权开发、提交 PR 和跟进 CI/bot review；不包含合并 PR 或真实订阅/服务操作。
