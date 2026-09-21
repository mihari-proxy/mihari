---
status: accepted
---

# ADR 0006：Windows 更新退出后由下一次正常启动清理

日期：2026-09-21。关联 [Issue #282](https://github.com/mihari-proxy/mihari/issues/282)。

Windows 无法在旧进程仍映射可执行文件时删除旧镜像。原 TUI 自动拉起新版后等待它退出，会长期保留旧镜像。决定取消 Windows 自更新后的自动拉起：恢复终端后提示“请重新输入 mihari”，当前进程退出；下次正常启动与界面同期执行有界清理，不增加辅助进程。

自身程序副本由 app 维护用例清理；历史 core 文件由 daemon/runtime 清理。TUI 只拥有任务和诊断生命周期。失败详情进入 F2，不弹窗、不阻塞启动。程序副本各自清理自身目录，无清理 journal、跨目录搜索或任意远程删除接口。

更新前可关闭已核实的其他实例，丢弃未提交输入，但须先拒绝新业务操作并等待已接收写入结束。手动 daemon 准备成功后强退并保持停止；已安装服务通过 SCM 停止和重启。无法证明身份、准备或整树退出时中止替换。手动 daemon 另提示按原方式启动，再输入 mihari。

主文件发布与整个应用更新完成分开；现有 `updated=true` 只表示发布，不改变 JSON/退出码。未完成实例退出、旧文件清理或新运行状态验证时，不宣称完整完成。

设计、执行计划与代码在同一 PR 交付，见[实施设计](../investigations/issue-282/implementation-design.md)。Windows 的此决策取代[旧自动 relaunch 等待设计](../superpowers/specs/2026-08-13-windows-tui-relaunch-lifecycle-design.md)；其他平台保持原行为。
