# Phase 4 后台与客户端边界诊断 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 完善既有后台调度、Web gateway、CLI/TUI 本地任务、stream、关闭和日志自身失败的诊断责任，并形成逐入口验收记录。

**Architecture:** 复用 Phase 1 的操作 ctx 和 Phase 2 的安全错误/Reporter/责任标记，在现有 owner 处补充记录与必要 cause 保留。后台与流式生命周期保留原有控制流程；只读/认证前缺少跨 IPC ID 的限制如实记录，不通过协议扩展消除。

**Tech Stack:** Go 1.26.0 / go1.26.5；context、slog、errors；既有 scheduler、WebSocket、TUI worker、FailureReporter。

**Spec:** [已通过设计](../specs/2026-09-09-operation-logging-diagnostics-design.md) §4、§7–9、§10 Phase 4；前置 [Phase 1](2026-09-09-operation-logging-phase-1.md)、[Phase 2](2026-09-09-operation-logging-phase-2.md)、[Phase 3](2026-09-09-operation-logging-phase-3.md)。

## Global Constraints

- 复用 `internal/logging`、`slog`、现有脱敏、轮转及生命周期管理，不引入第三方日志或 tracing 依赖。
- 保持 `/v1` DTO、JSON envelope、错误码、CLI 退出码和持久化格式；不向公开 Message/Details 填入内部 cause。
- 保持 daemon 单写入者、原生 IPC 和既有日志目录/权限边界。
- 不修改 mihomo，不扩展真实订阅、真实核心、系统服务或其他 testenv 操作。
- 不在本设计中实现 #197 的 setup 提示改进。
- 覆盖后台调度、Web gateway、CLI/TUI 本地任务、stream、关闭流程和 logger 自身故障。Web 写操作遵循现有 mutation coordinator，不能绕过安全边界；需要新增跨 IPC 元数据或 CLI 持久日志的部分先独立设计。
- 不新增 header/DTO、CLI 文件日志、trace系统、持久化格式、队列、日志消费者goroutine或共享核心的假operation关联。
- 审计只补充必要诊断/错误保留，不重写生命周期、重试、权限或业务状态。必须扩大scope的P0/P1先汇报；未经授权不commit/push/PR。

## 0. 基线、接口与文件边界

取证基线 `d76e09d`；实施前在既有 worktree/独立分支确认 Phase 1–3 已落地，读取根 AGENTS.md、.github/CONTRIBUTING.md、README.md、设计和前置计划。

共用接口不再另起体系：`diagnostics.Reporter func(context.Context,diagnostics.Record)`，Record 的 Component/Event/Level/Err，Wrap、MarkReported、AlreadyReported、FailureLevel；`logging.WithOperation/OperationMetadata` 与 NewDiagnosticReporter；client 的可选 SetDiagnosticReporter。Phase2实际执行owner已记录的错误必须保留marker，不能重新Wrap同一结果而重复诊断。

| 任务 | 允许的现有文件/owner | 测试与注入点 |
| --- | --- | --- |
| 4A scheduler | `app/runtime.go` RunScheduler装配闭包、`runtime/manager.go` reportBackground；subscription/geoip scheduler仅必要返回处理 | SchedulerOptions Snapshot/Refresh/Now/After/Jitter、geoip NeedsUpdate/Refresh；相邻scheduler tests |
| 4B Web | `web/server.go`、`proxy.go`、`app/runtime.go` webMutator | recordingMutator、webMutationRuntime、Transport/HTTPClient/wsObserver、integration/web_gateway_test.go |
| 4C stream | `control/client/runtime.go` Stream、`tui/session/session.go`、`cli/stream.go` | fake client、Backoff、session/CLI/runtime client tests |
| 4D 本地任务 | `tui/{run,installation_worker,logging_applier}.go`、`tui/ui/exportlogs.go`、已有CLI本地任务命令及root依赖注入 | exportRunner/installation worker fake、run cleanup tests、CLI dependencies |
| 4E logger/退出 | `logging/{config,rotator,runtime}.go`、`cmd/mihari/main.go`、`tui/run.go`、server现有关闭路径 | FailureReporter writer/clock、OpenLock/WriteWait、资源closer、server stream join tests |
| 文档/集成 | `docs/architecture.md` 的诊断审计表；`integration/operation_diagnostics_test.go` | Phase2真实JSON/IPC fixture，必要fake，不访问真实系统 |

