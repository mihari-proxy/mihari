# log / rule / conns 行数指示器实现计划

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 在 logs / rules / connections 三页 List 卡片下方增加统一的 `X/XXXX` 位置指示行（焦点行位置 / 过滤后总数）。

**Architecture:** 新增共享 helper `ui.FormatPositionIndicator(focused, pos, total)`，封装 `X/Total`、`—/Total`、`0/0` 三种形态。三页各自在 `View()` 拼接一行右对齐状态行（`PadCell` + `theme.Muted`），并把页 chrome 常量 +1（状态行占一行页面高度，`VisibleWindow` 据此各减一行可视数据）。位置与总数均渲染期派生，不新增状态字段。

**Tech Stack:** Go 1.24+、charm.land bubbletea/lipgloss v2、现有 `internal/tui/ui` 宽度感知工具。

**设计依据:** `docs/superpowers/specs/2026-08-13-log-rule-conns-linecount-design.md`（commit `da08b03`，issue #50）。

**环境注意:** 本 worktree 中 codegraph/gopls 可能指向 main 旧版、不可信。只认 `go build`/`go test` 与直接读文件。push 前本地跑 `gofmt -l .` 与 `golangci-lint run`（CI 有独立 lint job）。

---

## Task 1: 共享 helper `FormatPositionIndicator`

**Files:**
- Create: `internal/tui/ui/position.go`
- Create: `internal/tui/ui/position_test.go`

### Step 1: 写失败测试

创建 `internal/tui/ui/position_test.go`：

```go
package ui

import "testing"

func TestFormatPositionIndicator(t *testing.T) {
	tests := []struct {
		name    string
		focused bool
		pos     int
		total   int
		want    string
	}{
		{"focused row", true, 3, 50, "3/50"},
		{"not focused", false, 0, 50, "—/50"},
		{"empty list", false, 0, 0, "0/0"},
		{"empty list stays zero even if focused", true, 1, 0, "0/0"},
		{"full ten-thousand buffer", true, 10000, 10000, "10000/10000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatPositionIndicator(tt.focused, tt.pos, tt.total); got != tt.want {
				t.Fatalf("FormatPositionIndicator(%v, %d, %d) = %q, want %q",
					tt.focused, tt.pos, tt.total, got, tt.want)
			}
		})
	}
}
```

### Step 2: 运行验证失败

```console
go test ./internal/tui/ui/ -run TestFormatPositionIndicator -v
```
Expected: FAIL，`undefined: FormatPositionIndicator`。

### Step 3: 写最小实现

创建 `internal/tui/ui/position.go`：

```go
package ui

import "fmt"

// FormatPositionIndicator formats the list position indicator "X/Total".
// pos is the 1-based focused row index within the filtered list; total is the
// filtered visible count. When the focus is not on a data row, pass focused=false
// to render "—/Total". Empty lists always render "0/0".
func FormatPositionIndicator(focused bool, pos, total int) string {
	if total <= 0 {
		return "0/0"
	}
	if !focused {
		return fmt.Sprintf("—/%d", total)
	}
	return fmt.Sprintf("%d/%d", pos, total)
}
```

### Step 4: 运行验证通过

```console
go test ./internal/tui/ui/ -run TestFormatPositionIndicator -v
```
Expected: PASS。

### Step 5: 提交

```bash
git add internal/tui/ui/position.go internal/tui/ui/position_test.go
git commit -m "feat(ui): add FormatPositionIndicator helper for list position"
```

---

## Task 2: logs 页接入状态行

**Files:**
- Modify: `internal/tui/pages/logs/model.go`（`logChrome` 常量 `:296`、`View()` `:235-237`）
- Modify: `internal/tui/pages/logs/model_test.go`（新增 View 断言）
- Regenerate: `internal/tui/testdata/logs.golden`（通过 `-update`）

### Step 1: 写失败测试

在 `internal/tui/pages/logs/model_test.go` 末尾新增：

```go
func TestView_PositionIndicator(t *testing.T) {
	model := New(0)
	model.SetSize(80, 24)
	model.SetContentFocused(true)
	// Empty buffer + focus on control → "0/0".
	if view := model.View(); !strings.Contains(view, "0/0") {
		t.Fatalf("empty list should show 0/0:\n%s", view)
	}
}
```

> 若 `model_test.go` 未 import `strings`，补 `"strings"`。

### Step 2: 运行验证失败

```console
go test ./internal/tui/pages/logs/ -run TestView_PositionIndicator -v
```
Expected: FAIL（视图当前不含 `0/0`）。

### Step 3: 改 chrome 常量

`internal/tui/pages/logs/model.go:293-296`，把 `const logChrome = 8` 改为 `9`，并更新注释说明新增的状态行占一行：

```go
// logChrome is the page chrome outside log rows: Controls section
// (top + control + search + bottom = 4), Logs section (top + header + rule +
// bottom = 4), plus the position indicator line below the list (= 1), leaving
// the rest for data rows.
const logChrome = 9
```

### Step 4: View 注入状态行

