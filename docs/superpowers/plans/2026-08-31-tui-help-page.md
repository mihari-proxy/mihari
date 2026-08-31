# TUI `?` 帮助页 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 `?` 打开一份完整、可滚动、与真实按键同步的帮助页；每一行都是「键 + 作用」，同一键在不同页的不同作用分节展示、互不覆盖。底栏与帮助从同一张 `Catalog()` 生成。

**Architecture:** `internal/tui/ui/keymap.go` 持有 `[]KeyBinding`（身份是 Scope+Page+Mode，带 `Label` 和可选 `Footer`）。`RenderHelp` 生成帮助；`RenderFooter` / `RenderRailFooter` 生成底栏，输出必须等于现有 footer 字符串。各页 `FooterHints()` 只报告 `(Page, Mode)`。`Modal` 新增 `modalHelp` kind（`NewHelp`），标题带当前页名，`↑/↓` 滚动、`Esc` 关闭。根 shell 用 `RenderHelp(active, mode)` 打开帮助；Setup 非文本步骤通过 `OpenHelpMsg` 打开。

**Tech Stack:** Go 1.26 / toolchain go1.26.5，bubbletea v2，lipgloss v2，标准 `go test`。

**Spec:** `docs/superpowers/specs/2026-08-31-tui-help-page-design.md`

**工作目录（worktree）:** `C:\Users\Kinema\Documents\modular_dev\mihari\.worktrees\feat-162-tui-help-page`

## Global Constraints

- 目标分支 `feat/162-tui-help-page`，从 `origin/dev` 分出；禁止在 `main`/`dev` 上直接改。
- 不改 `/v1`、CLI、daemon、`FitFooter`、确认框文案、`CHANGELOG.md`。
- 不改页脚视觉布局：`RenderFooter` 必须与今天的 `Footer*` 字符串相等；不给 `FormHelp` 补 `?`/`q`。
- 不新增快捷键（帮助滚动只用已有的 `↑/↓` + `Esc`）。
- 不让 catalog 驱动各页 `Update()` 分发。
- Catalog 允许同一 `Keys` 多条绑定；`RenderHelp` 禁止跨页按 Display 合并。每行必须同时有键和说明。底栏只使用当前页/mode 且 `Footer != ""` 的 token。
- 修改过的 Go 文件必须 `gofmt`；错误不得包含订阅 URL / controller secret。
- 测试不访问公网、不读写真实用户目录、不用真实 mihomo。
- Conventional Commits，摘要中文，commit 必须 `-s`（DCO）。指向 `dev` 的功能 PR 不改 `CHANGELOG.md`。

---

## File structure

- Create: `internal/tui/ui/keymap.go` — `KeyBinding`、`Catalog()`、`RenderHelp()`、`RenderFooter()`、`RenderRailFooter()`、由生成结果赋值的 `Footer*` / `FormHelp` 变量
- Create: `internal/tui/ui/keymap_test.go` — catalog / 页脚字节级锁死 / 当前页优先 / 同键异义 / 源码漂移
- Modify: `internal/tui/ui/focus.go` — 增加 `OpenHelpMsg`
- Modify: `internal/tui/ui/strings.go` — 删除 `HelpBody` 和手写 `Footer*` 常量
- Modify: `internal/tui/ui/render.go` — `PageFooterHints` 转调 `RenderFooter`
- Modify: `internal/tui/modal.go` — `modalHelp`、`NewHelp`、滚动 `Update`/`View`
- Modify: `internal/tui/modal_test.go` — 滚动与 Esc
- Modify: `internal/tui/model.go` — `?` → `NewHelp(RenderHelp(...))`；处理 `OpenHelpMsg`
- Modify: `internal/tui/help_test.go` — 打开后含 `Global:` / 当前页；Setup 路径
- Modify: `internal/tui/pages/connections/model.go` 等 — `HelpMode()`
- Modify: `internal/tui/pages/setup/model.go` — 非文本步骤 `?` → `OpenHelpMsg`

---

### Task 1: 按键表 + `RenderHelp` + `RenderFooter`

**Files:**
- Create: `internal/tui/ui/keymap.go`
- Create: `internal/tui/ui/keymap_test.go`
- Modify: `internal/tui/ui/strings.go`（本任务先不删 `HelpBody`，下一任务接线后再删）

**Interfaces:**
- Consumes: `PageID`、`PageLabel`、`RailPages()`、现有 `Footer*` 常量
- Produces:
  - `func Catalog() []KeyBinding`
  - `func RenderHelp(active PageID, mode string) string`
  - `func RenderFooter(page PageID, mode string, opt FooterOpt) string`
  - `func RenderRailFooter() string`
  - constants `ModeSearch` `ModeDetail` `ModeColumns` `ModeForm` `ModePortsEdit` `ModeConfirm` `ModeSetup`
  - `var FooterRail` / `FooterProxies` / … / `FormHelp`（值为生成结果，供现有测试继续引用）

- [ ] **Step 1: 写失败测试**

创建 `internal/tui/ui/keymap_test.go`：

