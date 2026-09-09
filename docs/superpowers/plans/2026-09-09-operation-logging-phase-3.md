# Phase 3 主要业务模块诊断 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 按订阅、核心、配置事务和其他既有业务四个批次，保留关键失败原因并接入统一操作日志，保持所有业务结果与事务规则。

**Architecture:** 消费 Phase 2 的 diagnostics.Wrap/Reporter/执行 owner，以小范围 cause 修复配合实际日志输出测试。CLI/TUI 与控制客户端只沿已有 mutation DTO 传播 ID；领域包返回错误，runtime 实际执行边界负责最终诊断，避免重复记录。

**Tech Stack:** Go 1.26.0 / go1.26.5；现有 context、slog、diagnostics、runtime、subscription、core、supervisor、mihomo。

**Spec:** [已通过设计](../specs/2026-09-09-operation-logging-diagnostics-design.md) §4、§6–9、§10 Phase 3；前置 [Phase 1](2026-09-09-operation-logging-phase-1.md)、[Phase 2](2026-09-09-operation-logging-phase-2.md)。后台调度、Web、stream 和剩余退出边界属于 [Phase 4](2026-09-09-operation-logging-phase-4.md)。

## Global Constraints

- 复用 `internal/logging`、`slog`、现有脱敏、轮转及生命周期管理，不引入第三方日志或 tracing 依赖。
- 保持 `/v1` DTO、JSON envelope、错误码、CLI 退出码和持久化格式；不向公开 Message/Details 填入内部 cause。
- 保持 daemon 单写入者、原生 IPC 和既有日志目录/权限边界。
- 不修改 mihomo，不扩展真实订阅、真实核心、系统服务或其他 testenv 操作。
- 不在本设计中实现 #197 的 setup 提示改进。
- 每批只接入其真实入口和必要调用链，覆盖关键失败、补偿、恢复及 operation ID。保持事务语义，不把关联字段传入公开配置、凭据或持久化状态。
- 不引入 CLI 文件日志、只读请求/认证前跨 IPC 元数据、trace/header/新 DTO、日志队列或无 owner goroutine。
- 四个批次独立验证；不将日志审计变成全仓重构。必要的范围外 P0/P1 先汇报，用户未授权时不提交、推送或 PR。

## 0. 实施前核对与文件范围

基线取证：`d76e09d`；在既有 `/home/kinema/dev/mihari/.worktrees/issue-216` 独立分支实施，先确认 Phase 1/2 已完成并读取其最终接口。必须阅读根 AGENTS.md、.github/CONTRIBUTING.md、README.md、设计和本计划。

| 批次 | 允许改动的生产入口 | 对应测试/fixture |
| --- | --- | --- |
| 3A 订阅 | `runtime/subscription.go`、`subscription/{service,downloader}.go` | runtime/subscription_test.go 的 subscriptionManager；subscription service/downloader tests |
| 3B 核心 | `runtime/manager.go` Install/Restart、`core/{install,config,command}.go` 中已有错误转换、`supervisor/supervisor.go` | core/install_test.go（Prepare/Download）、runtime manager fakes、supervisor fakeStarter/fakeChild/fakeWaiter |
| 3C 配置事务 | `runtime/subscription.go` 的 prepareContent、commitRuntimeConfig、commitTrustedRuntimeConfig、markConfigDegraded；`core/trusted_runtime_unix.go` 中相应返回原因 | ValidateConfig/reload fake、trusted fixture、runtime subscription tests |
| 3D 其他 | `runtime/{sysproxy,tun,tun_trusted,geoip,panel,provider}.go`；geoip/panel/mihomo 中这几条调用链已有 cause 丢弃点 | runtime 相邻 tests；geoip/panel/mihomo 单元测试 |
| 每批控制入口 | `control/client/runtime.go`、server 对应 endpoint 文件、cli/runtime.go 及对应命令、tui 对应业务页 | 已有 DTO/命令/页面测试，只添加操作 ctx/元数据断言 |
| 本阶段配套 | 各包相邻 `*_diagnostics_test.go`；`integration/operation_diagnostics_test.go`；`docs/architecture.md` | 扩展 Phase 2 IPC fixture；更新审计登记 |

