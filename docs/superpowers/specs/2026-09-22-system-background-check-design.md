# System 后台版本检查与出口选择展示

日期：2026-09-22。

状态：已按确认方案实现，并通过 System 包测试、`go vet ./...` 与 `go test -race ./...`。

## 用户目标

- Core 检查版本更新时，与 Daemon 一样播放 Checking 动画。
- 检查更新不阻断用户操作。
- System → Network → Outbound Interface 不需要一直显示 Saved，选择的模式使用亮黄色。

## 既有约束

- 在独立 worktree 的 `feat/system-background-check` 分支工作，基于 `origin/dev`。
- [出口网卡设计](2026-09-21-egress-interface-design.md)区分已经保存的实例出口选择与弹窗中尚未 Apply 的候选；颜色不能使二者混淆。
- 旧出口设计要求核心停止时可保存、下次启动应用，不自动启动核心；界面不显示 Pending。本次展示调整不改变保存与应用的业务语义。
- 更新检查、实际下载与安装属于不同阶段；检查不占用 mutation busy 锁。

## 设计树与当前问题

- 检查动画：复用 Daemon 的 Checking 视觉，Core 与 Mihari 各自持有 checking 状态，并共用页面动画时钟。
- 非阻塞检查：Q3 已确认，版本检查期间允许全部 System 操作，包括打开出口网卡选择、改设置及发起 Core/Mihari 更新。实际下载、安装、通道切换和设置提交继续采用既有的互斥与防重复控制；后续操作使旧检查结果失效。
- 出口展示：
  - Q1 已确认：System 主页面的 Outbound Interface 不再显示常驻 Saved，也不增加成功后短暂显示 Saved 的提示。
  - Q2 已确认：亮黄色（256 色 228，`#FFFF87`）只标识当前已选定的出口模式；键盘正在浏览、尚未 Apply 的候选继续由焦点样式表达。
  - Q4 已确认：出口网卡弹窗保留 Saved，用于表达已保存选择并与未 Apply 的候选区分；本次只移除 System 主页面的常驻 Saved。

## 文档规则

已确认决定随访谈记录于本文。领域术语复用根目录 CONTEXT.md；只有需要新定义时才补充。此次局部交互可逆，暂不新增 ADR。

## 变更前的行为

以下描述本次改动之前的 System 页。

- Core 检查使用独立 checking 状态，只显示静态 Checking…；同通道成功结果缓存五分钟，进行中的检查去重。见 internal/tui/pages/system/core_check.go。
- Daemon 区域的 Mihari 版本检查通过页面共用的 pending 状态驱动动画；System 页面所有行的 Enter 操作因此被拒绝，导航仍可使用。见 internal/tui/pages/system/model.go 的 checkMihariVersion 和按键处理。
- 离开 System 后检查继续，结果仍投递到该页面；已有 generation 机制防止旧结果覆盖更新准备或通道切换后的状态。
- 主页面 Outbound Interface 带有 Saved 前缀；网卡弹窗另有已保存条目、详情和头部的 Saved 文案。去除主页面常驻状态不应默默扩大为取消弹窗中当前配置与候选的区别。

## 已确认的实现边界

- 两种版本检查使用独立的检查状态和 Checking 动画；检查期间仍允许其他行操作，包括修改设置。
- 实际下载、安装和设置提交继续遵守各自的忙碌与冲突控制，不因检查展示调整而放开重复执行。
- 沿用现有离页后完成检查、Core 五分钟缓存及通道变更后拒绝旧结果的规则；本次不另行改变自动检查频率。
