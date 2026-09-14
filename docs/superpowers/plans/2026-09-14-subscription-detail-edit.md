# 订阅详情内编辑与 URL/缓存分离执行计划

日期：2026-09-14
状态：T01–T10 实现与验证已完成；按后续授权提交至 dev PR 并跟进 CI 与 bot review。
设计：[已定稿设计](../specs/2026-09-14-subscription-detail-edit-design.md)
分支：`feat/tui-subscription-detail-edit`
工作目录：`.worktrees/feat-tui-subscription-detail-edit`
基线：最初 `6221d69`；开发前已 rebase 到 `origin/dev` @ `a80a9b6773576d9bce7b4886b7e7d85a8f3b104e`。

## 1. 执行范围与前置条件

用户在设计与执行文档确认后授权“rebase dev 然后开始开发”。已完成 rebase 与下述实现、测试、编译和文档。用户随后明确授权 commit、push、创建指向 dev 的 PR，并处理 CI 与 bot review 直至通过；GitHub Actions 每 10 分钟检查一次。合并仍需另行确认。

实现以设计正文和 §2.1 的 G1–G12 为准。本计划把已确认行为拆为文件、回归测试和提交边界，不重新选择产品方案。

- daemon 是订阅业务唯一写入者；TUI 仅经 `internal/control/client` 调用本地 named pipe / UDS。
- 保留旧缓存、InUse 和运行配置，直到新源刷新成功；URL 保存不隐含下载、reload 或另选 ActiveID。
- interval 实际变化立即置过期标记，直到成功刷新；保留更高 Status 优先级。Next 独立重算，auto-refresh 关闭显示 Manual。
- URL reveal 沿用既有认证，所有已认证本机用户可读取；不增加 owner/admin 限权，不挂载 Web gateway 路由。
- 已批准新增持久字段、公开布尔与只读 reveal 路由；保持现有 CLI 参数、JSON 错误 envelope 和退出码。不新增本功能能力标记、不保证混用版本或直接降级读取。
- 所有 TUI 自有标题、字段、提示、错误、帮助、进度和按钮均为英文。用户自定义订阅名称等数据不改写或翻译。
- 不新增依赖、不调整 Go/toolchain、不改系统服务、真实配置、真实订阅、真实 mihomo 或跨平台支持范围。
- 不修改 `CHANGELOG.md`，不安装 hooks；仅按本任务后续授权创建提交、推送与 PR。

开始实施时先复核 `.github/CONTRIBUTING.md`、`AGENTS.md`、README、设计和目标文件附近的规则。仓库规则引用的 `2026-08-03-mihari-architecture-design.md` 在该基线缺失；当前架构依据为 `docs/architecture.md`、`docs/unix-layout.md` 与已定稿设计，不创建替代历史文件。

本 worktree 尚无 `.codegraph/`，直接查看其文件；不要自动建立索引。若后续用户为该 worktree 建立索引，遵守 CodeGraph 优先规则。库 API 细节需要查询时使用项目规定的 ctx7 流程；本计划不升级或重新选用 UI 库。

## 2. 基线代码落点与执行风险

| 边界 | 当前落点 | 计划必须处理的差异 |
| --- | --- | --- |
| Catalog | `internal/subscription/model.go`、`catalog.go` | 增加三个持久字段；`fillDefaults` 当前在已有 ActiveID 时提前返回，补缓存来源必须在此前覆盖全部条目 |
| URL/interval 更新 | `internal/runtime/subscription.go` 的 `SetSubscription`、`mutateSubscription` | 当前 URL 更新清 generation/time/ActiveID；改为保留缓存、复位调度、清旧源错误，不能触发 ActiveID 变化造成的 reload |
| 刷新 | `internal/subscription/service.go`、`internal/runtime/subscription.go` | 成功已有 version 校验；`noteRefreshError` 目前只按 ID 写回。准备、提交、回滚的失败与新状态字段必须一起闭合 |
| 自动调度 | `internal/subscription/scheduler.go`、`internal/app/runtime.go` | `Due`、实际 `due` 都读取 UpdatedAt；Run 另有 failures/nextAllowed，旧缓存值不能抵消本次调度复位 |
| 协议投影 | `internal/control/protocol/subscription.go`、`internal/control/server/subscription.go` | PublicProfile 与 protocol.Subscription 分别映射，不能只改一层；新 reveal 不复用默认 show |
| 操作跟踪 | `internal/control/server/subscription.go`、`operations.go` | Add 和 refresh/use 已有跟踪；PATCH `updateSubscription` 尚未调用 operations.begin，须为保存核对补齐 |
| TUI 表单 | `internal/tui/pages/subscriptions/form.go`、`model.go` | 当前最后输入框 Enter 即提交，提交前关闭表单；改成独立 Save 焦点与请求生命周期 |
| TUI 根层 | `internal/tui/model.go`、`session/`、`ui/focus.go` | 已有连接事件和确认请求；订阅页须接收重连信号，确认取消必须通知正确 owner，迟到结果不得串弹层 |
| 状态/帮助 | `internal/tui/ui/table.go`、`strings.go`、`keymap.go` | 增加居中；替换订阅页列名和状态；其他页面的 ModeDetail 与通用字符串用途不能被误改 |

