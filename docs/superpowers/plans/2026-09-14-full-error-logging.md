# 完整错误日志实施计划

日期：2026-09-14

状态：方案与任务拆分完成；worktree 已有部分未提交实现与测试，尚未完成全范围实施及验收。

设计依据：[完整错误日志与独立用户提示设计](../specs/2026-09-14-full-error-logging-design.md)。

## 1. 工作位置与交付边界

| 项目 | 值 |
| --- | --- |
| Worktree | `.worktrees/full-error-logging` |
| 当前分支 | `feat/full-error-logging` |
| 创建时的 dev 基线 | `e2865d04f64c39591757f5b6f973ffcd4c6baf59`；当前 `origin/dev` 已前进，实施验收前同步 |
| 后续 PR 目标 | `dev` |
| 完整交付 | 日志升级实现与文档、本地验证、rebase dev、PR、CI 与 bot review 全绿 |
| 当前实施状态 | 已有部分生产与测试改动，尚未完成模块审计与全范围验证；尚未 commit、push、创建 PR |
| 本次汇报范围 | 按最新指示交付设计方案、模块 scope 和实施计划；已有实现保留，完成设计不等于实现验收通过 |

所有任务从本 worktree 执行。主工作目录保留用户原有 `.gitignore` 修改，不借用其他任务 worktree，不在 `main`/`dev` 修改代码。用户后续已明确授权实施、本地验证后 rebase dev、commit/push/PR，以及根据 CI 和 bot review 修复至全绿。GitHub Actions 轮询间隔为 5 分钟；尚未授权合并。

Q1–Q6 的决定已经收口：全模块链路审计；仅使用已有 logger；预期错误也记录；日志和导出不脱敏；三类逻辑内容限额为 256 KiB；普通错误不额外捕获堆栈。后续不重复询问这些决定。

## 2. 阶段依赖与交付顺序

| 阶段 | 任务 | 前置 | 阶段完成标准 |
| --- | --- | --- | --- |
| A 模块清单 | T1 | 设计 | 每个范围模块有入口、owner、出口及明确审计状态 |
| B 诊断基础 | T2、T3 | T1 | 原文/级别/长度/分片测试通过，公开输出仍独立 |
| C 错误链路 | T4、T5、T6 | T2、T3 | 各业务链与客户端、生命周期缺口得到回归验证 |
| D 导出交付 | T7、T8 | T3，且 C 提供可验证记录 | 快照与导出原文一致，页面红色说明可见 |
| E 验收 | T9 | T1–T8 | 全范围登记收口、文档同步、风险相关检查完成 |

按阶段独立审查，部署交付必须包含配套导出提示与文档，不能发布只有原文日志、仍声称已脱敏的中间状态。保持单一任务范围，允许阶段内的小步 TDD；不在本计划中要求并行 agent 或规定固定提交数量。

## 3. T1：建立模块审计登记

**产物**：[模块审计登记](2026-09-14-full-error-logging-audit.md) 已建立。搜索计数和初步定位仅用于确定范围，不能作为完整审计结果；逐项状态与证据仍需收口。

每个实际入口一行，至少包含：模块/入口、cause 源、转换点、记录 owner、logger 来源、公开输出、问题、拟修改文件、最小回归、最终结果。

状态统一使用“待审计”“确认缺口”“无需修改（附证据）”“无文件出口（设计接受）”“已修复并验证”。禁止仅以“已有 diagnostics_test.go”判定包已覆盖。

以下是基线中核查到的起始文件，不是只允许修改这些文件；所有子包和调用链都按设计 scope 审计：

