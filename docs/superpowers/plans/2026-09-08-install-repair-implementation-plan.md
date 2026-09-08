# Explicit Install Repair Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. 如用户选择分派执行，再使用 superpowers:subagent-driven-development。Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 三平台统一检测中断安装，由用户明确选择保留数据的修复安装或确认清单后的全新安装，消除机器服务入口的隐式事务恢复。

**UI language:** 用户明确要求保留全英文产品体验；TUI、CLI、安装脚本、帮助和确认输入全部使用英文。中文计划仅描述行为语义。

**Architecture:** app 持有安装状态、只读检测与安装编排；platform 提供受保护目录、原子提交和资源能力；service 提供平台停机证明与启动。安装资源先持久完成，再尝试启动；客户端通过只读协议或已提权的 app 安装用例获取状态。

**Tech Stack:** go.mod 的 Go 1.26.0/toolchain go1.26.5、现有 Cobra/Bubble Tea、x/sys、named pipe/Unix socket；无新增依赖。

**Spec:** [实现设计](../specs/2026-09-08-install-repair-implementation-design.md)；产品约束以 [R5](../specs/2026-09-08-interrupted-install-user-choice-design.md) 为准。

**状态：** 用户已于 2026-09-08 审核通过具体实现设计，随后明确授权提交、推送当前成果并等待 CI 与 bot review。下方任务完成状态以实际验证为准，不把已有 macOS 共享组代码计作本计划完成。PR 保持 draft；真实工作站系统服务验证未获授权。当前已实现的基础与尚未接通的入口见 [阶段进度](../reviews/2026-09-08-install-repair-foundation-status.md)。

## Global Constraints

- 只在 `feat/system-data-root` worktree 工作；保留所有既有修改，不修改 CHANGELOG。
- 查询、普通启动及失败处理不调用 RecoverLocked 或重放旧 action；repair/fresh 必须显式发起。
- 安装器只能在持锁、确认旧进程树退出并获得 data/endpoint 能力后写受管理内容；TUI 不直接写业务状态。
- complete 先于首次启动且必须 durable；用户配置错误、端口冲突、Ready 超时不得回写 applying。
- retain 保留用户数据；fresh 只删除确认摘要绑定的受管理条目，永久锁、其他用户、迁移来源及未知条目不删除。
- Windows/Linux/macOS，amd64/arm64，CGO_ENABLED=0；平台测试不能由交叉编译替代。
- 不执行真实服务安装、目录迁移或工作站原生特权实验；这些需另行授权隔离环境。
- 不自动 commit/push；用户明确要求提交时才检查精确范围并按仓库规则创建提交。
- 每项先用最小声明使测试可编译，再确认测试因目标行为缺失而 RED，随后实现；编译错误不算 RED。

## 依赖与交付边界

按 Task 1 → 2 → 3 → 4 → 5 → 6 → 7 顺序完成；每项复核后继续。Task 1–3 只提供能力，不提前切换生产入口。Task 4–6 的入口切换作为一个完整发布单元，不能发布 Windows 脚本仍绕过记录的混合状态。已有 R4 自动恢复/pending source 任务不再执行，Darwin 整组退出要求由 Task 3 接续。

## Task 1：状态、计划与只读分类

**Files:**
- Create: `internal/app/installation_state.go`、`internal/app/installation_inspect.go`。
- Test: `internal/app/installation_state_test.go`、`internal/app/installation_inspect_test.go`。
- Read: `internal/app/install_request.go`、`internal/app/install_journal.go`、`internal/control/protocol/error.go`。

**Interfaces:** app 的 `InstallationStatus`、`InstallationPlan`、`InstallationOutcome` 对应 spec §2/§5 的 DTO；内部 manifest/record 不作为 control DTO 暴露。`InstallationManager.Inspect(ctx context.Context) (InstallationStatus, error)`；`Plan(ctx context.Context, request InstallationPlanRequest) (InstallationPlan, error)`；`Execute(ctx context.Context, request InstallationExecuteRequest) (InstallationOutcome, error)`。`InstallationManager` 的原生依赖在 Task 2–4 组装，不让 CLI 构造 manifest。

请求值类型如下；Binary 为空由装配层解析当前新 executable，不使用旧服务 ImagePath 默认值。

