# Phase 2 错误链与首条诊断链路 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 满足 #216 的 settings 错误链、实际 warning、请求失败诊断和启动兜底验收，并以 Logging 设置更新验证 TUI/客户端/daemon 的 operation ID 关联。

**Architecture:** 在小型 `internal/diagnostics` 包中分离安全公开错误、内部 cause 和诊断记录责任。实际 mutation 执行收集 warning，释放业务锁后记录，再发布缓存结果；控制 API 仅为尚未记录的失败兜底。沿用 Phase 1 的 ctx 元数据和现有日志资源。

**Tech Stack:** Go language 1.26.0 / toolchain go1.26.5；标准库 errors/context/slog；既有 logging、runtime、原生 IPC。

**Spec:** [已通过设计](../specs/2026-09-09-operation-logging-diagnostics-design.md) §6–8、§10 Phase 2；前置 [Phase 1](2026-09-09-operation-logging-phase-1.md)。后续 [Phase 3](2026-09-09-operation-logging-phase-3.md)、[Phase 4](2026-09-09-operation-logging-phase-4.md) 消费本计划的接口。

## Global Constraints

- 复用 `internal/logging`、`slog`、现有脱敏、轮转及生命周期管理，不引入第三方日志或 tracing 依赖。
- 保持 `/v1` DTO、JSON envelope、错误码、CLI 退出码和持久化格式；不向公开 Message/Details 填入内部 cause。
- 保持 daemon 单写入者、原生 IPC 和既有日志目录/权限边界。
- 不修改 mihomo，不扩展真实订阅、真实核心、系统服务或其他 testenv 操作。
- 不在本设计中实现 #197 的 setup 提示改进。
- 本阶段不补齐其他领域 cause，不增加只读请求/header 关联、CLI 持久日志、tracing、日志队列或新的后台 worker。
- 用户未授权时不提交、推送或创建 PR；不得扩大 scope。若发现必须扩展范围的致命 P0/P1，先汇报。

## 0. 前置状态、文件范围与共同接口

在 `/home/kinema/dev/mihari/.worktrees/issue-216` 的独立分支执行。计划取证基线为 `d76e09d`，实施前须确认 Phase 1 已完成。先读根 AGENTS.md、.github/CONTRIBUTING.md、README.md、设计及 Phase 1；不把计划中的代码当作已存在代码。

| 文件 | 变更职责 |
| --- | --- |
| 新建 `internal/diagnostics/error.go`、`record.go`、对应 `_test.go` | 内部错误、责任标记、级别策略；不负责 IO |
| 新建 `internal/logging/diagnostics.go`、`diagnostics_test.go` | 有界、安全的诊断格式化和现有 logger 适配 |
| 新建 `internal/runtime/diagnostics.go`、`diagnostics_test.go` | 每次实际执行的 warning 收集与最终记录 |
| 修改 `internal/runtime/manager.go`、`settings.go`、`logging.go` 及相邻测试 | cause 保留、执行回调、settings 链路 |
| `internal/runtime/{onboarding,sysproxy,tun,tun_trusted,subscription,geoip,panel,preferences,provider}.go` | 仅现有 doOperation 回调/私有 settings helper 的 ctx 参数机械适配；对应测试只适配签名和本次 warning 契约 |
| `internal/app/runtime.go`、`internal/daemon/run.go`、`cmd/mihari/main.go` 及相邻测试 | 注入 reporter，启动安全兜底与资源 owner |
| `internal/control/server/server.go`、`runtime.go`、`logging.go`、新建 `diagnostics.go` 及测试 | 安全错误映射前的兜底；其他 endpoint 文件仅替换共同错误出口 |
| `internal/control/client/client.go`、`runtime.go` 及测试 | 可选 reporter，首条 Logging mutation 的客户端日志 |
| `internal/tui/run.go`、`pages/system/model.go` 及 logging 相关测试 | reporter 装配、Logging 异步结果元数据 |
| 新建 `internal/integration/operation_diagnostics_test.go` | 临时本地 IPC 的端到端诊断测试 |
| `docs/architecture.md`、根 `AGENTS.md` 的错误处理约定、设计中的审计登记 | 仅同步日志责任规则；不扩大写入权限 |