已存在的回归基础包括 `TestCatalogRoundTripAndPublicRedaction`、`TestNotModifiedRetainsCacheAndAdvancesMetadataVersion`、`TestRollbackRefreshRestoresCatalogAndCache`、`TestSubscriptionRefreshAndOfflineSwitch`、`TestReloadFailureRollsBackSubscriptionActivation`、`TestSubscriptionResponsesCannotEncodeURL`，以及订阅页的 form/model、根层 help 和 table/keymap 测试。保留仍然有效的断言，按新契约替换旧行为断言，不能只删除旧测试让全包通过。

## 3. 状态与接口实施约定

### 3.1 Catalog 与公开投影

| 内部字段 | 持久键 | 公开字段 | 更新规则 |
| --- | --- | --- | --- |
| `CacheURL string` | `cache-url,omitempty` | 不公开原值；仅 `CacheOutdated` / `cache_outdated,omitempty` | 初次成功缓存或刷新成功绑定实际请求源；读旧缓存缺省时补当前 URL |
| `ScheduleFrom time.Time` | `schedule-from,omitempty` | `ScheduleFrom` / `schedule_from,omitempty` | URL 或本条 interval 字符串实际变化时写入同一次提交的 UTC 时间；成功刷新清零 |
| `IntervalRefreshRequired bool` | `interval-refresh-required,omitempty` | `IntervalRefreshRequired` / `interval_refresh_required,omitempty` | 仅 interval 实际变化置 true；成功刷新清 false；失败、Use、重启不清 |

`CacheOutdated = 有有效缓存 && URL != CacheURL`。URL A→B→A 可以解除来源不一致，但不能凭 ScheduleFrom 非零伪造 interval 变更。interval 改回原字符串仍保留待刷新标记。旧目录的标记默认 false；正常省略字段不抬高 schema 版本，也不放松严格 YAML 解码。

普通 list/show、事件与公开错误不含 URL 或 CacheURL。文件日志按用户确认沿用 dev 原始错误策略，不恢复 redactor；reveal 响应不主动写日志。专门的 reveal 响应只返回设计约定的 schema 与当前 URL，不返回 CacheURL。时间字段零值的 JSON 编码沿用项目约定，测试不能假定 time.Time 的 omitempty 一定省略零值。

### 3.2 调度与缓存年龄

调度基点取非零 ScheduleFrom，否则 UpdatedAt；两者都为零时保持首次立即到期。实际调度继续添加既有 jitter，纯 Due 查询沿用其不加 jitter 的职责。Next 展示名义到期时间，不承诺精确到秒，不能因强制 Expired 而显示立即重试。

保留既有 retry backoff，但它应绑定产生失败的调度状态。URL/interval 改变形成新 ScheduleFrom 时，旧 nextAllowed/失败计数不能把下一次执行拖到原来的长 interval；不能通过“任意 profile.Version 变化都清 backoff”实现，否则改名也会改变重试行为。成功后的下一次等待从实际成功提交后的 catalog 重新推导，不能用下载前的时间与旧 interval 覆盖新状态。

Status 从高到低：本地操作中 → Disabled → Failed → Missing → Outdated → interval 标记导致的 Expired → 自然到期且 auto on 的 Expired → 自然到期且 auto off 的 Need refresh → Live/Cached。Next 在 auto off 时显示 Manual；其他已禁用/尚无缓存的既有展示按设计条件保留，避免旧 LastError 或旧 UpdatedAt 盖住仍未到期的新 ScheduleFrom。

### 3.3 UI 数据所有权

区分最新只读订阅快照、打开时编辑基线、用户草稿、本次冻结提交请求及异步请求身份。使用具体类型，不用无语义 map 保存字段状态。

- 字段最终值与编辑基线比较，构造 PATCH 的非 nil 指针；URL 未读取且未编辑仍为 nil。false、空 interval、DIRECT 对应的空 proxy_mode 是明确值，不能因零值而省略真实修改。
- `URL touched` 与“最终值是否不同”分开：用户编辑后删空不能再被 reveal 回填；主动空 URL 校验失败。
- 弹层实例 ID、订阅 ID、operation ID、连接代次分别守卫对应回调；不能只用当前焦点判断回调归属。
- context 由生命周期 owner 传递，不保存到结构体。新增查询使用有界 context、可取消等待；TUI Update 独占页面状态，命令只返回不可变消息。
- 关闭弹层清除 URL 草稿和该弹层的读取引用，不输出请求或响应正文；已经发送的保存由原 owner 收尾，不把关闭当成回滚。

## 4. 任务顺序与 TDD

全部任务初始未完成。每个行为先补测试、运行对应最小范围，确认 Red 来自行为断言，再写最小实现，运行目标包，最后重构。新增类型可先加最小字段/签名让测试编译；编译错误不算 Red。下文新增测试名称是建议名称，执行时记录真实名称与结果。

### T01：Catalog 字段、默认补齐与公开投影

文件：`internal/subscription/model.go`、`catalog.go`、`catalog_test.go`；可新增 `cache_state_test.go`。

- [x] Red：构造旧 YAML，含已有效 ActiveID、另一条有效缓存与一条无缓存记录；读取后所有有缓存条目的 CacheURL 都应补齐，ActiveID 不变。
- [x] 实现三个持久字段与两个公开布尔/一个公开时间；公开投影不得携带任何源 URL。
- [x] 新旧格式往返读写、Clone、未知字段拒绝、显式 false、空 interval、无缓存不误报 Outdated；旧文件首次补默认后再次保存/读取稳定。
- [x] 将缓存默认补齐置于 ActiveID 提前返回前，但不改变其他启动时合法缺省选择规则。

