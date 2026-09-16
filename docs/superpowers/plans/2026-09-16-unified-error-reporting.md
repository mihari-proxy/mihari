# Issue #197：统一 CLI/TUI 错误汇报执行方案

日期：2026-09-16

状态：实现与源码入口核对已完成，见验收登记第 13 节。已 rebase 最新 dev，本地最终验收通过，正在推进 PR/CI/bot review；尚未交付。

设计依据：[统一错误汇报设计](../specs/2026-09-16-unified-error-reporting-design.md)。覆盖与证据：[验收登记](2026-09-16-unified-error-reporting-audit.md)。

## 1. 工作位置与边界

| 项目 | 值 |
| --- | --- |
| Worktree | `C:/Users/Kinema/Documents/modular_dev/mihari/.worktrees/fix-197-cli-setup-diagnostics` |
| 分支 | `fix/197-cli-setup-diagnostics` |
| 创建基线 | `origin/dev` / `6bd40d1` |
| 当前开发基线 | `ad6ecae`，已接入 #260、#262、#261 与 #263；本地最终验收通过 |
| 关联 issue | #197；范围已扩展到全部实际 CLI/TUI 错误入口 |
| 当前授权 | Q1–Q12 已确认；用户已授权完整实施、提交 PR，并每 10 分钟检查 CI/bot review 至可用检查全绿后汇报 |
| 交付原则 | 完整实施和验证后先 rebase 最新 dev，再 commit/push、创建指向 dev 的 PR；处理 CI/bot review，按 10 分钟间隔检查；合并仍需用户确认 |

仅在本 worktree 工作，保留主工作目录与旧 #197 worktree 的全部现有修改。开始执行时核对 HEAD、状态和最近的 AGENTS.md，不自动切换主工作目录、不覆盖其他任务。发现 dev 更新时先评估相关差异，不为了规划阶段同步而改动分支。

已确定且不重问：所有日志和错误汇报不脱敏；保留有界采集；CLI 直接概要加详情；JSON 兼容新增诊断和 warnings；F2 全局历史与滚动详情；同版本配套、旧端明确降级；有界内存历史；重复失败独立记录、不自动合并或关联恢复。

不新增依赖、持久化格式、TCP 控制接口、用户配置项或生产 CLI 功能。不改 CHANGELOG；不运行真实订阅、真实 mihomo、系统安装/卸载等 testenv。不借此清理其他 refactor 或废弃命令。

## 覆盖范围速览

本表供范围审阅；具体入口和回归证据在验收登记 A01–A36 中逐项维护。各行同时覆盖失败、已有 warning、部分成功及取消后的清理失败，不把成功结果改成失败。

| 展示入口 | 本次覆盖 |
| --- | --- |
| TUI Setup | 初始化读取、端口、core 安装、订阅、GeoIP、完成、取消后的结果确认 |
| TUI Overview | 汇总读取、降级及配置异常、最近操作详情 |
| TUI Proxies | 组/provider、模式与节点选择、GLOBAL 候选、单个/批量测速及部分完成 |
| TUI Subscriptions | 列表/详情/URL、新增/编辑/启停/proxy mode、刷新/切换/删除、首次下载及自动刷新 |
| TUI Connections | 连接流、关闭单个/全部、列偏好、GeoIP 查询 |
| TUI Rules | rules/provider 读取、单个/全部刷新及部分完成 |
| TUI Logs | 日志和监控流的读取、解码、中断、缓冲丢弃；导出及复制详情 |
| TUI WebGUI | 状态、安装/更新/激活/回滚/卸载/重装、打开及后续 reload |
| TUI System | 端口、日志、core/GeoIP、sysproxy/TUN、服务、安装/卸载、self 更新及本地交互 |
| CLI | 无参数 TUI 启动、status、daemon、core、proxy、connections、rules、traffic、logs、sub、panel、sysproxy、tun、service、self；Unix service apply 与实际 daemon 服务/安装校验入口；help/completion 输出失败 |
| 公共与后台出口 | IPC 认证/连接/解码、session 轮询/重连、scheduler/supervisor/健康检查、初始化/ready/退出、stdout/stderr、日志自身故障、cleanup/rollback |

统一验收链路：**原始原因 → 执行 owner → 快照及记录 ID → 响应/历史/本地结果 → CLI 文本与 JSON / TUI 概要与 F2 / 日志**。必须证明已采集原文沿链路保留，不能仅证明新增字段存在。