只修改上表中已确认的失败路径及其相邻测试，不自动修复扫描发现的所有被忽略error。没有可观察错误返回的小接口，记录限制，不为日志扩展其业务职责。

## Task 4A：后台调度与最终错误

**Consumes:** Phase2运行时实际执行Reporter、Phase3业务cause；**Produces:** 调度入口ctx和无重复的后台诊断。

- [ ] **1. 在 app 的 scheduler 装配测试注入两次 Refresh 与可控时钟/ID工厂。** 第一次失败、第二次成功，断言实际backend执行两次且日志ID不同。单次Refresh内部fallback使用同一个ID。先让缺少ctx/记录的断言Red，而不是更改Scheduler.Run业务结果。
- [ ] **2. 保留既有调度ID来源与生成时机。** subscription/GeoIP在每一次调用Manager.Refresh/Update前生成现有ID，立即绑定ctx；不在循环外生成一次，不跨下一轮退避刷新复用已缓存的失败ID。现有时间戳策略可通过app局部可注入函数替换测试时钟，不引入新的全仓ID服务。

```go
operation := runtimeapi.Operation{ID: generatedID, Source: "scheduler"}
refreshContext = logging.WithOperation(refreshContext, logging.OperationMetadata{
    ID: operation.ID, Name: "subscription.refresh",
})
_, err := manager.RefreshSubscription(refreshContext, operation, profileID)
return err
```

上段 generatedID/profileID 是现有Refresh装配闭包中的ID与订阅ID局部变量；保留原始格式，绝不拿URL代替profileID。没有新的随机源；如复用已有随机ID路径，必须保持可注入、失败不使用常量占位，按现有fallback处理。

- [ ] **3. 避免多层重复。** 调度Refresh失败已由Manager记录时，scheduler只维持既有失败计数/退避，不再打详细ERROR。app对scheduler.Run返回值不再盲目忽略：按正常取消/真实失败分类，真正运行循环失败由其生命周期owner记录一次。两个scheduler仍都join，原Run返回/取消流程保持，不能因加日志提前结束另一个scheduler。
- [ ] **4. runtime增加ctx版本 `reportBackgroundContext(ctx,component,err)`，旧调用点按owner逐项接入。** 新Reporter存在时按FailureLevel记录；nil新Reporter时保留原OnBackgroundError回调，不两条出口都调用。只要error组合还有真实故障，就不能因contains canceled/deadline过滤整条错误。正常shutdown仍静默。
- [ ] **5. 具体回归矩阵：** subscription失败→退避→成功；GeoIP一次Refresh失败仍下一轮检查；循环本身失败；正常取消；ctx有效时upstream deadline；两项任务同时退出均完成清理；已有已报告错误不重复。运行 app/runtime、subscription/geoip scheduler目标测试后相关包/race。

## Task 4B：Web mutation、代理读取和WebSocket

**Files:** web server/proxy/app webMutator及对应测试；**Consumes:** Phase2 marker/Reporter，Phase3 mutation owner。

- [ ] **1. mutation回归Red。** 用既有 webMutationRuntime fake 返回未标记的APIError/普通error，注入内存reporter，验证app适配器ctx带其创建的ID；用Phase3真实Manager结果验证Web不重复记录已标记错误。保留全部allowlist/鉴权测试。
- [ ] **2. ID绑定在实际生成者webMutator。** SelectProxy/CloseConnection/CloseAllConnections现用时间戳，TUN现用newWebOperationID（随机失败退回时间戳），保留这些策略与fallback，不将这轮变为ID生成器改造。只增加用于测试的局部now/read-random注入，生产默认仍现有时间/随机来源。

