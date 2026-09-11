# 操作日志上下文与分阶段诊断完善设计

- 日期：2026-09-09
- 状态：设计及 Phase 1–4 实施计划已审核通过，用户已授权分阶段开发、提交与 PR；实现进度以各阶段验收为准
- 基线：`origin/dev`，`d76e09d`（包含 PR #215）
- 工作分支：`codex/issue-216-error-diagnostics`
- 关联：[Issue #216](https://github.com/mihari-proxy/mihari/issues/216)、[Issue #197](https://github.com/mihari-proxy/mihari/issues/197)

## 1. 目标与交付边界

先为现有 logger 增加操作上下文，再按模块完善错误传播、诊断记录和失败测试。同一次操作中，Mihari CLI/TUI 的本地处理、控制客户端的请求/响应和 daemon 执行记录可以通过同一个 `operation_id` 关联。

目标是让需要诊断的失败有明确处理责任和可追查记录，不要求每个非 nil error 都生成 ERROR 日志，也不承诺穷尽所有可能故障。

本设计定义基础能力和后续实施阶段，各阶段按范围独立计划和验收；实施顺序遵循用户授权，可以在统一授权下连续推进。当前 #216 是基础能力之后的第一条诊断闭环，不能用日志基础设施交付代替其验收。

约束：

- 复用 `internal/logging`、`slog`、现有脱敏、轮转及生命周期管理，不引入第三方日志或 tracing 依赖。
- 保持 `/v1` DTO、JSON envelope、错误码、CLI 退出码和持久化格式；不向公开 Message/Details 填入内部 cause。
- 保持 daemon 单写入者、原生 IPC 和既有日志目录/权限边界。
- 不修改 mihomo，不扩展真实订阅、真实核心、系统服务或其他 testenv 操作。
- 不在本设计中实现 #197 的 setup 提示改进。

## 2. 当前实现与缺口

### 2.1 已有能力

- `internal/logging/handler.go`：slog JSON handler，统一组件字段，对消息及结构化属性脱敏，支持 `With` 和 `WithGroup`。
- `internal/logging/redactor.go`：已知凭据和 URL 精确替换、通用敏感文本规则，并发安全规则快照。
- daemon、TUI、mihomo 输出已有固定日志文件、轮转与导出能力。
- `internal/control/protocol` 多种 mutation DTO 已包含 `operation_id`；CLI/TUI 已在业务操作入口生成 ID。
- `internal/runtime/manager.go` 的 `Operation.ID` 与 `doOperation` 用于进程内操作去重。该缓存不是永久的 exactly-once 保证。
- `internal/app/local_error_unix.go` 已示范安全 `Error()` 与内部 cause 并存的错误封装。

### 2.2 本设计要解决的缺口

- handler 尚未从 `context.Context` 提取日志关联字段；operation ID 没有贯穿统一日志路径。
- settings 保存失败被替换为固定 `APIError`，内部原因丢失。
- settings 的实际 directory-sync warning 被固定字符串替换；启动处也有同样处理。
- 控制请求没有统一失败诊断收口，非 APIError 映射为安全响应前缺少内部记录。
- `reportBackground` 统一忽略 canceled/deadline，无法区分正常取消与上游超时。
- 控制客户端有 redactor，但没有完整的请求日志接入；普通 CLI 没有独立文件日志生命周期。

根 AGENTS.md 引用的 `2026-08-03-mihari-architecture-design.md` 不在此基线。架构以根 AGENTS.md、[现行架构](../../architecture.md)、[Unix 布局](../../unix-layout.md) 和 [文件日志设计](2026-09-02-file-logging-export-design.md) 交叉核对。

## 3. 方案选择

| 方案 | 优点 | 代价 | 决定 |
| --- | --- | --- | --- |
| 每层手工传递 `logger.With(...)` | 机制简单，字段直观 | 需要额外传 logger，遗漏容易导致链路断开 | 组件固定字段可用，不作为操作传播主路径 |
| ctx 携带明确字段，handler 统一提取 | 复用现有 ctx 参数，适合并发和逐模块接入 | 必须使用 context 日志方法，并处理 handler 派生行为 | 采用 |
| 完整 tracing 框架、全仓同时接入 | 支持复杂 span 和跨服务追踪 | 新依赖及接入面过大，当前需求不足以支撑 | 本次不采用 |

日志上下文和内部错误封装分别承担关联与原因保留。不能依赖 logger 自动发现返回值 error，也不能通过给日志加 ID 恢复上游已丢失的 cause。

## 4. operation_id 的语义与传播

### 4.1 ID 表示一次逻辑操作

日志字段统一命名为 `operation_id`，与既有协议一致；讨论中的 op ID 指同一个概念。

- mutation 发起者生成 ID，写入 DTO，同时绑定本地 ctx。
- 相同逻辑请求的传输重试沿用 ID；内部重试、校验和补偿沿用该 ID，通过事件名称区分阶段。
- 用户修改参数重新提交或发起新的 mutation，生成新 ID，不能把同一个 ID 用于不同 payload。
- 一次 UI 动作可能发起多个独立 mutation。每个 mutation 保持自己的幂等 ID；本次不把它们强行合成一个 ID。未来如需要用户交互级关联，再设计独立的父关联字段。
- 后台任务每次逻辑执行在其入口生成 ID，执行内重试沿用；下一次调度生成新 ID。随机源必须可注入，生成失败不使用常量占位 ID。
- daemon 启停、日志轮转和 mihomo 原始输出等无业务操作的记录允许无 ID。

`instance_id`、`request_id`、`trace_id`、span 树不在第一阶段引入。operation ID 不能用作认证凭据，也不能替代进程/组件身份。

### 4.2 一条 mutation 链路

1. CLI/TUI 创建 ID，将其放入请求 DTO 和本地操作 ctx。
2. 控制客户端使用该 ctx 记录请求开始、传输失败或响应结果，并传播取消/超时。
3. daemon 认证、解析并校验请求后，从 DTO 提取 ID，绑定新的请求 ctx，再调用 runtime。
4. runtime、领域方法、mihomo 适配器继续传递 ctx；记录日志时使用 `InfoContext`、`WarnContext`、`ErrorContext` 或 `LogAttrs`。
5. 原 ctx 保留在客户端等待响应的执行路径，响应即使不回显 ID 也能关联。
6. TUI 异步结果消息显式携带 ID 等不可变诊断元数据，避免通过“当前操作”全局变量推断。页面更新按收到的操作结果关联，不保存整个 request ctx 到错误或模型状态。

同一进程同时处理不同操作，分别持有各自 ctx。创建 goroutine 时显式传递 ctx；不得假设自动继承，不使用 goroutine-local 存储，不修改全局 logger 的当前 ID。

协议 DTO 的 ID 是跨 IPC 的来源；ctx 不会自动跨进程。带 DTO 的调用以 DTO ID 为准，日志上下文不得修改 DTO 以匹配已有 ctx。

### 4.3 没有有效 ID 的路径

- 连接失败：客户端已有 ID，daemon 没有记录是正常结果。
- 请求在认证或解析阶段被拒绝：daemon 不为诊断额外解析原始 body，不承诺能关联客户端 ID。
- 现有只读请求没有 operation ID 字段：可做本地关联，但本阶段不承诺跨 IPC 关联。
- 扩展只读请求/认证前关联需要独立评审传输元数据方案；不在实现时隐式新增 header 或修改 DTO。
- 旧客户端或无 ID 本地调用继续正常工作，缺少日志 ID 不改变业务结果。

## 5. 日志上下文 API 与 handler 行为

在 `internal/logging` 定义小型、类型化操作元数据和绑定/读取辅助函数，名称建议为 `WithOperation` 与 `OperationFromContext`。使用私有 context key，不接受任意 `map[string]any`，不遍历输出整个 context。

第一阶段支持：

| 字段 | 来源与意义 |
| --- | --- |
| `operation_id` | 既有 mutation ID 或本地任务入口 ID |
| `operation` | 代码定义的操作名称，如 `settings.update`、`subscription.refresh` |

组件仍由现有 logger/组件属性区分。事件使用静态消息或静态事件名称；错误码、耗时、重试序号等由记录点按需添加，不成为必须配置的上下文集合。

handler 行为：

- 只在实际记录时读取操作上下文，`Enabled` 保持既有级别过滤。
- 操作字段始终位于 JSON 顶层，即使 logger 派生过 `WithGroup`。
- 有 ctx 操作元数据时，以其值为准；同名调用属性、预绑定属性不能产生重复 JSON key 或覆盖它。
- 无操作上下文时保留既有 `With` 行为；不得产生空 ID 或在 handler 中随机生成 ID。
- 派生 handler 保留关联提取能力，不缓存某个请求的 ctx；不得修改调用者 record 或共享属性切片。
- 提取的元数据必须在进入输出前经过现有 redactor，不能在脱敏完成后追加未净化字段。
- 使用 `Info()` 等无 ctx 调用不会自动取得链路 ID；模块接入时需要审计调用点。

日志元数据来自不可信客户端。ID 在日志侧仅接受 1–128 字节的 ASCII 字母、数字、`-`、`_`、`.`、`:`；非法或超长值省略，不能截断后伪装为完整关联 ID。不改变现有协议接受范围、运行时去重 key 或 API 结果。允许的 ID 仍经过脱敏；被脱敏的 ID 可能无法精确关联，以不泄露信息优先。操作名称由代码常量提供，不从 URL、对象名称或请求正文生成。

## 6. 内部错误与公开错误

Phase 2 引入明确职责的内部诊断错误封装，承载安全 API 错误、操作上下文和原始 cause。它不放入公开协议 DTO，也不保存 ctx、logger、请求对象或凭据。

- `Error()` 默认返回安全提示，避免现有调用点意外打印 cause。
- `errors.As` 仍能优先找到外层预期的 `protocol.APIError`；`errors.Is/As` 可以继续追溯内部 cause。
- 诊断格式化是显式行为，不依赖普通 `Error()` 自动展开；支持既有 `Unwrap() error` 和 `Unwrap() []error` 形态，避免重复展开，并限制深度与输出长度。
- HTTP/CLI 渲染仅使用安全 API 信息；内部诊断字符串不能进入 Message、Details、状态 LastError 或事件 payload。
- 不自动把所有已存在封装改成新类型；先用于 settings 与本次收口必需的边界。

错误封装的具体命名和最小方法集合在 Phase 2 计划中确定。必须是领域明确的内部包或已有合适包中的类型，不能放入无边界的通用 utils。

## 7. 记录责任、去重与级别

### 7.1 谁记录

| 边界 | 责任 |
| --- | --- |
| 文件、网络、领域底层 | 返回错误并保留原因；不为同一次失败逐层打 ERROR |
| mutation 实际执行完成处 | 记录该执行的最终故障，包含操作信息及诊断原因 |
| 控制 API | 对未被执行边界记录的意外失败兜底，然后返回安全响应 |
| 后台任务 owner | 记录终止失败、耗尽重试等最终结果，管理 goroutine 生命周期 |
| 客户端 | 记录本地传输失败；收到 daemon 错误时记录响应结果，不复制 daemon 内部原因 |
| CLI/TUI | 处理本地任务失败和用户反馈，不把每次展示失败提示重复记成 ERROR |

请求访问记录和故障诊断记录是不同事件：允许同一个 ID 有多条阶段日志，但同一次内部故障的详细诊断只由负责边界记录一次。

mutation 完成记录发生在慢操作结束、业务锁释放后。通过内部错误/执行结果中的报告状态让控制边界识别已报告故障，不使用全局 ID 集合，不修改公开 DTO。状态属于一次执行结果，必须并发安全；不在 error 中保存可变的全局“当前请求”。

`doOperation` 命中现存缓存或等待同一执行结果时，不重复记录详细故障；可按 DEBUG 记录重放。缓存淘汰后再次实际执行可以有新的结果日志，不宣称跨缓存生命周期去重。

warning 可能在持有 mutation 锁时产生，应随内部结果收集，在负责边界锁外输出。不能为日志增加无 owner 的 goroutine，也不能为所有业务返回值引入无必要的大范围接口重构。

### 7.2 级别策略

- ERROR：未恢复的实际操作失败、补偿失败、后台任务异常退出。
- WARN：已提交后的 durability warning、重试后恢复但值得关注的降级。
- INFO：有意义的操作完成或生命周期变化；不对高频轮询逐次输出。
- DEBUG：请求阶段、重试、缓存重放、预期冲突等诊断细节。
- 正常取消和一般输入校验不默认产生 ERROR。根据当前请求/任务上下文判断取消来源，不能仅看到 `DeadlineExceeded` 就忽略真正的上游超时。

settings replace 成功仍代表提交成功；目录 sync warning 必须保留实际原因并按 WARN 输出，不回滚已提交结果，不将客户端响应改为失败。

## 8. 输出位置、启动与 logger 自身失败

daemon 与 TUI 继续使用各自既有日志资源。控制客户端接收 owner 注入的可选 logger/报告接口，不自行创建目录或打开文件；没有接入诊断输出时不影响请求功能。

普通 CLI 当前没有独立文件 logger。第一阶段不创建 `mihari-cli.log`，不借用 TUI 专用文件，不扩大 Unix 用户日志写入例外。CLI 可携带操作 ctx 并复用现有安全错误渲染；以后要求持久化 CLI 全链路日志时，单独设计文件位置、权限、生命周期和导出，并取得所需批准。不能声称 Phase 1 后 CLI 每个阶段都已有持久化记录。

启动失败发生在 logger 可用前时，由启动 owner 使用注入的安全 stderr 出口兜底。尽早注册已知凭据；无法安全提取的配置/解析错误采用类型化安全摘要，不直接输出可能含配置原文的 cause。

CLI `--json` 继续输出既有单一 envelope，不能追加诊断 JSONL 或文本破坏解析。启动兜底必须遵守调用模式的输出所有权：专用 daemon 诊断 stderr 可以记录，CLI 模式走既有安全渲染。为保证机器输出兼容，缺少独立诊断出口的早期失败允许只输出安全摘要。

logger 自己的写入/关闭失败由已有资源 owner 处理，通过独立安全出口和受控健康状态报告，禁止回到同一个失败 logger 递归记录。Phase 2 审计已有行为，Phase 4 完善剩余故障注入；不以新建无限队列或每次失败启动 goroutine 兜底。

## 9. mihomo 的关联边界

Mihari 的 mihomo 适配器可以用同一操作 ctx 记录请求开始、结果、耗时和脱敏错误。

mihomo 是独立进程；当前采集 stdout/stderr 不包含 Mihari 的操作上下文。原始输出只保留可靠来源字段及时间，不把“当前正在操作”的 ID 附到同时发生的所有核心日志上。未来可以独立补充核心实例或 PID，但不宣称原始日志能按 operation ID 精确关联。

为单个操作启动的短命校验进程，只在生命周期由该操作独占且输出归属可证明时才能携带其 ID；这不推广到共享运行中的核心。

## 10. 分阶段实施与验收

### Phase 1：日志操作上下文

范围：`internal/logging` 的类型化元数据、ctx API、handler 提取、脱敏和派生行为。不批量修改业务模块、不新增协议字段。

先写失败测试，验证：

- ctx 元数据出现在日志中，普通无 ctx 日志行为兼容。
- 两个并发操作不会串 ID；子 ctx 保留父元数据，绑定新操作不修改原 ctx。
- `With`、`WithGroup`、嵌套属性和保留键冲突不产生重复顶层字段。
- ID 长度/字符限制仅影响日志，不改变请求、去重或业务结果；敏感元数据经过脱敏。
- `Enabled`、取消 ctx、共享 record/属性不会导致错误丢弃或数据竞争。

交付是可用日志能力，不是全链路已覆盖。

### Phase 2：#216 首条完整诊断链路

范围：内部错误封装、settings 保存/同步 warning、请求失败收口、启动兜底；选取 settings mutation 的 TUI/控制客户端/daemon 路径接入 operation ID。CLI 保持上述输出边界。

验收：

- 注入保存错误，先证明 cause 丢失，再验证 `errors.Is/As` 保留原始原因及既有 API 分类。
- 经临时本地 IPC 触发保存失败，在客户端与 daemon 观察到相同 ID，daemon 详细诊断记录一次。
- 重复请求、并发等待和缓存重放不重复诊断；缓存未命中实际执行的结果如实记录。
- 已提交 warning 保持成功/revision/内存发布语义，实际原因以 WARN 记录。
- 非 APIError 意外失败在公开安全响应前被记录。
- 取消、超时、预期冲突和参数错误符合级别策略。
- 日志、响应、状态和 stderr 不泄露测试凭据、controller secret、token、完整 URL 或配置原文；CLI JSON/退出码保持兼容。
- 日志初始化前失败、降级启动、日志资源关闭遵守输出与生命周期边界。

同时审计其余断点并登记，不顺手修复全仓。#216 完成判断以其所有验收项为准；发现无法在约定范围满足的项需明确报告，不能直接关闭 Issue。

### Phase 3：主要业务模块，独立批次

建议次序：订阅下载/刷新/切换 → 核心安装/监督 → 配置应用/重载/回滚 → 其余系统代理、TUN、GeoIP、面板业务。

每批只接入其真实入口和必要调用链，覆盖关键失败、补偿、恢复及 operation ID。保持事务语义，不把关联字段传入公开配置、凭据或持久化状态。

### Phase 4：其余运行边界与整体审计

覆盖后台调度、Web gateway、CLI/TUI 本地任务、stream、关闭流程和 logger 自身故障。Web 写操作遵循现有 mutation coordinator，不能绕过安全边界；需要新增跨 IPC 元数据或 CLI 持久日志的部分先独立设计。

每条审计记录包括入口、失败原因来源、处理策略、日志 owner、级别、关联方式、脱敏方式、回归测试和剩余限制。不以新增日志行数作为完成指标。

## 11. 验证与交付规则

- 各阶段遵循 Red–Green–Refactor，先运行最小回归，再扩大到相关包/本地 IPC 集成测试。
- 并发 ctx、handler 派生、操作去重执行 race；按风险执行全仓测试、vet、格式化和六目标 CGO-free 编译。
- 测试使用临时目录、可注入随机源/fake、临时 socket/named pipe，不依赖真实服务或真实用户目录。
- JSON 比较需验证字段集合和重复 key，不能仅依赖会覆盖重复 key 的 map 解码。
- 文档变更只检查内容、链接和 diff；不为纯文档重复运行 Go 测试。
- 仅在用户授权时提交；PR 指向 dev，合并需要用户确认，不修改 CHANGELOG。

已有环境观察：Go 1.26.5 下的 runtime、control/server、config、cli 基线测试曾通过；首次误用环境默认 Go 1.27.1 的全仓运行失败，包含目录所有者/权限检查问题。此记录不代替实现后的新验证，也不代表全仓基线干净。

## 12. 设计审阅重点

1. 接受先做上下文基础、再用 #216 验证首条链路，后续模块独立推进。
2. operation ID 沿一次逻辑 mutation 传播，保留既有幂等语义；多 mutation 用户流程不强行共用 ID。
3. 现有 mutation 可跨 CLI/TUI 和 daemon 关联；只读请求、认证前失败和 CLI 持久日志的完整覆盖留待后续独立设计。
4. 接受内部 cause 与公开提示分离、详细故障单点记录、WARN 与 ERROR 区分。

用户审阅本设计后，再使用 writing-plans 为获准阶段生成实现计划。