最小测试建议：`TestSubscriptionCacheState_LegacyCatalogWithActiveID`。

```console
go test ./internal/subscription -run '^TestSubscriptionCacheState_'
go test ./internal/subscription
```

完成证据：新字段往返正确、旧目录无批量 Outdated/Expired、公开 JSON 不含两种 URL。

### T02：URL 与 interval 更新事务

依赖：T01。文件：`internal/runtime/subscription.go`、`subscription_test.go`；可新增 `subscription_edit_test.go`。时间从订阅用例的可注入时钟取得，避免测试依赖真实时间。

- [x] 首个 Red：A 正在使用旧缓存，B 也有缓存；修改 A 的 URL 后 A 仍 active、generation/UpdatedAt/cache 字节不变、controller 没有 reload。现基线会清缓存标记并可能切到 B，应在行为断言上失败。
- [x] URL 实际变化清 ETag、LastModified、LastError，保留 CacheURL，设置 ScheduleFrom；非 InUse 修改不得改变当前 active。
- [x] interval 实际变化写 ScheduleFrom 并置 IntervalRefreshRequired；不改 UpdatedAt、缓存或 ActiveID。空↔显式和 12h↔等价时长字符串按字符串变化处理。
- [x] 单次 PATCH 同时修改 URL/interval 只取一次提交时钟；输入原值、仅改 Name/Mode/Auto refresh/Enabled 不复位调度；全局 interval 保持既有行为，不伪造本条 interval 变更。
- [x] 验证校验拒绝、保存失败、revision 冲突、相同 operation ID 重放均不产生部分状态；改名失败不得顺带清旧错误。
- [x] 删除旧的 TUI “update 后 !Cached 即清 ActiveID”推断需在 T06/T08同步校正；daemon 返回是权威。

最小测试建议：`TestSubscriptionEdit_URLPreservesActiveCache`、`TestSubscriptionEdit_IntervalResetsSchedule`。

```console
go test ./internal/runtime -run '^TestSubscriptionEdit_'
go test ./internal/runtime ./internal/subscription
```

完成证据：任何 URL/interval 保存均不下载，不因该保存改变正在使用的配置；catalog、内存状态与 revision 一致。

### T03：刷新成功、失败与回滚闭合

依赖：T01、T02。文件：`internal/subscription/service.go`、`service_test.go`、`internal/runtime/subscription.go`、`subscription_test.go`、`config_diagnostics_test.go`；按需新增 `subscription_refresh_state_test.go`。

- [x] Red：暂停 A URL 的下载，在另一 mutation 保存 B URL，再释放旧请求为错误；新条目不得出现 A 请求的 LastError。使用 channel 暂停点，不使用 Sleep。
- [x] PreparedRefresh 绑定源/profile version 与必要的缓存身份；成功和失败发布都检查身份。删除、编辑或更新代次后，旧成功/失败不得写回；错误状态持久化失败保留原始故障与安全诊断，不吞掉关键恢复失败。
- [x] 新 URL 拉取不带旧条件请求头。若不一致来源仍返回 304，不能把旧源缓存认作新源成功；有效同源 304 必须验证缓存存在并有效。
- [x] 成功提交更新 CacheURL、UpdatedAt、generation/元数据并清三类状态：LastError、ScheduleFrom、IntervalRefreshRequired。有效 304 不增加 generation，但推进成功元数据并清标记。
- [x] 当前 active 刷新必须通过候选校验和 apply，之后才能报告成功。普通与 trusted 路径均把新字段纳入 receipt 和回滚；仅下载成功不足以解除 Outdated/Expired。
- [x] 为下载、解析、prepareConfig、cache/catalog 写入、apply、catalog/cache restore、reload 补偿分别注入失败：可恢复失败保留旧 yaml/source/time/active/调度标记，并记录本次有效失败；回滚不能确认则保持既有 degraded 语义。
- [x] 失败写回复用 Manager 的 mutation 编排，不在已有锁内重新进入同一锁，不让读取/网络/进程等待新增到提交临界区，不扩展通用事务框架。
- [x] Outdated 时 Use 仍加载旧缓存，不发网络请求，不清 interval 标记。保持基线已实现的 routing/GLOBAL 恢复行为。
- [x] 成功执行 owner 及最终失败 owner 的诊断只记录一次；陈旧失败、正常取消不伪装成新的当前源错误。

最小测试建议：`TestSubscriptionRefreshState_StaleFailureAfterURLEdit`、`TestSubscriptionRefreshState_CommitAndRollback`。

```console
go test ./internal/subscription ./internal/runtime -run '^TestSubscriptionRefreshState_'
go test ./internal/subscription ./internal/runtime
```

完成证据：按设计列出失败矩阵结果；旧缓存、运行配置、metadata、revision 与 degraded 的断言均有覆盖。

### T04：调度复位与 backoff

依赖：T01–T03。文件：`internal/subscription/scheduler.go`、`scheduler_test.go`；核对 `internal/app/runtime.go`、`scheduler_diagnostics_test.go` 的适配，仅按需要改动。

