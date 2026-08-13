# TUI 连接页 Traffic 列固定宽度设计

日期：2026-08-13
状态：设计中
关联 issue：mihari-proxy/mihari#71
目标分支：`fix/issue-71-conns-traffic-width`
前序：状态栏同类修复 #67 / PR #68（`2026-08-13-statusbar-fixed-rate-width-design.md`）

## 1. 问题

连接（Connections）页表格的 `Traffic` 列在单连接速率较高时，速率的**数字部分被 `…` 截断**，只剩单位：

| 场景 | 实际值 | 当前显示 |
|---|---|---|
| **默认 100 列**（traffic 实际 19 宽，slot 8） | `↑1.0 KiB/s` | `↑… KiB/s` |
| 默认 100 列、≥100 MiB/s | `↑999.9 MiB/s` | `↑… MiB/s` |
| 列宽 24（slot 11）、≥100 MiB/s | `↑999.9 MiB/s` | `↑999… MiB/s` |
| 窄列 16（slot 7）、任何 KiB/s+ | `↑1.0 KiB/s` | `… KiB/s` |

注意：`MaxWidth: 24` 是上限，但默认 100 列 5 列布局下 `FitColumnWidths` 因 host `Flex: 3` 拿走大部分剩余空间，traffic `Flex: 1` 实际只分到 **19 宽**——连 KiB/s 级速率都被截断，这是最常见的触发场景（非极端高速）。

截图触发：用户在高速下载（数百 MiB/s）下看到 `↑999… MiB/s`。

## 2. 根因

`Traffic` 列规格（`internal/tui/pages/connections/render.go`）：

```go
"traffic": {MinWidth: 16, MaxWidth: 24, Flex: 1, Align: ui.AlignRight, Priority: 10},
```

列宽 24 → `RenderTrafficColumn`（`internal/tui/ui/table.go`）对半切：

```go
slot := max(1, (w-2)/2)   // w=24 → slot=11
up   := RenderTrafficSlot(theme.Success, "↑", upRate, slot)
down := RenderTrafficSlot(theme.Info, "↓", downRate, w-slot-2)
```

每个槽 11 列。最大常见速率串 `↑999.9 MiB/s` 占 **12 列**：

```
↑(1) + 999.9(5) + 空格(1) + MiB/s(5) = 12
```

`RenderTrafficSlot` 的契约是「单位右锚定、先截数字、绝不断单位」，于是多出的 1 列从数字末尾截：

```go
body := TruncateVisible(marker+digits, bodyW)   // bodyW = w - (unit宽+1) = 11-6 = 5
                                                  // "↑999.9"(6) → "↑999…"(5)
```

窄列更甚：槽 7 时 `bodyW = 7-6 = 1`，`↑1.0` → `…`，数字全没。

**现有测试固化了这个截断**：`internal/tui/ui/table_test.go` 的 `TestRenderTrafficColumn_UnitAnchoredRight` 断言 `↑100…` 出现、`TestRenderTrafficColumn_NarrowSlotTruncatesDigits` 断言槽 7 出现 `…`。

## 3. 目标

- `Traffic` 列在**被展示时**宽度足以容纳完整的 `↑999.9 XiB/s ↓999.9 XiB/s`，数字不再被截断。
- 宽度不足以完整显示时，**整列丢弃**（`Priority: 10` 已是最高、最后丢），而非展示残缺值。
- 与已合并的状态栏修复（#67 / #68）思路一致：固定槽宽，宁可丢弃也不残缺。

## 4. 非目标

- 不改 `RenderTrafficSlot` / `RenderTrafficColumn` 的底层截断逻辑——窄槽先截数字是正确的防御性渲染，调用方负责传入足够宽度。
- 不改 `FormatRate` / `formatIEC` / `FormatBytes` 全局契约。
- 不改 `FitPriorityColumns` / `FitColumnWidths` / `PriorityBar` 算法。
- 不紧凑化 traffic（保留 IEC 全格式 `XiB/s`，不学状态栏 compact 的 `999.9M`）。
- 不影响状态栏（#68 已修）、overview 卡片、连接详情的速率渲染。

## 5. 方案比较

### 5.1 采用：traffic 列固定宽 26（MinWidth=MaxWidth=26）

只改列规格两个数字，`RenderTrafficColumn` 在 w=26 时 slot=(26-2)/2=12，恰好容纳 `↑999.9 XiB/s`（12）。Flex 保留为 1（Min=Max 时 Flex 无实际作用，但保留字段一致性、零额外风险）。

```go
"traffic": {MinWidth: 26, MaxWidth: 26, Flex: 1, Align: ui.AlignRight, Priority: 10},
```

- 优点：最小改动（2 个数字）；语义干净——「要么完整、要么丢弃」；底层函数不动，无回归面。
- 代价：窄终端下 traffic 占 26 会挤掉其它列，极窄时 traffic 自身被丢（详见 §6）。