```go
type InstallationPlanRequest struct {
    Mode string // repair or fresh
    Binary string
    Enable *bool // nil: inherit according to spec
    Start *bool
}
type InstallationExecuteRequest struct {
    Plan InstallationPlan
    ResetConfirmed bool
}
type InstallationStatus struct {
    Schema string `json:"schema"`
    Kind string `json:"kind"`
    ServiceState string `json:"service_state"`
    StartFailed bool `json:"start_failed"`
    Reason string `json:"reason"`
    ID string `json:"id"`
}
```

`Kind` 只接受 spec 六个值；`ServiceState` 复用现有 running/stopped/not_installed/unknown 值。Reason 和 wire 必需字段完整取自 spec §5.1，不在实现中另行增删。独立查询 StartFailed 固定 false；失败证据只属于本次 Execute 的 outcome/error，不持久保存历史 UI 故障。

- [ ] **RED：** 表驱动覆盖 applying+有效持锁→in_progress、applying+无锁→interrupted、complete+stopped→installed、complete+当前Ready失败→installed/service_not_ready、访问拒绝→permission_required、损坏/不稳定快照→unknown。陈旧失败记录不能改变当前 stopped；断言读取前后记录 hash/文件数量一致，fake 恢复/写入/启动调用次数全部为零。
- [ ] 运行 `go test ./internal/app -run '^TestInstallation(Inspect|State|Plan)' -count=1`，确认失败来自分类、严格解析或摘要绑定缺失。
- [ ] **GREEN：** 严格实现 64 KiB/重复键/未知字段/EOF 校验；manifest 的已安装及不存在根规则；固定字段顺序生成确认摘要，拒绝非法模式和无依据根。检测每轮验证锁与记录版本，不跨用户交互持锁。
- [ ] **摘要测试：** 修改记录 ID/hash、根身份、candidate hash、enabled/run 策略或删除清单，旧确认全部失效；只改变查询时间/Ready 不影响摘要。repeat repair 的 base 不嵌套，不丢失实例范围。
- [ ] 再运行以上范围及 `go test ./internal/app`，审查 DTO 字段与 spec 一致。

## Task 2：受保护记录、永久锁与持久发布

**Files:**
- Create: `internal/platform/install_control.go`、`internal/platform/install_control_unix.go`、`internal/platform/install_control_windows.go`。
- Test: `internal/platform/install_control_test.go`、`internal/platform/install_control_unix_test.go`、`internal/platform/install_control_windows_test.go`。
- Read/reuse: `internal/platform/trustedroot_files_unix.go`、`internal/platform/publish_windows.go`、`internal/platform/ownedlease_unix.go`。

**Interfaces:** platform 的 `InstallControl` 提供只读打开、持锁打开、ReadState、按旧 hash 条件发布、保留前态、对已持有版本的同步确认、Close；不解析业务 DTO。发布返回下列值，app 不把 Published 当作 Durable。

```go
type InstallPublication struct {
    Published bool
    Durable bool
}
// PublishState(ctx context.Context, previousSHA256 string, next []byte)
//     (InstallPublication, error)
// ConfirmState(ctx context.Context, expectedSHA256 string) error
```

- [ ] **RED：** 临时根模拟首次创建竞争、永久锁冲突、只读不创建、权限/身份替换、状态损坏。模拟 rename 成功后 sync 失败，断言 Published=true、Durable=false；daemon 的 ConfirmState 失败时不能进入业务。
- [ ] 运行 `go test ./internal/platform -run '^TestInstallControl' -count=1`。
- [ ] **GREEN：** 使用平台现有 handle/可信父目录能力，实现 spec K 及权限。Unix 复用现有 install lease 后获得 operation 锁。Windows 使用 OS KnownFolder 和固定机器操作锁，拒绝调用者环境、重解析点与不可信 ACL。首次目录发布竞争只允许一方创建；输家重新打开已发布的永久锁，不能锁在自己的临时目录上继续。
- [ ] **持久顺序：** 同步 next 文件 → 身份约束替换 → 平台持久确认 → 重读同一版本。Unix 文件/父目录同步；Windows 按 spec §2.1 句柄相对 rename、文件及卷 FlushFileBuffers、SCM SYSTEM hive RegFlushKey 后重读，停机前核验本地 NTFS 和卷同步能力。不得用单纯 os.Rename 成功代替持久完成。
- [ ] **Windows 复用限制：** `publishNoReplaceLocked` 当前只支持不覆盖发布，且发布后 Flush/Close 失败只调用 warn，不能直接用作安装记录提交。新 adapter 必须支持受约束替换并把同步失败返回为 Durable=false/error；不改变现有日志导出的 warning 契约。
- [ ] **初始化用例：** 无实例初始化未安装；旧实例直接 applying；root 尚不存在绑定父目录；K 存在而 state 缺失拒绝修复记录。归档失败时 state 不变且不进入停机。previous-state 只留一份，永不自动恢复。
- [ ] 运行 `go test ./internal/platform`；Linux/macOS/Windows 原生测试分别验证各自 adapter。若 Windows 文件系统不能满足持久契约，禁止启用该发布路径，先修正实现与设计，不降级成“尽力同步”。