- [x] Red：UpdatedAt 已过期，但 ScheduleFrom 刚重置；Due 和实际 Run 均不得立即调用 Refresh。
- [x] 两个到期计算入口统一使用新基点，保留 enabled/auto-refresh 过滤、有效 interval 与 jitter。
- [x] 建立已有成功等待/失败 backoff 后，将 interval 从长改短；新的到期时间不再受旧 nextAllowed 拖延。反向短改长不得早拉；仅改名不清 backoff。
- [x] Run 每次触发前复核候选仍到期，避免已收集 dueIDs 后、另一个条目下载期间发生编辑，仍按过期列表拉取。准备后发生的竞争由 T03 的身份保护兜底。
- [x] 覆盖刚改 URL、同时改 interval、无成功缓存但已有 ScheduleFrom、重启重建调度器、auto off/on、disabled/enabled、手动成功刷新后调度与元数据一致。
- [x] 保留 retry 上下限、取消退出与后台错误 owner；以注入 Now/After/Jitter 驱动测试，不引入常驻额外 goroutine。

最小测试建议：`TestSubscriptionSchedule_ResetDefersExpiredProfile`、`TestSubscriptionSchedule_ResetReplacesOldWait`。

```console
go test ./internal/subscription -run '^TestSubscriptionSchedule_'
go test ./internal/subscription ./internal/app
```

完成证据：Next 名义到期与实际调度公式一致，旧等待不阻止复位，过期标记不等于立即网络拉取。

### T05：本地协议、reveal、操作跟踪与保密

依赖：T01–T03。文件：`internal/control/protocol/subscription.go` / `_test.go`、`internal/control/server/subscription.go` / `_test.go`、`operations_test.go`、`internal/control/client/runtime.go`；新增邻近 reveal/client 测试；`internal/runtime/subscription.go` 的只读用例及 `refreshSubscriptionLogSecrets`。

- [x] Red：已认证 GET `/v1/subscriptions/{id}/url` 返回约定 schema+URL，list/show 仍不带 URL；当前无此路由，应因状态/响应断言失败。
- [x] 在使用方附近定义最小只读接口，Manager 从 daemon 内存/catalog snapshot 获取 URL；不得令 TUI 读取 catalog 文件，也不扩大所有 RuntimeAPI fake 的必选方法集。
- [x] 添加类型化 `SubscriptionURL` 响应与 client 方法，沿用 path escaping、context、现有大小/超时和错误映射；URL 不进入 Message/Details、operation 摘要或事件，reveal 响应不主动写日志。
- [x] PublicProfile → protocol.Subscription 的 list/show/mutation 映射完整携带三个公开字段；JSON 测试覆盖显式值与零值约定。默认 show 和 CLI JSON 保留 URL 缺失断言。
- [x] 已认证允许、未认证拒绝、未知 ID、运行时不可用、请求取消均有测试；GET 不推进 revision。不新增 capability、不挂载 Web gateway。
- [x] 为 PATCH `updateSubscription` 加入既有 `operations.begin` 完整 handler 跟踪；响应正常/错误/取消路径均结算。沿用 running/finished/unknown 协议，不把错误结果或新增 ID塞进查询 DTO。
- [x] 沿用 dev 原始错误日志策略，不恢复已移除的脱敏逻辑；验证 reveal 响应未主动记录，TUI 传输错误使用安全文案。
- [x] Add 成功创建但首次下载失败保留现有 HTTP 201、ID、LastError 语义；不得改成整体添加失败。

最小测试建议：`TestSubscriptionURL_AuthenticatedRead`、`TestSubscriptionSaveTracking_PatchSettlement`。

```console
go test ./internal/control/server ./internal/control/client ./internal/control/protocol -run '^TestSubscription(URL|SaveTracking|Responses)'
go test ./internal/control/... ./internal/runtime ./internal/cli
```

完成证据：只有 reveal 路由响应含完整当前 URL；新字段投影齐全；PATCH 可以被现有 operation 查询观察。

### T06：列表状态、Next 与居中

依赖：T01、T05。文件：`internal/tui/pages/subscriptions/model.go`、`model_test.go`、`section_test.go`，`internal/tui/ui/table.go`、`table_test.go`、`strings.go`。

- [x] Red：interval 标记为 true、缓存刚刷新、auto off 时显示 Expired + Manual；URL 不一致仍由 Outdated 优先覆盖。
- [x] 表驱动覆盖全部 Status 优先级交叉，包括 pending/disabled/failed/missing 对 Outdated/Expired 的遮盖；Live 与 InUse 独立。
- [x] 改 URL/interval 后 Next 基于 ScheduleFrom；旧 UpdatedAt、强制过期及以前的错误不能让新截止时间显示立即到期。自然过期继续分 Expired / Need refresh。
- [x] 列名改成 InUse/Enabled/Status/Mode，Status MinWidth 12，先丢 Next/Last update。限制字符串替换作用域，不改其他页面的 Proxy/Load 文案。
- [x] 增加 AlignCenter，按可见终端宽度处理 ANSI、宽字符和截断；InUse 的标题与圆点按同一列宽居中，保留原左右对齐行为。
- [x] list 与 detail 共用状态计算，禁止后台新快照只更新列表而详情长期停在旧状态。

最小测试建议：`TestSubscriptionStatus_IntervalFlagPriority`、`TestPadCell_Center`。

```console
go test ./internal/tui/pages/subscriptions ./internal/tui/ui
```

完成证据：全状态表与窄宽度表格可验证。此任务不单独发布“删除 e”：按键切换随 T07–T09 一起完成。

### T07：统一详情/添加表单、字段导航与 URL 回填