通用签名适配不是其他模块日志完善：不在这些文件顺带更改 retry、rollback、错误码、状态、ID 或下载/校验行为。生产文件清单外的必要变更先核对是否属于同一共同出口，不默认授权扩展功能。

### Phase 2–4 共用的最小接口

```go
// package diagnostics; imports context, log/slog, control/protocol.
type Record struct {
    Component string // code constant
    Event     string // code constant
    Level     slog.Level
    Err       error  // internal only; never serialize this Record
}
type Reporter func(context.Context, Record)

func Wrap(api protocol.APIError, cause error) error
func MarkReported(err error) error
func AlreadyReported(err error) bool
func FailureLevel(ctx context.Context, err error) (slog.Level, bool)

// package logging; nil logger returns a nil reporter.
func NewDiagnosticReporter(logger *slog.Logger, redactor *Redactor) diagnostics.Reporter

// package runtime; Options gains DiagnosticReporter diagnostics.Reporter.
func (m *Manager) doOperation(ctx context.Context, key string,
    execute func(context.Context) (any, error)) (any, error)

// package client; configured before the first request, like SetRedactor.
func (c *Client) SetDiagnosticReporter(reporter diagnostics.Reporter) error
```

`diagnostics` 可以依赖稳定 protocol DTO，不依赖 runtime、server 或 logging；`logging` 依赖 diagnostics，避免循环。操作元数据仍由 Phase 1 ctx API 提供，错误对象不保存 ctx/logger/request。

## Task 1：安全错误封装与报告责任标记

**Files:** `internal/diagnostics/error.go`、`error_test.go`。
**Produces:** Wrap、MarkReported、AlreadyReported；供后续任务及 Phase 3/4 使用。

- [ ] **1. 用可编译空壳开始。** Wrap 暂时返回 api，MarkReported 暂时原样返回，AlreadyReported 暂时返回 false。然后添加下列行为测试，确认 Red 来自原因/标记丢失而非编译错误。

```go
func TestWrap_PreservesCauseAndPublicClassification(t *testing.T) {
    cause := &os.PathError{Op: "rename", Path: "/fixture/private.yaml", Err: os.ErrPermission}
    public := protocol.APIError{Code: protocol.CodeDataFailure, Message: "persist settings"}
    err := Wrap(public, cause)
    var api protocol.APIError
    var pathErr *os.PathError
    if !errors.Is(err, os.ErrPermission) || !errors.As(err, &pathErr) || pathErr != cause {
        t.Fatal("cause lost")
    }
    if !errors.As(err, &api) || api.Code != public.Code || err.Error() != public.Message {
        t.Fatal("public classification changed")
    }
    if AlreadyReported(err) { t.Fatal("unreported failure marked reported") }
    marked := MarkReported(err)
    if !AlreadyReported(fmt.Errorf("operation: %w", marked)) || !errors.Is(marked, cause) {
        t.Fatal("report marker must survive contextual wrapping")
    }
    joined := errors.Join(marked, errors.New("new failure"))
    if AlreadyReported(joined) { t.Fatal("a reported child must not suppress a new aggregate") }
}
```

- [ ] **2. Red：** `go test ./internal/diagnostics -run '^TestWrap_' -count=1`。
- [ ] **3. Green：** 用私有 `failure{api protocol.APIError; cause error}` 实现 `Error() string { return e.api.Message }`、`Unwrap() []error { return []error{e.api,e.cause} }`，nil cause 时只返回 api 子项。Wrap 复制公开字段，不能把 cause 文本拼入 Message/Details；已公开 Details 保持原值且不得被诊断代码修改。

```go
type reported struct { cause error }
func (e reported) Error() string { return e.cause.Error() }
func (e reported) Unwrap() error { return e.cause }
func MarkReported(err error) error {
    if err == nil { return nil }
    return reported{cause: err}
}
```