```go
package ui

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRenderHelp_CurrentPageComesBeforeOtherPages(t *testing.T) {
	body := RenderHelp(PageProxies, "")
	thisPage := strings.Index(body, "This page · "+PageLabel(PageProxies))
	subs := strings.Index(body, PageLabel(PageSubscriptions)+":")
	if thisPage < 0 || !strings.Contains(body, "Global:") {
		t.Fatalf("missing Global or current page:\n%s", body)
	}
	if subs < 0 || thisPage > subs {
		t.Fatalf("current page must precede Subscriptions:\n%s", body)
	}
	if !strings.Contains(body, "Ctrl+T") || !strings.Contains(body, "test all") {
		t.Fatalf("proxies keys missing:\n%s", body)
	}
}

func TestRenderHelp_CurrentModeComesAfterGlobal(t *testing.T) {
	body := RenderHelp(PageConnections, ModeSearch)
	global := strings.Index(body, "Global:")
	mode := strings.Index(body, "This mode · Search")
	page := strings.Index(body, "This page · "+PageLabel(PageConnections))
	if global < 0 || mode < 0 || page < 0 || !(global < mode && mode < page) {
		t.Fatalf("order Global < Search < Connections:\n%s", body)
	}
}

func TestRenderHelp_SameKeyKeepsPageSpecificActions(t *testing.T) {
	body := RenderHelp(PageConnections, "")
	conn := helpSection(t, body, "This page · "+PageLabel(PageConnections)+":")
	subs := helpSection(t, body, PageLabel(PageSubscriptions)+":")
	rules := helpSection(t, body, PageLabel(PageRules)+":")
	web := helpSection(t, body, PageLabel(PageWebGUI)+":")
	if !strings.Contains(conn, "p") || !strings.Contains(conn, "pause") {
		t.Fatalf("connections missing p/pause:\n%s", conn)
	}
	if strings.Contains(conn, "cycle proxy") {
		t.Fatalf("subscriptions action leaked into connections:\n%s", conn)
	}
	if !strings.Contains(subs, "p") || !strings.Contains(subs, "cycle proxy") {
		t.Fatalf("subscriptions missing p/cycle proxy:\n%s", subs)
	}
	if !strings.Contains(subs, "u") || !strings.Contains(subs, "activate") {
		t.Fatalf("subscriptions missing u/activate:\n%s", subs)
	}
	if strings.Contains(subs, "update the focused provider") {
		t.Fatalf("rules action leaked into subscriptions:\n%s", subs)
	}
	if !strings.Contains(rules, "u") || !strings.Contains(rules, "update the focused provider") {
		t.Fatalf("rules missing u/update provider:\n%s", rules)
	}
	if !strings.Contains(web, "u") || !strings.Contains(strings.ToLower(web), "update") {
		t.Fatalf("web gui missing u/update:\n%s", web)
	}
	if strings.Contains(web, "activate") {
		t.Fatalf("subscriptions activate leaked into web gui:\n%s", web)
	}
	for _, line := range strings.Split(body, "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasSuffix(trim, ":") {
			continue
		}
		if !strings.Contains(line, "  ") {
			t.Fatalf("help row is not key+action: %q", line)
		}
	}
}

func helpSection(t *testing.T, body, header string) string {
	t.Helper()
	start := strings.Index(body, header)
	if start < 0 {
		t.Fatalf("missing section %q in:\n%s", header, body)
	}
	rest := body[start:]
	lines := strings.Split(rest, "\n")
	var b strings.Builder
	b.WriteString(lines[0])
	b.WriteByte('\n')
	for _, line := range lines[1:] {
		if line != "" && !strings.HasPrefix(line, " ") && strings.HasSuffix(line, ":") {
			break
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func TestCatalog_FooterTokensHaveBindings(t *testing.T) {
	cat := Catalog()
	cases := []struct {
		footer string
		want   []string
	}{
		{FooterRail, []string{"↑/↓", "Enter", "?", "q"}},
		{FooterProxies, []string{"Enter", "t", "Ctrl+T"}},
		{FooterConnections, []string{"/", "x", "p", "Enter"}},
		{FooterRules, []string{"/", "r", "u", "Ctrl+U", "Enter"}},
		{FooterLogs, []string{"/", "p", "w", "G", "Enter"}},
		{FooterSubscriptions, []string{"a", "e", "Space", "p", "r", "Ctrl+R", "u", "d", "Enter"}},
		{FooterWebGUIActions, []string{"Space", "o", "i", "u", "r", "x", "b"}},
		{FooterSystem, []string{"Enter"}},
		{FooterSearchMode, []string{"←/→", "↑/↓", "Esc"}},
		{FooterColumnsMode, []string{"Space", "Enter", "Esc"}},
		{FormHelp, []string{"Tab", "Enter", "Esc"}},
		{FooterPortsEdit, []string{"Enter", "Esc"}},
	}
	displays := map[string]bool{}
	for _, b := range cat {
		displays[b.Display] = true
		for _, key := range strings.FieldsFunc(b.Display, func(r rune) bool {
			return r == '/' || r == ' '
		}) {
			if key != "" {
				displays[key] = true
			}
		}
	}
	for _, tc := range cases {
		for _, token := range tc.want {
			if !displays[token] && !catalogHasDisplayFragment(cat, token) {
				t.Fatalf("footer %q token %q missing from catalog", tc.footer, token)
			}
		}
	}
}

func catalogHasDisplayFragment(cat []KeyBinding, token string) bool {
	for _, b := range cat {
		if b.Display == token || strings.Contains(b.Display, token) {
			return true
		}
	}
	return false
}

func TestCatalog_KeysAppearInHandlerSource(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	uiDir := filepath.Dir(thisFile)
	tuiDir := filepath.Dir(uiDir)
	repo := filepath.Dir(filepath.Dir(tuiDir))
	filesFor := func(b KeyBinding) []string {
		switch {
		case b.Scope == ScopeGlobal:
			return []string{
				filepath.Join(tuiDir, "model.go"),
				filepath.Join(tuiDir, "modal.go"),
			}
		case b.Mode == ModeConfirm:
			return []string{filepath.Join(tuiDir, "modal.go")}
		case b.Mode == ModeSetup || b.Page == PageSetup:
			return []string{filepath.Join(tuiDir, "pages", "setup", "model.go")}
		case b.Page == PageProxies:
			return []string{filepath.Join(tuiDir, "pages", "proxies", "model.go")}
		case b.Page == PageConnections && b.Mode == ModeDetail:
			return []string{filepath.Join(tuiDir, "pages", "connections", "detail.go")}
		case b.Page == PageConnections:
			return []string{filepath.Join(tuiDir, "pages", "connections", "model.go")}
		case b.Page == PageRules:
			return []string{filepath.Join(tuiDir, "pages", "rules", "model.go")}
		case b.Page == PageLogs:
			return []string{filepath.Join(tuiDir, "pages", "logs", "model.go")}
		case b.Page == PageSubscriptions && b.Mode == ModeForm:
			return []string{filepath.Join(tuiDir, "pages", "subscriptions", "form.go")}
		case b.Page == PageSubscriptions:
			return []string{filepath.Join(tuiDir, "pages", "subscriptions", "model.go")}
		case b.Page == PageWebGUI:
			return []string{filepath.Join(tuiDir, "pages", "webgui", "model.go")}
		case b.Page == PageSystem:
			return []string{filepath.Join(tuiDir, "pages", "system", "model.go")}
		case b.Mode == ModeSearch:
			return []string{
				filepath.Join(tuiDir, "pages", "connections", "model.go"),
				filepath.Join(tuiDir, "pages", "rules", "model.go"),
				filepath.Join(tuiDir, "pages", "logs", "model.go"),
			}
		case b.Mode == ModeDetail:
			return []string{
				filepath.Join(tuiDir, "pages", "connections", "detail.go"),
				filepath.Join(tuiDir, "pages", "rules", "model.go"),
				filepath.Join(tuiDir, "pages", "logs", "model.go"),
				filepath.Join(tuiDir, "pages", "subscriptions", "model.go"),
				filepath.Join(tuiDir, "pages", "system", "model.go"),
			}
		case b.Mode == ModeColumns:
			return []string{filepath.Join(tuiDir, "pages", "connections", "model.go")}
		case b.Mode == ModeForm:
			return []string{filepath.Join(tuiDir, "pages", "subscriptions", "form.go")}
		case b.Mode == ModePortsEdit:
			return []string{filepath.Join(tuiDir, "pages", "system", "model.go")}
		default:
			return nil
		}
	}
	_ = repo
	for _, b := range Catalog() {
		for _, key := range b.Keys {
			needle := `"` + key + `"`
			found := false
			for _, file := range filesFor(b) {
				data, err := os.ReadFile(file)
				if err != nil {
					t.Fatalf("read %s: %v", file, err)
				}
				if strings.Contains(string(data), needle) {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("key %q (%s) not found in %#v", key, b.Label, filesFor(b))
			}
		}
	}
}

func TestRenderHelp_IncludesGlobalJumpAndQuit(t *testing.T) {
	body := RenderHelp(PageOverview, "")
	for _, want := range []string{"1–8", "Ctrl+C", "q", "?"} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q:\n%s", want, body)
		}
	}
}

func TestRenderFooter_MatchesCurrentLayout(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"rail", RenderRailFooter(), "↑/↓ page  Enter open  ? help  q quit"},
		{"overview", RenderFooter(PageOverview, "", FooterOpt{}), "Esc back  ? help  q quit"},
		{"proxies", RenderFooter(PageProxies, "", FooterOpt{}), "Esc back  Enter expand  t test  Ctrl+T test all  ? help  q quit"},
		{"connections", RenderFooter(PageConnections, "", FooterOpt{}), "Esc back  / search  x close  p pause  Enter details  ? help  q quit"},
		{"rules", RenderFooter(PageRules, "", FooterOpt{}), "Esc back  / search  r reload  u update  Ctrl+U update all  Enter details  ? help  q quit"},
		{"logs", RenderFooter(PageLogs, "", FooterOpt{}), "Esc back  / search  p pause  w wrap  G newest  Enter details  ? help  q quit"},
		{"subscriptions", RenderFooter(PageSubscriptions, "", FooterOpt{}), "Esc back  Enter details  a add  e edit  Space toggle  p proxy  r refresh  Ctrl+R refresh all  u use  d delete  ? help  q quit"},
		{"webgui-off", RenderFooter(PageWebGUI, "", FooterOpt{}), "Esc back  ? help  q quit"},
		{"webgui-on", RenderFooter(PageWebGUI, "", FooterOpt{WebGUIAvailable: true}), "Esc back  ↑/↓ panel  Space set default  o open  i install  u update  r reinstall  x uninstall  b rollback  ? help  q quit"},
		{"system", RenderFooter(PageSystem, "", FooterOpt{}), "Esc back  Enter activate  ? help  q quit"},
		{"search", RenderFooter(PageConnections, ModeSearch, FooterOpt{}), "Type to filter  ←/→ cursor  ↑/↓ leave  Esc done  ? help  q quit"},
		{"detail", RenderFooter(PageConnections, ModeDetail, FooterOpt{}), "Enter/Esc close  ? help  q quit"},
		{"columns", RenderFooter(PageConnections, ModeColumns, FooterOpt{}), "↑/↓ column  Space toggle  Enter save  Esc cancel  ? help  q quit"},
		{"form", RenderFooter(PageSubscriptions, ModeForm, FooterOpt{}), "Tab/Shift+Tab fields  Enter next/save  Esc cancel"},
		{"ports", RenderFooter(PageSystem, ModePortsEdit, FooterOpt{}), "Type address  Enter apply  Esc cancel  ? help  q quit"},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Fatalf("%s:\n got %q\nwant %q", tc.name, tc.got, tc.want)
		}
	}
	if FooterProxies != cases[2].want || FormHelp != cases[13].want || FooterRail != cases[0].want {
		t.Fatal("exported Footer* aliases must equal RenderFooter output")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run:

```powershell
cd C:\Users\Kinema\Documents\modular_dev\mihari\.worktrees\feat-162-tui-help-page
go test ./internal/tui/ui -run 'TestRenderHelp_|TestCatalog_|TestRenderFooter_' -count=1
```

Expected: FAIL，`RenderHelp` / `Catalog` / `RenderFooter` undefined。

- [ ] **Step 3: 最小实现**

创建 `internal/tui/ui/keymap.go`。必须包含 spec §5.2 的全部绑定（`Footer` 字段按 spec §5.3）；`RenderHelp` 按 spec §6 排序。从 `strings.go` 删掉手写 `Footer*` / `FormHelp` / `FooterPortsEdit` 常量，改由 keymap.go 的 `var` 承接同名导出，避免重复定义。`HelpBody` 本任务先留着。参考实现：

```go
package ui

