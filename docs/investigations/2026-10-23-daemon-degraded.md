# 2026-10-23 daemon degraded 日志调查

## 结论

输入：`mihari-logs-20261023-172613+0800.zip`。时间均为 UTC+08:00。

已确认的直接原因是启动时运行配置的 `bind-address` 未通过 Mihari 受管字段校验。daemon 无法装配 runtime，随后提供 health=degraded 的本地控制服务。

用户随后确认此前通过 TUI 的 System 页修改了端口或监听地址。进一步源码调查和隔离复现确认：普通运行路径的端点更新只保存 settings，没有同步运行配置；重启却直接校验已有运行配置，因此正常 TUI 操作即可产生本次同类错误。日志包仍不足以确定现场修改前后的具体地址。

## System 页更新链路缺陷与复现

`internal/tui/pages/system/model.go` 的 confirmPortEdit 经 applyPortUpdate 调用 UpdateOnboarding。`internal/runtime/onboarding.go` 的 updateOnboardingAttempt 准备并保存新 settings，发布内存 settings，并设置 onboardingRestartRequired。普通运行路径没有生成或提交新 runtime/config.yaml。TrustedCore 分支虽准备候选用于验证，也不能作为 Windows 普通路径已同步配置的证据。

而 `internal/app/runtime.go` 的普通启动路径对已有运行配置先执行 EnsureRuntimeConfig，没有在此之前根据新端点重新生成配置。两段代码对“重启应用新地址”的约定不一致：保存侧留下旧运行配置，启动侧要求它已经符合新 settings。

在 internal/runtime 中执行了临时调查测试，所有文件位于 t.TempDir，仅调用 Manager 更新及启动配置校验，没有运行真实核心或系统服务：

1. 以 mixed-addr=127.0.0.1:9190 创建合法 bootstrap 运行配置，确认启动校验通过。
2. 调用与 TUI 同一业务入口 UpdateOnboarding，把地址改为 127.0.0.2:9190。
3. 确认更新成功、返回 RestartRequired，磁盘 settings 已保存新地址。
4. 用磁盘新 settings 对已有运行配置执行 EnsureRuntimeConfig，得到 data_failure、field=bind-address。

命令 `go test ./internal/runtime -run '^TestInvestigation_EndpointChangeLeavesOldRuntime$' -v -count=1` 通过，证明上述故障路径已复现（该测试断言故障存在，不代表功能修复）。临时测试已删除，未修改生产代码。

同一缺陷也会影响 mixed 端口或 controller 地址修改；具体报错字段取决于哪一个受管字段首先不一致。仅修改 mixed 端口通常会先报 mixed-port；本例保留端口、修改 IP，精准复现 bind-address。示例地址仅为合成测试数据，不是现场配置值。

## 日志证据

行号对应 ZIP 内原始 JSONL 文件。

| 时间 | 文件与行号 | 事件 |
| --- | --- | --- |
| 17:21:20 | mihomo/mihomo.log:55204、55223、55225 | 核心初始化完成，controller 监听 127.0.0.1:9090 |
| 17:22:39.669 | daemon/mihari-daemon.log:224 | runtime_build_failed，data_failure，field=bind-address |
| 17:22:39.696 | daemon/mihari-daemon.log:225 | 本次启动的控制 named pipe 打开失败：Access is denied |
| 17:24:37.660 | daemon/mihari-daemon.log:234 | 订阅调度器 context canceled |
| 17:24:39.259 | daemon/mihari-daemon.log:235 | 再次 runtime_build_failed，field=bind-address |
| 17:24:42 起 | daemon/mihari-daemon.log:236 起、tui/mihari-tui.log | 控制请求返回 mihomo runtime is unavailable；installation.inspect 同样不可用 |
| 17:25:29.224 | daemon/mihari-daemon.log:277 | 再次 runtime_build_failed，field=bind-address |
| 17:26:07.888 | daemon/mihari-daemon.log:278 | 再次 runtime_build_failed，field=bind-address |

四次启动失败的原始原因相同：

```text
api error (data_failure): runtime configuration violates a mihari-managed field
details: {"field":"bind-address"}
```

17:22:39 的 pipe 拒绝访问是另一项启动失败；日志没有给出占用进程或 ACL 详情，不能确认是重复 daemon 还是权限问题。它不能解释后续仍可通过 IPC 返回 runtime unavailable 的降级状态。早间订阅 HTTP 502、TUI 旧二进制清理失败以及 pipe 关闭错误均没有证据表明触发了这次 bind-address 校验失败。

## 修复前的源码链路

调查基线：修复前的 `9a445bd6`，日志包不含运行程序的版本信息。以下链路描述该基线；本 PR 已将普通启动路径改为 `core.ReconcileRuntimeEndpoints`，见末尾“后续修复”。

