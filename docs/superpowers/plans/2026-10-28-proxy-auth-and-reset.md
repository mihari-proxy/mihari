# 代理认证与业务重置实施计划

依据已批准的 [设计](../specs/2026-10-23-proxy-authentication-design.md)。所有业务写入由 daemon 执行，使用现有认证本地协议，不增加依赖。

1. 配置与生成：可选 proxy-auth 设置；按用户名合并；认证/免认证模式；回环 CIDR 冲突；不可共享可变凭据。先证明旧生成器缺少合并和空用户拒绝行为，再实现。
2. 运行时：复用候选配置校验、原子发布、reload/失败恢复；慢校验外移，提交重查 generation、订阅缓存 hash、catalog 与 core epoch；不关闭已有连接。内核缺失时只校验生成规则并保存。
3. 本地协议与 CLI：只通过认证 /v1/proxy-auth 和 /v1/data-reset；查询默认隐藏密码，显式 reveal；proxy-auth 命令组使用隐藏输入或 stdin，整体替换请求绑定 revision；两次确认及 digest 绑定完整重置。
4. TUI：Ports & Authentication 区域添加独立认证弹窗；用户/网段滚动列表、模式、密码遮罩与显式显示；断线与异步结果用 epoch 隔离。Maintenance 提供预览和两次确认。
5. 重置：固定业务文件白名单，路径必须位于 data root，拒绝链接及目录替代；仅清除匹配受管 ID 的订阅 cache、设置/运行/偏好/面板选择及 core profile cache。保留程序、服务、资源、日志/导出、控制凭据和未知文件。
6. 恢复：执行前再次比较预览 digest；supervisor.Update 停止并回收子进程后才清除，保持 stopped。business-reset.state 持久区分 clearing 与 setup；中断清理在 daemon 启动时继续；Setup 屏障阻止未完成初始化的自动启动。启动降级只开放认证重置及业务数据重置，不启动内核/Web，也不开放其他业务 mutation。
7. 验证：有失败证据的定向单元测试；跨包 IPC/persistence/confirmation/reset 与 fake mihomo 子进程测试；全仓 test、race、vet、lint、无 CGO 跨平台构建；同步帮助文档及有意变更的 golden。
8. 提交 PR 至 dev；轮询当前提交的 CI/review，逐项修复并再次验证，不修改 CHANGELOG，不合并 PR。

实际测试证据见 PR 描述；不执行真实订阅、真实 mihomo、真实系统服务操作或工作站业务数据清理。
