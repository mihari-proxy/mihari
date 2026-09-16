# Issue #197：统一错误汇报覆盖与验收登记

日期：2026-09-16

状态：实现、入口核对及 rebase 后本地验收完成，见第 13–15 节；PR、CI 与机器人评审待完成。

依据：[设计](../specs/2026-09-16-unified-error-reporting-design.md) · [执行方案](2026-09-16-unified-error-reporting.md)。

## 1. 状态与证据规则

状态：待审计 → 确认缺口 → 已实现待验证 → 已实现并验证；已有完整行为可标“无需改动（附证据）”。不能以已有测试文件、grep 命中或一次全仓通过替代逐入口证据。

实施时把分组行细分到每个实际入口；每条记录补齐：原始 cause 源、转换点、实际 owner、记录 ID 的传播、客户端出口、Red/Green 测试、验证命令、残余限制。不存在的功能标“不适用”并说明，不算实现完成。

## 2. 范围登记

| ID | 范围/入口 | 必须证明的行为 | 任务 | 状态 | 回归与执行证据 |
| --- | --- | --- | --- | --- | --- |
| A01 | protocol/APIError/clone/Wrap | 可选详情原文往返；code/message/details/schema/exit 原义保持 | T1、T5 | 已实现并验证 | protocol `TestDiagnostic_RoundTripPreservesOriginalContent`、`TestDiagnostic_OptionalFieldsPreserveLegacyEnvelope`；diagnostics `TestDiagnostic_WrapPreservesSnapshotAndCause`；WarningOutcome 复制/引用/追加的独立性测试；第 12 节。 |
| A02 | formatter/HTTP/原因图 | 原文、包装/合并/已有堆栈；typed nil/循环/采集上限与结构化截断 | T2 | 已实现并验证 | `TestCapture_*`、`TestHTTPOriginal_*`，既有 logging formatter typed-nil/stack/合并原因测试；上游各 HTTP producer 的 BodyTruncated 回归见第 8 节；第 12 节。 |
| A03 | 终端显示/复制 | 控制字符转义仅作用于显示，复制/JSON 解码保留采集原文 | T2、T8 | 已实现并验证 | 第 6–13 节；最终复验见第 14 节 |
| A04 | 历史/owner | 双上限、淘汰、实例/序列、并发、同文本不同发生 ID | T3 | 已实现并验证 | `TestHistory_RecordLimitAndExplicitExpiry`、`ByteLimitDoesNotAlterReturnedOccurrence`、`RestartUnknownAndImmutableCopies`、`ConcurrentOccurrenceIdentity`；第 12 节。 |
| A05 | logger/owner 装配 | 无 logger/日志级别过滤仍可取得详情；不重复发布同一执行 | T4 | 已实现并验证 | `TestOwner_HistoryDoesNotDependOnFileOutlet`、`TestReportError_PreservesOccurrenceIDAndClassification`、`TestDaemonAssembly_*`；Run 装配共享进程 owner，文件过滤位于 history 后；第 12 节。 |
| A06 | warnings/commit 后失败 | 原因贯穿成功结果，exit/revision/状态不回滚 | T4、T5、T7 | 已实现并验证 | 第 6–13 节；最终复验见第 14 节 |
| A07 | 同步 IPC/历史端点 | 认证、原文、分页、预算、过期/未知/重启、旧端提示 | T5 | 已实现并验证 | client/server 的历史认证、分页、详情元数据及旧端测试；完整 JSON/精确 4 MiB 的真实 IPC 回归见第 10 节；查询不重放 mutation；第 12 节。 |
| A08 | 控制流/终止 | 小引用帧、旧客户端关闭行为、补取失败不重放操作 | T6 | 已实现并验证 | `TestStreamDiagnostics_TerminalReferenceRequiresOptIn`、`NormalCancellationDoesNotCreateOccurrence`、`TerminalWriteFailureKeepsBothOccurrences`；client `TestStreamTerminal_*`；第 12 节。 |
| A09 | CLI root/SetupError/参数 | 普通文本及 JSON 原因，稳定退出码，写失败不递归 | T7 | 已实现并验证 | 第 6–13 节；最终复验见第 14 节 |
| A10 | daemon/进程启动与退出 | 前台/服务/launchd/安装校验、监听/ready/清理/启动 fallback | T4、T7、T13 | 已实现并验证 | 第 8–13 节；最终复验见第 14 节 |
| A11 | F2/modal/最近操作 | 列表/滚动/复制、当前上下文优先、焦点恢复、同 ID 幂等 | T8 | 已实现并验证 | `TestDiagnosticsF2_*` 覆盖九页、当前动作优先、滚动/复制、旧浮层/输入恢复、选中快照淘汰、迟到详情及 Ctrl+C；`TestDiagnosticKey_DiscoverableInEveryPageAndInputMode`；第 12 节。 |
| A12 | session/认证/重连 | 有界同步、独立资源读取失败、断线/重启、无递归查询失败 | T9 | 已实现并验证 | session 历史游标/重启、独立读取/stream owner 测试；root `TestDiagnostics_HistoryQueryFailureDoesNotEnterHistory` 验证查询失败仅占临时槽且可查看/复制；第 12 节。 |
| A13 | Setup 加载/端口/core | 所有实际失败包含详情，输入和步骤恢复不变 | T10 | 已实现并验证 | 第 7–13 节；最终复验见第 14 节 |
| A14 | Setup 订阅/GeoIP/完成/取消 | 部分成功、结果不确定、revision conflict、settlement | T10 | 已实现并验证 | 第 8–13 节；最终复验见第 14 节 |
| A15 | Subscriptions / CLI sub | 列表/详情/URL/新增/编辑/启停/proxy mode/刷新/切换/删除 | T7、T10 | 已实现并验证 | 第 8–13 节；最终复验见第 14 节 |
| A16 | 订阅自动刷新/首次下载 | 后台失败、已保存但首次下载失败、LastError 来源明确 | T10、T13 | 已实现并验证 | 第 12–13 节按实际入口补齐；最终复验见第 14 节 |
| A17 | Overview / CLI status | 汇总/降级/配置错误可定位正确详情，不误关联最新全局错误 | T7、T11 | 已实现并验证  第 13 节实际入口与回归；第 14 节最终复验 |
| A18 | Core / CLI core | 状态/安装/更新/通道/重启/健康/自动重启 | T7、T12、T13 | 已实现并验证  第 13 节实际入口与回归；第 14 节最终复验 |
| A19 | Proxies / CLI proxy | 组/provider/routing/mode/选择/GLOBAL/单个与批量测速 | T7、T11 | 已实现并验证 | 第 8–13 节；最终复验见第 14 节 |
| A20 | Connections / CLI connections | 连接流、关闭单个/全部、列偏好、GeoIP | T7、T11 | 已实现并验证 | 第 8–13 节；最终复验见第 14 节 |
| A21 | Rules / CLI rules | 列表/provider/单个与全部刷新/部分完成 | T7、T11 | 已实现并验证 | 第 8–13 节；最终复验见第 14 节 |
| A22 | Logs / CLI logs/traffic | 四类监控流、读取/解码/中断/缓冲溢出/序列化 | T6、T7、T11 | 已实现并验证  第 13 节实际入口与回归；第 14 节最终复验 |
| A23 | WebGUI / CLI panel | 状态/install/update/use/rollback/uninstall/reinstall/open/reload | T7、T11 | 已实现并验证 | 第 8–13 节；最终复验见第 14 节 |
| A24 | System proxy/TUN / CLI | status/enable/disable/force、权限/冲突、应用/回滚 | T7、T12 | 已实现并验证  第 13 节实际入口与回归；第 14 节最终复验 |
| A25 | System 配置/本地交互 | 端口/logging/偏好、剪贴板、目录/链接打开 | T12 | 已实现并验证 | 第 8–13 节；最终复验见第 14 节 |
| A26 | 服务 / CLI service | status/install/uninstall/reinstall/start/stop/restart、提权拒绝 | T7、T12 | 已实现并验证  第 13 节实际入口与回归；第 14 节最终复验 |
| A27 | 安装浮层 / Unix apply | inspect/plan/repair/fresh、请求/预览/事务/校验、部分成功 | T7、T12 | 已实现并验证 | 第 12–13 节按实际入口补齐；最终复验见第 14 节 |
| A28 | 卸载/purge | preview/执行/停机/关闭/清理，未触碰真实系统服务 | T7、T12 | 已实现并验证 | 第 12–13 节按实际入口补齐；最终复验见第 14 节 |
| A29 | Self / CLI self | version/channel/check/download/verify/prepare/apply/discard | T7、T12 | 已实现并验证 | 第 8–13 节；最终复验见第 14 节 |
| A30 | 更新后部分成功 | 服务副本同步/cleanup/relaunch 不掩盖已更新事实 | T7、T12 | 已实现并验证  第 13 节实际入口与回归；第 14 节最终复验 |
| A31 | 日志导出/导出浮层 | 来源/范围/冲突/取消/读写/清理 warning/路径复制 | T12 | 已实现并验证 | 第 12–13 节按实际入口补齐；最终复验见第 14 节 |
| A32 | 日志自身失败 | open/write/rotate/close/FailureReporter 原文，无递归或 logger 依赖 | T4、T7、T12 | 已实现并验证 | 第 8–13 节；最终复验见第 14 节 |
| A33 | 跨模块底层原因 | config/state/platform/mihomo/资源安装等 cause 不提前丢弃 | T13 | 已实现并验证 | 第 8–13 节；最终复验见第 14 节 |
| A34 | 背景 owner | scheduler/supervisor/health/重启的实际汇报有客户端可查记录 | T4、T13 | 已实现并验证 | 第 12–13 节按实际入口补齐；最终复验见第 14 节 |
| A35 | 契约/权限/Web 边界 | IPC 认证和本地范围保持，Web 无原始本地诊断出口 | T14 | 已实现并验证 | control 认证详情与既有原生 IPC 测试；`TestGateway_LocalDiagnosticHistoryIsNotExposed` 对有/无浏览器认证均断言无本地诊断端点、无 controller 转发；第 12 节。 |
| A36 | 文档/帮助/旧规范 | F2/JSON/warnings/不脱敏策略一致，CHANGELOG 无修改 | T14 | 已实现并验证 | 第 12–13 节按实际入口补齐；最终复验见第 14 节 |

