# Darwin launchd 共享进程组停止设计

状态：用户已于 2026-09-08 批准同组方案，正在实现。对应 PR #211 FTWh。取代未实现的 root IPC 停止交接草案，不修改 R4/R3。

## 行为与边界

仅 macOS 已安装服务的 mihomo 继承 daemon 的 PGID。daemon 必须是 launchd 作业组长。Linux/Windows 和 macOS 前台模式保留现有分组。

新增隐藏的 `daemon --launchd-process-group` 参数，只允许与 `--system-service` 同时使用。启动前验证 PID==PGID，装配把 ShareProcessGroup 显式传入 runtime/supervisor。新 plist 使用该参数并明确 AbandonProcessGroup=false。安装器检查实际进程 argv，不能把新写入的磁盘 plist 当作旧进程已支持同组的证据。

仅重启/维护 mihomo 时，对实际持有的主进程发送 TERM/KILL 并等待。Darwin 使用 kqueue NOTE_EXIT 观察退出且不回收，在与信号共用的所有权锁内关闭信号入口，然后在锁外 Cmd.Wait 真正回收，避免已回收 PID 被复用后误杀。不会向共享组发 KILL。未使用 waitid WNOWAIT：Go 在 Darwin 禁用此等待优化，因为 WEXITED 存在收到 STOP 也返回的问题（Go #19314）。

主进程退出后，被动等待共享组只剩 daemon。使用 proc_listpids(PROC_PGRP_ONLY, pgid) 固定两个 PID 槽；XNU 在进程列表锁内复制成员。满两槽是仍有成员；仅一个槽必须是 daemon；短/未知结果拒绝。不能用非原子且无界重试的 sysctl 列表代替。正常退出、重启、健康失败及维护都走相同确认，失败设置 blocked，禁止重启或维护工作。

同组的并发校验/系统辅助进程可能延迟确认；超时拒绝，不枚举并杀死这些进程。确认时不持有它们完成所需的锁。固定 core 的 sing-tun Close 会异步启动 dscacheutil，因此仅等待 leader 不足。不会宣称约束任意主动脱离 PGID 的后代。

## 安装和恢复

已有 ProcessIdentity.Group 保存有界规范 token，绑定 boot/PID/start。安装器 bootout 后用 unix.Kill(-pgid,0) 的 POSIX 语义检查整个组，仅 ESRCH 成功；未知、复用、仍有成员或超时拒绝数据修改。跨 boot 不探测/信号旧 PGID；实际主进程身份丢失后不向历史组补发信号。

2026-09-08 hosted 原生验证确认：即使 AbandonProcessGroup=false，bootout 返回成功且 daemon 退出后，拒绝 TERM 的核心后代仍可能超过 30 秒存活。因此本设计不依赖 launchd 自动清除全部后代；组退出未获证明时，安装必须失败关闭。原生测试直接调用生产身份/组观察能力，确认残留不能获准，再通过仅属于测试夹具的退出控制释放后代并核实整组消失。该控制不作为产品杀进程权限，也不把辅助进程自然到期当作 bootout 的退出证明。

native session.bindState 将备份的旧 Definition/BootID 绑定到 adapter 的独立内存字段，不能被 Inspect unloaded 擦除。Disable、Stop Observe/Replay 和恢复均采用该依据。备份后看到不同 live daemon、旧实际 argv 无标记、同 boot 旧日志缺少组依据均不授权 bootout/迁移，保留恢复文件。

不新增 journal JSON 字段、控制协议、TCP 或依赖。CGO_ENABLED=0。新隐藏参数和现有 Group 字段的新 Darwin 值用于用户批准的同组行为。

## 验收

普通回归先 RED 后 GREEN：组残留不能执行 Maintain、Restart 或 crash/health 自动重启。真实 Darwin helper 证明父子孙同组、仅主进程退出时仍拒绝、全部后代退出后可继续。恢复测试重建 adapter/session，job unloaded 但组非空时不能修改数据或完成事务。覆盖旧实例、身份变化、跨 boot 和 Stop 重放。

仅在隔离 hosted runner 进行 native 验证，不操作工作站真实服务、账户或挂载。完成相关测试/race/vet、六目标 CGO0 构建、普通及两平台 native CI，等待最终 head 的实质 bot review；PR 保持 draft，不自动合并。
