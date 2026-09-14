# 资产与存储域完整错误日志审计

## 范围与闭包

本轮只审计 `internal/core`、`internal/geoip`、`internal/panel`、`internal/subscription`、`internal/config`、`internal/state`、`internal/onboarding`、`internal/preferences`、`internal/update` 的生产 Go 文件。首轮清单在修复前一次性锁定为 76 个文件；审计新增 `internal/core/http_diagnostics.go` 后，当前生产文件为 77 个。没有用后续 `rg` 次数或测试通过代替完整性判断。

逐文件检查了五类风险：`APIError` 是否丢失 cause、清理/关闭错误是否被吞、失败后 fallback/retry 是否无记录、诊断是否在日志边界前被脱敏、执行 owner 是否能拿到现有 reporter。公开 `APIError`、JSON、状态和事件继续保持安全且简短；文件诊断保留原始 URL、HTTP body、命令输出、配置片段、路径和底层 cause。HTTP body 统一限制为 256 KiB。

首轮候选共 32 组，主分类计数为：API/cause 11、cleanup/Close 9、fallback/retry 5、pre-redaction 2、reporter availability 5。以下表是有限闭包；修复后没有重新启动宽泛扫描。

## 有限候选清单

| # | 主分类 | 文件 / symbol | 处置与证据 | 回归验证 |
|---:|---|---|---|---|
| 1 | API/cause | `core/command.go`, `version.go`: `DetectVersion`, `ParseVersion`, `ValidateConfig` | 已修复。公开错误文本不变，私有链保留 stdout/stderr 与执行 cause。 | `TestCoreOriginal_CommandOutputPreservedInFileDiagnostics` |
| 2 | API/cause | `core/config.go`: `EnsureRuntimeConfig`, `BootstrapConfig` | 已修复。读取、YAML、CIDR 与 marshal cause 进入私有链。 | `TestCoreOriginal_RuntimeConfigCausePreserved` |
| 3 | API/cause | `core/release.go`: `LatestRelease` | 已修复。transport、状态、原始 body、read/Close 与 JSON cause 均保留。 | `TestCoreOriginal_HTTPFailurePreservesBodyAndClose`, `...HTTPTransportCausePreserved` |
| 4 | API/cause | `core/migration.go`: `DownloadMigrationCore` | 已修复。HTTP 来源和版本解析 cause 保留，公开分类不变。 | core original diagnostics focused tests |
| 5 | API/cause | `core/install.go`: gzip/zip/write candidate | 已修复。archive parser、复制、写入与文件操作 cause 保留。 | `TestCoreOriginal_ArchiveAndFilesystemCausePreserved` |
| 6 | API/cause | `core/provenance_pair.go`, `provenance.go`, `trusted_runtime.go` | 已修复。严格 JSON/token/decode、可信执行输出、commit/journal/recovery cause 均保留；可信来源校验边界未放宽。 | `TestCoreOriginal_ProvenanceParserCausePreserved` 及 core trusted tests |
| 7 | API/cause | `panel/archive/targz.go`, `archive/zip.go` | 已修复。gzip/tar/zip parser、entry open/read/copy cause 保留，公开 data failure 不泄露原文。 | `TestArchiveOriginal_ParserCausesArePreserved` |
| 8 | API/cause | `panel/release/github.go`: `getJSON` | 已修复。transport/read/decode 记录 raw URL、phase；decode 与失败状态保留有界 body。 | `TestGitHubDiagnostics_*` |
| 9 | API/cause | `panel/active.go`, `credential.go` | 已修复。JSON、hex、Write/Close cause 保留。 | panel package tests |
| 10 | API/cause | `subscription/document.go`, `catalog.go`, `provider_journal.go`, `resource_activation.go` | 已修复。YAML/JSON/token/decode/URL/duration parser cause 保留。 | `TestSubscription_ParsingErrorKeepsCause`, `TestJournalDiagnostics_ParserCausesArePreserved` |
| 11 | API/cause | `update/channel.go`, `self.go`, `official.go`, `version_probe.go` | 已修复。文件 IO、release JSON、checksum/binary transfer、子进程输出与底层 cause 保留。 | `TestUpdateChannel_IOCauseRetained`, `TestUpdateHTTP_*`, `TestUpdateDownload_*`, `TestVersionProbe_*` |
| 12 | cleanup/Close | `config/atomic.go`: temp Close/remove、directory sync | 已修复。主失败与清理失败聚合；提交后的 sync 仍作为 warning。 | `TestAtomicWrite_ReplaceAndCleanupFailuresKeepBothCauses`, `...DirectorySyncFailureAfterReplacement` |
| 13 | cleanup/Close | `config/settings.go`: creation lock Close/remove | 已修复。失败路径聚合，已提交路径返回 `CommitResult.Warning`。 | `TestLoadOrCreateOutcome_LockCleanupFailuresBecomeWarning` |
| 14 | cleanup/Close | `core/install.go`: staging/archive/candidate cleanup | 已修复。主失败聚合；成功结果后的 candidate cleanup 通过现有 reporter 记 WARN。 | core installer tests 与 original diagnostics tests |
| 15 | cleanup/Close | `core/provenance.go`: trusted candidate acquire/inspect/apply cleanup | 已修复。每个实际清理失败由 trusted execution owner 记 WARN。 | core trusted tests |
| 16 | cleanup/Close | `core/trusted_runtime.go`: commit/journal/recovery cleanup | 已修复。未恢复失败保留完整 cause；恢复成功的失败以 WARN 记录。 | core trusted transaction tests |
| 17 | cleanup/Close | `core/replace_windows.go`: restore/stale backup | 已修复。restore 失败与主错误聚合；成功替换后的 stale backup 删除失败返回 warning，由 `Candidate.Commit` 记录。 | `TestReplaceBinaryWithOps_ReturnsPostCommitCleanupWarning` |
| 18 | cleanup/Close | `geoip/downloader.go`: HTTP Close、candidate Close/remove | 已修复。失败聚合；成功 response/candidate cleanup 用现有 reporter 记 WARN。 | `TestGeoIPHTTP_StatusBodyAndCloseCauseArePreserved`, `...SuccessCloseFailureIsWarning` |
| 19 | cleanup/Close | `geoip/service.go`: reader replacement/recovery/open verification | 已修复。reader Close 失败阻止提交或进入恢复聚合，避免悄然切换 owner。 | `TestGeoIPDiagnostic_ReaderCloseFailuresStopCommit`, `...CommitFailuresKeepReopenCauses` |
| 20 | cleanup/Close | `panel/archive/*`, `credential.go`, `install.go` | 已修复。archive/destination/candidate/credential 清理失败聚合或 WARN；成功事务语义不变。 | archive original diagnostics 与 panel diagnostics tests |
| 21 | fallback/retry | `core/trusted_runtime.go`: recovered commit/journal | 已修复。恢复成功保持成功/既有返回语义，同时由执行 owner 记 WARN。 | core trusted transaction tests |
| 22 | fallback/retry | `geoip/service.go`: initial database degraded open | 已修复。`NotExist`/预期 unavailable 跳过，其余实际降级原因 WARN。 | geoip service tests |
| 23 | fallback/retry | `panel/service.go`: active/list/build readiness/prune fallback | 已修复。继续使用安全 fallback，实际读盘/遍历/清理失败由 panel owner WARN。 | panel service tests |
| 24 | fallback/retry | `subscription/downloader.go`: auto-mode direct/proxy attempts | 已修复。失败重试 WARN，后续恢复 INFO，最终失败留给执行 owner 一次记录。 | `TestDownloader_AutoFallbackReportsAttemptAndRecovery` |
| 25 | fallback/retry | `update/replacement.go`, `version_probe.go`, `apply_prepared.go` | 已修复。版本探测失败不提升权限、不阻断既有 fallback；`SelfUpdater` owner 记 WARN。 | `TestVersionProbe_ObservesAndNeverElevates` |
| 26 | pre-redaction | core/geoip/panel/subscription/update HTTP clients | 已修复。日志私有链保留 raw URL、状态、最多 256 KiB body 和 cause；`APIError.Error()` 不含这些值。 | 各包 HTTP diagnostics tests |
| 27 | pre-redaction | core command/parser/filesystem、onboarding persistence warning | 已修复。原始命令输出、配置/parser/path 与 warning cause 只挂在私有错误链；公开消息稳定。 | core original diagnostics；`TestUpdate_PostCommitWarningPublishesAndReportsStableWarning` |
| 28 | reporter availability | `core.Installer`, `TrustedExecution` | 已修复。新增借用式 `Reporter` / `SetDiagnosticReporter`，nil 时跳过；app/runtime 已完成 legacy 与 trusted 装配验证。 | app/runtime 装配套件；core focused tests |
| 29 | reporter availability | `geoip.Downloader`, `ServiceOptions`, `Service` | 已修复。下载、reader 与 degraded fallback 共用现有 reporter，nil 时不创建日志。 | app/runtime 装配套件；geoip diagnostics tests |
| 30 | reporter availability | `panel.ServiceOptions`, release client 与 mutation cleanup | 已修复。service 向下载/发布/清理路径传播现有 reporter。 | app/runtime 装配套件；panel diagnostics tests |
| 31 | reporter availability | `subscription.Downloader`, service/scheduler ownership | 已确认并补齐。Downloader 使用注入 reporter；scheduler 等待同一次 refresh 结果，不重复记录最终失败。 | subscription downloader/service tests |
| 32 | reporter availability | `update.SelfUpdater`, `OfficialReleaseSource`, prepared apply | 已确认并补齐。response Close、版本 probe、替换后 cleanup 使用现有 owner reporter，nil 时跳过。 | app/runtime 装配套件；update diagnostics tests |