## 3. 各入口补充模板

```text
登记 ID / 具体入口：
原始原因产生位置：
转换/丢失点：
实际执行 owner：
记录 ID / operation ID 来源与传递：
响应/事件/本地结果：
CLI 文本 / JSON / TUI / 日志出口：
新旧能力与详情不可用时表现：
回归测试名与 Red 失败原因：
Green / 目标包 / 集成 / race 证据：
状态与未验证限制：
```

## 4. 最终验证记录

下表记录最终实现版本的验收结果；基础版本或中间版本的测试不等于最终验收。本表只在实际执行并核对对应版本后填写。

| 验证 | 状态 | 环境/命令/结果 |
| --- | --- | --- |
| 各任务最小 Red/Green | 通过 | 第 6–13 节记录具体行为及正确失败原因 |
| 受影响包测试 | 通过 | 最小目标包验证及最终全仓验证 |
| integration | 通过 | 全仓普通测试；最终 `go test -race ./internal/integration` 通过（55.911s） |
| go test ./... | 通过 | Windows，`ad6ecae` 上 `go test ./...` 通过；此前同基线覆盖率比较见第 14 节 |
| go test -race ./... | 通过 | Windows，`ad6ecae` 全仓复验 exit 0；未变化且已通过的包复用 Go 测试缓存 |
| go vet ./... | 通过 | 最终 rebase 版本，exit 0 |
| golangci-lint v2.12.2 | 通过 | 最终 rebase 及 fixture 修复后复验，0 issues |
| Unix layout security | 通过 | `python -m pytest scripts/test/test_unix_layout_security.py -q`：74 passed，4 skipped |
| gofmt / diff 检查 | 通过 | `gofmt -l cmd internal` 无输出；源码 diff 无空白错误；golden 保留终端单元格填充空格 |
| 受影响包与全仓覆盖率对比 | 通过 | 同基线 78.6640% → 78.8040%；逐包解释见第 14 节 |
| CGO0 windows amd64 / arm64 | 通过 | 两目标 `go build ./cmd/mihari`，产物在临时目录 |
| CGO0 linux amd64 / arm64 | 通过 | 两目标 `go build ./cmd/mihari`；未在本机执行 Unix 测试 |
| CGO0 darwin amd64 / arm64 | 通过 | 两目标 `go build ./cmd/mihari`；未在本机执行 Unix 测试 |
| 真实订阅/mihomo/服务 testenv | 不在授权范围 | 不作为默认验收前置 |

## 5. 设计阶段风险及收口依据

以下为实施前风险清单，处理及回归证据见第 6–14 节；持续失败加速有界历史淘汰是已接受的保留限制。

- Reporter 当前无 ID 回传，必须先建立 owner 发生记录再传给响应，不能按文本推断。
- 当前 warnings 多为 report 后丢弃；DTO 新增字段本身不足以恢复原因。
- 流终止只有 close 时客户端无法取得远端原因；新增协商终止事件不可误发给旧端。
- 日志级别过滤不能影响历史采集；不能从已过滤 logger 后面补接历史。
- 列表与详情之间可能淘汰；补取失败必须独立于业务操作结果。
- 窗口正文缓存、固定选中记录和列表元数据都需有界，不能仅限制 daemon ring。
- 持续后台失败会产生重复记录并加快淘汰，这是 Q12 接受的限制；不在实现时擅自加入智能归并。

## 6. 阶段实施记录（非最终验收）

工作树：`fix/197-cli-setup-diagnostics`，基线 `6bd40d1`，均为未提交修改。

