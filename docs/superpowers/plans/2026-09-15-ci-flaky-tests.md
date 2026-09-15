# CI 偶发失败修复方案

日期：2026-09-15。
状态：用户已授权执行、提交、推送、创建 PR，并根据 CI/bot review 反馈持续修复至全绿。实现与验证记录见执行计划。

后续实施以[执行计划](2026-09-15-ci-flaky-tests-execution.md)中的任务顺序、测试矩阵和验收条件为准；本文保留调查证据与设计依据。

## 工作位置与范围

- Worktree：`C:/Users/Kinema/Documents/modular_dev/mihari/.worktrees/fix-ci-flaky-tests`
- 分支：`fix/ci-flaky-tests`
- 基线：`origin/dev @ d132db2b2f82a245fd52c18182f741f662a6b5f0`
- 后续 PR 目标：`dev`。
- 执行基线已 fast-forward 到 `origin/dev @ f365631`，保留原计划与用户工作区。
- 授权包括实现、提交、推送、PR 和反馈修复；合并仍需用户确认。

修复范围为有失败证据的测试，以及确定性回归证明必要的最小生产修复。沿用 Go/toolchain、依赖、协议、持久化格式、平台范围和诊断策略。保留 race 的 30 分钟上限，不引入失败重试、跳过测试或 continue-on-error。

已阅读 CONTRIBUTING、README、目标实现与测试。AGENTS 引用的 `2026-08-03-mihari-architecture-design.md` 在当前 dev 与原工作区中均不存在；本方案不重新设计架构边界，遵循用户提供的架构约束。若后续必须扩大边界，需另行说明。

## 证据与可信度

