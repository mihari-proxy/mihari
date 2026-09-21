# Issue #282：更新残留与启动清理调查

状态：Q1–Q5 已确认，实现与本机验证完成；Linux/macOS 原生验证待隔离环境执行。基线为 origin/dev `6a9da72b`。

Issue：https://github.com/mihari-proxy/mihari/issues/282

## 已明确的需求

- 排查旧版本文件残留原因，并检查 Linux 与 macOS。
- 旧版本文件无需强制立即删除；后续启动尝试清理，失败可在 F2 查看。
- 更新残留与仍承担恢复职责的事务备份是不同概念，见根目录 CONTEXT.md。

## 修复前基线事实

1. `internal/update/replace_windows.go` 与 `internal/core/replace_windows.go` 先将目标改名为 `.old-<UnixNano>`，再发布候选；候选发布失败尝试还原，成功后立即 Remove 旧文件。删除失败作为 warning 返回，不撤销已成功的替换。当前没有对这种残留的启动重试入口。
2. 对应 `replace_unix.go` 直接 Rename 覆盖目标，因此 Linux/macOS 的这条路径不会生成同类 `.old-*`。这不能证明所有更新路径都没有残留，也不能证明旧进程已退出。
3. Issue 对常规核心更新路径的描述需要按当前 dev 修正：`internal/app/runtime.go` 为普通平台安装器设置 `Updates = core.NewUpdateStore`；`internal/core/update_files.go` 的 Apply 使用 `os.Root.Rename`，并不经过上述旧 stash helper。Unix 受保护运行使用对应的 provenance store。核心事务应独立调查与清理。
4. `internal/core/update_transaction.go` 的 Finish 只接受 committed / rolled_back，核对当前核心、journal、marker 和各残留对象身份，按顺序清理候选、备份、恢复副本、journal、marker。`internal/runtime/core_update.go` 在成功或确认回滚后立即调用 Finish，失败收集 warning。
5. 核心事务已允许完成后清理失败而继续执行已确认核心；但新普通更新遇到仍存在的 journal 会拒绝。若改为统一延后清理，必须处理同一 daemon 生命周期内连续更新，不能仅删除 Finish 调用。
6. `cmd/mihari/main.go` 的 daemon 启动及 `internal/tui/run.go` 分别建立诊断 Owner 与内存 History；`internal/diagnostics/owner.go` 先写内存历史，再交给可选文件出口。启动清理 warning 可复用这条 F2 链路，不需要新增本地诊断协议。

## 进程与平台对照

| 平台 | 自更新旧文件 | 核心进程停止机制 | 当前结论 |
| --- | --- | --- | --- |
| Windows | 先 stash，随后单次删除；当前旧进程可能仍映射镜像 | Start 后加入匿名全局 Job；停止只 Kill 直接进程 | 可解释旧文件残留；存在启动后入 Job 的窗口和未等待整棵 Job 退出的缺口，但未实际复现孤儿进程 |
| Linux | Rename 覆盖，无 `.old-*` | 启动前 Setpgid，Pdeathsig；停止发送进程组信号 | 无同形 stash 累积；没有通用后代为空的等待，不能据此宣称已复现孤儿 |
| macOS | Rename 覆盖，无 `.old-*` | 普通模式发送组信号；launchd 共享组另等 group peers 清空 | 无同形 stash 累积；共享组与普通模式的退出确认能力不同 |

自更新在 `internal/update/apply_prepared.go` 发布主二进制后才调用服务同步。无已安装服务时 `internal/service/service.go` 直接成功返回；有服务时停服务也不会结束正在发起更新的 TUI/CLI。因此立即删除旧映射文件的时机本身就可能早于旧进程退出，不能将一次删除失败直接解释为孤儿进程。

`internal/platform/install.go` 的服务副本同步不使用 `.old-*`，不属于同形 stash 累积来源。`internal/platform/runtime_job_windows.go` 已有命名 Job、ActiveProcesses 查询和整树终止能力，但当前生产启动链未调用其 Create/Open 入口，不能把仅存在的能力视为已生效保障。

上述结论由 `internal/supervisor/command.go`、`child_windows.go`、`child_linux.go`、`child_darwin.go`、`child_mode_darwin.go` 及调用链静态核查得出。清理文件与结束进程是两个行为；延后清理不宣称已修复进程生命周期。

## 必须保留的恢复材料

- Core 的非终态 update journal、candidate、backup、restore、transaction marker、interrupted-update 记录仍可能承担恢复职责。仅 committed/rolled_back 可归档为待清理记录；清理保留原有的对象身份校验。
- Unix 安装的 install-transaction、其引用的候选/服务定义备份/迁移对象，以及 applying 状态的恢复证据，由安装恢复流程管理，不能纳入通用启动扫描。
- 当前目标缺失、事务不可读、身份不一致或与正在更新的进程竞态时，不能因匹配旧文件名就判定可删除。

## 已确认的设计决定

