# System 页焦点滚动 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** System 页内容超界时按焦点做行级滚动，失败原因钉在顶部；行级窗口算法抽成 `ui.EnsureLineVisible` / `ui.SliceLines`，Proxies 与 System 共用。

**Architecture:** `internal/tui/ui/viewport.go` 增加两个纯函数（与现有 `VisibleWindow` 并列，不合并）。System 把分区行交给 helper，错误条作为 0/1 行非滚动 chrome，`viewH = height - chrome`。Proxies 只改 `ensureFocusVisible` / `View` 调用点，`lastError` 仍可滚走，`FocusFirst` 仍打回第一组。根 shell `Height(ContentHeight)` 不改。

**Tech Stack:** Go 1.26（`go.mod` toolchain）、bubbletea v2、lipgloss v2（`charm.land/lipgloss/v2`）、标准 `go test`。

**Spec:** `docs/superpowers/specs/2026-08-31-system-page-scroll-design.md`

**Issue:** https://github.com/mihari-proxy/mihari/issues/158

**工作目录（worktree）:** `.worktrees/feat-158-system-page-scroll`  
**分支:** `feat/158-system-page-scroll`（从 `origin/dev`）

## Global Constraints

- 纯 TUI。不改 `/v1` DTO、错误码、JSON envelope、CLI、持久化、daemon 写入路径。
- 功能 PR 不修改 `CHANGELOG.md`。
- 不恢复双列，不加 PgUp/PgDn，不修 #89 / #90。
- 不上 bubbles Viewport；不把行级函数合并进 `ui.VisibleWindow`；不抽 `LineViewport` 结构体。
- System `FocusFirst` 不打回第一行；Proxies `FocusFirst` 保持打回第一组 + `scrollY=0`。
- 测试不访问公网、不读用户目录、不启真实 daemon。
- 提交仅在用户明确要求时执行；计划里的 commit 步骤是任务边界，执行期须再确认。
- 所有改过的 Go 文件 `gofmt`；验证命令在 worktree 根目录运行。

## File Structure

| 文件 | 职责 |
| --- | --- |
| `internal/tui/ui/viewport.go` | 新增 `EnsureLineVisible`、`SliceLines`；不改 `VisibleWindow` |
| `internal/tui/ui/viewport_test.go` | 新建；helper 表驱动 + 可选 1 条与 `VisibleWindow` 对照 |
| `internal/tui/pages/system/model.go` | `scrollY`、`buildSectionContent`、`errorChromeLines`、`ensureFocusVisible`（Task 2 空桩，Task 4a 换成真 clamp）、`View`、`Update` defer、页外 mutator 调用点 |
| `internal/tui/pages/system/scroll_test.go` | 新建；六条必写 System 滚动测试 |
| `internal/tui/pages/proxies/model.go` | 仅调用点替换 |
| `docs/superpowers/specs/2026-08-31-system-page-scroll-design.md` | 已写好的规格，随 PR 入库 |

不改：`internal/tui/model.go` 根裁切、`internal/tui/ui/viewport.go` 的 `VisibleWindow` 语义、协议包、CLI、Proxies `FocusFirst` / `lastError` 放置、`CHANGELOG.md`。

---

### Task 1: 行级视口 helper

**Files:**
- Create: `internal/tui/ui/viewport_test.go`
- Modify: `internal/tui/ui/viewport.go`（在 `VisibleWindow` 之后追加两个函数；**不改** `VisibleWindow` 本体）

**Interfaces:**
- Consumes: 无
- Produces:
  - `func EnsureLineVisible(scrollY, viewH, n, focusStart, focusEnd int) int`
  - `func SliceLines(lines []string, scrollY, height int) []string`

- [ ] **Step 1: 写失败测试**

新建 `internal/tui/ui/viewport_test.go`：

```go
package ui

import (
	"reflect"
	"strings"
	"testing"
)

func TestEnsureLineVisible(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name                         string
		scrollY, viewH, n            int
		focusStart, focusEnd, want   int
	}{
		{name: "viewH zero keeps scrollY", scrollY: 4, viewH: 0, n: 20, focusStart: 10, focusEnd: 11, want: 4},
		{name: "viewH negative keeps negative scrollY", scrollY: -3, viewH: -1, n: 20, focusStart: 0, focusEnd: 1, want: -3},
		{name: "empty lines returns 0", scrollY: 9, viewH: 5, n: 0, focusStart: 0, focusEnd: 1, want: 0},
		{name: "no focus clamps stale scrollY", scrollY: 40, viewH: 5, n: 20, focusStart: -1, focusEnd: 0, want: 15},
		{name: "inverted focus clamps", scrollY: 40, viewH: 5, n: 20, focusStart: 3, focusEnd: 3, want: 15},
		{name: "focus above window scrolls up", scrollY: 10, viewH: 5, n: 20, focusStart: 2, focusEnd: 3, want: 2},
		{name: "focus below window scrolls down", scrollY: 0, viewH: 5, n: 20, focusStart: 12, focusEnd: 13, want: 8},
		{name: "focus already visible unchanged", scrollY: 4, viewH: 5, n: 20, focusStart: 6, focusEnd: 7, want: 4},
		{name: "exclusive end already visible", scrollY: 0, viewH: 5, n: 20, focusStart: 4, focusEnd: 5, want: 0},
		{name: "tall block pins start then clamps", scrollY: 0, viewH: 5, n: 20, focusStart: 3, focusEnd: 12, want: 3},
		{name: "n shrinks below scrollY", scrollY: 50, viewH: 5, n: 8, focusStart: -1, focusEnd: 0, want: 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := EnsureLineVisible(tc.scrollY, tc.viewH, tc.n, tc.focusStart, tc.focusEnd)
			if got != tc.want {
				t.Fatalf("EnsureLineVisible(%d,%d,%d,%d,%d)=%d want %d",
					tc.scrollY, tc.viewH, tc.n, tc.focusStart, tc.focusEnd, got, tc.want)
			}
		})
	}
}

func TestSliceLines(t *testing.T) {
	t.Parallel()
	lines := []string{"a", "b", "c", "d", "e"}
	cases := []struct {
		name          string
		in            []string
		scrollY, h    int
		want          []string
		sameBacking   bool
	}{
		{name: "height zero returns all", in: lines, scrollY: 2, h: 0, want: lines, sameBacking: true},
		{name: "height negative returns all", in: lines, scrollY: 2, h: -4, want: lines, sameBacking: true},
		{name: "height covers all", in: lines, scrollY: 0, h: 5, want: lines},
		{name: "height taller than n", in: lines, scrollY: 0, h: 9, want: lines},
		{name: "middle window", in: lines, scrollY: 1, h: 2, want: []string{"b", "c"}},
		{name: "scrollY past end clamps", in: lines, scrollY: 40, h: 2, want: []string{"d", "e"}},
		{name: "negative scrollY clamps to 0", in: lines, scrollY: -3, h: 2, want: []string{"a", "b"}},
		{name: "empty input", in: nil, scrollY: 3, h: 4, want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SliceLines(tc.in, tc.scrollY, tc.h)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("SliceLines(%v,%d,%d)=%v want %v", tc.in, tc.scrollY, tc.h, got, tc.want)
			}
			if tc.sameBacking && len(tc.in) > 0 && len(got) > 0 {
				// height<=0 may return the same backing slice.
				tc.in[0] = "mutated"
				if got[0] != "mutated" && got[0] != "a" {
					t.Fatalf("unexpected aliasing %q", got[0])
				}
				tc.in[0] = "a"
			}
		})
	}
}

func TestLineViewportIsNotVisibleWindow(t *testing.T) {
	t.Parallel()
	// VisibleWindow is item-index, anchored-bottom when following.
	start, end := VisibleWindow(20, 10, 0, true, 0)
	if start != 10 || end != 20 {
		t.Fatalf("VisibleWindow following got [%d,%d) want [10,20)", start, end)
	}
	// Line viewport with focus at the bottom of a 10-line window stays at 0
	// when the exclusive end is already visible — not anchored to the tail.
	if got := EnsureLineVisible(0, 10, 20, 9, 10); got != 0 {
		t.Fatalf("EnsureLineVisible exclusive-end=%d want 0", got)
	}
	lines := strings.Split("0 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19", " ")
	got := SliceLines(lines, 0, 10)
	if len(got) != 10 || got[0] != "0" || got[9] != "9" {
		t.Fatalf("SliceLines window=%v", got)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```console