上述领域目录不构成任意文件修改授权。每批先登记具体“丢 cause/无 owner/重复记录”证据，修改仅限对应函数与直接测试，不能新增业务分支。Download 的实际实现与测试都在 core/install.go、install_test.go，不创建新的 download.go 业务文件。

### 共享测试收集器

在 `runtime/diagnostics_test.go` 复用 Phase 2 recorder；若尚未有等价 helper，添加下面这个测试私有类型，不新建跨包 testutils。其他包只有确实需要时复制这个小 fixture。

```go
type diagnosticRecord struct {
    Operation logging.OperationMetadata
    Record diagnostics.Record
}
type diagnosticRecorder struct {
    mu sync.Mutex
    records []diagnosticRecord
}
func (r *diagnosticRecorder) report(ctx context.Context, record diagnostics.Record) {
    operation, _ := logging.OperationFromContext(ctx)
    r.mu.Lock()
    defer r.mu.Unlock()
    r.records = append(r.records, diagnosticRecord{Operation: operation, Record: record})
}
func (r *diagnosticRecorder) snapshot() []diagnosticRecord {
    r.mu.Lock(); defer r.mu.Unlock()
    return append([]diagnosticRecord(nil), r.records...)
}
```

每批至少一个测试使用真实 `logging.NewDiagnosticReporter` + 内存 JSON writer，而非只断言 recorder；校验脱敏与 Phase 1 顶层 ID。logger 资源与 reporter 由现有 owner 注入，不让领域包开文件。

## Task 3A：订阅下载、刷新、切换和添加后刷新

**Consumes:** diagnostics.Wrap、runtime doOperation 的执行 ctx、Reporter、Phase 1 OperationMetadata。
**Produces:** 订阅原因可追溯；原有字段/刷新行为不变；此批已存在 mutation 的客户端与 server ID 接入。

- [ ] **1. 补充最小原因回归。** 在 subscription/downloader_test.go 添加下列测试；无需新 HTTP stub 即可证明既有转换丢失原因。

```go
func TestDownloaderDiagnostic_PreservesNetworkCause(t *testing.T) {
    injected := errors.New("transport fixture failure")
    err := toAPIError(networkFailureError{cause: injected})
    var api protocol.APIError
    if !errors.Is(err, injected) || !errors.As(err, &api) || api.Code != protocol.CodeNetworkFailure {
        t.Fatal("network error cause or public code lost")
    }
    if err.Error() != "subscription download failed" { t.Fatal("public message changed") }
}
```

- [ ] **2. Red：** `go test ./internal/subscription -run '^TestDownloaderDiagnostic_' -count=1`。
- [ ] **3. 最小修正。** toAPIError 仅在原 netFail 分支改为下面形式，不改变 `isFallbackable`、orderFor、HTTP状态判断或取消优先级：

```go
return diagnostics.Wrap(protocol.APIError{
    Code: protocol.CodeNetworkFailure, Message: "subscription download failed",
}, err)
```

service/runtime 中相同类型的转换沿用既有 API Code/Message/Details，只保留原 cause；上下文 `fmt.Errorf("静态操作: %w", err)` 不能直接作为公开消息。noteRefreshError 仍只写安全摘要，不能把内部诊断写入 catalog/LastError。

- [ ] **4. 扩展真实 mutation 失败测试并记录 Red。** 在既有 subscriptionManager fixture 通过 Options.DiagnosticReporter 注入 recorder（仅改变测试 helper 的装配，不改变生产构造语义）。补充下表断言：

| 基于既有测试/注入 | 保持业务断言 | 新诊断断言 |
| --- | --- | --- |
| TestAddSubscriptionKeepsProfileWhenFetchFails | 注册成功，profile保留、uncached、安全LastError | child刷新失败的详细日志一次，ID为 `add-fetch`；父add不能改成失败 |
| TestRefreshCannotRecreateSubscriptionDeletedDuringDownload | stale结果不能重建删除对象，revision_conflict | 不刷ERROR，ID为 refresh，不串 remove |
| downloader auto transport fallback | transport失败才fallback，HTTP状态失败不fallback | 同一次 Refresh 内重试不新建业务ID；恢复后不误记最终ERROR |
| 下载/commit失败 | catalog/cache/revision维持既有结果 | 错误链保留注入原因，runtime owner一次记录，返回安全 |
| 同一ID重放与两个ID并发 | 现有 doOperation 语义 | 无重复详细日志，不串ID |

