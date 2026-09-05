# Unix Base Dir 执行计划评审 R1

- 结论：**REQUEST_CHANGES**。
- 日期：2026-09-05；代码基线：`2d00f61e720fa27f115dea52f7b4a95cc35a599f`。
- 计划：`docs/superpowers/plans/2026-09-05-unix-base-dir-implementation-plan.md`，R1，430 行。
- **计划 SHA256：`93E662D20B928C3DECB457B28ABB04F54111851DDFD4BC766EB5D43DAE565336`。**
- 对照规格：Unix Base Dir R4，SHA256 `3A97A33DBB1B1CA5122F2A25F8545FD5A1EBD73B5DA5A160047749FF8A942F3E`，技术设计 PASS 继续有效。
- 审阅范围：全量计划、R4、当前平台/IPC/核心/provider/TUI/service/CI 相关入口。未修改计划、spec 或代码，未实施或提交。

计划已正确纳入预审指出的四个容易遗漏的入口：所有 `-v`/`-t`/正式核心执行前验证、validation 日志初始化不得隐含 LoadOrCreate、TUI 在 Apply 前退出清理、Web provider 写路由保持当前拒绝。分阶段构建内部能力、最后切换默认系统模式的方向正确。以下问题针对任务是否可以按给定接口与门禁实施，不是把尚未写代码或尚未执行平台测试当作缺陷。

## UBP-R1-01 — 跨任务接口还不能传递安全身份与导出结果

**严重度：Major / 可接线性阻塞。计划行 64–103、159–187、219–234、236–263。**

几处接口缺失会迫使不同任务的实施者自行重设计调用边界：

1. T04 需要期望的 peer owner 及 system/private listener mode，但没有给出 owned Listen/Dial 的具体输入。T05 `WithCredentialProvider(endpoint, provider)` 只有路径及 `Load`，也没有传递 mode/expected peer 或已验证 dial capability 的位置。当前 `control/transport/unix.go:15,47` 以及 `client/client.go:24–34` 是裸 endpoint 接线，不能自然得到完整 layout 的身份约束。
2. `SourceReader` 只有 Next(payload,redacted,error)/Close；T10 却声明消费“记录和结束统计”。现有 manifest 需要 lines、skipped_invalid、redacted、sources（`logging/export_json.go:21–42`），v2 还需要 collected/not_requested 状态。客户端验证 complete/source_end 后，assembler 通过何接口获得可信终态没有定义。
3. `PolicyInput` 不含订阅 ID/generation，`ProviderSpec` 却要求产生它们；Generation 被定义成 string，而现有 `subscription.Profile.Generation` 是 uint64（`subscription/model.go:36–37`）。必须明确身份由谁绑定及何时不可再改，不能由每个调用方任意补字段。
4. `Manager.RefreshProvider(..., protocol.MutationRequest)` 把 transport DTO 带入内部用例，和当前 `UpdateRuleProvider(ctx, operation runtime.Operation, name)`、其他 Manager mutation 不一致（`runtime/manager.go:418–435`）。如果在委派时重建 Operation，还可能丢失内部 Source/幂等信息。

**修订条件：**

- 为 owned listener、verified dial、CredentialProvider client 构造写出相容的签名或 options，明确 caller 只传可信 layout/owner 条件、谁持两个锁、哪个层执行 peer 校验。保持旧 Listen/Dial 构造作为受控兼容适配器，直到 T18 切换。
- 增加不可伪造成功终态的来源统计接口/结果值，说明 SourceReader 正常 EOF、source_end、complete、失败与 assembler Publish 的时序；本地与远端来源都能生成 v2 必要统计。
- 将 generation 与现有模型统一，定义 PolicyInput/输出 provider 的身份绑定点；将 RefreshProvider 改用现有 runtime.Operation，并让边界 handler 继续做 DTO 适配。
- 增加一个跨任务编译/集成 fixture：layout → verified client → snapshot reader → assembler，以及 subscription candidate → provider identity → Manager mutation。无需写完整方法体，但签名必须能闭合。

## UBP-R1-02 — 若按当前示例执行，部分测试没有有效 Red 或可能假阳性

**严重度：Major / TDD 可执行性阻塞。计划行 28、126–141、194–204。**

