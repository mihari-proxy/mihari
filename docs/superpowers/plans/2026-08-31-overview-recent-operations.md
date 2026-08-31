# Overview Recent operations SysProxy / TUN 账本 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Overview Recent operations 对 System proxy / TUN 写出动作、结果摘要、成败，并在行尾右对齐显示操作完成时间。

**Architecture:** 纯 TUI。`OperationRecord` 增加 `Action`/`Detail`；壳层 `recordActionOutcome` 经窄接口读取 page 结果 DTO，由纯函数填表；Overview 按优先级拼左侧摘要并把 `At` 以 `15:04:05` 右对齐。不改 daemon、`/v1`、CLI。

**Tech Stack:** Go 1.26、bubbletea v2、lipgloss v2、`go test`

**Spec:** `docs/superpowers/specs/2026-08-31-overview-recent-operations-design.md`

## Global Constraints

- 工作目录：`.worktrees/feat-159-overview-ops`（分支 `feat/159-overview-recent-operations`，基线 `origin/dev`）
- 禁止改 `CHANGELOG.md`、`internal/control/protocol` 字段、`internal/runtime`、CLI、Web
- 禁止在 `main` / `dev` 上 commit；本分支也等用户明确要求再 commit（计划里的 commit 命令是预备稿，带 `-s` DCO）
- 每个行为先写失败测试，确认失败原因是缺行为而不是编译错误，再写最小实现
- 所有修改过的 Go 文件 `gofmt`；测试不访问公网、不读真实用户目录
- 新文案走 `internal/tui/ui/strings.go` 常量，测试断言常量不是裸字符串（格式化串的动态部分除外）
- 错误展示不得使用非 `APIError` 的 `err.Error()` 原文

## File map

- Create: `internal/tui/operation_ledger.go` — `newOperationRecord`、动作/失败映射、窄接口
- Create: `internal/tui/operation_ledger_test.go` — 填表表驱动测试
- Modify: `internal/tui/ui/monitor.go` — `OperationRecord` 加 `Action`、`Detail`
- Modify: `internal/tui/ui/strings.go` — 账本短句常量
- Modify: `internal/tui/model.go` — `recordActionOutcome` 调用填表函数
- Modify: `internal/tui/model_test.go` — 通用记账回归 + SysProxy/TUN 壳层记账
- Modify: `internal/tui/pages/system/model.go` — `ProxyStatus()` / `TunStatus()`
- Modify: `internal/tui/pages/overview/model.go` — `formatOperationLine`、View 使用它
- Modify: `internal/tui/pages/overview/model_test.go` — 渲染、右对齐、窄宽

---

### Task 1: 填表纯函数 — SysProxy 成功路径

**Files:**
- Create: `internal/tui/operation_ledger.go`
- Create: `internal/tui/operation_ledger_test.go`
- Modify: `internal/tui/ui/monitor.go`
- Modify: `internal/tui/ui/strings.go`

**Interfaces:**
- Consumes: `ui.ActionIntentMsg`、`protocol.SystemProxyStatus`、现有 `ui.ActionEnableSystemProxy` 等常量
- Produces: `newOperationRecord(intent ui.ActionIntentMsg, result tea.Msg, at time.Time) ui.OperationRecord`；`OperationRecord.Action` / `Detail`；常量 `LedgerCleared`、`LedgerOverwroteForeignFmt`

- [ ] **Step 1: 给 `OperationRecord` 加字段，并加文案常量**

`internal/tui/ui/monitor.go` 的 `OperationRecord` 改为：

```go
type OperationRecord struct {
	ID     string
	Object string
	Action string
	Detail string
	State  string
	At     time.Time
}
```

`internal/tui/ui/strings.go` 在 `ForceEnableSystemProxyLabel` 旁增加：

```go
	LedgerCleared             = "cleared"
	LedgerOverwroteForeignFmt = "overwrote foreign → %s"
	LedgerForeignProxyInUse   = "foreign proxy in use"
	LedgerOtherTunInUseFmt    = "other TUN in use (%s)"
	LedgerOtherTunInUse       = "other TUN in use"
```

这一步是加法，现有复合字面量仍然编译。

- [ ] **Step 2: 写失败测试（Enable / Force / Disable 成功）**

`internal/tui/operation_ledger_test.go`：