import (
	"strings"
	"unicode/utf8"
)

type KeyScope uint8

const (
	ScopeGlobal KeyScope = iota
	ScopePage
	ScopeMode
)

const (
	ModeSearch    = "search"
	ModeDetail    = "detail"
	ModeColumns   = "columns"
	ModeForm      = "form"
	ModePortsEdit = "ports-edit"
	ModeConfirm   = "confirm"
	ModeSetup     = "setup"
)

type KeyBinding struct {
	Keys    []string
	Display string
	Label   string
	Footer  string // empty = help-only
	Scope   KeyScope
	Page    PageID
	Mode    string
}

type FooterOpt struct {
	WebGUIAvailable bool
}

func Catalog() []KeyBinding {
	return []KeyBinding{
		{Keys: []string{"1", "2", "3", "4", "5", "6", "7", "8"}, Display: "1–8", Label: "jump to a rail page outside text input", Scope: ScopeGlobal},
		{Keys: []string{"?"}, Display: "?", Label: "this help", Footer: "? help", Scope: ScopeGlobal},
		{Keys: []string{"q"}, Display: "q", Label: "quit outside text input", Footer: "q quit", Scope: ScopeGlobal},
		{Keys: []string{"ctrl+c"}, Display: "Ctrl+C", Label: "quit always", Scope: ScopeGlobal},
		{Keys: []string{"up", "down"}, Display: "↑/↓", Label: "select a rail page", Footer: "↑/↓ page", Scope: ScopeGlobal},
		{Keys: []string{"enter"}, Display: "Enter", Label: "open the selected page from the rail", Footer: "Enter open", Scope: ScopeGlobal},
		{Keys: []string{"esc"}, Display: "Esc", Label: "return to the rail, close a dialog, or step back in Setup", Footer: "Esc back", Scope: ScopeGlobal},
		{Keys: []string{"tab"}, Display: "Tab", Label: "reserved for forms and dialogs", Scope: ScopeGlobal},

		{Keys: []string{"esc"}, Display: "Esc", Label: "return to the rail", Scope: ScopePage, Page: PageOverview},

		{Keys: []string{"enter"}, Display: "Enter", Label: "expand a group or select a node", Footer: "Enter expand", Scope: ScopePage, Page: PageProxies},
		{Keys: []string{"t"}, Display: "t", Label: "test the focused node", Footer: "t test", Scope: ScopePage, Page: PageProxies},
		{Keys: []string{"ctrl+t"}, Display: "Ctrl+T", Label: "test all", Footer: "Ctrl+T test all", Scope: ScopePage, Page: PageProxies},
		{Keys: []string{"up", "down", "left", "right"}, Display: "↑/↓/←/→", Label: "move", Scope: ScopePage, Page: PageProxies},

		{Keys: []string{"/"}, Display: "/", Label: "search", Footer: "/ search", Scope: ScopePage, Page: PageConnections},
		{Keys: []string{"x"}, Display: "x", Label: "close the focused connection", Footer: "x close", Scope: ScopePage, Page: PageConnections},
		{Keys: []string{"p"}, Display: "p", Label: "pause or resume", Footer: "p pause", Scope: ScopePage, Page: PageConnections},
		{Keys: []string{"enter"}, Display: "Enter", Label: "open details or activate a control", Footer: "Enter details", Scope: ScopePage, Page: PageConnections},
		{Keys: []string{"ctrl+x"}, Display: "Ctrl+X", Label: "close all active connections", Scope: ScopePage, Page: PageConnections},

		{Keys: []string{"/"}, Display: "/", Label: "search", Footer: "/ search", Scope: ScopePage, Page: PageRules},
		{Keys: []string{"r"}, Display: "r", Label: "reload", Footer: "r reload", Scope: ScopePage, Page: PageRules},
		{Keys: []string{"u"}, Display: "u", Label: "update the focused provider", Footer: "u update", Scope: ScopePage, Page: PageRules},
		{Keys: []string{"ctrl+u"}, Display: "Ctrl+U", Label: "update all providers", Footer: "Ctrl+U update all", Scope: ScopePage, Page: PageRules},
		{Keys: []string{"enter"}, Display: "Enter", Label: "open details or activate a control", Footer: "Enter details", Scope: ScopePage, Page: PageRules},

		{Keys: []string{"/"}, Display: "/", Label: "search", Footer: "/ search", Scope: ScopePage, Page: PageLogs},
		{Keys: []string{"p"}, Display: "p", Label: "pause or resume", Footer: "p pause", Scope: ScopePage, Page: PageLogs},
		{Keys: []string{"w"}, Display: "w", Label: "wrap", Footer: "w wrap", Scope: ScopePage, Page: PageLogs},
		{Keys: []string{"G"}, Display: "G", Label: "jump to newest", Footer: "G newest", Scope: ScopePage, Page: PageLogs},
		{Keys: []string{"enter"}, Display: "Enter", Label: "open details or activate a control", Footer: "Enter details", Scope: ScopePage, Page: PageLogs},

		{Keys: []string{"enter"}, Display: "Enter", Label: "details", Footer: "Enter details", Scope: ScopePage, Page: PageSubscriptions},
		{Keys: []string{"a"}, Display: "a", Label: "add", Footer: "a add", Scope: ScopePage, Page: PageSubscriptions},
		{Keys: []string{"e"}, Display: "e", Label: "edit", Footer: "e edit", Scope: ScopePage, Page: PageSubscriptions},
		{Keys: []string{"space"}, Display: "Space", Label: "enable or disable", Footer: "Space toggle", Scope: ScopePage, Page: PageSubscriptions},
		{Keys: []string{"p"}, Display: "p", Label: "cycle proxy mode", Footer: "p proxy", Scope: ScopePage, Page: PageSubscriptions},
		{Keys: []string{"r"}, Display: "r", Label: "refresh", Footer: "r refresh", Scope: ScopePage, Page: PageSubscriptions},
		{Keys: []string{"ctrl+r"}, Display: "Ctrl+R", Label: "refresh all", Footer: "Ctrl+R refresh all", Scope: ScopePage, Page: PageSubscriptions},
		{Keys: []string{"u"}, Display: "u", Label: "activate", Footer: "u use", Scope: ScopePage, Page: PageSubscriptions},
		{Keys: []string{"d"}, Display: "d", Label: "delete", Footer: "d delete", Scope: ScopePage, Page: PageSubscriptions},

		{Keys: []string{"up", "down", "k", "j"}, Display: "↑/↓", Label: "select a panel", Footer: "↑/↓ panel", Scope: ScopePage, Page: PageWebGUI},
		{Keys: []string{"space"}, Display: "Space", Label: "set default", Footer: "Space set default", Scope: ScopePage, Page: PageWebGUI},
		{Keys: []string{"o"}, Display: "o", Label: "open", Footer: "o open", Scope: ScopePage, Page: PageWebGUI},
		{Keys: []string{"i"}, Display: "i", Label: "install", Footer: "i install", Scope: ScopePage, Page: PageWebGUI},
		{Keys: []string{"u"}, Display: "u", Label: "update", Footer: "u update", Scope: ScopePage, Page: PageWebGUI},
		{Keys: []string{"r"}, Display: "r", Label: "reinstall", Footer: "r reinstall", Scope: ScopePage, Page: PageWebGUI},
		{Keys: []string{"x", "d"}, Display: "x / d", Label: "uninstall", Footer: "x uninstall", Scope: ScopePage, Page: PageWebGUI},
		{Keys: []string{"b"}, Display: "b", Label: "rollback", Footer: "b rollback", Scope: ScopePage, Page: PageWebGUI},

		{Keys: []string{"enter"}, Display: "Enter", Label: "activate the focused row", Footer: "Enter activate", Scope: ScopePage, Page: PageSystem},
		{Keys: []string{"up", "down"}, Display: "↑/↓", Label: "move", Scope: ScopePage, Page: PageSystem},

		{Display: "type", Label: "filter the list", Footer: "Type to filter", Scope: ScopeMode, Mode: ModeSearch},
		{Keys: []string{"left", "right"}, Display: "←/→", Label: "move cursor", Footer: "←/→ cursor", Scope: ScopeMode, Mode: ModeSearch},
		{Keys: []string{"up", "down"}, Display: "↑/↓", Label: "leave the field", Footer: "↑/↓ leave", Scope: ScopeMode, Mode: ModeSearch},
		{Keys: []string{"esc"}, Display: "Esc", Label: "finish search", Footer: "Esc done", Scope: ScopeMode, Mode: ModeSearch},

		{Keys: []string{"enter", "esc"}, Display: "Enter / Esc", Label: "close", Footer: "Enter/Esc close", Scope: ScopeMode, Mode: ModeDetail},
		{Keys: []string{"left", "right"}, Display: "←/→", Label: "switch tabs", Scope: ScopePage, Page: PageConnections, Mode: ModeDetail},
		{Keys: []string{"up", "down"}, Display: "↑/↓", Label: "scroll", Scope: ScopePage, Page: PageConnections, Mode: ModeDetail},

		{Keys: []string{"up", "down"}, Display: "↑/↓", Label: "move", Footer: "↑/↓ column", Scope: ScopeMode, Mode: ModeColumns},
		{Keys: []string{"space"}, Display: "Space", Label: "toggle", Footer: "Space toggle", Scope: ScopeMode, Mode: ModeColumns},
		{Keys: []string{"enter"}, Display: "Enter", Label: "save", Footer: "Enter save", Scope: ScopeMode, Mode: ModeColumns},
		{Keys: []string{"esc"}, Display: "Esc", Label: "cancel", Footer: "Esc cancel", Scope: ScopeMode, Mode: ModeColumns},

		{Keys: []string{"tab", "shift+tab"}, Display: "Tab / Shift+Tab", Label: "move between fields", Footer: "Tab/Shift+Tab fields", Scope: ScopeMode, Mode: ModeForm},
		{Keys: []string{"enter"}, Display: "Enter", Label: "next or save", Footer: "Enter next/save", Scope: ScopeMode, Mode: ModeForm},
		{Keys: []string{"esc"}, Display: "Esc", Label: "cancel", Footer: "Esc cancel", Scope: ScopeMode, Mode: ModeForm},

		{Display: "type", Label: "edit the address", Footer: "Type address", Scope: ScopeMode, Mode: ModePortsEdit},
		{Keys: []string{"enter"}, Display: "Enter", Label: "apply", Footer: "Enter apply", Scope: ScopeMode, Mode: ModePortsEdit},
		{Keys: []string{"esc"}, Display: "Esc", Label: "cancel", Footer: "Esc cancel", Scope: ScopeMode, Mode: ModePortsEdit},

		{Keys: []string{"tab", "shift+tab", "left", "right"}, Display: "Tab / ←/→", Label: "toggle Confirm / Cancel", Scope: ScopeMode, Mode: ModeConfirm},
		{Keys: []string{"enter"}, Display: "Enter", Label: "activate the selected button", Scope: ScopeMode, Mode: ModeConfirm},
		{Keys: []string{"esc"}, Display: "Esc", Label: "cancel", Scope: ScopeMode, Mode: ModeConfirm},

		{Keys: []string{"tab", "shift+tab"}, Display: "Tab / Shift+Tab", Label: "move between fields", Scope: ScopePage, Page: PageSetup},
		{Keys: []string{"enter"}, Display: "Enter", Label: "continue", Scope: ScopePage, Page: PageSetup},
		{Keys: []string{"esc"}, Display: "Esc", Label: "previous step, or cancel on the first step", Scope: ScopePage, Page: PageSetup},
		{Keys: []string{"q"}, Display: "q", Label: "quit on non-text steps", Scope: ScopePage, Page: PageSetup},
		{Keys: []string{"?"}, Display: "?", Label: "this help on non-text steps", Scope: ScopePage, Page: PageSetup},
	}
}

