# Issue #259：订阅超时与 AUTO 文案执行计划

日期：2026-09-16。状态：开发及本地验证完成，准备提交 PR；下方任务清单保留原计划，实际证据与调整见第 3 节。

- 设计依据：[订阅刷新超时与 AUTO 文案修复设计](../specs/2026-09-16-issue-259-subscription-timeout-design.md)。用户后续已授权实施、提交 PR 并每 10 分钟跟进 CI/bot review。
- Issue：[#259](https://github.com/mihari-proxy/mihari/issues/259)。provider override 留在 [#258](https://github.com/mihari-proxy/mihari/issues/258)。
- 工作区：`.worktrees/feat-220-remote-rule-download`；分支：`feat/220-remote-rule-download`；沿用既有隔离工作区。
- 起始基线：`origin/dev` / `6bd40d18903cc796e2d2d808aa6c0774a81bbf7f`。执行前重新检查，不自动重置、rebase 或覆盖用户修改。
- 未获得合并或真实环境操作授权；不操作真实订阅、mihomo 或系统服务。

## 1. 固定范围与验收目标

只处理订阅主 YAML 的 Add / Refresh 等待链路、成功 HTTP 响应正文超时的回退缺口，以及订阅 Mode 的 TUI 展示。保留现有下载出口配置和 mihomo routing mode 行为。

| 层级 | 目标预算 | 约束 |
| --- | --- | --- |
| 代理 / 直连单次下载 | 各 30 秒，保留 | 每次覆盖响应正文；父 ctx 取消后不再回退 |
| Manager Add / Refresh | 整次 120 秒 | 注册、下载、准备、锁等待、提交、首次 reload 共用；嵌套不续期 |
| 配置字节恢复及补偿 reload | 独立 10 秒 | 只用于恢复；普通与 trusted 路径都受限 |
| 已有外层 routing 恢复 | 保留 15 秒 | 与内层顺序发生时按最多 25 秒留余量 |
| typed client Add / Refresh | 每次 180 秒 | 普通控制请求及 mihomo HTTP 请求仍保留原预算 |
| TUI Add / Refresh | 每条 180 秒 | 复用 client 策略值；批次 owner 只有生命周期取消 |

必须满足：代理单次超时后直连成功，CLI/TUI 能等到真实业务结果；主动取消仍传播；提交后失败可以在独立、有上限的恢复 context 中补偿。预算约束针对可取消的等待，不声称文件系统 IO 可被硬实时打断。

TUI 将内部 `auto` 展示为完整的 `PROXY w Fallback to DIRECT`（26 个 ASCII 字符）。窄列表可以整列隐藏 Mode，表单必须显示完整值。CLI 参数及人类输出、JSON `proxy_mode`、catalog `proxy-mode`、枚举和默认 DIRECT 保持兼容。

不引入新依赖、DTO、配置字段、错误码、CLI flag、后台任务 API、自动 mutation 重放或 Refresh 结果核对协议。不修改 `CHANGELOG.md`。不改变 provider 更新归属、interval、缓存和 override，不处理环境代理/TUN 导致的物理出口问题。

## 2. 执行约定与依赖

所有路径均相对上述 worktree。开始实施前阅读当前 `AGENTS.md`、`.github/CONTRIBUTING.md`、`README.md`、相关代码/测试及现存的 [架构说明](../../architecture.md)；检查目标子目录是否有补充规则。用户规则引用的历史架构文件 `docs/superpowers/specs/2026-08-03-mihari-architecture-design.md` 在本基线不存在，不能记录为已读；实施时若恢复该文件，补读其约束。

每项行为变更执行 Red → Green → Refactor：先添加能编译的行为测试，实际运行并确认因旧行为失败，再实现最小修复，最后运行关联测试。下文新测试名称是建议名称；不得以编译失败、只断言常量或只更新快照代替 Red。已有兼容性测试可直接作为保护，不要求人为制造失败。

| 任务 | 依赖 | 可交付结果 |
| --- | --- | --- |
| T00 | 无 | 工作区与基线核验 |
| T01 | T00 | Add / Refresh 专用 IPC 预算 |
| T02 | T00 | 成功响应 body 超时可回退 |
| T03 | T00 | 订阅恢复路径有独立上限 |
| T04 | T03 | Manager 整次执行预算 |
| T05 | T01、T04 | TUI 生命周期与逐条预算，CLI/Setup 入口核验 |
| T06 | T05 | 完整 Mode 文案及窄屏布局 |
| T07 | T01–T06 | 跨包回归和契约保护 |
| T08 | T07 | 完整验证与交付记录 |

推荐按表顺序执行；T03 必须先于 T04，避免先增加取消触发点却仍使用已取消的 context 恢复。

测试中的时限通过 owner 附近的私有配置/构造选项缩短，不使用可变全局变量，不为了测试扩大公开协议。用 channel、可控 body/transport 和 context deadline 建立时序，不固定 Sleep，也不实际等待 30/60/180 秒。集成层不为缩短测试引入生产公开测试开关；可组合可观察的默认 deadline 断言与可控超时错误，真实小预算到期留在对应包内验证。

## T00：核验工作区与现有行为

- [ ] 在当前 worktree 运行 `git status --short --branch`、`git diff --stat`、`git rev-parse HEAD`；确认不在 main/dev，记录既有修改。
- [ ] 核对设计中的入口、deadline、回退分类及恢复路径仍与代码一致；如基线有实质变化，先修订计划。
- [ ] 读取目标包现有测试，优先扩展已有 fake 和 fixture；不复制业务实现作为 fake。
- [ ] 记录相关包的测试/覆盖率基线，临时产物放临时目录或保持未跟踪。已有失败单独记录，不能算本次回归或顺手扩大修复。

验收：任务分支和基线明确；没有重置、清理或覆盖既有文件，没有真实订阅、mihomo、服务或用户配置访问。

## T01：Add / Refresh 使用专用 IPC 预算

目标文件：`internal/control/client/client.go`、`runtime.go`；核验 `provider_client_unix.go`。测试复用 `runtime_test.go`、`subscription_outcome_test.go`、`provider_client_unix_test.go`，新增同包 `subscription_timeout_test.go`。

**Red：**

- [ ] `TestSubscriptionTimeout_LongRequestsOutliveOrdinaryBudget`：Add 和 Refresh 在旧短预算之后才返回，仍成功；分别覆盖等待响应头和读取正文。通过小预算注入与事件控制完成，不等真实 10 秒。
- [ ] `TestSubscriptionTimeout_ParentDeadlineAndCancellation`：显式较短父 deadline 和主动取消优先，取消能到达阻塞 transport/body。
- [ ] `TestSubscriptionTimeout_CredentialReadSharesBudget`：预算从 credential 读取前开始，读取只发生一次；派发前超时不发送请求。
- [ ] `TestSubscriptionTimeout_ConcurrentRequestsKeepIndependentBudgets`：同一个 client 并发长请求与普通请求，普通请求仍受原预算约束，原 client Timeout/Transport 未变。
- [ ] 表驱动覆盖普通静态 client、credential-provider client 和 `NewHTTP` 注入 client；保留 Unix 禁止重定向、mutation 不重放及既有 dispatched/OutcomeUnknown 测试。

```console
go test -run '^TestSubscriptionTimeout_' ./internal/control/client
```

**Green / 最小实现：**

- [ ] 在 internal client 定义供 TUI 复用的 `SubscriptionMutationTimeout = 180 * time.Second`。
- [ ] typed Add / Refresh 创建并释放请求 context，早于 `requestToken`；私有请求选项沿既有 doRuntime 链传入预算，普通调用不变。
- [ ] 复制 `requestHTTP()` 返回的 client，只修改局部副本 Timeout；保留 Transport、Jar、CheckRedirect。不要修改共享 client、默认 transport、token/provider。
- [ ] cancel 覆盖响应正文完整消费与关闭；保留自定义 transport 的自身限制、既有错误分类和响应丢失语义。

验收：上述回归与 `go test ./internal/control/client` 通过；仅 Add / Refresh 获得长预算，认证和重放行为未改变。

## T02：补齐成功响应正文超时的 AUTO 回退

目标文件：`internal/subscription/downloader.go`、`downloader_test.go`；复用 `downloader_original_test.go` 的兼容性覆盖。

**Red：**

- [ ] `TestDownloaderAuto_BodyTimeoutFallsBack`：代理先返回成功响应头，body 阻塞至单次 deadline，父 ctx 仍有效；直连恰好一次并成功，`FellBack=true`，失败 body 已关闭，部分正文未被采用。
- [ ] `TestDownloaderAuto_ParentCancellationPreventsBodyFallback`：用户取消或父 deadline 到期后不发送直连。
- [ ] `TestDownloaderAuto_BodyErrorFallbackClassification`：普通 EOF/UnexpectedEOF/非 timeout 读取错误不新增回退；非 2xx 即使错误正文超时也不回退；PROXY 模式不回退。
- [ ] 保护已有 transport timeout/refused/reset、证书/非超时 DNS、URL/redirect、HTTP status、大小上限和无效 YAML 的分类；直连失败不能标记 `FellBack=true`。
- [ ] 内部诊断保留 read 阶段原始 cause，公开错误不暴露合成 token；校验所有响应 body 关闭。

```console
go test -run '^TestDownloader' ./internal/subscription
```

**Green / 最小实现：**

- [ ] 仅把成功响应的可识别 body timeout 包装为既有 `networkFailureError`，保留 `diagnostics.HTTPError{Phase: "read"}` 原因链。
- [ ] 沿用 Fetch 的模式/父 ctx 判断和现有直连调用，保留每次 30 秒和 16 MiB 限制。不把所有 body 错误统一变成可回退。

验收：目标测试及 `go test ./internal/subscription` 通过；没有新增 mutation 重试或 provider 下载逻辑。

## T03：先保证取消后的恢复可完成且有上限

目标文件：`internal/runtime/subscription.go`、`subscription_test.go`、`routing_config_test.go`；阅读 `routing_config.go`，保留既有外层 15 秒策略。可新增同包 `subscription_timeout_test.go` 存放本任务及 T04 测试。

**Red：**

- [ ] `TestSubscriptionTimeout_ReloadCancellationRestoresPreviousState`：候选配置替换后取消首次 reload，补偿接收仍有效且有 deadline 的 context，旧配置字节、订阅状态与必要的出口选择恢复。
- [ ] 表驱动覆盖 Routing 存在/不存在 × 普通/trusted 四条路径；检查诊断 operation 关联保留。
- [ ] `TestSubscriptionTimeout_CompensationIsBounded`：fake 补偿阻塞到独立小预算结束；调用返回，未遗弃 goroutine，无法确认恢复时沿用 degraded。
- [ ] 覆盖内层与外层都执行的情况：两层各有上限，不能错误断言整段恢复只耗时 10 秒；失败的候选不继续下载或重新提交。

```console
go test -run 'TestSubscriptionTimeout_|TestReloadFailureRollsBackSubscriptionActivation|TestRouting_FailedConfigReloadRestoresPreviousExit' ./internal/runtime
```

**Green / 最小实现：**

- [ ] 订阅普通/trusted 的配置字节恢复及补偿 reload 使用 `context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)`，退出时释放 cancel。
- [ ] 保持缓存/catalog receipt 回滚、外层 routing 的 15 秒恢复、degraded 和诊断 owner 语义。不改造成通用恢复框架，不扩展 trusted 内部其他同步事务。

验收：四种路径有可观察状态断言，独立恢复按上限停止；`go test ./internal/runtime` 通过。若底层未传播 context 导致无法建立此边界，记录具体阻塞点并修订设计，不能用后台遗弃任务假装有上限。

## T04：Manager 统一 Add / Refresh 整次预算

目标文件：`internal/runtime/subscription.go`、`subscription_timeout_test.go`；按必要性在 `manager.go` 放私有预算注入。测试复用 `subscription_test.go`、`subscription_edit_test.go` 和 `internal/subscription/scheduler_test.go`。

**Red：**

- [ ] `TestSubscriptionTimeout_ManagerBoundsExecution`：分别在下载、候选校验、mutation 锁等待和首次 reload 阻塞，观察共同 deadline 与取消传播；提交前失败保留最后有效配置。
- [ ] `TestSubscriptionTimeout_AddRefreshDoesNotRenewDeadline`：Add 注册与内部 Refresh 观察同一个更早截止时间，父短 deadline 也不能被续期。
- [ ] 保护 `TestAddSubscriptionKeepsProfileWhenFetchFails`：注册成功后首次刷新失败，返回已注册 profile/last_error，不删除条目或重复注册。
- [ ] owner 与重复等待者分别取消的回归：等待者不能延长或取消 owner；保留 operation ID、`-fetch` 子操作与既有去重语义。
- [ ] scheduler 的单次刷新 deadline 失败进入既有失败/退避路径，后续调度仍可执行；只有生命周期结束才退出调度。

```console
go test -run 'TestSubscriptionTimeout_|TestAddSubscription|TestRefreshCannotRecreateSubscriptionDeletedDuringDownload' ./internal/runtime
go test -run 'TestScheduler' ./internal/subscription
```

**Green / 最小实现：**

- [ ] Manager Add / Refresh 在执行链入口派生 120 秒 context，贯穿注册、下载、准备、锁、提交、首次 reload；通过父子 deadline 自然保留剩余预算。
- [ ] 预算保存在 owner 附近，私有可缩短；不把 context 存入结构体，不给每个步骤重新计时。
- [ ] scheduler 继续调用同一 Manager 方法；如现有错误分支把单次 deadline 误当生命周期结束，只修复此必要分支。

验收：`go test ./internal/runtime ./internal/subscription` 通过；既有 revision 冲突、删除期间晚到刷新、失败回滚仍成立。不新增公开 timeout 错误码；裸 deadline 的当前 `internal` 映射作为已知限制记录。

## T05：TUI 生命周期与逐条预算；核验 CLI / Setup

目标文件：`internal/tui/pages/subscriptions/model.go`、邻近 model/save/edit 测试；`internal/tui/model.go`、`run.go`、`run_test.go`。核验 `internal/cli/subscription.go` / `subscription_test.go`、`internal/tui/pages/setup/execution.go` / `experience_test.go`，仅在回归证明必要时改这些入口的生产代码。

**Red：**

- [ ] `TestSubscriptionTimeout_SubscriptionsRequestDeadlines`：Add、行 Refresh、每个批量 item 传给 fake Client 的 ctx 都有单条预算，包含尚未启动的 tea.Cmd。
- [ ] `TestSubscriptionTimeout_StopCancelsQueuedAndRunningCommands`：在返回命令后、运行命令前 Stop，及请求开始后 Stop，均能取消；多行并发请求全部取消，Stop 幂等。
- [ ] `TestSubscriptionTimeout_BatchUsesFreshItemBudget`：前一条已消耗时间，后一条仍获得新的单条预算；batch owner 本身不带单条 deadline。
- [ ] 批量首次失败/取消后不调用剩余项；保留 revision、operation ID 递进与原有冲突刷新行为；成功/失败消息按执行身份清理取消记录。
- [ ] 扩展 `TestRunShutdown_LifecycleOrder`：`program.Run()` 返回后，Subs 请求先取消，再关闭 session/logger；生命周期取消到达页面请求。
- [ ] CLI 的 Add/Refresh 传递 command.Context，取消后不重发 mutation。Setup Add 与重试继承 beginExecution 生命周期，取消后的 settlement 仍使用原 15 秒路径，不新增短 deadline。

```console
go test ./internal/tui/pages/subscriptions ./internal/tui/pages/setup ./internal/tui ./internal/cli
```

**Green / 最小实现：**

- [ ] Subs 接收由 Run ctx 派生 cancel-only scope 的工厂函数；旧 New 保留默认工厂以兼容已有调用。页面不新增 context.Context 字段。
- [ ] 单条 deadline 复用 client 的 `SubscriptionMutationTimeout`；Add 复用 saveCancel，行刷新按 operation ID 登记 cancel，批量按 base ID 登记 owner cancel。
- [ ] 创建/登记发生在 tea.Cmd 返回前，执行结束及时 cancel，消息清理对应记录；Stop 覆盖所有类别。不要因旧消息删除新操作的取消记录。
- [ ] 批次不再用 `N × 30 秒`；每条创建/释放子 context，取消后不继续下一条。
- [ ] root 注入工厂；Run 在资源清理前显式 Stop，保留原有关闭流程的幂等性。不增加通用任务框架或单次取消按钮。

验收：上述包测试通过；TUI 等待时仍可处理事件和退出；Setup 不新增 Mode 控件。保持 Add/Set 与 Refresh 的结果未知处理差异，不声称 Refresh 已实现 settlement。

## T06：完整 Mode 展示与窄屏布局

目标文件：`internal/tui/pages/subscriptions/model.go`、`dialog.go`、`form.go`、`form_layout.go`、`dialog_layout.go`；复用 model/form/layout/dialog_layout 测试及 `internal/tui/subscription_detail_test.go`。按需要更新 `internal/tui/ui/strings.go`、相关帮助测试、`docs/commands.md` 和 README 中对应段落。

**Red：**

- [ ] 更新 `TestProxyModeLabelRendersThreeStates` 并补充列表、新增、编辑/详情、聚焦帮助：AUTO 展示完整 26 字符，模式循环与保存结果仍为原始 `auto`。
- [ ] 根窗口 72×22：页面宽 58，表格预算 50，整列隐藏 Mode；Name/InUse/Enabled/Status 保留。不能只按全终端宽度测试页面。
- [ ] 根窗口 100×28：完整 Mode 可见，低优先级列按既有规则隐藏；宽屏也检查可见宽度。
- [ ] 72×22 的表单 Mode 标签/值分两行，完整值、箭头、焦点、滚动及保存/取消仍正常；宽表单保留单行布局。
- [ ] DIRECT/PROXY 无 AUTO 的列表不无谓保留 26 列宽。不可将 AUTO 截成 PROXY 或引入另一种缩写。
- [ ] 保护 CLI `--proxy direct|proxy|auto`、人类输出 `auto`、JSON/catalog round-trip、默认 DIRECT，以及 unrelated Routing Mode / Auto refresh 文案。

```console
go test ./internal/tui/pages/subscriptions ./internal/tui/ui ./internal/tui ./internal/cli ./internal/subscription
```

**Green / 最小实现：**

- [ ] 复用统一展示 helper；Mode 列依据当前内容计算完整宽度，保留列优先级 Name8/InUse7/Enabled6/Status5/Mode4/Traffic3/Last2/Next1。
- [ ] 表单窄宽度将标签与值拆行，接入既有 formFieldRows/滚动计算；不只替换字符串或扩大固定对话框。
- [ ] 聚焦 Mode 说明：`Download subscription YAML via proxy; retry DIRECT on eligible network errors.`
- [ ] 命令/用户文档解释 auto、等待与取消语义，并建议 CLI/TUI 与 daemon 同步升级。不改模式 token、Setup 表单、Routing Mode 或 CHANGELOG。

验收：布局断言覆盖完整文字、实际可见宽度、列隐藏及交互；不能仅更新黄金快照。文档不声称所有代理错误都会直连，也不承诺 TUN/环境代理下的物理直连。

## T07：跨包回归与兼容性核验

目标文件：新增 `internal/integration/subscription_timeout_test.go`；复用 `control_test.go`、`subscription_edit_test.go`、`setup_operations_test.go` 的本地控制/fake mihomo fixture。只使用临时目录、本地 IPC/loopback 与合成订阅。

- [ ] 本地 typed client → server → Manager → downloader → fake 校验/reload：成功响应 body 发生可控超时，直连一次成功，客户端收到正确 profile/有效状态。
- [ ] 在边界观察默认客户端 deadline 和 Manager deadline；核验客户端预算大于执行上限加内外恢复上限。与 T01–T04 的实际小预算到期测试共同证明时序，不只比较数字。
- [ ] 两次下载均失败：不改变既有有效配置、cache/catalog 的已提交有效数据；错误/last_error 按既有用例语义记录。
- [ ] 提交后取消：验证恢复成功的最终状态，以及恢复失败的 degraded；不能只断言调用次数。
- [ ] Add 已注册但初次获取失败：返回既有注册结果，不因请求层重试新增条目。
- [ ] CLI/TUI 相关链路不把响应丢失当作未提交；保护既有 Add/Set OutcomeUnknown，Refresh 不新增重放或结果查询承诺。
- [ ] Unix credential 路径与 Windows named pipe 的平台专用测试在相应平台执行；平台受限时记录未运行项。公共响应/事件无合成凭据，文件诊断遵守原始 cause 策略。

```console
go test -run 'TestSubscriptionTimeout_' ./internal/integration
go test ./internal/integration
```

验收：从客户端可观察到回退成功及失败状态；listener、body、goroutine、fake 进程均清理。测试不能访问公网或读取真实用户配置；不为集成便利暴露新生产 DTO/开关。

## T08：完整验证与交付

- [ ] 格式化修改过的 Go 文件，检查 `git diff --check`；确认无 provider、CHANGELOG、go.mod/toolchain 或无关变更。
- [ ] 目标包和相关集成通过后执行以下完整检查；已经通过的同一版本检查不无故重复。

```console
go test ./...
go test -race ./...
go vet ./...
gofmt -l .
```

- [ ] 对修改包核对覆盖率变化；若下降，用未覆盖路径和现有断言说明，必要时补真实行为测试，不添加无断言测试。
- [ ] `CGO_ENABLED=0` 编译 `./cmd/mihari`：Windows/Linux/macOS × amd64/arm64 六个目标。输出到独立临时目录，结束恢复原 GOOS/GOARCH/CGO_ENABLED；不提交二进制。
- [ ] race 运行使用平台支持的工具链；它与 CGO-free 发布构建分开。如当前 Windows 无 race 前提，明确未验证，交由受支持平台补验，不能把跨编译算作平台测试通过。
- [ ] 检查最终 diff、Markdown 本地链接、帮助/设计/实现一致性。更新设计中的状态及实际偏差，清楚区分已实现、已验证和受限未验证。
- [ ] 交付变更摘要、实际命令结果和限制。除非用户另行明确要求，不 commit/push/建 PR/合并，不操作真实环境。

最终验收：所有必要回归通过，CLI/TUI 可以等待代理超时后的直连结果，取消与恢复有明确 owner 和预算，AUTO 展示无歧义且持久化/协议兼容。

## 3. 实施记录

以下是 2026-09-16 的实际执行结果。清单中的测试名称是建议名，最终采用同等行为断言及已有兼容性测试；不把所有原建议名当作已新增的测试。

| 任务 | Red 命令及正确失败原因 | Green / 关联验证 | 状态与偏差 |
| --- | --- | --- | --- |
| T00 | 不适用 | 基线 6bd40d1；七个相关包基线测试通过；origin/dev 未变化 | 完成；用户其他 worktree 未修改 |
| T01 | `go test -run '^TestSubscriptionTimeout_' ./internal/control/client`：8 个长请求场景被普通短预算截断，credential 没有 deadline | `go test ./internal/control/client` 通过；追加 credential 实际到期不派发、父取消与 Unix 构造回归 | 完成；Unix 原生测试由 CI 对应平台执行 |
| T02 | `go test -run '^TestDownloaderAuto_Body' ./internal/subscription`：正文超时没有直连调用 | `go test -run '^TestDownloader' ./internal/subscription` 通过，后续全包/全仓普通测试通过 | 完成；真实小预算 body 到期、关闭、部分内容丢弃及错误矩阵 |
| T03 | `go test -run '^TestSubscriptionTimeout_' ./internal/runtime`：恢复继承取消并错误 degraded | 普通/routing 两种回归通过，core 中 trusted/routing 两种跨包 fixture 回归通过；独立小预算补偿到期测试通过 | 完成；旧诊断测试的“取消必须导致恢复失败”断言同步修正 |
| T04 | 同上：下载缺少 daemon deadline，校验/reload 无共同 deadline | runtime 包通过；下载/校验/锁/reload 实际到期、Add 不续期、独立等待者取消；scheduler deadline 后继续退避重试 | 完成 |
| T05 | Subs 目标回归：Add/行/批次仍是短预算，排队行/批次逃过 Stop | Subs/root TUI、CLI、Setup 相关包通过；批次确认取消也释放 scope；Run 返回后先 Stop 再创建资源 cleanup 已审阅 | 完成；CLI/Setup 生产代码无必要变更，补充入口取消测试 |
| T06 | Mode label、列宽、窄表单与帮助断言失败 | Subs 与 root 72×22/100×28 测试通过，相关黄金文件经对比更新 | 完成；帮助按 DIRECT/PROXY/AUTO 各自语义展示，保存的原始值不变 |
| T07 | 复用前述跨边界 Red 场景与现有事务测试 | `go test -run '^TestSubscriptionTimeout_' ./internal/integration` 及 integration 全包通过 | 完成；测试组织调整见下文 |
| T08 | 不适用 | 全仓普通测试、race、vet、独立缓存 lint、六平台 CGO0 构建通过；最后新增测试所在六个包 race 及全仓普通测试复核通过 | 本地完成；PR/CI/bot review 由线上状态跟踪 |

### 测试组织调整

- IPC 集成使用真实本地代理连接拒绝触发代理→直连链路，结合真实控制客户端、daemon、Manager、downloader 和 fake reload，验证成功回退、失败保留缓存、Add 注册保留及实际默认 deadline。成功响应 body 的实际计时到期单独在 downloader 包内用注入 HTTP client 验证；这样不等待生产固定的 30 秒，也不为测试扩大 Downloader 生产选项。两组证据共同覆盖回退分类和跨包编排，不声称 IPC 集成等待了真实 30 秒正文超时。
- trusted 跨包恢复测试复用 `internal/core` 已有的外部测试包与 test-only trusted fixture，覆盖真实 Manager→TrustedExecution，未新增生产 trust 构造器。
- TUI Run 生命周期由页面 Stop 回归、root owner 取消回归和 Run 中明确的资源关闭顺序共同核验；原有 shutdown 顺序测试继续通过。没有新增通用任务框架。

### 本地验证与覆盖率

- `go test ./...` 通过；后续新增测试所在包已重新通过。
- `go vet ./...` 通过。
- `golangci-lint run ./...`（v2.12.2，独立 GOLANGCI_LINT_CACHE）通过，0 issues。初次共享缓存有失效旧 worktree 路径警告，已用独立缓存复核。
- `gofmt -l cmd internal` 无输出，`git diff --check` 通过。
- `CGO_ENABLED=0 go build ./cmd/mihari` 的 Windows/Linux/macOS × amd64/arm64 六个目标全部通过；产物位于系统临时目录。
- `python -m pytest scripts/test/test_unix_layout_security.py -q`：74 passed，4 skipped；没有真实权限/账户/挂载操作。
- 对照同一基线临时只读 worktree，`go test -cover`：client 85.5%→85.5%，runtime 76.1%→76.3%，subscription 82.8%→83.5%，Subs 87.5%→87.8%，root TUI 85.5%→85.5%。基线 worktree 核验无变更后已移除。
- `go test -race ./...` 通过；Windows subscription 包耗时约 484 秒，无竞态报告。
- PR、CI 与 bot review 的最终结果由 PR 的当前 head 检查记录证明；未进行真实订阅或服务验证。
