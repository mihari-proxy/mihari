# 架构说明

本文档记录 mihari 的运行时架构与关键机制。对外行为见 [README](../README.zh-CN.md),完整命令见 [commands.md](commands.md)。

## 控制面

Mihari 围绕一个由守护进程持有的控制面(control plane)设计,由 CLI、TUI 和浏览器面板共享:

- CLI、TUI 和浏览器面板通过本地命名管道 / Unix 域套接字连接同一守护进程控制面。
- 控制 API 从不绑定 TCP 端口。
- 控制面经过认证:Unix 系统令牌位于 B/control.token，Windows/显式私有 P 保留单根 control.token。
- 守护进程可以安装、校验、托管、查询并重启 mihomo,同时将 Mihari 托管的 TCP 控制器保持在 loopback。
- 守护进程还负责订阅持久化、有界的自动刷新、校验过的配置生成、重载回滚与离线配置切换。
- 控制面新增只读端点 `GET /v1/service/status`,返回 mihari 自身的 OS 服务注册状态(`running`/`stopped`/`not_installed`/`unknown`);`GET /v1/core` 增加可选 `localReady`/`localVersion` 字段反映本地 core 就绪。两者均为向后兼容增量,不改变现有协议字段、onboarding `Complete` 契约或持久化格式。
- `/v1` 的 `CoreStatus`、`CoreInstallResult` 增加可选 `channel`;`MutationRequest` 增加可选 `channel` 以显式指定本次安装通道。均为向后兼容增量。
- `GET /v1/logging` 与 `PATCH /v1/logging` 是稳定的 v1 本地控制协议：前者返回完整 Logging 状态，后者在 revision 预检后更新级别、单文件大小或保留数量。它们供 TUI 使用，不增加 CLI 命令。
- Windows 私有日志的授权主体优先为数据根的个人 owner SID。owner 为 Administrators 时，从既有 DACL 的显式个人用户 full-control ACE 解析主体，再写入个人用户与 LocalSystem 的受保护 DACL，避免未提权用户失去读取权限；多个用户、deny ACE 或无法解析的授权主体均拒绝猜测。旧 ACL 已丢失个人授权时，仅成功以 WRITE_DAC 打开数据根的交互进程可补回自身 SID，LocalSystem 不猜测桌面用户。日志写入器持有自身序列锁后，修复三个固定日志序列的当前文件、归档和锁文件，通过 no-follow handle 核对文件 identity，不改内容；其他序列轮转导致 identity 变化时有界重试。SYSTEM 尚不能确定用户时仅保留受保护的旧 BA/SYSTEM 根权限，不回写根目录。子项创建及加固重新读取根策略，并在应用后复查，避免服务缓存或并发迁移覆盖个人授权。
- daemon 装配失败但控制通道可 listen 时驻留降级控制面,`GET /v1/status` 的 `health` 为 `degraded`,并带可省略 `last_error`。
- OS 服务 `Start` 等待控制通道 Ready;listen 失败则向 SCM 返回错误,不得保持假 running。
- 托管端口预检失败时 details 可含占用 PID 与进程基名;不自动杀进程。

## Settings 提交与降级

- Settings 的单文件 replace 是提交点：replace 前失败时磁盘仍为旧文件；replace 成功后的目录 sync 失败仅作为已提交后的 durability warning，上报诊断但不回滚、不把已生效的 mutation 报为失败。
- onboarding、系统代理或 TUN 等需要补偿的 mutation，若补偿写在提交点前失败，daemon 会按已经提交的磁盘状态收敛内存、推进 revision，并将 health 标为 `degraded`。只读请求仍可用；后续 mutation 返回 `invalid_state`，必须重启后重新加载并重试。
- 该 degraded 边界不新增事务文件或持久化 schema。旧版二进制以 `KnownFields(true)` 严格解码 `mihari.yaml`，不能读取非默认 `log:` 块；降级前须在 System → Logging 恢复 `info` / 10 MiB / 3 份文件以自动移除该块，或备份后手动删除 `log:`。

## 诊断错误链

- Phase 1 的 operation metadata 已用于 Phase 2 的 Logging 更新链路和 Phase 3 的主要业务 mutation。CLI/TUI 生成 ID，既有 `/v1` mutation DTO 携带 `operation_id`，本地控制客户端、控制服务器和 daemon 在各自的诊断 ctx 中绑定同一 ID 与静态 operation 名；没有增加 header 或持久化状态。一个实际 mutation 执行使用一个 ID；订阅 Add 后的立即拉取是独立子操作，批量 provider 更新的每个子操作也保留各自既有 ID。
- settings 保存失败保留稳定的公开 `data_failure` / `persist settings` 分类，同时在内部错误链保留 cause，供 `errors.Is`/`errors.As` 与 daemon 的受控诊断使用。诊断 logger 输出有界、脱敏的类型化摘要，不能把路径、凭据、完整 URL 或配置原文带入公开响应、状态或事件。
- 每次实际 mutation 执行是详细失败诊断的唯一 owner；同一 key 的缓存重放和并发等待者不会重复记录。控制服务器只为未被 owner 标记的意外失败补一条记录。对这条 settings 链路，已提交后的目录同步 warning 保持成功、revision 与内存发布，并在业务锁释放后以实际原因记录 WARN。
- 控制客户端为已接入的 mutation 记录 DEBUG 的开始、成功或已解析 daemon 响应；本地传输和响应解码失败按统一失败策略记录。daemon 的 `runtime.doOperation` 在实际执行完成后记录一次最终失败：含真实 cause 的失败为 ERROR，revision、参数、系统代理/TUN 冲突等预期拒绝为 DEBUG，只有纯 ctx 取消时不制造失败记录；订阅和核心的实际成功执行另记 INFO。未知 `errorString` 只输出保守类型摘要，不复制其不可信文本。

Phase 3 已完成下表中的请求链和自主 supervisor 边界；这里的“完成”只表示表内真实入口及已列故障点已有 cause、关联和 owner 回归，并不表示全仓所有错误都已覆盖。