func footerTokens(keep func(KeyBinding) bool) []string {
	var out []string
	seen := map[string]bool{}
	for _, b := range Catalog() {
		if b.Footer == "" || seen[b.Footer] || !keep(b) {
			continue
		}
		seen[b.Footer] = true
		out = append(out, b.Footer)
	}
	return out
}

func joinFooter(parts []string) string {
	clean := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			clean = append(clean, p)
		}
	}
	return strings.Join(clean, "  ")
}

func RenderRailFooter() string {
	tokens := footerTokens(func(b KeyBinding) bool {
		return b.Scope == ScopeGlobal && (b.Footer == "↑/↓ page" || b.Footer == "Enter open")
	})
	return joinFooter(append(tokens, "? help", "q quit"))
}

func RenderFooter(page PageID, mode string, opt FooterOpt) string {
	helpQuit := []string{"? help", "q quit"}
	switch mode {
	case ModeSearch, ModeDetail, ModeColumns, ModePortsEdit:
		tokens := footerTokens(func(b KeyBinding) bool {
			return b.Mode == mode && (b.Page == "" || b.Page == page)
		})
		return joinFooter(append(tokens, helpQuit...))
	case ModeForm:
		return joinFooter(footerTokens(func(b KeyBinding) bool { return b.Mode == ModeForm }))
	default:
		if page == PageWebGUI && !opt.WebGUIAvailable {
			return joinFooter(append([]string{"Esc back"}, helpQuit...))
		}
		tokens := footerTokens(func(b KeyBinding) bool {
			return b.Scope == ScopePage && b.Page == page && b.Mode == ""
		})
		return joinFooter(append(append([]string{"Esc back"}, tokens...), helpQuit...))
	}
}

