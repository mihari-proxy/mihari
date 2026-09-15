# CI 偶发失败执行计划

日期：2026-09-15。
状态：执行中。用户已授权实现、提交、推送、PR，以及处理 CI/bot review 反馈至全绿；不自动合并。
依据：[调查证据与修复思路](2026-09-15-ci-flaky-tests.md)。

## 工作区与实施约束

- Worktree：`C:/Users/Kinema/Documents/modular_dev/mihari/.worktrees/fix-ci-flaky-tests`
- 分支：`fix/ci-flaky-tests`
- 建立基线：`d132db2b2f82a245fd52c18182f741f662a6b5f0`，来源 `origin/dev`。
- PR 目标：`dev`；不修改 `CHANGELOG.md`。
- 保留用户原工作区修改，不在 main/dev 上编辑或提交。
- 不新增依赖，不改 Go/toolchain、持久化格式、公开 CLI/JSON 或网络/权限边界。
- 产品的日志写锁等待仍为 250ms，race 的包级上限仍为 30m。
- 不以自动重试、跳过测试、降低矩阵覆盖或忽略失败来稳定 CI。
- 不改全局取消日志策略。本任务只处理错误选择、资源回收证明与测试中的时间假设。
- 所有命令均在上述 worktree 执行；测试仅使用 fixture、临时目录、loopback 和仓库测试子进程。

## 交付范围和顺序

| 任务 | 交付物 | 前置条件 | 性质 |
| --- | --- | --- | --- |
| 0 | 基线记录、准确变更范围 | 无 | 只读核对 |
| 1 | WebSocket 确定性错误选择回归、可靠的连接回收测试 | 0 | 测试及被回归证明必要的最小生产修复 |
| 2 | 不依赖 50ms/200ms/1s 竞争的快照测试 | 0 | 预期仅测试 |
| 3 | 按复制进度证明导出取消与清理 | 0 | 预期仅测试 |
| 4 | 日志分片测试捕获写入失败，独立设置完整性测试预算 | 0 | 预期仅测试及测试子进程 |
| 5 | macOS ENOENT 定位证据；可复现时的最小修复 | 4 | 平台调查；修复范围取决于证据 |
| 6 | 本地验证、三平台 CI 证据、交付说明 | 1–4；5 必须给出状态 | 验收 |

按 0 → 1 → 2 → 3 → 4 → 5 → 6 顺序执行。Task 5 可形成独立修复，但不得从最终未解决项中消失。计划不要求启动 subagent。

## Task 0：核对基线

- [ ] 阅读 `.github/CONTRIBUTING.md`、`README.md`、目标路径适用的 `AGENTS.md` 与相关代码/测试。
- [ ] 核对分支、HEAD 和工作区；目前预期只有两份计划文档未跟踪。若出现其他修改，先确定归属，禁止清理覆盖。
- [ ] 对照 origin/dev 的变化，仅报告差异，不擅自重置 worktree。若基线确需更新，保留本任务文件再正常合并/变基。
- [ ] 运行相关包基线，并记录测试名称、平台、命令、退出码。既有失败属于修复证据，不误标成新增回归。

```powershell
git branch --show-current
git rev-parse HEAD
git status --short
go test -count=1 ./internal/web ./internal/control/client ./internal/logging
```

**完成条件：** 执行位置无误，基线可解释，未覆盖其他工作。

## Task 1：WebSocket 错误优先级与回收

**修改文件：**

- `internal/web/diagnostics_test.go`
- `internal/web/server_test.go`
- `internal/web/server.go`：仅在下面的 Red 确认生产错误选择缺陷时修改。

### 1A. 确定性回归

- [ ] 新增 `TestWebSocketRelayFailure_IndependentFaultOutranksCancellation`，复用现有 `webSocketRelayResult`；不使用真实网络和 sleep。
- [ ] 包含下列矩阵；以 `errors.Is`/原始 CloseStatus 检查最终原因，不匹配包装字符串。

| 第一条结果 | 第二条结果 | 预期 |
| --- | --- | --- |
| 包装后的 context.Canceled | 独立 permission 故障 | 保留 permission 原因 |
| 独立 permission 故障 | 包装后的 context.Canceled | 同上 |
| 包装后的 context.Canceled | 异常 CloseError（如 PolicyViolation） | 保留原始异常关闭状态 |
| 包装后的 context.Canceled | Join(cancel, permission)，stopped=true | 保留混合链中的实际故障 |
| 正常 Close 帧 | 关闭过程中诱发的纯取消/closed | 保留既有正常关闭规则 |