go test ./internal/tui/ui -run "TestEnsureLineVisible|TestSliceLines|TestLineViewportIsNotVisibleWindow" -v
```

预期：编译失败，`undefined: EnsureLineVisible` / `undefined: SliceLines`。不是公式断言失败。

- [ ] **Step 3: 最小实现**

在 `internal/tui/ui/viewport.go` 的 `VisibleWindow` 之后追加（文件目前无 import，这两个函数也不需要）。注释必须写明：这是**行级**窗口（终端行 / 半开焦点块），与 `VisibleWindow` 的 item-index 锚底表格窗口不是同一原语。

```go
// EnsureLineVisible keeps [focusStart, focusEnd) inside a window of viewH
// lines and returns the new scrollY. n is len(lines).
//
// This is a line-based viewport (terminal rows + half-open focus block).
// Do not confuse it with VisibleWindow, which is an item-index,
// anchored-bottom table window used by logs/rules/connections.
//
// Semantics (match proxies ensureFocusVisible):
//   - viewH <= 0: return scrollY unchanged
//   - n == 0: return 0
//   - focusStart < 0 || focusEnd <= focusStart: clamp scrollY into [0, max(0,n-viewH)]
//   - focusEnd-focusStart >= viewH: pin scrollY = focusStart, then clamp
//   - else keep the block inside [scrollY, scrollY+viewH), then clamp
func EnsureLineVisible(scrollY, viewH, n, focusStart, focusEnd int) int {
	if viewH <= 0 {
		return scrollY
	}
	if n == 0 {
		return 0
	}
	maxScroll := max(0, n-viewH)
	clamp := func(y int) int { return min(max(0, y), maxScroll) }
	if focusStart < 0 || focusEnd <= focusStart {
		return clamp(scrollY)
	}
	if focusEnd-focusStart >= viewH {
		scrollY = focusStart
	} else {
		if focusStart < scrollY {
			scrollY = focusStart
		}
		if focusEnd > scrollY+viewH {
			scrollY = focusEnd - viewH
		}
	}
	return clamp(scrollY)
}

// SliceLines returns the visible window of lines.
// height <= 0 means "not sized yet" and returns lines unchanged (same
// backing slice is allowed). Never panics on empty input or out-of-range
// scrollY. Do not pass height 0 to mean "empty window".
func SliceLines(lines []string, scrollY, height int) []string {
	if height <= 0 {
		return lines
	}
	n := len(lines)
	start := min(max(0, scrollY), max(0, n-height))
	return lines[start : start+min(height, n-start)]
}
```

- [ ] **Step 4: 跑测试确认通过**

```console
go test ./internal/tui/ui -run "TestEnsureLineVisible|TestSliceLines|TestLineViewportIsNotVisibleWindow" -v
gofmt -w internal/tui/ui/viewport.go internal/tui/ui/viewport_test.go
```

预期：PASS。此时 **不要** 改 System / Proxies。

- [ ] **Step 5: Commit**（仅当用户要求提交时）

```console
git add internal/tui/ui/viewport.go internal/tui/ui/viewport_test.go
git commit -m "feat: 增加行级视口 EnsureLineVisible 与 SliceLines"
```

---

### Task 2: System 最小桩（让滚动测试能编译）

**Files:**
- Modify: `internal/tui/pages/system/model.go`（`Model` 结构体约 345 行 `height` 旁；`View` 约 1014–1026；`visibleErrorDetail` 附近）

**Interfaces:**
- Consumes: 无 helper 调用（本任务禁止调用 `ui.SliceLines` / `ui.EnsureLineVisible`）
- Produces:
  - `system.Model.scrollY int`（零值 0）
  - `func (m *Model) errorChromeLines() []string`（薄封装，无 MaxWidth）
  - `func (m *Model) ensureFocusVisible()`（**空实现**，函数体为空，无 helper 调用；只为 Task 3 能编译。Task 4a 换成真 clamp）

- [ ] **Step 1: 加字段、薄 chrome 封装、ensure 空桩**

在 `Model` 的 `height int` 旁增加：

```go
scrollY int // top visible section line; excludes error chrome
```

在 `visibleErrorDetail` 附近增加薄封装。语义对齐**今天的** `View`：不 trim、不把 `\n` 换成空格、不做 `MaxWidth`。测试 5 会在长字符串上因 `lipgloss.Width > 56` 或内部 `\n` 而行为红。

```go
func (m *Model) errorChromeLines() []string {
	detail := m.visibleErrorDetail()
	if detail == "" {
		return nil
	}
	return []string{m.theme.Danger.Render(detail)}
}