- [ ] **5. 接入该批 DTO 的 ctx。** client AddSubscription、RefreshSubscription、UseSubscription、SetSubscriptionEnabled、UpdateSubscription、RemoveSubscription 从各自 DTO ID绑定，server 正常认证解析后绑定同值；TUI subscriptions/CLI相应命令只传 ctx 与不可变结果元数据。记录用静态 operation 名称，不输出订阅 name/URL/config/body。

AddSubscription 已在注册提交后发起独立 `operation.ID+"-fetch"` 的 Refresh；保留这个既有子 ID，不统一成父 ID，不因为自动刷新失败回滚注册。对同一个 add 缓存重放是否再触发该子操作，保留现有行为与子去重结果，不能用日志需求改变。

- [ ] **6. Green：** `go test ./internal/subscription ./internal/runtime -run 'Subscription|Refresh|Downloader|Diagnostic' -count=1`；再执行两包全测试/race及对应client/server/TUI测试。成功摘要只在实际执行owner对已接入的静态操作输出一次 INFO；cache重放最多DEBUG，避免每层重复。

## Task 3B：核心安装、重启与监督失败

**Files:** 表中的核心链路、相关 fake/tests，以及 app/runtime.go 中 supervisor reporter 的装配。
**Consumes:** Phase 2 Reporter/Wrap；**Produces:** 安装和显式重启的诊断；监督器独立运行事件保留真实原因。

- [ ] **1. 核心 AIO 提示回归先 Red。** 该测试直接复用 Phase 2 Wrap，验证既有 withAIOHint 再构造 APIError 时的 cause 断裂。

```go
func TestCoreDiagnostic_AIOHintKeepsCause(t *testing.T) {
    injected := errors.New("release transport failure")
    original := diagnostics.Wrap(protocol.APIError{
        Code: protocol.CodeNetworkFailure, Message: "download failed",
    }, injected)
    err := withAIOHint(original)
    var api protocol.APIError
    if !errors.Is(err, injected) || !errors.As(err, &api) || api.Code != protocol.CodeNetworkFailure {
        t.Fatal("AIO hint discarded original failure")
    }
    if !strings.Contains(api.Message, "install-aio-remote.sh") { t.Fatal("existing hint lost") }
}
```

- [ ] **2. 运行 `go test ./internal/core -run '^TestCoreDiagnostic_' -count=1`，再将 withAIOHint 的原 `return apiError` 改为 `return diagnostics.Wrap(apiError,err)`。** 其余已确认丢 cause 的下载、校验、replace 返回点以同样规则最小修改，不能改变可信核心判断、大小/hash限制、校验命令或CGO支持。
- [ ] **3. 安装端到端 failure matrix。** 用 runtime fakeInstaller/fakeCandidate/fakeSupervisor 覆盖 Prepare失败、Commit失败、Restart失败、维护失败；每项先新增 cause/日志断言，确认失败后最小修正。保留 revision/候选身份/cleanup次序和现有API分类；从安装器返回 error到runtime执行完成时一次详细记录。Candidate.Commit无ctx仍保持小接口，由owner关联，不能把ctx存入candidate。
- [ ] **4. supervisor 自有生命周期。** 新增可选 `DiagnosticReporter diagnostics.Reporter` 到 supervisor.Options，由 app 装配；只在 Run 的实际子进程启动失败、异常退出、健康检查导致状态变化、终止失败的责任点记录。Observe.LastError仍安全；循环普通健康成功不逐次输出。使用既有fakeStarter/fakeChild/fakeWaiter/Now逐项验证事件和既有状态/退避不变，记录必须在监督器的锁外。