- [ ] cancellation 与独立故障的组合覆盖未取消和已取消的父 context；stopped 标记按实际时间含义设置，不能为了得到预期结果随意翻转。
- [ ] 运行 Red，确认失败是“返回取消，遗漏真实故障”，而非编译失败。

```powershell
go test -count=1 -run '^TestWebSocketRelayFailure_IndependentFaultOutranksCancellation$' ./internal/web
```

### 1B. 最小生产修复

- [ ] 保留同一连接 reader 的正常关闭证据，以及诱发 closed-write 的排除规则。
- [ ] 对剩余候选先寻找独立实际故障，再回退到既有取消/关闭处理；不能因先到的可报告取消立即返回。
- [ ] 只将已识别的纯终止链当成取消/关闭；混合 Join、未知错误、无法完整检查的错误链继续作为实际故障保留。
- [ ] 不把整个 `errors.Is(err, context.Canceled)` 为真的链一律静默；不改 `diagnostics.FailureLevel` 全局策略。
- [ ] 两个 relay join 后仍由现有 owner 报告一次，不在每个 goroutine 增加独立日志出口。
- [ ] 重跑新回归与 `TestWebSocketRelayFailure_PreservesIndependentFaults`。

### 1C. 真实连接测试

- [ ] 给 `TestGatewayWebSocketClientCloseStopsUpstream` 绑定已有 `webSocketRelayJoinObserver`。
- [ ] CloseNow 后依次验证上游退出、peer-close 原因、`handlerResult == 0` 和 SessionCount 归零。所有等待必须有保护期限，失败路径也释放资源。
- [ ] 该网络测试不再固定断言一次 ERROR：CloseNow 后底层断连与 request context 取消是可竞争的可观察结果。
- [ ] 用包装的 Reporter 收集原始 `diagnostics.Record`，并继续写入测试日志 buffer。handler 完成后检查报告次数不超过一次、event/component 正确、cause 非空，没有伪造 operation ID。
- [ ] 若报告为取消类别，检查原始链确为纯取消，不能只看格式化文本；若是独立实际故障，严格要求 ERROR。未知或混合故障不能进入宽松取消分支。
- [ ] 固定“真实错误必须一次 ERROR”的责任由确定性错误选择回归与现有读限制、PolicyViolation、混合故障测试共同承担；不得一起放宽。
- [ ] 保留已有 request cancellation 回收测试。禁止直接换用 Background/WithoutCancel 来压制取消；这会改变已升级连接的生命周期，需要单独设计。

```powershell
go test -count=1 -run '^(TestWebSocketRelayFailure_|TestGatewayWebSocket)' ./internal/web
go test -race -count=1 ./internal/web
```

**完成条件：** 新回归先 Red 后 Green；到达顺序不再掩盖独立故障；真实断连的两路转发和 handler 都完成；原有真实错误强断言有效。

## Task 2：快照客户端超时独立性

**修改：** `internal/control/client/logging_snapshot_test.go`。预期不改生产代码。

- [ ] 新增 `TestSnapshotHTTP_DoesNotMutateBaseClient`：普通 client 设非零 Timeout；snapshot client Timeout 必须为零，普通 client 的值不变。
- [ ] 针对 `*http.Transport` 验证克隆隔离及 ResponseHeaderTimeout 的快照预算，保留普通 transport 原配置。关闭测试 transport 的空闲连接。
- [ ] 对自定义 RoundTripper 验证仍使用原 transport；重定向和 credential provider 约束复用已有测试，不制造不必要的新接口。
- [ ] 将 `TestOpenMachineSnapshot_DoesNotReuseClientHTTPTimeout` 改为直接验证配置和使用既有 fixture 完成快照/导出，去掉 200ms sleep 与临时压缩到 1s 的快照预算。
- [ ] 成功流程继续验证目标归档身份、内容/manifest 和 body 关闭。沿用正常快照预算，并用外层测试保护期限防止挂死。
- [ ] 以一次受控故障注入验证新测试能失败：临时令 clone.Timeout 继承 base.Timeout，确认回归命中后精确还原该行；该临时改动不得残留或提交。无需为了制造 Red 改坏其他行为。
- [ ] 保留 idle/total timeout 和取消关闭 body 的专门测试。

```powershell
go test -count=1 -run '^(TestSnapshotHTTP_|TestOpenMachineSnapshot_)' ./internal/control/client
go test -race -count=1 ./internal/control/client
```