// no-op stub so Task 3 tests compile. Task 4a replaces this with the
// real clamp (buildSectionContent + ui.EnsureLineVisible). Do not call
// helpers or mutate scrollY here.
func (m *Model) ensureFocusVisible() {}
```

这与 `scrollY` / 薄 `errorChromeLines` 是同一类桩：让滚动测试能编译，但 **不得** 改变行为。Task 3 的测试 5 **一律**在写 `lastError` 之后调用 `ensureFocusVisible()`，不要在测试里判断方法是否存在。测试 6 **不要**自己调 ensure——它锁的是 `ApplyServiceStatus` 内部必须调用。

可选：`View` 改为用 `errorChromeLines()` 拼错误条，但仍然 `append(..., m.renderSections()...)` 返回全文。不要切片。若保持 `View` 直接调 `visibleErrorDetail()`，测试 5 只打 `errorChromeLines()` 也够——两种都允许，但不要在这里接线 helper。

本任务 **不要** 改 `SetSize` / `FocusFirst` / `Update`，**不要** 给空桩加 helper 调用。

- [ ] **Step 2: 确认现有 System 测试仍绿**

```console
go test ./internal/tui/pages/system
```

预期：PASS。桩不得改变 `View` 全文语义。

- [ ] **Step 3: Commit**（仅当用户要求提交时）

```console
git add internal/tui/pages/system/model.go
git commit -m "refactor: System 页增加 scrollY、错误条薄封装与 ensure 空桩"
```

---

### Task 3: System 滚动失败测试

**Files:**
- Create: `internal/tui/pages/system/scroll_test.go`

**Interfaces:**
- Consumes: `Model.scrollY`、`errorChromeLines()`、`ensureFocusVisible()`（Task 2 空桩）；现有 `New`、`NewWithService`、`fakeClient`、`fakeService`、`withElevation`、`updateKey`（`model_test.go:2108`）；`rowGitHub`、`rowMixed`、`rowDaemon` 常量；`service.StatusKind` / `service.StatusNotInstalled` / `service.StatusRunning`
- Produces: 六条必写测试，接线前必须**行为红**（不是编译失败）。测试 5 一律调用 `ensureFocusVisible()`，不要因为空桩而跳过。测试 6 不自己调 ensure。

- [ ] **Step 1: 写六条失败测试**

新建 `internal/tui/pages/system/scroll_test.go`：

```go
package system

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/service"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func aboutFixture(t *testing.T) *Model {
	t.Helper()
	model := New(&fakeClient{}, func() string { return "op" })
	model.SetSnapshot(protocol.Status{}, protocol.CoreStatus{})
	model.SetSize(80, 40)
	model.SetContentFocused(true)
	full := strings.Count(model.View(), "\n") + 1
	if full <= 12 {
		t.Fatalf("about fixture view lines=%d want > 12", full)
	}
	return model
}

func viewLineCount(view string) int {
	if view == "" {
		return 0
	}
	return strings.Count(view, "\n") + 1
}

