# Proxies 页节点切换失败反馈 Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** proxies 页切换节点失败时显示可见的红色错误提示，并让 `selectionResultMsg` 实现 shell 的 action-outcome 契约（#80）。

**Architecture:** 纯 TUI 单页改动。`Model` 增加 `lastError`（rules 页同款模式）；`selectionResultMsg` 失败时设 `ui.ProxySelectFailed`、成功时清空；`buildContent` 首行以 `theme.Danger` 渲染错误；`selectFocused` 发起新选择前清旧错误。后端零改动。

**Tech Stack:** Go + bubbletea v2 + lipgloss v2，标准 `go test`。

**设计文档:** `docs/superpowers/specs/2026-08-14-proxies-select-error-feedback-design.md`

**工作目录（worktree）:** `C:\Users\Kinema\Documents\modular_dev\mihari\.claude\worktrees\fix-proxies-select-silent-failure`

**注意（memory 教训）:** worktree 内 gopls/codegraph 指向 main 旧版，不可信；验证只认 `go build` / `go test`。push 前必须过 gofmt + golangci-lint（CI 有独立检查）。

---

### Task 1: `selectionResultMsg` 实现 action-outcome 契约

**Files:**
- Modify: `internal/tui/pages/proxies/model.go:61-65`（`selectionResultMsg` 定义处）
- Test: `internal/tui/pages/proxies/model_test.go`

**Step 1: 写失败测试**

在 `internal/tui/pages/proxies/model_test.go` 末尾（`stripProxyANSI` 之前或文件尾均可）追加：

```go
func TestSelectionResultMsg_ImplementsActionOutcomeContract(t *testing.T) {
	boom := errors.New("boom")
	if got := (selectionResultMsg{err: boom}).Err(); got != boom {
		t.Fatalf("Err()=%v want boom", got)
	}
	if got := (selectionResultMsg{}).Err(); got != nil {
		t.Fatalf("zero-value Err()=%v want nil", got)
	}
}
```

**Step 2: 跑测试确认失败**

```bash
cd "C:\Users\Kinema\Documents\modular_dev\mihari\.claude\worktrees\fix-proxies-select-silent-failure" && go test ./internal/tui/pages/proxies/ -run TestSelectionResultMsg_ImplementsActionOutcomeContract -v
```

预期：编译失败，`selectionResultMsg{...}.Err undefined (type proxies.selectionResultMsg has no field or method Err)`。

**Step 3: 最小实现**

`internal/tui/pages/proxies/model.go`，在 `selectionResultMsg` 定义后追加（照抄其他页的注释风格）：

```go
// Err implements the shell's action-outcome contract so proxy selections are
// classified Succeeded/Failed in the Recent operations ledger.
func (m selectionResultMsg) Err() error { return m.err }

var _ interface{ Err() error } = selectionResultMsg{}
```

**Step 4: 跑测试确认通过**

同 Step 2 命令。预期：PASS。

**Step 5: Commit**

```bash
git add internal/tui/pages/proxies/model.go internal/tui/pages/proxies/model_test.go
git commit -m "test(tui): proxies selectionResultMsg 实现 action-outcome 契约 (#80)"
```

---

### Task 2: 失败时显示错误提示（Now 不变、pending 清除）

**Files:**
- Modify: `internal/tui/ui/strings.go:100`（`TimeoutLabel` 行后加常量）
- Modify: `internal/tui/pages/proxies/model.go`（`Model` 字段、`selectionResultMsg` 分支、`buildContent`）
- Test: `internal/tui/pages/proxies/model_test.go`（新测试 + `fakeClient` 加 `selectErr`）

**Step 1: 扩展 fakeClient 并写失败测试**

`internal/tui/pages/proxies/model_test.go` 的 `fakeClient` 结构体加 `selectErr error` 字段，`SelectProxy` 改为：