**完成条件：** 即使机器调度较慢，独立性测试也不依赖跨过某个毫秒窗口；错误复用普通 Timeout 能被直接捕获；快照超时与取消保障仍被测试。

## Task 3：导出取消的复制边界与清理

**修改：** `internal/logging/export_zip_test.go`。预期不改 `export_zip.go`。

当前 `runCheckpoint` 在回调前检查 ctx，回调取消后仍可能完成当前 32KiB 块，再由 `copySpool` 的 ctx 检查返回。因此验收允许完成当前块，不要求取消瞬间停止所有 IO。

- [ ] 新增 `TestCopySpool_CancelStopsBeforeNextChunk`，使用大于三个 buffer 的确定性内存输入与计数 reader/writer。
- [ ] 在第二次 stageWriteZip checkpoint 取消；断言 `context.Canceled`、实际读取/写入不超过两个 buffer、没有第三次复制，并证明已有非零复制发生。
- [ ] 保留并修改 `TestExportWithOps_CancellationDuringMultiChunkZipCopyReturnsPromptly`：取消仍发生在 ZIP 复制途中，验证未发布、目标父目录无残留，借助现有 Observe/关闭钩子验证能力资源已关闭。
- [ ] 去掉“完整导出必须小于 2 秒”的断言。若记录 elapsed，从调用 cancel 时起算并仅作诊断；挂死由明确保护期限发现，不作为性能门槛。
- [ ] 计数 reader/writer 是测试边界，不复制生产 copy 循环。无需启动无法可靠停止的等待 goroutine。
- [ ] 使用受控故障注入确认移除取消检查时会被复制数量、错误结果或发布断言捕获，随后还原。

```powershell
go test -count=1 -run '^(TestCopySpool_CancelStopsBeforeNextChunk|TestExportWithOps_CancellationDuringMultiChunkZipCopyReturnsPromptly)$' ./internal/logging
```

**完成条件：** 证明复制取消边界及所有清理结果，不再把取消前的准备耗时误判成取消延迟。

## Task 4：日志分片完整性和错误可见性

**修改：** `record_fragment_lifecycle_test.go`、`record_fragment_subprocess_test.go`、`rotator_subprocess_test.go`，均位于 `internal/logging`。

- [ ] 为完整性测试加入 start barrier，两个 writer 均准备好后同时释放，保留并发竞争。
- [ ] 使用 `logger.Handler().Handle` 与等价的 ERROR Record 保留现有 group/attr 输入，并捕获返回错误。不得把错误检查省略在 slog 的无返回值方法之后。
- [ ] 测试 Reporter 用 mutex 保护的结构记录 FailureClass 与 cause；worker 通过结果槽/channel 返回错误，由测试主 goroutine 统一判断。
- [ ] 先在原 250ms 预算下运行有诊断版本，保存结果。若仍未复现，只记录“未复现”，不认定已证明锁超时。
- [ ] 完整性测试明确注入 WriteWait=10s；仍严格要求 Handle 成功、Dropped 为零、无 FailureReporter 事件。不要增加失败重试。
- [ ] 保留每条逻辑记录的 ID、分片总数、index 唯一性与重组原文断言；加强错误数量输出，不在测试失败中打印完整长内容。
- [ ] 子进程 fragment 模式捕获 Handle/Close 错误并返回非零；保留其他 subprocess 模式，避免借机重写整个测试框架。
- [ ] 复用可注入 OpenLock 的模式验证“写入失败不再被测试 helper 隐藏”；与成功完整性路径分开。
- [ ] 重跑默认策略测试，确保 250ms 丢弃语义及 Open 的 hard cap 未改变。WriteWait 注入不会改变 Open 的 hard cap，这两种超时必须分别报告。

```powershell
go test -count=1 -run '^TestRecordFragment_' ./internal/logging
go test -count=1 -run '^(TestRotatingWriter_WriteDropsWhenLockTimesOut|TestRotatingWriter_OpenBackgroundContextHasHardLockCap|TestRotatingWriter_TwoProcessesPreserveAllSequencesAndRotate)$' ./internal/logging
go test -race -count=30 -run '^TestRecordFragment_ConcurrentWritersRotateSnapshotAndExport$' ./internal/logging
```

**完成条件：** 完整性测试仍能检出丢失、混合和写入失败，但不把产品的短写锁预算当作 CI 调度保证；未知失败有可归因证据。

## Task 5：macOS 锁文件 ENOENT 定位