func downToGitHub(t *testing.T, model *Model) *Model {
	t.Helper()
	limit := len(model.rows()) + 2
	for i := 0; i < limit && model.focusID != rowGitHub; i++ {
		model = updateKey(t, model, tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if model.focusID != rowGitHub {
		t.Fatalf("focus=%q want %q after %d downs", model.focusID, rowGitHub, limit)
	}
	return model
}

func TestView_ShortHeightKeepsFocusedRowVisible(t *testing.T) {
	model := aboutFixture(t)
	model.SetSize(80, 12)
	model.FocusFirst()
	model.SetContentFocused(true)
	if model.focusID != rowMixed {
		t.Fatalf("focus=%q want %q", model.focusID, rowMixed)
	}
	top := model.View()
	if strings.Contains(top, ui.AboutSectionTitle) || strings.Contains(top, ui.AboutGitHubDisplay) {
		t.Fatalf("short top view still shows About:\n%s", top)
	}
	downs := 0
	limit := len(model.rows()) + 2
	for model.focusID != rowGitHub && downs < limit {
		model = updateKey(t, model, tea.KeyPressMsg{Code: tea.KeyDown})
		downs++
	}
	if model.focusID != rowGitHub {
		t.Fatalf("focus=%q want GitHub", model.focusID)
	}
	view := model.View()
	if !strings.Contains(view, ui.AboutSectionTitle) || !strings.Contains(view, ui.AboutGitHubLabel) {
		t.Fatalf("focused About missing:\n%s", view)
	}
	if !strings.Contains(view, ui.FocusMarker) {
		t.Fatalf("missing focus marker:\n%s", view)
	}
	if viewLineCount(view) > 12 {
		t.Fatalf("view lines=%d exceed 12\n%s", viewLineCount(view), view)
	}
	if strings.Contains(view, ui.PortsConfigSectionTitle) {
		t.Fatalf("Ports Config still visible after scroll:\n%s", view)
	}
	for i := 0; i < downs; i++ {
		model = updateKey(t, model, tea.KeyPressMsg{Code: tea.KeyUp})
	}
	if model.focusID != rowMixed {
		t.Fatalf("focus after ups=%q want %q", model.focusID, rowMixed)
	}
	top = model.View()
	if !strings.Contains(top, ui.PortsConfigSectionTitle) {
		t.Fatalf("Ports Config missing after scroll up:\n%s", top)
	}
	if strings.Contains(top, ui.AboutSectionTitle) {
		t.Fatalf("About still visible at top:\n%s", top)
	}
	if model.scrollY != 0 {
		t.Fatalf("scrollY=%d want 0 at top", model.scrollY)
	}
}

func TestView_ErrorDetailPinnedWhileScrolled(t *testing.T) {
	model := aboutFixture(t)
	model.SetSize(80, 12)
	model.FocusFirst()
	model.SetContentFocused(true)
	model = downToGitHub(t, model)
	if strings.Contains(model.View(), ui.PortsConfigSectionTitle) {
		t.Fatalf("expected Ports Config scrolled away before error:\n%s", model.View())
	}
	model.SetOpenBrowser(func(string) error { return errors.New("browser missing") })
	model = updateKey(t, model, tea.KeyPressMsg{Code: tea.KeyEnter})
	view := model.View()
	first := strings.Split(view, "\n")[0]
	if !strings.Contains(first, ui.AboutGitHubOpenFailed) {
		t.Fatalf("error not pinned on first line %q\n%s", first, view)
	}
	if strings.Contains(view, "browser missing") {
		t.Fatalf("raw browser error leaked:\n%s", view)
	}
	if !strings.Contains(view, ui.AboutSectionTitle) || !strings.Contains(view, ui.AboutGitHubLabel) {
		t.Fatalf("About missing after error chrome:\n%s", view)
	}
	if viewLineCount(view) > 12 {
		t.Fatalf("view lines=%d exceed 12\n%s", viewLineCount(view), view)
	}
	if strings.Contains(view, ui.PortsConfigSectionTitle) {
		t.Fatalf("Ports Config visible after error:\n%s", view)
	}
}

func TestFocusFirst_PreservesGitHubAndScrolls(t *testing.T) {
	model := aboutFixture(t)
	model.SetSize(80, 12)
	model.SetContentFocused(true)
	model = downToGitHub(t, model)
	model.FocusFirst()
	if model.focusID != rowGitHub {
		t.Fatalf("FocusFirst reset focus to %q", model.focusID)
	}
	view := model.View()
	if !strings.Contains(view, ui.AboutSectionTitle) || !strings.Contains(view, ui.AboutGitHubLabel) {
		t.Fatalf("GitHub not visible after FocusFirst:\n%s", view)
	}
	if strings.Contains(view, ui.PortsConfigSectionTitle) {
		t.Fatalf("Ports Config visible after FocusFirst:\n%s", view)
	}
	if viewLineCount(view) > 12 {
		t.Fatalf("view lines=%d exceed 12\n%s", viewLineCount(view), view)
	}
}

func TestView_SetSnapshotKeepsGitHubVisible(t *testing.T) {
	model := aboutFixture(t)
	model.SetSize(80, 12)
	model.SetContentFocused(true)
	model = downToGitHub(t, model)
	model.SetSnapshot(protocol.Status{
		Capabilities: []string{protocol.CapabilitySystemProxy, protocol.CapabilityTUN},
	}, protocol.CoreStatus{})
	if model.focusID != rowGitHub {
		t.Fatalf("focus=%q after SetSnapshot", model.focusID)
	}
	view := model.View()
	if !strings.Contains(view, ui.AboutSectionTitle) || !strings.Contains(view, ui.AboutGitHubLabel) {
		t.Fatalf("GitHub missing after SetSnapshot:\n%s", view)
	}
	if viewLineCount(view) > 12 {
		t.Fatalf("view lines=%d exceed 12\n%s", viewLineCount(view), view)
	}
}

func TestView_LongErrorChromeStaysOneLine(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		snippet string
		extra   string // nonempty: first line must also contain this (locks \n → space)
	}{
		{
			name:    "elevation",
			payload: ui.ServiceElevationRequired,
			snippet: "Administrator",
		},
		{
			name:    "multiline",
			payload: "line-one\n" + strings.Repeat("x", 80),
			snippet: "line-one",
			extra:   "xxx",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			model := aboutFixture(t)
			model.SetSize(58, 12)
			model.SetContentFocused(true)
			model = downToGitHub(t, model)
			model.lastError = tc.payload
			model.ensureFocusVisible()
			chrome := model.errorChromeLines()
			if len(chrome) != 1 {
				t.Fatalf("chrome lines=%d", len(chrome))
			}
			if got := lipgloss.Width(chrome[0]); got > 56 {
				t.Fatalf("chrome visual width=%d want <=56", got)
			}
			view := model.View()
			first := strings.Split(view, "\n")[0]
			if !strings.Contains(first, tc.snippet) {
				t.Fatalf("first line %q missing %q\n%s", first, tc.snippet, view)
			}
			if tc.extra != "" && !strings.Contains(first, tc.extra) {
				t.Fatalf("first line %q missing %q (newline must become space)\n%s", first, tc.extra, view)
			}
			if !strings.Contains(view, ui.AboutSectionTitle) || !strings.Contains(view, ui.AboutGitHubLabel) {
				t.Fatalf("GitHub/About missing:\n%s", view)
			}
			if viewLineCount(view) > 12 {
				t.Fatalf("view lines=%d exceed 12\n%s", viewLineCount(view), view)
			}
		})
	}
}