```go
package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

type fakeProxyOutcome struct {
	err    error
	status protocol.SystemProxyStatus
}

func (f fakeProxyOutcome) Err() error { return f.err }
func (f fakeProxyOutcome) ProxyStatus() protocol.SystemProxyStatus {
	return f.status
}

func TestNewOperationRecord_SystemProxySuccess(t *testing.T) {
	at := time.Date(2026, 8, 31, 14, 32, 1, 0, time.Local)
	status := protocol.SystemProxyStatus{
		Target: "127.0.0.1:7890",
		Observed: protocol.SystemProxyObserved{
			Enabled: true, Server: "127.0.0.1:7890", Owned: true,
		},
	}
	tests := []struct {
		name   string
		action ui.Action
		wantA  string
		wantD  string
	}{
		{
			name:   "enable",
			action: ui.ActionEnableSystemProxy,
			wantA:  ui.EnableSystemProxyLabel,
			wantD:  "127.0.0.1:7890 · " + ui.PortOwned,
		},
		{
			name:   "force",
			action: ui.ActionForceSystemProxy,
			wantA:  ui.ForceEnableSystemProxyLabel,
			wantD:  fmt.Sprintf(ui.LedgerOverwroteForeignFmt, "127.0.0.1:7890"),
		},
		{
			name:   "disable",
			action: ui.ActionDisableSystemProxy,
			wantA:  ui.DisableSystemProxyLabel,
			wantD:  ui.LedgerCleared,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := newOperationRecord(ui.ActionIntentMsg{
				Action: test.action, Object: ui.SystemProxyLabel, Key: "system:" + string(test.action),
			}, fakeProxyOutcome{status: status}, at)
			if got.Object != ui.SystemProxyLabel || got.Action != test.wantA || got.Detail != test.wantD || got.State != ui.SucceededLabel {
				t.Fatalf("record=%+v want action=%q detail=%q", got, test.wantA, test.wantD)
			}
			if !got.At.Equal(at) {
				t.Fatalf("At=%v want %v", got.At, at)
			}
		})
	}
}
```

- [ ] **Step 3: 跑测试确认失败**

```powershell
cd .worktrees/feat-159-overview-ops
go test ./internal/tui -run TestNewOperationRecord_SystemProxySuccess -count=1
```

Expected: 编译失败，`newOperationRecord` 未定义；若已加空函数则 FAIL，`Action`/`Detail` 为空。

- [ ] **Step 4: 最小实现**

`internal/tui/operation_ledger.go`：

```go
package tui

import (
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

type proxyOutcome interface {
	Err() error
	ProxyStatus() protocol.SystemProxyStatus
}

type tunOutcome interface {
	Err() error
	TunStatus() protocol.TunStatus
}

func newOperationRecord(intent ui.ActionIntentMsg, result tea.Msg, at time.Time) ui.OperationRecord {
	state := ui.SucceededLabel
	if res, ok := result.(resultErr); ok && res.Err() != nil {
		state = ui.FailedLabel
	}
	object := intent.Object
	if object == "" {
		object = intent.Title
	}
	return ui.OperationRecord{
		ID: intent.Key, Object: object, Action: ledgerAction(intent.Action),
		Detail: ledgerDetail(intent.Action, result, state == ui.FailedLabel),
		State: state, At: at,
	}
}

func ledgerAction(action ui.Action) string {
	switch action {
	case ui.ActionEnableSystemProxy:
		return ui.EnableSystemProxyLabel
	case ui.ActionEnableTun:
		return ui.EnableTunLabel
	case ui.ActionDisableSystemProxy:
		return ui.DisableSystemProxyLabel
	case ui.ActionDisableTun:
		return ui.DisableTunLabel
	case ui.ActionForceSystemProxy:
		return ui.ForceEnableSystemProxyLabel
	case ui.ActionForceTun:
		return ui.ForceEnableTunLabel
	default:
		return ""
	}
}

func ledgerDetail(action ui.Action, result tea.Msg, failed bool) string {
	switch action {
	case ui.ActionEnableSystemProxy, ui.ActionForceSystemProxy, ui.ActionDisableSystemProxy:
		status := protocol.SystemProxyStatus{}
		if outcome, ok := result.(proxyOutcome); ok {
			status = outcome.ProxyStatus()
		}
		if failed {
			return proxyFailureDetail(result, status)
		}
		return proxySuccessDetail(action, status)
	case ui.ActionEnableTun, ui.ActionForceTun, ui.ActionDisableTun:
		status := protocol.TunStatus{}
		if outcome, ok := result.(tunOutcome); ok {
			status = outcome.TunStatus()
		}
		if failed {
			return tunFailureDetail(result, status)
		}
		return tunSuccessDetail(action, status)
	default:
		return ""
	}
}

func proxySuccessDetail(action ui.Action, status protocol.SystemProxyStatus) string {
	server := strings.TrimSpace(status.Observed.Server)
	if server == "" {
		server = strings.TrimSpace(status.Target)
	}
	switch action {
	case ui.ActionDisableSystemProxy:
		return ui.LedgerCleared
	case ui.ActionForceSystemProxy:
		if server == "" {
			return ui.LedgerOverwroteForeign
		}
		return fmt.Sprintf(ui.LedgerOverwroteForeignFmt, server)
	default:
		if server == "" {
			return ui.PortOwned
		}
		return server + " · " + ui.PortOwned
	}
}

func proxyFailureDetail(result tea.Msg, status protocol.SystemProxyStatus) string {
	return "" // Task 2
}

func tunSuccessDetail(action ui.Action, status protocol.TunStatus) string {
	return "" // Task 3
}

func tunFailureDetail(result tea.Msg, status protocol.TunStatus) string {
	return "" // Task 3
}
```

Task 1 只要求 SysProxy 成功测试通过；失败/TUN 函数可先返回空字符串。

