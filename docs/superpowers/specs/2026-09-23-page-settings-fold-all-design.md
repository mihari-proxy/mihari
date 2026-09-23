# Page Settings 默认全部展开与全部折叠

日期：2026-09-23。

状态：已确认，按本文实现。

## 用户目标

- Page Settings 增加「展开全部」和「折叠全部」。
- 每次打开时，八个页签都展开。

## 既有约束

- 行为改在 `internal/tui/page_settings.go` 的现有弹窗上，不新增协议、CLI 或偏好字段。
- 折叠状态只存在于这一次打开，不写入 daemon 偏好。
- 全局 `?` 帮助不收录这两个键，与现有 Ctrl+S 一样只出现在弹窗底栏。
- 不修改 `CHANGELOG.md`，也不回写 `docs/superpowers/specs/2026-09-20-proxies-auto-latency-design.md`。该文保留「打开时只展开来源页」的历史决定。
- 弹窗可操作的最小窗口仍是 72×22。更小只显示 “Terminal too small”，只有 Esc 有效。

## 已确认的设计

- Q1：`]` 展开全部八个页签，`[` 折叠全部八个页签。`Ctrl+[` 在终端里等于 Esc，不能用作折叠键。
- Q2：每次打开，八个页签全部展开，包括七行 “No settings available yet”。焦点停在来源页标题，现有滚动保证该标题可见。
- Q3：目录、配置列表、Cancel、Save 四个区域都能按这两个键。保存进行中忽略，与 Ctrl+S 相同。草稿和左侧圆点不动。`]` 不移动光标。`[` 如果光标在某个设置项上，落到该页签标题；已经在标题上则留在标题上。右侧标题的 Enter / Space 仍只切换那一节。左侧 Enter 仍只展开目标节并跳过去，其他节保持原样。
- Q4：底栏显示 `] Expand all` 与 `[ Collapse all`。一般提示为 `Tab area  ↑/↓ move  Enter select  ] Expand all  [ Collapse all  Esc cancel`。正在调整测速并发数时为 `←/→ adjust (1–50)  Tab area  ] Expand all  [ Collapse all  Esc cancel`。72 列放不下时，从 `] Expand all` 起换到下一行，并少显示一行列表。
- 保存：Ctrl+S 和 Save 不关闭弹窗。有改动时播放橙色 Saving badge，成功后显示绿色 Done，失败显示红色 Failed 和单行错误详情。没有改动时不请求 daemon，直接显示 Done。Esc 或 Cancel 关闭；保存进行中忽略按键，包括 Esc。

## 文档规则

已确认决定记录于本文。此次交互可逆，不新增 ADR。