依赖：T05、T06。文件：`internal/tui/pages/subscriptions/form.go`、`model.go`、`form_test.go`、`model_test.go`；可新增 `detail.go`、`detail_test.go`、`url_test.go` 分离表现逻辑。

- [x] Red：Enter 打开可编辑详情；在每个输入/循环字段上 Enter 都不提交，只有 Save 焦点 Enter 调用一次 PATCH。
- [x] 已有条目用只读状态区 + Name/URL/Interval/Auto refresh/Mode + Save；添加只保留 Name/URL/Mode + Save，Mode 默认 DIRECT。
- [x] 文本框 ↑/↓/Tab/Shift+Tab 按字段导航；左右键保留文本光标行为，Space 是字符。循环行左右/Space 只改草稿；输入框 Enter 下一项，Save Enter 提交。
- [x] 表单的 PATCH 只含实际修改字段；显式 false、空 interval、DIRECT 均正确。所有字段未改时不发送空 mutation，保留 Save 后返回列表的交互。
- [x] reveal 异步回填受弹层/订阅/连接身份及 URL touched 保护；测试先粘贴后返回、关闭再打开同一 ID、切换 ID、返回顺序颠倒、失败后仍可粘贴。
- [x] 读取失败/未完成且未编辑省略 URL；主动清空显示 `URL is required.` 并阻止保存；使用无真实凭据 fixture。
- [x] 列表保留 Enter、a、Space、p、r、Ctrl+R、u、d；新详情上线同时移除 e。弹层内 p/r/u 等只作为文本字符，不穿透列表。
- [x] 关闭释放该弹层读取与 URL 引用，并恢复根层正确输入模式；不持久保存草稿。

最小测试建议：`TestSubscriptionForm_OnlySaveSubmits`、`TestSubscriptionReveal_UserInputWins`。

```console
go test ./internal/tui/pages/subscriptions -run '^TestSubscription(Form|Reveal)_'
go test ./internal/tui/pages/subscriptions
```

完成证据：添加和详情使用同一导航规则；输入任何字段不会意外触发业务 mutation。

### T08：Saving、覆盖确认、Unknown 与重新提交

依赖：T05、T07。文件：`internal/tui/pages/subscriptions/model.go`，可新增 `save.go`、`save_test.go`；`internal/tui/model.go`、`model_test.go`、`ui/focus.go`；复用 `internal/control/client/operations.go`。

- [x] Red：保存命令未返回时弹层保持，显示 `Saving...`；连续 Enter、Esc、循环/文本输入不再提交、关闭或修改冻结请求。
- [x] 明确成功关闭并选中该 ID；明确失败留表单显示安全英文错误。错误分类不直接显示 transport `err.Error()`，不能用未知/超时响应冒充明确失败。
- [x] 冲突展示设计英文 Overwrite/Cancel，默认 Cancel。确认取最新 revision，只提交原请求实际改动字段，使用新 operation ID；再次冲突再次确认。Cancel/Esc 放弃待覆盖请求并回列表，删除对象不重建。
- [x] 根确认通道必须支持此 owner 的取消与确认回调，以及 `Overwrite`/`Submit again` 的按钮文案；以最小可选字段扩展既有确认 UI 或本页局部状态，默认保持其他页面确认框行为。
- [x] Unknown 仅为尚不能确定的结果。连接可用则直接查询，断开则由根层重连事件触发；接收连接代次，拒绝旧代次/旧弹层回调。
- [x] 查询只调用 OperationStatus 与订阅读取/reveal，不重放 mutation。running 时继续可取消等待；finished 后读取业务状态，finished 本身不代表成功。
- [x] 修改已有 ID：核对实际提交字段与服务端状态；相符可采用当前状态，但不宣称精确证明是哪次操作导致。缺失、读取失败或并发歧义不能伪报已保存。
- [x] 添加响应缺失且无法可靠证明新 ID 时保持 Unknown；不按名称、数量或唯一差集候选直接认领，不新增结果恢复协议。
- [x] 无法确定后停止等待动画，显示 `Unable to confirm the previous save.`，允许 Submit again；必须再弹确认，添加文案明确可能重复创建。Cancel 不发送；确认才用新 operation ID 及最新 revision 重提实际修改字段。
- [x] 重提若再次结果未知仍走同样流程，不无限重试。查询已明确 running 时不开放重提。Unknown Esc 关闭不表示撤销；原请求 owner 收尾不会关闭另一个新弹层。
- [x] 正常 Add 响应中已经有 ID 且首次刷新失败：返回列表选中该 ID，显示设计英文提示，r 刷新，不再次 Add。
- [x] 覆盖重复结果、重连、切页、关闭、查询取消、TUI 退出；所有新增命令/等待器有终止条件，不存储 context，不遗留后台查询。

最小测试建议：`TestSubscriptionSave_WaitsForResult`、`TestSubscriptionSave_ConflictChangedFieldsOnly`、`TestSubscriptionSave_UnknownResubmitRequiresConfirmation`。

```console
go test ./internal/tui/pages/subscriptions -run '^TestSubscriptionSave_'
go test ./internal/tui/... ./internal/control/...
```

完成证据：每种 Saving/Conflict/Unknown 转移均有发送次数、字段内容、operation ID/revision、弹层身份与英文文案断言。

### T09：小终端、帮助与根层按键整合