## Task 3：安全停机与正常启动

**Files:**
- Modify: `internal/service/definition_process.go`、`internal/service/definition_linux.go`、`internal/service/definition_launchd_group.go`、`internal/service/service.go`。
- Modify/review existing dirty: `internal/platform/process_group_darwin.go`、`internal/platform/process_group_wait_darwin.go`、`cmd/mihari/launchd_service_darwin.go`、`internal/supervisor/child_windows.go`。
- Create: `internal/service/install_stop_windows.go`、`internal/service/install_stop_windows_test.go`。
- Create: `internal/platform/runtime_job_windows.go`、`internal/platform/runtime_job_windows_test.go`、`internal/platform/boot_session_windows.go`、`internal/platform/boot_session_windows_test.go`、`internal/app/runtime_registration_windows.go`、`internal/app/runtime_registration_windows_test.go`。
- Test: `internal/app/install_stop_authority_test.go`、`internal/service/definition_launchd_group_test.go`、`internal/service/definition_systemd_test.go`、`internal/supervisor/child_windows_test.go`。

**Interfaces:** app 消费 `Quiesce(ctx context.Context) error` 与 `StartAndCheck(ctx context.Context) error` 的平台 session 方法。成功 Quiesce 表示已阻止自动重启、验证身份并确认所有受管写入者退出；单独的 SCM/launchd/systemd stopped 不满足。StartAndCheck 在 complete 提交后调用，不持有 data/endpoint/gate。

- [ ] **RED：** daemon leader 退出而 core/后代存活时 Quiesce 不成功；身份变化、权限错误、观察不完整均拒绝写入。空树可继续；取消/超时返回有界错误且不泄漏句柄。
- [ ] 运行 `go test ./internal/service ./internal/supervisor ./internal/platform -run '(InstallStop|ProcessGroup|LaunchdGroup|Systemd)' -count=1`，必要时对 Windows 新测试使用明确 Test 名称避免零匹配。
- [ ] **GREEN Linux：** 复用受验证 cgroup identity、禁止重启并确认组空，保留 KillMode=control-group。不能把 cgroup 路径丢失但身份未知直接解释为安全。
- [ ] **GREEN macOS：** 接续既有共享组实现；保留 gate、单 generation 登记及跨 boot 校验；所有 core 派生前完成登记；不实现 R4 pending source 授权。检测须包含 bootout 后残留后代。
- [ ] **GREEN Windows：** 按 spec §4.2 建立带 ACL 的外层 named Job，daemon 业务前先入组；startup.lock、runtime-windows active/empty 和 HKLM volatile boot session 均纳入错误注入。installer 双查 SCM PID、持有并核对进程创建时间/SID、Job 句柄及 ActiveProcesses=0。child 不能 breakaway，daemon 清理不等待被 installer 持有的 gate；同 boot Job 缺失不能当作空树，完整重启必须以受保护 boot session 变化验证。
- [ ] 对每个平台 fake 验证“临时启用→启动→收口期望策略”；启用开关不参加 definition hash。策略不符单独报错，不能触发安装重放。
- [ ] 原生测试只在已授权隔离 hosted runner 执行，记录窗口覆盖；本地 fake 和跨编译不能被称作已证明真实整树退出。

## Task 4：显式安装、修复和精确重建

**Files:**
- Create: `internal/app/installation_repair.go`、`internal/app/installation_repair_test.go`、`internal/app/installation_reset.go`、`internal/app/installation_reset_test.go`。
- Modify: `internal/app/installer_unix.go`、`internal/app/install_entrypoints.go`、`internal/app/install_recovery.go`、`internal/app/install_transaction.go`。
- Create: `internal/app/installation_adapter_unix.go`、`internal/app/installation_adapter_windows.go`；Test: `internal/app/installation_legacy_test.go`。