```go
type fakeClient struct {
	selectedGroup string
	selectedNode  string
	operationID   string
	selectErr     error
	delay         uint16
	delayErr      error
	delayCalls    map[string]int
}

func (c *fakeClient) SelectProxy(_ context.Context, group string, request protocol.ProxySelectionRequest) (protocol.MutationResult, error) {
	c.selectedGroup, c.selectedNode, c.operationID = group, request.Name, request.OperationID
	if c.selectErr != nil {
		return protocol.MutationResult{}, c.selectErr
	}
	return protocol.MutationResult{Schema: "mihari/v1", OperationID: request.OperationID}, nil
}
```

追加测试：

```go
func TestModel_SelectProxyFailureShowsError(t *testing.T) {
	client := &fakeClient{selectErr: errors.New("upstream down")}
	model := New(client, func() string { return "op-1" })
	model.SetSize(80, 24)
	model.SetGroups(protocol.ProxyGroups{Groups: []protocol.ProxyGroup{{
		Name: "GLOBAL", Now: "old", Nodes: []protocol.ProxyNode{{Name: "node-a", Type: "VLESS"}},
	}}})
	updateProxyKey(t, model, tea.KeyPressMsg{Code: tea.KeyEnter}) // 展开组
	updateProxyKey(t, model, tea.KeyPressMsg{Code: tea.KeyDown})  // 焦点到节点
	applyProxyCmd(t, model, updateProxyKey(t, model, tea.KeyPressMsg{Code: tea.KeyEnter}))

	if model.groups[0].Now != "old" {
		t.Fatalf("failed selection must not update Now, got %q", model.groups[0].Now)
	}
	if _, pending := model.pending[FocusID{Group: "GLOBAL", Node: "node-a"}]; pending {
		t.Fatal("pending marker should clear after failure")
	}
	view := model.View()
	if !strings.Contains(view, model.theme.Danger.Render(ui.ProxySelectFailed)) {
		t.Fatalf("view missing failure feedback %q:\n%s", ui.ProxySelectFailed, view)
	}
}
```

**Step 2: 跑测试确认失败**

```bash
go test ./internal/tui/pages/proxies/ -run TestModel_SelectProxyFailureShowsError -v
```

预期：编译失败，`undefined: ui.ProxySelectFailed`。

**Step 3: 最小实现（三处）**

1. `internal/tui/ui/strings.go`，`TimeoutLabel = "Timeout"` 行后加（gofmt 会重排对齐）：

```go
	ProxySelectFailed        = "Proxy selection failed"
```

2. `internal/tui/pages/proxies/model.go` `Model` 结构体 `pending` 字段后加：

```go
	lastError      string
```

3. `Update` 的 `selectionResultMsg` 分支改为：

```go
	case selectionResultMsg:
		delete(m.pending, FocusID{Group: typed.group, Node: typed.node})
		if typed.err != nil {
			m.lastError = ui.ProxySelectFailed
			return m, nil
		}
		if index := m.groupIndex(typed.group); index >= 0 {
			m.groups[index].Now = typed.node
		}
		return m, nil
```

4. `buildContent` 开头（`focusStart, focusEnd = -1, -1` 之后）插入：

```go
	if m.lastError != "" {
		lines = append(lines, m.theme.Danger.Render(m.lastError))
	}
```

**Step 4: 跑测试确认通过**

同 Step 2 命令，另跑整包：`go test ./internal/tui/pages/proxies/ -v`。预期：全部 PASS。

**Step 5: Commit**

```bash
git add internal/tui/ui/strings.go internal/tui/pages/proxies/model.go internal/tui/pages/proxies/model_test.go
git commit -m "fix(tui): proxies 页切换节点失败时显示红色错误提示 (#80)"
```

---

### Task 3: 成功结果与新选择发起时清除错误

**Files:**
- Modify: `internal/tui/pages/proxies/model.go`（成功分支清 `lastError`；`selectFocused` 开头清）
- Test: `internal/tui/pages/proxies/model_test.go`

**Step 1: 写失败测试**

