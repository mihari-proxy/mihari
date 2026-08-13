# log / rule / conns 页面行数展示设计

日期：2026-08-13
状态：设计定稿，待实现
关联 issue：mihari-proxy/mihari#50
目标分支：`worktree-log-rule-conns-linecount`

## 1. 背景

TUI 的 Logs、Rules、Connections 三个列表型页面在数据量较大时，用户无法直观感知当前焦点位置与列表总数。长时间滚动浏览日志或连接时缺乏「进度感」，需要反复滚动才能判断剩余内容量；过滤生效时更无法判断命中了多少条。

三页均为手写渲染循环，围绕共享的 `ui.VisibleWindow(count, height, chrome, following, focused)`（`internal/tui/ui/viewport.go:13`）做锚底滚动，不使用 bubbles list/table/viewport 组件，也没有 PgUp/PgDn/滚轮。三页都没有页内状态栏，只通过 `FooterHints()` 把快捷键交给根 shell 的全局底栏。

本设计在三页的表格下方增加一条统一的位置指示行 `X/XXXX`，其中 X 为当前焦点行在过滤后列表中的位置，XXXX 为过滤后可见总数。

## 2. 目标

- 在 logs / rules / connections 三页表格下方提供统一的位置指示行 `X/XXXX`。
- X 反映当前焦点行在过滤后列表中的 1-based 位置；XXXX 反映过滤后可见行数。
- 焦点离开数据行或列表为空时有明确的降级显示。
- 复用现有宽度感知工具（`ui.VisibleWidth` / `ui.PadCell`），保证宽字符对齐。
- 与现有「标题全量计数」语义共存，顺带消解 connections 页标题（全量）与表格（过滤后）的不一致。
- 不改动稳定的控制协议、持久化格式或 daemon 边界。

## 3. 非目标

- 不新增 PgUp/PgDn、鼠标滚轮或其他滚动方式（属另一独立改造）。
- 不改动 Controls 区现有信息布局（logs 的 Unread/Dropped/Stale、connections 的 `[Active N | Closed N]` chip、rules 的过滤/搜索栏均保留原位）。
- 不改动各页标题现有的「数据集全量计数」语义。
- 不新增缓存字段；位置与总数均在渲染期派生，与现有 `visibleEntries` / `VisibleIndexes` / `visibleRows` 一致。
- 不重构与本功能无关的渲染代码。

## 4. 用户界面

### 4.1 位置

每页 List 区卡片紧下方新增一条独立状态行，右对齐 `X/XXXX`，占 1 行页面高度。以 connections 页为例（过滤后 50 条，焦点第 3 行）：

```text
┌ Connections ─────────────────────────────┐
│ [Active 1200 | Closed 35]      sort ▲    │
│ Search: [tcp____________]                │
├──────────────────────────────────────────┤
│ SOURCE    DEST           PROTO     RULE  │
│ 10.0.0.5  cdn.example     TCP      REJ   │
│ 10.0.0.5  api.example     TCP      REJ   │
│ 10.0.0.5  stats.example   TCP      REJ ▶ │
│ ...                                      │
│                                3/50      │
└──────────────────────────────────────────┘
  3 = 焦点行在过滤后列表的位置 / 50 = 过滤后可见总数
```

logs 与 rules 页结构相同：状态行位于各自 List 区卡片紧下方。

### 4.2 降级显示

```text
焦点在数据行          3/50
焦点在搜索栏/列头     —/50
列表为空              0/0
```

`—` 为连字符占位，表示当前无聚焦数据行。connections 因游标是字符串 `rowID`，焦点不在 `focusRow` 时无行位置，必然走此降级；logs / rules 焦点离开行区时也统一走此降级，以保持三页行为一致。

### 4.3 对齐与宽度

状态行右对齐到 List 区内宽 `ui.FullSectionInner(width)`（`internal/tui/ui/section_card.go:14`），使用 `ui.VisibleWidth`（`table.go:389`，底层 `lipgloss.Width`，正确处理 CJK/全角）与 `ui.PadCell`（`table.go:217`）。**不得使用 `ui.RuneCount`**（rune 数 ≠ 显示宽度）。

最坏情形 `10000/10000`（logs 环形缓冲满）约 11 字符，窄终端下不易被截断；状态行不进入根 footer 的优先级丢弃逻辑。

## 5. 指示器语义

| 量 | 定义 | logs 来源 | rules 来源 | connections 来源 |
|---|---|---|---|---|
| X | 焦点行在过滤后列表的 1-based 位置 | `m.focused + 1`（`model.go:35`，且 `m.focus == focusRow`） | `m.focus.row + 1`（`model.go:41`，且 `m.focus.kind == focusRow`） | `rowIndex(rows, m.focus.rowID) + 1`（`model.go:582`，且 `m.focus.kind == focusRow` 且 rowIndex ≥ 0） |
| XXXX | 过滤后可见行数 | `len(m.visibleEntries())`（`model.go:268`） | `len(m.VisibleIndexes())` / `len(m.visibleProviderIndexes())`（`model.go:152` / `:483`） | `len(m.visibleRows())`（`model.go:512`） |

降级规则（三页统一，封装进共享 helper）：

- `total == 0` → `0/0`
- 焦点不在数据行 → `—/total`
- 否则 → `pos/total`