func TestView_ApplyServiceStatusKeepsGitHubVisible(t *testing.T) {
	// Live path is syncSystemServiceStatus in internal/tui/model.go:259-265.
	// It calls ApplyServiceStatus and does not enter System Update, so the
	// Task 4a Update defer does not cover it.
	withElevation(t, true)
	svc := &fakeService{status: service.StatusNotInstalled}
	model := NewWithService(&fakeClient{}, svc, func() string { return "system-op" })
	updated, _ := model.Update(serviceStatusMsg{status: service.StatusNotInstalled, elevated: true})
	model = updated.(*Model)
	model.SetSnapshot(protocol.Status{}, protocol.CoreStatus{})
	model.SetSize(80, 40)
	model.SetContentFocused(true)
	full := strings.Count(model.View(), "\n") + 1
	if full <= 12 {
		t.Fatalf("service fixture view lines=%d want > 12", full)
	}
	model.SetSize(80, 12)
	model = downToGitHub(t, model)
	beforeRows := len(model.rows())
	// Grow serviceRows: elevated NotInstalled is status+install+reinstall;
	// Running is status+uninstall+reinstall+stop+restart.
	model.ApplyServiceStatus(service.StatusRunning, true)
	if got := len(model.rows()); got <= beforeRows {
		t.Fatalf("ApplyServiceStatus did not grow rows: %d → %d", beforeRows, got)
	}
	if model.focusID != rowGitHub {
		t.Fatalf("focus=%q after ApplyServiceStatus", model.focusID)
	}
	view := model.View()
	if !strings.Contains(view, ui.AboutSectionTitle) || !strings.Contains(view, ui.AboutGitHubLabel) {
		t.Fatalf("GitHub missing after ApplyServiceStatus:\n%s", view)
	}
	if viewLineCount(view) > 12 {
		t.Fatalf("view lines=%d exceed 12\n%s", viewLineCount(view), view)
	}
}
```

说明：

- 测试 1 在未接线时：`SetSize(80,12)` 的 `View()` 仍是全文 27 行，含 Ports Config **和** About，行数 > 12 → 红。不要把「桩阶段 `scrollY == 0`」当成失败条件；真正的红是 View 内容。
- 测试 2 在未接线时：`Enter` 会写 `lastError`，但全文仍从 Ports Config 起，且第一行不是错误条（错误条在全文最顶，Ports 仍在）——未切片时第一行可能已经是错误条。若薄封装让错误出现在第一行，本测试仍会因「仍含 Ports Config / 行数 > 12」而红。这是期望。
- 测试 3 未接线时 `FocusFirst` 已保留 `rowGitHub`（现有语义），但 View 仍是全文含 Ports Config → 红。这锁的是接线后不得照抄 Proxies 把焦点打回 Mixed。
- 测试 5 **一律**在 `lastError = payload` 之后调用 `model.ensureFocusVisible()`（Task 2 空桩，不是编译失败；禁止「方法不存在就跳过」）。About/GitHub 用与测试 1–4 相同的 De Morgan：`!Contains(About) || !Contains(GitHub)`，缺一即红。子测试名固定为 `"elevation"` / `"multiline"`，不要用 `payload[:min(24,len)]`。
- Task 3 红阶段（Task 2 空桩、尚未 4a 切片）：测试 5 仍红在 `lipgloss.Width(chrome[0]) > 56`（薄 chrome 无 MaxWidth；`ServiceElevationRequired` 76 字符）以及 View 仍是全文（行数 > 12）。`"multiline"` 还红在第一行缺 `"xxx"`：薄封装不把 `\n` 换成空格，`Split(view,"\n")[0]` 只有 `"line-one"`。接线后 `MaxWidth(56)` + `ReplaceAll("\n"," ")` + 真 ensure 才绿。
- 测试 6 未接线时（空桩 ensure、View 未切片）：View 仍是 27+ 行全文，行数 > 12，或同时含 Ports Config 与 About → 红，与测试 4 接线前相同。**不要**靠把 ensure 放进 `View` 让本测试在 Task 4a 变绿（规格 7.8 / Decision 8 禁止）。4a 切完 View 之后本测试必须继续红，直到 Task 4b 给 `ApplyServiceStatus` 加上 ensure：服务行变多会把 GitHub 挤出 12 行窗口（或 naive 只在 `SetSize` 接线时同样被挤出）。

- [ ] **Step 2: 跑测试确认行为红（能编译）**

```console
go test -run "TestView_ShortHeightKeepsFocusedRowVisible|TestView_ErrorDetailPinnedWhileScrolled|TestFocusFirst_PreservesGitHubAndScrolls|TestView_SetSnapshotKeepsGitHubVisible|TestView_LongErrorChromeStaysOneLine|TestView_ApplyServiceStatusKeepsGitHubVisible" ./internal/tui/pages/system -v
```

预期：编译成功，测试失败。**不是** `undefined: scrollY` / `undefined: errorChromeLines` / `undefined: ensureFocusVisible`。失败原因按测试：

- 测试 1–4：短 View 仍含 Ports Config / 行数超过 12。
- 测试 5：`lipgloss.Width(chrome[0]) > 56`；第一行缺 snippet（elevation 缺 `"Administrator"`，multiline 缺 `"line-one"` 或 `"xxx"`）；ensure 空桩 + View 未切片时行数 > 12。接线后若漏掉 ensure，`SliceLines` 会留下 About 标题、clip 掉 GitHub，`!Contains(About) || !Contains(GitHub)` 变红。
- 测试 6：全文仍含 Ports Config、行数 > 12。4a 之后若只切片 / 只在 `SetSize` 接线，服务行变多后 GitHub 被挤出窗口（About 标题可能还在）。

若编译失败，回到 Task 2，不要在本任务里实现滚动。

- [ ] **Step 3: Commit**（仅当用户要求提交时；此时测试是故意红的，若仓库策略不允许提交红测试，把本 commit 与 Task 4a 合并）

```console
git add internal/tui/pages/system/scroll_test.go
git commit -m "test: 增加 System 页短窗口焦点滚动回归"
```

---

### Task 4a: System viewport 接线（chrome、切片、页内 ensure）

本任务让测试 1、2、3、5 变绿。测试 4（`SetSnapshot`）和测试 6（`ApplyServiceStatus`）**必须继续红**，直到 Task 4b。不要靠把 `ensureFocusVisible` 写进 `View` 提前让 4/6 变绿（规格 7.8 / Decision 8）。

**Files:**
- Modify: `internal/tui/pages/system/model.go`
  - import：增加 `lipgloss "charm.land/lipgloss/v2"`（当前该文件无 lipgloss）
  - `SetSize`（约 453）
  - `FocusFirst`（约 462）
  - `Update`（约 615）入口 `defer`
  - `View`（约 1014）
  - `renderSections`（约 1029）改为 `buildSectionContent` 的唯一路径
  - `errorChromeLines` 换成完整 MaxWidth 路径
  - `markRowOutcome`（约 1515）在同步写入之后调 ensure
  - 替换 Task 2 的空桩 `ensureFocusVisible` 为真 clamp
  - **不要**改 `SetSnapshot` / `ApplyServiceStatus` / `SetWebGUI`（那是 Task 4b）

**Interfaces:**
- Consumes: `ui.EnsureLineVisible`、`ui.SliceLines`（Task 1）；Task 2 的 `scrollY` / 薄 chrome / 空桩 `ensureFocusVisible`
- Produces: 测试 1、2、3、5 变绿；测试 4 与测试 6 **仍红**（GitHub 被挤出窗口）。现有非滚动 System 测试仍绿

- [ ] **Step 1: 完整 `errorChromeLines`**

替换 Task 2 的薄封装（顺序：trim → `\n` 变空格 → Danger → `MaxWidth(max(1,width-2))` → 只留第一视觉行）。`width<=0` 时跳过 MaxWidth，但仍压内部换行：

```go
func (m *Model) errorChromeLines() []string {
	detail := strings.TrimSpace(m.visibleErrorDetail())
	if detail == "" {
		return nil
	}
	detail = strings.ReplaceAll(detail, "\n", " ")
	rendered := m.theme.Danger.Render(detail)
	if m.width > 0 {
		rendered = lipgloss.NewStyle().MaxWidth(max(1, m.width-2)).Render(rendered)
	}
	if i := strings.Index(rendered, "\n"); i >= 0 {
		rendered = rendered[:i]
	}
	return []string{rendered}
}
```

禁止 `lipgloss.NewStyle().Width(n)`。`lipgloss.Width(str)` 只出现在测试里。

- [ ] **Step 2: `buildSectionContent` 替换 `renderSections`**

把 `renderSections` 的分组循环收成唯一渲染路径，并返回焦点块 `[focusStart, focusEnd)`（相对分区行，不含 chrome）：

- 焦点行是该分区第一行：焦点块 = 顶边框 + 该 body 行（`focusStart = sectionBase`，`focusEnd = sectionBase + 1 + bodyLineCount`）。
- 否则：焦点块 = 该 row 占用的 body 行（当前 `labelPart+value` 无 `\n`，计 1 行）。用 `strings.Split(rowLine, "\n")` 的行数，以便将来 value 带换行。
- 不把底边框算进焦点块。
- 错误条不放进 `lines`。

删除独立的 `renderSections`，避免两套循环。

参考结构：

```go
func (m *Model) buildSectionContent() (lines []string, focusStart, focusEnd int) {
	focusStart, focusEnd = -1, -1
	inner := ui.FullSectionInner(m.layoutWidth())
	clock := m.rowSpinClock
	if clock.IsZero() {
		clock = time.Unix(0, 0)
	}
	type sectionBuf struct {
		title        string
		body         []string
		focusIndex   int // body index of focused row, -1 if none
		focusIsFirst bool
		focusLines   int
	}
	var sections []sectionBuf
	for _, item := range m.rows() {
		if len(sections) == 0 || sections[len(sections)-1].title != item.section {
			sections = append(sections, sectionBuf{title: item.section, focusIndex: -1})
		}
		idx := len(sections) - 1
		// ... same label/value/chip/focus-style as current renderSections ...
		rowLine := labelPart + value
		rowLines := strings.Split(rowLine, "\n")
		if item.id == m.focusID {
			sections[idx].focusIndex = len(sections[idx].body)
			sections[idx].focusIsFirst = sections[idx].focusIndex == 0
			sections[idx].focusLines = len(rowLines)
		}
		sections[idx].body = append(sections[idx].body, rowLines...)
	}
	for _, sec := range sections {
		body := strings.Join(sec.body, "\n")
		if body == "" {
			body = " "
		}
		section := ui.RenderBorderedSection(m.theme, sec.title, body, inner)
		sectionLines := strings.Split(section, "\n")
		sectionBase := len(lines)
		lines = append(lines, sectionLines...)
		if sec.focusIndex >= 0 {
			bodyOffset := sectionBase + 1
			if sec.focusIsFirst {
				focusStart = sectionBase
				focusEnd = bodyOffset + sec.focusLines
			} else {
				focusStart = bodyOffset + sec.focusIndex
				focusEnd = focusStart + sec.focusLines
			}
		}
	}
	return lines, focusStart, focusEnd
}
```

芯片 / 编辑 / RowFocus 逻辑必须从现有 `renderSections`（`model.go:1045-1074`）**逐行搬**，不要改视觉。这是拷贝指令，不是 TBD。上面 `// ... same label/value/chip ...` 仅因这一句才可接受。

