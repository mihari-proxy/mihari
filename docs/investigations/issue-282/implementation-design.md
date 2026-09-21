---
status: implemented
---

# Windows 自更新退出与启动清理设计

关联 [Issue #282](https://github.com/mihari-proxy/mihari/issues/282)，决策见 [ADR 0006](../../adr/0006-windows-self-update-finalization.md)。实现基于 dev 的 `6a9da72`，设计和代码在同一 PR 交付。

## 用户行为

Windows TUI 更新发布新版后恢复终端，退出并提示 **“请重新输入 mihari”**，不自动拉起新版、不增加辅助进程。新版正常 TUI 或 daemon 启动后并行清理本副本的旧文件；清理不阻塞界面或 SCM Ready。失败原文进入 F2 诊断历史，不弹窗、不令启动失败，也不受文件日志开关或级别影响。

已核实的其他客户端先请求退出，1 秒后仍未退出可强制结束；未提交输入丢弃。相关 daemon 先拒绝新业务写入并等待已接收工作结束，准备超时则在替换前中止。手动 daemon 在准备成功后强退并保持停止，另提示先按原方式启动 `mihari daemon`。系统服务由 SCM 停止、同步和重启，并验证新版 daemon 版本。

`updated=true` 继续只表示主二进制已发布，既有 JSON envelope 和退出码不变。当前 updater 的镜像可能仍被映射，清理和新运行状态未收尾时不能据此宣称完整更新完成。无持久化完成凭证，也不推断其他目录副本的状态。

## 所有权与调用链

| 工作 | 所有者 |
|---|---|
| 自身程序目录清理 | `internal/app.CleanupApplicationBinary` 调用 platform 文件能力 |
| TUI 后台任务、取消、回收、F2 | `tui.Run` |
| data/bin 历史 core 残留 | daemon/runtime 的 `CleanupOldCore` |
| mutation 准入与在途计数 | daemon/Manager |
| 目标锁、实例发现与停止、服务恢复 | Windows app 更新用例 |
| core 每代进程树 | supervisor 的 Windows child |
| daemon 整树验证 | app 持有并核验 daemon 的受保护 Windows Job |

TUI 不删除业务文件；daemon 不接受客户端指定的任意删除路径。没有新增依赖、常驻清理服务、清理 journal 或网络监听。

## 更新顺序

1. 现有 updater 下载、校验固定候选，取得目标预览和确认，并在目标目录完成 staging。
2. app 按目录稳定排序取得 `.mihari-binary.lock`，复验目标文件和持久服务定义。通过 Restart Manager 枚举实际使用预览目标的进程，持有进程句柄，并校验 PID、创建时间、镜像与 SID。
3. 已连接 daemon 必须属于这些目标且声明准备能力。其他实例必须有通过身份/ACL 校验的客户端退出事件；未知旧实例要求先正常关闭，不能按进程名全杀。
4. 通过认证本地 IPC 排空 daemon 已接收的业务工作，随后取得并校验其受保护 Job 的成员关系。
5. 请求其他客户端退出，必要时结束已核实进程。手动 daemon 终止整个受管 Job；服务经 SCM stop。确认相关进程退出及 Job 的 ActiveProcesses 为零，再次检查是否有新实例出现。
6. updater 再次验证预览并发布候选。既有服务同步流程验证服务目标、启动服务并确认新版版本。失败保留已发布事实；停止过的服务在 lease 释放时尝试恢复。
7. 释放门控、Job/进程句柄、退出事件和文件锁，当前 TUI 输出提示后退出。自己的旧镜像由下一次正常启动清理。

新实例仍可能在最终观察后由外部启动；Windows 替换/删除结果继续按实际 OS 结果处理，残留留给下一次清理，不将一次枚举当成永久无占用证明。

## 最小本地准备协议

Windows 增加能力 `app-update-prepare-v1`：

- `POST /v1/app-update/prepare` 接收 `operation_id`；返回原有 schema、操作 ID、daemon PID/创建时间/镜像/SID 及受保护 Job 名称。
- `DELETE /v1/app-update/prepare/{operation_id}` 由同一真实调用进程释放准备。
- 只走原有 bearer 认证的 named pipe，不映射到 Web gateway，不提供任意 PID kill 或路径删除接口。

调用者须有提权 token；普通控制 token 不足以关闭全局写入。管理员本身已具备本机停服务权限，协议不扩大该权限。app 另将返回的 daemon 身份与实际更新目标关联。服务端不相信请求正文自报 PID，也不把调用者镜像路径作为授权依据。

传输层从持有的 pipe 获取客户端 PID，再打开进程句柄。仅接受创建时间不晚于本次 Accept 开始的进程，避免已退出客户端的 PID 被后来进程复用。合法的新调用者可能需重新连接一次；服务端要求关闭连接，客户端最多重试一次。该判断依赖当前固定 go-winio 的 Accept 在调用后才创建可连接 pipe；依赖升级时须复核。

每次实际 mutation 执行独立登记，缓存命中和等待同一结果不重复计数，缓存超过 256 项的执行仍计数。内部 core 启动准备、启动回调和日志同步的锁外工作也纳入。先关闭入场，等待已接收工作，再取得提交锁确认静止；30 秒 drain 超时恢复准入。只读状态和诊断继续可用。

成功交付的许可不因 HTTP 断开、请求结束或 TTL 自动失效；同一 owner 显式释放，或持有的内核句柄证明 owner 已退出时，才恢复准入。watcher 随 daemon 取消和回收；观察错误不能误当退出证明。

提权 daemon 在启动任何子进程前加入独立、禁止 breakaway、受 SYSTEM/Administrators ACL 保护的外层 Job，句柄归整个进程生命周期。普通权限手动 daemon 保持可运行，但不声明可验证的整树准备能力；更新前需要先正常关闭。旧 daemon 同样不声明能力，不能假装已 drain。

## 文件清理边界

只处理当前程序目录中精确的 `<basename>.old-<规范正十进制 int64 时间戳>`；core owner 只处理已知 `data/bin/mihomo.exe.old-*`。这些名字保留为更新残留名称空间，名字并不构成历史事务来源证明。

Windows platform 固定本地卷和各级目录句柄，拒绝 reparse 跳转并在持锁期间禁止目录重命名；验证最终目录和锁 ACL。LocalSystem 只接受现有 ACL 中唯一的具体数据用户，以及既有管理员/SYSTEM 信任者，不改 ACL 或接受宽泛组写入。

枚举非递归，最多 4096 项；清理轮次 2 秒。文件通过父目录相对句柄打开，拒绝目录、reparse 和多硬链接，再按同一已核验句柄执行普通删除。被映射镜像仍由 Windows 拒绝删除；保留原始失败供 F2 查看，下次启动重试。不删除当前程序、candidate、core journal/backup、配置、订阅或其他副本目录。

更新和清理共用目标目录锁，避免删除本次 rename 回滚 stash。core 清理还经过 runtime mutation 锁；正常 core 更新仍使用原有事务，不改持久化格式。锁文件为空，卸载白名单允许 `bin/.mihari-binary.lock`。

## Windows 进程树

每代 core 使用独立匿名 Job，创建进程时挂起，在执行用户代码前加入 Job，然后恢复唯一初始线程。绑定失败回收所创建的进程；没有 Start 后已经派生后代的窗口。

停止时终止该代 Job，确认 ActiveProcesses 为零。Wait 先观察直接进程退出，再终止剩余后代，最后等待 exec 输出管道回收，避免孙进程继承管道导致 Wait 卡住。外层 daemon Job 供更新器证明并等待整个旧运行树，内层 core Job 用于不影响 daemon 的 core 重启。

客户端退出事件名绑定 PID 和创建时间；提权客户端事件仅管理员/SYSTEM 可写，updater 校验 ACL，防止普通权限进程伪造提权 daemon 的“可直接关闭客户端”标记。

## 兼容与验证范围

旧版发起第一次升级仍执行旧 updater，可能保留旧 TUI 等待链。新版清理失败会进入 F2；退出旧会话后手动运行新版可重试。不能追溯赋予旧版新协议保证。

自动测试只用临时目录、合成进程和 fake 服务；不修改开发机 SCM，不访问真实订阅或真实 mihomo。Windows 原生覆盖映射旧文件、删除互斥、链接拒绝、Restart Manager 身份、客户端退出以及子进程树。真实提权 SCM/控制台验收仍需要独立授权的隔离 testenv。执行记录见[计划](../../superpowers/plans/2026-09-21-windows-update-finalization.md)。