AlreadyReported 只沿单一 `Unwrap() error` 链查找外层 reported，限制 32 层；碰到多分支错误（包括新的 failure 封装）即 false，不因某个旧子错误已经记录就压制新 aggregate。同一次已记录错误应保留外层 marker 原样传播；如果重新构造不同公开错误/合并新失败，这是新的诊断结果，不转移旧 marker。

- [ ] **4. 增加边界断言：** nil、嵌套 API cause 外层分类优先、`errors.Join` 多个原因、单链循环/深度超过 32（AlreadyReported 有界返回 false）；Wrap 及 marker 均不改变 `errors.Is/As`。运行 `go test ./internal/diagnostics -count=1`。

## Task 2：级别策略与安全日志适配

**Files:** `diagnostics/record.go`、`record_test.go`、`logging/diagnostics.go`、`diagnostics_test.go`。
**Consumes:** Task 1、Phase 1 OperationMetadata；**Produces:** Record、Reporter、FailureLevel、NewDiagnosticReporter。

- [ ] **1. 添加表驱动级别测试。** FailureLevel 的空壳返回 ERROR,true；下表中不符合 ERROR 的行先产生行为 Red。

| 场景 | 输入 | 预期 |
| --- | --- | --- |
| 无错误 | nil | 不记录 |
| 正常取消 | ctx 已取消且 err 的取消分支无其他实质故障 | 不记录 |
| 上游超时 | ctx 仍有效，err 为 DeadlineExceeded | ERROR |
| deadline 与磁盘故障聚合 | ctx 已过期，errors.Join(deadline,PathError) | ERROR，不能整体抑制 |
| 参数/预期冲突 | 已有 invalid_argument、revision_conflict、sysproxy/tun conflict 分类 | DEBUG |
| settings/data/internal/network/upstream 实际失败 | 相应分类或未知 error | ERROR |

```go
func TestFailureLevel_UpstreamDeadlineIsNotCancellation(t *testing.T) {
    level, emit := FailureLevel(context.Background(), context.DeadlineExceeded)
    if !emit || level != slog.LevelError { t.Fatal("upstream timeout was suppressed") }
    ctx, cancel := context.WithCancel(context.Background()); cancel()
    level, emit = FailureLevel(ctx, errors.Join(context.Canceled, os.ErrPermission))
    if !emit || level != slog.LevelError { t.Fatal("real failure hidden by cancellation") }
}
```

- [ ] **2. 建立内存 logger 验证真实输出。** 让 reporter 接收 Wrap 后的 PathError、单链/多链、注册 secret、完整 URL、超长文本、循环 unwrap、配置解析错误。断言原始原因的安全描述可辨识，公开 Error() 仍只有标题；日志不含路径/凭据/URL/配置内容，输出有上限，ctx ID 存在且 JSON 可解析。先运行 `go test ./internal/diagnostics ./internal/logging -run 'Diagnostic|FailureLevel' -count=1` 并记录 Red。
- [ ] **3. 实现只在输出边界使用的私有格式化器。** 单/多分支遍历最多 32 层、64 节点，避免循环/重复节点；不以普通 fmt `%+v` 输出结构体。PathError 取 Op 与底层原因，不取 Path；`url.Error` 不取 URL；配置/YAML 解析、命令执行输出等可能含内容的错误采用安全类型摘要，不展开不可信原文。其他原始文本须经过已注册秘密与通用 redactor，未能安全判断时保留类型/静态操作摘要。每段先脱敏再截断，总计最多 4096 UTF-8 字节，不能先截断后令 secret/URL 匹配失效。任意 Error()/Unwrap 的无限阻塞不在本阶段可保证范围，不启动 watchdog goroutine。

```go
// Adapter body outline; diagnosticText is the bounded formatter above.
return func(ctx context.Context, record diagnostics.Record) {
    if !logger.Enabled(ctx, record.Level) { return }
    logger.LogAttrs(ctx, record.Level, record.Event,
        slog.String("component", record.Component),
        slog.String("cause", diagnosticText(record.Err, redactor)))
}
```