| 审计组 | 起始代码/测试 | 首要检查 |
| --- | --- | --- |
| 通用错误 | `internal/diagnostics/error.go`、`record.go`、`http.go` | 内部 cause、取消/预期级别、HTTP 提前脱敏 |
| 日志 | `internal/logging/diagnostics.go`、`handler.go`、`capture.go`、`config.go` | 原文丢失、长度、属性、日志资源失败 |
| 文件/配置 | `internal/config`、`internal/state`、`internal/preferences`、`internal/onboarding` | IO/解析/同步转换以及公开状态 |
| 订阅 | `internal/subscription/downloader.go`、`scheduler.go`；`internal/runtime/subscription_test.go` | 请求、缓存、刷新、调度和回滚原因 |
| Panel | `internal/panel/release/github.go`、`internal/panel/diagnostics_test.go` | 请求/读取/解析 cause 丢失及清理 |
| Core/GeoIP | `internal/core/install_diagnostics_test.go`、`internal/geoip/diagnostics_test.go` | 安装、发布与补偿的实际失败路径 |
| Mihomo/Web | `internal/mihomo/http_diagnostics_test.go`、`internal/web/http_body.go` | 状态/正文、握手、流、代理 owner |
| Supervisor | `internal/supervisor/diagnostics_test.go` | 自主监督、健康检查、重启与显式调用责任 |
| 系统操作 | `internal/sysproxy`、`internal/tundetect`、`internal/service`、`internal/update`、`internal/elevate` | 预期拒绝、权限、进程退出和补偿 |
| 平台适配 | `internal/platform`；`internal/control/credential`、`transport` | 原生系统原因和接口中间转换 |
| 编排 | `internal/app/scheduler_diagnostics_test.go`、`internal/runtime/manager.go`、`internal/daemon` | 尝试、最终失败、缓存重放和日志出口 |
| 控制边界 | `internal/control/server/diagnostics.go`、`internal/control/client/diagnostics_test.go` | 认证/解析/只读失败兜底和原始 cause |
| 普通 CLI | `internal/cli/root.go`、`service.go`、`local_tasks_diagnostics_test.go` | nil reporter、参数解析、分类前记录、JSON |
| TUI/装配 | `internal/tui/run.go`、`internal/tui/ui/logging.go`、`cmd/mihari/main.go` | 异步任务身份、已有日志生命周期 |
| 导出 | `internal/logging/machine_snapshot.go`、`assemble.go`、`internal/control/client/logging_snapshot.go` | 多阶段脱敏、字节摘要/统计、旧对端 |
| UI | `internal/tui/ui/exportlogs.go`、`strings.go`、`exportlogs_review_test.go` | 导出前和成功态说明、共享页面 |
| 契约与安全回归 | `internal/control/protocol/logging_snapshot_test.go`、`internal/integration`、`internal/securitytest` | 文件/IPC 权限不变，公开错误不接收内部诊断 |

审计显式搜索：替换为新 `APIError`、`errors.New`/`fmt.Errorf` 丢失 cause、忽略 Close/Cleanup 返回值、静默取消/冲突、脱敏后再封装、未注入 reporter、直接 renderer 输出、recover 后固定摘要。逐项判断业务语义，不机械地在每个 `if err != nil` 添加日志。

`void` cleanup 接口列为可见性检查点：若实现已获得真实 error 却丢弃，优先在既有 owner 内保留/报告；确需最小内部返回值调整才能暴露已证明的失败时允许在本范围内修复。不能为“可能存在失败”重构全部生命周期接口。

## 4. T2：通用诊断和记录级别

**主要文件**：`internal/diagnostics/{error,record}.go`、`internal/logging/{diagnostics,handler,operation_attrs}.go` 及邻近测试。

先建立失败回归，再实施：

1. 使用带文件路径、URL、凭据、多行配置片段的 `errors.New`，断言日志保留全部原文。
2. 单层、多层包装和合并错误：原始文本、操作上下文、各分支原因存在；`errors.Is/As` 与公开 `APIError` 保持原语义。
3. Path/Link/Syscall/URL/Net/JSON/YAML/Exec 等实际错误类型不再被固定摘要替代；保留可获得的字段、行列号、退出码和 stderr。
4. `WithAttrs`、`WithGroup`、消息、error 属性和 operation metadata 经文件 logger 后不替换秘密值；元数据格式/重复键校验仍保留。
5. 预期错误和主动取消返回 INFO；真实 cause 优先于外层“预期”分类，不能把真实磁盘/网络错误降为 INFO；显式日志级别继续过滤。
6. typed nil、共享 DAG、循环、深度/节点边界有可识别结果；nil/disabled logger 不遍历 error。不要无条件调用递归 wrapper 的 `Error()`，绕过图预算；正常原文保留与异常 formatter 边界分别验证。