| 已覆盖入口 | operation ID 来源与关联 | cause 产生点 | 最终 owner 与级别 | 保持的状态不变量 | 主要回归测试 |
| --- | --- | --- | --- | --- | --- |
| 订阅 Add/Refresh/Use/Enabled/Set/Remove | CLI/TUI 生成并经既有 DTO 传递；静态名为 `subscription.*`；Add 的立即拉取使用独立 `add-fetch` 子 ID | `subscription.Downloader` 的传输转换，以及 `Service` 的 304/cache 读取和原子写入转换 | `runtime.doOperation`；真实失败 ERROR、revision 冲突 DEBUG、实际成功 INFO；客户端阶段 DEBUG | fallback 次序、失败 Add 后的注册结果、cache/catalog、revision、`LastError` 与 stale commit 拒绝不变；重放不再次下载或记录 | `TestSubscriptionDiagnostic_FailedReplayAndNewIDKeepCausesIsolated`、`TestSubscriptionDiagnostic_AddFailedChildKeepsSafeLastErrorAndCause`、`TestSubscriptionMutationRoutes_BindOperationMetadata` |
| 核心 Install/Update/Restart | CLI、System/Setup TUI 的既有 DTO ID；`core.install` / `core.restart` | `core.Installer` 的下载、保存、校验、AIO 提示与二进制替换转换；runtime 的 Prepare/Commit/Restart/Maintain 返回链 | 显式请求由 `runtime.doOperation` 最终记录，真实失败 ERROR、成功 INFO；客户端阶段 DEBUG | channel、candidate identity、安装 revision、cleanup、trusted maintenance 路由和公开 API 分类不变 | `TestCoreDiagnostic_InstallFailureMatrixKeepsCauseAndSingleOwner`、`TestCoreDiagnostic_FailureJSONKeepsSafeCauseAndExecutionOperation`、`TestCoreMutationsBindDiagnosticOperationContext` |
| 核心自主监督 | 无请求 ID；`Supervisor.Run` 清除继承的 operation metadata | 自主启动、进程异常退出、第三次健康检查以及终止失败 | supervisor 生命周期 owner；真实失败 ERROR，纯健康检查取消不记录 | 显式 Restart/Maintain 错误仍返回 runtime owner；backoff、停止与进程句柄回收顺序不变；`CoreStatus.last_error` 仍是静态安全摘要 | `TestSupervisorDiagnostic_AutonomousStartFailureIsSafeAndUncorrelated`、`TestSupervisorDiagnostic_ThirdHealthCancellationDoesNotEmitFailure`、`TestSupervisorDiagnostic_ExplicitRestartFailureReturnsToOwnerWithoutReport` |
| 订阅配置生成、校验、发布、reload 与补偿 | 沿触发它的 `subscription.*` 执行 ctx；普通与 trusted 恢复分别保留原有 ctx 选择 | `prepareContent` 文件 IO、`ValidateConfig`，普通 `commitRuntimeConfig` 的首次 reload/restore/第二次 reload，trusted 提交的 capability/reload/restore，以及 `TrustedExecution.Publish` | `runtime.doOperation` 聚合并最终记录一次 ERROR；已提交的 settings durability warning 仍由同一批次在锁外记 WARN | 候选校验、原子替换、普通与 trusted 恢复顺序、generation/hash、cache/catalog/receipt、revision、degraded 状态和静态 `LastError` 不变 | `TestConfigDiagnostic_ReloadCompensation`、`TestConfigDiagnostic_CatalogRestoreKeepsBothCauses`、`TestConfigDiagnostic_TrustedPublicationKeepsRecoveryCauses` |
| 系统代理 Enable/Disable | CLI/System TUI 的既有 DTO ID；`system_proxy.enable` / `system_proxy.disable` | backend Get/Enable/Disable/readback 与 settings/live 补偿 | `runtime.doOperation`；真实 backend/补偿失败 ERROR，外部代理冲突和非 Mihari 所有的 Disable 为 DEBUG；客户端阶段 DEBUG | force、所有权门控、settings/live 恢复顺序、revision、degraded 与 `LastError` 不变 | `TestSystemProxyDiagnostic_BackendFailuresKeepCauseAndReplayJSON`、`TestSystemProxyDiagnostic_ForeignConflictIsDebug`、`TestSystemProxyDiagnostic_IPCOwnerDedup` |
| 普通与 trusted TUN Enable/Disable | CLI/System TUI 的既有 DTO ID；`tun.enable` / `tun.disable` | live snapshot/Configs、PATCH/permission/fallback 转换、应用确认和 settings/live/trusted 补偿 | `runtime.doOperation`；真实失败 ERROR、TUN 冲突 DEBUG；客户端阶段 DEBUG | detector 仍是 best effort；force、live 确认、配置字节、settings-first trusted 恢复、revision/degraded 和原 marker 不变 | `TestTunDiagnostic_RuntimeFailureMatrixJSON`、`TestTunDiagnostic_TrustedConfirmationAndRollbackJSON`、`TestTunDiagnostic_IPCOwnerDedup` |
| GeoIP Update | Setup TUI 的既有 DTO ID；`geoip.update`；没有新增 CLI mutation | `FileCandidate.Commit` 激活恢复，以及 `PreparedUpdate.Commit` 的 pair reopen/restore；runtime 原样接收 prepare/commit cause | `runtime.doOperation`；真实失败 ERROR、stale revision 冲突 DEBUG；客户端阶段 DEBUG | 主错误文本、pair 原子切换/恢复次序、generation、stale 拒绝、失败 candidate cleanup 和公开 `internal` envelope 不变 | `TestGeoIPDiagnostic_PairOpenFailureKeepsBothRestoreCauses`、`TestGeoIPDiagnostic_FileActivationFailureKeepsRestoreAttempt`、`TestGeoIPDiagnostic_IPCRawFailureKeepsInternalEnvelope` |
| Panel Install/Update/Activate/Rollback/Uninstall/Reinstall | CLI/Web GUI TUI 的既有 DTO ID；静态名为 `panel.*`；浏览器 Open 不在此链 | `panel.Service.download` 的请求创建、传输、响应读取/关闭转换；runtime 原样接收 prepare/commit cause | `runtime.doOperation` 的实际执行 owner 记录 ERROR；客户端阶段 DEBUG | identity/revision、staging cleanup、active/previous build 和公开 API 分类不变 | `TestPanelDiagnostic_OperationsKeepOwnerCauseAndReplayJSON`、`TestPanelDiagnostic_IPCOwnerDedup`、`TestPanelDiagnostic_TUIMetadata` |
| 原生 rule provider Refresh（单个/批量） | Rules TUI 每个既有 mutation ID；`rule_provider.refresh`，批量子项不共用结果元数据 | `mihomo.Client.do` 在该实际调用链可达的请求创建、传输和响应读取转换 | `runtime.doOperation` 的实际执行 owner 记录 ERROR；客户端阶段 DEBUG | 原生 PUT method/escaped path、批量子项身份、HTTP status code/message/details 和响应关闭不变 | `TestProviderDiagnostic_AdapterKeepsCause`、`TestProviderDiagnostic_RealAdapterOwnerJSONAndReplay`、`TestProviderDiagnostic_IPCActualAdapterOwnerDedup` |

