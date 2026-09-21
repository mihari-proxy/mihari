# Issue #282 调查记录

调查基线：`181eaccc`；实现基线：`6a9da72`。关联 [GitHub issue](https://github.com/mihari-proxy/mihari/issues/282)。

## 根因

Windows 替换把目标改名为 `.old-<UnixNano>`，发布 candidate 后立即删除一次。旧进程仍映射镜像时 Windows 拒绝删除，之后没有清理重试。

TUI 原先通过 Windows relaunch 启动新版后 Wait 新进程退出，旧进程因此持续存在。这个等待原本修复了外部 shell 与新 TUI 争用控制台，但连续更新会积累等待链。

旧 Windows supervisor 只结束直接 PID；子孙进程可能继续运行或持有输出管道。正式回归现在要求每代 core 的 Job 终止和退出验证。

正常 core 更新已采用事务存储，不能用旧 `internal/core/replace_windows.go` 推断当前主更新路径。历史 `mihomo.exe.old-*` 清理由 core owner 补偿，正常 journal/backup 不在清理范围。

## 回归证据

临时目录中的合成可执行文件能复现“运行中改名成功、普通删除失败、退出后删除成功”。正式测试：

- `internal/platform/binary_cleanup_windows_test.go`：映射镜像、下次重试、精确名称、链接拒绝、维护锁。
- `internal/platform/update_process_windows_test.go`：Restart Manager PID/创建时间、客户端正常退出与已核验进程终止。
- `internal/supervisor/child_tree_windows_test.go`：立即派生后代、父进程自然退出、主动整树停止及继承管道回收。
- `internal/runtime/update_quiescence_test.go`：在途排空、超时恢复、缓存溢出、嵌套已接收工作与准入拒绝。
- app/server/transport/daemon/TUI 相邻测试覆盖协调、owner lease、真实 pipe、Ready 和诊断任务。

测试不触碰真实服务、用户业务数据或真实 mihomo。正式测试取代仅证明故障存在的临时调查探针。

[实施设计](implementation-design.md) · [执行计划](../../superpowers/plans/2026-09-21-windows-update-finalization.md) · [ADR](../../adr/0006-windows-self-update-finalization.md)