注意：`internal/tui/model.go` 已有 `resultErr`。实现本文件后若与 `model.go` 重复定义会编译失败。**不要在 `operation_ledger.go` 再声明 `resultErr`**，复用 `model.go` 里已有的那个。

- [ ] **Step 5: 再跑测试确认通过**

同 Step 3。Expected: PASS。

- [ ] **Step 6: Commit（仅当用户要求）**

```powershell
git add internal/tui/ui/monitor.go internal/tui/ui/strings.go internal/tui/operation_ledger.go internal/tui/operation_ledger_test.go
git commit -s -m "feat(tui): 为 SysProxy 账本填动作与成功摘要"
```

---

### Task 2: 填表纯函数 — SysProxy 失败映射

**Files:**
- Modify: `internal/tui/operation_ledger.go`
- Modify: `internal/tui/operation_ledger_test.go`

**Interfaces:**
- Consumes: `protocol.APIError`、`CodeSystemProxyConflict` / `CodeSystemProxyNotOwned` / `CodePermissionDenied` / `CodeRevisionConflict`
- Produces: `proxyFailureDetail` 返回 spec §6.2 短句

- [ ] **Step 1: 写失败测试**

追加到 `operation_ledger_test.go`：

```go
func TestNewOperationRecord_SystemProxyFailure(t *testing.T) {
	at := time.Unix(1, 0)
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "conflict",
			err: protocol.APIError{
				Code: protocol.CodeSystemProxyConflict, Message: "system proxy is managed by another application",
				Details: map[string]any{"current_server": "10.0.0.1:8080"},
			},
			want: ui.LedgerForeignProxyInUse,
		},
		{
			name: "not owned",
			err:  protocol.APIError{Code: protocol.CodeSystemProxyNotOwned, Message: ui.SystemProxyNotOwnedMessage},
			want: ui.LedgerForeignProxyInUse,
		},
		{
			name: "permission",
			err:  protocol.APIError{Code: protocol.CodePermissionDenied, Message: "administrator privileges are required"},
			want: ui.ServiceNotElevatedLabel,
		},
		{
			name: "revision",
			err:  protocol.APIError{Code: protocol.CodeRevisionConflict, Message: "state revision changed"},
			want: ui.SystemChangedMessage,
		},
		{
			name: "api message",
			err:  protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "enable system proxy"},
			want: "enable system proxy",
		},
		{
			name: "raw error",
			err:  errors.New("The operation completed successfully. (registry)"),
			want: ui.SystemProxyActionFailed,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := newOperationRecord(ui.ActionIntentMsg{
				Action: ui.ActionEnableSystemProxy, Object: ui.SystemProxyLabel,
			}, fakeProxyOutcome{err: test.err}, at)
			if got.State != ui.FailedLabel || got.Action != ui.EnableSystemProxyLabel || got.Detail != test.want {
				t.Fatalf("record=%+v want detail=%q", got, test.want)
			}
			if strings.Contains(strings.ToLower(got.Detail), "registry") {
				t.Fatalf("leaked raw error: %q", got.Detail)
			}
			if strings.Contains(got.Detail, "10.0.0.1:8080") {
				t.Fatalf("leaked current_server: %q", got.Detail)
			}
		})
	}
}

func TestNewOperationRecord_SystemProxyFailureUsesLastError(t *testing.T) {
	got := newOperationRecord(ui.ActionIntentMsg{
		Action: ui.ActionEnableSystemProxy, Object: ui.SystemProxyLabel,
	}, fakeProxyOutcome{
		err:    errors.New("ignored raw"),
		status: protocol.SystemProxyStatus{LastError: "enable system proxy"},
	}, time.Unix(1, 0))
	if got.Detail != "enable system proxy" {
		t.Fatalf("detail=%q want last_error", got.Detail)
	}
}
```

失败查找顺序（spec）：APIError Code 映射 → status.LastError → 域 fallback。`LastError` 测试的 err 不是 APIError，所以应走 LastError。

- [ ] **Step 2: 跑测试确认失败**

```powershell
go test ./internal/tui -run 'TestNewOperationRecord_SystemProxyFailure' -count=1
```

Expected: FAIL，`Detail` 为空或不是短句。

- [ ] **Step 3: 最小实现 `proxyFailureDetail`**

```go
func proxyFailureDetail(result tea.Msg, status protocol.SystemProxyStatus) string {
	if msg := mappedAPIErrorDetail(result); msg != "" {
		return msg
	}
	if text := strings.TrimSpace(status.LastError); text != "" {
		return text
	}
	return ui.SystemProxyActionFailed
}

func mappedAPIErrorDetail(result tea.Msg) string {
	res, ok := result.(resultErr)
	if !ok || res.Err() == nil {
		return ""
	}
	var apiError protocol.APIError
	if !errors.As(res.Err(), &apiError) {
		return ""
	}
	switch apiError.Code {
	case protocol.CodeSystemProxyConflict, protocol.CodeSystemProxyNotOwned:
		return ui.LedgerForeignProxyInUse
	case protocol.CodeTunConflict:
		return tunConflictDetail(apiError.Details)
	case protocol.CodePermissionDenied:
		return ui.ServiceNotElevatedLabel
	case protocol.CodeRevisionConflict:
		return ui.SystemChangedMessage
	}
	if msg := strings.TrimSpace(apiError.Message); msg != "" {
		return msg
	}
	return ""
}

func tunConflictDetail(details map[string]any) string {
	return ui.LedgerOtherTunInUse // Task 3 填接口短名
}
```