保留 `diagnostics.Wrap` 的公开提示职责。拆分“文件诊断原文”和“终端/公开输出处理”依赖，禁止一键删除共享 Redactor 后让 FailureReporter、TUI 安全文本或普通响应失去原有职责。

**最小验证**：`go test ./internal/diagnostics ./internal/logging`。本阶段不要通过只把旧安全测试删除来获得绿色结果；文件日志断言改为原文，公开输出断言保留。

## 5. T3：256 KiB 与物理记录分片

**主要文件**：`internal/logging/{diagnostics,capture,handler,config,rotator,record_mutex}.go`，必要时在 `internal/logging` 新增专用记录编码文件；`internal/diagnostics/http.go`。

逻辑正文统一限额，先完整限额判断，再执行合法 UTF-8 分片；任何阶段不能继续偷偷保留原先 4/16/64 KiB 限额。错误链附加内容也受总诊断预算约束。

物理记录继续适配 `internal/control/protocol/logging_snapshot.go` 已有 1 MiB payload / 2 MiB frame 约束，不修改协议常量、schema 或公开错误 DTO。实现设计中的按需 64 KiB 正文分片，编码后再次检查。新日志字段只描述逻辑记录身份和片段，不重新定义业务 operation ID。

必须先失败的回归：

- `256 KiB - 1`、恰好 `256 KiB`、超过上限，含中文及跨字节边界；截断标记准确。
- 最坏 JSON 转义使原文膨胀，原文重组完整，每条物理 JSONL 和 base64 frame 都在协议限额内。
- 无需分片的小记录仍只输出一条；需要分片时 timestamp/级别/owner/operation 一致。
- 并发、两个 TUI 进程写共享文件时，片段不会被误归到其他逻辑记录；跨片段不假装磁盘原子提交。
- `record_id` 来源失败或 writer 中途失败：使用既有独立故障出口、业务不失败；已写片段可识别为不完整。
- 核心 stdout/stderr 的分块、半行、Flush/Close、重启和超长无换行输入，保留终止与内存边界。
- 新片段跨轮转、快照采集边界或保留文件淘汰时，根据 index/count 能识别缺片；不能通过放大锁或无限缓存保证完整。

**最小验证**：`go test ./internal/logging ./internal/diagnostics ./internal/control/protocol`。跨进程 writer/轮转测试复用现有 subprocess fixture。

## 6. T4：底层业务与 HTTP cause 修复

**范围**：`config/state/subscription/panel/core/geoip/mihomo/web/platform/sysproxy/tundetect/service/update/elevate/onboarding/preferences` 及控制 credential/transport。以 T1 登记为准，逐链处理。

先修复已有明确证据的 `internal/panel/release/github.go`：注入请求创建、transport、读取、JSON 解码错误，证明 `errors.Is/As` 可追溯原始 cause，日志原文存在，而公开分类/Message/Details 保持兼容。非 2xx 响应保留状态和有界诊断正文，不转储成功响应。

HTTP 统一检查 `internal/diagnostics/http.go`、`internal/mihomo`、`internal/web/http_body.go` 及其他下载适配器：

- 删除日志内容的提前 secret 替换；失败响应最多取得 256 KiB 正文并准确标记超限。
- HTTP URL/错误正文用于内部诊断，既有 REST/WebSocket/网关用户响应单独处理。
- 读取、正文关闭、传输失败不能互相覆盖；保持 context、超时、请求重试策略和资源关闭。
- 核对握手返回值的真实正文长度；依赖已经截断的场景明确标记。不得未经证据扩大重试/改库/新增依赖来追求原文。

其余业务路径沿原 owner 返回完整 cause，按 T1 的缺口添加最小故障注入。路径/凭据允许出现在文件日志，不表示测试可以读取真实用户文件；全部使用 fake、临时目录和合成凭据。

**验证**：每个修复先执行一个目标测试，再执行目标包及调用它的 runtime/集成回归。例如 `go test ./internal/panel/... ./internal/mihomo ./internal/web`；不能一次大改后仅运行全仓测试代替 Red 阶段。

## 7. T5：编排、重试、回滚与后台 owner

