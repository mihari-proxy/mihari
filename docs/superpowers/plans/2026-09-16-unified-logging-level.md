# Issue #209 统一日志级别执行方案

日期：2026-09-16

状态：任务 0–8 已实施，本地验收完成，远端 CI 与 bot review 随 PR 跟踪。下文保留原实施合同；实际函数和测试名称以代码为准，执行记录见第 8 节。

设计：[统一日志级别与 mihomo 状态同步](../specs/2026-09-16-unified-logging-level-design.md)

工作目录：`.worktrees/feat-209-unified-logging-level`

工作分支：`feat/209-unified-logging-level`

基线：`origin/dev` @ `6bd40d18903cc796e2d2d808aa6c0774a81bbf7f`

## 1. 交付边界

本次实现：System 全局日志设置与 mihomo 日志配置一致、真实捕获级别、被动 silent、2 秒外部日志级别观察、Web 日志配置请求适配、独立实时流、未保存状态及回归测试。

以下进入 [#257](https://github.com/mihari-proxy/mihari/issues/257)，不在 #209 顺带实现：运行模式等其他字段的外部变化采纳、任意字段/组合配置修改、第三方面板操作支持矩阵。已有运行模式、TUN、GLOBAL 路径继续运行，不重写其产品语义。

不新增依赖，不更改 Go/toolchain 或支持平台，不修改 CHANGELOG。禁止连接真实订阅、真实用户内核或修改系统服务。用户随后已授权实施、commit、push、创建 PR，并每 10 分钟检查 CI 与 bot review；合并仍需用户确认。

## 2. 现有代码与实施落点

| 现状 | 落点及预期修改 |
| --- | --- |
| `config.Settings.Validate`、`logging.ParseLevel` 只接受四档 | 分开主动值校验与持久化/观察值校验，后者增加 silent |
| `subscription.Generate` 保留源 log-level；`core.BootstrapConfig` 固定 info | 统一从 settings 映射内核级别，覆盖源值，保持原缓存 |
| `Manager.UpdateLogging` 只执行 Save → Publish → Apply | 增加受控内核变更、确认、配置事务和补偿；轮转单独修改保留原路径 |
| `changeRoutingLocked` 已有内核先执行、回读、保存及补偿 | 复用思路与错误边界，不直接复用会修改 routing 的用例 |
| 普通配置事务用 runtime 路径；trusted 事务用 capability 和空 reload 路径 | 两种实现均须覆盖，不能给 root 增加普通文件 IO 旁路 |
| 普通启动 `EnsureRuntimeConfig` 对已有文件只做不变量检查 | 补齐每次内核启动前从保存意图准备日志配置，防止旧文件复活旧级别 |
| `Manager.Run` 持有 supervisor、scheduler、gateway 生命周期 | 在此挂接可取消、可 join 的日志观察任务 |
| app Health 回调每次健康检查调用 `RestoreRouting` | 日志启动恢复只在每个子进程启动阶段执行，不能每次健康检查覆盖外部修改 |
| `LoggingStatus` 无 saved/live 差异，TUI session 按 revision 拉取 | 增加可选同步状态；状态转换推进 revision，使已连接 TUI 刷新 |
| 捕获构造固定 stdout INFO、stderr WARN | 加入 mihomo 专用行级别识别，保留通用固定级别 writer 的用途 |
| mihomo stream client 连接 `/logs` 不带 level | TUI/CLI 共用实时日志来源显式请求 debug，显示过滤仍由客户端处理 |
| Web 单字段 mode 或 TUN 写入已受控；log-level 被拒绝 | 只增加单字段日志 PATCH，不开放任意 PUT/混合写 |

实施以本 worktree 实际源码为准。CodeGraph 索引位于主工作区；若提示 stale 或主工作区已有其他任务修改，应直接核对本 worktree 的对应小段源码。

## 3. 先固定的技术合同

### 3.1 三种级别表示

| 用途 | 允许值 |
| --- | --- |
| System / 本地 PATCH 主动设置 | debug、info、warn、error |
| Mihari 保存、读取、内部应用、被动观察 | debug、info、warn、error、silent |
| 与 mihomo 配置交互 | debug、info、warning、error、silent |

- warning/warn 在内核输入边界归一为 Mihari 的 warn；向 mihomo 输出 warning。
- 本地请求原有四档校验保持严格，不因内部 ParseLevel 支持 silent 而放开主动 silent。
- Web 单字段 log-level PATCH 接受四档及 warning 别名，silent 主动写入不在本次允许范围；直接外部修改内核为 silent 则允许观察采纳。
- 建议内部定义具名的静默级别，并在 handler 的 Enabled 边界明确禁用普通记录，不依靠随意大的数值碰巧过滤。状态序列化从 settings 取字符串，不输出 slog 的 ERROR+N。
- silent 不删除文件，不禁用导出，不取消 API/UI 错误或独立 FailureReporter；仅调整 MaxFiles/MaxSizeMB 必须保持 silent。

### 3.2 本地控制状态的兼容增量

继续使用 `GET/PATCH /v1/logging`。现有 `level` 仍表示 Mihari 已保存并应用到文件日志组件的配置；保存失败时不把它伪装成内核的新值。请求字段、错误 envelope、错误码和退出码保持。

拟在 `protocol.LoggingStatus` 增加下列可选字段：

| JSON 字段 | 类型 | 含义 |
| --- | --- | --- |
| `core_level` | string, omitempty | 已确认的当前实例内核级别，统一为 warn 等 Mihari 拼写；无法确认时省略 |
| `sync_state` | string, omitempty | applied / pending / unsaved / unknown |
| `sync_message` | string, omitempty | 固定、安全的状态解释，不含底层 cause、路径或配置原文 |

状态含义：

- applied：保存值与当前已确认内核值一致。
- pending：已保存，内核明确停止/缺失，等待启动。
- unsaved：内核变化已确认，但同步保存尚未成功；`level` 与 `core_level` 可以不同。
- unknown：当前实例无法确认、正在启动/恢复，或返回不支持的值；不得把连接错误当 stopped。
- 补偿无法确认沿用已有 mutation degraded 状态，不把一次可重试的外部同步保存失败升级成全局故障。

无新字段的旧响应仍按既有配置应用，不推断新同步状态；协议测试证明旧字段 JSON 形状不变。新字段的具体加入属于本方案需要审阅的公开 JSON 增量，不能以“可选字段”为理由跳过项目契约检查。

状态变化推进全局 revision，使现有 `Session.pollLogging` 能重新读取；相同观察和相同错误状态不每 2 秒刷 revision。仅观察状态转换不增加 configGeneration，真正保存/发布配置才增加。每次实际重试使用新的执行身份，不能被失败 operation 缓存永久挡住。

### 3.3 受控在线修改：内核先执行，保存后发布

适用于 System Level 和 gateway 的 log-level PATCH；轮转参数与 Level 同时修改时必须在同一结果中提交。

1. 校验主动输入，读取 settings generation、订阅缓存身份、核心实例代次及客户端 revision；复制必要输入后释放短锁。
2. 在 mutation 提交区外生成目标 runtime 候选并执行内核校验。此时尚无可见状态变化。
3. 取得统一 mutation 所有权，重新验证所有输入，读取旧内核级别和旧 runtime 文件/capability。冲突时清理候选并返回，不提交旧结果。
4. 向 mihomo PATCH 目标级别，回读确认规范化后的真实值。失败或未知结果进入有界确认/补偿，不把 HTTP 204 当作完整事务成功。
5. 用已有配置事务发布已校验候选并 reload，复用既有 routing/GLOBAL 恢复合同；随后再次确认日志级别。trusted 路径保留 hash、generation、capability 和空 reload 路径要求。
6. 最后原子保存 settings。成功后发布内存、Mihari logger 配置及 revision，再返回成功。保存后的 directory-sync warning 保持成功，并在业务锁外报告。
7. settings 提交前失败：恢复旧 runtime，再恢复并确认修改前的实际内核级别；不能只恢复文件就假定 live 已恢复。必要时用独立、有时限的恢复 context 完成，不受请求已取消截断。
8. 补偿失败：记录原始错误链，标记现有 degraded/fence；如果现有可信恢复路径要求停止核心，沿用它，不能假报旧状态完整恢复。

`settingsMu` 和 coordinator 的状态锁不承担网络、磁盘或进程校验 IO；串行 mutation 所有权不能被误当成可随意持有的状态 mutex。避免在已持有 maintenance 时重入 `UpdateLogging` 或 `commitRuntimeConfig` 的外层协调器。

失败矩阵必须固定：

| 失败点 | 文件与保存值 | 内核 | 客户端结果 |
| --- | --- | --- | --- |
| 候选校验 / 预检冲突 | 无变化 | 无变化 | 既有参数/冲突/数据错误 |
| PATCH 失败或超时 | settings 未提交 | 回读并恢复旧实际值 | 失败，未知恢复则 degraded |
| runtime 发布 / reload / 确认失败 | 恢复旧 runtime，settings 未提交 | 恢复并确认旧实际值 | 失败或 degraded |
| settings 未提交失败 | 恢复旧 runtime，settings 保持旧值 | 恢复并确认旧实际值 | 失败或 degraded |
| settings 已提交的 sync warning | 保留新配置 | 保留新值 | 成功 + 文件 warning |
| 提交后请求取消 | 保留新配置并完成状态发布 | 保留新值 | 可通过既有操作/状态查询确认；不重做 mutation |

这是 live、runtime、settings 的可补偿事务，不承诺多个文件和内核内存具备单一物理原子提交点。进程在中间退出时由已提交 settings 决定下次启动；必须通过重新装配测试证明。

### 3.4 外部采纳：只同步已发生的变化，不重放修改

执行细化：外部采纳先完成 settings 持久化和文件日志应用，不为了“再次生效”向 mihomo 发 PATCH/reload，也不先改 runtime 文件。runtime 是生成物，在下一次正常配置生成或每次内核启动前从已保存 settings 重建。这样 Q12 的保存失败路径没有撤销内核状态的副作用；与受控 Level 修改立即完成 runtime 事务的路径明确区分。

1. daemon 正常运行时每 2 秒尝试一次；停止、缺失、启动中、受控 mutation/恢复中、安装 validation mode 时不采纳。
2. 在短暂读取门控内取得核心代次、settings generation 和状态，锁外带超时读取 `/configs`，仅解析 log-level。
3. 取得 mutation 所有权，复查代次/generation，必要时再次读取级别；过期读数直接丢弃，下一次重试读取最新值。
4. 与保存值相同：只收敛同步状态，不重复保存、Apply 或推进无意义 revision。
5. 与保存值不同且合法：准备只修改日志级别的 settings 候选，原子保存；成功后发布设置、应用 logger，标记运行配置生成物待追平。
6. 保存失败：内核保持实际值，Mihari 文件日志继续使用最后成功保存值，发布 unsaved 状态和安全提示；下次重新观察后再保存，不重试旧的固定 payload。
7. 下一次受控 runtime 生成使用最新已提交 generation。在未保存观察尚未解决时，可能把旧级别写回内核的配置 mutation 应先尝试同步，仍失败则明确拒绝这次配置变更；不能靠订阅刷新静默撤销外部修改。无关只读、日志导出等不受限。
8. 外部工具没有 revision/CAS，无法和任意直连修改实现严格串行化；承诺是正常读写条件下按周期收敛。请求中途出现更新的外部值时，以新观察重试，不虚构无竞态保证。

每次内核启动前的重建是这一方案的必要部分，不能仅在 daemon 启动时执行，或用“下次订阅刷新会修复”代替。

### 3.5 启动、停止与任务生命周期

- 明确停止/缺失时，主动修改只保存 settings、应用 Mihari 日志并返回 pending；没有核心可用时不强行调用 `mihomo -t`，也不自动启动。
- 计划给 supervisor 增加窄的可选启动准备回调（如 `BeforeStart(context.Context) error`）：在前一子进程已退出、startGate 保护下、调用 Starter.Start 前执行。由 app 注入 Manager 的配置准备，不把业务生成逻辑放进 supervisor。
- Manager 在回调内从最新保存意图生成/校验启动配置；准备慢 IO 仍在 mutation 提交区外，发布时复核 generation、缓存和停止状态。没有旧进程时无需对旧 controller reload。
- 普通/trusted 启动都接入，覆盖首次启动、显式 Restart、崩溃重启与安装后的启动。不得降低可信核心校验或绕过既有停止事务。
- 回调不能持 Manager 所有权再调用 `Supervisor.Maintain`，避免与 startGate 形成锁序反转；关闭、准备失败、backoff 和显式重启均须测试。
- 启动/结束推动独立的实例代次；不能只靠 PID 判断新旧实例，PID 可复用。观察器在该实例成功启动并报告 running 后才采纳外部值。
- 不把“恢复保存的日志级别”无条件加到每次周期 Health 回调，否则会反复抹掉尚未采纳的外部修改。
- 日志观察任务沿用 Manager.Run 子任务的独立 cancel+done 模式，supervisor 提前返回时也必须 cancel 并 join。一个实例最多一个在途观察，2 秒表示检查周期，不保证慢 IO 时仍每 2 秒完成。

### 3.6 捕获、实时流与 Web

- 新增 mihomo 专用捕获构造入口，例如 `NewMihomoCaptureWriter`；保留 `NewLineCaptureWriter` 固定级别的通用契约。
- 只解析已识别格式的顶层 severity：原生 text 的 level 字段和 JSON 顶层 level。不能用全文搜索把 msg 内的 `level=error` 当作行级别。
- 为解析建立去 ANSI 的临时视图，原始 msg 保持不变；warning/warn 等价，已识别 fatal/panic 可按 ERROR 捕获，无级别堆栈行按 Q5 回退，不猜测继承。
- 真实行级别用于 Enabled 判断、记录外层 level 和分片，不能先按 stdout INFO 丢掉 warning/debug 再尝试解析。保持 256 KiB 上限和既有 UTF-8/分片/Flush 生命周期。
- 内部 `mihomo.Client.streamURL(StreamLogs)` 显式带 `level=debug`，避免无参默认 info 永远看不到 debug。其他 stream 不变；TUI 本地 Level 筛选继续独立并统一 warn/warning 匹配。
- Web `/logs` HTTP/WS 保留调用者 level/format 和默认行为，不套全局文件日志阈值；改变全局日志设置不改写已有流的订阅筛选。
- Web 只允许单字段 `PATCH /configs {"log-level":"..."}`，经同一 daemon 用例执行并在全部受控提交成功后返回 204。GET /configs 仍读取内核事实。
- 混合 mode/TUN/log-level、未知键、受管端口/controller/secret、任意完整 PUT 在副作用前拒绝；多字段事务属于 #257。

## 4. 按依赖顺序执行的 TDD 任务

所有新增行为先形成可编译的断言失败，再最小实现。新增符号可先加不实现行为的骨架，但编译错误不算 Red。下列 Test 名称为拟新增；每项以实际最终名称运行并保留结果。

### 任务 0：基线与故障注入准备

文件：现有 `internal/runtime/logging_test.go`、`routing_test.go`、`config_diagnostics_test.go`、`internal/app/runtime_trusted_test.go`。

- 记录本分支 HEAD、已有未提交文件和测试环境，确认无需要保留的用户代码改动被覆盖。
- 先运行 logging/config/subscription/control logging 相关现有测试；已有失败单独记录，不因本任务无关修复扩大范围。
- 为日志事务复用或补齐最小 fake：Configs/PatchConfigs/Reload、验证、原子保存 outcome、可控阻塞点和 fake waiter。fake 记录事件顺序，不复制生产状态机。
- 把受控与外部采纳两张失败表变成测试清单，尤其区分 Committed=false 与 Committed=true+Warning。

验证：`go test ./internal/config ./internal/logging ./internal/subscription`，`go test ./internal/runtime -run 'TestLogging|TestRouting_PersistFailure|TestConfigDiagnostic_ReloadCompensation'`。

### 任务 1：级别模型、silent 与生成器

修改：`internal/config/settings.go`，新增 `internal/config/logging.go`（级别验证/映射的小文件，按需要），`internal/logging/config.go`、`handler.go`、`runtime.go`，`internal/subscription/generator.go`，`internal/core/config.go` 及邻近测试。

- 首个 Red：`TestGenerate_ManagedLoggingLevelOverridesSource`，默认 settings 应覆盖源 debug 为 info；设置 debug 后覆盖源 info；现有代码会保留源值。
- 覆盖 bootstrap、缺省、省略默认块、warning 映射、silent Load/Save/Clone、输入文档不变、overrides 不能压过受管值。
- `TestRuntime_SilentSuppressesOrdinaryRecords`：DEBUG/INFO/WARN/ERROR 均不新增记录，保留历史和独立 FailureReporter。
- `TestLogging_ActiveUpdateRejectsSilent`：内部解析已放开 silent 后，主动请求仍无副作用地拒绝它；server/runtime 双边均测试。
- 单独修改保留参数时保留 silent；从 silent 切回四档可恢复记录。

验证：`go test ./internal/config ./internal/logging ./internal/subscription ./internal/core ./internal/control/server ./internal/runtime`。

### 任务 2：每次子进程启动前按保存值准备配置

修改：`internal/supervisor/supervisor.go`、`internal/app/runtime.go`，新增 `internal/runtime/logging_startup.go`，必要的 `internal/core/config.go`/trusted 接口使用点及测试。

- Red：已有 runtime 为 info、保存值为 debug 时，fake child 启动看到的配置必须是 debug；随后崩溃重启仍如此。
- 增加窄启动准备回调，接入非 trusted 与 trusted 的既有生成/校验/发布能力；缺少活动缓存失败时不伪造空订阅覆盖已有效配置。
- 覆盖 bootstrap、核心缺失后安装、保存 silent、启动准备失败不 spawn、取消清理、generation 改变重准备/冲突、前一 child Wait 完成后才写入和启动。
- 用事件/通道证明 callback 与维护/Restart/关闭无锁序反转；不在周期 Health 重置日志设置。
- `EnsureRuntimeConfig` 不能仅因旧文件存在就绕过保存意图，也不能因为旧版 log-level 不一致而永久阻止升级启动。

验证：`go test ./internal/supervisor ./internal/app ./internal/core ./internal/runtime`，随后相关 integration。

### 任务 3：受控日志事务与回滚

修改：`internal/runtime/logging.go`，新增 `logging_config.go` / `logging_transaction_test.go`，必要的 `subscription.go` 配置 receipt/恢复小接口；复用 `settings.go` 保存发布合同。

- Red：`TestLogging_OnlineChangeConfirmsCoreBeforeSettingsPublish`，断言内核实际值、runtime YAML、settings、日志过滤和 revision 都达到目标。
- 为新的 target settings 生成候选，不把 current settings 误用于 log-level；提交时复核 generation、活动缓存和实例代次。
- 用顺序记录证明内核确认先于 Mihari 状态发布；再覆盖第 3.3 节每个故障点，断言旧 live、旧文件、旧 settings、revision、补偿次数和诊断 owner。
- 特别覆盖旧 live 与旧 saved 本来不同的情况、PATCH 超时但已生效、reload 成功但回读不匹配、同 operation ID 重放、排队 revision 冲突、提交后取消。
- 轮转参数单独修改不发内核请求；同值但 live 不同不能被本地 no-op 优化漏掉；保留既有相同值请求 revision 语义。
- stopped/missing 保存返回 pending，controller 连接失败不当作 stopped；root/普通分支各有完整成功和回滚证明。

验证：`go test ./internal/runtime -run 'TestLogging|TestManagerSettings|TestConfigDiagnostic'`，再运行 runtime 全包及相关 integration。

### 任务 4：外部采纳与两秒观察任务

修改：新增 `internal/runtime/logging_sync.go` / `logging_sync_test.go`、`logging_startup.go`，`manager.go` 生命周期与核心代次记录，必要的配置准备前置检查。

- Red：`TestLoggingSync_AdoptsObservedCoreLevel`，fake core 外部变更后一次 tick 更新 settings/文件日志，且不发送 PatchConfigs/Reload。
- `TestLoggingSync_PersistFailurePreservesCoreAndRetriesLatest`：保存失败时保持 live debug、saved info；下一次外部改为 warn 后重试保存 warn，而非旧 debug。
- 覆盖 silent 被动采纳，未知值/错误类型不保存；无变化不反复保存或 revision；unsaved→applied、unknown→applied 能被 TUI 观察。
- 覆盖旧观察跨 TUI 修改、订阅生成、reload、进程重启、PID 复用、停机与 validation mode；旧结果不得落盘。
- 待同步失败时，生成 runtime 的操作不能用旧 level 覆盖外部实际值；恢复后使用新的 generation。两秒检测之间的外部直连竞态仅能收敛，不声称具有 CAS。
- 成功采纳后立即 daemon 重建/子进程重启，应从新 saved 生成 runtime；未保存退出则明确使用旧 saved。
- 用 fake waiter/channel 证明只有一个在途 poll，超时可取消，Manager/supervisor 提前退出也 cancel+join；不缓存整个长期 Run 的错误或保存过期 context。
- 重复故障遵循既有诊断级别并避免每 tick 产生无意义状态/日志风暴。

验证：`go test ./internal/runtime -run 'TestLoggingSync|TestLoggingStartup|TestManager'`，`go test -race ./internal/runtime ./internal/app ./internal/supervisor`。

### 任务 5：协议、TUI 同步状态与 silent 显示

修改：`internal/control/protocol/logging.go`、server/client 邻近测试，`internal/tui/session/session.go` 及测试，`internal/tui/model.go`、`logging_applier.go`、`pages/system/model.go`、`internal/tui/ui/strings.go` 及相关 golden。

- Red：`TestLoggingStatus_SavedAndObservedLevelsRemainDistinct`，unsaved 响应保留旧 level 并携带新 core_level；未知时不能用 saved 冒充 live。
- 测试可选字段精确 JSON、旧字段仍存在、旧响应缺新字段、主动 silent 拒绝、只读状态可返回 silent、secret/cause 不进入 DTO。
- TUI 同时展示 Saved/Core 差异及 pending/unsaved/unknown，正常 applied 保持简洁；全部内置文案英文。
- System 可显示 silent 及来源提示，但循环选择只有四档；沿用现有循环起点从 silent 恢复普通档位，不新增隐蔽手动 silent 命令。
- session 对状态-only revision 重新拉取；旧 epoch/旧 revision 的迟到响应不能覆盖新状态；两个 TUI 的 logger 同步更新。
- unsaved 时本地 logger 仍用 `level`，不擅自使用 `core_level`；成功采纳后再同步。断线继续沿用现有 bootstrap 策略。

验证：`go test ./internal/control/... ./internal/tui/...`。

### 任务 6：解析捕获的真实级别

修改：`internal/logging/capture.go`，新增 `mihomo_level.go` / 对应测试，`cmd/mihari/main.go` 捕获装配及测试。

- Red：`TestMihomoCapture_StdoutWarningSurvivesWarnThreshold`，证明 stdout 上的 warning 不能再因 INFO 映射被丢弃。
- 覆盖四档、warning/warn、明确 fatal/panic、文本和 JSON 顶层级别、ANSI、未知级别和无级别回退。
- 反例：msg 内含 level=error、嵌套 JSON 含 level、普通英文 error 字样，不得误判。
- 真实级别驱动外层 JSON level、过滤与分片；原文保留，包括合成 URL/token，沿用完整错误日志策略。
- 保持 chunk 边界、多行、CRLF、空行、256 KiB 截断、UTF-8 和 child 间 Flush/Close 生命周期。

验证：`go test ./internal/logging ./cmd/mihari`，相关 export/fragment integration。

### 任务 7：Web 写入适配与实时流独立性

修改：`internal/web/server.go`、`internal/app/runtime.go` webMutator，`internal/mihomo/stream.go`、`internal/tui/pages/logs/model.go` 及邻近测试。

- Red：`TestGatewayLoggingPatch_UsesConfirmedDaemonMutation`，单字段日志 PATCH 由 Manager 确认且持久化后才 204；失败不得伪装成功或直接透传未知字段。
- 规范化 warning，严格拒绝 silent 主动设置、非法值、混合字段、完整 PUT 和受管字段；既有 mode/TUN 合法请求保持。
- GET /configs 保持内核原有 JSON 拼写和实际值，不伪造 Mihari 保存值替换。
- `TestStreamLogs_RequestsDebugIndependentlyOfFileLevel` 固定 TUI/CLI 流参数；TUI warn 与 warning 等价过滤，仍保持现有精确等级筛选。
- `TestGatewayLogs_GlobalLevelDoesNotClampSubscription` 覆盖 HTTP 与 WS、level/format 参数、现有连接与重连；全局 info/silent 时 debug 流按独立订阅可见。
- 不修改第三方面板资产；fixtures 明确模拟的是 API 请求或真实上游订阅请求，不声称已新增面板设置按钮。

验证：`go test ./internal/web ./internal/app ./internal/mihomo ./internal/tui/pages/logs ./internal/integration`。

### 任务 8：端到端回归、文档与验收

新增：`internal/integration/unified_logging_test.go`（名称可按仓库习惯调整）。使用临时目录、fake mihomo controller 和既有可信测试 fixture。

- System/control 修改 → 内核确认 → runtime/settings → 文件过滤 → TUI 状态 → Web GET 一致。
- 外部 silent → tick → 保存/静默 → TUI 被动显示 → 四档恢复；Web debug 实时流始终独立。
- 保存失败 → unsaved → 更新的外部值 → 成功重试 → 子进程/daemon 重启沿用新值。
- 订阅刷新/切换、配置 reload、轮转单独修改不悄悄复原已采纳级别。
- 事务各失败点及崩溃后重新装配，验证最后一次成功保存的权威和最后有效文件。
- 假服务、listener、goroutine、child、临时文件全部回收，不启动真实系统服务。
- 同步 README 中英、`docs/architecture.md`、`docs/commands.md` 和旧日志设计的相关段落；保留旧设计的历史说明，不误恢复脱敏策略。
- 明确 silent 为可加载的新保存值、主动 API 仍四档、升级降级需兼容数据备份；记录 #209 与 #257 的范围。

## 5. 实施顺序与交付检查点

依赖顺序：0 → 1 → 2 → 3 → 4 → 5 → 6 → 7 → 8。

任务 6 与事务逻辑可以独立实现，但本方案默认顺序执行，不要求并行 agent。每项通过对应验证后再进入下一项；这些是工作检查点，不是自动授权逐项 commit。

- 检查点 A（任务 1–2）：值模型、被动 silent、每次启动使用保存值成立。
- 检查点 B（任务 3–4）：受控回滚与外部重试不会互相套用，旧观察和配置生成不能覆盖新意图。
- 检查点 C（任务 5–7）：TUI 能看见同步差异，gateway 写请求和实时流语义正确。
- 检查点 D（任务 8）：跨包与全部必要检查通过，文档和变更范围完整。

## 6. 验证命令与执行记录

实施前最小基线及每任务目标测试先行；最后统一执行一次必要的全仓检查，只有新修改或失败才重跑扩大范围。

```console
go test ./internal/integration
go test ./...
go test -race ./...
go vet ./...
gofmt -l .
python -m pytest scripts/test/test_unix_layout_security.py -q
git diff --check
```

race 使用可用的平台编译工具链，与发布 CGO-free 构建分开；Windows 缺编译器时如实记录并由对应 CI 覆盖，不能把未执行写成通过。Unix 原生/权限测试由 CI 的临时主机执行，不在开发机提权运行。按 CI 固定版本执行 golangci-lint；不为本任务自动升级工具链或安装系统依赖。

在独立命令进程中执行六目标构建，输出到不提交的临时目录：

```powershell
$buildOutput = Join-Path ([System.IO.Path]::GetTempPath()) ('mihari-209-build-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $buildOutput | Out-Null
$env:CGO_ENABLED = '0'
foreach ($targetOS in @('windows', 'linux', 'darwin')) {
    foreach ($targetArch in @('amd64', 'arm64')) {
        $env:GOOS = $targetOS
        $env:GOARCH = $targetArch
        $suffix = if ($targetOS -eq 'windows') { '.exe' } else { '' }
        $targetFile = Join-Path $buildOutput ('mihari-' + $targetOS + '-' + $targetArch + $suffix)
        go build -o $targetFile ./cmd/mihari
        if ($LASTEXITCODE -ne 0) { throw "Build failed: $targetOS/$targetArch" }
    }
}
```

需要覆盖率比较时，在独立基线 checkout 和任务分支分别生成临时报告，比较受影响包及全仓；不提交报告，不凭空设新百分比门槛。报告实际执行的测试、不能验证的目标、原因及 CI 替代证据。

## 7. 完成标准

- [x] 所有已确认 Q1–Q12 行为都有对应断言，尤其 silent 被动支持与 Q12 的非回滚语义。
- [x] 受控修改失败恢复可确认；外部采纳失败不发送撤销内核的请求。
- [x] 普通与 trusted 每次启动都不会从旧 runtime 文件复活旧 level。
- [x] TUI 可以区分 saved/live，且状态-only 变化可及时刷新。
- [x] 面板 Level 筛选不会被误当全局修改；Web/内部实时流都能观察 debug。
- [x] 文件捕获按真实级别过滤，原文、大小限制、导出及资源回收保持。
- [x] 必要测试、race、vet、格式、本地安全检查与六目标无 CGO 构建有真实结果；Unix 原生 CI 检查随 PR 跟踪。
- [x] 仅修改 #209 相关文件，不包含 CHANGELOG、临时报告、真实配置或第三方面板资产。
- [x] 用户另行要求提交/PR 后才执行；合并仍需用户确认。

## 8. 执行记录

实现包含保存值到生成配置的统一映射、每次子进程启动前准备、受控在线事务和失败补偿、2 秒外部观察与未保存状态、被动 silent、真实捕获级别、TUI 同步展示、Web 单字段日志 PATCH，以及独立实时流。

新增回归覆盖普通/trusted 发布与回滚、实际内核值不同于保存值、校验期间 generation/缓存内容变化、无法确认补偿时阻止继续修改、保存后取消与幂等重放、失败外部采纳后的最新值重试、失败受控修改保留未保存保护、daemon 重新装配和子进程重启、多个 TUI 模型刷新、截断 JSON 级别和嵌套字段反例。跨包 fake mihomo 测试覆盖 silent 前后的已有 WebSocket、重连、HTTP 流与查询参数。

基线 `6bd40d1` 与功能分支的相关包覆盖率比较如下（百分比为 statements，独立 checkout，未提交临时报告）：

| 包 | 基线 | 实现后 |
| --- | ---: | ---: |
| runtime | 76.1% | 76.5% |
| logging | 87.0% | 87.1% |
| core | 77.1% | 77.2% |
| supervisor | 84.3% | 84.3% |
| subscription | 82.8% | 82.8% |
| web | 83.7% | 84.0% |
| app | 77.3% | 77.4% |
| tui/pages/system | 86.7% | 86.7% |
| tui/pages/logs | 79.7% | 80.3% |
| tui | 85.4% | 85.5% |

本地使用 Windows / Go 1.26.5；Unix 原生权限检查由 CI 临时主机承担。`python -m pytest scripts/test/test_unix_layout_security.py -q`：74 passed、4 skipped（当前平台不适用的原生场景）。未运行真实订阅、真实 mihomo 或系统服务测试。

首次全仓 race 中，既有 `TestCredentialRefresh_LongLivedSessionRecoversAfterStopDeleteStart` 在删除临时 control.token 时遇到 Windows 文件占用；该测试在功能分支与基线各自单独连续运行三次均通过。最终 `go test -race ./...` 全仓重跑通过，未为此修改凭据逻辑，不据此声称首次失败已证明属于基线问题。

最终版本的 `go vet ./...`、golangci-lint v2.12.2（0 issues）、`gofmt -l .`、`git diff --check` 与六目标 CGO-free 构建通过。全仓普通测试及跨包集成测试通过；测试仅使用临时目录与 fake controller/child。