依赖：T06–T08。文件：`internal/tui/pages/subscriptions/` 的表现层及测试、`internal/tui/ui/keymap.go`、`keymap_test.go`、`strings.go`，`internal/tui/help_test.go`、`golden_test.go`、`model.go`；必要时更新对应 golden fixture。

- [x] Red：在根层最低 72×22 下，将焦点移到每个字段与 Save 都完整可见，并能滚回顶部读取状态。
- [x] 正文整体滚动，底部提示固定；长 URL 单行横滚，长错误换行不吞焦点；resize 后钳制 scroll，不能只测试页面获得 72×22 而忽略根层边框占用。
- [x] 订阅页不再使用 ModeDetail，其他页面继续使用。按焦点与保存状态过滤帮助，循环键只在对应字段出现。
- [x] footer 移除 e edit，p 改 mode/cycle mode；Saving 不显示 Esc cancel，Unknown 的 Esc 为 close；帮助/底栏由原 catalog 同源生成。
- [x] 根层 ?/q/数字跳页等不截走文本输入；文本中的 URL 问号、p/r/u/Space 可输入。Saving 不经快捷键回到可提交状态；保留既有 Ctrl+C 退出语义并回收 owner。
- [x] 只更新真实改变的 golden，人工检查 rendered strings 与全部英文文案；不能通过全量重录忽略其他页面退化。

最小测试建议：`TestSubscriptionLayout_FocusVisibleAtMinimumSize`、`TestSubscriptionHelp_ContextualBindings`。

```console
go test ./internal/tui/pages/subscriptions ./internal/tui/ui ./internal/tui
```

完成证据：最小尺寸、常用大尺寸、缩放、长数据及所有焦点/请求状态均可操作，其他页面帮助不变。

### T10：跨包验收与用户文档

依赖：T01–T09。文件：新增 `internal/integration/subscription_edit_test.go`，复用 `runtime_test.go`、`fakemihomo_test.go`、`control_test.go`、`setup_operations_test.go`、`web_gateway_test.go` 的隔离装配；更新 README 中英文、`docs/commands.md`、`docs/architecture.md`、必要的 `docs/unix-layout.md` 及订阅帮助设计的对应条目。

- [x] 通过真实本地 client/server + 临时目录 + fake mihomo 验证 A InUse → 改 URL → 继续运行旧缓存 → 到期/手动刷新 → 成功应用新缓存；失败恢复场景与 inactive 场景成对覆盖。
- [x] 并发阻塞下载 → 修改 URL/interval 或删除 → 释放旧成功/失败，外部读取确认无陈旧写回；旧 URL/新 URL 均不进入非 reveal DTO，reveal 响应不主动写日志。
- [x] 重启装配读取 catalog，保留 URL/CacheURL、调度起点、interval 标记与 active；不依赖 TUI 打开才恢复调度。
- [x] 本地认证 reveal 成功、未认证拒绝、Web gateway 不暴露；协议默认 list/show 和 CLI text/JSON 无 URL，CLI 原参数名与退出码不变。
- [x] PATCH operation 生命周期、响应丢失与核对不重放有跨包测试；无法证明 Add 身份时走用户确认重提，不增加持久 operation 字段。
- [x] 用户文档说明 URL 不即时拉取、Outdated 可 Use 旧缓存、interval 修改强制 Expired、新 Enter/Save 操作、结果未知后的二次确认、TUI 全英文、配套升级与不支持直接降级。
- [x] 降级说明要求停止 daemon 后对匹配布局的完整业务数据做兼容备份/恢复，覆盖 catalog、缓存、settings/state 和运行配置之间的一致性；不提供“删两个 YAML 字段即可无损降级”的错误指导，不在本任务实际运行备份/服务命令。
- [x] 同步 `docs/architecture.md` 的“URL 只在私有目录”表述，明确本次已批准 authenticated reveal 的窄读取出口，list/show 限制与 reveal 不主动记录日志限制继续成立；记录老二进制严格解码限制。

```console
go test ./internal/integration -run '^TestSubscriptionEdit_'
go test ./internal/integration
```

完成证据：设计 §9 每条验证均能定位到单元或集成测试；变更仅涉及计划范围，未触碰 CHANGELOG。

## 5. 最终验证与执行记录

### 5.1 验证顺序

先完成任务最小测试与目标包，再执行全仓检查。验证已执行；真实结果与平台限制见 §5.3。

```console
go test ./...
go test -race ./...
go vet ./...
gofmt -l .
python -m pytest scripts/test/test_unix_layout_security.py -q
git diff --check
git status --short
```

Windows race 需要可用的 race/编译工具环境；不得为了通过 race 修改发布 CGO-free 约束。无法本机执行时记录确切限制，交由已有原生 CI 验证，不能写成通过。已有 Unix 安全检查属于合并验收的一部分，且不在工作站运行真实 root/two-UID testenv。

对 Windows、Linux、macOS 的 amd64/arm64 六个目标分别以 CGO_ENABLED=0 编译 `./cmd/mihari`。下面 PowerShell 示例仅构建临时产物并恢复当前进程环境，不操作服务或真实数据：

