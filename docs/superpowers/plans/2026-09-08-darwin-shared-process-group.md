# Darwin Shared Process Group Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task.

**当前状态（2026-09-08）：** 用户已改选 [R5 三平台安装中断提示与重新安装策略](../specs/2026-09-08-interrupted-install-user-choice-design.md)：检测后提示，提供保留数据的修复安装和明确清除数据的全新安装，不自动恢复中断事务。下方共享组执行记录保留；R4 自动恢复、重放及 pending source 授权任务暂停，不按历史计划继续实施。登记、安全停机和三平台显式重新安装接管需要按 R5 完成具体设计后重写实施计划。本轮只修改文档，不改代码、提交或推送。

**R5 已确认边界：** 安装完整与服务启动失败分别判定；有效执行者/未知权限状态不误报中断；修复入口独立于旧程序；保留用户数据且不自动切换全新安装；重复中断仍由用户选择。未来验收须覆盖安装完整提交后 Ready 失败或再次断电，不得将其误报为安装事务中断。

**后续审核入口：** [三平台实现设计](../specs/2026-09-08-install-repair-implementation-design.md) 与 [新实施计划](2026-09-08-install-repair-implementation-plan.md)。本文件下方仅保留历史执行记录；其“不得修改 Windows/Linux 分组”等旧阶段约束不能替代新方案的三平台审查要求。

**Goal:** 修复 PR #211 macOS 停机证明缺失。

**Architecture:** launchd 服务的 mihomo 继承 daemon PGID；core 重启只信号直接持有的主进程，再确认组只剩 daemon。安装器保留 durable 组身份，bootout 后确认整个组不存在。

**Tech Stack:** Go 1.26.5、x/sys v0.46.0、Darwin libSystem/launchd、现有 hosted native CI。

**Spec:** `docs/superpowers/specs/2026-09-08-darwin-shared-process-group-design.md`

## Global Constraints

- 用户已批准同组方案；原 root IPC/journal 回执方案不实施。
- 无新增依赖；CGO_ENABLED=0；不改变 Windows/Linux 分组。
- 不修改 R4/R3、CHANGELOG 或工作站系统服务。
- 不用仅 leader 消失或未知状态证明整组退出。
- 先运行回归 RED，再实施最小实现并验证 GREEN。

## Task 1: Darwin 原生组观察与不回收等待

Files: 新建 internal/platform/process_group.go、process_group_test.go、process_group_darwin.go、process_group_darwin.s、process_group_darwin_test.go。

Interfaces:
```go
func DarwinGroupHasPeers(context.Context) (bool, error)
func DarwinWaitChildExit(pid int) error
```

- [ ] 固定两槽纯判定 RED：0槽、非4字节倍数、仅self、满两槽、单槽非self、错误/cancel。
- [ ] 实现 proc_listpids libSystem trampoline；验证自身 PID==PGID。满两槽返回true，唯一self返回false，其他短/未知结果error。
- [ ] 实现指定子进程 kqueue NOTE_EXIT 观察，EINTR重试，不回收；覆盖注册前已退出和 STOP 不误判，核对 Apple ABI 后 CGO0 编译。
- [ ] Darwin helper 自建组，验证 child/grandchild 残留和退出，不移动/信号测试主进程及 shell。

## Task 2: Supervisor 退出证明

Files: internal/supervisor/command.go、supervisor.go；新增 child_mode_darwin.go、child_mode_other.go、supervisor_tree_test.go。

Interfaces:
```go
// CommandStarter gains ShareProcessGroup bool.
type descendantWaiter interface {
    WaitDescendants(context.Context) error
}
```

- [ ] RED：child Wait 已完成而后代确认报错时，Maintain 不执行 work，Restart 不再 Start；正常 crash 退出也不能自动拉起。
```go
err := s.Maintain(ctx, func() error { commits++; return nil })
if err == nil || commits != 0 {
    t.Fatal("descendant exit failure allowed maintenance")
}
```
- [ ] 共享模式启动时不设置 Setpgid；Darwin wrapper 通过 kqueue NOTE_EXIT、互斥的信号入口和 exited 状态保护实际 Cmd.Wait，非共享模式保持现有方法。
- [ ] 在 stopChild 和正常退出路径调用带 StopTimeout 的 WaitDescendants；错误进入 blocked。blocked 同时响应 Restart/Maintain，禁止 backoff 工作绕过。
- [ ] 运行 go test 和 go test -race ./internal/supervisor。

## Task 3: 显式服务模式装配

Files: internal/cli/daemon.go、root.go 及测试；cmd/mihari/unix_layout.go、main.go、Darwin/Linux 辅助文件；internal/app/runtime.go 及测试。

Interfaces: cli.Dependencies.RunLaunchdServiceDaemon func(context.Context) error；RuntimeBuildOptions.ShareProcessGroup bool -> CommandStarter 同名字段。

- [ ] CLI RED：缺 system-service、显式 false、validation 混用、无平台能力不能启动；合法组合只调用专用回调。
- [ ] 新 hidden --launchd-process-group；macOS 回调在业务前校验组长，Linux 不提供能力。
- [ ] 专用回调保留 RunUnixSystemService/activation gate，通过装配字段传递共享模式，不使用全局变量。
- [ ] 验证前台false/服务true并运行 cli/app/cmd 测试及跨编译。