## 无独立候选的已审文件

下列文件也在首轮逐文件审计内，但没有形成独立候选；平台 companion 与数据模型文件的相关行为已并入上表对应组：

- config：`routing.go`、`settings_conflict_unix.go`、`settings_conflict_windows.go`、`atomic_unix.go`、`atomic_windows.go`。
- core：`provenance_darwin.go`、`provenance_linux.go`、`provenance_unix.go`、`replace_unix.go`、`trusted_runtime_unix.go`、`verified_command.go`、`verified_command_unix.go`。
- geoip：`failure.go`、`scheduler.go`、`sync_unix.go`、`sync_windows.go`。
- onboarding：`activation.go`。
- panel：`adapter.go`、`catalog.go`、`metacubexd/adapter.go`、`static_root.go`、`zashboard/adapter.go`。
- state：`coordinator.go`、`store.go`；冲突是预期分类，cause 已返回给 mutation owner。
- subscription：`generator.go`、`legacy_resources.go`、`model.go`、`provider_geo_catalog.go`、`provider_store_darwin.go`、`provider_store_linux.go`、`provider_store_unix.go`、`resource_state.go`、`scheduler.go`、`userinfo.go`。
- update：`installed_version.go`、`replacement_version.go`、`replace_unix.go`、`testdata/versionprobe/main.go`。