- [ ] **Step 4: 跑测试确认通过**

同 Step 2。Expected: PASS。

- [ ] **Step 5: Commit（仅当用户要求）**

```powershell
git add internal/tui/operation_ledger.go internal/tui/operation_ledger_test.go
git commit -s -m "feat(tui): 映射 SysProxy 账本失败原因"
```

---

### Task 3: 填表纯函数 — TUN 成功与失败

**Files:**
- Modify: `internal/tui/operation_ledger.go`
- Modify: `internal/tui/operation_ledger_test.go`

**Interfaces:**
- Consumes: `protocol.TunStatus`、`CodeTunConflict` Details `other_tun_interfaces`（`[]string` 与 `[]any`）
- Produces: `tunSuccessDetail` / `tunFailureDetail` / `tunConflictDetail`

- [ ] **Step 1: 写失败测试**

```go
type fakeTunOutcome struct {
	err    error
	status protocol.TunStatus
}

func (f fakeTunOutcome) Err() error                 { return f.err }
func (f fakeTunOutcome) TunStatus() protocol.TunStatus { return f.status }

func TestNewOperationRecord_TunSuccess(t *testing.T) {
	liveOn := true
	liveOff := false
	at := time.Unix(2, 0)
	on := protocol.TunStatus{Stack: "gVisor", LiveEnable: &liveOn}
	off := protocol.TunStatus{Stack: "gVisor", LiveEnable: &liveOff}
	live := ui.LiveLabel + " " + ui.OnLabel
	dead := ui.LiveLabel + " " + ui.OffLabel
	tests := []struct {
		name   string
		action ui.Action
		status protocol.TunStatus
		wantA  string
		wantD  string
	}{
		{"enable", ui.ActionEnableTun, on, ui.EnableTunLabel, "gVisor · " + live},
		{"force", ui.ActionForceTun, on, ui.ForceEnableSystemProxyLabel, "gVisor · " + live},
		{"disable", ui.ActionDisableTun, off, ui.DisableTunLabel, dead},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := newOperationRecord(ui.ActionIntentMsg{
				Action: test.action, Object: ui.TUNLabel,
			}, fakeTunOutcome{status: test.status}, at)
			if got.Action != test.wantA || got.Detail != test.wantD || got.State != ui.SucceededLabel {
				t.Fatalf("record=%+v want action=%q detail=%q", got, test.wantA, test.wantD)
			}
		})
	}
}

func TestNewOperationRecord_TunConflictUsesInterfaceName(t *testing.T) {
	got := newOperationRecord(ui.ActionIntentMsg{
		Action: ui.ActionEnableTun, Object: ui.TUNLabel,
	}, fakeTunOutcome{err: protocol.APIError{
		Code: protocol.CodeTunConflict, Message: "other TUN adapters detected",
		Details: map[string]any{"other_tun_interfaces": []any{"Meta Tunnel", "wintun"}},
	}}, time.Unix(3, 0))
	want := fmt.Sprintf(ui.LedgerOtherTunInUseFmt, "Meta Tunnel")
	if got.State != ui.FailedLabel || got.Detail != want {
		t.Fatalf("record=%+v want %q", got, want)
	}
}

func TestNewOperationRecord_TunConflictWithoutNames(t *testing.T) {
	got := newOperationRecord(ui.ActionIntentMsg{
		Action: ui.ActionEnableTun, Object: ui.TUNLabel,
	}, fakeTunOutcome{err: protocol.APIError{Code: protocol.CodeTunConflict, Message: "other TUN adapters detected"}}, time.Unix(3, 0))
	if got.Detail != ui.LedgerOtherTunInUse {
		t.Fatalf("detail=%q", got.Detail)
	}
}

func TestNewOperationRecord_IgnoresUnrelatedActions(t *testing.T) {
	got := newOperationRecord(ui.ActionIntentMsg{
		Action: ui.ActionRestartCore, Object: "mihomo", Key: "restart-core",
	}, outcomeResultMsg{}, time.Unix(4, 0))
	if got.Action != "" || got.Detail != "" || got.Object != "mihomo" || got.State != ui.SucceededLabel {
		t.Fatalf("record=%+v", got)
	}
}
```

`outcomeResultMsg` 已在 `model_test.go` 同包定义，可直接用。

- [ ] **Step 2: 跑测试确认失败**

```powershell
go test ./internal/tui -run 'TestNewOperationRecord_Tun' -count=1
```

Expected: FAIL，TUN Detail 为空。

- [ ] **Step 3: 最小实现**