nil logger 返回 nil；redactor 不可为 nil，装配缺失时以安全摘要格式化，不输出 raw cause。既有 JSON handler 再执行一次脱敏。此 reporter 与 `logging.FailureReporter`（日志 IO 自身失败出口）职责不同，不替换后者。
- [ ] **4. 重跑目标测试及 race。** 记录级别按用户现有配置过滤；“已报告”表示该边界已承担记录责任，不承诺磁盘故障或用户过滤下必然持久化。

## Task 3：实际执行 owner、缓存与 settings warning

**Files:** runtime diagnostics/manager/settings/logging 及文件表中的机械 ctx 适配文件、相关测试。
**Consumes:** Reporter、Phase 1 ctx；**Produces:** Options.DiagnosticReporter 和新的 doOperation callback 形式。

- [ ] **1. 在既有 `newTestManager` / `recordingLoggingRuntime` fixture 添加实际行为回归。** 使用真实临时目录、现有 fake；下列 reporter 为测试收集器，可用局部 mutex 保护记录。

```go
func TestSettingsDiagnostic_CauseSurvives(t *testing.T) {
    cause := errors.New("injected replace failure")
    manager := newTestManager(Options{
        Settings: config.Defaults(), SettingsPath: filepath.Join(t.TempDir(), "settings.yaml"),
        Logging: &recordingLoggingRuntime{dir: "logs"},
        SaveSettings: func(string, config.Settings) (config.CommitResult, error) {
            return config.CommitResult{}, cause
        },
    })
    _, err := manager.UpdateLogging(context.Background(), Operation{ID: "settings-cause"},
        LoggingUpdate{Level: stringPointer("debug")})
    if !errors.Is(err, cause) { t.Fatal("settings cause was lost") }
    var api protocol.APIError
    if !errors.As(err, &api) || api.Code != protocol.CodeDataFailure || api.Message != "persist settings" {
        t.Fatal("public error changed")
    }
}
```

扩展 `TestManagerSettings_PostCommitWarning` / Logging warning fixture：保存返回 `Committed:true, Warning: injected`，结果仍成功、revision 增加、内存生效，记录 WARN 且 `errors.Is(record.Err,injected)`；不再要求被替换后的固定 error 字符串。新增 reporter 内以有界 ctx 调用 `manager.LoggingStatus` 验证记录时已释放业务锁，失败应表现为 context 超时而非悬挂。

- [ ] **2. Red：** `go test ./internal/runtime -run 'SettingsDiagnostic|PostCommitWarning' -count=1`。新 Options 字段先声明、尚未接线，以断言 Red 代替编译失败。
- [ ] **3. 调整 doOperation 的执行包装。** 回调变为 `func(ctx context.Context)(any,error)`；每个现有调用点仅增加形参使闭包内 ctx 使用新的执行 ctx。既有 key、空 key 直执行、缓存容量和身份规则不变。

每次实际执行创建私有 operationDiagnostics 收集器，由执行 owner 放入私有 ctx key：只持有本次有限 settings 写入的 warning 和不可变操作元数据；不是 Manager 全局队列，没有消费者 goroutine。新增私有 `collectWarning(ctx,component,event string,err error)`，append 只做内存操作且锁保护；执行 callback 必须等待其使用该 scope 的任务结束后返回。独立嵌套 doOperation 创建新收集器，不能把子操作 warning 混入父操作。

```go
// All three actual-execution branches call this local wrapper.
executeOnce := func() (any, error) {
    executionCtx, batch := newOperationDiagnostics(ctx, key)
    result, err := execute(executionCtx) // callback defers release its business locks
    m.flushDiagnostics(executionCtx, batch)
    if err != nil && m.diagnosticReporter != nil && !diagnostics.AlreadyReported(err) {
        if level, emit := diagnostics.FailureLevel(executionCtx, err); emit {
            m.diagnosticReporter(executionCtx, diagnostics.Record{
                Component: "runtime", Event: "operation.failed", Level: level, Err: err,
            })
            err = diagnostics.MarkReported(err)
        }
    }
    return result, err
}
```