明确不扩展：浏览器面板自身错误 UI、独立安装脚本、未装配的 CLI 子命令、新业务命令、自动重试策略、错误文本合并计数或恢复关联、诊断数据库。Web 后端已有日志仍适用不脱敏原则，本地诊断接口不暴露给浏览器。

## 2. 阶段依赖与完成标准

### 范围审阅与交付物

上面的覆盖范围速览是供用户确认的具体清单。已确认的产品原则继续有效；范围确认不等于实现验收，现有未提交代码也不能作为已完成的证明。

本方案的执行交付物为：

1. **共享诊断链路**：同一份有界采集的原始原因贯穿 owner、IPC、本地结果、CLI、TUI 和日志，明确标记采集截断或详情不可用。
2. **用户可见结果**：CLI 文本直接展示概要与详情，JSON 保留结构化诊断；TUI 九页均可通过 F2 查看、滚动和复制，关闭后恢复原输入与焦点。
3. **逐入口覆盖证据**：按 A01–A36 登记失败、warning、部分成功、后台及清理路径；每项提供实现位置、回归测试和实际验证结果。
4. **契约与行为证明**：错误码、退出码、已提交状态及 revision 保持原义；成功后的 warning 不改成失败，查询详情不重做业务操作，旧端明确降级。
5. **最终验证报告**：列出测试、race、静态检查、覆盖率对比及六轴无 CGO 构建结果；环境限制单列，文档与最终实现同步。

审查以这些可观察结果为准，不以改动文件数、新增字段数或仅有全仓测试通过替代。各阶段任务、入口和验证命令如下。

| 阶段 | 任务 | 前置 | 完成标准 |
| --- | --- | --- | --- |
| A 契约与基础 | T0–T3 | 设计 | 有覆盖登记、明确 DTO、共享原文 formatter、有界历史与生命周期 |
| B 端到端数据 | T4–T6 | A | 同一发生记录从 owner 到响应/历史/流，支持 warnings、旧端和边界失败 |
| C CLI | T7 | B | 实际命令及独立 stderr 出口保留详情，JSON 与退出码正确 |
| D TUI 公共能力 | T8–T9 | B | F2、历史同步、焦点/滚动/复制、本地错误和后台事件可用 |
| E 功能接入 | T10–T13 | C、D | 九个页面及所有 CLI/后台/本地流程逐项有回归证据 |
| F 收口 | T14–T15 | E | 跨层集成、规则文档、跨平台验证和覆盖登记全部收口 |

建议执行顺序：T0 → T1 → T2 → T3 → T4 → T5 → T6 → T7 → T8 → T9 → T10 → T11 → T12 → T13 → T14 → T15。逻辑阶段是审查边界，不预设固定提交数，不要求并行 agent。不得发布只改变错误采集、客户端尚不能消费详情的中间版本。

### 阶段检查点

| 检查点 | 必须可观察的结果 | 未满足时的处理 |
| --- | --- | --- |
| A → B | 合成原始错误可生成有界快照；同一快照身份稳定；不同发生不合并；容量与并发测试通过 | 留在基础阶段修复，不先接入所有页面 |
| B → C/D | 同一次实际失败的响应和历史身份一致；成功 warnings 不改变业务结果；旧端、淘汰与流中断均有明确表现 | 修复 owner 或协议传播，禁止在前端拼接假原因补洞 |
| C/D → E | 一条远端错误和一条无 daemon 的本地错误均可在 CLI 及 TUI 查看；F2 滚动、复制、焦点恢复成立 | 修复公共出口，页面接入不得各自实现详情机制 |
| E → F | A01–A36 已细分到实际入口并有证据；九页、CLI、后台、启动及清理出口无遗漏 | 按缺失入口补回归；全仓测试通过不能替代范围核查 |
| F → 交付 | 集成、test/race/vet、六轴 CGO0 构建及文档检查结果已登记；明确未验证项 | 修复失败或如实列出环境限制，不声称未运行项目通过 |

Q12 的复杂度边界贯穿各阶段：只做发生记录与 ID 幂等。相同文本的再次失败仍新增记录，不实现内容去重、重复计数、恢复关联或自动清理已恢复错误。容量淘汰及缺口提示承担长期后台重复失败的内存控制。