**Interfaces:** 完成 Task 1 的 InstallationManager.Plan/Execute；消费 Task 2 存储与 Task 3 平台 session。app 的 `InstallationPlan` 保存 schema、mode、instance、candidate_sha256、preserve、delete、plan_sha256；instance 是有类型的记录版本/根身份/服务策略 DTO。`InstallationOutcome` 保存 schema、installation_complete、service_state、start_failed、id。删除条目含规范路径和类别；执行重新解析能力，不从 DTO 直接构造递归删除权限。

- [ ] **RED：** 将配置/订阅/偏好/日志及未知文件写入 t.TempDir，执行 interrupted→repair；逐文件比较原始 bytes/hash，确认无旧 snapshot import/restore。用 fake 服务 Ready 错误验证 complete 保留且第二次 start 不安装。
- [ ] 运行 `go test ./internal/app -run '^TestInstallation(Repair|Reset|Legacy|Commit)' -count=1`。
- [ ] **GREEN：** 实现 prepare → 锁内重验 → previous-state → durable applying → Quiesce → data/E → 受控部署 → durable complete → 释放启动所需锁 → StartAndCheck。任一前置失败停止本次操作；错误后不自动 RecoverLocked。
- [ ] **fresh 授权：** 将允许条目固定为 spec §4.1 清单；缺少确认、摘要失配、根交换、外链/硬链写入、未知顶层目录均拒绝扩大删除。只移除获批条目本身及安全可遍历的内容，不删除 D/P 根或永久锁。不跨多个条目承诺整体原子，部分删除时仍 applying，下次只能明确再选。
- [ ] **认证资源：** 两种模式均保留 control token；fresh 的 web credential 仅随已获批 web 条目移除并由既有 owner 重建。自定义 credential 与删除条目相交时计划拒绝；测试覆盖 Unix B 中 token、Windows D 中 token。
- [ ] **重复中断：** 在每个副作用前后失败两次再 repair；原有未获 reset 授权的数据始终保留。fresh 已删除的数据不会被下一次 repair 复原，UI 必须准确说明。
- [ ] **旧格式：** 无 K 时验证旧布局；不擅造旧 complete。v1 pending 仅显式 repair；新模式禁止旧 recover 回放。普通 install/reinstall/update 碰到 pending 返回提示。旧 Windows 无历史仅报告可验证结果；首次采用先抑制旧版本自动启动、证明停机。
- [ ] **其他操作：** install/update/reinstall 共享提交边界且不 reset；uninstall 安全停机、只移除服务定义/注册、保留安装文件及数据后提交 installed=false，不再启动。binary=null 不表示物理文件已删除。卸载中断后的 repair 保留原数据；迁移多根无唯一证据时不能自动挑根或继续旧迁移。
- [ ] **迁移记录：** operation=migrate、source_scope 绑定选择证据。K 一旦发布，旧 v1 journal 只读；repair/fresh 跨两次中断原样继承 source_scope，repair 不续拷贝，fresh 不碰源或与源重叠的目标，complete 后仍保护来源。
- [ ] **首次采用窗口：** repair 接受 verified legacy；fence 前中断仅容许未改内容且匹配 base 的旧实例运行；fence 后、stop 前及替换前三个窗口分别注入，验证未提前写资源、旧版本不能被恢复重新拉起。
- [ ] 运行 `go test ./internal/app ./internal/service`。检索生产 RecoverLocked 调用逐个列出：机器安装入口的自动调用全部去掉；仅明确保留的旧兼容显式入口可存在，且新模式拒绝。

## Task 5：CLI、只读协议与三平台装配

**Files:**
- Create: `internal/cli/service_installation.go`、`internal/cli/service_installation_test.go`。
- Modify: `internal/cli/root.go`、`internal/cli/service.go`、`internal/cli/service_apply.go`、`cmd/mihari/unix_layout.go`、`cmd/mihari/windows_layout.go`。
- Create: `internal/control/protocol/installation.go`、`internal/control/client/installation.go`、`internal/control/server/installation.go` 及各自 `installation_test.go`。
- Modify: `internal/app/runtime.go`、`internal/control/server/runtime.go`，在现有 runtimeRoutes 中注册新 installationRoutes，复用 `server.go` 的统一认证。