在 `View()` 中，`list := ui.RenderBorderedSection(...)`（`:235`）之后、`content := controls + "\n" + list`（`:237`）之前插入状态行计算，并把它拼到 `content`。`entries` 已在 `:226` 声明，直接复用：

```go
	list := ui.RenderBorderedSection(m.theme, ui.LogsSectionTitle, strings.Join(listLines, "\n"), inner)

	pos, total, focused := 0, len(entries), false
	if m.focus == focusRow && total > 0 {
		pos, focused = m.focused+1, true
	}
	indicator := m.theme.Muted.Render(ui.FormatPositionIndicator(focused, pos, total))
	listStatus := ui.PadCell(indicator, m.layoutWidth(), ui.AlignRight)

	content := controls + "\n" + list + "\n" + listStatus
```

### Step 5: 运行单测通过

```console
go test ./internal/tui/pages/logs/ -run TestView_PositionIndicator -v
```
Expected: PASS。

### Step 6: 更新 golden 并跑全包

```console
go test -update ./internal/tui/
go test ./internal/tui/... 
```
检查 `internal/tui/testdata/logs.golden` 的 diff：应只新增末尾一行 `0/0`（或 `—/N`、`X/N`，取决于 golden 触发时的焦点态）与可视行 -1。若 diff 出现意料外变化，排查后再继续。

### Step 7: 提交

```bash
git add internal/tui/pages/logs/model.go internal/tui/pages/logs/model_test.go internal/tui/testdata/logs.golden
git commit -m "feat(logs): show X/Total position indicator below the log list"
```

---

## Task 3: rules 页接入状态行

**Files:**
- Modify: `internal/tui/pages/rules/model.go`（`rulesChrome` `:383`、`View()` `:318-321`）
- Modify: `internal/tui/pages/rules/model_test.go`（新增 View 断言）

> rules 无 golden 快照，靠行为断言覆盖。

### Step 1: 写失败测试

在 `internal/tui/pages/rules/model_test.go` 末尾新增（参考现有用例构造 `Model` 的方式，必要时用 `New(nil, nil)` + `SetRules`）：

```go
func TestView_PositionIndicator(t *testing.T) {
	model := New(nil, nil)
	model.SetSize(80, 24)
	model.SetRules(protocol.RuleList{Rules: []protocol.Rule{
		{Type: "DOMAIN", Payload: "a.com", Proxy: "DIRECT"},
		{Type: "DOMAIN", Payload: "b.com", Proxy: "REJECT"},
	}})
	model.SetContentFocused(true)
	model.focus = pageFocus{kind: focusRow, row: 1} // 第二条
	view := model.View()
	if !strings.Contains(view, "2/2") {
		t.Fatalf("focused row 2 of 2 should show 2/2:\n%s", view)
	}
	model.focus = pageFocus{kind: focusControl}
	if view := model.View(); !strings.Contains(view, "—/2") {
		t.Fatalf("control focus should show —/2:\n%s", view)
	}
}
```

> 按现有测试文件的 import 与构造习惯调整（`protocol` 已在该包使用）。

### Step 2: 运行验证失败

```console
go test ./internal/tui/pages/rules/ -run TestView_PositionIndicator -v
```
Expected: FAIL。

### Step 3: 改 chrome 常量

`internal/tui/pages/rules/model.go:379-383`，把 `const rulesChrome = 9` 改为 `10`，更新注释：

```go
// rulesChrome is the page chrome outside table rows: Controls section
// (top-title + control strip + search + bottom = 4), List section (top-title +
// bottom = 2), the header/rule lines returned by render* (= 2), plus the
// position indicator line below the list (= 1); effective 10.
const rulesChrome = 10
```

### Step 4: View 注入状态行

`View()` 中 `list := ui.RenderBorderedSection(...)`（`:319`）之后、`content := controls + "\n" + list`（`:321`）之前。`listN` 已在 `:309-317` 算好，复用作 total：

```go
	list := ui.RenderBorderedSection(m.theme, listTitle, strings.Join(listLines, "\n"), inner)

	rulePos, ruleFocused := 0, m.focus.kind == focusRow && listN > 0
	if ruleFocused {
		rulePos = m.focus.row + 1
	}
	ruleIndicator := m.theme.Muted.Render(ui.FormatPositionIndicator(ruleFocused, rulePos, listN))
	listStatus := ui.PadCell(ruleIndicator, m.layoutWidth(), ui.AlignRight)

	content := controls + "\n" + list + "\n" + listStatus
```

> rules 与 providers 两个视图共用同一段：`listN` 在两视图下都已正确表示过滤后总数。

### Step 5: 运行验证通过

```console
go test ./internal/tui/pages/rules/ -run TestView_PositionIndicator -v
go test ./internal/tui/pages/rules/
```
Expected: PASS。

### Step 6: 提交

```bash
git add internal/tui/pages/rules/model.go internal/tui/pages/rules/model_test.go
git commit -m "feat(rules): show X/Total position indicator below the rule list"
```

---