var (
	FooterRail          = RenderRailFooter()
	FooterContent       = RenderFooter(PageOverview, "", FooterOpt{})
	FooterOverview      = FooterContent
	FooterProxies       = RenderFooter(PageProxies, "", FooterOpt{})
	FooterConnections   = RenderFooter(PageConnections, "", FooterOpt{})
	FooterRules         = RenderFooter(PageRules, "", FooterOpt{})
	FooterLogs          = RenderFooter(PageLogs, "", FooterOpt{})
	FooterSubscriptions = RenderFooter(PageSubscriptions, "", FooterOpt{})
	FooterWebGUI        = RenderFooter(PageWebGUI, "", FooterOpt{})
	FooterWebGUIActions = RenderFooter(PageWebGUI, "", FooterOpt{WebGUIAvailable: true})
	FooterSystem        = RenderFooter(PageSystem, "", FooterOpt{})
	FooterSearchMode    = RenderFooter(PageConnections, ModeSearch, FooterOpt{})
	FooterDetailMode    = RenderFooter(PageConnections, ModeDetail, FooterOpt{})
	FooterColumnsMode   = RenderFooter(PageConnections, ModeColumns, FooterOpt{})
	FooterPortsEdit     = RenderFooter(PageSystem, ModePortsEdit, FooterOpt{})
	FormHelp            = RenderFooter(PageSubscriptions, ModeForm, FooterOpt{})
)