在webMutator生成operation之后，用同一operation.ID绑定调用Manager的ctx；其返回前对尚未报告的错误执行下列公共owner逻辑。成功摘要也在这个知道ID的入口记录。web.Server拿不到派生ctx的ID时不伪造它，不新增请求/响应字段传回浏览器。

```go
if err != nil && reporter != nil && !diagnostics.AlreadyReported(err) {
    if level, emit := diagnostics.FailureLevel(ctx, err); emit {
        reporter(ctx, diagnostics.Record{Component: "web", Event: "mutation.failed", Level: level, Err: err})
        err = diagnostics.MarkReported(err)
    }
}
return err
```

- [ ] **3. Server对解析/不支持操作只记录适当DEBUG，对未到adapter的意外故障安全兜底。** 不变更既有APIError→400、普通故障→502及allowlist拒绝语义。所有名称是静态事件，不记录browser auth、controller address/secret、请求URI/query/body。
- [ ] **4. reverse proxy ErrorHandler 在返回现有502前记录安全诊断。** 给ProxyOptions/Server.Options增加可选Reporter并由app注入；请求原有ctx仅本地关联，无ID合法，不发新header。保留Rewrite/ModifyResponse凭据隔离；不打印URL或response header。
- [ ] **5. WS双向relay。** 以现有两个relay都结束后的owner为唯一详细记录位置；首个真实失败记录一次，其诱发的取消/正常close不再记录ERROR。握手失败在握手owner记录，不能在两个relay和父handler各打一条。保留双向join、ctx终止、带限读取和goroutine回收。
- [ ] **6. 扩展既有测试：** `TestGatewayWebSocketHandlerWaitsForBothRelaysAfterNormalClose`、UpstreamClose/ClientClose/ContextCancel/HandshakeFailuresAreSanitized；正向依旧转发、凭据不泄露，错误/正常close日志数量与级别可判定。运行 web/app目标测试和 integration/web_gateway_test.go，之后race。

## Task 4C：控制stream与TUI重连诊断

**Files:** control/client/runtime.go、tui/session/session.go、cli/stream.go及相邻测试。
**Consumes:** 可选client Reporter与Phase2本地/远端错误来源约定；**Produces:** stream错误记录，保留流协议和重连决策。

- [ ] **1. 复用现有stream fake添加日志断言。** 先证明关键失败缺少诊断再最小接入。客户端Stream不新增协议ID；只消费调用方已有ctx元数据，因此不承诺daemon端stream与客户端跨IPC共享ID。
- [ ] **2. TUI session Options增加可选Reporter，由现有Run资源owner注入。** superviseStreams保留新鲜Status决定daemon重连的优先级：旧stream错误可作为局部诊断，但不得抢占Status错误的公开分类。记录实际失败/恢复转变，轮询成功与每个traffic/memory事件不打印INFO。
- [ ] **3. 日志责任在session与client之间明确选择。** client独立调用时自己报告本地stream transport/decode故障；session使用一个不重复报告相同失败的内部调用路径或消费reported marker，callback造成的正常终止不能标为transport故障。不要改变Stream公开签名/事件结构；只在内部返回error时保留来源与报告责任。

`errOneStreamEvent` 是CLI非follow模式的正常结束信号。通用client不能引用cli包识别它，也不能把任意callback error都打ERROR：callback返回错误原样交调用方owner，CLI先处理此sentinel，真正输出失败沿既有Exit/renderer处理。nil客户端reporter仍保持原行为。

- [ ] **4. 保留CLI输出契约。** 非流式JSON维持既有单envelope；`--json --follow` 原本逐条输出StreamEvent，继续事件流，不加诊断行、不把流改成单envelope。CLI无独立日志文件，只有已有安全输出或显式注入的诊断出口。
- [ ] **5. 回归矩阵及命令：**

