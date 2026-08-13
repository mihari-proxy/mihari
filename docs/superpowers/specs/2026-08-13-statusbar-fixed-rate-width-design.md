# TUI 顶部状态栏固定上传/下载槽宽设计

日期：2026-08-13
状态：已实施，待 Pull Request
关联 issue：mihari-proxy/mihari#67
目标分支：`fix/issue-67-statusbar-rate-width`

## 1. 问题

TUI 最上方状态栏 `RenderStatusBar`（`internal/tui/ui/statusbar.go`）用 `PriorityBar` 按实测宽度做优先级裁剪：宽度不够时按 版本(1) → 内存(2) → 订阅(3) → conn/速率(5) → Core(5) → Title(6) 丢字段。

上传 / 下载速率当前是变宽字符串：

| 模式 | 空闲 | 高峰 | 速率段宽度差 |
|---|---|---|---|
| Full | `↑0 B/s  ↓0 B/s`（14 列） | `↑999.9 MiB/s  ↓999.9 MiB/s`（26 列） | 12 列 |
| Compact | `↑0/s ↓0/s`（9 列） | `↑999.9M/s ↓999.9T/s`（19 列） | 10 列 |

临界宽度下，版本、内存、订阅会随流量反复出现或消失。

Full 变宽来自 `FormatRate` / `formatIEC`：字节档用 `%d`（`0 B/s`…`1023 B/s`），更高档用 `%.1f`（`1.0 KiB/s`…`999.9 MiB/s`）。Compact 变宽来自 `formatCompactIEC`（`0`…`999.9M`）。

## 2. 目标

- 上传、下载各自占用固定列宽，数值在槽内右对齐，不随位数或单位切换改变占用宽度。
- 同一终端宽度下，仅因速率从 `0 B/s` 涨到 `12.3 MiB/s` 不得改变状态栏可见字段数量，也不应让其余字段左右抖动。
- Full 与 Compact 两种状态栏都要稳定。
- 不改动 `FormatRate` / `FormatBytes` 的全局契约，不影响 overview、connections 表格和连接详情。

## 3. 非目标

- 不固定 `N conn` / `Nc`、内存、订阅用量（慢变量，不是本抖动源）。
- 不改 overview / connections 表格 / 连接详情的速率渲染。
- 不改 `PriorityBar` 丢段算法或优先级数字。
- 不改控制协议、持久化、CLI。

## 4. 方案比较

### 4.1 采用：只在状态栏把 ↑ / ↓ 各自垫成固定槽宽

用现成的 `PadCell(..., AlignRight)`，在 `RenderStatusBar` 的速率 segment 内垫齐。`FormatRate` / `formatCompactIEC` 本身不变。

优点：改动局部，直接消掉 `PriorityBar` 的测量抖动。代价：空闲时速率段永久占满槽宽，部分终端会比现在更早丢掉版本/内存。这是稳定换出来的，可接受。

### 4.2 不采用：改全局 `FormatRate`

overview、connections 列、连接详情都会变。Connections 已有 `RenderTrafficSlot`，会重复或互相打架。

### 4.3 不采用：只提高速率段优先级、永不丢弃

字段数量可能稳一点，但左侧字段仍会被变宽的速率挤掉，抖动还在。

## 5. 详细设计

### 5.1 槽宽

右对齐垫到本格式的最大常见宽度（覆盖到 `999.9 XiB/s` / `999.9T`）：

| 模式 | 每个方向槽宽 | 依据 | 整段恒定宽 |
|---|---|---|---|
| Full | 11 | `999.9 MiB/s` | `↑`+11+`  `+`↓`+11 = 26 |
| Compact | 6 | `999.9M`（`/s` 仍在槽外） | `↑`+6+`/s `+`↓`+6+`/s` = 19 |

超出槽宽时走现有 `PadCell` 截断（`…`）。家用代理到不了 `1000 TiB/s`，可接受。

右对齐后 `/s` 齐，数字往左长：

```text
↑     0 B/s  ↓     0 B/s
↑  1.2 KiB/s  ↓ 12.3 MiB/s
↑999.9 MiB/s  ↓999.9 MiB/s
```

Compact：

```text
↑     0/s ↓     0/s
↑   1.2K/s ↓    84M/s
↑999.9M/s ↓999.9M/s
```

### 5.2 实现

只改 `internal/tui/ui/statusbar.go`：

```go
const (
    statusRateWidth        = 11 // "999.9 MiB/s"
    statusCompactRateWidth = 6  // "999.9M"
)

func statusRateLabel(value int64, compact bool) string {
    if compact {
        return PadCell(formatCompactIEC(value), statusCompactRateWidth, AlignRight)
    }
    return PadCell(FormatRate(value), statusRateWidth, AlignRight)
}
```

速率 segment 改为：

- Full：`fmt.Sprintf("↑%s  ↓%s", statusRateLabel(up, false), statusRateLabel(down, false))`
- Compact：`fmt.Sprintf("↑%s/s ↓%s/s", statusRateLabel(up, true), statusRateLabel(down, true))`

不改 `FormatRate`、`FormatBytes`、`PriorityBar`、connections 的 `RenderTrafficSlot`。

### 5.3 对现有丢段金句的影响

`TestStatusBar_SegmentDropTiers` 的速率段会变宽（例如 Full `↑3.0 MiB/s  ↓12.0 MiB/s` → `↑  3.0 MiB/s  ↓ 12.0 MiB/s`）。同一 `width` 可能少显示一档低优先级字段，丢弃顺序不变。实现时按新的恒定宽度重算金句。

## 6. 测试策略

行为变更采用 Red–Green–Refactor。测试加在 `internal/tui/ui/statusbar_test.go`。

1. Full / Compact 下，`0` 与约 `12.3 MiB/s` 的速率段可见宽度相同。
2. 选接近丢段阈值的宽度（Full 无 badge 约 88 列；Compact 带 badge 约 93 列）：空闲与高峰的可见字段集合相同。
3. 更新 `TestStatusBar_SegmentDropTiers` 金句，使垫齐后的恒定宽度与丢段顺序一致。

## 7. 验收标准

- 同一终端宽度下，仅改变上传/下载速率不得改变可见字段集合。
- Full 速率段恒为 26 列，Compact 速率段恒为 19 列（在字段未被丢掉时）。
- `FormatRate` 既有单测与 connections / overview 渲染不受影响。
- 不引入凭据、订阅 URL 或 controller 地址泄露。