- [ ] **Step 3: 真 `ensureFocusVisible`（替换 Task 2 空桩）与 `View`**

用下面的真 clamp **整函数替换** Task 2 的 `func (m *Model) ensureFocusVisible() {}`。不要另写一个同名方法。

```go
func (m *Model) ensureFocusVisible() {
	lines, focusStart, focusEnd := m.buildSectionContent()
	avail := m.height - len(m.errorChromeLines())
	m.scrollY = ui.EnsureLineVisible(m.scrollY, avail, len(lines), focusStart, focusEnd)
}

func (m *Model) View() string {
	if m.detail != nil {
		return m.theme.Content.Width(m.width).Height(m.height).Render(
			m.theme.Title.Render(strings.TrimSpace(m.detail.label)+" details") + "\n\n" + m.detail.detail + "\n\n" + ui.EscCloseHint,
		)
	}
	errorLines := m.errorChromeLines()
	sectionLines, _, _ := m.buildSectionContent()
	if m.height <= 0 {
		return strings.Join(append(append([]string{}, errorLines...), sectionLines...), "\n")
	}
	if len(errorLines) >= m.height {
		return strings.Join(errorLines[:m.height], "\n")
	}
	avail := m.height - len(errorLines)
	return strings.Join(append(errorLines, ui.SliceLines(sectionLines, m.scrollY, avail)...), "\n")
}
```

`View` **不得**调用 `ensureFocusVisible`。`height>0 && avail==0` 时 **不得** `SliceLines(..., 0)`。把 ensure 放进 `View` 会让测试 4 / 测试 6 在本任务就绿——那是错的，立刻改回来。

- [ ] **Step 4: 页内调用点（不含页外 mutator）**

`Update` 函数**第一行**（`switch` 之前）：

```go
func (m *Model) Update(message tea.Msg) (ui.Page, tea.Cmd) {
	defer func() {
		if m.detail != nil {
			return
		}
		m.ensureFocusVisible()
	}()
	// existing switch ...
```