显式 Restart/Install 结果由 runtime 使用请求 ID记录；监督器长期 Run、自动重启和原始核心stdout不借用最近一次请求ID。为避免两次详细诊断，监督器把同一次同步请求失败原样返回给owner，不在返回前再次记录；自主异常才由监督器记录。不得重写 Start/Wait/Terminate/Kill生命周期。

- [ ] **5. 接入 InstallCore/RestartCore 的现有 DTO、CLI及对应TUI入口。** 不改变setup展示文案；日志关联能力不等于 #197完成。
- [ ] **6. 验证：** core/supervisor/runtime/client/server目标测试→包测试→race；跨平台核心边界使用已有fake并运行六目标CGO-free构建，不实际安装/启动真实核心。

## Task 3C：配置校验、原子替换、reload 与 rollback

**Files:** runtime/subscription.go（实际配置事务所在处）、core/trusted_runtime_unix.go 必要的原因返回；相邻测试。
**Consumes:** Wrap/Reporter和既有配置事务；**Produces:** 可区分原始失败与恢复失败的单次诊断。

- [ ] **1. 在 TestReloadFailureRollsBackSubscriptionActivation 及相关成功恢复测试上追加 cause 断言。** 使用该fixture的 reloadController 已有 `reload func(context.Context) error` 注入逐次返回错误，以测试私有计数区分第一次拒绝和第二次恢复拒绝；不修改其嵌入fakeController接口。保存原有文件/catalog/generation/degraded/revision断言，先让新增 errors.Is/诊断断言失败。

```go
first := errors.New("initial reload failure")
restore := errors.New("rollback reload failure")
// Assert on the operation's returned error, before protocol serialization.
if !errors.Is(err, first) || !errors.Is(err, restore) {
    t.Fatal("initial or compensation cause missing")
}
var api protocol.APIError
if !errors.As(err, &api) || api.Details["degraded"] != true {
    t.Fatal("existing degraded classification changed")
}
```

- [ ] **2. 在 commitRuntimeConfig 保留首个 Reload error变量。** 原先 `if err := Reload(...); err == nil` 分支改为命名 firstErr，失败之后继续既有restore/reload顺序。成功恢复返回既有安全失败消息，cause为firstErr；恢复不确定返回既有degraded API，cause为 `errors.Join(firstErr,restoreErr,reloadErr)`。不能以新诊断修改ctx取消和提交行为。
- [ ] **3. 对 trusted 路径作等价 cause 保留，不合并两套事务实现。** 普通 rollback现用原ctx，trusted现用WithoutCancel，保持各自现状。restore、receipt、配置identity校验失败分别保留；API details/degraded状态继续由原有路径决定。
- [ ] **4. 处理准备/校验原因的安全边界。** ValidateConfig可能包含配置内容，内部保留原因但输出用Phase2安全摘要，不打印候选YAML或命令完整输出。markConfigDegraded/noteRefreshError的持久摘要不展开cause。恢复成功可有WARN恢复说明，但最终请求失败仍只在owner一条详细ERROR；不把已提交warning改成失败。
- [ ] **5. 验证表：**

| 故障注入 | 必須保持的结果 | 诊断 |
| --- | --- | --- |
| ValidateConfig拒绝 | 最后有效配置不变、无reload | 原因可追溯；内容不泄露 |
| candidate原子替换失败 | 保持旧文件 | data/upstream分类按既有逻辑；保留IO原因 |
| 首reload失败，restore+reload成功 | 保持既有失败响应、旧有效配置恢复 | 首原因保留；不标degraded |
| restore或第二次reload失败 | 既有degraded/stopCoreOnUnlock/revision语义 | 所有真实失败原因留在内部链，详细记录一次 |
| 取消/陈旧结果 | 现有ctx及revision/identity校验 | 不改变补偿时机或缓存规则 |

- [ ] **6. 运行 runtime/core相关目标测试、integration订阅/配置回归、race；检查写入顺序、LastError、Details与原有测试一致。** 本批不修复邻近事务设计问题。

## Task 3D：系统代理、TUN、GeoIP、面板与provider