```go
func TestModel_SelectProxySuccessClearsError(t *testing.T) {
	client := &fakeClient{selectErr: errors.New("upstream down")}
	model := New(client, func() string { return "op-1" })
	model.SetSize(80, 24)
	model.SetGroups(protocol.ProxyGroups{Groups: []protocol.ProxyGroup{{
		Name: "GLOBAL", Now: "old", Nodes: []protocol.ProxyNode{{Name: "node-a", Type: "VLESS"}},
	}}})
	updateProxyKey(t, model, tea.KeyPressMsg{Code: tea.KeyEnter})
	updateProxyKey(t, model, tea.KeyPressMsg{Code: tea.KeyDown})
	applyProxyCmd(t, model, updateProxyKey(t, model, tea.KeyPressMsg{Code: tea.KeyEnter}))
	if !strings.Contains(model.View(), ui.ProxySelectFailed) {
		t.Fatal("failure feedback missing")
	}

	client.selectErr = nil
	applyProxyCmd(t, model, updateProxyKey(t, model, tea.KeyPressMsg{Code: tea.KeyEnter}))
	if strings.Contains(model.View(), ui.ProxySelectFailed) {
		t.Fatal("stale failure feedback after success")
	}
	if model.groups[0].Now != "node-a" {
		t.Fatalf("Now=%q want node-a", model.groups[0].Now)
	}
}

func TestModel_NewSelectionClearsStaleError(t *testing.T) {
	client := &fakeClient{selectErr: errors.New("upstream down")}
	model := New(client, func() string { return "op-1" })
	model.SetSize(80, 24)
	model.SetGroups(protocol.ProxyGroups{Groups: []protocol.ProxyGroup{{
		Name: "GLOBAL", Now: "old", Nodes: []protocol.ProxyNode{{Name: "node-a", Type: "VLESS"}},
	}}})
	updateProxyKey(t, model, tea.KeyPressMsg{Code: tea.KeyEnter})
	updateProxyKey(t, model, tea.KeyPressMsg{Code: tea.KeyDown})
	applyProxyCmd(t, model, updateProxyKey(t, model, tea.KeyPressMsg{Code: tea.KeyEnter}))
	if !strings.Contains(model.View(), ui.ProxySelectFailed) {
		t.Fatal("failure feedback missing")
	}

	command := updateProxyKey(t, model, tea.KeyPressMsg{Code: tea.KeyEnter})
	if command == nil {
		t.Fatal("expected selection command")
	}
	if strings.Contains(model.View(), ui.ProxySelectFailed) {
		t.Fatal("stale error must clear when a new selection starts")
	}
}
```

**Step 2: 跑测试确认失败**

```bash
go test ./internal/tui/pages/proxies/ -run 'TestModel_SelectProxySuccessClearsError|TestModel_NewSelectionClearsStaleError' -v
```

预期：两个测试均 FAIL——Task 2 的实现成功路径未清 `lastError`，`selectFocused` 也不清。

**Step 3: 最小实现**

1. `selectionResultMsg` 成功路径（`if typed.err != nil` 块之后、`groupIndex` 之前）加：

```go
		m.lastError = ""
```

2. `selectFocused()` 在 `m.pending[id] = true` 之前加：

```go
	m.lastError = ""
```

**Step 4: 跑测试确认通过**

同 Step 2 命令 + 整包回归 `go test ./internal/tui/pages/proxies/`。预期：全部 PASS。

**Step 5: Commit**

```bash
git add internal/tui/pages/proxies/model.go internal/tui/pages/proxies/model_test.go
git commit -m "fix(tui): proxies 页成功切换或发起新选择时清除错误提示 (#80)"
```

---

### Task 4: 全量预检（不产生 commit，除非有遗漏修正）

**Step 1: 格式化检查**（CI 有 `test -z "$(gofmt -l .)"` 硬检查）

```bash
gofmt -l .
```

预期：无输出。若有输出 → `gofmt -w <files>` 后 amend/追加 commit。

**Step 2: lint**（CI 独立 job，staticcheck；本地 v2.12.2 同 CI 版本）

```bash
golangci-lint run
```

预期：`0 issues.`

**Step 3: 全量测试**

```bash
go test ./...
```

预期：全部 ok。随后 push、开 PR（关联 #80，`Fixes #80`）、盯 CI 全绿；merge 由用户执行（仓库 squash-only）。
