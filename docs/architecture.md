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

Phase 4 仍需审计后台调度、Web gateway、其余 CLI/TUI 本地任务、stream 生命周期、关闭流程和 logger 自身故障。当前 Web gateway listen 失败已收口为 `web gateway address is unavailable`（`internal/web/server.go`）；mihomo stream 的连接、关闭和无效 JSON 收口位于 `internal/mihomo/stream.go`。Phase 3 没有为这些边界增加跨 IPC 元数据或 owner，也不处理 #197 的 Setup 用户提示。

## 核心安装

守护进程通过同一条下载、校验、替换链路安装 mihomo,并支持 `stable` 与 `alpha` 两个通道:

- `stable` 从 GitHub `/releases/latest` 取当前稳定版;`alpha` 从固定滚动 tag `Prerelease-Alpha` 取预览版。
- 展示与协议里的 Version 永远来自 `ParseVersion(mihomo -v)`:稳定版为 semver(如 `v1.19.x`),alpha 为 `alpha-{sha}`(实测 `-v` 形如 `Mihomo Meta alpha-dd7bc4c ...`,不是 `v1.19.x`)。GitHub tag `Prerelease-Alpha` 从不作为版本显示或写入 Version。
- 本次安装意图随请求进入安装链路;`settings.core-channel` 只在 Commit 成功后写入,表示上次成功安装的通道。切换失败或未提交时,持久化通道与界面仍为旧值。
- all-in-one 包在 `data/bin/core-channel` 写入 sidecar(第 1 行通道,第 2 行 stamp)。安装器覆盖 sidecar,不改 `mihari.yaml`;守护进程仅在 stamp 变化时把打包通道写入 settings,避免旧 sidecar 覆盖用户后来在 System 页切换的通道。

## TUI

- TUI 只通过 `internal/control/client` 经原生 IPC 控制面与本地守护进程通信。它从不打开 mihomo 控制器、从不接收控制器密钥。
- TUI 的日志直接写入例外是经 `internal/logging` 在当前 UID 的 U/logs 追加/轮转固定 `mihari-tui.log*`（Windows/显式私有 P 保留单根）;不得写 `mihari.yaml`、订阅、token、面板或其他业务状态。日志配置变更仍只走 daemon 控制面。
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

业务写入归 daemon/Manager；窄例外为 root installer 的持锁停机迁移/安装事务，以及 app 对固定 channel sidecar 的受锁原子维护。安装锁顺序 B→私有服务 P→data→endpoint，永久锁不 unlink。activation 之前恢复 source，之后只修复 target；旧树和日志保留，未知身份拒绝覆盖。已安装服务启动先校验全局 B 的 matching activation 与 binary hash，取得 data/E 后再校验，不重入 installer 持有的 install lease。未标记 root P 前台只看 P。root P channel 写入持 P 锁时只读相关 B journal，相关未完成事务拒绝；非 root P 不读 B。

所有平台由共同的 subscription.Generate 保留非托管配置，仅覆盖 Mihari 管理的关键参数，TUN 只覆盖 enable；配置语义与原生 provider 由 mihomo 处理。root runtime 保留可信核心 provenance，仍只允许 v1.19.30 的四个 Unix hash；不再按完整字段白名单拒绝配置，也不保证限制所有额外 listener 或文件访问。旧 provider/resource WAL 先恢复，再从原订阅缓存生成；缺失原缓存时保留旧数据并报错，历史资源不主动清理。验证子进程先恢复历史事务，再执行认证和校验，不启动真实业务、后台刷新或核心。可信 I 及全部祖先必须满足 owner/ACL/挂载规则，不能自动修复主机祖先；离线信任位置为解析后的 I/install-trust。无服务 self-update 使用编译通道且不访问 B；服务更新走统一安装事务。

日志/export 持有真实目录 identity，逐来源 Finish 与完整 EOF 校验后才发布；工作、快照、流、进程、日志与 FS 全部关闭并 join 后才释放 daemon lease。SIGTERM 参与同一清理路径。完整路径、权限、迁移、credential 轮换与恢复合同见 [Unix 布局与恢复](unix-layout.md)。

覆盖项:

```text
MIHARI_DATA=/abs/path
MIHARI_CONTROL_ENDPOINT=...
MIHARI_CONTROL_CREDENTIAL=...
```