关闭 overlay 时 `m.detail = nil` 后 defer 仍会 clamp。overlay 仍开着时跳过。`webGUIStatusMsg`（`model.go:724`）走这条 defer，所以 live Web GUI 刷新已被覆盖。

另外必须：

```go
func (m *Model) SetSize(width, height int) {
	m.width, m.height = width, height
	m.ensureFocusVisible()
}

func (m *Model) FocusFirst() {
	if m.rowIndex(m.focusID) < 0 {
		m.focusID = rowDaemon
	}
	m.ensureFocusVisible()
	// 不要把 focusID 打回 rowMixed，不要无条件 scrollY=0
}

func (m *Model) markRowOutcome(rowID string, ok bool, detail string) {
	// existing assignment, including the early return on ok
	// call ensure on every exit (defer at the top of this function is simplest)
	defer m.ensureFocusVisible()
	// ... existing body unchanged ...
}
```

`Load` / `checkMihariVersion` 的同步失败走 `markRowOutcome`，因此 ensure 发生在 chrome +1 **之后**。禁止只把 ensure 放在 `Load()` 顶部。`ApplyRootNetworkStatus` 不必调。

**不要**在本任务给 `SetSnapshot` / `ApplyServiceStatus` / `SetWebGUI` 加 ensure。那是 Task 4b 的闸门。

- [ ] **Step 5: 跑滚动测试——1/2/3/5 绿，4/6 仍红**

```console
go test -run "TestView_ShortHeightKeepsFocusedRowVisible|TestView_ErrorDetailPinnedWhileScrolled|TestFocusFirst_PreservesGitHubAndScrolls|TestView_LongErrorChromeStaysOneLine" ./internal/tui/pages/system -v
```

预期：PASS（测试 5 在完整 chrome + 真 ensure + View 切片后变绿）。

```console
go test -run "TestView_SetSnapshotKeepsGitHubVisible|TestView_ApplyServiceStatusKeepsGitHubVisible" ./internal/tui/pages/system -v
```

预期：**FAIL**。View 已切片，但页外 mutator 未 clamp：`SetSnapshot` 拉长 Network、`ApplyServiceStatus` 拉长 Service 后 GitHub 被挤出 12 行窗口（About 标题可能还在）。若这两条已经 PASS，先检查 `View` 是否违例调用了 ensure——不要继续 4b 去「顺手修」。

- [ ] **Step 6: 现有 System 测试（跳过仍红的两条）**

```console
go test ./internal/tui/pages/system -skip "TestView_SetSnapshotKeepsGitHubVisible|TestView_ApplyServiceStatusKeepsGitHubVisible"
gofmt -w internal/tui/pages/system/model.go internal/tui/pages/system/scroll_test.go
```

预期：PASS。不得改现有测试的 `SetSize` 高度。About 系列不 `SetSize`，依赖 `height<=0` 返回全文。

- [ ] **Step 7: Commit**（仅当用户要求提交时）

```console
git add internal/tui/pages/system/model.go
git commit -m "fix: System 页内容超界时按焦点滚动"
```

---

### Task 4b: 页外 mutator 调用点

本任务让测试 4 与测试 6 变绿。必须把函数体整段贴进 `model.go`，不要写「与 Task 4a 类似」。

**Files:**
- Modify: `internal/tui/pages/system/model.go`
  - `SetSnapshot`（约 468）
  - `ApplyServiceStatus`（约 408）
  - `SetWebGUI`（约 443）

**Interfaces:**
- Consumes: Task 4a 的真 `ensureFocusVisible`
- Produces: `TestView_SetSnapshotKeepsGitHubVisible` 与 `TestView_ApplyServiceStatusKeepsGitHubVisible` 变绿；`go test ./internal/tui/pages/system` 全绿

- [ ] **Step 1: 三个页外 mutator 都调用 ensure**

`SetSnapshot` / `ApplyServiceStatus` 改 `rows()` 且不进 `Update`。`ApplyServiceStatus` 的 live 路径是根 shell `syncSystemServiceStatus`（`internal/tui/model.go:259-265`）。`SetWebGUI` **必须**同样调用 ensure；live 面板行走 `webGUIStatusMsg`，已被 Task 4a 的 `Update` defer 覆盖，本任务不为它再加一条必写测试。

用下面的完整函数体替换现有三个方法（保留原赋值，只在末尾加 ensure）：

```go
func (m *Model) SetSnapshot(status protocol.Status, core protocol.CoreStatus) {
	m.status, m.core = status, core
	m.ensureFocusVisible()
}

func (m *Model) ApplyServiceStatus(status service.StatusKind, loaded bool) {
	if m == nil {
		return
	}
	m.serviceLoaded = loaded
	if loaded {
		m.serviceStatus = status
	}
	m.ensureFocusVisible()
}

func (m *Model) SetWebGUI(status protocol.WebGUIStatus) {
	m.webGUI = status
	m.webGUILoaded = true
	m.ensureFocusVisible()
}
```

- [ ] **Step 2: 跑测试 4 与测试 6 确认通过**

```console
go test -run "TestView_SetSnapshotKeepsGitHubVisible|TestView_ApplyServiceStatusKeepsGitHubVisible" ./internal/tui/pages/system -v
```

预期：PASS。

- [ ] **Step 3: 跑全部 System 测试**

```console
go test ./internal/tui/pages/system
gofmt -w internal/tui/pages/system/model.go
```

预期：PASS。

- [ ] **Step 4: Commit**（仅当用户要求提交时）

```console
git add internal/tui/pages/system/model.go
git commit -m "fix: System 页外 mutator 在行数变化后保持焦点可见"
```

---

### Task 5: Proxies 调用点迁到 helper

**Files:**
- Modify: `internal/tui/pages/proxies/model.go`
  - `View` 约 213–225
  - `ensureFocusVisible` 约 312–340

**Interfaces:**
- Consumes: `ui.EnsureLineVisible`、`ui.SliceLines`（Task 1）
- Produces: 现有 Proxies 导航测试继续绿，断言一字不改

- [ ] **Step 1: 替换 `ensureFocusVisible`**

保留空组 / 未设高 早退（不要把空组交给 helper 的 `n==0 → 0`，以免清掉残留 `scrollY`）：