```go
func tunSuccessDetail(action ui.Action, status protocol.TunStatus) string {
	live := ui.LiveLabel + " " + ui.OffLabel
	if status.LiveEnable != nil && *status.LiveEnable {
		live = ui.LiveLabel + " " + ui.OnLabel
	}
	if action == ui.ActionDisableTun {
		return live
	}
	stack := strings.TrimSpace(status.Stack)
	if stack == "" {
		return live
	}
	return stack + " · " + live
}

func tunFailureDetail(result tea.Msg, status protocol.TunStatus) string {
	if msg := mappedAPIErrorDetail(result); msg != "" {
		return msg
	}
	if text := strings.TrimSpace(status.LastError); text != "" {
		return text
	}
	return ui.TunActionFailed
}

func tunConflictDetail(details map[string]any) string {
	names := ui.DetailStrings(details, "other_tun_interfaces")
	if len(names) == 0 || strings.TrimSpace(names[0]) == "" {
		return ui.LedgerOtherTunInUse
	}
	return fmt.Sprintf(ui.LedgerOtherTunInUseFmt, strings.TrimSpace(names[0]))
}
```

用 `ui.DetailStrings` 解析 `other_tun_interfaces`（兼容 `[]string` / `[]any`）。System 页通过同名薄封装调用，不要再复制一份。

- [ ] **Step 4: 跑测试确认通过**

```powershell
go test ./internal/tui -run 'TestNewOperationRecord_' -count=1
```

Expected: PASS。

- [ ] **Step 5: Commit（仅当用户要求）**

```powershell
git add internal/tui/operation_ledger.go internal/tui/operation_ledger_test.go
git commit -s -m "feat(tui): 为 TUN 账本填动作、Live 与冲突摘要"
```

---

### Task 4: 接入 `recordActionOutcome` 与 System 结果访问器

**Files:**
- Modify: `internal/tui/model.go`（`recordActionOutcome`）
- Modify: `internal/tui/model_test.go`
- Modify: `internal/tui/pages/system/model.go`

**Interfaces:**
- Consumes: `newOperationRecord`
- Produces: 真实 `systemProxyActionResultMsg.ProxyStatus()` / `tunActionResultMsg.TunStatus()`，使运行时结果满足壳层窄接口

- [ ] **Step 1: 写失败测试**

在 `model_test.go` 的 `TestActionCompletedRecordsOperations` 中，成功通用路径增加：

```go
	if record.Action != "" || record.Detail != "" {
		t.Fatalf("unrelated action must not set Action/Detail: %+v", record)
	}
```

（`assertRecord` 内，在现有字段断言之后。）

再追加：

```go
func TestActionCompletedRecordsSystemProxyLedger(t *testing.T) {
	model := NewModel()
	updated, _ := model.Update(actionCompletedMsg{
		Intent: ui.ActionIntentMsg{
			Action: ui.ActionEnableSystemProxy, Key: "system:enable-system-proxy",
			Object: ui.SystemProxyLabel,
		},
		Result: fakeProxyOutcome{status: protocol.SystemProxyStatus{
			Observed: protocol.SystemProxyObserved{Server: "127.0.0.1:7890", Owned: true},
		}},
	})
	model = updated.(Model)
	if len(model.operations) != 1 {
		t.Fatalf("operations=%v", model.operations)
	}
	record := model.operations[0]
	if record.Action != ui.EnableSystemProxyLabel || !strings.Contains(record.Detail, "127.0.0.1:7890") || record.State != ui.SucceededLabel {
		t.Fatalf("record=%+v", record)
	}
	if record.At.IsZero() {
		t.Fatal("At zero")
	}
}
```

`fakeProxyOutcome` 与 `operation_ledger_test.go` 同包，可共用；若不想跨文件依赖，把 fake 挪到 `operation_ledger_test.go` 即可（同包测试文件共享）。

- [ ] **Step 2: 跑测试确认失败**

```powershell
go test ./internal/tui -run 'TestActionCompletedRecords' -count=1
```

Expected: FAIL，壳层仍写出空 `Action`/`Detail`。

- [ ] **Step 3: 最小实现**

`recordActionOutcome` 改为：

```go
func (model *Model) recordActionOutcome(intent ui.ActionIntentMsg, result tea.Msg) {
	model.recordOperation(newOperationRecord(intent, result, time.Now()))
}
```

`internal/tui/pages/system/model.go` 在 `Err()` 旁增加：

```go
func (m systemProxyActionResultMsg) ProxyStatus() protocol.SystemProxyStatus { return m.status }

func (m tunActionResultMsg) TunStatus() protocol.TunStatus { return m.status }
```

- [ ] **Step 4: 跑测试确认通过**

```powershell
go test ./internal/tui ./internal/tui/pages/system -count=1
```

Expected: PASS。`TestActionCompletedRecordsOperations` 的 Title 回退、Failed、nil result 仍成立。

- [ ] **Step 5: Commit（仅当用户要求）**

```powershell
git add internal/tui/model.go internal/tui/model_test.go internal/tui/pages/system/model.go
git commit -s -m "feat(tui): 壳层把 SysProxy/TUN 结果写入账本"
```

---

### Task 5: Overview 拼行 + 右对齐时间

