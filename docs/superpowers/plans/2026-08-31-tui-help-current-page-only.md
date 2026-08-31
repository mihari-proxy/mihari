# TUI `?` 帮助仅展示当前页 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `?` 帮助正文只显示 Global、当前叠加 mode（若有）、当前页键，不再罗列其它页面。

**Architecture:** Catalog 仍是全量 SSOT。只改 `RenderHelp` 的节选择：删掉「其余 rail 页 / 未激活 mode / 非当前 Setup」三段输出。底栏不改。

**Tech Stack:** Go 1.26 / toolchain go1.26.5，bubbletea v2，标准 `go test`。

**Spec:** `docs/superpowers/specs/2026-08-31-tui-help-current-page-only-design.md`

**工作目录（worktree）:** `C:\Users\Kinema\Documents\modular_dev\mihari\.worktrees\feat-162-help-current-page-only`

## Global Constraints

- 分支 `feat/162-help-current-page-only`，从 `origin/dev`（已含 #178）分出；禁止在 `main`/`dev` 上直接改。
- 不改 Catalog 绑定表、不改 `RenderFooter` / `FitFooter` / 页脚视觉。
- 不新增快捷键；不改 `/v1`、CLI、daemon、`CHANGELOG.md`。
- 修改过的 Go 文件必须 `gofmt`；commit 必须 `-s`（DCO）。

---

## File structure

- Modify: `internal/tui/ui/keymap.go` — `RenderHelp` 只写 Global / This mode / This page
- Modify: `internal/tui/ui/keymap_test.go` — 当前页过滤、同键分次渲染
- Modify: `internal/tui/help_test.go` — shell 帮助不再要求出现其它页节
- Modify: `docs/superpowers/specs/2026-08-31-tui-help-page-design.md` — §6 排序与本 spec 对齐

---

### Task 1: `RenderHelp` 只输出当前页

**Files:**
- Modify: `internal/tui/ui/keymap.go`（`RenderHelp`，约 284–321 行）
- Modify: `internal/tui/ui/keymap_test.go`
- Modify: `internal/tui/help_test.go`
- Modify: `docs/superpowers/specs/2026-08-31-tui-help-page-design.md` §6

**Interfaces:**
- Consumes: 现有 `Catalog()`、`RenderHelp(active PageID, mode string) string`
- Produces: 同一签名；正文仅 Global + 可选 This mode + This page

- [ ] **Step 1: 把失败测试改成「当前页过滤」**

将 `TestRenderHelp_CurrentPageComesBeforeOtherPages` 改名为并改写成：

```go
func TestRenderHelp_ShowsOnlyGlobalAndCurrentPage(t *testing.T) {
	body := RenderHelp(PageProxies, "")
	if !strings.Contains(body, "Global:") {
		t.Fatalf("missing Global:\n%s", body)
	}
	if !strings.Contains(body, "This page · "+PageLabel(PageProxies)) {
		t.Fatalf("missing current page:\n%s", body)
	}
	if !strings.Contains(body, "Ctrl+T") || !strings.Contains(body, "test all") {
		t.Fatalf("proxies keys missing:\n%s", body)
	}
	for _, forbidden := range []string{
		PageLabel(PageConnections) + ":",
		PageLabel(PageRules) + ":",
		PageLabel(PageSubscriptions) + ":",
		PageLabel(PageLogs) + ":",
		PageLabel(PageWebGUI) + ":",
		PageLabel(PageSystem) + ":",
		PageLabel(PageSetup) + ":",
		"Search:",
		"Confirm:",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("help leaked other section %q:\n%s", forbidden, body)
		}
	}
}
```

将 `TestRenderHelp_SameKeyKeepsPageSpecificActions` 改为分次渲染（不再要求同一份正文里同时有 Subs/Rules/Web GUI 节）：

```go
func TestRenderHelp_SameKeyKeepsPageSpecificActions(t *testing.T) {
	conn := RenderHelp(PageConnections, "")
	subs := RenderHelp(PageSubscriptions, "")
	rules := RenderHelp(PageRules, "")
	web := RenderHelp(PageWebGUI, "")
	if !strings.Contains(conn, "pause") || strings.Contains(conn, "cycle proxy") {
		t.Fatalf("connections p:\n%s", conn)
	}
	if !strings.Contains(subs, "cycle proxy") || strings.Contains(subs, "pause or resume") {
		t.Fatalf("subscriptions p:\n%s", subs)
	}
	if !strings.Contains(subs, "activate") || strings.Contains(subs, "update the focused provider") {
		t.Fatalf("subscriptions u:\n%s", subs)
	}
	if !strings.Contains(rules, "update the focused provider") {
		t.Fatalf("rules u:\n%s", rules)
	}
	if !strings.Contains(strings.ToLower(web), "update") || strings.Contains(web, "activate") {
		t.Fatalf("web gui u:\n%s", web)
	}
	if strings.Contains(conn, PageLabel(PageSubscriptions)+":") {
		t.Fatalf("connections help listed subscriptions:\n%s", conn)
	}
}
```