T02 要求“先写拒绝 stub 以得到正确 Red”（139），但示例只断言 symlink 被拒绝（128–136）；总是返回拒绝的 stub 会直接通过。该示例也可能因 t.TempDir 的祖先或文件系统不满足 RootPolicy 而通过，根本没执行应用 symlink 检查。

T06 例子仅输入 `external-controller-unix` 而没有有效订阅内容，且只断言任意 error。当前 `subscription/document.go:27–31` 本来就拒绝既无 proxies 也无 proxy-providers 的文档，因此该输入不足以证明 root policy 起效。用通用拒绝 stub 同样不能得到预期行为 Red。

**修订条件：**

- T02 先建立同一可信临时祖先下正常目录能成功打开的正例，再只改变最终组件为 symlink；拒绝断言验证明确类别/检查阶段，并断言没有修改 target。为 t.TempDir 在严格策略下是否可用提供 fixture 获取方式。
- T06 使用现有 parser/generator 可接受的完整代理配置作基线，加入唯一危险字段后精确断言 RootConfigPolicy 的 data_failure/安全字段路径，且错误不含值。至少一个允许配置的成功测试应先使拒绝 stub 出现真实 Red。
- 将“每项先拒绝 stub”改成按测试目标选择最小可编译 scaffold；缺行为导致断言失败才是 Red，不能把已有错误、更早的错误或编译失败计作证据。
- 在任务验收记录预期失败断言和实际失败原因，避免只记录命令退出非零。

## UBP-R1-03 — Unix-only 能力与共用文件的构建矩阵未闭合

**严重度：Major / Windows 兼容与任务依赖阻塞。计划行 51–60、72–80、94–100、143–172、279–322。**

`TrustedRoot`、`OwnedInstallLease` 放在 `_unix.go`，但计划中的 `NewPrivateFSFromRoot` 和 app `InstallTransaction` 接口使用这些类型，拟修改/创建的 `privatefs.go`、`app/install_transaction.go` 等是共用文件。当前 `privatefs.go` 在 Windows 参与构建（平台状态由 `_windows.go` 提供），`app` 也参与 Windows 主程序装配。如果没有明确 build tags、共同 opaque 声明或 Windows 适配器，按文件表直接实施会引入 undefined type；仅在全局写“平台代码用后缀/标签”不能指明此处怎么分。

T04 还计划直接把现有 Listen 改为 owned listener，但 T18 前现有 `daemon/run.go:28` 仍调用裸 endpoint 的 transport.Listen。需要明确新入口与旧入口怎样并存，使中间检查点不会把旧生产启动提前接到未准备的系统锁/模式。

**修订条件：**

- 增加逐文件或逐文件族的 build-tag 表，注明 opaque 声明放共同文件还是 Unix 文件、哪些 app/install 实现和 tests 仅 Unix、Windows 经哪个 factory/adapter 保持现有行为。
- 将 NewPrivateFSFromRoot 等 Unix capability 桥接放入明确 Unix 文件，或给出不会改变 Windows 安全模型的公共类型设计。
- T03/T04/T12/T14 后至少做 Windows 与一个 Unix 目标的编译检查，防止直到 T19 才发现任务签名不能共存；未到 T18 时生产入口必须仍走原兼容装配。
- DAG 与 Files 同步更新，不能让未实现的适配器只隐含存在于“Windows 回归”一句中。

## UBP-R1-04 — 核心 pair journal 和受管执行器的任务归属不清晰

**严重度：Major / 安全接线与依赖阻塞。计划行 35–40、206–217、279–322。**

T07 要实现 binary+receipt 写前 journal、旧 pair 恢复以及所有执行入口验证，但没有指定该 journal 的拥有包、持久路径/动作数据和与 Prepare/Commit 的接口；明确的通用 actions/journal 直到 T12 才建立，DAG 却把 T07 排在 T12 之前。若 core 去调用 app 安装事务，会违反当前 app → runtime/core 依赖方向；若新建 core-local pair journal，应直接把它列入 T07，而不是让实施者猜测“同一写前 journal”指 provider 还是安装 journal。