- 修改前全仓 `go test -coverprofile=... ./...` 通过，总覆盖率 78.4%；覆盖文件保存在系统临时目录，不提交。
- T1–T3 基础：可选 DTO 原文往返、共享 formatter、字节/图/上游截断、有界内存历史和记录身份已加入。`internal/control/protocol`、`internal/diagnostics`、`internal/logging` 目标包通过；仍需完成全部 wrapper/来源审计。
- T4 执行 owner：`TestOperationDiagnostic_SnapshotSurvivesReplayWithoutAnotherOccurrence`、`TestOperationDiagnostic_MissingReporterStillReturnsOriginalDetail`、`TestOperationDiagnostic_CommittedWarningReturnsOnReplay` 均先因快照/警告缺失失败，再通过。`TestOwner_ExpectedFailureHasHistoryEvenAtDebug` 证明文件日志级别不抑制实际失败历史。
- T4 装配：`TestDaemonAssembly_DiagnosticHistorySharesRuntimeOccurrences` 和 `TestDaemonAssembly_EarlySettingsFailureRemainsInDegradedHistory` 先因未传历史失败，装配修复后通过。TUI 本地 owner 仍待接入。
- T4/T5 warnings：调用级 Result 接收器、operation cache warning 引用、强类型成功/失败响应、客户端补取已接通。`TestDiagnosticWarnings_CommittedResultAndReplayContainOriginalCause` 的 Red 是成功响应没有 warnings，修复后通过；已验证原 revision、状态及 mutation 执行次数不变。
- T5 引用：`TestDiagnosticReference_FetchesOriginalWithoutRepeatingMutation` 覆盖 available/expired/restarted/unavailable，先因未补取失败，修复后通过。旧 daemon 不提供详情时显示 unsupported，不增加业务请求。失败响应里的此前警告也有客户端回归。
- T5 预算：`TestDiagnosticResponse_InlineBudgetReferencesExcessWarnings` 先因超额正文仍内联失败，修复后通过，含最坏 JSON 控制字符转义。完整响应编码后总预算与极大业务载荷的处理仍未完成。
- T7 原 #197：`TestExecute_OriginalSetupDetails` 与 `TestExecute_TerminalEscapesControlsWhileJSONPreservesOriginal` 先因 cause 丢失/原始终端控制序列失败，修复后通过。JSON 保留原文，文本展示统一概要/code/details。`TestExecute_CommittedWarningKeepsSuccessfulExitAndOriginalDetail` 先因文本无警告失败，接入公共 mutation renderer 后通过。
- 已更新相应旧断言：仅本地错误/日志原文策略相关测试改为验证原文；分类、退出码、认证提示、单一 JSON envelope、实际执行 owner 次数等断言保留。warnings 切片使 DTO 不再可比较，原直接比较改为完整结构比较。
- 最近目标 race：`go test -race ./internal/diagnostics ./internal/runtime ./internal/control/client ./internal/control/server` 已通过（T7 展示改动前）。
- 第一轮 `go test ./...` 中 TUI/root 与 System 测试因 DTO 直接比较编译失败；修复后目标包通过。随后在 T7 公共 CLI 出口及成功 warnings 接入后重跑 `go test ./...`，当前阶段全仓通过（Windows，2026-09-16）。后续功能仍未完成，最终实现版本仍须重跑验收。

该轮尚未收口：完整响应总预算、流终止诊断、TUI F2/本地历史/session 同步、九页全接入、底层原因全审计、启动/清理 fallback 全面移除脱敏、完整文档与最终六轴验证。后续进度见第 7 节。尚未创建 PR，CI/bot 轮询尚未开始。

## 7. T6/T8/T9 与 Setup 阶段记录（非最终验收）

- T6 已接入协商终止引用和客户端只读补取。`TestStreamDiagnostics_TerminalReferenceRequiresOptIn`、`TestStreamTerminal_ResolvesReferenceWithoutDeliveringItAsData` 验证旧客户端关闭语义、引用不作为业务数据、256 KiB 原始正文通过单条查询传递。server 取消及终止写失败的完整边界审计仍待完成。
- T8 F2 已接入 root，保留输入与已有确认浮层；支持列表/详情、滚动、原文复制、复制失败和列表淘汰时固定选中快照。`TestDiagnosticsF2_*` 验证上述行为及晚到详情不覆盖新选择。窄屏列表分两行展示时间/严重程度/来源及概要，不让元数据挤掉整个概要。
- T8/T9 本地 Run 装配独立 128 条/16 MiB 内存历史及 owner，无文件 logger 仍可工作。F2 打开前和生命周期 tick 同步，按 ID 幂等。`TestDiagnosticsF2_SeesLocalOwnerWithoutFileLogger` 先因 F2 无记录失败，接线后通过。
- T9 session 按实例及序号读取有界元数据页，不预取正文；已有重连循环保留游标。`TestDiagnosticHistory_ResumesCursorAndPublishesRestart`、`TestDiagnosticHistory_QueryFailureDoesNotFailBusinessPoll` 先因缺少查询/事件失败，接线后通过。历史查询故障独立于业务连接状态。
- T9 实际 core/subscription/proxy/rules/provider/preferences/WebGUI/logging 读取失败以对应资源事件发布；独立读取继续，不再为未执行的 proxy 读取制造错误。root 保留最后成功快照并显示当前资源失败概要，恢复清除概要但保留历史。对应 `TestSession_ResourceFailurePreservesCauseAndContinuesReads`、`TestDiagnostics_ResourceFailureRetainsSnapshotAndShowsSummary` 均经历正确 Red 后通过。
- T9 `TestSession_StreamFailurePublishesOwnerOccurrence` 先因只写文件失败，修复后流错误事件携带 owner 的原记录 ID。`TestOwner_NormalCancellationDoesNotEnterHistory` 先因正常取消入历史失败，修复后正常取消不入历史；与取消合并的真实 cleanup 失败仍入历史。
- F2 已进入九页及输入/保存/浮层模式的帮助和 footer；`TestDiagnosticKey_DiscoverableInEveryPageAndInputMode` 先红后绿。原 session 诊断生命周期测试完整保留，新增历史测试使用独立文件。15 个现有 golden 每个只变更一行 footer，经 `go test -run '^TestGolden' ./internal/tui -update` 重新生成并普通测试复核。
- T10 Setup 起步：移除 safeText 脱敏和 4096 字符诊断裁剪；使用共享 formatter 展示分类、原文和已有建议。`TestSetupErrorDetails_PreservesOriginalCauseAndURL` 先因 URL/长原因丢失失败后通过。Load 的 core 原因和资源结果向 shell 暴露，`TestSetupReadFailure_ReachesShellWithoutMakingReadinessFalse` 先红后绿，业务就绪状态仍为未确认。
- Setup 上述修改后 `go test ./...` 已通过（Windows，2026-09-16；本阶段版本，非最终验收）。已通过 `go test ./internal/tui/... ./internal/control/client ./internal/control/server ./internal/diagnostics`；TUI root/session/diagnostics 的目标 race 已通过（随后追加列表展示和 Setup 修改，最终验收需涵盖最终版本）。`gofmt -l cmd internal` 无输出。
- 普通源码 `git diff --check -- . ':(exclude)*.golden'` 通过；完整 diff 检查仅报告部分 golden footer 的行末填充空格。golden 按终端单元格宽度逐字节比较，不能直接删除填充。此项须在最终交付明确记录。

仍未收口：T5 完整编码后响应预算；T6 全部终止边界；T8/T9 完整历史可用性、分页校验和所有资源/取消路径；T10 Setup 本地校验、端口探测、多个独立失败、warnings/部分成功及 settlement；其余页面逐入口接入；T13 底层原因和 fallback；T14 文档；T15 最终验证、PR 和 CI/bot review。