```powershell
$taskBuildDir = Join-Path ([System.IO.Path]::GetTempPath()) ('mihari-sub-edit-build-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $taskBuildDir -ErrorAction Stop | Out-Null
$taskSavedEnv = @{}
foreach ($taskEnvName in @('CGO_ENABLED', 'GOOS', 'GOARCH')) {
    $taskSavedEnv[$taskEnvName] = [Environment]::GetEnvironmentVariable($taskEnvName, 'Process')
}
try {
    $env:CGO_ENABLED = '0'
    foreach ($taskOS in @('windows', 'linux', 'darwin')) {
        foreach ($taskArch in @('amd64', 'arm64')) {
            $env:GOOS = $taskOS
            $env:GOARCH = $taskArch
            $taskSuffix = if ($taskOS -eq 'windows') { '.exe' } else { '' }
            $taskOutput = Join-Path $taskBuildDir "mihari-$taskOS-$taskArch$taskSuffix"
            go build -trimpath -o $taskOutput ./cmd/mihari
            if ($LASTEXITCODE -ne 0) { throw "Build failed: $taskOS/$taskArch" }
        }
    }
}
finally {
    foreach ($taskEnvName in @('CGO_ENABLED', 'GOOS', 'GOARCH')) {
        [Environment]::SetEnvironmentVariable($taskEnvName, $taskSavedEnv[$taskEnvName], 'Process')
    }
}
```

按风险比较 subscription/runtime/control/TUI 及全仓覆盖率，不引入固定门槛；覆盖率下降需核对被改变行为而非堆调用测试。coverage.out、构建产物与临时报告不提交。通过后不无理由重复全套检查。

### 5.2 追踪矩阵

| 设计要求 | 任务 | 必须保留的证据 |
| --- | --- | --- |
| G1 reveal 权限与保密 | T05、T10 | authenticated/unauthenticated、list/show 无 URL、reveal 响应不主动记录、Web 不暴露 |
| G2/G3 配套升级与降级 | T01、T05、T10 | 旧目录读入、新字段往返、严格解码、用户文档 |
| G4/G4.1/G10 interval 与 Next | T01、T02、T04、T06、T10 | 立即 Expired、优先级、Manual、重启、nextAllowed 复位 |
| G5 旧源错误 | T02、T03、T10 | 清旧错误、迟到失败与成功都不写回 |
| G6/G6.1 URL 输入 | T07、T09 | 回填竞态、未读省略、主动清空、按键不穿透 |
| G7 冲突覆盖 | T08 | 二次确认、仅实际修改字段、新 revision、再次冲突 |
| G8/G8.1 保存 | T08、T09 | Saving 保留弹层、成功选中、失败可改、迟到结果归属 |
| G8.2/G12 Unknown | T05、T08、T10 | 查询不重放、finished 非成功、重提再确认、新 operation ID |
| G9 布局 | T06、T09 | 72×22 根层尺寸、焦点与顶部状态可达、固定 footer |
| G11 部分成功 Add | T03、T05、T08、T10 | 返回新 ID、首次失败提示、r 刷新不再 Add |
| 全英文与原列表键 | T06–T10 | UI 自有英文文案、e 移除、Enter 新入口、帮助一致 |
| daemon/配置事务 | T02–T05、T10 | 单写入路径、锁外准备、身份复核、回滚与 degraded |

### 5.3 实施记录（2026-09-14）

完成每个任务时记录真实证据；仅填写已实际完成的验证，不预填成功结果。

| 任务 | 状态 | Red 命令与正确失败原因 | Green/回归结果 | 涉及文件与剩余限制 |
| --- | --- | --- | --- | --- |
| T01 | 完成 | `go test ./internal/subscription -run TestSubscriptionCacheState_`：已有 ActiveID 时未补来源、新持久键/公开状态缺失 | 目标包与全仓/race 通过 | model.go、catalog.go、cache_state_test.go；旧目录迁移和字段往返 |
| T02 | 完成 | `go test ./internal/runtime -run TestSubscriptionEdit_`：URL 编辑清空缓存/active、interval 未复位 | 目标测试、runtime 全包与全仓/race 通过 | subscription.go、subscription_edit_test.go；沿 Service 注入时钟复位候选 |
| T03 | 完成 | `go test ./internal/subscription -run TestSubscriptionRefreshState_`：迟到错误污染新源、commit 未清标记、异源 304 被接受 | service/runtime 回归、304、回滚与全仓/race 通过 | prepare/commit/rollback 守卫；旧诊断回归按新失败状态语义保留 |
| T04 | 完成 | `go test ./internal/subscription -run TestSubscriptionSchedule_`：旧缓存年龄/nextAllowed 提前或延迟新调度、队列不复核 | 调度目标测试、subscription 全包与 race 通过 | scheduler.go、schedule_state_test.go；保留 jitter，按调度身份清旧退避 |
| T05 | 完成 | `go test ./internal/control/server -run TestSubscriptionURL_`：reveal 404；`TestSubscriptionSaveTracking_`：PATCH 为 unknown | control 全包、IPC 认证/投影/生命周期与 race 通过 | protocol/server/client/runtime；不恢复 dev 已移除的日志脱敏 |
| T06 | 完成 | `go test ./internal/tui/pages/subscriptions -run TestDetailStatus_`：旧 Stale/Retry pending 不满足 Expired/Outdated/Next | 状态优先级、居中/显示宽度与 TUI 全包通过 | InUse/Enabled/Status/Mode；Status 最小 12 列 |
| T07 | 完成 | `go test ./internal/tui/pages/subscriptions -run TestDetailForm_`：未改字段仍提交、缺少 Mode/Save；`TestDetailReveal_LongURL`：2048 字符截断 | 导航、touched、空值、迟到/跨连接/跨弹层、长 URL 回归通过 | form.go、dialog.go；无 UI 人为截断；仅 touched 且值变化才提交 URL |
| T08 | 完成 | `go test ./internal/tui/pages/subscriptions -run TestDetailSave_`：Enter 非编辑、Save 提前关闭；旧核对 revision 可误报成功；空 PATCH 被发送 | Saving/冲突/Unknown/再确认/新 ID/默认 Cancel/部分成功/旧核对回归通过 | dialog.go；请求有 timeout/cancel；异步回调归属订阅页，原始错误仅用于诊断 |
| T09 | 完成 | `go test ./internal/tui -run TestSubscriptionDetail_`：输入模式消息前 q 退出；`TestDetailLayout_`：Saving footer 宣称 cancel；`TestSubscriptionHelp_`：表单仍显示列表动作 | 72×22 根层、滚动/resize、长 URL、焦点、帮助/底栏与 TUI 全包通过 | 正文滚动，PgUp/PgDn 读状态；共享 keymap，移除只读 detail 和 e |
| T10 | 完成 | 跨包复用前述 Red 行为；新增集成验证与文档静态检查 | 全仓、race、vet、六目标编译、Unix 静态安全脚本通过（4 项条件跳过） | 真实 IPC + fake controller/mihomo；响应丢失不重放；中英文 README/commands/architecture 同步 |