下列限制仍然成立：普通 CLI 没有持久化文件 logger，非流式 JSON 模式维持单一安全 envelope；只读请求没有 mutation operation ID，认证前失败也不会借用尚未解析的 ID。共享运行中的 mihomo stdout/stderr 不附加当前请求 ID，不能按 operation ID 精确归因。Panel `PreparedMutation.Cleanup` 的既有接口没有错误返回，因此该 cleanup 内部失败仍不可观察。logger 建立前只有 Unix 显式 system-service/launchd 或 Windows SCM 的非交互 daemon owner 可使用注入的安全 stderr 摘要；日志资源写入或关闭失败继续使用独立的非 JSON fallback，不能递归写入失效 logger。

Phase 4 完成了下表所列后台调度、Web gateway、CLI/TUI 本地任务、stream、关闭和 logger 故障入口的人工审计。表中的“覆盖”只表示列出的入口和断言已有明确 owner 或已如实登记可见性边界；它不表示每一种可能的 error、所有 cleanup 或所有平台原生路径都能产生诊断。

| 已审计入口 | 失败源与处理策略 | 最终 owner 与级别 | operation ID 来源或缺失原因 | 脱敏方式 | 具体回归断言 | 剩余限制 |
| --- | --- | --- | --- | --- | --- | --- |
| 订阅与 GeoIP scheduler 的单次刷新 | 每次回调在进入 Manager 前绑定新 ctx；下载、prepare/commit 失败沿既有 mutation 返回，重试/下轮检查仍由原 scheduler 决定 | 实际 mutation 仍由 `runtime.doOperation` 单点记录：真实失败 ERROR；订阅成功 INFO，GeoIP 不新增成功 summary；已有 marker 阻止 loop 重复记录 | 保留既有时间戳 ID：`scheduler-<profile>-<UTC>` / `scheduler-geoip-<UTC>`；同一次 auto fallback 共用该次 ID，每次重试生成新 ID | 继续使用类型化、有界 cause 摘要；订阅 URL、token 和路径不进入 JSON | `TestSchedulerSubscriptionRefresh_RetryUsesFreshContextAndExecution` 断言 entry/backend ID、失败/成功事件、marker/cause、cache 提交和秘密缺失，但不直接检查 level；`TestSchedulerSubscriptionRefresh_FallbackKeepsAttemptID` 断言一次 clock 读取及 entry/direct/record 共用 ID；`TestSchedulerGeoIPRefresh_FailureStillChecksNextRound` 断言失败后继续、下一次新 ID、candidate commit/cleanup 和单条失败。Phase 3 已列的 `TestSubscriptionDiagnostic_FailedReplayAndNewIDKeepCausesIsolated` 与 `TestGeoIPDiagnostic_IPCRawFailureKeepsInternalEnvelope` 断言同一 runtime owner 的失败 ERROR | scheduler 成功 INFO 未由本行 focused 测试直接断言；时间戳 ID 保留既有碰撞性质；未新增全局 ID 服务 |
| subscription/GeoIP scheduler loop 的终止结果 | 两个固定 runner 各自在返回且 cleanup 完成后分类；纯 owner 取消静默，joined 真实故障保留；一个 loop 失败不取消另一个，父取消后 join 两者 | `runSchedulers` 是 terminal owner，`background.failed` ERROR；Reporter 与 legacy callback 二选一；已报告结果静默 | loop 生命周期故障没有逻辑 mutation，不制造 operation ID | Reporter 使用安全 cause；legacy 仍只收到原错误对象和静态 component | `TestRunSchedulers_ReportsLoopFailureAndJoinsBoth` 断言报告后仍等待父取消及 sibling cleanup；`TestRunSchedulers_FailureClassificationAndLegacy` 断言取消/超时/join/marker分类与出口互斥；`TestRunSchedulers_ShutdownJoinsBothFailuresAndCleanup` 断言两次 cleanup 后两条无 ID ERROR | scheduler 现有 `Run` 通常只返回 nil/取消；没有为制造错误改变其返回语义 |
| Manager 持有的 Web gateway、scheduler 外壳与 core recovery 后台结果 | 使用实际生命周期 ctx 分类；纯 shutdown 取消静默，live-context 上游取消/超时和 joined 真实故障可见，已有 mutation marker 去重 | `Manager.reportBackgroundContext`，`background.failed` ERROR；新 Reporter 优先，legacy 只作替代 | 这些是组件生命周期结果；core recovery 明确清除/不创建 operation metadata | 类型化摘要，不输出路径或 token | `TestManagerBackgroundDiagnostic_ReporterOwnsSchedulerFailure` 断言级别、cause、ctx 和安全 JSON；`TestManagerBackgroundDiagnostic_RealMutationMarkerPreventsSecondReport` 断言真实 mutation 不重复；`TestManagerBackgroundDiagnostic_CoreRecoveryHasNoInventedOperation` 断言无伪造 ID；既有 `TestManagerReportsWebGatewayError` / `TestManagerIgnoresWebGatewayCancellation` 分别断言 Web gateway component/cause 与 shutdown 取消静默 | 这里只负责最终后台结果，不替代实际 mutation owner |
| Web 的 proxy select、connection close/close-all、config patch 与 TUN mutation | Web adapter 在调用 Manager 前绑定 ID；Manager marker 优先；未标记结果由 adapter 收口，server 仅兜底未标记意外错误 | `webMutator.reportResult`：成功 `mutation.succeeded` INFO，预期拒绝 DEBUG，真实失败 `mutation.failed` ERROR；server fallback 沿相同失败分类 | select/close/config 使用既有 UTC 纳秒 ID；TUN 使用既有 16-byte random hex，失败时退回时间戳；静态 operation 名由代码提供 | safe Reporter 只保留类型化原因；浏览器凭据、controller secret/address、query/body 和对象名不输出 | `TestWebMutatorDiagnostics_ContextOutcomeAndID` 覆盖五种操作的真实 Manager 入参、ID、成功/拒绝/失败/取消和单条 JSON；`TestWebMutatorDiagnostics_TUNRandomFallback` 直接断言 TUN random-source 失败后的时间戳 ID；`TestWebMutatorDiagnostics_RealManagerOwnsFailure` 与 `TestWebGatewayDiagnostics_RealManagerFailureIsReportedOnce` 断言真实 owner 去重及原 HTTP 400 envelope | ID 只在本地 Web→Manager 链使用，没有增加 header/DTO；缓存重放语义未变 |
| Web mutation 的解析、shape、allowlist 与缺少 mutator 拒绝 | 解析错误保留错误对象供安全摘要；shape/allowlist 拒绝不附带动态输入；既有 400/403/502 响应不变 | gateway `mutation.rejected` DEBUG；调用返回未标记意外错误时 `mutation.failed` 按 FailureLevel | 拒绝发生在 adapter 创建 mutation ID 前，gateway 不伪造 ID | 静态事件和安全 parser cause；未知字段、正文、group/node/connection 值不进入日志 | `TestGatewayMutationDiagnostics_RejectionsAndFallback` 逐个断言 malformed/empty/managed/unknown/shape/missing-mutator 的状态码、级别、记录数、无 ID 与敏感值缺失 | 认证前和尚未解析的请求无法取得 mutation ID；没有为诊断额外解析 body |
| Controller HTTP reverse proxy | 请求构造或 transport 失败由既有 ErrorHandler 返回原 502，同时产生安全诊断；认证剥离、controller credential 注入和响应 header 隔离不变 | proxy owner `proxy.failed`，真实失败 ERROR；实际 ctx 取消静默 | 不创建请求 ID；若调用者本地 ctx 已有合法 metadata，只在该进程日志中保留 | 类型化 transport cause；上游 URL、controller secret、请求 query/body/headers 不输出 | `TestControllerProxyDiagnostics_SafeFailureAndOriginalContext` 断言原 502、permission cause、ctx 和秘密缺失；`TestControllerProxyDiagnostics_PreservesLocalMetadata` 断言 metadata 只作本地关联 | 普通只读代理请求无跨 IPC operation ID |
| WebSocket handshake 与双向 relay | controller 地址解析、upstream dial、browser accept 各自在失败处收口；relay 首个结果触发原取消/peer close，等待两个方向结束后再按方向与 close provenance 选唯一实际故障 | handshake owner `websocket.handshake.failed` ERROR；post-join owner `websocket.relay.failed` ERROR；normal/going-away close 与实际 ctx 取消静默 | WebSocket 路径无既有 mutation DTO，不新建跨 IPC ID | safe cause/Close 状态摘要；地址、credential、payload 和 close reason 原文不输出 | `TestGatewayWebSocketHandshakeFailuresAreSanitized` / `TestGatewayWebSocketAcceptFailureDiagnostic` 断言三类握手失败安全且单点；`TestGatewayWebSocketCloseReadArrivesAfterInducedWrite` 断言并发 graceful close 静默、异常 close 只记真实状态且两 relay 已 join；`TestWebSocketRelayFailure_PreservesIndependentFaults` 断言 joined 独立故障不被关闭噪声隐藏；`TestGatewayWebSocketReadLimitKeepsSingleFailureOwner` 断言 32 KiB gateway read bound、relay join、upstream cleanup 和单 owner | 没有为 stream 增加协议关联字段 |
| 本地控制客户端 `Stream` | focused 覆盖的 transport、HTTP/WebSocket handshake、read/size/decode 和异常关闭保留可用 cause；合法 daemon error envelope 与本地失败分开；callback 错误原样返回且不误归为 transport。地址校验和 token 获取也进入同一实现 owner，但没有对应 focused cause/级别断言 | `control.client`：本地 `stream_failed` 按 FailureLevel，合法远端 `stream_response` DEBUG；新报告结果加 marker，已有 marker 去重；正常关闭/实际取消静默 | 可读取调用者本地 ctx metadata，但 stream 请求不新增 query/header/DTO，因此 daemon 与客户端之间没有统一 ID | 复用安全 wrapper/Reporter；不输出 endpoint、token、原 payload 或未知文本 | `TestStreamDiagnostics_TransportCauseAndReportedOwnership`、`TestStreamDiagnostics_RemoteEnvelopeAndInvalidHandshake`、`TestStreamDiagnostics_ReadDecodeAndBounds` 断言其覆盖分支的 cause/API 分类、级别、单 owner、边界与秘密缺失；`TestStreamDiagnostics_CallbackAndNormalTerminationStayQuiet` 断言 callback identity/count、正常/取消静默和 nil Reporter；`TestStreamDiagnostics_CancellationDeadlineAndNilReporter` 断言 canceled dial、live deadline、nil Reporter 与 pre-reported ownership；`TestStreamDiagnostics_NativeTransportKeepsMetadataLocal` 断言原生 IPC 请求未携带 metadata 且连接完成清理 | 非取消 token 获取失败和无效 base address 的 cause/级别目前只有实现检查，没有 focused diagnostic assertion；`requestToken` 已抹去的底层 cause 无法恢复；`CloseNow`/body close 无新 error 接口，只验证功能清理 |
| TUI stream generation、重连与恢复 | session 消费本代结果后先作本地诊断，再以 fresh Status 决定公开重连错误；正常 generation 结束按现有 reopen 处理；首次健康信号恢复 backoff；终止路径取消并 drain 活跃 generation | session 对未标记故障记 ERROR，正常 `streams_ended` DEBUG，首次 `streams_recovered` INFO；client 已标记故障不重复 | session 不创建跨 IPC ID；仅保留传入的进程内 ctx metadata（如有） | TUI Reporter 使用同一 redactor；公开结果仍由 fresh Status 的安全分类决定 | `TestSessionDiagnostics_FailureOwnershipAndFreshStatus` 断言 fresh Status 优先、marker/callback/normal-end ownership；`TestSessionDiagnostics_BackoffRecoveryAndJoinedReopen` 断言 backoff reset 和一次 INFO；`TestSessionDiagnostics_TerminalPathsJoinActiveProducers` / `TestSessionDiagnostics_CloseJoinsRealClientReports` 断言终止发布和 logger close 前 join | 每代仍使用既有 first-result 策略，不聚合所有 callback 故障；依赖注入的 Stream/Reporter 最终返回 |
| CLI stream 命令 | 沿用 `control/client.Stream` owner；CLI callback/output writer 错误保留原退出码和 renderer，不当作传输故障 | 有显式注入 Reporter 时由 client 记录；CLI 展示层不重复 ERROR | 没有新增 CLI stream operation ID | text 与单一 JSON envelope 继续使用既有安全渲染；诊断不混入 stdout/stderr 协议输出 | `TestStreamCLI_DiagnosticInjectionPreservesOutputContracts` 断言 text、非 follow/follow JSON 字节、事件顺序、writer failure exit/envelope 和零误报 | 普通 CLI 不创建持久化日志；无注入 Reporter 时没有额外诊断输出 |
| TUI installation inspect/plan 与 self-update prepare worker | 每个实际 worker 创建/复用值型 metadata，错误在 worker Done 和资源 join 前、mutex 外报告；异步 result 携带自己的 metadata，迟到结果不借用当前 UI task | TUI `installation.inspect.failed` / `.plan.failed` / `self.prepare.failed` 按 FailureLevel；真实失败 ERROR、实际取消静默、已有 marker 去重 | 每个实际任务取得独立本地 ID；生成失败只记静态 WARN 并以空 ID 继续执行。`Context` 的实现也保留同任务的现有 metadata | 安全 Reporter；ID 生成错误只输出固定 `diagnostic identity unavailable`，不输出原错误 | `TestLocalTaskDiagnostics_InstallationOwnersBindDistinctOperations` / `TestLocalTaskDiagnostics_InstallationConcurrentFailureIDs` 断言独立 actual ctx/并发 ID；`TestLocalTaskDiagnostics_InstallationFailureOnceAndCancellation` 断言 ERROR、取消、nil/unavailable Reporter 与 marker 分类；`TestLocalTaskDiagnostics_InstallationIDFailureStillExecutes` 断言安全 ID 失败且任务继续；`TestLocalTaskDiagnostics_InstallationAsyncResultsCarryOwnValues` / `TestLocalTaskDiagnostics_InstallationPlanResultCarriesOperation` 断言异步结果携带自己的值；`TestLocalTaskDiagnostics_PreparedUpdateFailure` 覆盖 self prepare；`TestLocalTaskDiagnostics_InstallationJoinIncludesReporterOutsideLock` 与 `TestRun_LocalInstallationFailureUsesOwnedReporterBeforeClose` 断言 reporter 在锁外且 join/logger close 前完成 | 没有 focused 测试把“已存在但 ID 为空”的 metadata 传入 worker；该保留行为只是实现检查。prepared candidate/worker 的 void cleanup 只能验证 join/清理结果，不能推断不存在内部失败 |
| TUI logging apply | coalescer 每次真正调用 void `Apply` 时创建 `logging.apply` ctx；不为被合并请求制造身份或成功/失败事件 | `localLogging.Apply` 没有 error 返回，任务层无结果级别；logger 资源故障仍归 `FailureReporter` 的独立 outlet | 每次 actual Apply 生成本地 ID；不复用 lifecycle operation | FailureReporter 使用固定安全摘要；任务 ctx 不进入该 outlet | `TestLocalTaskDiagnostics_ApplierBindsActualExecution` / `TestLocalTaskDiagnostics_ApplierDoesNotReuseLifecycleOperation` 断言 actual call 的新 metadata；`TestLocalTaskDiagnostics_ApplierIDFailureAndVoidVisibility` 断言 ID 失败不阻止 Apply 且没有虚构结果记录 | void `Apply` 无法证明成功或暴露 task-correlated failure；FailureReporter 没有 operation ctx |
| TUI 日志导出 | worker 捕获最终 error、panic 与第一个 warning；预期单 cause rejection 做有界透明 unwrap，joined IO/未知原因保守为真实失败；报告后再取消/join | `logs.export.rejected` DEBUG；`logs.export.warning` WARN；`logs.export.failed` 按 FailureLevel（真实失败 ERROR）；已有 marker 去重 | 每次实际导出创建本地 ID；生成失败静态 WARN 后无 ID 继续 | 不记录输出路径、归档内容或 warning 原文；Reporter 使用安全 cause | `TestLocalTaskDiagnostics_ExportBindsActualExecution` / `TestLocalTaskDiagnostics_ExportResultsHaveIndependentValues` 断言实际执行 ID 与独立结果值；`TestLocalTaskDiagnostics_ExportExpectedOutcomesAreNotErrors` / `TestLocalTaskDiagnostics_NativeExportRejectionsRemainDebug` 断言 bare 与真实 exporter rejection 为 DEBUG；`TestLocalTaskDiagnostics_ExportWrapperClassificationPreservesRealCauses` 断言单 cause、joined/depth/cycle/marker；`TestLocalTaskDiagnostics_ExportFailureWarningAndCancellation` 断言 WARN/ERROR/取消/秘密缺失和单条记录；`TestLocalTaskDiagnostics_ExportWithoutReporterDoesNotInspectError` / `TestLocalTaskDiagnostics_ExportWithoutReporterPreservesTypedNilError` 断言 nil Reporter 不遍历 opaque/typed-nil error 且仍完成 cleanup/join | 启用 Reporter 后，通用 marker 对合成 typed-nil receiver 的既有风险仍待最终独立评审，不在 Phase 4 修改通用 helper |
| TUI cleanup 后的 installation execute 与 prepared self apply | 旧 TUI 先关闭 worker、session、applier、logger、FS 并恢复终端，随后执行；只绑定值型 metadata，不延长已关闭 logger 生命周期。nil Reporter 是 `finishInstallationRun` / `finishPreparedRun` 的实现边界 | task helper 的 Reporter 明确为 nil；失败仍由既有同步返回、固定 warning 和外层 renderer 负责，没有持久日志级别 | cleanup 后分别绑定 `installation.execute` / `self.apply` 的本地 ID，供 executor ctx 使用 | 固定 warning 不包含原 cause；公开 partial-success/错误渲染保持安全 | `TestLocalTaskDiagnostics_LateTasksBindAfterCleanup` 只断言 cleanup 后执行、metadata 和原 error identity，不直接观察 Reporter；`TestPreparedUpdate_CleanupBeforeApplyAndRelaunch` 断言 cleanup→Apply/relaunch 顺序、原 cause 返回和固定安全 warning | cleanup 后没有持久诊断 outlet；nil Reporter 来自实现检查而非本行测试的直接可观察断言，不能声称失败已写入文件 |
| CLI service action/status/apply、installation inspect/plan/execute 与 self update | 每个同步本地 owner 在调用前绑定 ctx，在既有分类/渲染前报告最终失败；self update 的 prepare/apply/cleanup cause 只在 owner 的局部变量聚合 | `cli` 的 `service.*.failed`、`service.apply.failed`、`installation.*.failed`、`self.update.failed` 按 FailureLevel；取消静默、预期拒绝 DEBUG、真实失败 ERROR | 复用既有 `Dependencies.NewOperationID`；每个实际任务一个 ID，生成失败静态 WARN 后继续；self prepare/apply 共用一次 `self.update` ID | DiagnosticReporter 只输出安全摘要；CLI stdout/stderr、单一 JSON envelope 和退出码保持原样 | `TestLocalTaskDiagnostics_ServiceActionBindsOperation`、`TestLocalTaskDiagnostics_ServiceApplyBindsAndReports`、`TestLocalTaskDiagnostics_CLILocalOwnersPreserveJSONAndExit` 断言实际 ctx、单条诊断以及注入前后 stdout/stderr/exit 完全一致；`TestLocalTaskDiagnostics_ServiceBorrowsReporterWithoutChangingOutput` 断言取消静默、nil Reporter、安全 ID 失败与成功 JSON 不变；`TestLocalTaskDiagnostics_LegacyServiceOwnersReport` 覆盖 legacy controller | 默认依赖没有持久 Reporter；普通 CLI 因而仍只有既有用户输出，不产生文件日志 |
| daemon logger 写入/Apply/Close 与进程关闭 | rotator 的 lock/write/unlock/void Apply 失败走独立、按类别限速的 `FailureReporter`；runtime Close join 文件与 lock cause；daemon 汇总 stdout/stderr/mihomo/daemon/FS 的全部 close 后一次报告，并保留原 run/open error | 非 JSON 独立 outlet 的 `logging: <kind>:` 固定记录；没有 slog 级别或 operation ID；失败 outlet 只尝试一次，不递归 | logger 自身故障不属于业务操作；启动早期和 routine shutdown 都不伪造 ID | 已知 secret 注册后脱敏，早期 cleanup 用固定摘要；不输出路径、CR 或 raw cause | `TestRotatingWriter_FailuresKeepCountsAndSafeFallback` 断言 n/error/dropped/config、限速单行和秘密缺失；`TestRuntime_CloseJoinsFileAndLockErrorsWithoutClosingSharedFS` 断言 cause join/幂等/FS ownership；`TestRunDaemonWith_CloseFailureUsesIndependentSafeOutlet` 覆盖五个 closer、partial/early/nil/failed outlet、顺序、单次与安全文本 | nil outlet 可静默；失败 outlet 也可能不可见；同类别一秒窗口可抑制后续记录；void Apply 只能由资源 reporter 表示类别，不能绑定 task cause |
| TUI logger bootstrap/cleanup 与 Run 资源关闭 | bootstrap 和 cleanup 使用独立固定 warning；cleanup 先 cancel/session，再 join exporter/applier，最后关闭 logger/FS，全部完成后报告；重复 cleanup 返回缓存的 joined error | 非 JSON `Warning: TUI file logging ... failed`，无 slog 级别/operation ID；按 bootstrap/cleanup 类别限速 | 资源生命周期事件不对应用户 task，不创建 ID | 固定文本且经既有 redactor；写入失败不递归 | `TestRunFactoryClosesPartialResourcesLogging` 断言真实 Run open failure 的固定 bootstrap warning 与 partial FS cleanup；`TestTUILoggingBootstrapFailureIsRateLimited` 断言 bootstrap 类别窗口；`TestRunCleanup_CloseFailureReportsAfterAllResourcesOnce` 断言 join cause、顺序、重复调用和关闭后单条固定 cleanup warning；`TestTUILoggingFailureReporter_OutputFailureDoesNotRecurse` 断言每类一次及窗口恢复；`TestRunCleanup_NetworkTerminateDoesNotDropDiskWorkers` 断言 disk worker 先于日志/FS 释放 | void worker/session/applier close 接口不能产生新增 returned error；nil/failing warning outlet 可静默 |
| 控制服务器 shutdown 与 hijacked stream join | 保留既有 Shutdown timeout 后 force close、snapshot/handler join 和 `ErrServerClosed` 过滤；正常 shutdown 返回 nil，不增加第二个诊断 owner | 没有为预期 shutdown deadline 新增 ERROR；非预期 `Serve` 返回沿 daemon 外层返回/渲染边界传播，不增加专用诊断事件 | 生命周期关闭没有 operation ID | 不新增错误输出；daemon 外层若报告仍走安全 Reporter | `TestServeContextCancelToleratesShutdownTimeout` 直接断言注入 Shutdown timeout 后返回 nil；`TestServe_ForceClosesBeforeJoiningSnapshotWorker` 断言 force close 后 join；`TestServe_JoinsHijackedStreamBeforeReturning` 断言 runtime stream 结束后才返回且原返回为 nil；原生 IPC lifecycle 集成覆盖 stop/delete/start | 该接口未增加各 closer 的错误诊断；测试证明已知 join 结果，不能据此推断所有无 error cleanup 不会失败 |