**Files:**
- Modify: `internal/tui/pages/overview/model.go`
- Modify: `internal/tui/pages/overview/model_test.go`

**Interfaces:**
- Consumes: `ui.OperationRecord` 的 `Action`/`Detail`/`State`/`At`
- Produces: `formatOperationLine(theme ui.Theme, op ui.OperationRecord, width int) string`

- [ ] **Step 1: 写失败测试**

```go
func TestFormatOperationLine_SystemProxyWideKeepsDetailAndTime(t *testing.T) {
	at := time.Date(2026, 8, 31, 14, 32, 1, 0, time.Local)
	line := formatOperationLine(ui.DefaultTheme(), ui.OperationRecord{
		Object: ui.SystemProxyLabel,
		Action: ui.EnableSystemProxyLabel,
		Detail: "127.0.0.1:7890 · " + ui.PortOwned,
		State:  ui.SucceededLabel,
		At:     at,
	}, 80)
	plain := stripANSI(line)
	if !strings.Contains(plain, ui.EnableSystemProxyLabel) || !strings.Contains(plain, "127.0.0.1:7890") || !strings.Contains(plain, ui.SucceededLabel) {
		t.Fatalf("line=%q", plain)
	}
	clock := at.Local().Format("15:04:05")
	if !strings.HasSuffix(strings.TrimRight(plain, " "), clock) {
		t.Fatalf("time not right-aligned: %q", plain)
	}
	if lipgloss.Width(line) != 80 {
		t.Fatalf("width=%d want 80 line=%q", lipgloss.Width(line), plain)
	}
}

func TestFormatOperationLine_UnrelatedKeepsObjectStateAndTime(t *testing.T) {
	at := time.Date(2026, 8, 31, 14, 12, 44, 0, time.Local)
	line := formatOperationLine(ui.DefaultTheme(), ui.OperationRecord{
		Object: "mihomo", State: ui.SucceededLabel, At: at,
	}, 60)
	plain := stripANSI(line)
	if !strings.Contains(plain, "mihomo") || !strings.Contains(plain, ui.SucceededLabel) {
		t.Fatalf("line=%q", plain)
	}
	if strings.Contains(plain, " ·  · ") {
		t.Fatalf("empty action inserted: %q", plain)
	}
	if !strings.HasSuffix(strings.TrimRight(plain, " "), at.Local().Format("15:04:05")) {
		t.Fatalf("time=%q", plain)
	}
}

func TestFormatOperationLine_ZeroAtOmitsClock(t *testing.T) {
	line := formatOperationLine(ui.DefaultTheme(), ui.OperationRecord{
		Object: "mihomo", State: ui.SucceededLabel,
	}, 40)
	plain := stripANSI(line)
	if strings.Contains(plain, ":") && looksLikeClock(plain) {
		t.Fatalf("unexpected clock: %q", plain)
	}
}
```

若 overview 测试包还没有 `stripANSI`，用：

```go
func stripANSI(value string) string {
	var b strings.Builder
	esc := false
	for _, r := range value {
		if r == 0x1b {
			esc = true
			continue
		}
		if esc {
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				esc = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
```

`looksLikeClock` 可用 `regexp.MustCompile(`\d{2}:\d{2}:\d{2}`)`。检查 overview 测试是否已有类似辅助函数，有则复用。

需要 `import lipgloss "charm.land/lipgloss/v2"`。

- [ ] **Step 2: 跑测试确认失败**

```powershell
go test ./internal/tui/pages/overview -run TestFormatOperationLine_ -count=1
```

Expected: 编译失败，`formatOperationLine` 未定义。

- [ ] **Step 3: 最小实现**

在 `overview/model.go` 增加（View 仍可暂不调用，本 Task 只让纯函数测试过）：

```go
func formatOperationLine(theme ui.Theme, operation ui.OperationRecord, width int) string {
	state := ui.ToneStyle(theme, ui.ClassifyStatusTone(operation.State)).Render(operation.State)
	object := valueOr(operation.Object, operation.ID)
	left := object + " · " + state
	if operation.Action != "" {
		if operation.Detail != "" && operation.State == ui.FailedLabel {
			left = object + " · " + operation.Action + " · " + state + " · " + operation.Detail
		} else if operation.Detail != "" {
			left = object + " · " + operation.Action + " · " + operation.Detail + " · " + state
		} else {
			left = object + " · " + operation.Action + " · " + state
		}
	}
	return padRightTime(left, operation.At, width)
}

func padRightTime(left string, at time.Time, width int) string {
	if width <= 0 {
		return left
	}
	if at.IsZero() {
		return left
	}
	clock := at.Local().Format("15:04:05")
	gap := width - lipgloss.Width(left) - lipgloss.Width(clock)
	if gap < 1 {
		return left
	}
	return left + strings.Repeat(" ", gap) + clock
}
```

本 Task 的宽行测试还不要求丢 Detail；`gap < 1` 时先整列丢掉时间即可。截断规则在 Task 6。

需要 `import "time"`。

- [ ] **Step 4: 跑测试确认通过**

同 Step 2。Expected: PASS。

- [ ] **Step 5: Commit（仅当用户要求）**