### 5.2 不采用：仅提高 MaxWidth 24→26（MinWidth 保持 16）

满列时完整，但窄列（16–25）仍截断。违背「确保宽度」的语义——用户要的是不残缺，不是「宽时凑合、窄时残缺」。

### 5.3 不采用：traffic 列改用紧凑格式（`formatCompactIEC`）

`↑999.9M` 槽 7 即可。但：
- 状态栏设计文档（§4.2）已否决「改全局 FormatRate」，并明确 compact 是状态栏专属；
- 丢掉 `/s` 与 IEC 精度，与连接页「可读速率」诉求不符；
- 仍需调列宽，不比 5.1 简单。

## 6. 布局影响分析

`avail = SectionTextWidth(FullSectionInner(layoutWidth)) - 2`，gap=2。默认列集 `[host, traffic, network, rule, start]`，各 MinWidth 12/26/10/12/10。

| 终端宽 | avail | 改后保留列 | 改前保留列 | 差异 |
|---|---|---|---|---|
| 100 | 92 | 全 5 列（traffic=26 完整） | 全 5 列（traffic=19，连 KiB/s 截断） | traffic 完整 |
| 80 | 72 | host/traffic/network/rule | 全 5 列 | 丢 start |
| 60 | 52 | host/traffic/network | host/traffic/network | 同（traffic 由截断→完整） |
| 50 | 42 | host/traffic | host/traffic/network | 多丢 network |
| 40 | 32 | host（traffic 被丢） | host/traffic | traffic 自身被丢 |

- 100 列（默认/典型）：所有默认列齐全，traffic 完整，**无负面代价**。
- 80 列：丢 `start`（开始时间）换 traffic 完整——时间列重要性低于实时速率，可接受。
- ≤50 列：逐级丢尾部列；traffic 优先级最高，最后丢。极窄（≤40）时 traffic 自身被丢，符合「不残缺」。
- `FitColumnWidths` 分配验证（100 列，budget=84）：host=17, traffic=26, network=11, rule=15, start=15，总和 84+gaps 8=92 ✓。

## 7. 详细设计

唯一代码改动点：`internal/tui/pages/connections/render.go` 的 traffic 列规格。

```diff
- "traffic": {MinWidth: 16, MaxWidth: 24, Flex: 1, Align: ui.AlignRight, Priority: 10},
+ "traffic": {MinWidth: 26, MaxWidth: 26, Flex: 1, Align: ui.AlignRight, Priority: 10},
```

渲染路径不变：`renderConnection` → `ui.RenderTrafficColumn(theme, upRate, downRate, widths[index])` → `ui.PadCell(...)`。`widths[index]` 现为 26（满列），`RenderTrafficColumn` slot=12，`↑999.9 XiB/s` 完整。

## 8. 测试策略

Red–Green–Refactor。底层与集成两层覆盖。

### 8.1 底层（`internal/tui/ui/table_test.go`，保留 + 新增）

- **保留** `TestRenderTrafficColumn_UnitAnchoredRight`（w=24）：底层在 24 列仍截断 `↑100…`，验证防御渲染未变。
- **保留** `TestRenderTrafficColumn_NarrowSlotTruncatesDigits`（w=16）：窄槽截断行为不变。
- **新增** `TestRenderTrafficColumn_FullRateFitsAt26`：w=26 时
  - `RenderTrafficColumn(theme, "999.9 MiB/s", "1.5 GiB/s", 26)` 可见宽 = 26；
  - 包含完整 `↑999.9 MiB/s` 与 `↓1.5 GiB/s`，**不含** `…`；
  - 两行单位列对齐（沿用现有测试的单位锚定断言法）。

### 8.2 集成（`internal/tui/pages/connections/`，列规格）

- **新增** traffic 列规格断言：`connectionColumns()` 中 traffic 的 `MinWidth==26 && MaxWidth==26`。
- **新增/扩展** `keptConnectionColumns` 在 100 列下 traffic 实际宽度 = 26，且 `↑999.9 MiB/s` 经 `renderConnection` 后完整（无 `…`）。

### 8.3 回归

- `go test ./internal/tui/...` 全绿。
- `gofmt -l .` 空、`golangci-lint run` 0 issue（CI 双预检）。

## 9. 验收标准

1. w=26 时 `↑999.9 MiB/s ↓1.5 GiB/s` 完整显示，可见宽 26，无 `…`。
2. 连接页 100 列默认布局下 traffic 列宽 26、速率完整。
3. 现有 w=24 / w=16 截断测试不变（底层防御渲染保留）。
4. 状态栏（#68）、overview、连接详情渲染不受影响。
5. gofmt + golangci-lint + `go test ./...` 全绿。
6. 不引入凭据、订阅 URL 或 controller 地址泄露。