## 8. 页面传播、独立出口与采集边界（非最终验收）

以下均为 `fix/197-cli-setup-diagnostics` 未提交工作树上的阶段证据，不关闭整组入口。

- 页面结果现在保留独立读取失败和成功 warnings。Setup 的多个读取错误分别进入历史；WebGUI 六类 mutation、Connections close/close-all、Rules 与 Subscriptions 批量部分完成保留先前警告和实际 revision，不重放失败后的未执行项目。页面 `warning_result_test.go` 与 root `page_diagnostics_test.go` 覆盖实际命令结果。
- 日志自身 FailureReporter、TUI 日志初始化/关闭、daemon 启动及早期 cleanup fallback 已移除脱敏和路径隐藏，保留有界原文、独立出口和限流。相关 logging/tui/cmd 原有清理顺序、单次汇报和非递归断言保留；本轮 `go test ./internal/control/server ./internal/control/client ./internal/control/protocol ./internal/logging ./internal/tui/... ./cmd/mihari` 全部通过。
- T5 `TestDiagnosticResponse_EncodedBudgetKeepsBusinessAndReferencesDetail` 的 Red 为 3 MiB 业务数据加 256 KiB NUL 详情编码后达 4,719,018 字节。公共 writer 现在先检查完整 JSON 加换行的大小，超限将可查询详情改为引用；业务字段、revision 和 owner 保存的原文保持。该回归已 Green。**业务本身贴近 4 MiB、连引用元数据也放不下的边界仍待完成，不据此关闭 T5。**
- 请求 JSON 解码失败改为返回 rejection owner 的同一个快照。`TestDecodeControlJSON_ReturnsOriginalCauseAndOwnerIdentity` 覆盖未知字段及尾随非法 JSON，原始 decoder 原因和合成 token 保留；HTTP 400/invalid_argument 不变。Red 为响应没有 diagnostic，修复后通过。
- `TestStreamDiagnostics_NormalCancellationDoesNotCreateOccurrence` Red 为正常取消进入历史，修复后正常取消无发生记录；合并的真实 cleanup 原因仍保留。文件日志已有 INFO 取消策略不因此改变。
- Proxies 选择、routing、LoggingObservedMsg 的 committed warning 已到达共享窗口。`TestProxySelection_PreservesCommittedWarning`、`TestRoutingResult_PreservesCommittedWarning`、`TestDiagnostics_LoggingWarningKeepsOriginAfterPageSwitch` 均先红后绿，不改变成功分类。
- `TestWebGUI_ErrorDisplayKeepsRawCredentialsAndEscapesControls` 证明错误含 token/open_url 时不再把整页替换成 unavailable；错误原文中的终端控制序列转义显示。普通 WebGUI OpenURL 仍不作为页面业务字段展示。
- `TestPageReads_OriginalFailuresReachGlobalHistory` 已强化为真正的命令结果在用户切到 Overview 后到达，不再由测试人工补 PageResult 包装。Setup/System/WebGUI/Rules/Subscriptions 的五个子测试先因归属错误失败；结果增加明确的 DiagnosticPage 合同后通过。Connections/Proxies 的异步结果同样保留来源。
- T13 HTTP 采集：core、update、mihomo、GeoIP、panel/release、subscription 与 Web HTTP 观察器现在设置显式 BodyTruncated。现有大正文/关闭资源回归增加结构化标记断言后先红，再最小接线 Green。`TestGitHubDiagnostics_CollectionLimitIsStructured` 同时证明字面量 `[truncated]` 不被解释成截断事实。mihomo 流 JSON 解码正文使用同一边界标记。
- T12 `TestSystemLocalFailures_PreserveCauseForGlobalDetails` 覆盖 clipboard/browser/channel path/channel read，Red 为同步分支丢弃原因。现在返回 ui.DiagnosticMsg，保留原始 error 与已有概要，失败前置条件不启动 update check；无 file logger 也保留快照。相关 System 旧测试改为允许诊断消息，仍断言未启动更新和原行状态。
- `TestModePicker_CancelConfirmationDoesNotReportFailureOrLoseStatus` 的 Red 为取消未执行操作制造错误并使已知 routing 状态失效。结果现在显式标记 cancelled，清除 pending、保留确认前状态且不报告错误。

### 本阶段验证

- `go test ./...` 全部通过，包含 HTTP 截断标记和首批页面 warning/WebGUI 修复；随后增加的页面归属、System 本地错误及 routing 取消修复已另跑目标测试。
- `go test -race ./internal/control/server ./internal/control/client ./internal/diagnostics ./internal/tui/...` 通过；最后两项 System/Proxies 修复后，又执行 `go test -race ./internal/tui ./internal/tui/pages/proxies ./internal/tui/pages/system`，全部通过。
- `gofmt -l cmd internal` 无输出；`git diff --check -- . ':(exclude)*.golden'` 通过。golden 填充空格的已有说明见第 7 节。
- 当前已知 `origin/dev` 相比本分支基线新增 `a8a0a6c`（#260，订阅刷新超时/代理回退），提交 PR 前须核对重叠与兼容性；未为此覆盖主工作目录或已有修改。

仍需完成：T5 极限业务载荷；历史分页/元数据验证；终止帧写失败完整审计；Setup 校验/端口/settlement/取消，以及各页面剩余直接提示/部分成功/后台入口；全量 cause 与 owner 审计；文档策略统一和最终全仓/race/vet/lint/覆盖率/六轴构建。尚未 commit/push/创建 PR，CI/bot 轮询尚未开始。


## 9. 新基线整合与诊断读取验证（非最终验收）

- 工作分支已从 `6bd40d1` 快进到 `964904e`，接入 #260 订阅超时和 #262 统一日志级别。恢复任务修改时解决 LoggingStatus、logging 测试、operation diagnostics 集成断言、Subscriptions refresh-all 四处冲突：新增 CoreLevel/SyncState/SyncMessage、逐项超时与 cancellation operation ID 均保留，同时保留 warnings。
- 恢复前快照为 `a18c818f21c04673275d83ed2e34545f1aeb7c66`（stash，未删除）；49 个未跟踪任务文件全部恢复，冲突已清零，所有修改仍未提交。主工作目录没有改动。本快照只用于恢复工作，不属于交付 commit。
- `TestDiagnosticHistory_RejectsInvalidMetadataWithoutReportingRecursively` 的九类 malformed page 均先因被接受而失败；客户端现在拒绝未知状态、错误实例/ID、重复或超前记录、不推进的游标、空 continuation、列表正文和超长概要，并不返回污染记录、不递归 report。`TestDiagnosticHistory_ValidPagesMatchServerCursorSemantics` 使用实际 History.List 验证首次查询、淘汰、继续、尾页与重启均可读取。
- `TestStreamTerminal_MissingReferenceKeepsActualTransportCause` 先因缺少远端详情状态说明失败。现在意外关闭但未收到 terminal reference 时，记录真实本地 websocket cause 并明确远端详情未收到；保留既有 daemon_unavailable/code/message，nil logger 也可获取快照，不猜测远端 ID、不重放流。
- 新基线上 `go test ./internal/control/... ./internal/runtime ./internal/integration ./internal/tui/... ./cmd/mihari` 全部通过。
- 新基线上上述流修复后 `go test -race ./internal/control/client ./internal/control/server ./internal/tui/session ./internal/runtime` 全部通过。
- `gofmt -l cmd internal` 无输出，普通源码 diff whitespace 检查通过。完整全仓最终验证、六轴构建、覆盖率、PR 和 CI/bot 仍待剩余实现收口后执行。