**Files:** 表中3D入口及明确丢原因的领域转换；对应CLI/TUI/client/server现有mutation入口。
**Consumes:** Phase2 owner与前三批规则；**Produces:** 剩余主要业务批次的诊断覆盖。

- [ ] **1. 按下表一次处理一个域，先用原fixture新增明确cause/日志断言。** 有编译所需新可选reporter字段时先加空接线，再以缺少诊断的行为失败作为Red；不调整原有业务断言。

| 域/函数 | 注入与既有测试 | 最小修正与验收 |
| --- | --- | --- |
| sysproxy mutate/compensateSystemProxy | SaveSettings/backend Apply失败、外部代理冲突、late cancel、补偿失败 | Wrap原API与真实error；保留OS应用顺序、committed/degraded、force gate；冲突DEBUG、真失败ERROR |
| tun mutate/compensate/applyTun + trusted | TunDetect、Controller、SaveSettings、trusted fixture | 保持tun.enable覆盖、core信任、双路径补偿；不改变网络暴露/权限 |
| geoip UpdateGeoIP | PrepareGeoIP/Candidate.Commit、成对restore失败、候选stale | 记录实际错误与成对恢复结果；原子对/状态/清理不变 |
| panel install/update/reinstall/activate/rollback/uninstall | PanelService fake、staging/identity/commit失败；已有cleanup调用及清理结果 | 不记录Web token/下载URL；prepared接口不加ctx；锁外owner记录。PreparedMutation.Cleanup无返回值，无法经该接口观察的失败只登记限制，不新增返回error或报告接口 |
| provider UpdateRuleProvider | controller返回失败、unsupported mutation | 保持mihomo原生provider路径和统一mutation coordinator；不能恢复已移除资源图事务 |

仅在原本丢弃cause的API转换点使用 `diagnostics.Wrap(existingAPI, originalError)`；同一个已报告的执行结果必须保留外层marker原样返回，不能把所有error一律重新Wrap。真正合并新失败或改变公开分类时才创建新结果；同时失败用errors.Join后外层安全包装，公共code/message/details仍取原有分支。不能简单 `return errors.Join(api,cause)` 使Error()泄露。

增加至少一项域owner→真实control.Server回归：既有Manager详细记录一次，server收到同一marker结果不重复；另用“已报告子错误 + 新补偿失败”的新aggregate验证新故障不会被旧marker抑制。

- [ ] **2. 每个域测试至少一个真实handler输出。** 断言静态operation名称、调用方ID、错误码、无敏感内容；两次同ID重放不重复详细记录。成功仅有必要摘要，不输出配置对象、节点列表或OS环境。
- [ ] **3. 接入现有DTO边界。** 只对该域已携带operation_id的请求绑定ctx；没有ID的只读接口不新增字段/header。客户端保留原传输分类和ResponseBody清理；TUI异步结果带值元数据，不改变现有交互/状态机。
- [ ] **4. 每域独立Green→包测试→相关IPC集成/race，全部通过后再处理下一个域。** 日志自身IO出错的剩余故障矩阵不在此扩展，归Phase4。

## Task 3E：业务批次验收与审计登记

- [ ] 在 `docs/architecture.md` 的诊断说明下登记已覆盖入口：操作ID来源、cause产生点、最终owner、日志级别、状态不变量、测试名称。未覆盖项明确列到Phase4，不写“全仓所有错误已覆盖”。
- [ ] 运行相关包和 `internal/integration` 后，执行 Go1.26.5 `go test ./...`、`go test -race ./...`、`go vet ./...`、修改文件gofmt和Phase1 Task4六目标CGO-free构建。基线失败、环境不能运行项单列，不能修改权限或排除失败测试来通过。
- [ ] diff检查只有本批确认的原因保留/记录/ctx与测试；原协议、错误码/退出码、ID幂等语义、持久化与事务行为未改。原始mihomo日志没有伪造请求关联。没有执行真实服务或公网测试。
- [ ] 交付各批次真实结果；仅在用户授权时commit/push/PR，不自动关闭与日志展示有关的 #197。计划审核不等于上述实现/测试已完成。