Phase 4 保留几条明确边界：认证前、预解析和只读请求没有统一跨 IPC ID；stream 的本地 metadata 不通过新增 header/DTO 传输；普通 CLI 没有持久化文件 logger；共享 mihomo stdout/stderr 不附加请求 ID。TUI logging `Apply`、部分 worker/cleanup 接口没有 error 返回，只能验证调用、顺序和可观察清理结果，不能推断内部从未失败。cleanup 后执行的 installation/self-update 使用 nil Reporter，只保留原同步错误或固定 warning。启用 Reporter 时，合成 typed-nil error receiver 进入通用 marker 遍历的既有风险留待最终独立评审；本阶段未修改 generic helper，也不据此承诺支持所有任意 error 实现。#197 的 Setup 提示仍不在本设计范围。

## 核心安装

守护进程通过同一条下载、校验、替换链路安装 mihomo,并支持 `stable` 与 `alpha` 两个通道:

- `stable` 从 GitHub `/releases/latest` 取当前稳定版;`alpha` 从固定滚动 tag `Prerelease-Alpha` 取预览版。
- 展示与协议里的 Version 永远来自 `ParseVersion(mihomo -v)`:稳定版为 semver(如 `v1.19.x`),alpha 为 `alpha-{sha}`(实测 `-v` 形如 `Mihomo Meta alpha-dd7bc4c ...`,不是 `v1.19.x`)。GitHub tag `Prerelease-Alpha` 从不作为版本显示或写入 Version。
- 本次安装意图随请求进入安装链路;`settings.core-channel` 只在 Commit 成功后写入,表示上次成功安装的通道。切换失败或未提交时,持久化通道与界面仍为旧值。
- all-in-one 包在 `data/bin/core-channel` 写入 sidecar(第 1 行通道,第 2 行 stamp)。安装器覆盖 sidecar,不改 `mihari.yaml`;守护进程仅在 stamp 变化时把打包通道写入 settings,避免旧 sidecar 覆盖用户后来在 System 页切换的通道。