1. `internal/app/runtime.go` 的普通运行路径调用 `core.EnsureRuntimeConfig(paths.RuntimeConfig, settings)`；失败直接返回，发生在受管端口探测之前。
2. `internal/platform/paths.go` 定义运行配置为数据根下的 `runtime/config.yaml`。
3. `internal/core/config.go` 读取已有 YAML，通过 `BootstrapConfig(settings)` 生成期望字段，再以 `reflect.DeepEqual` 逐项比较 `mixed-port`、`allow-lan`、`bind-address`、`external-controller`、`secret`，遇到首个不一致立即返回。
4. 期望的 `bind-address` 是 `netip.ParseAddrPort(settings.MixedAddr).Addr().String()`，即 settings 的 `mixed-addr` IP 部分。默认 settings 对应 127.0.0.1，但日志不能证明故障现场使用默认值。缺失字段、错误类型或不同字符串都可能失败；日志没有记录具体差异。若运行版本采用当前检查顺序，mixed-port 和 allow-lan 已通过，后两个字段尚未检查。
5. `cmd/mihari/main.go` 记录 runtime_build_failed；该错误不属于 ManagedPortConflict，因此走 `runDegradedDaemon`，不创建端口恢复 runtime。
6. `internal/app/degraded.go` 构造 health=degraded 的 Store；daemon 保留 IPC 和日志快照，未装配业务 Manager，因而 runtime/installation 接口不可用。

当前 `internal/subscription/generator.go` 也会强制把 bind-address 写成 settings.MixedAddr 的 IP，覆盖订阅与 overrides 的对应值。因此，仅凭订阅原文带了其他 bind-address，不能解释通过当前生成器生成后的不一致。

## 后续需要的最小证据

需要故障数据根中 `settings.yaml` 的 `mixed-addr`，以及 `runtime/config.yaml` 的 `bind-address`（含 YAML 类型），并确认运行 Mihari 的版本。可同时核对 mixed-port、allow-lan、external-controller 是否匹配；无需提供 secret 或完整订阅。这些证据可区分缺失字段、地址差异、类型问题以及版本行为差异。调查没有访问实际安装目录或改动系统服务。

## 调查阶段的范围与验证

仅保留独立 worktree 和调查文档，没有修改生产代码或真实配置，没有启动 daemon 或真实 mihomo，没有提交。已直接读取 ZIP 的 manifest、daemon/TUI JSONL 和 mihomo 日志并核对源码；执行过上述隔离临时复现测试，随后删除临时测试文件。未执行全仓测试，本次未修改生产行为。AGENTS.md 指定的旧架构文档在当前基线不存在，已查阅现有 docs/architecture.md。

## 后续修复

修复分支从 origin/dev 创建，普通 daemon 启动在核心更新恢复检查之后、构造控制客户端和启动核心之前调用 core.ReconcileRuntimeEndpoints。只同步 mixed-port、bind-address、external-controller；保留其余运行配置，secret 和 allow-lan 继续严格匹配，缺失字段、字段类型错误或非法 YAML 不进入修复。配置原本匹配时不重写、不额外运行核心检查。

不匹配时先生成同目录临时候选；存在本地核心则通过其 -t 检查，检查有 15 秒超时。发布前重查原文件字节，发生变动拒绝覆盖，成功后沿用原子写入。核心尚未启动，不执行 live reload。尚未安装核心时可保存端点，沿用 bootstrap 配置无需本地核心的行为。未完成核心更新时仍跳过该路径，保留既有修复控制面。

该启动 API 要求调用方独占此数据根的业务写入权。Unix 启动已有 daemon lease；Windows 改为先独占控制 named pipe，再读取 settings、装配 runtime，并将同一 listener 交给正常或 degraded 控制服务。Windows 的保证要求共享同一数据根的 daemon 使用同一控制端点；用不同 MIHARI_CONTROL_ENDPOINT 同时运行多个 daemon 共享同一数据根不在此保证范围内。重复启动在进入业务配置处理前失败，listener 在装配失败及服务退出时关闭，底层只关闭一次。执行同步时 Manager.Run 尚未启动，因此不存在当前 Manager 的并发配置 mutation。源文件重查用于发现候选准备期间的变化，不是为不遵守单写入者约束的任意外部写者提供 compare-and-swap 保证。ReconcileRuntimeEndpoints 在完成读取或 bootstrap 创建后重新检查自身传入的 context；这不表示既有 BuildRuntimeWithOptions 整个装配过程已支持调用方取消。

正式回归测试 TestBuildRuntime_EndpointUpdateSurvivesRestart 通过业务 Manager 保存端点后重新装配 daemon，覆盖 Mixed IP、Mixed 端口、Controller 和 Web 地址。旧代码在前三种场景失败，修复后全部通过。core 测试另外覆盖校验前不发布、校验失败或取消保留原文件、准备期间源配置变动拒绝覆盖、候选清理、认证/非法字段拒绝和配置无变动时保持原字节。全部使用合成配置及隔离目录。

候选临时文件在成功提交后清理失败，只通过现有诊断 reporter 记录 WARN，不把已成功更新的配置改判为启动失败；准备本身已失败时仍合并保留清理错误。运行配置发布使用既有 AtomicWriteWithCommit 结果，目录同步等提交后 warning 同样记录 WARN 并保持成功，不与未提交失败混淆。候选清理和已提交 warning 均有单独回归测试。