logs 特性：缓冲为固定 10000 的环形（`defaultCapacity`，`model.go:17`），满则覆盖最旧并累加 `dropped`。`XXXX` 是当前缓冲区内**实际保留**的条数（恒 ≤ 10000），不等于自启动以来的日志总量；被覆盖丢失的部分由 Controls 区现有 `Dropped: N`（`model.go:205`）单独体现，二者互补不冲突。

## 6. 架构与接入点

### 6.1 共享 helper

在 `internal/tui/ui/` 新增：

```go
// FormatPositionIndicator formats the list position indicator "X/Total".
// pos is the 1-based focused row index within the filtered list; total is the
// filtered visible count. When the focus is not on a data row, pass focused=false
// to render "—/Total". Empty lists always render "0/0".
func FormatPositionIndicator(focused bool, pos, total int) string
```

三页复用此 helper，避免重复实现降级逻辑与格式化。

### 6.2 状态行注入

每页在 List 区卡片渲染之后、返回视图之前 append 一条状态行，右对齐：

- logs：`View()`（`internal/tui/pages/logs/model.go:193`），`logChrome` 由 8 改为 9（`model.go:296`）。
- rules：`renderRules()`（`model.go:385`）/ `renderProviders()`（`model.go:433`）所在视图，`rulesChrome` 由 9 改为 10（`model.go:383`）。
- connections：`tableLines()`（`internal/tui/pages/connections/render.go:53`）或其 `View()`，`connectionChrome` 由 9 改为 10（`render.go:51`）。

各页 `chrome` 常量 +1 表示状态行占用了一行页面高度，`ui.VisibleWindow` 据此把可视数据行各减 1。

### 6.3 与现有计数的关系

- 各页标题保留「数据集全量计数」语义：rules `Rules · N` / `Providers · N`（`FormatRulesTitle`，`section_card.go:48`）、connections `Connections · N active` / `N closed`（`FormatConnectionsTitle`）、logs 仍为 `Logs`。
- 状态行专注「焦点位置 / 过滤后总数」。两者各司其职：connections 原本「标题全量 vs 表格过滤」的矛盾，由状态行补上过滤维度后自然消解，无需改动标题。

## 7. 测试策略

行为变更采用 Red–Green–Refactor。

### 7.1 共享 helper 单测（`internal/tui/ui`）

- 焦点在行：`FormatPositionIndicator(true, 3, 50)` → `3/50`。
- 焦点不在行：`FormatPositionIndicator(false, 0, 50)` → `—/50`。
- 空列表：`FormatPositionIndicator(false, 0, 0)` → `0/0`。
- logs 满量：`FormatPositionIndicator(true, 10000, 10000)` → `10000/10000`。

### 7.2 三页渲染测试

- 焦点在数据行时状态行显示 `pos/total`，焦点移动后 X 实时变化。
- 过滤生效时 XXXX 跟随过滤后总数变化。
- 焦点切到搜索栏/列头时显示 `—/total`。
- 空结果集显示 `0/0`。
- 三页 golden 快照（`internal/tui/pages/connections/render_test.go`、`internal/tui/golden_test.go` 及 logs/rules 对应快照）因新增一行需同步更新。
- 现有 `TestModel_DefaultBufferCapacityIsTenThousand`（`logs/buffer_test.go:36`）等不受影响。

### 7.3 回归验证

```console
go test ./internal/tui/...
go test ./...
go vet ./...
gofmt -l cmd internal
golangci-lint run
```

## 8. 文档影响

实现完成后按需更新 `README.md` / `README.zh-CN.md` 的 TUI 能力说明，以及 `docs/architecture.md` 中三页渲染相关描述（仅当用户可见行为实际变化）。

## 9. 验收标准

- logs / rules / connections 三页表格下方均存在右对齐的 `X/XXXX` 状态行。
- 焦点在数据行时 `X` 为过滤后列表中的 1-based 位置，上下移动时实时更新。
- 过滤生效时 `XXXX` 跟随过滤后可见总数。
- 焦点离开数据行时显示 `—/total`；空列表显示 `0/0`。
- connections 标题全量计数与状态行过滤后计数并存且语义清晰。
- chrome +1 后各页可视数据行正确、滚动行为无回归。
- 宽字符下列对齐正确（使用 `VisibleWidth` / `PadCell`）。
- 新增 helper 与三页渲染均有测试，golden 快照同步更新，`go test` / `vet` / `gofmt` / `golangci-lint` 全绿。

## 10. 已选方案与否决方案

采用「表格下方独立状态行（方案 B），仅右对齐 `X/XXXX`，状态行不承载其他页状态」。

否决：

1. **页标题里追加（方案 A）**：最低改动、不占数据行，但用户更看重独立醒目的位置指示；且 logs 标题本无计数、rules/connections 标题已有全量计数，强行塞入会混淆语义。
2. **根底栏（方案 C）**：复用 `FitFooter`，但根底栏预算紧张（`width-2` 且主动丢段），`X/XXXX` 在窄终端易被挤掉。
3. **状态行左侧顺带整理页状态**：信息密度更高，但改动面与 golden 快照变动显著扩大，超出「增加行数展示」的需求范围；现有 Controls 区信息布局保持不动。