**首先只改测试诊断。** 生产候选位置为 `internal/platform/privatefs_unix.go`；在确认原因前不得修改。

- [ ] 子进程启动失败时先停止/回收其进程，再读取 stderr；避免进程仍在写 bytes.Buffer 时并发读取。记录 exit code、阶段、writer 标签、测试临时根是否存在。
- [ ] 每个 child 只有一个 Wait 所有者。任何 Scan/等待都必须能在失败保护期限后关闭 stdin、Kill 并 Wait，不能泄漏子进程。
- [ ] 对两个 child 的并行初始化加入明确启动屏障，保留独立的 rotation 测试，不用预建锁文件或串行初始化掩盖这个场景。
- [ ] 在可用的隔离 macOS 执行环境，或后续获准推送后的现有 macOS CI 中运行下列命令。仅创建临时目录及测试子进程，不使用 root、系统服务或真实配置。

```sh
go test -count=100 -timeout=10m -run '^TestRecordFragment_TwoProcessesKeepOriginalContentAcrossRotation$' ./internal/logging
```

- [ ] 若失败，核对父目录是否先被移除/替换、持有目录 FD 是否仍关联预期目录，以及创建路径和失败 child 的退出顺序。
- [ ] 根据证据将错误定位到测试生命周期或平台打开逻辑，先加入对应的最小回归，记录正确 Red 后再修复。
- [ ] 平台修复必须保留 no-follow、目录身份和权限检查。不得新增无界 ENOENT 重试或放宽安全检查。

**出口必须三选一并记录：**

1. 已复现并修复：提供 macOS Red/Green 与重复验证。
2. macOS 重复未复现：只交付诊断增强，记录命令/次数，问题仍未解决。
3. 无可用 macOS 执行环境：任务标记“等待平台验证”，保留后续命令；不阻塞 1–4 的本地实现，不把本地 Windows 通过当作 macOS 修复证据。

## Task 6：集中验收与交付

### 6A. 本地必要验证

先完成每项最小测试，再集中运行；不要在无新变化时重复整个矩阵。

```powershell
go test -count=1 ./internal/web ./internal/control/client ./internal/logging
go test -race -count=1 ./internal/web ./internal/control/client ./internal/logging
go test -count=1000 -run '^TestGatewayWebSocketClientCloseStopsUpstream$' '-covermode=atomic' '-coverpkg=./...' ./internal/web
go test -race -count=100 -run '^TestGatewayWebSocketClientCloseStopsUpstream$' ./internal/web
go test -count=1 ./internal/integration
go vet ./internal/web ./internal/control/client ./internal/logging
git diff --check
git diff --stat
git status --short
```

- [ ] 所有修改过的 Go 文件运行 gofmt；通过明确文件清单检查格式，避免无关全仓 CRLF 改动。
- [ ] 涉及平台生产修复时，额外运行 `go test ./internal/platform` 和相关 Unix 安全回归；按受影响目标无 CGO 编译，环境变量只设置在独立进程内，构建产物放临时目录。
- [ ] 对比相同基线、同样覆盖率参数的相关包覆盖情况，解释结构变化造成的下降；不增加固定百分比门槛。
- [ ] 核对新增诊断不泄露业务敏感内容、控制协议/JSON 无变化、产品写锁预算无变化、无测试残留进程。
- [ ] 所有临时故障注入已精确还原。待提交 diff 只含计划内文件。

### 6B. 远端验收与提交边界

- [ ] 后续获准提交时，先检查准确 diff，再按可独立审查的意图组织 Conventional Commits，执行 DCO sign-off。没有明确授权时不创建 commit、push 或 PR。
- [ ] 推送后使用现有 CI 验证 Windows/Linux/macOS unit、race、vet，以及六目标无 CGO build 与相关安全检查；不为本任务提高 30m 上限。
- [ ] 在 Task 5 需要 macOS 专项复现且已有 CI 无法执行时，再提出具体的临时诊断 job 变更；不默认建立新 workflow。
- [ ] PR 目标 dev；描述包含原始触发、最终行为、确定性回归、重复测试数据和未解决项。合并仍需用户确认。

## 完成定义

本执行计划编写完成与修复完成是两个状态。修复交付须分别报告：

- Task 1–4 的实现及验证是否完成。
- Task 5 的三个出口之一，以及剩余风险。
- 本地实跑项目、远端 CI 结果和未验证平台。
- 生产代码修改原因及对应的先失败后通过证据。

不能以某次 CI 恰好通过、某次重跑成功或重复实验未复现，替代确定性行为测试和未解决项说明。