### 当前执行位置

T1–T14 的实现、实际入口核对与跨层回归已收口，证据见验收登记第 6–13 节。T5 的完整 JSON/精确 4 MiB/引用头边界、T6 的取消与终止帧写失败、页面结果与本地同步错误、后台 owner 和原始 cause 传播均已有回归。

工作分支已再次 rebase 到 `ad6ecae`，纳入 #263 Conns 详情改版。保留新布局、滚动及关闭交互；五种详情状态新增 F2 打开/关闭后内容和滚动位置不变的回归。T15 本地验证见验收登记第 14–15 节，接下来按授权提交 PR 并处理 CI/bot review。旧检查点不替代最终验证。

## 3. 全任务 TDD 规则

每个行为变更遵循：

1. 先在邻近测试或 integration 加入真实缺口的回归断言；新 DTO/接口先补可编译的最小结构。
2. 用 `go test -count=1 -run '^TestName$' ./internal/<package>` 跑最小范围，记录行为断言失败。编译失败或测试未匹配不算 Red。
3. 写最小实现使其通过，不混入无关重构。
4. 跑对应包及跨包边界测试，记录结果；并发任务另跑目标 race。
5. 保持通过后重构；把登记状态、测试名、命令和结果同步到审计表。

下列测试名称是拟新增的行为目标，不代表测试已经存在或通过。禁止为了“先红”增加无意义的生产 stub；若阶段是保持行为的 formatter 搬迁，先锁定现有行为，再为新的结构化结果写失败测试。

## 4. T0：建立逐入口实施登记

**文件**：本方案、验收登记；只读检查 CLI 注册树、TUI 页面/浮层/session、各 owner 的 reporter 接线。

把登记表每个范围行细分到实际入口，记录：原因源、变换点、执行 owner、记录身份、响应/事件、文本/JSON/TUI/日志出口、测试缺口。检索 `APIError` 重建、固定 fallback、丢弃 cleanup error、仅更新 stale、丢弃 warnings、Redactor 调用和路径隐藏，逐项判断，不能机械地给每个 `if err != nil` 加报告。

**完成条件**：全部认可范围可映射到 T1–T15；原文已经保留的入口有证据，尚未核查的标为待审计。保持未装配 CLI 命令未装配。

## 5. T1：类型化诊断与协议基线

**修改入口**：`internal/control/protocol/{error,status,runtime}.go` 及各成功结果 DTO；`internal/diagnostics/{error,record}.go`。

**新增候选**：`internal/control/protocol/diagnostics.go`、`diagnostics_test.go`；`internal/diagnostics/snapshot.go`。

**Red 目标**：

- `TestDiagnostic_OptionalFieldsPreserveLegacyEnvelope`：旧 code/message/details/schema 未改变，诊断省略与存在均可解码。
- `TestDiagnostic_RoundTripPreservesOriginalContent`：合成 token、URL、路径、多行配置经过 JSON 编解码不变。
- `TestDiagnostic_WrapAndClonePreserveSnapshot`：所有 wrapper/normalizer 保留同一诊断身份和内容，`errors.Is/As` 仍找到原因。

**实现步骤**：

1. 定义强类型诊断快照、引用、warning、列表/单条读取结果；协议包不导入 diagnostics，避免循环依赖。
2. 明确 `available/unsupported/expired/unavailable` 等详情状态与 daemon instance/sequence；不存在的诊断不写假原文。
3. APIError 增加可选 diagnostic，真实部分成功 DTO 增加可选 warnings；检查 NewError、Wrap、复制 APIError 的所有路径。
4. 定义 capability、单条采集限制、响应内联预算和历史初始容量；不修改原有 Details 键语义或错误退出映射。
5. 如需移动 operation context 元数据，使用 diagnostics 可依赖的公共归属，logging 保持兼容调用，不引入 diagnostics → logging 环。

**验证**：`go test ./internal/control/protocol ./internal/diagnostics`。

## 6. T2：共享有界原文 formatter 与终端显示转义

**修改入口**：`internal/logging/{diagnostics,handler}.go`、`internal/diagnostics/http.go` 与现有原文/边界测试。

**新增候选**：`internal/diagnostics/format.go`、`format_test.go`、`display.go`、`display_test.go`。