## Task 4: connections 页接入状态行

**Files:**
- Modify: `internal/tui/pages/connections/render.go`（`connectionChrome` `:51`、`View()` `:33-37`）
- Modify: `internal/tui/pages/connections/render_test.go`（新增断言）
- Regenerate: `internal/tui/testdata/connections.golden`

### Step 1: 写失败测试

在 `internal/tui/pages/connections/render_test.go` 新增：

```go
func TestView_PositionIndicator(t *testing.T) {
	model := New(nil, nil)
	model.SetSize(80, 24)
	// Empty history, focus on control → "0/0".
	if view := model.View(); !strings.Contains(view, "0/0") {
		t.Fatalf("empty list should show 0/0:\n%s", view)
	}
}
```

### Step 2: 运行验证失败

```console
go test ./internal/tui/pages/connections/ -run TestView_PositionIndicator -v
```
Expected: FAIL。

### Step 3: 改 chrome 常量

`internal/tui/pages/connections/render.go:47-51`，把 `const connectionChrome = 9` 改为 `10`，更新注释：

```go
// connectionChrome is the page chrome outside table rows: Controls section
// (top + title + control strip + search + bottom = 5), List section
// (top + title + header + rule + bottom = 5 minus the header/rule lines that
// are part of tableLines itself), plus the position indicator line below the
// list (= 1); effective 10.
const connectionChrome = 10
```

### Step 4: View 注入状态行

`View()` 中 `list := ui.RenderBorderedSection(...)`（`:35`）之后、`base := controls + "\n" + list`（`:37`）之前插入：

```go
	list := ui.RenderBorderedSection(m.theme, listTitle, listBody, inner)

	rows := m.visibleRows()
	connTotal := len(rows)
	connPos, connFocused := 0, false
	if m.focus.kind == focusRow {
		if idx := rowIndex(rows, m.focus.rowID); idx >= 0 && connTotal > 0 {
			connPos, connFocused = idx+1, true
		}
	}
	connIndicator := m.theme.Muted.Render(ui.FormatPositionIndicator(connFocused, connPos, connTotal))
	listStatus := ui.PadCell(connIndicator, m.layoutWidth(), ui.AlignRight)

	base := controls + "\n" + list + "\n" + listStatus
```

> `rowIndex` 与 `visibleRows` 均为本包既有函数（`model.go:582`、`:512`）。

### Step 5: 运行验证通过

```console
go test ./internal/tui/pages/connections/ -run TestView_PositionIndicator -v
```
Expected: PASS。

### Step 6: 更新 golden 并跑全包

```console
go test -update ./internal/tui/
go test ./internal/tui/...
```
检查 `internal/tui/testdata/connections.golden` diff：仅新增末尾指示行 + 可视行 -1。

### Step 7: 提交

```bash
git add internal/tui/pages/connections/render.go internal/tui/pages/connections/render_test.go internal/tui/testdata/connections.golden
git commit -m "feat(connections): show X/Total position indicator below the connection table"
```

---

## Task 5: 全量回归与验收

### Step 1: 全量构建与测试

```console
go build ./...
go test ./...
```
Expected: 全绿。若有其它包的 golden/快照因渲染变化失败，用 `go test -update` 仅在确认 diff 合理后重生成。

### Step 2: 静态检查（CI 同款）

```console
go vet ./...
gofmt -l cmd internal
golangci-lint run
```
Expected: `gofmt -l` 无输出；lint 0 issues。

### Step 3: 验收清单核对

逐项确认（对应设计 §9）：
- [ ] logs/rules/connections 三页列表下方均有右对齐 `X/XXXX`。
- [ ] 焦点在数据行时 X 随上下移动实时变化。
- [ ] 过滤生效时 XXXX 跟随过滤后总数。
- [ ] 焦点离开数据行（搜索栏/列头/chip）显示 `—/total`；conns 焦点在 header/control/search 时同样 `—/total`。
- [ ] 空列表显示 `0/0`。
- [ ] connections 标题全量计数（`N active`）与状态行过滤后计数并存。
- [ ] chrome +1 后滚动/可视行无回归。

### Step 4: （可选）手动验证

若用户授权并具备运行中的 mihomo 守护进程：

```console
go run ./cmd/mihari
```
进入 Logs / Rules / Connections 页，确认指示行在焦点移动与过滤时行为正确。

### Step 5: 收尾提交（如有 fixup）

若前面步骤有遗漏的格式/快照修复，合并提交：

```bash
git add -A
git commit -m "test(tui): finalize line-count indicator golden snapshots"
```

---

## 风险与回退

- **可视行 -1**：三页各少一行数据。若窄终端下体验明显变差，可在后续迭代加「极窄宽度时隐藏指示行」的降级，但不在本计划范围。
- **golden 漂移**：`-update` 重生成后务必人工审 diff，确认只有预期的「新增指示行 + 可视行 -1」变化，无连锁排版错乱。
- **回退**：所有改动在独立分支 `worktree-log-rule-conns-linecount`，回退即放弃该分支。