私有辅助签名：`newOperationDiagnostics(ctx context.Context,key string)(context.Context,*operationDiagnostics)`；`(*Manager).flushDiagnostics(ctx context.Context,batch *operationDiagnostics)`。metadata 取现有 key 的第一个冒号前后两部分（SplitN 2，ID 内允许冒号），操作名称用既有 prefix 的静态映射；未知 prefix 不当作动态日志事件。回调不得生成或改变 operation ID。已从 DTO 绑定的 ID 必须与此执行 key 一致；执行 key 是现有去重身份，修正仅日志 ctx。

`entry.result/err` 写入及 `close(entry.done)` 必须在 executeOnce 返回之后；缓存读取或等待者不调用 reporter。无 ID 和缓存满后的直接执行也走 executeOnce。reporter 为 nil 时不标记；已取消等待者不借用执行者的结果报告状态。

- [ ] **4. settings helper 增加 ctx。** `saveSettingsCandidate(ctx,candidate)`、`updateSettings(ctx,update)`、`restoreSettings(ctx,settings)` 调用链机械适配。保存失败改为 `diagnostics.Wrap(existingAPI, err)`；返回值矛盾时仍保持既有 data_failure，若 err 非 nil保留它，若 err nil 用静态的“invalid commit outcome”内部原因。Committed/Warning 判定不变。

实际 warning 返回值继续保留，同时 collectWarning；没有 scope 的私有 helper 单元调用通过返回的 CommitResult 验证 warning，不声称自己已记录。所有生产 settings helper owner 必须在执行 scope 内，包含 core、onboarding、sysproxy、tun/trusted 的现有写入/补偿路径；这部分仅为防止 warning 漏记，不顺带修复各域其他 cause。旧 OnBackgroundError 不再重复接收这些 settings warning，其他后台回调保留给 Phase 4。

- [ ] **5. Green 与缓存/锁验收。** 测试相同 ID 并发请求（用 channel 控制 save）、缓存重放、无 ID、满缓存执行、独立两个 ID 各有一条诊断；warning 不重复、收集器不串操作、报告时锁外且 cache 结果发布后完全初始化。运行 `go test -race ./internal/runtime -count=1`，所有旧 settings/补偿行为断言保留。

## Task 4：控制 API 兜底与装配

**Files:** control/server diagnostics/server/runtime/logging 与其他 endpoint 的共同出口调用，app/runtime、daemon/run、main 装配及测试。
**Produces:** 各 owner 的 `DiagnosticReporter diagnostics.Reporter` 可选字段，`(*Server).writeControlError(ctx,writer,err)`。

- [ ] **1. 注入伪 Runtime 的未分类 error，测试响应与诊断。** 复用 server logging/runtime fixtures；先添加可编译 Options 字段和方法空壳（只委托旧 writeControlError），验证新诊断记录缺失的 Red。
- [ ] **2. 新方法在既有错误映射前执行 AlreadyReported + FailureLevel + reporter。** 保留旧 `writeControlError` 的 HTTP status/envelope 实现作为内部纯映射，不重复渲染。各 endpoint 改为传已有 request.Context；不额外读取 body、不修改认证和校验顺序。缺少 request 参数的 helper 显式增加 ctx，范围仅共同错误出口。
- [ ] **3. Logging PATCH 认证/正常解码后以 DTO ID 绑定 ctx，调用 UpdateLogging 和错误出口。** 其他 mutation 的 ID 接入留 Phase 3；Phase 2 对未关联 ID 的意外错误仍能安全记录 endpoint 的静态操作上下文。不能把原始 URI/query/body 当 operation 名称。
- [ ] **4. 贯穿 app、daemon、正常及 degraded server 装配。** app 将 reporter 传 Manager，daemon 将 reporter 传 Server；normal/degraded/validation 分支都保留可选 reporter，不创建新的 logger 或文件，不改变 readiness/lifecycle。
- [ ] **5. 验证 matrix：** 未分类错误→1条 ERROR+旧 internal envelope；runtime marker→0条重复诊断；参数/冲突不刷 ERROR；nil reporter→原行为；坏 JSON/认证失败不额外解析敏感 body。运行 `go test ./internal/control/server ./internal/daemon ./internal/app -count=1`，按环境报告已知失败。