**Red 目标**：原始包装/合并原因和已有 DiagnosticText 保留；结构化截断原因可靠；原文自身包含 `[truncated]` 不误判；typed nil、循环、共享 DAG、节点/深度限制受控；终端转义不执行 ESC/OSC/控制字符且不改变底层原文。

**实现步骤**：

1. 将现有 formatter 抽取为返回结构化 capture 结果的共享实现，保留 256 KiB 和原因图预算。
2. HTTP 上游已经截断时显式传递事实；未知原始长度不编造 original_bytes，不通过搜索标记推断。
3. logging 使用相同采集结果；保留 JSONL 物理分片与原始内容，不为 IPC 复用日志分片。
4. 显示转义与原文存储分离。TUI 复制和 JSON 解码使用原文；CLI/TUI 终端只渲染转义版本。
5. 不新增堆栈采集、不读额外配置；既有非法 UTF-8 采集替换/标识保持，不扩成二进制转储。

**验证**：`go test ./internal/diagnostics ./internal/logging`。保留现有日志/导出原文回归，不整体删除原有安全测试。

## 7. T3：有界历史与发生记录身份

**新增候选**：`internal/diagnostics/history.go`、`history_test.go`、`owner.go`、`owner_test.go`。

**Red 目标**：条数和总字节双上限、最旧淘汰、分页/查询、进程重启实例变化、未知/已过期区分、并发发布/读取、相同文本两次发生得到不同 ID、同一快照复制保持 ID。

**实现步骤**：

1. daemon 默认 256 条/32 MiB，本地客户端 128 条/16 MiB；同时约束 ID 和元数据，单条详情仍限 256 KiB。
2. 实例 ID 和时钟可注入；sequence 单调推进，游标绑定实例，不通过文本查找记录。
3. owner 在发布前创建快照/ID，向内存与日志分发；日志级别仅控制文件出口，不关闭历史采集。
4. 返回不可变快照/深拷贝；formatter 在锁外执行，历史锁只保护短时内存操作。
5. 历史读取失败不递归写回同一历史；窗口正在查看的记录只固定一份有界快照，关闭后释放。

**验证**：`go test ./internal/diagnostics`、`go test -race ./internal/diagnostics`。

## 8. T4：owner 装配、失败与 warnings 的用例传递

**修改入口**：`internal/runtime/diagnostics.go`、`manager.go`、实际 use case 结果类型；`internal/app`、`internal/daemon`、`cmd/mihari/main.go`；`internal/tui/ui/logging.go` 的本地任务接线。

**Red 目标**：nil/关闭/级别过滤 logger 下历史仍可用；一次实际失败只有一个发生记录；缓存重放和并发等待者复用记录；真实第二次执行另有 ID；commit 后 sync 警告仍成功且原因可传；日志 writer 失败不递归。

**实现步骤**：

1. 进程装配建立诊断 owner，注入 daemon 与客户端本地任务；普通 CLI 不创建文件 logger。
2. 错误发布后的 ID/快照通过内部错误包装或结果显式带回，替换“report 后无返回凭据”的断点。
3. 现有 AlreadyReported/实际 owner 去重只针对同一次执行，不能变成跨事件文本去重。
4. collectWarning 保留结构化 warning 并贯穿用例结果，report 和返回共享同一发生 ID；不从日志反解析。实现采用显式创建、调用范围内的 diagnostics.Result 接收器（通过 context 传递）；operation cache 保存 warning 引用，重放时回传原记录。控制适配器把接收器内容映射到强类型 WarningOutcome，避免给只返回 error 的领域接口和持久化模型混入协议字段。
5. 启动失败、logger 建立前及关闭后有直接诊断出口；启动早期可采集到的记录转交进程 owner，交接不能重复发布。
6. 后台 scheduler/supervisor/health/config 的现有实际汇报也进入历史；继续维持现有任务生命周期和重试策略。

**验证**：目标 runtime/app/daemon/cmd 包测试，加 `internal/integration/operation_diagnostics_test.go` 对应场景。并发 owner 测试必须通过 race。

## 9. T5：同步 IPC、warnings 与历史查询

**修改入口**：`internal/control/server/{server,runtime,diagnostics}.go`、`internal/control/client/{client,runtime}.go`；按 T1 结果扩展真实成功 DTO。