最终验收：

- 已 fetch `origin/dev` 并无冲突 rebase 到 `a80a9b6`。源工作区的 `.gitignore` 修改和原始未跟踪设计文档均保留。
- `go test ./...`、`go test -coverprofile=<临时文件> ./...`、`go test -race ./...`、`go vet ./...` 通过；最后补充的响应丢失集成测试另经 `go test -race ./internal/integration` 全包验证。
- `gofmt -l .` 无输出；`git diff --check` 通过。没有 CHANGELOG、依赖/toolchain 或系统服务变更。
- Windows/Linux/macOS × amd64/arm64 六目标均以 `CGO_ENABLED=0 go build` 通过；构建产物仅在本机 Temp 中，未加入仓库。
- `python -m pytest scripts/test/test_unix_layout_security.py -q`：74 passed、4 skipped。当前机器为 Windows；未执行原生 Linux/macOS 测试、root/two-UID testenv、真实订阅/真实 mihomo 或系统服务操作。
- 新 IPC 测试 `TestSubscriptionEdit_IPCPreservesCacheAndRestartState`、`TestSubscriptionEdit_IPCStaleFetchCannotWriteBack`、`TestSubscriptionEdit_LostResponseObservationNeverReplaysPatch` 验证来源保留、重启、迟到结果和只读核对；原 fake mihomo 生命周期用例增加 active URL 编辑到成功应用的验证。Web gateway 用例确认浏览器入口不能取得 reveal 响应。
- 库 API 查询使用 ctx7：先 resolve Bubbles，再读取 `/charmbracelet/bubbles/v2.0.0` 的 textinput 宽度/焦点资料；不增加或升级依赖。

覆盖率按同一 Windows 环境对 `a80a9b6` 临时只读 worktree 与功能 worktree 执行全仓 profile 后比较。临时基线 worktree 已清理，profile 位于 Temp，未提交。

| 范围 | rebase 基线 | 最终实现 |
| --- | --- | --- |
| 全仓 | 78.01% | 78.24% |
| subscription | 82.20% | 82.84% |
| runtime | 75.74% | 76.00% |
| control/client | 85.08% | 85.20% |
| control/server | 81.37% | 81.55% |
| control/protocol | 83.45% | 83.45% |
| TUI 订阅页 | 78.43% | 85.07% |
| TUI ui | 82.05% | 82.26% |
| TUI 根包 | 85.46% | 85.45% |

根包新增 20 条语句覆盖 17 条，比例微降 0.01 个百分点，已增加根层最低尺寸与立即按键归属回归；连接/关闭回调另由订阅页状态测试覆盖。未修改的 platform 包本次观测 71.94% → 71.83%（相同 3493 条语句，Windows 环境分支少覆盖 4 条），没有对应生产/测试 diff；不为数值波动添加无行为断言的测试。关键失败、回滚与身份守卫用例继续保留。

## 6. 审查与交付切分

沿设计 §10 划为四组审查边界，在同一功能 PR 中交付：

1. Catalog/runtime/scheduler：T01–T04，先把缓存来源、调度与事务行为闭合。
2. 本地协议与安全：T05，完整 DTO 投影、URL reveal 和 PATCH 跟踪。
3. TUI 列表：T06，列名、Status、Next、居中；此组独立交付时保留原可用编辑入口。
4. TUI overlay 与最终交付：T07–T10，Enter/e 切换、Save、确认、Unknown、帮助、布局及文档同步。

移除 e 与 Enter 打开新编辑详情必须在同一可用交付中完成，不能把“只读详情 + 已删除 edit”作为中间可合并结果。根据后续提交 PR 指令，本次使用一个完整功能 PR，保证协议、持久化和 TUI 同步交付。

交付时报告完成任务、实际通过的检查、未验证项与原因，并检查精确 diff。已有设计文件仍需与执行文档一同保留在本分支工作区；不覆盖主工作区旧副本或其 `.gitignore` 修改。后续功能 PR 指向 dev，合并仍需用户确认。