- Q1：本次实现聚焦启动清理。孤儿进程问题只排查、记录证据及后续修复范围，不修改进程生命周期。
- Q2：旧版本及本次范围内的更新残留统一延后到后续启动尝试清理。更新阶段保留必要的回滚保护，不立即尝试删除已退役旧文件；清理失败不改变更新或启动成功，下次启动再试。
- Q3：daemon 启动清理其管理的文件；TUI 打开时处理当前 Mihari 程序旁的残留。普通 status、self version 等 CLI 查询不触发删除。
- Q4：清理覆盖 Mihari/mihomo 旧二进制及已确认完成事务的候选/回滚残留；中断或状态不明的事务材料保留。下载缓存、面板历史版本和用户备份不纳入。

- Q5：允许同次运行内连续更新，接受新增独立的内部待清理记录。保留清理依据、释放当前事务位置，用户配置、CLI 和控制 API 不变，旧版本不会处理新记录。

已确认清理所有权和范围，具体持久化取舍见 [ADR 0006](../adr/0006-defer-update-residue-cleanup.md)。未完成或身份不明的恢复事务不能按文件名通配删除。

## 已确认的具体实施方案

1. Windows 替换完成后保留旧 stash；替换失败时还原旧二进制的行为保持。仅匹配已知目标旁精确的 `.old-<正整数时间戳>` 普通文件，禁止递归删除、跟随链接或触碰当前目标。清理与替换窗口互斥；目标缺失或身份不明时保留残留。
2. daemon 在取得实例所有权、建立诊断历史后清理自己管理的旧二进制及核心事务；TUI 通过 app 用例清理当前 Mihari 程序旁的残留，不直接读取或写入核心业务目录。普通查询命令和安装验证子进程不触发清理。
3. 核心更新提交或健康确认回滚后，不立即 Finish 删除退役对象，而是核对最终核心和事务身份，把精确的终态记录持久保存到该事务目录中的独立 pending-cleanup.json；确认保存成功后才释放当前 journal 位置。若上次归档未完成，下次 BeginUpdate 会重试这个步骤。任何非终态仍按现有恢复规则阻断。
4. 启动清理当前终态事务时复用最终核心身份检查；清理较早的已归档终态事务时，核对归档记录、事务 ID、marker 及每个待删对象身份，不要求当前核心仍等于该历史版本。当前活跃或未决事务引用的材料不得删除。清理顺序及部分失败重试必须覆盖崩溃边界。
5. 不直接复用 `interrupted-update.json` 作为新终态记录：它已有中断恢复语义。单独的待清理记录更容易区分恢复材料与可清理残留；不增加外部配置或控制协议。这项内部持久化扩展已获 Q5 授权。
6. 清理只在本次启动尝试，不启动无限重试循环、不为删除文件结束旧进程。权限、占用或实际 IO 失败以 WARN 写入已有诊断历史，包含目标路径和原始原因；文件 logger 不可用或级别过滤不影响 F2。失败保留待清理依据，在后续启动重试。
7. Linux/macOS 不为统一行为额外制造 Windows 风格 stash；共用核心已完成事务清理语义。已安装状态证明、迁移保留树等仍有用途的材料不属于更新残留，即使所属安装已完成也不泛化删除。

已实现回归覆盖：更新时不删旧文件；Windows 占用失败后解锁并再次启动成功；连续两次核心更新及健康确认回滚；较早事务面对较新核心的清理；非终态/不明身份/链接/非匹配文件保留；归档与部分删除崩溃后重试；daemon/TUI 失败记录进入 F2；CLI 查询无删除副作用。验证结果见下节。

## 已运行验证

Windows 主机执行并通过：

- `go test ./...`，包含开发集成测试。
- `go test -race ./...`；最后的清理边界改动另对 core、app、daemon、tui、update、platform、runtime、cmd/mihari 及 integration 补跑 race。
- `go vet ./...`、修改文件的 gofmt 检查、`git diff --check`。
- `CGO_ENABLED=0` 编译 Windows/Linux/macOS 各 amd64、arm64 共六个目标。
- Linux/macOS 的 core、app 测试二进制交叉编译。

回归测试按 Red–Green 添加，覆盖：连续更新与回滚归档、归档同步失败、每个清理删除边界重试、非终态及身份不符材料保留、空事务目录回收、未决旧 journal 阻止旧核心扫描、Windows 文件占用后解锁重试、链接及无关文件保留，以及 daemon/TUI 启动时序和无文件 logger 时的诊断历史。Unix 的 binary-only stage 测试明确保留未消耗候选及安装恢复 journal，已发布 stage 延后到启动清理。

Windows 占用测试使用真实文件句柄共享限制，未启动真实服务或复现运行镜像/孤儿进程。Linux/macOS 只完成静态核查及交叉编译，原生受保护目录与 root 安装测试未执行；没有操作真实订阅、服务或用户配置。

## 工作区与资料

- 新 worktree：`.worktrees/fix-282-deferred-cleanup`，分支 `fix/282-deferred-cleanup`，从最新 origin/dev 创建。
- 原主工作区 `.gitignore` 修改及既有 `fix-282-startup-cleanup` worktree 的未提交改动均保留。当前分支复用了经审查的 Windows 旧文件清理 helper 与测试；未改写原 worktree。
- 仓库规范引用的 `docs/superpowers/specs/2026-08-03-mihari-architecture-design.md` 在本基线不存在；已阅读现存 `docs/architecture.md` 与仓库规范作为架构依据。