**新增候选**：server/client `diagnostics.go` 或独立 `diagnostic_history.go` 及邻近测试（已有 diagnostics.go 时扩展，不覆盖）。

**Red 目标**：认证 IPC 原文往返、同一失败响应与历史 ID 一致；成功 warnings 保持 HTTP/exit/revision 语义；未认证拒绝；旧端无能力；列表后淘汰；最坏 JSON 转义；响应预算不足改用引用；详情查询失败不重做 mutation。

**实现步骤**：

1. Status 宣告 diagnostics-v1，添加只读列表与单条详情端点；只接认证本地 IPC，不接 Web gateway。
2. 列表只返回有界元数据、实例和序列水位，单条详情单独取；返回过期/未知/重启/不可用的明确状态。
3. 同步响应内联原始诊断文本总预算 256 KiB。超预算使用引用而不再次截断内容；warning 元数据数组也有明确边界。
4. 完整 JSON 编码后验证大小；普通 response 4 MiB 上限保持，不能序列化后切字节或把编码超限变为已提交业务失败。
5. client 类型化读取/补齐详情。补取失败保留原业务结果及诊断引用，附“详情不可用”；不得更改成功为失败。
6. 非持久化历史淘汰可能造成引用失效，客户端必须明示，不能拿相似消息代替。
7. 正常同版本路径优先。旧端可通过 status capability 或初始请求协商识别，不为每个命令增加不必要探测，不要求重新运行操作。

**验证**：`go test ./internal/control/protocol ./internal/control/server ./internal/control/client`；新增 integration 覆盖 fake 业务失败与成功警告。

## 10. T6：流终止诊断与客户端生命周期

**修改入口**：`internal/control/protocol/runtime.go` StreamEvent；server/client `runtime.go`；`internal/tui/session`。

**Red 目标**：支持能力的流在 close 前得到终止诊断引用；旧客户端只收到原有关闭语义；终止帧写失败只报告实际 transport 失败；正常 EOF/主动取消不伪装业务失败；流读取最大 1 MiB 不被诊断正文突破。

**实现步骤**：

1. 明确流请求的 opt-in 与终止事件字段，不把详情塞进 WebSocket close reason。
2. 流中只携带引用；客户端只读补取，失败时保留终止类型和“远端详情未收到”。
3. 客户端 callback/stdout writer 失败属于本地诊断，不标为远端业务失败。
4. 读取、补取、session 关闭均传播 context；listener/stream/response body 的关闭路径有测试。

**验证**：控制 client/server 流测试、TUI session 测试、CLI stream 测试及对应 race。

## 11. T7：CLI 与独立终端出口

**修改入口**：`internal/cli/{root,runtime,stream,service,service_apply,service_installation,self,subscription,panel}.go` 和其他命令 renderer；`cmd/mihari/{process,main}.go`、平台装配；`internal/logging/config.go`、`internal/tui/run.go`。

**Red 目标**：原 #197 的普通 SetupError/PrepareLocalRoot 文本及 JSON 含真实原因；所有既有 code/exit 保留；本地错误无 logger 仍可见；多行 cause/警告可读；成功附警告 exit 0；stderr writer 故障不递归；普通 JSON 不混文本；路径/token 不被替换。

**实现步骤**：

1. Execute 统一渲染概要、code、可用 operation ID 和原文详情；归类前保留原 error，避免 normalize 后丢 cause。
2. 文本和 JSON 从同一诊断对象读取；已有 Hint 保留为提示，不混进原始 cause 或伪造新原因。
3. 将各命令直接 warning 输出接入统一结果渲染；成功 JSON 增加 warnings，失败 stderr 单个 envelope。
4. 覆盖无参数 TUI 失败、daemon 前台/Unix 服务/安装校验入口、本地服务和提权、self/update/channel、purge 清理及浏览器打开。
5. 移除错误汇报实际路径上的 Redactor 和路径隐藏；日志资源失败继续使用独立出口，但带原始具体原因。
6. 不激活未装配的 service 子命令，不给正常 CLI 增加日志文件，不把 stdout/stderr 输出故障吞掉。

**验证**：`go test ./internal/cli ./cmd/mihari ./internal/logging`；平台特有行为使用已有 fake/临时目录测试。

## 12. T8：TUI 全局诊断模型和 F2 窗口