| 问题 | 证据 | 当前结论 |
| --- | --- | --- |
| macOS race 包级超时 | [34360227412](https://github.com/mihari-proxy/mihari/actions/runs/34360227412)：subscription 600.047s，panic after 10m | 历史问题；PR #222 已设置 `-timeout=30m`。不是近期失败的共同原因 |
| WebSocket 关闭断言不稳定 | [34922177821](https://github.com/mihari-proxy/mihari/actions/runs/34922177821)：预期 ERROR，实际 INFO/context canceled | 最新 dev 本地 coverage 重复 1000 次，3 次同类失败；已复现 |
| 快照测试 1 秒预算 | [34573116252](https://github.com/mihari-proxy/mihari/actions/runs/34573116252)：1.08s 后导出失败 | 测试同时使用 50ms 客户端超时、200ms sleep、1s 快照预算，不能仅凭报错断言“复用了 HTTP Timeout” |
| 导出取消耗时断言 | [34339955098](https://github.com/mihari-proxy/mihari/actions/runs/34339955098)：2.3139s | 计时从导出前开始，包含实际取消前的快照、读取和归档准备；断言未隔离取消延迟 |
| Windows 日志分片记录缺失 | [34925581757](https://github.com/mihari-proxy/mihari/actions/runs/34925581757)：logical records lost or mixed | 250ms 写锁等待是嫌疑；测试没有收集 handler 错误、Reporter 和 Dropped。race 重复 20 次未复现 |
| macOS 子进程锁文件 ENOENT | [34922177821](https://github.com/mihari-proxy/mihari/actions/runs/34922177821)：OpenAppend lock 文件失败 | 尚未定位；必须与写锁等待分开处理 |

既有本地验证均在 Windows、相同 dev 基线的独立临时检出中完成：WebSocket 普通 100 次及 race 200 次通过，coverage 1000 次失败 3 次。重复比例只描述该次实验，不代表 CI 失败概率。

## Task 1：WebSocket 关闭与诊断（优先）

涉及：`internal/web/server_test.go`、`internal/web/diagnostics_test.go`；必要时最小修改 `internal/web/server.go`。

### 原因与实现选择

当前 `proxyWebSocket` 在升级连接后，从 `r.Context()` 派生 relay context。`CloseNow()` 没有发送正常关闭帧；底层读错误与 request context 取消可能以不同顺序到达。依赖 v1.8.15 的 `accept.go` 注释及 [官方 README](https://github.com/coder/websocket/blob/v1.8.15/README.md) 都提示升级后使用 request context 可能产生意外行为。

因此，真实断连接测试不能证明“每次必定产生同一种传输错误”。同时，当前 `webSocketRelayFailure` 按结果顺序选取首个可报告错误，存在取消先到时遮蔽另一条独立故障的可能，需要确定性测试验证。

### 测试与修复步骤

1. 用现有 `webSocketRelayResult` 构造回归：已取消 context 下，第一条结果为包装后的 `context.Canceled`，第二条为独立故障；交换到达顺序。两者都必须选择独立故障，而非取消原因。先运行并记录 Red。
2. 为判定补齐正常关闭帧、异常状态、纯取消、由 owner 关闭导致的 `net.ErrClosed`、混合 `errors.Join(context.Canceled, independentFault)`。保留既有“同一连接的 reader 关闭证据”规则。
3. 若 Red 确认选择错误，采用先检查两路独立故障、再处理剩余终止原因的最小实现。不要把简单 `errors.Is(err, context.Canceled)` 当成吞掉整个错误链的理由。
4. 将真实 `CloseNow` 测试的核心断言收敛到：上游确实因对端关闭退出；两个 relay 都 join；handler 完成；会话计数归零；不会由测试 deadline 强行结束。用现有 `wsObserver.handlerResult` 明确等待完成。
5. 对断连接过程中产生的诊断，按识别出的终止原因验证：独立实际失败必须恰好记录一次 ERROR；纯关闭/取消按既有策略处理；拒绝重复报告或无关错误。固定级别的强断言放在可控故障测试中，不能简单接受任意 INFO/ERROR。
6. 继续保留读限制、异常关闭状态及混合故障测试对单次 ERROR 的严格要求。

本方案保留当前 request 取消传播。直接换成 Background/WithoutCancel 会牵涉 gateway Shutdown 对已升级连接的所有权与回收，不能仅为稳定 CI 引入。若确定性回归证明必须改变此路径，应先补充明确的 gateway 生命周期方案。

## Task 2：把短时间假设改为行为证明

### 快照 HTTP Timeout 独立性

涉及：`internal/control/client/logging_snapshot_test.go`。

- 拆分“客户端配置独立性”与“成功读取快照并完成导出”。
- 对专用 snapshot client 验证：Timeout 不继承普通 client 的 50ms；普通 client 保持不变；传输、鉴权及重定向限制继续有效；若克隆 transport，其头部预算采用快照策略。
- 流程验证使用已有 fixture 与可控 transport/握手 channel，去掉用于跨过 50ms 的固定 sleep，并采用足以容纳 CI 调度的测试保护期限。超时仅负责发现挂死。
- 保留独立的 idle/total timeout、取消关闭 body、失败不发布归档测试，以证明移除时间竞争没有移除超时保障。
- 必须证明将 snapshot client 改回复用普通 client 时，独立性测试会失败。

### 导出取消

涉及：`internal/logging/export_zip_test.go`。

- 使用已有 `Checkpoint(stageWriteZip)` 在第二个 chunk 取消。
- 验证取消后返回 `context.Canceled`、没有继续处理下一 chunk、没有发布目标文件、workspace 与临时文件清理完毕。
- 从实际调用 cancel 的位置开始记录取消延迟；取消前的准备工作不计入。
- 优先以 checkpoint 次数、返回结果及清理状态证明行为；若保留等待上限，给出适当的挂死保护期限，不将 2 秒总导出耗时当作正确性条件。
- 所有释放 channel、文件和临时资源必须在失败路径同样清理。

## Task 3：日志分片并发测试与失败诊断

涉及：`internal/logging/record_fragment_lifecycle_test.go`、`record_fragment_subprocess_test.go`、`rotator_subprocess_test.go`。

1. 先让测试收集每次 `slog.Handler.Handle` 的返回错误，并以并发安全的容器记录 FailureReporter 与各 writer.Dropped()。子进程 fragment 模式也必须在 Handle/Close 失败时返回非零，不能让 slog 忽略错误后继续报成功。
2. 保持两 writer 同时开始的并发场景，给“完整性与轮转”测试显式设置较宽的 WriteWait（建议 10s 作为挂死保护）。产品默认 250ms 不变；默认超时丢弃策略继续由独立的可控锁测试验证。
3. 断言每个 record_id、fragment_count/index、重组原文，保留分片交错和轮转覆盖。失败信息包含记录/分片数量、drop 数和错误类别，避免只留下“丢失或混合”。
4. 若加强诊断后发现实际文件写入/轮转缺陷，先构造确定性回归，再修改最小生产路径；不能预先认定全部记录缺失都是锁超时。

### macOS ENOENT 独立调查入口

- 当前失败来自 legacy `PrivateFS.openAppendLocked` 的 `openat(O_CREAT)`，发生在取得 advisory lock 之前；提高 WriteWait 无法修复它。
- 补充子进程退出码、初始化阶段、父目录存在性及已持目录身份的测试诊断，避免输出业务敏感内容。
- 在 macOS 用临时根及双子进程复现初始化竞争，区分文件系统创建、目录生命周期和测试清理问题；本地 Windows 的通过不能替代该验证。
- 不以预建锁文件、无限重试 ENOENT 或移除无链接/身份验证来掩盖问题。
- 若无法确定性复现，保留为明确未解决项；不得宣称所有 CI flake 已修复。涉及平台实现的修复单独评审。

## Task 4：验证与验收

每一项生产行为变更均按 Red–Green–Refactor，先执行最小回归，再运行相关包。

```powershell
go test -count=1 ./internal/web ./internal/control/client ./internal/logging
go test -race -count=1 ./internal/web ./internal/control/client ./internal/logging
go test -count=1000 -run '^TestGatewayWebSocketClientCloseStopsUpstream$' '-covermode=atomic' '-coverpkg=./...' ./internal/web
go test -race -count=100 -run '^TestGatewayWebSocketClientCloseStopsUpstream$' ./internal/web
go test -race -count=30 -run '^TestRecordFragment_ConcurrentWritersRotateSnapshotAndExport$' ./internal/logging
go test ./internal/integration
go vet ./internal/web ./internal/control/client ./internal/logging
```

PowerShell 下给 coverage 参数加引号，避免原生参数传递影响实际测试范围。只在相关失败、代码更改或尚未消除的不确定性存在时增加重复验证。

验收要求：

- WebSocket 的确定性错误优先级回归通过；coverage 重复实验不再出现原有断言失败；真实故障与资源回收断言保持有效。
- 快照独立性测试能捕获复用普通 HTTP 超时的回归，取消/超时后的资源与输出约束仍成立。
- 日志完整性测试不依赖 250ms 调度假设；若写入失败能立即给出可归因证据。
- macOS ENOENT 必须得到 macOS 证据，或者在交付中明确仍未解决。
- 修改的 Go 文件通过 gofmt；必要时运行平台包测试，并对受影响目标作无 CGO 编译检查。
- 通过现有 Windows/Linux/macOS CI 全量 unit/race/vet 与构建矩阵；本地尚未执行的项目不标记通过。
- 不修改 CHANGELOG，不提交临时报告或覆盖率文件。提交、推送、PR 与合并按授权范围处理。

## 建议实施顺序

先完成 Task 1 与 Task 2，再完成 Task 3 的测试诊断与等待隔离。macOS 初始化问题独立跟进，以实际回归证据决定是否追加平台修复。CI 配置当前不需要扩大总超时。