后续优先：完成 T5 极限业务响应的诊断引用传输；完成页面直接校验、Setup settlement/主动取消、System 初始化通道读取和 prepared task/cleanup 的实际 cause 传播；新增 logging sync owner 纳入最终范围审计。第 8 节列出的其余未完成事项保持有效。


## 10. 极限响应、流写失败与页面本地诊断（非最终验收）

- T5 极限预算：`TestDiagnosticBudgetIPC_ExactBusinessLimitPreservesCommittedWarning` 先因 4 MiB 成功业务响应附带诊断后超限失败；`TestDiagnosticResponse_ExactErrorBudgetPreservesClassification` 先因失败 envelope 超限失败。现在必要时通过有界引用响应头传送元数据；真实 IPC 验证保存仅执行一次、revision/业务字段保持，查询取回完整已采集原文。错误 HTTP 状态、code/message/details 同样不变。
- `TestDiagnosticReferences_WorstCaseMetadataFitsHeaderBudget` 验证 64 条最大元数据加错误引用的最坏转义和 base64 预算；编码/解码拒绝正文和无效引用。客户端测试覆盖 available/expired/unavailable，只查询、不重放 mutation；损坏的 header 不改变成功结果。
- `TestDiagnosticResponse_InvalidSupplementDoesNotInvalidateCommit` 两个 Red 分别为缺少引用 ID 和过长概要导致 writer 丢弃已提交响应。现在传回明确 unavailable 及 warning 遗漏数量，保持业务正文和原采集对象。`TestWarningOutcome_AppendBoundsPreexistingWarnings` 先因已有超长列表逃过上限失败，修复后始终保留最多 64 条，并计入全部遗漏。
- T6 `TestStreamDiagnostics_TerminalWriteFailureKeepsBothOccurrences` 使用真实 websocket 握手与注入的 net.Conn 写失败，证明原始 upstream 错误和终止帧实际 transport 错误各记录一次；通过 handler 完成事件确认结束，不依赖 sleep。
- T10 `TestSetupValidation_PublishesGlobalDiagnostic` 两个 Red 为端口/订阅输入仅设置短文案，修复后返回全局 DiagnosticMsg。`TestSetupPortProbe_RetainsOriginalFailure` Red 为原绑定原因丢失，现在保留 bind/close 原因及原可用性分类；已确认属于当前进程的监听不产生错误记录。
- `TestSetupSavedSubscription_PublishesPartialFailure` Red 为保存成功但首次拉取失败没有全局记录。现在保留已保存订阅和 revision，并展示 warning；已有 warning 结果不另造同一 fallback。`TestSetupSettlement_PreservesReadbackWarnings` Red 为 settlement 丢弃读取 warnings，接入共同结果合同后通过。
- `TestSetupCancellation_OnlySuppressesOwnedCancellation` Red 为主动取消使用根 context 判断而变成错误；action/endpoints/completion 结果现在携带原操作确认的取消标志，保留原 Err 供 settlement 使用，真实 cleanup join 仍进入历史。System `TestPreparationCancellation_RetainsCleanupFailureWithoutInventingCancelError` 同样先红后绿，使用准备任务自己的 context。
- T12 `TestSystemChannelDiscoveryFailure_ReachesShellWithoutLogger` 对 discovery/path/read 三个入口先红；lazy 通道发现现在保留一个待发送 DiagnosticMsg，经下一次 Update 送至 shell，无 file logger 也可显示，缓存失败后的重绘不重复记录。

阶段验证：`go test ./internal/control/... ./internal/tui/... ./internal/integration` 通过；随后流终止写失败测试通过。`go test -race ./internal/control/protocol ./internal/control/server ./internal/control/client ./internal/tui/pages/setup ./internal/tui/pages/system ./internal/tui` 通过（System preparation 取消修复之前）；preparation 修复后 System 全包测试通过。最终验证仍须覆盖最终工作树。

仍未收口：各页剩余实际入口及 CLI/后台 cause/owner 全范围核查、prepared cleanup/导出等本地任务、旧端与全部取消路径、T14 当前文档策略更新、T15 全仓/race/vet/覆盖率/六轴构建。开发完成后按最新要求先 rebase 最新 dev，再提交 PR，随后每 10 分钟检查 CI/bot。尚未提交或创建 PR。


## 11. 导出与本地安装结果（非最终验收）

- `TestExportOutcome_OriginalDetailsWithoutFileLogger` 五个模式先因结果无 shell 合同失败；导出错误、恢复的 panic/原堆栈、成功 warnings、主动取消及取消后的 cleanup 现经共同合同传播。70 次 warning 保留 64 条及明确遗漏 6 条，同文本再次发生不合并。原有文件日志测试更新为逐次 warning 及独立最终失败；panic 的公开错误文案仍保持，原始内容进入诊断。新增 typed-nil 分类保护，资源释放、结果通道关闭及 worker join 断言保留。
- `TestExportLocalFailures_ReachGlobalDetails` 的 clipboard/time 两个 Red 均为丢失返回原因；现在路径复制和时间校验返回全局诊断，保留已导出的文件路径、输入与未启动导出的约束。时间解析不再丢弃原 ParseError，机器源不可用和反向时间范围也保留诊断。旧测试只调整允许诊断命令，仍证明没有启动不合法导出。
- `TestDiagnosticsF2_ExportResultsReachDetailsThroughActiveModal` 经真实导出命令结果和 root Update 验证 error/warning 都在弹窗消费前入历史，F2 可读原文，退出 F2 后导出弹窗保持。
- `TestDiagnostics_LocalInstallationAndCleanupKeepOrigin` 先因 installation status 结果无 Err 合同失败；安装检查、预览失败及 prepared cleanup 结果现保留 System 来源。安装预览按自己 context 识别取消，取消伴随的实际 cleanup 继续传播。
- T14 已更新 AGENTS.md、README、docs/commands.md 与 docs/architecture.md 的当前不脱敏、本地诊断、warning 成功语义与 F2 政策。历史审计段落保留并标明策略由 #197 替代；交付前仍需复核全部陈旧说明。

验证：导出变更后 `go test ./...` 全部通过；`go test -race ./internal/tui/ui ./internal/tui/pages/system ./internal/tui` 通过。随后安装结果合同修改的 TUI 全包测试通过，追加 `TestDiagnostics_InstallationPlanCancellationKeepsCleanupCause` 后 `go test -race ./internal/tui -count=1` 通过。所有当前源码 gofmt/whitespace 检查在上一个检查点通过，提交前继续复核最终 diff。

下一轮仍须核查：导出 default-path preview 错误、导出 UI 对非主动 deadline 的说明、installation status/plan 验证失败原因为何丢弃、其余页面/CLI/后台实际入口与 owner；完成范围登记后再执行最终验收。此轮未提交、未创建 PR，没有开始 CI/bot 轮询。

