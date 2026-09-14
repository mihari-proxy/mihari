# Proxies 顶部控制区与 GLOBAL 定位

日期：2026-09-14。用户已确认本方案。
分支：`fix/proxies-mode-layout`，基于 `origin/dev` @ `e2865d0`。

## 已确认的设计

- Mode 和 GLOBAL 共用一张紧凑的 Routing 圆角卡片，与下方代理组卡片左右对齐。两行入口加上下边框共四行，状态说明按需在框内追加一行。
- 复用主题的紫色标题、灰色边框和说明，以及 `RowFocus` 与焦点标记。仅当前内容区的焦点行高亮。长名称按终端显示宽度截断，优先保留状态及操作提示。
- 保留 Mode 的 Enter 弹窗交互、GLOBAL 候选来源与选择行为。
- GLOBAL 入口按 Enter 后展开并聚焦现有 GLOBAL section。能放下一屏时自动滚动到整个 section（含底边框）完整可见；超过一屏时将标题放到列表视口顶部，尽量展示候选，后续方向键继续按现有导航规则工作。
- 下方代理组样式保持现状；固定控制区高度从列表视口扣除。

本设计更新了 [运行模式设计 Q15](2026-09-13-routing-mode-design.md) 对顶部两行入口的视觉取舍。没有新增依赖，没有改变协议、持久化、模式切换或 daemon 写入边界。

## 实现依据

现有 `routingKey` 已设置 GLOBAL 为展开，但 `buildContent` 给组标题的焦点范围仅含顶部边框和标题正文；因此跳转可能只把标题滚动到屏幕底部。显式跳转应请求整个组的渲染范围，普通键盘导航继续使用原有焦点范围。

复用 `ui.RenderBorderedSection`、`FullSectionInner`、`SectionTextWidth` 和 `EnsureLineVisible`。通过 Context7 查阅 [Lip Gloss 官方文档](https://github.com/charmbracelet/lipgloss/blob/main/README.md)，核对了主题样式、边框及渲染宽高测量；实际字符预算与仓库现有卡片组件保持一致。

## 验证范围

- 正常、待生效和未知状态下的卡片边框、提示、焦点移动及失焦样式。
- 窄屏、常规屏、宽屏和包含中文、emoji 的长名称。
- GLOBAL 位于列表后部或末尾时，展开并完整展示；超过视口高度时置顶，继续向下移动仍保持候选焦点可见。
- 更新 Compact / Full 的现有 Routing 顶部渲染快照，运行 TUI 测试、race 与 vet，并检查无 CGO 构建。
- 仅使用本地 fixture；不连接真实订阅、不启动真实 mihomo、不操作系统服务。