## Task 4: Launchd 组身份和恢复

Files: internal/service/definition_build.go、definition_parse.go、definition_launchd.go、definition_darwin.go、definition_recovery.go 及测试；internal/app/install_native_session_unix.go 和恢复测试。

Interface:
```go
// Pure in-memory binding, implemented only by LaunchdAdapter.
BindStopAuthority(Definition, string)
```

- [ ] RED：PID消失但组非空、Inspect擦除、不同live实例、旧无标记实例、Stop Observe/Replay均不得放行。
- [ ] plist添加hidden标记和AbandonProcessGroup=false；parser只接受精确合法argv。
- [ ] 有界解析实际 KERN_PROCARGS2，仅解析argc个argv，不扫描环境；前后核对内核身份。Group token严格绑定boot/PID/start。
- [ ] Wait只以POSIX kill0 ESRCH判空；跨boot不访问旧PGID；不向失去身份的组信号。
- [ ] bindState注入旧备份；拒绝未持久化的新身份；恢复原组非空时不写数据、不完成事务。
- [ ] 运行 service/app/integration 单元、race/vet、native fixture。

## Task 5: 集成和复审

- [ ] 登记并运行 required native helper/恢复回归。
- [ ] 检查准确diff、gofmt、相关测试/race/vet和六目标CGO0构建。
- [ ] 独立意图的中文 Conventional Commits，git commit -s，推送当前功能分支。
- [ ] 等待最终head普通和native CI，读取sanitized result.json required checks和cleanup。
- [ ] 回复已证实修复线程，集中触发一次bot review，等待完成并修复实质发现。
- [ ] 更新PR真实状态；保持draft、不合并。

## Execution record

- Tasks 1–3: implemented with RED/GREEN regressions. Darwin uses kqueue NOTE_EXIT rather than waitid; fixed-size proc_listpids observes remaining members. Windows full tests/race/vet passed; Darwin native execution remains pending.
- Task 4: implemented Group binding, actual argv/identity observation, passive group drain, Stop replay and recovery guards. Review identified an unresolved availability gap: a completed journal cannot prove the final daemon generation exited, and cold/stopped updates need durable startup registration. The guards refuse unknown state instead of inventing a Group.
- Task 5: new supervisor/recovery/Darwin helpers are required by native evidence verification. A hosted-only real launchd job tests TERM-resistant descendants; its independently recoverable ledger is checked during always cleanup, including unknown PGID refusal.
- Local verification: Windows go test ./..., go test -race ./..., go vet ./... passed; Python evidence/cleanup tests 66 passed and 4 platform-only skips; six CGO0 production targets and Linux/Darwin arm64 tagged native packages compiled. Latest recovery edits also require the owning agent's targeted verification.
- Current changes are uncommitted and unpushed. Remote 6f1c070 CI success does not certify these changes or close FTWh. No merge-ready claim.
- Supplemental design: [macOS runtime handoff addendum](../specs/2026-09-08-darwin-runtime-group-handoff-addendum.md). R3's historical review closed two R2 findings but did not cover the full production startup/Manager authorization chain. R4 now specifies the trusted selector, source/target checkpoint continuity, later Stop invalidation, registration-backed in-memory business authorization, tail-only recovery after business handoff, lifecycle hash inheritance and actual control-plane readiness. R4 is awaiting user review; the runtime record, start gate and complete startup authority remain unimplemented. This document-only revision does not certify local code or CI.

## R4 历史补充工作边界（已被 R5 取代，不执行）

以下仅映射补充设计至未来实施任务，不替代获批后的详细实施计划：

1. **可信启动选择（R4 §7.1、§7.4）**：提取 native metadata 的纯校验；新合同服务选择 source/target 的定义、binary hash 和布局。普通 pending 拒绝回归保持。首次候选选择不产生业务授权。
2. **登记与 gate（R4 §2–§6）**：固定全局目录原子初始化、永久锁、严格两状态记录、整组清空和同步失败路径。继续复用已有 kqueue/组观察实现。
3. **恢复终点与重放（R4 §7.2–§7.3）**：Start/RestoreStart 之前完成资源恢复；更晚 Stop intent 作废旧检查点，包括已经 unloaded 的幂等停机；交还过运行权后仅收尾或停机后重试启动，不覆盖新业务数据。新 lifecycle 继承已完成的对应 hash。
4. **完整业务授权（R4 §7.5）**：登记后签发 app 非导出的授权实现，经 cmd/daemon/app options 传至 runtime/Manager。credential、provider recovery、core validation 和全部 worker 都在授权之后，真实 phase 不伪装。
5. **实际运行与收尾（R4 §7.6、§9.1）**：直接 Start、重放、done 和运行中幂等分支验证同代登记/live 身份、业务控制面和 Enabled 策略；hosted Darwin 使用真实 daemon、临时业务状态和 fake core 证明恢复可用与退出清理。

补充计划须逐项包含 RED 原因、GREEN 外部可观察结果、文件/接口、故障注入和相应验证范围。尤其不可用只调用纯 selector、伪造允许授权、fake manager.running 或 launchd job loaded 来替代端到端恢复验收。PR 持续 Draft，不合并；所有新验证结论必须对应实际最终 head。
