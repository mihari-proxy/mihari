# 公共日志链路一次性扫描登记

工作目录：`.worktrees/full-error-logging`；分支：`feat/full-error-logging`。

本登记对应用户要求的全量扫描与修复。先枚举全部责任文件并集中查找候选，再按下面固定编号收口；修复后只检查差异和失败测试，不重新开始全仓发现。

## 文件覆盖

| 范围 | 核查内容与责任 |
| --- | --- |
| `internal/diagnostics/{error,record,http}.go` | 内部 cause、已记录标记、预期错误和取消分类、HTTP 正文与握手副本 |
| `internal/logging/{diagnostics,handler,record_writer,capture}.go` | 原文格式化、任意 error 属性、逻辑限额、编码分片、stdout/stderr 生命周期 |
| `internal/logging/{config,runtime,rotator,record_mutex}.go` | 文件出口、配置更新、加锁、轮转、写入与关闭、独立故障出口 |
| `internal/logging/{operation_context,operation_attrs}.go` | 值型操作身份与保留字段；不承担业务执行或 IO |
| `internal/logging/{snapshot,machine_snapshot,assemble,export,export_json,export_zip}.go` | 固定日志来源、快照/ZIP、预算、解析统计、原文与清理原因 |
| `internal/logging/{access_windows,access_unix,redactor}.go` | 既有权限策略和终端兜底脱敏；没有把内容脱敏重新接回文件/导出 |
| `internal/integration`、`internal/securitytest` | 跨模块和权限验收；securitytest 两个带构建标签的 Go 文件是测试辅助，不是产品文件日志出口 |

## 固定候选清单

| ID | 候选 / 当前结论 | 证据与验证 |
| --- | --- | --- |
| L01 | formatter 旧摘要/提前脱敏已由已有实现移除；本轮补齐 APIError.Details 原文与超长外层错误的根因保留 | `TestDiagnosticOriginal_APIErrorRetainsDetails`、`TestDiagnosticOriginal_LongOuterRetainsLeafCause` 红→绿；既有完整错误链、循环、深度、UTF-8 测试 |
| L02 | AlreadyReported 对 typed nil 调用 Unwrap 会 panic；已修复 | `TestAlreadyReported_TypedNilDoesNotInvokeUnwrap` 先触发 panic，再验证返回 false；仍不新增 recover |
| L03 | FailureLevel / NormalCancellation 已区分预期 INFO、实际 ERROR 与旧取消回调 | `record_test.go`、`expected_logging_test.go`；不因取消 ctx 吞掉并存 IO 原因 |
| L04 | HTTPBody 原文预算 256 KiB、UTF-8 截断；握手上游副本限制明确标记 | `http_original_test.go`；握手调用方由客户端组核查 |
| L05 | 分片只识别 msg/cause，以及一条日志有多个大 error 时会丢整条记录；已修复 | `TestRecordFragment_ErrorAttributeDoesNotRequireCauseKey` 三种键名及分组、`TestRecordFragment_MultipleLargeErrorsRemainComplete` 双字段红→绿；逐字段分片保留元数据和现有单记录上限 |
| L06 | 逻辑分片、并发、跨进程、轮转、IPC 已有回归；本轮只执行相关回归 | `record_fragment*_test.go`、集成 `logging_fragment_ipc_test.go`；ID/容量/部分写入故障有明确出口 |
| L07 | export/assemble 警告原先只剩固定文字；现在安全 Error() 包装实际 cause | `TestExportOriginal_SuccessWarningsKeepCleanupCause` 对两条导出路径和发布/最终清理分别红→绿；成功仍成功 |
| L08 | export target 目录打开/检查/解析转换丢因；logging 层已保留 | `TestExportOriginal_TargetOpenCauseSurvives`；Windows OpenPublishDir 进一步丢因交系统组同一候选修复，最终套件验证 |
| L09 | assemble 预算拒绝、spool 创建失败时丢弃 Close；读取失败时丢弃 Finish；已保留 | `TestAssembleOriginal_ReadFinishAndCloseCausesSurvive` 红→绿；reader 仍关闭一次，公开错误固定 |
| L10 | rotator 初始化失败忽略锁 Close；已加入返回原因 | `TestRotatorOriginal_OpenFailureRetainsLockCleanup` 对 lock/unlock 两个分支红→绿 |
| L11 | rotator 写入结束 Close 失败未进入独立 reporter；已修复 | `TestRotatorOriginal_WriteCloseFailureUsesIndependentReporter` 红→绿；Stat/write/close 原因合并，不写回失效 logger |
| L12 | export JSON、source spool、ZIP copy 未检查 short write；已修复 | `TestExportOriginal_ShortWriteIsFailure` 三出口红→绿；差异复核新增读取与写入/取消同时失败回归，保留双方原因；不能成功发布缺字节结果 |
| L13 | snapshotSource 已聚合打开/Stat/Unlock/Close 错误并清理句柄，无需改写 | `snapshot_test.go`、`machine_snapshot_test.go`；machineSourceReader 的 hash.Write 按接口保证不会失败 |
| L14 | 损坏/超长历史 JSONL 的跳过是既有导出协议语义，无需改为业务失败 | `SkippedInvalid` 统计明确；正常 EOF/时间范围过滤不是错误。真实 reader/writer IO 错误继续返回 |
| L15 | capture 忽略 Handler 返回值属于进程输出排空策略，已补责任注释 | 写入/编码故障由 recordWriter/rotator 的独立 FailureReporter 报告；不能让日志故障阻断核心 stdout/stderr |
| L16 | config/runtime/recordMutex/operation 元数据无需新增 owner | 校验/取消直接返回；Apply 维护失败经独立 reporter，业务锁与日志锁规则保持 |
| L17 | access 初始化、终端 FailureReporter、Redactor 采用已接受出口策略 | logger 建立前无文件出口；最终终端写入失败不递归重试；权限、保留字段、固定来源不因原文日志放宽 |
| L18 | 有效历史 JSON 重编码可能因 HTML/Unicode 转义放大并超过物理限额；客户端组接手修复 | 本地与 machine source 统一校验后保留原始字节；`TestExportOriginal_NearLimitHTMLEscapableRecordRemainsBounded` 负责上限与摘要回归 |

## 本轮验证

所有新增行为先运行失败测试，再运行修复后的目标测试。目标输出在系统临时目录：

- `mihari-logging-scan-red.txt` / `mihari-logging-scan-green.txt`：导出原因和 rotator 初始化；绿阶段曾仅剩系统组负责的目标目录 cause。
- `mihari-common-scan-red.txt` / `mihari-common-scan-green.txt`：错误属性分片与 typed nil。
- `mihari-logging-write-red.txt` / `mihari-logging-write-green.txt`：关闭故障和 short write。
- `mihari-api-details-red.txt` / `mihari-api-details-green.txt`：公开错误 Details 的文件诊断。

本组套件：`go test ./internal/logging ./internal/diagnostics`，结果见 `mihari-logging-scan-suite.txt`。该套件已通过（logging 31.5 秒，diagnostics 0.36 秒），包含系统组补齐后的 L08 跨模块回归；随后 L12 的双重失败补充改动通过目标测试，纳入最终全仓复验。临时文件不提交。跨组最终全仓/race/vet/六目标构建另行统一执行，不能沿用较早版本结果冒充最终验收。