三个只读 Close 点保留现状：`config.Settings.Load`、subscription catalog load、`preferences.Service.Load` 已完成全部读取，Close 不能改变已返回数据，且这些底层 loader 没有持有日志 outlet。它们不创建 reporter，也不把成功读取改成失败。`preferences/service.go` 的读取、解析、写入错误均已带操作上下文返回给 owner。

## 行为不变量

- 没有改变控制协议 DTO、JSON envelope、CLI 退出码、状态结构或持久化格式。
- 没有新增依赖、网络测试、真实订阅、真实 mihomo 或系统服务操作。
- 普通 CLI 不会因这些包创建日志；所有 reporter 都是借用式注入，nil 时跳过。
- 预期拒绝/取消不提升为 ERROR；retry/recovered warning 使用 WARN，恢复完成使用 INFO，最终真实失败仍由同一次执行 owner 记录。
- `errors.Is` / `errors.As` 保留底层 cause；可信 core provenance 验证顺序和拒绝条件未改变。

## 验证结果

- 聚焦红绿测试覆盖 command output、HTTP transport/status/body/read/Close、archive/journal parser、reader ownership、版本探测 fallback、settings lock cleanup、onboarding post-commit warning，以及 Windows 替换后 cleanup warning。
- 收口后只运行一次组内组合套件：`go test ./internal/core ./internal/geoip ./internal/panel/... ./internal/subscription ./internal/config ./internal/state ./internal/onboarding ./internal/preferences ./internal/update`，全部通过。
- 所有已修改和新增的 owned Go 文件通过 `gofmt -l` 检查；owned 路径通过 `git diff --check`。