**修改入口**：`internal/tui/{model,modal,operation_ledger}.go`、`internal/tui/ui/{focus,keymap,strings,monitor}.go`。

**新增候选**：`internal/tui/diagnostics.go`、`diagnostics_modal.go` 及测试。

**Red 目标**：全局/输入模式/确认浮层 F2；当前操作→当前页→最新的选择顺序；列表与详情切换；窄屏、长行/多行滚动；复制原文；复制失败保留窗口；后台新记录不抢焦点/滚动；Esc 恢复表单/确认状态；Ctrl+C 退出；相同 ID 幂等、不同 ID 同文不合并。

**实现步骤**：

1. 建立 daemon 引用和本地快照的统一视图，列表元数据与正文缓存分别有界；本地/daemon instance 不混淆。
2. 复用现有 modal 的滚动/复制机制，增加记录列表；不为每页实现一套弹窗。
3. Tab 切换焦点、方向键移动/滚动、PgUp/PgDn、Home/End、c、Esc；小屏以列表/详情模式切换。
4. F2 临时覆盖已有浮层但保留其状态；关闭恢复，不隐式确认或提交表单。
5. 当前详情固定一个有界快照，列表淘汰时可继续看；关闭释放，外部新消息不切换选中项。
6. 空态、不支持、过期、读取失败、截断均可区分；帮助/footer 和最近操作详情入口同步。

**验证**：`go test ./internal/tui ./internal/tui/ui`，增加真实 View 尺寸和文本断言，不依赖截图字符串替代行为测试。

## 13. T9：session 后台同步和局部读取失败

**修改入口**：`internal/tui/session/{session,client,decode}.go`；root 的 service/sysproxy/TUN poll 与 action dispatch。

**Red 目标**：logger 禁用仍收到诊断；重连续读、实例重启、淘汰缺口、乱序返回；单资源读取失败可见且不误判整个 daemon 离线；history 查询错误不递归；关闭全部 goroutine；正常取消不弹假错误。

**实现步骤**：

1. session 生命周期内有界同步历史元数据，详情按需取。不能每次 poll 重新获取所有大正文。
2. 为实际执行失败的 core/subscriptions/rules/providers/preferences/webgui/logging 等读取发出带诊断的事件，不只 `return` 或清空快照。
3. 区分单资源错误与连接/认证不可用；可继续的独立读取继续，连接失效走现有重连。保留既有并发/重试预算，不虚构未执行请求的结果。
4. 页面概览保留旧快照和失败提示；最近错误历史不随恢复自动删除或标记 recovered。
5. session 只按诊断 ID 避免响应/历史重复插入；重复发生的后台失败自然进入独立记录。

**验证**：`go test ./internal/tui/session ./internal/tui`、`go test -race ./internal/tui/session ./internal/tui`。

## 14. T10：Setup 与订阅全链路

**修改入口**：`internal/tui/pages/setup`、`pages/subscriptions`、`internal/tui/setup.go`、`internal/runtime/{onboarding,subscription}.go` 中真实存在的入口；`internal/subscription`、`internal/onboarding`；CLI sub。

**Red 目标**：Setup 加载/保存/安装/GeoIP/完成均可 F2；取消后不确定结果保留诊断；revision 冲突不丢输入；添加保存成功但首次下载失败为部分成功；订阅编辑/URL查看/自动刷新失败保留原文；复制带合成 URL/token 不被替换。

**实现步骤**：

1. Setup 的 fail/safeText 替换为统一诊断入口，保留概要、步骤恢复、输入及操作 ID。
2. 订阅页面不再只保留 APIError.Message；列表 LastError、局部结果与后台失败建立明确诊断引用，不靠相同文本关联。
3. 覆盖新增、编辑、启停、proxy mode、单个/全部刷新、切换、删除、URL reveal、保存后 readback；更新对应 CLI renderer。
4. 只修复回归证明的 cause 丢失，不重构订阅调度或 Setup 状态机。

**验证**：目标页面、subscription/onboarding/runtime/cli 测试及 `internal/integration/setup_operations_test.go`、`subscription_edit_test.go` 对应场景。

## 15. T11：Overview、Proxies、Connections、Rules、Logs、WebGUI

**修改入口**：对应六个 `internal/tui/pages` 包，相关 runtime/client/CLI；`internal/mihomo` 与 `internal/panel` 的实际 cause 源。