**主要范围**：`internal/app`、`runtime`、`daemon`、`supervisor`，以及各业务包自身的 scheduler/worker。

复用 `runtime.doOperation`、warning 收集和 `diagnostics.MarkReported`；对控制兜底、后台 owner 和显式调用责任做统一回归。预期错误级别调整需要沿 owner 实际使用路径验证，不能只更新 `FailureLevel` 单测。

故障场景：

- 第一次/第二次失败、第三次恢复：前两次原因可见，恢复结果可关联；耗尽时最后一次由最终 owner 写 ERROR，不再重复 WARN。
- 主错误加回滚/清理错误：所有真实 cause 可见，不能把新失败覆盖成只剩 `rollback failed`。
- mutation 重放和并发等待者：实际执行只报告一次；另一次真实执行允许新记录。
- settings 已提交后 sync warning：WARN，仍返回成功并保留内存/revision。
- 自主监督和后台任务：沿真实生命周期使用 operation metadata，不错误继承其他请求 ID。
- 正常取消可见但不伪装成未恢复业务 ERROR；伴随实际 IO 失败的取消不能吞掉 IO 原因。

报告在业务锁外，采集事件不新增高频业务错误采样/静默丢弃，不改变既有后台任务调度；logger 自身 failure reporter 仍保留原有资源保护与限速。

**最小验证**：`go test ./internal/runtime ./internal/app ./internal/daemon ./internal/supervisor`，然后 `go test ./internal/integration`。重点复用 runtime 的各类 `*_diagnostics_test.go`。

## 8. T6：CLI/TUI/控制边界和生命周期

**主要文件/范围**：`internal/cli/{root,service,runtime}.go`、`internal/tui/run.go`、`internal/tui/ui/logging.go`、session/pages、`internal/control/{client,server}`、`cmd/mihari/{main,unix_layout,windows_layout}.go`。

对照设计中的出口规则实施，不新增普通 CLI logger：

- 相同 CLI 任务分别注入 reporter 和 nil reporter；有注入时保留原始诊断，没有时无文件操作，两种模式输出/退出码一致。
- CLI 根级参数/解析错误和控制 API 认证/预解析拒绝：有现成 reporter 的 owner 报告；不为日志重新读取请求 body，不创建虚假的请求身份。
- 客户端记录自身传输/解码失败；已解析 daemon 错误不复制 daemon cause 再记一次失败。
- TUI 各 worker 在其 owner 生命周期内记录错误；迟到结果不借用当前 UI 任务的 metadata；取消/join/close 顺序保持。
- TUI cleanup 后安装/self-update，以及 logger 建立前的路径，登记为设计接受的无文件出口，不补建日志文件或使用已关闭 logger。
- 已有 recover 路径在可用文件 reporter 内记录 panic 值及可获得堆栈，保持原有返回/退出行为；普通错误不采集新堆栈。

**最小验证**：`go test ./internal/cli ./internal/tui/... ./internal/control/... ./cmd/mihari`。保留 `local_tasks_diagnostics_test.go`、`stream_diagnostics_test.go`、shutdown 和原生 IPC 回归。

## 9. T7：原文快照与导出

**主要文件**：`internal/logging/{machine_snapshot,assemble,export_json,export}.go`、`internal/control/client/logging_snapshot.go`、`internal/control/server/logging_snapshot.go`、`cmd/mihari/main.go` 的导出装配。

去除 daemon snapshot、本地源、客户端 `secondRedact`、最终 exporter 的内容替换。继续验证 source identity、时间窗口、快照 frame、摘要/字节数、schema、来源白名单、权限和完整终止；去除脱敏不等于把快照改成任意文件读取。

回归要求：

- 同一条含 secret/URL/path/config 的新日志，本地导出和 Unix machine snapshot→client→Assemble 导出后内容一致。
- 未实施替换的新路径 `redacted=false/0`；新 client 接收旧 daemon 的 true/计数时按现有统计语义如实保留。
- 历史脱敏记录正常读取，不“恢复”已丢失原文；旧 reader/peer 的行为明确登记。
- 256 KiB 与最坏转义的分片记录可完整穿过全部 reader；frame/record 现有边界仍拒绝恶意超长输入。
- 片段可跨轮转，但导出不擅自扩大用户选定时间窗口或读取已经淘汰的文件；缺片通过原记录元数据可识别。
- 摘要基于实际传输字节计算；记录校验和时间过滤后保留原始 JSON 字节，仅统一 CRLF 行结束符，避免重新编码放大合法记录。未知字段和数值语义保持；Redacted 统计不计入历史占位符。
- 取消、部分来源不可用、归档发布失败和 cleanup 继续满足现有临时文件与句柄关闭规则。