```go
func (m *Model) ensureFocusVisible() {
	if m.height <= 0 || len(m.groups) == 0 {
		return
	}
	lines, focusStart, focusEnd := m.buildContent()
	m.scrollY = ui.EnsureLineVisible(m.scrollY, m.height, len(lines), focusStart, focusEnd)
}
```

- [ ] **Step 2: 替换 `View` 切片**

空组卡片分支不动。有组时：

```go
func (m *Model) View() string {
	if len(m.groups) == 0 {
		inner := ui.FullSectionInner(m.width)
		body := m.theme.Muted.Render(ui.NoProxyGroups)
		return ui.RenderBorderedSection(m.theme, ui.ProxiesSectionTitle, body, inner)
	}
	lines, _, _ := m.buildContent()
	return strings.Join(ui.SliceLines(lines, m.scrollY, m.height), "\n")
}
```

不要保留第二套手写 `if height > 0 && len(lines) > height`。

**不要改：** `buildContent` 把 `lastError` 插在 `lines[0]`；`FocusFirst` 打回第一组且 `scrollY=0`；`move` / `SetSize` / `SetGroups` / expand 的 ensure 调用点；`scrollY` 仍在 `proxies.Model`。

- [ ] **Step 3: 跑 Proxies 测试确认行为保持**

```console
go test ./internal/tui/pages/proxies
```

预期：PASS，尤其是 `TestNavigation_ScrollKeepsFocusVisible` 与 `TestNavigation_ScrollKeepsExpandedNodeVisible`。若失败，是 helper 语义与旧 clamp 不一致——改 helper 并同时保证 Task 1 表驱动仍绿，不要改这些测试的断言。

- [ ] **Step 4: Commit**（仅当用户要求提交时）

```console
git add internal/tui/pages/proxies/model.go
git commit -m "refactor: Proxies 页改用共享行级视口"
```

---

### Task 6: 规格入库与全量验证

**Files:**
- Create (already on disk, untracked): `docs/superpowers/specs/2026-08-31-system-page-scroll-design.md`
- Create: `docs/superpowers/plans/2026-08-31-system-page-scroll.md`（本计划）

**Interfaces:**
- Consumes: Task 1–4b、5 的全部改动
- Produces: worktree 可提交的完整 #158 变更（仍不 push、不改 CHANGELOG）

- [ ] **Step 1: 格式与 vet**

```console
gofmt -w internal/tui/ui/viewport.go internal/tui/ui/viewport_test.go internal/tui/pages/system/model.go internal/tui/pages/system/scroll_test.go internal/tui/pages/proxies/model.go
gofmt -l internal/tui/ui internal/tui/pages/system internal/tui/pages/proxies
go vet ./internal/tui/ui ./internal/tui/pages/system ./internal/tui/pages/proxies
```

预期：`gofmt -l` 无输出；vet 无报错。

- [ ] **Step 2: 全量 TUI 测试**

```console
go test ./internal/tui/ui
go test ./internal/tui/pages/system
go test ./internal/tui/pages/proxies
go test ./internal/tui/...
```

预期：全部 PASS。不跑 `go test -race`（本改动无新增共享可变状态）。不跑 testenv。

- [ ] **Step 3: 确认未改禁区**

```console
git diff --stat
```

不得出现：`CHANGELOG.md`、`internal/control/protocol/**`、`internal/cli/**`、`internal/tui/model.go`（根裁切）、`VisibleWindow` 函数体被改语义。

- [ ] **Step 4: Commit 规格与计划**（仅当用户要求提交时）

```console
git add docs/superpowers/specs/2026-08-31-system-page-scroll-design.md docs/superpowers/plans/2026-08-31-system-page-scroll.md
git commit -m "docs: System 页焦点滚动设计与实现计划"
```

---

## Self-Review

**Spec coverage:**

| 规格要求 | 任务 |
| --- | --- |
| `EnsureLineVisible` / `SliceLines` 语义与表驱动 | Task 1 |
| 不合并 `VisibleWindow`、对照测试 | Task 1 `TestLineViewportIsNotVisibleWindow` |
| System 桩避免编译失败（`scrollY`、薄 chrome、空桩 `ensureFocusVisible`） | Task 2 |
| 六条 System 红绿测试 | Task 3–4b |
| 钉顶 0/1 行 chrome、`MaxWidth(width-2)`、禁 `Style.Width()`；第一行含错误 snippet / `\n`→空格 | Task 4a Step 1 + 测试 5 |
| `avail==0` 不传给 `SliceLines` | Task 4a Step 3 |
| `Update` defer；Load 路径 ensure 在 `markRowOutcome` 之后 | Task 4a Step 4 |
| `SetSize` / `FocusFirst` / `markRowOutcome` | Task 4a Step 4 |
| `SetSnapshot` / `ApplyServiceStatus` / `SetWebGUI` | Task 4b Step 1 |
| `ApplyServiceStatus` 轮询路径（不进 `Update`） | Task 4b + 测试 6 |
| `SetWebGUI` 必须调 ensure；live 路径 `webGUIStatusMsg` 由 Update defer 覆盖，无额外必写测试 | Task 4a defer + Task 4b |
| System `FocusFirst` 不打回第一行 | Task 4a Step 4 + 测试 3 |
| 分区首行焦点块含顶边框 | Task 4a Step 2 |
| Proxies 行为保持迁入 | Task 5 |
| 单 PR、不改 CHANGELOG / 协议 / 根裁切 | Task 6 Step 3 |
| 规格入库 | Task 6 Step 4 |

**Placeholder scan:** 无 TBD / “类似 Task N”。Task 4a Step 2 的 `// ... same label/value/chip ...` 绑在「逐行搬 `renderSections`（`model.go:1045-1074`）」拷贝指令上。Task 4b 贴了完整函数体。测试与实现代码均内联。

**Type consistency:** helper 签名在 Task 1 Produces 与 Task 4a/5 Consumes 中一致：`EnsureLineVisible(scrollY, viewH, n, focusStart, focusEnd int) int`、`SliceLines(lines []string, scrollY, height int) []string`。`ApplyServiceStatus(status service.StatusKind, loaded bool)` 与 `internal/service` 的 `StatusKind` / `StatusRunning` / `StatusNotInstalled` 一致。