**Red 目标**：

- Overview：降级/配置/汇总失败能定位详情；不凭最后一条全局错误替换对应对象错误。
- Proxies：provider 读取、routing、选择、GLOBAL 过期、单个/批量测速失败与部分成功有各自记录。
- Connections：流断开、close/close-all、列偏好保存、GeoIP 查询的原有静默失败可见。
- Rules：列表/provider 读取、单个/全部刷新、部分完成保留具体失败及已完成事实。
- Logs：断流/缓冲丢弃/详情序列化失败；监控流没有伪造独立 Traffic 页面。
- WebGUI：状态/安装/更新/激活/回滚/卸载/重装/open URL/浏览器打开/操作后 reload 失败均可查。

先每页加入目标行为失败测试，再改结果消息/快照/渲染与统一诊断入口。按审计登记逐入口完成，禁止只接每页的第一个失败分支。

**验证**：六页面包、相关 CLI/runtime/mihomo/panel 包；provider_delay、control、runtime 等相关 integration。

## 16. T12：System、服务/更新/安装浮层和日志导出

**修改入口**：`internal/tui/pages/system`、`internal/tui/ui/exportlogs.go`、`internal/tui/run.go`、相关 app/service/update/elevate/platform、CLI service/self。

**Red 目标**：端口/logging/sysproxy/TUN/core/GeoIP/通道/版本检查；服务状态与安装启停；安装预览/repair/fresh；卸载预览与清理；更新成功但服务同步/cleanup/relaunch 失败；导出成功但清理失败；复制和目录打开失败均保留原文且主结果语义正确。

**实现步骤**：

1. System 的短字符串 outcome 与 actionErrorDetail 接入统一快照，维持各行状态及 confirmation 流程。
2. prepared task 的 prepare/apply/discard、取消、退出清理分别归属实际 owner；不能给未执行动作记录失败。
3. 导出、日志初始化和关闭错误也进入统一详情或终端 fallback；不影响导出原文字节/摘要。
4. 独立本地任务无 daemon 也能报告；daemon history 不可用不阻止本地详情。
5. 用 fake service/updater/uninstaller/clipboard/browser 验证，不安装系统服务、不触碰真实数据根。

**验证**：System/ui/tui/cli/app/service/update/logging/cmd 中受影响测试，self_update_service、uninstall、file_logging_export 等 integration。

## 17. T13：补齐底层原因及后台 owner 审计

**范围**：diagnostics、logging、config/state/preferences/onboarding、subscription、core/geoip/panel、mihomo、supervisor、sysproxy/tundetect、service/update/elevate、platform、app/runtime/daemon、control、CLI/TUI、cmd 装配。

**逐项流程**：

1. 从 T0 登记逆向追踪原始 error 是否被 APIError/errors.New/fixed string 提前替换。
2. 如已正确保留，列出回归与出口证据，标“无需改动”；有缺口则先写行为回归，再最小修复。
3. 后台 scheduler、健康检查、重启、日志自身失败必须走已装配 owner；不能只修有 HTTP 响应的路径。
4. 检查 warning 在成功返回、缓存重放、等待者和取消清理中的传播，不重复发布同一次发生。
5. 不再使用脱敏函数处理日志或错误汇报。未使用的 Redactor 实现可保留，本期不做无关清理；Web 凭据隔离、正常业务字段可见性不属于移除范围。

**完成条件**：登记中每个入口均有可观察结果与回归证据；没有“只有 fixed fallback”或“只写文件、客户端无法定位”但被误标完成的条目。

## 18. T14：跨层集成与文档一致性

**新增候选**：`internal/integration/unified_diagnostics_test.go`、`diagnostic_history_test.go`、`diagnostic_warnings_test.go`、`diagnostic_stream_test.go`。

**必验端到端场景**：

1. fake mihomo HTTP 失败携带合成凭据/URL/正文 → owner → IPC → CLI JSON 与 TUI 详情/复制，内容一致。
2. 本地凭据或目录初始化失败无 logger、无 daemon，CLI 仍展示原始原因。
3. mutation 已提交后同步 warning → exit 0、revision 与状态不回滚，warnings 含原因。
4. 同一失败经响应和历史仅一条；重复执行相同失败为两条；缓存重放不增加发生记录。
5. TUI 重连、daemon 重启和记录淘汰；新旧能力矩阵（新新、新旧、旧客户端兼容载荷）。
6. 256 KiB 采集/最大 JSON 转义、内联预算转引用、详情过期和流终止失败；绝不重放业务 mutation。
7. Windows named pipe 与 Unix socket 路径及授权保持；Web gateway 无本地诊断路由。
8. 关闭/取消/IO故障的清理与非递归 fallback。