## 12. 本地出口补齐、基础验收与检查点（非最终交付）

以下证据对应 `964904e` 上的未提交工作树。主目录 `.gitignore` 未触碰；尚未 rebase 最终 dev、commit/push 或创建 PR。基础行按实际证据关闭，其他入口仍逐项验收，不把基础完成换算为整个功能完成。

### 新增回归与修复

- 详情响应的超长 summary/object/instance、非法 severity 及 expired 附带正文均先 Red；`TestDiagnosticDetail_RejectsMalformedMetadataWithoutPublishing` 修复后 Green，拒绝污染结果且不递归 report。
- Export 的 default-path preview 原因、主动取消与 upstream deadline 区分、安装 status/plan 校验、Subscriptions 本地表单和保存后 URL readback、剪贴板读取/旧详情复制失败均先补缺口回归再修复。已保存订阅首次下载失败通过原执行 owner 的同一记录返回 warning；runtime 重放测试与真实 IPC → CLI 文本/JSON 测试证明成功退出、原文保留及只下载一次。
- System 参数解析及端口探测保留原始原因；已确认本实例监听的占用不伪报失败。卸载不可用可进入统一详情；行提示和卸载预览转义终端控制字符。`TestSystemPortProbe_ReportsOriginalFailure`、`TestSystemLocalRejection_IsInspectable`、`TestSystemErrorSummary_EscapesTerminalControls` 均 Red → Green。
- `TestDiagnostics_HistoryQueryFailureDoesNotEnterHistory` 先 Red：查询失败被重新插入本地历史。现仅占一份有界临时详情，在 F2 内可滚动/复制，查询恢复后清除，不污染 daemon 或本地历史。安装权限/能力拒绝也经 `TestDiagnostics_InstallationRejectionIsInspectable` Red → Green。
- `TestBuildExportLogs_DialogPreservesOutcomeAndOriginalWarning` 使用真实被取消的操作 context；另测未取消操作的 upstream cancellation。原始 warning 保留、业务结果说明正确。旧 fixture 仅返回 Canceled 而未取消操作，因此按新约定应视为实际失败。
- `TestManagerBackgroundDiagnostic_HistoryKeepsActualSchedulerFailure` 使用 Manager.Run 与真实内存 owner，证明无文件 logger 时 scheduler 原始错误可查、无虚构 operation ID。九页 F2 当前动作优先及 Ctrl+C、Web gateway 不开放本地历史均增加直接回归。
- CLI purge 的资源关闭与执行分支先丢弃 cause，进度 writer 失败被忽略；`TestExecute_Purge*` Red → Green，保持原退出分类、只执行一次且关闭失败不执行卸载。`TestExecute_SelfReplacementWarningDoesNotDiscardFailureCause` Red → Green，JSON 兼容性提示不再重建 APIError 而丢失 apply 原因。
- WarningOutcome 增补独立测试：clone/append 不共享可变快照；缓存引用保留无查询 ID 的唯一正文；超限遗漏计数正确。诊断 header 覆盖坏编码、坏元数据、编码失败无半成品，以及 unavailable fallback 不捏造 ID。
- 修复本轮 race 实际发现的 golden 测试全局时区竞态：测试进程启动时固定时区，不再逐 case 修改 time.Local。原 golden 内容未为此改写。
- README 中英文、CONTRIBUTING 中英文、commands、当前 architecture 与历史设计策略指向已同步。订阅业务字段的 URL 省略与错误诊断保留原文明确区分。

### 实际检查结果

- `go test -race ./...` 全部通过；subscription 用时 372.686 秒，正常结束，不是超时或被终止。结果留于系统临时目录 `mihari-197-race-final.log`，该文件名不代表整个任务最终完成。
- 此后修改了查询临时详情、安装拒绝和 lint 相关代码，再运行 `go test -race ./internal/control/server ./internal/control/client ./internal/runtime ./internal/tui/...` 全部通过。随后新增的 CLI purge/self 修改和 protocol/Web 测试仍须最终复验。
- `go test ./internal/diagnostics ./internal/control/... ./internal/runtime ./internal/tui/... ./cmd/mihari` 全部通过；新增 CLI/Web/TUI 目标回归通过。
- 六个 `CGO_ENABLED=0` 构建通过：Windows/Linux/macOS × amd64/arm64。产物仅在系统临时目录 `mihari-197-builds`，后续 CLI 修改须复验。
- `go test -coverprofile=... ./...` 在任务工作树与 `964904e` 的临时只读基线副本均通过。检查点总覆盖率 78.52%（28510/36309），基线 78.55%（27535/35053）。该检查点早于本轮末尾的 CLI、protocol 回归；protocol 单包随后由 72.0% 提升至 88.5%，需重算最终全仓结果。
- `golangci-lint run ./...` 为 0 issues；`go vet ./...`、`gofmt -l cmd internal`、源码 `git diff --check` 通过。golden 的布局填充空格沿用既有检查说明。
- `python -m pytest scripts/test/test_unix_layout_security.py -q`：74 passed、4 skipped。未执行真实账户/挂载/系统服务操作。

### 尚未完成

- 继续审查 A03、A06、A09–A10、A13–A34 的剩余实际入口，尤其是本地 CLI 成功 warning 的 JSON 出口、后台各实际 owner、Overview/Logs 状态来源和跨模块固定文案转换。登记每行以完整范围为准，不把部分回归覆盖当作整行完成。
- A36 当前文档已更新，最终帮助/文档一致性仍随最终实现复核；完成跨层整体验收和覆盖率解释后，在最终代码上运行必要检查。
- 开发完成后 rebase 最新 dev，复验，commit/push 并创建 dev PR；之后按 10 分钟间隔检查和处理 CI/bot review，全部可用检查通过后汇报。不自行合并。


## 13. 剩余出口核查与跨层验收（rebase 前）

### 本轮新增证据

