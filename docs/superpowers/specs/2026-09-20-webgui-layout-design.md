# Web GUI 布局优化

状态：设计已确认并实现，相关回归测试通过。

## 用户要求

- 美化 Web GUI 顶部信息区，先在对话中比较多个 TUI 方案。
- Installing 动画移到 Manage 后面。
- Update 增加 badge，放在 Manage 后面。

## 修改前的状态

- 顶部展示网关地址、默认面板和浏览器会话数。
- 已安装卡片有 Open 和 Manage；未安装卡片只有 Install。
- 安装和重新安装的 Installing 占用卡片顶部状态位；更新尚无对应动画。
- 页面宽度不足 90 列时，面板卡片改为纵向排列。

## 已确认的设计

- 顶部采用 D：Gateway、Default panel、Browser sessions 三行纵向属性表。
- Updating 是更新执行中的动画 badge，放在 Manage 后面。
- Update available 是发现新版的提示，放在 Latest 版本后面，不放在 Manage 后面。
- 首次安装没有 Manage，Installing 放在 Install 后面。
- 重新安装使用 Reinstalling 文案，放在 Manage 后。
- 窄屏放不下 badge 时，整体换到操作按钮下方。
- 成功或失败后立即清除动画；成功刷新版本，失败沿用错误提示及 F2 详情。
- 更新执行中 Latest 保留 Update available；更新成功且版本刷新后显示 Up to date。

相关术语已记录于 CONTEXT.md。此次视觉布局决定易于逆转，暂不创建 ADR。