## Task 5：首条 TUI/客户端链路

**Files:** client client/runtime、tui run/system model 与相关测试。
**Consumes:** SetDiagnosticReporter、Record；没有 CLI logger 文件。

- [ ] **1. 客户端 setter 与 SetRedactor 使用同一 started/tokenMu 装配规则。** 首请求之后设置返回既有 invalid_state；nil reporter 可在启动前用于显式关闭。测试先给空壳 setter/字段再验证请求缺失诊断的 Red。
- [ ] **2. 仅 UpdateLogging 接入操作 wrapper。** 从请求 DTO 建立 ctx，静态 operation 为 `logging.update`，DEBUG 请求开始/安全响应结果，真正客户端传输/解码失败按 FailureLevel 记录；收到合法 daemon APIError 是响应结果，不能重复输出其内部 cause。不通过任意 struct 反射取得 ID，不在 Phase 2 批量接入所有 client 方法。

```go
ctx = logging.WithOperation(ctx, logging.OperationMetadata{
    ID: request.OperationID, Name: "logging.update",
})
```

日志方法和 endpoint 用静态常量；不输出 input/output/token、URL 或响应 body。保留 requestToken、credential refresh、max response limit、body.Close 及现有错误分类。客户端 transport/parse 原因若在 doRuntimeLimit 中丢失，仅沿原安全错误 Wrap 原因；不改变协议错误来源判定。

客户端在 `runtime.go` 增加私有 `runtimeOutcome{err error; remoteEnvelope bool}` 和 `doRuntimeOutcome(ctx,method,path,input,output,responseLimit)`。将现有 doRuntimeLimit 的单次请求/解码流程移入该方法；旧 doRuntimeLimit 只返回 outcome.err，因此其他client方法的行为/日志接入不变。UpdateLogging 消费完整outcome。只有成功解析合法非2xx错误envelope且Code非空时 remoteEnvelope=true；request构造/credential/transport/read/size/成功响应decode失败、无效非2xx envelope均为false。错误码不能作为来源判断。

错误响应解析增加等价私有结果helper，把“是否解析成功”与原error一起返回；保持既有401 authenticationError提示封装、读限额和关闭责任，不重读body、不修改envelope判定规则。新增配对测试：合法远端 `data_failure` 为DEBUG结果；本地无效响应同样返回 `data_failure` 但记ERROR。另覆盖有效/无效401与过大响应，确认原公开错误和认证提示不变。所有来源信息只存在client内部结果，不进入error JSON、DTO或header。
- [ ] **3. TUI Run 在创建请求前，用现有 Runtime.Logger/Redactor 注入 reporter。** 初始化失败继续既有 TUI 行为，诊断可选。System Logging action 在 closure 内绑定同一 ID，异步结果携带 `logging.OperationMetadata` 值；不得在 Model 增加 request ctx、不得更改 epoch/pending/revision/重连逻辑。结果展示最多 DEBUG，不替代 #197。
- [ ] **4. 测试 client ID 覆盖陈旧 ctx、连接失败有本地 ID、daemon响应失败 DEBUG、过大/坏 JSON 响应本地 ERROR、并发不同 ID 隔离。** 扩展 System logging tests 验证两次结果乱序仍用各自元数据、既有 epoch 行为保持。运行对应 client、TUI System 目标测试，再运行包测试。

## Task 6：启动失败、warning 和 stderr

**Files:** cmd/mihari main/main_test，必要的 app 装配测试；不改公开 CLI renderer。