- CLI self-update 与 Unix service apply 的成功 JSON 现在将兼容性 warning 放进可选 `warnings`，不向 stderr 混入文本，退出码和成功业务字段保持。`TestExecute_SelfReplacementSuccessKeepsWarningInJSON`、`TestServiceApply_JSONSuccessContainsStructuredWarning` 先 Red 后 Green。InstallResult 严格解码接受该已批准的可选字段，继续拒绝未知字段；`TestInstallResult_OptionalWarningsPreserveStrictRoundTrip` 同时覆盖旧无 warning 文档。
- Logs 序列化错误原先在 View 中被吞掉，现在在打开详情时汇报一次并保留原日志。原控制字符不再删除，终端转义显示，Raw JSON 由未修改的记录编码。`TestLogDetail_SerializationFailureIsInspectable`、`TestLogDetail_OriginalControlsAreEscapedWithoutRemoval` Red → Green；容量淘汰继续使用原有 Dropped 计数，不把有意有界保留制造成新的失败发生。
- 安装请求/结果/状态/journal 原先将实际 reader、JSON parser、文件错误换成固定断言；现保持原 code/message 与严格校验，cause 单独包装。`TestInstallDecode_PreservesOriginalCause`、`TestReadInstallRequestFile_PreservesFilesystemCause`、`TestInstallationCodecs_PreserveReadAndSyntaxCauses` Red → Green。安装事务验证和服务适配的相同转换点作了 cause 透传，未改变事务、权限、持久化结构或系统操作。
- CLI status 无分类的 transport 错误和 panel open 浏览器失败原先丢弃原因，`TestStatus_TransportCauseReachesDetails`、`TestPanelOpen_OriginalBrowserFailureReachesDetails` Red → Green。self 路径解析也沿同一方式保留原因。
- Unix 服务 runner 的实际错误、非零 exit 的 stdout/stderr 进入原始详情；公开 code/message/details 的原有语义保留。`TestServiceCommand_PreservesOriginalCause`、`TestServiceCommand_NonzeroExitPreservesOutput` Red → Green。systemd/launchd/proc/XML 解析与文件读取的既有错误也保留 cause，无实际错误的验证断言不虚构底层 cause。
- Overview 配置/订阅 LastError 改为终端转义。`TestOverview_ConfigErrorEscapesControls` Red → Green。概要状态不反推 occurrence；F2 使用已有执行 ID、页面来源和显式来源标签，不声称无 ID 的旧 LastError 与全局最新记录相同。
- 配置生成 clone/final/TUN mapping 的 YAML 原因原先丢失；`TestGenerate_PreservesOriginalEncodingCause` 四个断言 Red → Green。面板 URL、TUN 提交前 cache 读取、checksum hex 解析及 CLI ID 读取的分类转换也保留实际 cause；不额外转储输入文件。
- `TestDiagnosticIPC_UpstreamFailureReachesF2AndOriginalCopy` 经 httptest mihomo HTTP 503 → 真实本地 named pipe/Unix socket → control server/history → typed client → 实际 Rules reload 命令 → root F2 → 复制，验证合成 token、控制字符和配置片段原文，且 upstream 只请求一次、daemon history 与页面 occurrence ID 相同。测试自身关闭 IPC、HTTP 和空闲连接，不使用真实用户配置或服务。
- 全仓复验发现旧 `TestReplacementConfirmation_CLIUsesVerifiedCandidate` 仍期待成功 JSON 的 warning 在 stderr。按新合同改为解码 stdout 的 WarningOutcome，并保留固定候选、只替换一次、风险文案、拒绝不写入、candidate 清理等断言；目标集成测试通过。

### 实际入口与共用出口核对

下表记录剩余分组的生产转换点。每个返回结果由 root 在页面消费前捕获 Err/DiagnosticErrors/Warnings；页面归属随异步结果携带，切页不改变归属。CLI 失败最终走 Execute，共用原始详情和稳定退出分类；runtime mutation 最终失败由 doOperation owner 记录一次，成功 warning 由 Result 收集并在重放时保留原 ID。

| 登记 | 生产入口 / owner / 客户端出口 | 行为证据 |
| --- | --- | --- |
| A03、A09 | CLI Execute、SetupError/PrepareLocalRoot；F2 与旧详情复制；Logs、Overview、System、WebGUI 的错误显示 | CLI `TestExecute_OriginalSetupDetails`、`TestExecute_TerminalEscapesControlsWhileJSONPreservesOriginal`；本节 Logs/Overview；root F2 copy/eviction 与旧详情复制失败回归 |
| A06 | settings commit、core/GeoIP/subscription/panel/routing/sysproxy/TUN/preferences 成功 DTO、批量结果 | runtime `TestOperationDiagnostic_CommittedWarningReturnsOnReplay`；真实 IPC `TestDiagnosticWarnings_*`；各页 `warning_result_test.go`；本节 self/apply JSON 回归 |
| A10 | main 早期 settings/degraded、daemon listener/ready/runtime、服务入口、shutdown；进程 owner 共用 history，独立 fallback | `TestDaemonAssembly_*`、`TestDaemonStartupFallback_PreservesOriginalCause`、`TestRunDaemonWith_CloseFailureUsesIndependentOriginalOutlet`；daemon、service/launchd/install-validation 既有入口测试 |
| A13、A14 | Setup onboarding/core/sub/GeoIP 独立读取、端口探测/保存、core 安装、订阅首次下载、完成、settlement、取消 | `TestPageReads_OriginalFailuresReachGlobalHistory`；Setup local_diagnostics/warning_result/experience；第 7–12 节 Red/Green |
| A15、A16 | sub 所有已装配 CLI；Subscriptions 表单、URL/readback、启停/proxy-mode/刷新/切换/删除；首次下载和 scheduler | 表单与 URL readback 回归、`TestBulkRefresh_PreservesWarningBeforeLaterFailure`、`TestSubscriptionDiagnostic_FirstFetchFailureReturnsWarningWithoutReplay`、真实 IPC → CLI first-fetch 与 scheduler diagnostics 测试 |
| A17 | session Status/Core/Subscriptions 等独立读取 → Overview；degraded history；最近操作保留显式 ID | `TestDiagnostics_ResourceFailureRetainsSnapshotAndShowsSummary`、`TestDaemonAssembly_EarlySettingsFailureRemainsInDegradedHistory`、九页当前动作优先回归；不从状态字符串猜测关联 |
| A18 | CLI core status/install/update/restart；System load/core channel/install/update/restart；supervisor 后台 owner | CLI runtime_test 的 core 命令上下文；System core action/load/result 合同及既有成功/失败/revision 测试；supervisor diagnostics 的 start/exit/health/termination/restart owner 测试 |
| A19–A21 | Proxies group/provider/routing/GLOBAL/单个批量 delay；Connections 流/close/close-all/preferences/GeoIP；Rules list/provider/bulk | 各页 result_diagnostics 合同、真实页面读取和 warning/bulk 部分成功回归；T14 实际 Rules HTTP → IPC → F2/copy；未执行的批量项不造失败 |
| A22 | logs/traffic CLI、session 四类流、日志 buffer/details | opt-in terminal stream、解码及真实 transport 原因回归；`TestBuffer_OverflowReportsDroppedEntriesAndKeepsNewest`；本节序列化与转义回归 |
| A23 | WebGUI/panel status/install/update/use/rollback/uninstall/reinstall/open/reload | `TestWebGUIActions_CommittedWarningsReachF2`、页面读取/原文显示回归、CLI browser cause 回归；普通业务 OpenURL 仍不默认展示 |
| A24、A25 | System proxy/TUN status/action/force、ports/logging/preferences、clipboard/browser/channel discovery | System result_diagnostics，既有 force/revision/rollback 用例；`TestSystemLocalFailures_*`、`TestSystemValidation_*`、port probe 与 channel discovery 回归；CLI sysproxy/tun 稳定分类测试 |
| A26–A28 | service status/action/purge；安装 status/plan/prepare/execute；Unix apply/request/native validation | service 和 app 本节 cause 回归、CLI purge 单次运行/关闭前置、installation prompt/worker/cancellation/cleanup 和 strict codecs；未装配 install-* CLI 不借此激活 |
| A29、A30 | self version/channel/prepare/apply/discard；服务副本同步/版本验证；退出后 cleanup/relaunch | Self CLI 原因与成功 warning；System preparation 取消及 cleanup；app self completion 保留 sync/status/wait 原因；prepared_update/finishRun errors.Join 保留已更新事实与清理/重启原始失败 |
| A31 | Export preview/time/execute/panic/warning/cancel/cleanup/copy | `TestExportOutcome_*`、`TestExportLocalFailures_*`、`TestExportPreviewFailure_*`；实际 modal → root F2 和 cmd export 回归 |
| A32 | logger open/write/rotate/close + TUI bootstrap/close + 独立 FailureReporter | `TestRotatingWriter_FailuresKeepCountsAndOriginalFallback`、`TestFailureReporter_PreservesCausePathsAndCredentials`、output nonrecursive、runtime close joins 与 TUI shutdown diagnostics |
| A33、A34 | config/state/platform 错误包装，HTTP 失败正文/截断，安装 codec/service/YAML 转换；scheduler/supervisor/health owner | 原 HTTP producer 回归；本节 cause regression；`TestManagerBackgroundDiagnostic_HistoryKeepsActualSchedulerFailure`；app 装配将同一 reporter 注入 core/controller/GeoIP/panel/subscription/supervisor/schedulers |
| A36 | AGENTS、README 中英文、CONTRIBUTING 中英文、commands、architecture、历史策略指向；F2 footer/help | 当前政策已同步；最终 rebase 后再检查新增模式与 docs。CHANGELOG 不在 diff 内 |