更新 AGENTS.md、CONTRIBUTING 对应规则、README 中英文、commands、architecture、历史日志设计中的当前策略指向；明确新诊断字段和 F2 操作。旧测试中“公开错误不可携带 cause”的断言只在新批准的 CLI/TUI/本地诊断范围内改写；保留认证、Web secret 隔离、路径权限和不读真实数据的断言。

设计所引旧 architecture spec 不存在时使用当前 `docs/architecture.md`，并修正文档中失效指向；不编造旧文档内容。

## 19. T15：最终验证与交付检查

先完成目标包与相关 integration 后执行以下最终检查，记录执行平台、commit/diff 基线、实际输出与失败原因：

```console
go test ./internal/integration
go test ./...
go test -race ./...
go vet ./...
golangci-lint run ./...
python -m pytest scripts/test/test_unix_layout_security.py -q
gofmt -l cmd internal
git diff --check
git status --short --branch
```

Windows race 需要可用工具链；不得把 `CGO_ENABLED=0` 的发布构建环境沿用到 race。无法运行时明确列为未验证并说明原因，不把普通 test 当 race 替代。

Lint 使用 CONTRIBUTING 与 CI 固定的 golangci-lint v2.12.2；Unix layout 检查使用仓库既有无真实账户/挂载操作的测试。工具缺失时记录环境限制，最终仍核对对应 CI 结果，不把缺失视为通过。

跨平台无 CGO 编译按以下六轴执行，每轴使用独立输出路径，完成后恢复 shell 的 GOOS/GOARCH/CGO_ENABLED；产物不提交：

| GOOS | GOARCH | 命令形态 |
| --- | --- | --- |
| windows | amd64 | `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o <临时输出>.exe ./cmd/mihari` |
| windows | arm64 | 同上，GOARCH=arm64 |
| linux | amd64 | `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o <临时输出> ./cmd/mihari` |
| linux | arm64 | 同上，GOARCH=arm64 |
| darwin | amd64 | `CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -o <临时输出> ./cmd/mihari` |
| darwin | arm64 | 同上，GOARCH=arm64 |

表中是环境变量意义，不是 PowerShell 可直接执行的前缀语法；PowerShell 使用 `$env:CGO_ENABLED = '0'`、`$env:GOOS`、`$env:GOARCH` 分别设置后执行 build。先保存到任务专用变量，finally 恢复，不复用 HOME 等系统变量。

覆盖率：在同平台/工具链比较修改前后的受影响包与全仓基线，临时 coverage 输出不提交。下降时解释结构原因及行为覆盖，不能靠无断言测试补数字。

**关闭工作项前必须满足**：

- 所有审计入口为“已实现并验证”或“无需改动（有证据）”；未执行的平台/testenv 验证单列。
- 采集→IPC→CLI/TUI→复制/JSON 的原文和截断语义闭环；按记录 ID 关联，无内容去重。
- warning/部分成功/revision/exit 语义未退化，旧端提示明确，诊断查询失败不重放 mutation。
- 九页、实际 CLI、后台、独立启动/清理/fallback 均完成接入；未激活新命令。
- 文档规则与实现一致，CHANGELOG 无改动，无临时产物、无用户修改覆盖。
- 交付准确 diff、验证结果及剩余限制；按已有授权 commit/push/创建 PR，随后每 10 分钟检查 CI 与 bot review；可用检查全绿后汇报，不自行合并。

## 20. 执行记录模板

每任务完成时追加：

```text
任务：Tn
基线/工作树：
新增行为断言：
Red 命令与正确失败原因：
最小实现文件：
Green 与相关集成/race：
采集/契约/生命周期影响：
审计登记行：
未验证项及原因：
```

本文件是执行与验收依据，任务是否完成以对应证据为准。出现新事实时只调整受影响任务与登记，不默默改变已经确认的产品决策；涉及新依赖、持久化或超出已批准的协议/网络边界时另行说明。