func RenderHelp(active PageID, mode string) string {
	cat := Catalog()
	var b strings.Builder
	write := func(title string, rows []KeyBinding) {
		if len(rows) == 0 {
			return
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(title)
		b.WriteString(":\n")
		width := 8
		for _, row := range rows {
			if n := utf8.RuneCountInString(row.Display); n > width {
				width = n
			}
		}
		seen := map[string]bool{}
		for _, row := range rows {
			key := row.Display + "\t" + row.Label
			if seen[key] {
				continue
			}
			seen[key] = true
			pad := width - utf8.RuneCountInString(row.Display)
			b.WriteString("  ")
			b.WriteString(row.Display)
			b.WriteString(strings.Repeat(" ", pad+2))
			b.WriteString(row.Label)
			b.WriteString("\n")
		}
	}

	write("Global", filter(cat, func(x KeyBinding) bool { return x.Scope == ScopeGlobal }))

	if mode != "" && mode != ModeSetup {
		write("This mode · "+modeTitle(mode), filter(cat, func(x KeyBinding) bool {
			if x.Mode != mode {
				return false
			}
			return x.Page == "" || x.Page == active
		}))
	}

	write("This page · "+PageLabel(active), filter(cat, func(x KeyBinding) bool {
		return x.Scope == ScopePage && x.Page == active && x.Mode == ""
	}))

	for _, id := range RailPages() {
		if id == active {
			continue
		}
		write(PageLabel(id), filter(cat, func(x KeyBinding) bool {
			return x.Scope == ScopePage && x.Page == id && x.Mode == ""
		}))
	}

	for _, m := range []string{ModeSearch, ModeDetail, ModeColumns, ModeForm, ModePortsEdit, ModeConfirm} {
		if m == mode {
			continue
		}
		write(modeTitle(m), filter(cat, func(x KeyBinding) bool {
			return x.Scope == ScopeMode && x.Mode == m
		}))
	}
	if active != PageSetup {
		write(PageLabel(PageSetup), filter(cat, func(x KeyBinding) bool {
			return x.Page == PageSetup && x.Mode == ""
		}))
	}
	return strings.TrimRight(b.String(), "\n")
}

func modeTitle(mode string) string {
	switch mode {
	case ModeSearch:
		return "Search"
	case ModeDetail:
		return "Detail"
	case ModeColumns:
		return "Columns"
	case ModeForm:
		return "Form"
	case ModePortsEdit:
		return "Ports edit"
	case ModeConfirm:
		return "Confirm"
	case ModeSetup:
		return "Setup"
	default:
		return mode
	}
}

func filter(in []KeyBinding, keep func(KeyBinding) bool) []KeyBinding {
	out := make([]KeyBinding, 0, len(in))
	for _, item := range in {
		if keep(item) {
			out = append(out, item)
		}
	}
	return out
}
```

Web GUI 的 `↑/↓` 用一条绑定收录 `up`/`down`/`k`/`j`，帮助里只出现一行；源码漂移测试仍会检查四个键名。

若 `TestCatalog_FooterTokensHaveBindings` 因 `x / d` 不含裸 `x` 失败：把 Web GUI Display 改成 `"x"` 加单独 `"d"` 行，或让测试接受 `x / d` 包含 `x`（上面的 `catalogHasDisplayFragment` 已用 `Contains`）。

- [ ] **Step 4: 跑测试确认通过**

```powershell
gofmt -w internal/tui/ui/keymap.go internal/tui/ui/keymap_test.go
go test ./internal/tui/ui -run 'TestRenderHelp_|TestCatalog_|TestRenderFooter_' -count=1
```

Expected: PASS。若源码漂移失败，按失败的 `key` 核对 spec：没绑的键从表里删掉，绑了的补进 catalog 或修正 `filesFor`。若 `TestRenderFooter_MatchesCurrentLayout` 失败，只改 catalog 的 `Footer` 字段或 `RenderFooter` 拼接顺序，禁止再手写一串平行 footer。

- [ ] **Step 5: Commit**

```powershell
git add internal/tui/ui/keymap.go internal/tui/ui/keymap_test.go internal/tui/ui/strings.go
git commit -s -m "feat(tui): 按键表同时生成帮助与页脚"
```

---

### Task 2: 可滚动 `NewHelp` modal

**Files:**
- Modify: `internal/tui/modal.go`
- Modify: `internal/tui/modal_test.go`

**Interfaces:**
- Consumes: Task 1 的长 `RenderHelp` 正文（本任务用任意多行字符串即可）
- Produces: `func NewHelp(title, body string) *Modal`；`Update` 识别 `up`/`down`；`View` 限制在 `height-2` 内并画 `▴` / `▾ N more lines`

- [ ] **Step 1: 写失败测试**

在 `internal/tui/modal_test.go` 追加：

```go
func TestHelpModal_ScrollsInsideCompactTerminal(t *testing.T) {
	lines := make([]string, 0, 40)
	for i := 0; i < 40; i++ {
		lines = append(lines, fmt.Sprintf("line-%02d extra text to force wrapping checks", i))
	}
	modal := NewHelp("Keyboard help", strings.Join(lines, "\n"))
	view := modal.View(72, 22)
	if strings.Contains(view, "line-39") {
		t.Fatalf("compact help showed last line without scrolling: %s", view)
	}
	if !strings.Contains(view, "▾") {
		t.Fatalf("compact help missing overflow marker: %s", view)
	}
	for _, line := range strings.Split(view, "\n") {
		if w := lipgloss.Width(line); w > 72 {
			t.Fatalf("help line width %d > 72: %q", w, line)
		}
	}
	if modal.Update(tea.KeyPressMsg{Code: tea.KeyDown}) != ModalNone {
		t.Fatal("down should scroll, not close")
	}
	scrolled := modal.View(72, 22)
	if !strings.Contains(scrolled, "line-") {
		t.Fatalf("scrolled view empty: %s", scrolled)
	}
	if scrolled == view {
		t.Fatal("down did not change help view")
	}
	if modal.Update(tea.KeyPressMsg{Code: tea.KeyEscape}) != ModalClose {
		t.Fatal("esc should close help")
	}
}

func TestHelpModal_IgnoresEnterAndTab(t *testing.T) {
	modal := NewHelp("Keyboard help", "body")
	if modal.Update(tea.KeyPressMsg{Code: tea.KeyEnter}) != ModalNone {
		t.Fatal("enter closed help")
	}
	if modal.Update(tea.KeyPressMsg{Code: tea.KeyTab}) != ModalNone {
		t.Fatal("tab closed help")
	}
}
```

在文件 import 中补 `fmt` 和 `lipgloss "charm.land/lipgloss/v2"`（若尚未导入）。

- [ ] **Step 2: 跑测试确认失败**

```powershell
go test ./internal/tui -run 'TestHelpModal_' -count=1
```

Expected: FAIL，`NewHelp` undefined。

- [ ] **Step 3: 最小实现**

`internal/tui/modal.go`：

1. `modalKind` 增加 `modalHelp`。
2. `Modal` 增加 `scroll int`。
3. `NewHelp`：

```go
func NewHelp(title, body string) *Modal {
	return &Modal{kind: modalHelp, title: title, body: body}
}
```

4. `Update`：在 `esc` → `ModalClose` 之后、confirmation 分支之前：

```go
if m.kind == modalHelp {
    switch key.String() {
    case "up":
        m.scroll = max(0, m.scroll-1)
    case "down":
        m.scroll++
    }
    return ModalNone
}
```

5. `View`：当 `m.kind == modalHelp` 时按 Connections 详情同样的窗口算法切 `strings.Split(m.body, "\n")`：
   - `boxWidth := min(64, max(24, width-8))`
   - `maxBoxHeight := max(5, height-2)`
   - 标题 1 行 + 空行 1 行 + 边框约 2 行 → `visibleHeight := max(1, maxBoxHeight-4)`
   - 先按 `scroll` 切片，若需要 `▴` / `▾ N more lines` 再各占一行，重算 `contentRows`
   - `m.scroll` clamp 到 `max(0, len(lines)-contentRows)`
   - `theme.Dialog.Width(boxWidth).MaxHeight(maxBoxHeight)` 包住 `title + "\n\n" + body`
   - `lipgloss.Place` 居中

`▾ N more lines` 用 `fmt.Sprintf("▾ %d more lines", len(lines)-end)`，muted 样式与 `pages/connections/detail.go` 一致。

`NewDetail` / `NewConfirmation` 的 `View` 路径保持原样（不要给短详情加滚动）。

- [ ] **Step 4: 跑测试确认通过**

```powershell
gofmt -w internal/tui/modal.go internal/tui/modal_test.go
go test ./internal/tui -run 'TestHelpModal_|TestDetailModal|TestConfirmation' -count=1
```

Expected: PASS。

- [ ] **Step 5: Commit**

```powershell
git add internal/tui/modal.go internal/tui/modal_test.go
git commit -s -m "feat(tui): 帮助弹层支持在最小终端里滚动"
```

---

### Task 3: Shell 接线 + `HelpMode` + Setup `?`

**Files:**
- Modify: `internal/tui/ui/focus.go`
- Modify: `internal/tui/ui/strings.go`（删除 `HelpBody`）
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/help_test.go`
- Modify: `internal/tui/pages/connections/model.go`
- Modify: `internal/tui/pages/rules/model.go`
- Modify: `internal/tui/pages/logs/model.go`
- Modify: `internal/tui/pages/subscriptions/model.go`
- Modify: `internal/tui/pages/system/model.go`
- Modify: `internal/tui/pages/setup/model.go`
- Test: `internal/tui/pages/setup` 若已有按键测试文件则追加，否则写在 `help_test.go`

**Interfaces:**
- Consumes: `RenderHelp`、`NewHelp`、`Catalog` mode 常量
- Produces:
  - `type OpenHelpMsg struct{}`
  - `type HelpModeProvider interface { HelpMode() string }`（可就地定义在 `model.go` 或 `ui/page.go`）
  - 各页 `HelpMode() string`
  - Setup 非文本步骤 `?` 返回 `OpenHelpMsg`

- [ ] **Step 1: 写失败测试**

`internal/tui/help_test.go` 现有 `TestHelpDialogOpensFromRailAndContentAndClosesOnEsc` 继续用。追加：

```go
func TestHelpDialog_ShowsCurrentPageFirst(t *testing.T) {
	model := NewModel()
	model.active = ui.PageProxies
	model.railIndex = 1
	model.focus = ui.Focus{Area: ui.FocusContent, Page: ui.PageProxies}
	model = updateModelKey(t, model, tea.KeyPressMsg{Code: '?', Text: "?"})
	content := model.View().Content
	if model.modal == nil || !strings.Contains(content, ui.HelpTitle) {
		t.Fatalf("help did not open: %s", content)
	}
	thisPage := strings.Index(content, "This page · "+ui.PageLabel(ui.PageProxies))
	subs := strings.Index(content, ui.PageLabel(ui.PageSubscriptions)+":")
	if thisPage < 0 || subs < 0 || thisPage > subs {
		t.Fatalf("current page not first:\n%s", content)
	}
	if strings.Contains(content, ui.HelpBody) {
		t.Fatal("stale HelpBody still rendered")
	}
}

func TestHelpDialog_SearchModeSectionComesFirst(t *testing.T) {
	model := NewModel()
	model.width, model.height = 80, 24
	model.resizePages()
	model.active = ui.PageConnections
	model.railIndex = 2
	model.focus = ui.Focus{Area: ui.FocusContent, Page: ui.PageConnections}
	page, ok := model.pages[ui.PageConnections].(*connectionspage.Model)
	if !ok {
		t.Fatal("connections page missing")
	}
	page.SetContentFocused(true)
	page.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	model = updateModelKey(t, model, tea.KeyPressMsg{Code: '?', Text: "?"})
	content := model.View().Content
	mode := strings.Index(content, "This mode · Search")
	pageIdx := strings.Index(content, "This page · "+ui.PageLabel(ui.PageConnections))
	if mode < 0 || pageIdx < 0 || mode > pageIdx {
		t.Fatalf("search mode should precede page:\n%s", content)
	}
}

func TestHelpDialog_OpensFromSetupOnNonTextStep(t *testing.T) {
	model := NewModel()
	model.active = ui.PageSetup
	model.focus = ui.Focus{Area: ui.FocusContent, Page: ui.PageSetup}
	if page, ok := model.pages[ui.PageSetup].(*setuppage.Model); ok {
		page.SetStepForTest(setuppage.StepCore) // 若没有导出，改用公开测试钩子或在 setup 包内测 OpenHelpMsg
	}
	updated, _ := model.Update(ui.OpenHelpMsg{})
	model = updated.(Model)
	if model.modal == nil || !strings.Contains(model.View().Content, ui.HelpTitle) {
		t.Fatal("OpenHelpMsg did not open help")
	}
}
```

**不要发明 `SetStepForTest`。** 若 setup 模型没有测试钩子，把 Setup `?` 行为测在 `internal/tui/pages/setup`：

```go
func TestModel_QuestionMarkOpensHelpOnNonTextStep(t *testing.T) {
	model := newTestModel(t) // 使用该文件已有的构造 helper，名称以仓库实际为准
	model.step = stepCore
	_, cmd := model.Update(tea.KeyPressMsg{Code: '?', Text: "?"})
	if cmd == nil {
		t.Fatal("expected OpenHelpMsg command")
	}
	msg := cmd()
	if _, ok := msg.(ui.OpenHelpMsg); !ok {
		t.Fatalf("got %T", msg)
	}
}

func TestModel_QuestionMarkStaysInEndpointField(t *testing.T) {
	model := newTestModel(t)
	model.step = stepEndpoints
	// ensure inputs exist as existing tests do
	_, cmd := model.Update(tea.KeyPressMsg{Code: '?', Text: "?"})
	if cmd != nil {
		if _, ok := cmd().(ui.OpenHelpMsg); ok {
			t.Fatal("typed ? in endpoints opened help")
		}
	}
}
```

先读 `internal/tui/pages/setup/model_test.go` 里现有的 model 构造函数再写，不要复制一个不存在的 `newTestModel`。

`TestHelpDialog_ShowsCurrentPageFirst` 引用 `ui.HelpBody`：本任务删除该常量后，把那条断言改成确认正文含 `Global:` 且不含旧句子 `Subscriptions: a add`。

- [ ] **Step 2: 跑测试确认失败**

```powershell
go test ./internal/tui -run 'TestHelpDialog_' -count=1
```

Expected: `OpenHelpMsg` undefined，或帮助仍渲染 `HelpBody`。

- [ ] **Step 3: 最小实现**

1. `internal/tui/ui/focus.go` 在 `InputModeMsg` 旁：

```go
// OpenHelpMsg asks the root shell to open the keyboard help overlay.
type OpenHelpMsg struct{}
```

2. `internal/tui/ui/page.go` 或 `render.go`：

```go
type HelpModeProvider interface {
	HelpMode() string
}
```

3. 各页 `HelpMode()` 与现有 `FooterHints()` 同分支。然后把 `FooterHints()` 改成只转调 `RenderFooter`，不要再 return 手写常量。例子（connections）：

```go
func (m *Model) HelpMode() string {
	switch {
	case m.detail != nil:
		return ui.ModeDetail
	case m.columnsOpen:
		return ui.ModeColumns
	case m.searching:
		return ui.ModeSearch
	default:
		return ""
	}
}

func (m *Model) FooterHints() string {
	return ui.RenderFooter(m.ID(), m.HelpMode(), ui.FooterOpt{})
}
```

Web GUI：`return ui.RenderFooter(m.ID(), "", ui.FooterOpt{WebGUIAvailable: m.available})`。
`PageFooterHints` 改为 `return RenderFooter(id, "", FooterOpt{})`。
订阅表单 overlay 里的 `ui.FormHelp` 可继续用导出变量（已是生成结果），或同样改成 `RenderFooter(PageSubscriptions, ModeForm, FooterOpt{})`。

subscriptions：form → `ModeForm`，detail → `ModeDetail`。
system：`editID != ""` → `ModePortsEdit`。
rules/logs：detail / searching 同 connections。

4. `internal/tui/model.go` 的 `Update`：

- 在 `case ui.ConfirmationRequestMsg` 旁处理 `case ui.OpenHelpMsg:` → `model.openHelp()`。
- 抽出：

```go
func (model *Model) openHelp() (Model, tea.Cmd) {
	mode := ""
	if page, ok := model.pages[model.active].(ui.HelpModeProvider); ok {
		mode = page.HelpMode()
	}
	model.modal = NewHelp(ui.HelpTitle+" · "+ui.PageLabel(model.active), ui.RenderHelp(model.active, mode))
	return model, nil
}
```

- `?` 处理改为：`active == PageSetup` 时不要在 shell 抢键；否则 `return model.openHelp()`。顺序仍是：`ctrl+c` → modal → `?`（非 Setup）→ Setup dispatch → `q` → …。

5. `internal/tui/pages/setup/model.go`：在 `esc` 分支附近，若 `key.String() == "?"` 且 `m.step != stepEndpoints && m.step != stepSubscription`：

```go
return m, func() tea.Msg { return ui.OpenHelpMsg{} }
```

文本步骤不要拦截 `?`。

6. 删除 `ui.HelpBody`。全仓库搜 `HelpBody`，测试改为断言 `Global:`。

- [ ] **Step 4: 跑测试确认通过**

```powershell
gofmt -w internal/tui/model.go internal/tui/help_test.go internal/tui/ui/focus.go internal/tui/ui/page.go internal/tui/ui/strings.go internal/tui/pages/connections/model.go internal/tui/pages/rules/model.go internal/tui/pages/logs/model.go internal/tui/pages/subscriptions/model.go internal/tui/pages/system/model.go internal/tui/pages/setup/model.go internal/tui/pages/setup/model_test.go
go test ./internal/tui ./internal/tui/ui ./internal/tui/pages/... -count=1
```

Expected: PASS。`HelpBody` 无引用。

- [ ] **Step 5: Commit**

```powershell
git add internal/tui/model.go internal/tui/help_test.go internal/tui/ui/focus.go internal/tui/ui/page.go internal/tui/ui/strings.go internal/tui/pages/connections/model.go internal/tui/pages/rules/model.go internal/tui/pages/logs/model.go internal/tui/pages/subscriptions/model.go internal/tui/pages/system/model.go internal/tui/pages/setup/model.go internal/tui/pages/setup/model_test.go
git commit -s -m "feat(tui): 按当前页打开可滚动快捷键帮助"
```

---

### Task 4: 回归与格式

**Files:**
- 本任务只跑验证，不新增功能文件。若 compact golden 因无 modal 的 View 未变则不动 `testdata/`。

- [ ] **Step 1: 包测试**

```powershell
cd C:\Users\Kinema\Documents\modular_dev\mihari\.worktrees\feat-162-tui-help-page
go test ./internal/tui/... -count=1
go test -race ./internal/tui/... -count=1
go vet ./internal/tui/...
gofmt -l internal/tui
```

Expected: 测试通过；`gofmt -l` 无输出（Windows `core.autocrlf` 误报时以 CI LF 为准，仍应对刚改的文件跑 `gofmt -w`）。

- [ ] **Step 2: 编译检查（本机 Windows + 无 CGO 交叉）**

```powershell
$env:CGO_ENABLED = '0'
go build -o bin/mihari-windows-amd64.exe ./cmd/mihari
$env:GOOS = 'linux'; $env:GOARCH = 'amd64'; go build -o bin/mihari-linux-amd64 ./cmd/mihari
$env:GOOS = 'darwin'; $env:GOARCH = 'arm64'; go build -o bin/mihari-darwin-arm64 ./cmd/mihari
```

Expected: 三个目标都能编过。不要把 `bin/` 产物提交。

- [ ] **Step 3: 扩大测试（若 Task 1–3 只动了 tui）**

```powershell
go test ./... -count=1
```

Expected: PASS。不必为这个 TUI-only 变更强跑全仓 `-race`，但 `./internal/tui/... -race` 必须过。

- [ ] **Step 4: Commit（仅当 Step 1–3 改了测试或格式）**

无代码改动则不要空 commit。若 `gofmt` 动了文件：

```powershell
git add -u internal/tui
git commit -s -m "test(tui): 帮助页回归与格式"
```

---

## Self-review

1. **Spec coverage:** §5 catalog + §5.3 footer recipe → Task 1（`RenderFooter` 字节级锁死）；§6 RenderHelp 顺序与同键异义 → Task 1 测试；§7 滚动 → Task 2；§8 shell/Setup/`HelpMode` + §9 页脚转调 `RenderFooter` → Task 3；§10 测试清单 → Task 1–3；§11 不改 README/CHANGELOG → 无对应文件。
2. **Placeholder scan:** Setup 测试 helper 名要求执行者先读 `model_test.go`，这是必要的仓库本地名字，不是 TBD。
3. **Type consistency:** `OpenHelpMsg`、`HelpMode()`、`FooterOpt`、`ModeSearch` 等与 spec 一致；`NewHelp` 取代帮助路径上的 `NewDetail`；`HelpBody` 在 Task 3 删除；`Footer*` / `FormHelp` 改为生成结果的 `var`。