**最小验证**：`go test ./internal/logging ./internal/control/client ./internal/control/server ./internal/control/protocol`，随后现有 `internal/integration/file_logging_export_test.go` 与原生快照集成场景。

## 10. T8：导出说明和文档策略同步

**主要文件**：`internal/tui/ui/exportlogs.go`、`strings.go`、`exportlogs_review_test.go`、`exportlogs_ux_test.go`、`internal/logging/export_json.go`、`AGENTS.md`、README 中英版本、`docs/architecture.md` 及相关日志历史设计。

共享导出 overlay 在表单、导出进行中、成功态和失败重试态都保留说明；使用既有 `theme.Danger`，文字遵循现有界面语言。建议英文文本：

> Logs are not redacted and may contain passwords, access tokens, full subscription URLs, and user configuration. Review them before sharing.

不新增确认弹窗/勾选项、不自动上传、不增加 ZIP 中的独立配置文件。ZIP manifest 同步说明原文策略。

先断言导出前/后说明存在，再验证红色样式、窄窗口换行和无颜色文本；可以使用现有 View/golden 测试，不需要连接真实 daemon 或用户配置做 UI 验证。

更新 `AGENTS.md` 时，把“日志不得出现 secret”替换为本会话授权的文件日志/日志导出规则；普通错误响应、事件、状态、FailureReporter 独立终端策略、业务写入者与权限规则保持明确。历史文档加替代说明，不擦除已实施事实。

**最小验证**：`go test ./internal/tui/ui ./internal/logging`，检查 Logs/System 两个入口和文档链接。不得修改 `CHANGELOG.md`。

## 11. T9：综合验收与模块登记收口

完整回归必须证明两个同时成立的结果：文件日志保留 fixture 原文，公开错误保持预期简洁契约。至少贯穿网络、文件、配置、进程、预期拒绝、取消、重试、回滚、nil logger、导出十类场景。

按风险执行：

```console
go test ./...
go test -race ./...
go vet ./...
gofmt -l .
```

跨平台编译在 PowerShell 临时作用域设置 `CGO_ENABLED=0`、`GOOS`、`GOARCH`，对 Windows/Linux/macOS 的 amd64/arm64 六个组合执行 `go build ./cmd/mihari`，输出到临时目录并恢复环境。不要把 `CGO_ENABLED=0` 套用于 race；缺少 race 工具链时记录原因，不自行安装系统依赖。

原生平台相关测试通过各 OS 已有 CI 验证；本机编译通过不能替代 Unix IPC、权限或服务行为的原生测试。若触及 Unix layout 安全边界，执行既有 `python -m pytest scripts/test/test_unix_layout_security.py -q`；不在本机安装真实服务或运行真实订阅。

最终登记每个范围模块：已修改且回归通过、无需修改且有证据、无出口的已接受边界；剩余缺口不能隐入“全仓测试通过”。覆盖率仅用于定位遗漏，不设置新固定门槛。

最终检查 Git 变更只包含任务文件，无用户既有修改、临时产物、CHANGELOG、依赖/toolchain 变更。报告实际运行的命令、结果和未验证项；计划中的命令不能写成已通过。

## 12. 本次设计交付检查

设计整理检查设计与计划的本地相对链接、引用代码路径、模块覆盖、任务依赖、长度与协议兼容、授权边界，并核查 Git 现有变更。本文列出的 Go/UI/集成/跨平台命令是验收要求；实施后的实际执行结果记录在审计总表，不能因存在测试文件或部分回归结果就标记全部通过。

设计范围已落到具体模块、关键文件和验收场景；T1 模块审计与各阶段实际验收结果见[审计总表](2026-09-14-full-error-logging-audit.md)，不以设计范围或只读定位替代完成证据。