本轮实现和源码核对已完成，最终状态须由 rebase 后全仓验证收口。平台真实服务/testenv 不运行；跨平台仅编译和仓库 fake 测试。所有先前检查点仍保留，不冒充本轮最终结果。


## 14. 最终 dev 整合与验证

- 已执行 `git rebase origin/dev`，工作分支基线为 `03d33a1`（#261）。恢复快照 `e5c5a87b1921c50d8b3f504c0930e7c47c4fba31` 保留，先前快照同样未删除；主 worktree 的 `.gitignore` 修改未动。
- README、WebGUI View 移动、Connections golden 和快捷键期望四处冲突已解决。保留 #261 布局和菜单，将不脱敏政策移入新 render.go；golden 仅新增 F2 提示。`TestPageOverlays_FitInsideShell` 增补在新 WebGUI Manage/Rules 详情里 F2 → Esc 后保持原模式，所有 TUI 包已通过。
- 最终基线上 lint 0 issues、`go vet ./...`、`gofmt -l cmd internal`、源码 diff whitespace 检查通过；Unix layout fake/security 测试 74 passed、4 skipped。
- 最终基线上六轴 `CGO_ENABLED=0` 编译通过：windows/linux/darwin × amd64/arm64；产物仍仅在系统临时目录。
- 全仓普通测试（含覆盖率）、race、同基线覆盖率比较均已完成。最终 race exit 0；PR 与远端 CI/bot review 在本地验收后推进。


### 最终覆盖率与验证中发现的测试竞态

- Windows 同工具链、同基线 `03d33a1` 的全仓覆盖率测试均通过：基线 27733/35255 = **78.6640%**，本次 28821/36573 = **78.8040%**，提升 0.1400 个百分点。profile 保存在系统临时目录。
- 相关包比例小幅下降并不来自删除行为断言：CLI 980/1218 → 1009/1259，client 753/880 → 864/1012，Setup 759/949 → 789/994，Subscriptions 846/960 → 876/1000，System 1262/1456 → 1307/1520，session 230/267 → 251/294；新增诊断传输、nil/取消/兼容及故障 fallback 分支扩大分母，实际覆盖语句均增加。旧业务、事务、退出码和生命周期测试保留，并新增对应原文/owner/部分成功回归。
- formatter 从 logging 移至 diagnostics 导致 logging 分子分母同时减少（1552/1781 → 1447/1670）；跨包 formatter 的 typed-nil/循环/stack/截断断言保留。diagnostics 从 62.84% 提升至 72.02%，protocol 从 83.45% 提升至 88.53%。cmd 的重复 fallback 收口后为 331/496（基线 343/513），新增 degraded/bootstrap/history 测试证明原行为出口未退化。其余页面下降均小于 1，详情参见临时完整 profile。
- 首次最终 race 发现 `TestCredentialRefresh_LongLivedSessionRecoversAfterStopDeleteStart` 的 Windows fixture 删除临时凭据时，与仍运行的读者冲突。仅为 fixture 读写/删除加读写锁，不修改生产 credential 或 session；仍观察 missing token 和重建后重连、四条 stream 恢复、无额外目录写入。该测试 `-race -count=20` 通过；随后完整集成 race（55.911s）和全仓 `go test -race ./...` 复验均通过。

## 15. 再次整合 #263

- 用户提示近期 merge 后再次 fetch，发现 `ad6ecae`（Conns 单页详情）。在保留完整快照 `eeff5aab85418e1ac180d8821a51c88e9f9ed113` 后再次 `git rebase origin/dev`；当前基线更新为 `ad6ecae`。
- 三处冲突为 golden 测试、Connections 主快照、快捷键期望。保留上游单页布局、滚动及全部新变体，将 F2 提示加入五个快照（每个仅一行变化）。保留进程级 UTC 测试初始化，避免恢复旧的全局时区测试竞态。
- 五种 Connections 详情变体额外验证 F2 → Esc 后完整视图不变，覆盖紧凑、已关闭、暂停、滚动到底部。README 和页面动作自动合并已核对，warnings 传播保持。
- 受影响的 Connections/Logs/UI 目标测试及全部 TUI race 通过；lint 0 issues、vet、gofmt 与六轴 CGO0 编译复验通过。第 14 节覆盖率比较基线保持为 `03d33a1`，不冒充本次新基线数据；#263 仅整合详情展示及相关测试，没有修改诊断采集或协议。
- `ad6ecae` 最终全仓 `go test ./...` 与 `go test -race ./...` 均 exit 0（集成普通测试 27.175s，race 57.178s）。本地验收完成；远端 CI/bot review 随 PR 继续。

## 16. PR 自查补充

- PR #264 等待远端检查期间发现 daemon degraded LastError 的 shell footer 仍直接使用原文。`TestShellFooter_DegradedErrorEscapesControlsWithoutChangingStatus` 先因 ESC/NUL/换行未转义失败，再通过既有单行诊断显示函数修复；原始状态文本保持不变。
- 修复后目标回归、全部 TUI race、lint（0 issues）、TUI vet 和 diff 检查通过。变更仅为显示边界的一行调用及对应回归；等待最新 PR CI/bot review。

## 17. 首轮 CI 修正

- Linux/macOS unit 与 Linux coverage 均在同一新增测试 `TestReadInstallRequestFile_PreservesFilesystemCause` 失败：测试假设底层一定是 `*os.PathError`，但 Unix `readHostFile` 使用 `unix.Open`，实际返回 `syscall.Errno`。生产路径已经保留原始 cause。
- 测试改为验证 `errors.Is(os.ErrNotExist)`、原生文件读取错误文本进入 Capture、外层仍为 InvalidArgument；保留三项实际行为断言，不增加平台跳过。Windows 下相关三组错误传播回归 `-race -count=20` 通过；app lint 通过。Unix 由后续 CI 复验。
- Cubic 因月度额度耗尽（91,860/80,000 行）返回 neutral；CodeRabbit 因标签配置跳过，均不计作审查通过。Pullfrog 仍在审查，最新提交结果持续核对。