## TUI

- TUI 只通过 `internal/control/client` 经原生 IPC 控制面与本地守护进程通信。它从不打开 mihomo 控制器、从不接收控制器密钥。
- TUI 的日志直接写入例外是经 `internal/logging` 在当前 UID 的 U/logs 追加/轮转固定 `mihari-tui.log*`（Windows/显式私有 P 保留单根）;不得写 `mihari.yaml`、订阅、token、面板或其他业务状态。日志配置变更仍只走 daemon 控制面。
- System 页面 Logging 下方的 Maintenance 区提供 **Completely Uninstall Mihari**。确认框默认 Cancel；确认后先关闭 TUI 资源，再由已提权的本地卸载路径停止并注销 OS 服务，然后删除预检通过的受管文件。CLI 对应入口是 `mihari service uninstall --purge --yes`。普通 `service uninstall` 仍只注销服务。
- 搜索与表单字段中的括号粘贴和 Ctrl+V 使用纯 Go 实现的 `github.com/atotto/clipboard` 辅助库;Mihari 本身从不把密钥写入剪贴板。
- 页面:独立的首次运行 Setup 路由、Overview、可展开的 Proxies、带本地 GeoIP 详情的活动/已关闭 Connections、Rules/Providers、有界的结构化 Logs 流、订阅管理表单、分类的 System 页面,以及驱动面板安装/更新/激活/打开/回滚的 Web GUI 页面(在守护进程通告 `web-gui` 能力之后)。
- Setup 安装核心、可添加初始订阅、准备本地 GeoIP 数据,并请求守护进程持久化校验过的本地端点。
- Setup 第一步用短连接 `net.Listen` 预检三个托管端口的可用性:占用端口标红(Danger)并提供一键自动切换到下一个可用端口(从 `port+1` 起搜索,上限 `+1024`,三端口保持互异);权限等未知错误不标红、不阻塞,仍由守护进程启动时兜底校验。预检以 generation 守卫拒绝迟到的探测结果。
- 进入 core / GeoIP 步骤时,Setup 经只读 `GET /v1/core`、`GET /v1/geoip/status` 探测本地资源就绪:已就绪显示版本并提示「将直接使用、无需下载」,失败回退静态文案且绝不阻塞流程。
- Setup 审查页汇总端口(改端口且守护进程报告需重启时标注「需重启生效」)/ core 来源与版本(本地已有/新装/安装失败)/ 订阅 / GeoIP / mihari 服务注册状态(经 `GET /v1/service/status` 拉取);跳过项如实标注。各步结果在命令闭包内回写 Model,依赖 Bubble Tea 的 cmd→channel→Update happens-before 保证。
- System 页面通过与 `mihari service` 相同的本地服务适配器管理 OS 服务(安装/卸载/启动/停止/重启/状态);这些操作要求进程已经提权,且不经过守护进程控制协议。当守护进程通告相应能力时,System 页面显示实时的系统代理与 TUN 状态,并通过本地控制 API 切换它们(开启外部代理或其他 TUN / mihomo 实例需要强制确认;Mihari 从不清除其他产品的代理)。
- System 页面的 Ports Config 可修改 Mixed / Controller / Web 端口;占用按本实例 PID 显示 `Owned`,或 `Occupied by name (pid)` / `Available`。写入复用 onboarding 更新,应用后通常 `RestartRequired`。没有对应 CLI。
- System 页面的 Logging 区可修改 daemon-owned 的 level、最大文件大小与保留数量；更新经稳定的 `/v1/logging` 控制协议热应用，不需要 daemon restart。Logs 页的 `e` 与 System → Logging 的 **Export logs** 打开同一个本地导出对话框；导出不增加 CLI 命令；Unix 系统模式使用可选的 machine-log-snapshot-v1 控制协议。
- System 页面还在进入时以只读方式检查 Mihari 的最新 GitHub Release,并用 `当前版本 · 最新版本 available`、`当前版本 · Up to date` 或 `ahead of <channel> <latest>` 展示结果。实际更新先准备固定候选，再按真实目标版本确认；降级及未知兼容性显示完整风险。确认后，本地 updater 在控制协议之外复核候选、目标和服务定义，替换 Mihari 可执行文件并尝试同步已安装服务；该写操作要求 TUI 进程已经具备管理员/root 权限,不会自动触发 UAC 或 sudo。旧 Bubble Tea 程序先关闭工作、IPC、日志与文件所有者并恢复终端，再提交和进入新 TUI。取消与迟到准备结果由 Run 所有者清理。Unix 复用安装锁，Windows 在主程序替换、服务停止及服务副本复制边界复核；后续同步失败保留主程序已更新的部分成功状态。预览只在本次调用中存在，跨 Unix helper 调用用不透明 preview_id 绑定，不改变 daemon /v1、安装请求或 journal 格式。
- Mihari 应用通道 `main`/`dev` 与 mihomo Core 通道 `stable`/`alpha` 分开：应用通道写在 Unix B/P 的 `mihari-channel` sidecar（Windows 为旧数据根），不进 `mihari.yaml` / `/v1`；AIO `--channel` 只写该 sidecar；CLI/TUI 自更新仍走 GitHub Releases。
- System 页面的 `Core Channel` 行可在 `stable` / `alpha` 之间切换;切换后由守护进程按新通道重装核心。版本行显示 `ParseVersion(mihomo -v)` 的身份 token,从不显示 `Prerelease-Alpha`。
- 规则顺序从不排序;onboarding、系统、provider、订阅、面板和浏览器变更都经由守护进程变更协调器,破坏性或大范围操作需要确认。