当前核心提交是 `core.Candidate.Commit()` 单文件替换（`internal/core/install.go:82–119`），由 `runtime.Manager.Install` 在 mutation 锁内调用（`manager.go:514–535`）。执行则有两条不同底层路径：`core.OSCommandRunner.Run`（`core/command.go:15`）和 `supervisor.CommandStarter.Start`（`supervisor/command.go:18–24`）。计划列出 `supervisor/command.go`，却没有给出验证命令如何获得同一环境 allowlist 的执行器接口。

**修订条件：**

- 明确 pair journal 属于 core、运行时还是低层持久化组件，并列出文件、schema/记录内容、恢复入口及 tests；如复用下层通用 WAL，调整 DAG 使它先存在，禁止 core → app 倒置。
- 定义 PreparedCore/Candidate 的验证、pair Commit、Cleanup/Recovery 所有权，保证未匹配 receipt 时所有 `-v`/`-t`/正式 Start 都不可执行，同时旧核心失败保留语义不被破坏。
- 给出共用 VerifiedCore/CommandRunner 或等价接口，固定 binary 身份、环境和 core-home，明确接到 `BuildRuntimeWithOptions`、localReadyVersion、ValidateConfig 和 supervisor 的任务位置。
- 增加 pair 的两次文件替换各前后崩溃、重启恢复两次、探测被拒时 runner 零调用、环境注入被清除的验收命令；T07 验证范围应包含所修改的 app 装配测试。

## UBP-R1-05 — 必需的隔离安全门禁还缺实际 runner 契约

**严重度：Major / 验收可执行性阻塞。计划行 377–386、388–413。**

T19 正确要求 Linux/macOS 两 UID、ACL/mount、peer、缺能力不得 skip，但只命名脚本和 workflow，没有给出怎样执行带 `unix_security` tag 的命令、runner 的输入/输出与退出约定、可信临时锚点或两个 UID 的隔离准备。此处容易出现两种“绿但未验”：只跑默认 `go test ./...` 从未执行 tagged tests；或者正例在 `/tmp`/用户 runner 目录因祖先策略被拒，所有负例通过但从未成功建立系统 capability。当前 `.github/workflows/ci.yml` 尚无该 tagged 安全 job，已有默认测试不能自然覆盖它。

**修订条件：**

- 写出最小命令合同，例如脚本校验专用 CI 标记和参数后构建/运行 `go test -tags=unix_security ...`，或编译测试二进制再切换 UID；注明 root 测试与两个 UID 客户端如何传递临时路径/结果，不必提供整份脚本实现。
- 分 Linux/macOS 指定可信临时 root 创建位置/所有权策略，明确父目录满足 R4 §5，测试中的默认 B 通过依赖注入改为唯一临时目录；不要修改真实 Mihari 目录、真实账户 home 或真实服务。
- 定义专用 UID 的选择/创建与回收、Linux mount namespace、macOS ACL 设置/清理、失败 cleanup 的责任和超时；无法提供必要原语/身份时 job 失败，不能 skip。
- 让脚本输出逐项正例/负例/实际 UID/平台能力结果并由 workflow 验证。至少每个平台证明一次合法 capability、两个普通用户成功连接，以及越权数据/日志读取失败。
- 明确 T18 只是代码接线检查点，最终交付/可合并状态必须包含 T19 的必需安全 job 成功；不能只以第 32 行“T18 门禁通过”描述完整验收。

## 小项及已确认事项

- 计划第 216 行 `app.NewRuntime` 实际符号为 `BuildRuntime` / `BuildRuntimeWithOptions`（`internal/app/runtime.go:51,55`），应修正定位；所抽查的 Modify 文件均存在，包括 session/client.go、panel/install.go、geoip/service.go、tui/actions.go、runtime/capabilities.go。
- T08 的 Web 默认拒绝、T16 的只读 secrets 收集、T17 的 TUI cleanup-before-Apply 已明确，不再重复要求改设计。
- 当前用户授权仅覆盖计划与审核，首部第 13 行及结尾边界正确；本报告不要求现在实施、创建 commit/PR 或运行真实服务来证明计划通过。
- R4 规格不需要因上述计划接线问题重写；修订任务接口、依赖、测试和 runner 契约即可进入下一轮计划评审。