```powershell
git add internal/tui/pages/overview/model.go internal/tui/pages/overview/model_test.go
git commit -s -m "feat(tui): Overview 账本行右对齐操作时间"
```

---

### Task 6: 窄卡优先级截断

**Files:**
- Modify: `internal/tui/pages/overview/model.go`（`formatOperationLine`）
- Modify: `internal/tui/pages/overview/model_test.go`

**Interfaces:**
- Consumes: Task 5 的 `formatOperationLine`
- Produces: spec §7.2 优先级：先丢 Detail，再丢时间，永不先丢 Action / State

- [ ] **Step 1: 写失败测试**

```go
func TestFormatOperationLine_NarrowDropsDetailKeepsActionStateTime(t *testing.T) {
	at := time.Date(2026, 8, 31, 14, 32, 1, 0, time.Local)
	line := formatOperationLine(ui.DefaultTheme(), ui.OperationRecord{
		Object: ui.SystemProxyLabel,
		Action: ui.EnableSystemProxyLabel,
		Detail: "127.0.0.1:7890 · " + ui.PortOwned,
		State:  ui.SucceededLabel,
		At:     at,
	}, 42)
	plain := stripANSI(line)
	if !strings.Contains(plain, ui.EnableSystemProxyLabel) || !strings.Contains(plain, ui.SucceededLabel) {
		t.Fatalf("missing action/state: %q", plain)
	}
	if strings.Contains(plain, "127.0.0.1:7890") {
		t.Fatalf("detail should drop first: %q", plain)
	}
	if !strings.Contains(plain, at.Local().Format("15:04:05")) {
		t.Fatalf("time should remain after dropping detail: %q", plain)
	}
}

func TestFormatOperationLine_VeryNarrowDropsTimeKeepsActionState(t *testing.T) {
	at := time.Date(2026, 8, 31, 14, 32, 1, 0, time.Local)
	line := formatOperationLine(ui.DefaultTheme(), ui.OperationRecord{
		Object: ui.SystemProxyLabel,
		Action: ui.ForceEnableSystemProxyLabel,
		Detail: "overwrote foreign → 127.0.0.1:7890",
		State:  ui.SucceededLabel,
		At:     at,
	}, 28)
	plain := stripANSI(line)
	if !strings.Contains(plain, ui.ForceEnableSystemProxyLabel) || !strings.Contains(plain, ui.SucceededLabel) {
		t.Fatalf("missing action/state: %q", plain)
	}
	if strings.Contains(plain, at.Local().Format("15:04:05")) {
		t.Fatalf("time should drop before action: %q", plain)
	}
}

func TestFormatOperationLine_FailureDetailAfterState(t *testing.T) {
	at := time.Date(2026, 8, 31, 14, 40, 3, 0, time.Local)
	line := formatOperationLine(ui.DefaultTheme(), ui.OperationRecord{
		Object: ui.TUNLabel,
		Action: ui.EnableTunLabel,
		Detail: fmt.Sprintf(ui.LedgerOtherTunInUseFmt, "Meta Tunnel"),
		State:  ui.FailedLabel,
		At:     at,
	}, 80)
	plain := stripANSI(line)
	enableAt := strings.Index(plain, ui.EnableTunLabel)
	failedAt := strings.Index(plain, ui.FailedLabel)
	reasonAt := strings.Index(plain, "Meta Tunnel")
	if enableAt < 0 || failedAt < enableAt || reasonAt < failedAt {
		t.Fatalf("want Object · Action · Failed · reason, got %q", plain)
	}
}
```

宽度数字若与 `lipgloss.Width` 实际差 1–2，以测试失败信息为准微调，但语义不变：42 够放下 `System proxy · Enable · Succeeded` + 时钟，不够放下完整 Detail；28 不够时钟。

- [ ] **Step 2: 跑测试确认失败**

```powershell
go test ./internal/tui/pages/overview -run 'TestFormatOperationLine_Narrow|TestFormatOperationLine_VeryNarrow|TestFormatOperationLine_Failure' -count=1
```

Expected: Narrow 测试 FAIL（长 Detail 仍在，或时间被裁掉）。

- [ ] **Step 3: 按预算选择左侧变体再右对齐**

把 `formatOperationLine` 改成：先构造候选左侧（有/无 Detail），用 `fits(left, width, withTime)` 判断 `lipgloss.Width(left)+1+8 <= width`（时钟 8 列：`15:04:05`）。不 fit 就去掉 Detail；仍不 fit 就去掉时间；再不行用 `ui.TruncateDisplay` 按显示宽度截 Object（CJK 双宽），留 Action · State。

不要把整行丢给 `MaxWidth`。

- [ ] **Step 4: 跑测试确认通过**

```powershell
go test ./internal/tui/pages/overview -run TestFormatOperationLine_ -count=1
```

Expected: PASS。若宽度常数过严/过松，只改测试宽度，不放宽优先级。

- [ ] **Step 5: Commit（仅当用户要求）**

```powershell
git add internal/tui/pages/overview/model.go internal/tui/pages/overview/model_test.go
git commit -s -m "feat(tui): 窄卡账本先丢摘要再丢时间"
```