## Web 网关

- 守护进程在 `web-addr`(默认 `127.0.0.1:9191`)上启动回环 Web 网关。
- 浏览器认证使用存储在数据根目录下的专用 Web 访问凭据;它绝不是 mihomo 控制器密钥,也不会出现在状态 DTO、默认 CLI 输出或日志中。
- `panel open` 铸造一次性本地 URL、启动 OS 浏览器,且不打印令牌。
- 面板静态资产位于 `web/{panel}/{build}/` 下,使用原子 `active.json` 切换,并保留一个先前构建用于回滚。
- 浏览器 REST 与 WebSocket 流量在网关处认证;网关只将控制器密钥注入被代理的控制器请求。
- 未知写入默认拒绝;核心升级与托管字段写入永远不会到达 mihomo。

## 订阅

- 订阅 URL 仅存储在守护进程私有的目录中,并从 list/show 响应与常规错误中省略。
- 每个有效配置都有独立缓存,因此 `sub use` 在无 provider 网络访问时也能工作。
- 每个订阅可独立配置拉取代理(`direct` / `proxy` / `auto`);`auto` 在代理失败时回退直连。
- 生成的配置总是在 `mihomo -t` 与重载之前恢复 Mihari 托管的内环回控制器、密钥与端口不变量。

## 系统代理与 TUN