## 执行记录

执行基线已更新至 `f365631`。首轮生产变更为 `webSocketRelayFailure`：将纯取消暂存为后备结果，优先保留另一条 relay 的独立故障。没有改变 request context 传播、全局日志级别策略和产品锁超时。

- Task 0：相关包基线通过。
- Task 1：新确定性回归先出现 12 个错误选择失败，最小生产修复后通过；保留正常关闭、混合链和异常状态的既有回归。真实 CloseNow 测试等待两个 relay 完成，仍严格要求一次终止报告，仅纯取消链可为 INFO。coverage 重复 1000 次通过。
- Task 2：去除 200ms sleep 和 1s 人工快照上限，直接检查 snapshot/base client 隔离及 transport/redirect 策略；临时复用 base.Timeout 后两个测试按预期失败，已还原临时变更。成功快照与专门的超时/取消测试通过。
- Task 3：用实际复制字节与剩余输入验证取消后不进入第三块；去除完整导出的 2s 断言。临时让 copySpool 忽略 ctx 后新测试正确失败，已还原。原有清理与未发布断言保持通过。
- Task 4：加入同时开始屏障、Handle 错误捕获、Dropped/FailureReporter 断言；原 250ms 配置带诊断 race 重复 5 次未复现。完整性测试改用 10s WriteWait，产品 250ms 不变。子进程分片写入失败会返回非零，辅助回归证明写入在失败处停止。
- Task 5：双子进程在初始化前同时释放；子进程使用 2 分钟保护期限，失败先回收再读取诊断，stderr buffer 并发安全，Wait 仅消费一次。现有 macOS unit job 增加原测试的 100 次重复检查；ENOENT 尚无确定根因，等待原生平台证据，不将其描述为已修复。
- Task 6 本地验证：`go test -count=1 ./...`、`go vet ./...`、目标包 unit/race、集成测试全部通过；WebSocket race 重复 100 次、coverage 重复 1000 次通过；日志并发 race 重复 30 次通过；workflow 策略测试 79 项通过。Web 包覆盖率基线 83.6%，本分支 83.7%（两个基线提交之间该包没有变更）。修改的 Go 文件 gofmt 检查与 diff whitespace 检查通过。全仓三平台 race/构建及 macOS 专项重复等待 PR CI，未以本地目标包结果替代。

实施中的取舍：未重设计 gateway 的升级连接生命周期。macOS 平台修改以原生复现证据为前提。

### macOS 原生 Red 与后续修复

PR #248 首轮 CI `34933283302` 在 macOS 专项 100 次检查中多次失败，错误一致为 `open append mihari-tui.log.lock: no such file or directory`。该错误来自旧版 PrivateFS 的 `unix.Openat`，发生在获取 advisory lock 之前；两个 child 均在父进程发出后续写入命令前初始化，此时没有日志轮转或测试目录清理。三平台完整 race、其余平台 unit、六目标构建、安全检查通过。

后续将旧版 Unix append-create 与现有 `trustedOpen` 对齐：先 `O_CREAT|O_EXCL`，仅 `EEXIST` 时打开已存在文件；其他错误立即返回。保留 held dirfd、`O_NOFOLLOW`、`O_NONBLOCK`、普通文件验证及权限收紧，不增加 ENOENT 重试。新增 16 个独立 PrivateFS 并发打开同一新文件的回归，验证同一文件身份和所有追加内容。原生失败测试已先于此生产修改运行；后续 macOS Green 仍须由 CI 确认，尚不能归因到某个具体内核缺陷。

本地新增回归重复 30 次通过；Linux amd64/macOS arm64 平台测试二进制与 CLI 无 CGO 编译通过。后续 CI 使用同一个 100 次检查作为 macOS 验收。

`98c203f` 的 CI `34934792110` 三平台 unit/race/vet、六目标无 CGO build、lint、coverage 全部通过，macOS 双进程初始化重复 100 次通过；`34934792281` 的 Linux/macOS 原生安全与汇总检查通过。由此 Task 5 获得同一原生复现测试的 Red/Green 证据。

Pullfrog 分别审查 `ee5bcea` 与 `98c203f`，未发现生产代码问题；采纳其唯一建议，将直接验证文件身份和追加内容的 `TestPrivateFS_ConcurrentOpenAppendKeepsSameFile` 也加入 macOS 100 次重复步骤。CodeRabbit 手动审查受免费额度限制，cubic 月度额度耗尽，二者未提供实际审查结果。