`TestRenderHelp_CurrentModeComesAfterGlobal` 保持，并追加：搜索态正文不含 `Rules:`。

`internal/tui/help_test.go` 的 `TestHelpDialog_ShowsCurrentPageFirst`：删掉「同一份 body 必须含 Subs 节」的断言；改为 `RenderHelp(PageProxies, "")` **不含** `PageLabel(PageSubscriptions)+":"`。

- [ ] **Step 2: 跑测试确认失败**

```powershell
cd C:\Users\Kinema\Documents\modular_dev\mihari\.worktrees\feat-162-help-current-page-only
go test ./internal/tui/ui -run 'TestRenderHelp_' -count=1
```

Expected: `TestRenderHelp_ShowsOnlyGlobalAndCurrentPage` FAIL，正文仍含 `Conns:` 或 `Subs:`。

- [ ] **Step 3: 最小实现**

`internal/tui/ui/keymap.go` 的 `RenderHelp`：写完 Global / This mode / This page 之后 **直接** `return strings.TrimRight(b.String(), "\n")`。删除：

```go
	for _, id := range RailPages() { ... }
	for _, m := range []string{ModeSearch, ModeDetail, ModeColumns, ModeForm, ModePortsEdit, ModeConfirm} { ... }
	if active != PageSetup {
		write(PageLabel(PageSetup), ...)
	}
```

`RailPages()` 若因此不再被本文件使用，不要为了「还能用上」而保留循环；只删循环调用即可，不要删 `RailPages` 本身（它仍被 model.go / layout_test.go / render_test.go / render_rail_test.go 使用）。

前序 spec `docs/superpowers/specs/2026-08-31-tui-help-page-design.md` §6 排序规则改为：

1. `Global:` 永远第一。
2. 若 `mode != ""` 且 `mode != ModeSetup`，`This mode · <ModeLabel>:`。
3. `This page · <PageLabel(active)>:`。
4. 停止。不输出其它页、未激活 mode、非当前 Setup。

- [ ] **Step 4: 跑测试确认通过**

```powershell
gofmt -w internal/tui/ui/keymap.go internal/tui/ui/keymap_test.go internal/tui/help_test.go
go test ./internal/tui/ui -run 'TestRenderHelp_|TestCatalog_|TestRenderFooter_' -count=1
go test ./internal/tui -run 'TestHelpDialog_' -count=1
go test ./internal/tui/... -count=1
```

Expected: PASS。底栏测试不得被改坏。

- [ ] **Step 5: Commit**

```powershell
git add internal/tui/ui/keymap.go internal/tui/ui/keymap_test.go internal/tui/help_test.go docs/superpowers/specs/2026-08-31-tui-help-page-design.md docs/superpowers/specs/2026-08-31-tui-help-current-page-only-design.md docs/superpowers/plans/2026-08-31-tui-help-current-page-only.md
git commit -s -m "fix(tui): 帮助页只展示当前页相关快捷键"
```

---

### Task 2: 回归

- [ ] **Step 1:**

```powershell
go test ./internal/tui/... -count=1
go test -race ./internal/tui/... -count=1
go vet ./internal/tui/...
gofmt -l internal/tui
```

Expected: 通过；`gofmt -l` 无输出。

- [ ] **Step 2:** 本机构建 exe（不提交 `bin/`）：

```powershell
$env:CGO_ENABLED = '0'
go build -trimpath -ldflags "-s -w" -o bin/mihari.exe ./cmd/mihari
```

- [ ] **Step 3:** 无额外代码则不要空 commit。

---

## Self-review

1. **Spec coverage:** 只显示 Global + 当前 mode + 当前页 → Task 1；同键分次证明 → Task 1 测试；底栏不动 → 无 footer 改动。
2. **Placeholder scan:** 无 TBD。
3. **Type consistency:** `RenderHelp(PageID, string) string` 不变。