- `sysproxy enable` 将桌面 HTTP/HTTPS/SOCKS 系统代理指向 Mihari 的混合端点。如果另一产品已持有代理,enable 会以 `system_proxy_conflict` 失败,除非传入 `--force`(TUI 会要求确认)。
- `sysproxy disable` 只清除**由 Mihari 持有**的代理;它不会关闭外部代理。
- 在 Windows 上,当 Mihari 作为 LocalSystem 服务运行时,它写入**交互式控制台用户**的 WinINET 配置单元(`HKEY_USERS\<SID>\…`),而不是 SYSTEM 自己的 `HKCU`,因此桌面浏览器能感知到变更。
- `tun enable|disable` 仅持久化并覆盖 `tun.enable`，保留订阅中的其他 TUN 参数，不自动注入 stack，并在可用时通过控制器实时生效。TUN 根据 OS 不同可能需要提权或安装服务。
- `tun enable` 前检测其他 TUN 网卡与其他 mihomo 进程,并按本实例内核 PID 与 live `tun.device` 扣除自身;Down 状态的残留适配器忽略。冲突时以 `tun_conflict` 失败,除非传入 `--force`(TUI 会要求确认)。`--force` 只绕过冲突门控,不绕过 live 核对:内核未真正开启则回滚 Desired。