| 既有测试/场景 | 保留断言 | 日志断言 |
| --- | --- | --- |
| TestRuntimeClientStreamReadsStableEvents | event schema和读取顺序 | 不为每个event打印日志 |
| TestSession_StatusCategoryTakesPrecedenceOverOldStream | 新鲜status错误决定重连 | 旧stream失败不覆盖它 |
| CoreRestartReconnectsStreamsWithoutDaemonReconnect | stream reopen、保留snapshot | 诊断不改变daemon健康判断 |
| ConsecutiveStreamFailuresIncreaseBackoffUntilStreamDataRecovers | 原backoff计数、恢复清零 | 每次实际失败/恢复有合适事件，无逐层重复 |
| OneUpstreamPerStreamAndStopsAllGoroutines | old producer全部join后重开 | 不新增日志goroutine，关闭后不迟到写 |
| CLI非follow与输出writer失败 | sentinel成功、真实writer失败原退出码 | sentinel不ERROR，JSON/文本输出无日志污染 |

运行 `go test ./internal/control/client ./internal/tui/session ./internal/cli -count=1`，再race。IO/事件限额和重连状态机一律不因诊断调整。

## Task 4D：CLI/TUI本地任务与异步结果

**Files:** tui安装/导出/logging applier/run，CLI本地service/self-update等已有命令的owner及相邻测试；文件以现有命令实现位置登记，不新增命令。
**Consumes:** Phase1 OperationMetadata、Phase2 Reporter；**Produces:** 已有本地任务的诊断，不建立CLI文件资源。

- [ ] **1. 分别用已有installation worker、exportRunner、logging applier fixture补充失败结果与日志断言。** 每次逻辑任务在owner建立ctx元数据，跨异步消息传值，不保存request ctx到Model/error。每个worker仍由既有Run/cleanup拥有；不新增后台执行者。
- [ ] **2. 本地任务ID只用于诊断，不能改变业务执行条件。** 复用已有任务/operation ID；无ID入口可在owner用已有可注入随机机制生成。如纯诊断ID生成失败，记录安全摘要并执行原任务且无ID，不生成常量、不把日志关联失败升级为安装/导出业务失败。
- [ ] **3. 每个本地owner只记录其负责的最终失败/恢复。** 安装、导出传输、日志配置应用不得互相套用“当前操作”。UI结果展示用DEBUG或不记，不改变表单文案和public API；#197不在此实施。导出文件内容/目录/凭据不进入原始日志，使用静态事件与可展示摘要。
- [ ] **4. CLI诊断注入只借用owner提供的Reporter。** 本地任务失败保留现有返回/退出码，未注入就走既有渲染；不能借用mihari-tui.log或新建mihari-cli.log。命令context元数据不进入settings/metadata。
- [ ] **5. 验收：** 两项并发任务ID隔离、乱序结果仍携带各自ID、取消正常、真实失败一次记录、logger不可用任务仍可按既有规则执行、export/installation/applier在退出前join。运行各目标tests及tui包race；fake不执行真实安装/服务操作。

## Task 4E：关闭顺序与logger自身故障

**Files:** 既有logging/config/rotator/runtime、daemonLoggingResources.Close、tui executeRunShutdown/newRunCleanup/finishRun、server shutdown相邻测试。
**Consumes:** 已有FailureReporter及资源owner；**Produces:** 失败可查且无递归、无新写入权限。

- [ ] **1. 先扩展已有测试证明漏记点。** 优先检查以下fixture，不先改关闭顺序：`TestRunShutdown_LifecycleOrder`、`TestRunCleanup_NetworkTerminateDoesNotDropDiskWorkers`、`TestServe_JoinsHijackedStreamBeforeReturning`、daemon资源close-order；把某个closer换为返回注入error的fake，断言其他资源仍关闭、原返回语义保持且安全诊断可追查。
- [ ] **2. 明确两种出口。** logger仍可用且任务worker已结束前，owner用统一诊断Reporter；logger写入/关闭自身失败使用已有 `logging.FailureReporter` 或TUI独立安全摘要出口，不能经同一个坏logger再次记录。读取Close/Shutdown返回error不等于授权改写生命周期或公开退出契约。
- [ ] **3. 给现有FailureReporter增加下列测试，而非新建第二套限流。** 文件放 `logging/rotator_test.go` 或相邻新的diagnostics测试，复用现有1秒窗口和可注入时钟。