---

### Task 7: Overview `View` 使用新拼行

**Files:**
- Modify: `internal/tui/pages/overview/model.go`（`View` 循环）
- Modify: `internal/tui/pages/overview/model_test.go`
- Modify: `internal/tui/model_test.go`（Overview 含 Title 回退的那段仍要看到 Object 与 Succeeded）

**Interfaces:**
- Consumes: `formatOperationLine`、`SectionTextWidth(fullCardInner())`
- Produces: Recent operations 卡内可见 Action / Detail / 右对齐时间

- [ ] **Step 1: 写失败测试**

```go
func TestOverview_RecentOperationsShowsActionDetailAndTime(t *testing.T) {
	at := time.Date(2026, 8, 31, 14, 32, 1, 0, time.Local)
	model := New()
	model.SetSize(100, 30)
	model.SetSnapshot(Snapshot{
		Operations: []ui.OperationRecord{{
			Object: ui.SystemProxyLabel,
			Action: ui.EnableSystemProxyLabel,
			Detail: "127.0.0.1:7890 · " + ui.PortOwned,
			State:  ui.SucceededLabel,
			At:     at,
		}},
	})
	view := model.View()
	for _, want := range []string{
		ui.RecentOperationsTitle, ui.SystemProxyLabel, ui.EnableSystemProxyLabel,
		"127.0.0.1:7890", ui.PortOwned, ui.SucceededLabel, at.Local().Format("15:04:05"),
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q:\n%s", want, view)
		}
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```powershell
go test ./internal/tui/pages/overview -run TestOverview_RecentOperationsShowsActionDetailAndTime -count=1
```

Expected: FAIL，View 仍是 `Object · State`，没有 Enable / 地址 / 时钟。

- [ ] **Step 3: 改 `View` 循环**

把：

```go
state := ui.ToneStyle(m.theme, ui.ClassifyStatusTone(operation.State)).Render(operation.State)
lines = append(lines, fmt.Sprintf("%s · %s", valueOr(operation.Object, operation.ID), state))
```

换成：

```go
width := ui.SectionTextWidth(m.fullCardInner())
lines = append(lines, formatOperationLine(m.theme, operation, width))
```

`width` 提到循环外计算一次。

- [ ] **Step 4: 跑相关测试**

```powershell
go test ./internal/tui/pages/overview ./internal/tui -count=1
```

Expected: PASS。`TestOverview_RendersAuthoritativeCardsAndSessionOperations` 仍能看到 `mihomo` 与 `Succeeded`；有 `At` 的那条现在行尾会多时钟。

- [ ] **Step 5: Commit（仅当用户要求）**

```powershell
git add internal/tui/pages/overview/model.go internal/tui/pages/overview/model_test.go
git commit -s -m "feat(tui): Overview 渲染 SysProxy/TUN 账本详情"
```

---

### Task 8: 格式化与回归

**Files:** 本计划改过的全部 Go 文件

- [ ] **Step 1: gofmt**

```powershell
gofmt -w internal/tui/operation_ledger.go internal/tui/operation_ledger_test.go internal/tui/model.go internal/tui/model_test.go internal/tui/ui/monitor.go internal/tui/ui/strings.go internal/tui/pages/system/model.go internal/tui/pages/overview/model.go internal/tui/pages/overview/model_test.go
```

- [ ] **Step 2: 包测试 + vet**

```powershell
go test ./internal/tui ./internal/tui/pages/overview ./internal/tui/pages/system -count=1
go vet ./internal/tui ./internal/tui/pages/overview ./internal/tui/pages/system
gofmt -l internal/tui/operation_ledger.go internal/tui/operation_ledger_test.go internal/tui/model.go internal/tui/model_test.go internal/tui/ui/monitor.go internal/tui/ui/strings.go internal/tui/pages/system/model.go internal/tui/pages/overview/model.go internal/tui/pages/overview/model_test.go
```

Expected: 测试 PASS，vet 无输出，`gofmt -l` 无输出。

- [ ] **Step 3: 按风险扩大**

```powershell
go test ./internal/tui/...
```

- [ ] **Step 4: 对照 spec 自检**

- SysProxy/TUN 成功能区分 Enable / Disable / Force
- 失败有短因，无 registry 原文、无 `current_server`
- 时间右对齐 `15:04:05`；`At` 为零不画
- 窄卡保留 Action + 成败
- 其它动作 Action/Detail 为空
- 未改 `/v1`、daemon、CHANGELOG

---

## Self-review

1. **Spec coverage:** §5 填表 → Task 1–4；§6 文案 → Task 1–3 常量；§7 渲染与时钟 → Task 5–7；§9 测试 → 各 Task 的失败测试；System 徽章明确不做。
2. **Placeholders:** 无 TBD。Task 1 的 TUN/失败函数返回空是刻意的任务边界，Task 2/3 填满。
3. **Types:** `newOperationRecord`、`proxyOutcome`/`tunOutcome`、`formatOperationLine(theme, op, width)` 在后续 Task 中名称一致。