## GeoIP

- GeoIP 连接详情由守护进程本地解析。国家与 ASN MMDB 文件从公共 `Loyalsoldier/geoip` release 分支下载,与对应的 `.sha256sum` 文件校验,并作为 MMDB 数据库验证。
- 当任一本地文件缺失或至少 30 天未更新时刷新。刷新失败会保留上一对有效的数据库,且不会禁用其他连接详情。

## 数据路径与安装边界

Unix 默认入口 B 为 Linux `/var/lib/mihari` 或 macOS `/Library/Application Support/mihari`，业务 D 为 B/data；E/C/channel 分别为 B/control.sock、B/control.token、B/mihari-channel，I 默认 `/usr/local/lib/mihari`。B root0711、D root0700、C/channel root0644、E root0666，普通用户通过认证控制同一机器代理。机器日志为 D/logs，本用户 U/logs 与 U/logs-export 独立；Linux U 使用绝对 XDG_STATE_HOME 或可信 home，macOS 使用可信 home/Library/Logs/mihari。默认发现不使用 XDG_RUNTIME_DIR，root 不信 HOME/SUDO_USER/XDG。

显式 MIHARI_DATA=P 保留 P 本身的私有布局与0700/0600权限；Windows 路径、named pipe、DACL、服务与本地 ZIP v1 保留。Unix 系统导出为 mihari-logs-export/v2，通过固定机器快照协议组合机器和本用户日志，离线须明确选仅本用户日志。客户端不创建 B/D/C，也不直接读取 D。

业务写入归 daemon/Manager；窄例外为 root installer 的持锁停机迁移/安装事务，app 对固定 channel sidecar 的受锁原子维护，以及服务已停止后的全量卸载删除预检通过的受管文件根。安装锁顺序 B→私有服务 P→data→endpoint，永久锁不 unlink。activation 之前恢复 source，之后只修复 target；旧树和日志保留，未知身份拒绝覆盖。已安装服务启动先校验全局 B 的 matching activation 与 binary hash，取得 data/E 后再校验，不重入 installer 持有的 install lease。未标记 root P 前台只看 P。root P channel 写入持 P 锁时只读相关 B journal，相关未完成事务拒绝；非 root P 不读 B。

所有平台由共同的 subscription.Generate 保留非托管配置，仅覆盖 Mihari 管理的关键参数，TUN 只覆盖 enable；配置语义与原生 provider 由 mihomo 处理。root runtime 保留可信核心 provenance，仍只允许 v1.19.30 的四个 Unix hash；不再按完整字段白名单拒绝配置，也不保证限制所有额外 listener 或文件访问。旧 provider/resource WAL 先恢复，再从原订阅缓存生成；缺失原缓存时保留旧数据并报错，历史资源不主动清理。验证子进程先恢复历史事务，再执行认证和校验，不启动真实业务、后台刷新或核心。可信 I 及全部祖先必须满足 owner/ACL/挂载规则，不能自动修复主机祖先；离线信任位置为解析后的 I/install-trust。无服务 self-update 使用编译通道且不访问 B；服务更新走统一安装事务。

日志/export 持有真实目录 identity，逐来源 Finish 与完整 EOF 校验后才发布；工作、快照、流、进程、日志与 FS 全部关闭并 join 后才释放 daemon lease。SIGTERM 参与同一清理路径。完整路径、权限、迁移、credential 轮换与恢复合同见 [Unix 布局与恢复](unix-layout.md)。

覆盖项:

```text
MIHARI_DATA=/abs/path
MIHARI_CONTROL_ENDPOINT=...
MIHARI_CONTROL_CREDENTIAL=...
```