- [ ] **1. 用现有 LoadSettings/openDaemonRuntime/buildDaemonRuntime/Run deps 注入失败。** 确认 logger 建立前的具体安全摘要缺失、settingsCommit.Warning 原因丢失产生 Red。
- [ ] **2. 启动 owner 在 logger 可用后使用统一 reporter。** 已提交 settingsCommit.Warning 输出 WARN 实际安全原因并继续启动。BuildRuntime 失败被安全记录后再进入现有 degraded；Cause 不进入 state LastError；无正常 logger 时使用注入的独立 stderr writer。
- [ ] **3. 明确输出模式而非检测终端猜测。** daemon 专用诊断 stderr 可记录脱敏摘要；CLI 文本/JSON 调用仍走既有安全返回渲染，非流式 JSON 保持单 envelope，已有 stream 事件输出保持原契约。不能把“额外诊断”附加到 CLI JSON stderr。启动 context 只绑定已有操作元数据，无 ID 正常。
- [ ] **4. 不安全配置解析文本不打印。** secret 注册失败/配置无法加载时只用 typed file operation、错误分类或解析失败摘要，不能依赖尚未加载的 secret 列表盲目脱敏原文。既有 FailureReporter 继续负责 logger 写入失败，禁止递归调用失效 logger。
- [ ] **5. 断言启动返回/降级/ready/close 顺序与前一致；日志未初始化、logger Close 出错、失败输出自身不可写时不 panic、不重试风暴、不改变已有退出码。** 运行 `go test ./cmd/mihari ./internal/daemon -count=1` 与相关 race 测试；记录此阶段审计到的其余断点供 Phase 4。

## Task 7：IPC 与 #216 验收

**Files:** 新建 integration/operation_diagnostics_test.go，复用 integration/runtime_test.go 的 fake/IPC 生命周期与 transport/testutil 临时 endpoint。

- [ ] **1. 将 Task 3 的 Manager、真实 control.Server/client、两个内存 JSON logger 接到临时原生 IPC。** 测试 fixture 注入 SaveSettings、reporter、ready channel；使用已有 temp socket/named pipe helper，所有 server/worker 通过 cancel + Wait 清理。无需真实 mihomo 或真实目录。
- [ ] **2. 执行 Logging PATCH `operation_id=settings-ipc`。** 客户端和 daemon 开 DEBUG：保存错误时 daemon详细记录恰好一次，client响应结果带同一 ID，error envelope仍 data_failure / persist settings，无秘密。重放同 ID 不再调用 SaveSettings、不再详细记录。新 ID 实际执行并记录。
- [ ] **3. 同 fixture 切换成功+warning 保存结果。** 响应成功、revision/内存配置更新、一次 WARN 包含安全实际原因；无 ERROR。通过真实 handler 断言没有重复顶层 JSON key，而不是只看 callback 次数。
- [ ] **4. CLI compatibility 用既有 Execute fixture 验证错误码/单 envelope，不将整个 IPC fixture引入 CLI 单元测试。** 测试日志与响应均注入并检查 secret、token、完整 URL 和配置片段不出现。
- [ ] **5. 全部 #216 验收逐项映射 Task 1–7，登记未覆盖的其他模块断点。** 没有证据的项不能标通过；不因 Phase 2 完成自动关闭 #197。

## Task 8：验证与阶段交付

- [ ] `GOTOOLCHAIN=go1.26.5 go test ./internal/diagnostics ./internal/logging ./internal/runtime ./internal/control/client ./internal/control/server ./internal/daemon ./internal/app ./internal/tui/... ./cmd/mihari ./internal/integration`。
- [ ] `GOTOOLCHAIN=go1.26.5 go test -race ./...`、`go vet ./...`、修改 Go 文件 gofmt；先目标范围后全仓，不重复无必要测试。
- [ ] 执行 Phase 1 Task 4 的六目标 CGO-free 构建命令，临时产物不入仓库；root 所有者等环境基线失败单独记录，不更改测试断言规避。
- [ ] 检查 diff：协议/CLI契约/持久化/事务顺序未改，额外文件只限共同 ctx/诊断出口机械适配。未开始 Phase 3/4 领域完善，无外部写操作/commit。
- [ ] 交付仅列真实通过的检查、失败/未验证项、#216验收证据及其他模块审计记录。此计划的编写/审核不代表上述测试已运行。