```go
type countingFailedWriter struct { calls int }
func (w *countingFailedWriter) Write(p []byte) (int, error) {
    w.calls++
    return 0, errors.New("diagnostic output unavailable")
}
func TestFailureReporter_OutputFailureDoesNotRecurse(t *testing.T) {
    writer := &countingFailedWriter{}
    fixed := time.Unix(100, 0)
    reporter := NewFailureReporter(writer, NewRedactor("fixture-secret"), func() time.Time { return fixed })
    for i := 0; i < 20; i++ { reporter.Report(FailureClass("write"), errors.New("fixture-secret")) }
    if writer.calls != 1 { t.Fatal("failed fallback must remain rate limited without recursion") }
}
```

若首次已通过，记录为覆盖补充，不伪造Red。只有日志丢失/递归/重复等目标断言确实失败才最小修正既有owner/reporter。保留现有路径脱敏、CRLF净化和错误输出自身失败时的安全忽略理由。

- [ ] **4. 扩展 `TestRotatingWriter_FailureStormRateLimitedRedacted`、WriteReturnsUnlockFailure、ApplyReportsUnlockFailure 和TUI bootstrap/cleanup脱敏测试。** 注入锁超时、writer失败、unlock失败、Close失败；无raw secret/路径、有限fallback记录、原丢弃/返回计数语义不变。不为metadata只增日志字段而调整OS权限/轮转算法。
- [ ] **5. 验证所有stream/worker/child先按原顺序结束，capture/log资源按既有顺序关闭；最后日志资源关闭后的错误用独立出口。** 不能让defer日志调用在logger已关闭之后悄悄丢失，也不能为延长日志寿命留下后台worker。运行相关目标测试、race以及既有shutdown集成。

## Task 4F：逐入口审计与最终验收

- [ ] 扫描已授权模块的error路径并人工判定owner，不以字符串搜索数量当完成证据：

```bash
rg -n 'return .*err|_ = .*\(|\.Error\(|\.Warn\(|reportBackground' internal/runtime internal/app internal/web internal/tui internal/cli internal/control internal/logging
```

- [ ] 在 `docs/architecture.md` 诊断章节登记每条实际入口：失败源→处理策略→最终owner→级别→ID来源/缺失原因→脱敏→单元/集成测试→剩余限制。每个“已覆盖”必须对应具体测试断言；需要新协议/CLI日志/业务重构的条目标为范围外，不纳入本轮实现。
- [ ] 明确保留的限制：认证前/只读请求无统一跨IPC ID；普通CLI没有持久化日志；原始mihomo共享输出不带请求ID；现有无error返回的cleanup只能验证清理结果而不能推断不存在失败。这些限制不能被“全链路已覆盖”掩盖。
- [ ] 运行 Go1.26.5 的目标测试→相关integration→`go test ./...`、`go test -race ./...`、`go vet ./...`、修改文件gofmt、Phase1 Task4六目标CGO-free构建。全仓环境基线失败明确列出，不排除测试、不更改真实用户/系统状态。
- [ ] 检查diff无协议/CLI契约/持久化格式/业务生命周期/权限扩展，无CHANGELOG变更、临时产物或未经授权的commit/push。按各模块证据交付，不承诺“所有可能错误”绝对穷尽。

本计划仅安排既有Phase4范围；subagent审核必须按上述边界判断，范围内修正后复审直至PASS，范围外致命P0/P1先向用户报告。计划通过不表示代码已实现或测试已执行。