**Interfaces:** CLI Dependencies 新增 `InstallationInspect func(context.Context) (app.InstallationStatus, error)`、`InstallationPlan func(context.Context, app.InstallationPlanRequest) (app.InstallationPlan, error)`、`InstallationExecute func(context.Context, app.InstallationExecuteRequest) (app.InstallationOutcome, error)`；绑定 app manager 的方法。control 使用 `GetInstallationStatus(ctx context.Context) (protocol.InstallationStatus, error)`，返回脱敏 status，不返回 plan/manifest。写入仍不走新增 HTTP endpoint。

- [ ] **RED：** repair/fresh/install-plan/install-status 路由参数；fresh 无交互且缺任一 --yes/--plan-sha256 拒绝；新 --binary 必须绝对路径。旧 reinstall 输出与数据语义不扩大。Ready 失败输出标准 error envelope、非零退出且 details 标识安装已完整。

以下回归固定“状态无法确认仍是一次成功查询”，不让 CLI 将其偷换为执行成功或自动修复。放在新 service_installation_test.go；导入 bytes、context、encoding/json、testing 和 internal/app。

```go
func TestInstallationStatus_UnknownIsReadOnlyResult(t *testing.T) {
    calls := 0
    deps := Dependencies{InstallationInspect: func(context.Context) (app.InstallationStatus, error) {
        calls++
        return app.InstallationStatus{
            Schema: "mihari.install-status/v1", Kind: "unknown",
            ServiceState: "unknown", Reason: "record_invalid",
        }, nil
    }}
    stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
    exit := Execute(context.Background(), []string{"service", "install-status", "--json"}, stdout, stderr, deps)
    var got app.InstallationStatus
    if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
        t.Fatalf("status JSON: %v", err)
    }
    if exit != ExitOK || calls != 1 || got.Kind != "unknown" || got.StartFailed || stderr.Len() != 0 {
        t.Fatalf("exit=%d calls=%d kind=%s start_failed=%v stderr_bytes=%d", exit, calls, got.Kind, got.StartFailed, stderr.Len())
    }
}
```

- [ ] 运行 `go test ./internal/cli ./internal/control/protocol ./internal/control/client ./internal/control/server -run 'Installation|Service' -count=1`。
- [ ] **GREEN：** 按 spec §5 绑定命令和 DTO，使用现有 error code/退出码。plan 先显示保留/删除范围；授权后 Execute 重验摘要。补充 `--help`，明确 fresh 不删除未知文件、repair 不保证修好用户损坏配置。
- [ ] **契约回归：** 覆盖 --enable/--start 的继承/显式覆盖和摘要绑定；按 §5.1 验证 status 中 unknown/permission_required 是成功的状态查询，执行权限失败仍非零，IPC 认证和 daemon_unavailable 保持原语义。Ready 失败只输出标准 error，不同时打印成功 outcome。
- [ ] **启动：** complete reader 前后核验同一安装身份；未持久/不可信/applying 不启动业务。完整后 start 失败不改记录；不用旧 journal 再阻挡新模式的正常启动；未创建 K 的便携路径保持原行为。
- [ ] **统一入口：** service action、Unix service apply、Windows Manager、自更新替换均路由到 app 安装编排；切断 Windows AfterReplace 才记安装结果的路径。制品验证留在停机临界区外。
- [ ] 全部四个 control/cli 包和 `go test ./cmd/mihari ./internal/app` 通过后，检查现有 /v1 DTO 快照没有变化，新只读 route 复用 IPC 认证。

## Task 6：TUI 提示与独立安装脚本

**Files:**
- Create: `internal/tui/installation_prompt.go`、`internal/tui/installation_prompt_test.go`。
- Modify: `internal/tui/prepared_update.go`、`internal/tui/prepared_update_test.go`、`internal/tui/run.go`、`internal/tui/model.go`。
- Modify: `scripts/install/install.sh`、`scripts/install/install.ps1`、`scripts/install/install-aio.sh`、`scripts/install/install-aio.ps1`、`scripts/install/install-aio-remote.sh`、`scripts/install/install-aio-remote.ps1`。
- Create: `scripts/install/test_install_repair.py`；Modify: `README.md`、`docs/unix-layout.md`。

**Interfaces:** TUI 通过 control/client 获取在线 status，离线调用已授权 app inspector；复用 prepared-update 的候选归属、取消 join 和退出后执行。脚本下载完整新 binary 后调用 Task 5 命令，不复制安装状态机。

- [ ] **RED：** confirmed interrupted 仅弹一次；in_progress/unknown/permission_required/普通连接失败不显示“检测到中断”。取消不创建记录、不 stop、不 restore、不 delete。repair 默认推荐，fresh 必须进入路径清单和第二次确认。
- [ ] 运行 `go test ./internal/tui -run 'Installation|Prepared' -count=1`；脚本测试用临时 fake binary 记录 argv，不调用公网/提权/系统服务：`python -m pytest scripts/install/test_install_repair.py -q`。
- [ ] **GREEN：** 固定文案 "An interrupted installation was detected. Reinstallation is recommended."；两个按钮 "Repair installation (keep data, recommended)"、"Fresh installation (reset managed data)" 及 "Cancel"。服务启动失败显示独立英文提示并提供启动重试，不默认清数据。CLI 交互确认输入为 `reinstall`。
- [ ] **权限：** 离线无权检查只提示管理员入口，不能断言中断；不自动 UAC/sudo。已提权 TUI 确认后先 cancel/join 任务，关闭自己的日志/FS，再交给 app 执行。
- [ ] **脚本：** 新 repair/fresh 模式映射相应新命令；普通无人值守中断即退出。fresh 非交互必须显式确认摘要；旧程序缺失/损坏仍能通过新下载程序检查及修复。移除先运行旧 uninstall 或无记录直接替换的旁路。
- [ ] 文档说明两个选项、配置损坏不会自动清空、fresh 再次中断不能恢复已删除数据、旧安装识别限制、权限入口；不把实现 hash/schema 展现在普通用户弹窗里。

## Task 7：故障矩阵、全入口审计与交付

**Files:** Create `internal/integration/installation_repair_test.go`、`internal/integration/installation_start_test.go`；Modify `scripts/test/test_unix_layout_security.py`、`scripts/test/unix_security.py`、`scripts/test/unix_security_host.py`；Create `docs/install-repair.md`。

- [ ] **RED：** 使用真实 app reader/manager，fake 只替换 OS/下载/IO 边界，依次注入 applying publish/sync、停机、部分 reset、binary/definition 发布、complete rename/sync、锁释放和 Ready 错误。每个窗口验证当前 status、是否允许业务及数据字节，不只断言函数调用顺序。
- [ ] 运行 `go test ./internal/integration -run '^TestInstallation' -count=1`；先修复被失败证明的最小实现，再执行集成全包。
- [ ] **入口审计：** 对 CLI、TUI、两类脚本、service Manager、自更新、daemon 启动逐项记录落到同一 app 路径的证据。普通命令不再恢复中断；v1 新模式拒绝没有绕过；完整但启动失败始终可单独重试。
- [ ] **当前平台：** `go test ./...`、`go test -race ./...`、`go vet ./...`、`gofmt -l .`；Python 使用项目现有测试入口，附加上述安装脚本与 `python -m pytest scripts/test/test_unix_layout_security.py -q`。race 如缺编译工具链明确记为未验证，不静默跳过。
- [ ] **编译：** 在 task 专用 PowerShell 环境依次设置 CGO_ENABLED=0、GOOS 为 windows/linux/darwin、GOARCH 为 amd64/arm64，构建 `./cmd/mihari` 到临时目录；恢复原环境。输出不得提交。
- [ ] **原生：** 三平台 hosted 隔离环境分别验证完整安装后重启、活跃安装/中断、旧进程后代残留、修复保留数据、fresh 范围及 Windows 文件持久同步。尚未授权时只交付测试与明确的未验证表，不能宣称原生验收已通过。
- [ ] **复核交付：** 使用 requesting-code-review 进行独立审计，处理确认的问题；展示准确 diff 范围、验证结果和残余限制。用户未要求 commit/push 时保持工作区供审阅。

## 计划自审映射

| 设计要求 | 任务 |
| --- | --- |
| K/schema/严格解析/初始化/持久性 | 1、2 |
| 安装状态与运行状态分离 | 1、4、5、7 |
| 三平台树退出、macOS generation/gate | 3、7 |
| 新修复/精确 reset/重复中断 | 4、6、7 |
| CLI/错误/只读 IPC/权限获取 | 5、6 |
| legacy、所有旧自动恢复入口与自更新 | 4、5、6、7 |
| 数据保护、取消无副作用、独立新程序 | 4、6、7 |

用户已审核通过并开始实施。各任务只有在实现、相应验证与独立复核完成后才勾选；设计获批不表示功能或原生验收已完成。
