# Daemon 启动失败可观测性 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 端口冲突带占用者信息；runtime 装配失败时驻留可查询的降级控制面；控制通道 listen 失败时向 SCM 报失败；TUI/CLI 展示净化后的原因。

**Architecture:** 预检仍以 `net.Listen` 为准，占用者查找经 `internal/platform` 平台文件 best-effort 注入。装配失败则 `state.Health=degraded` + `Runtime: nil` 调用现有 `daemon.Run`。`service.program.Start` 等待 `daemon.Options.Ready` 或 run 错误。TUI 只改页脚文案；客户端仍只走控制协议。

**Tech Stack:** Go 1.26，`golang.org/x/sys`（已有），kardianos/service，标准 `go test`。`CGO_ENABLED=0`。

**设计文档:** `docs/superpowers/specs/2026-08-17-daemon-startup-visibility-design.md`

**工作目录（worktree）:** 必须在 `.worktrees/fix-daemon-startup-visibility`（分支 `fix/daemon-startup-visibility`）。禁止修改主工作区 `main`。

## Global Constraints

- 禁止在 `main` 上直接改代码或提交。
- `/v1` 只允许加法：`protocol.Status.last_error` 可省略；`health` 新增值 `degraded`；`managed port is unavailable` 的 code/message 不变。
- 客户端不得读取 daemon 私有文件；不写 last-error sidecar。
- 错误文案不得包含 token、controller secret、订阅 URL。
- 平台实现必须放在 `_windows.go` / `_linux.go` / `_darwin.go`。
- 测试不得访问公网、真实用户目录或已安装的 mihomo。
- 每个行为先写失败测试，再写最小实现。
- 修改过的 Go 文件必须 `gofmt`。
- Conventional Commits：类型英文、摘要中文。一个 task 一个 commit。

---

### Task 1: Snapshot.LastError 与 Health 常量

**Files:**
- Modify: `internal/state/store.go`
- Test: `internal/state/store_test.go`

**Interfaces:**
- Consumes: 现有 `Snapshot` / `Store`
- Produces: `state.HealthOK`、`state.HealthDegraded`；`Snapshot.LastError string`

- [ ] **Step 1: 写失败测试**

在 `internal/state/store_test.go` 追加：

```go
func TestStoreRoundTripsLastErrorAndDegradedHealth(t *testing.T) {
	store := NewStore(Snapshot{Health: HealthDegraded, LastError: "managed port is unavailable"})
	got := store.Load()
	if got.Health != HealthDegraded || got.LastError != "managed port is unavailable" {
		t.Fatalf("got=%#v", got)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```powershell
Set-Location D:\modular_dev\mihari\.worktrees\fix-daemon-startup-visibility
go test ./internal/state -run TestStoreRoundTripsLastErrorAndDegradedHealth -count=1
```

预期：编译失败，`HealthDegraded` / `LastError` 未定义。

- [ ] **Step 3: 最小实现**

`internal/state/store.go` 在 `Snapshot` 定义前增加：

```go
const (
	HealthOK       = "ok"
	HealthDegraded = "degraded"
)
```

在 `Snapshot` 中 `Health` 后增加 `LastError string`。

- [ ] **Step 4: 跑测试确认通过**

同 Step 2。预期：PASS。再跑 `go test ./internal/state -count=1`。

- [ ] **Step 5: Commit**

```powershell
git add internal/state/store.go internal/state/store_test.go
git commit -m "feat(state): Snapshot 增加 LastError 与 degraded 健康值"
```

---

### Task 2: Status.last_error 加法契约

**Files:**
- Modify: `internal/control/protocol/status.go`
- Test: `internal/control/protocol/runtime_test.go`

**Interfaces:**
- Consumes: Task 1 的语义（字符串，不直接依赖 state 包）
- Produces: `protocol.Status.LastError`，json `last_error,omitempty`

- [ ] **Step 1: 写失败测试**

在 `internal/control/protocol/runtime_test.go` 的 `TestStatus_OldJSONStillDecodes` 后追加：

```go
func TestStatus_LastErrorAdditiveRoundTrip(t *testing.T) {
	want := Status{Schema: "mihari/v1", Health: "degraded", LastError: "managed port is unavailable"}
	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"last_error":"managed port is unavailable"`) {
		t.Fatalf("raw=%s", raw)
	}
	var got Status
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Health != want.Health || got.LastError != want.LastError {
		t.Fatalf("got=%#v", got)
	}
}

func TestStatus_LastErrorOmittedWhenEmpty(t *testing.T) {
	raw, err := json.Marshal(Status{Schema: "mihari/v1", Health: "ok"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "last_error") {
		t.Fatalf("raw=%s", raw)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```powershell
go test ./internal/control/protocol -run "TestStatus_LastError" -count=1
```

预期：`Status` 没有 `LastError` 字段，编译失败。

- [ ] **Step 3: 最小实现**

`internal/control/protocol/status.go` 的 `Status` 增加：

```go
LastError string `json:"last_error,omitempty"`
```

放在 `Health` 之后、`StartedAt` 之前。

- [ ] **Step 4: 跑测试确认通过**

同 Step 2，再跑 `go test ./internal/control/protocol -count=1`。确认 `TestStatusJSON` 不在本包；`TestStatus_OldJSONStillDecodes` 仍过。

- [ ] **Step 5: Commit**

```powershell
git add internal/control/protocol/status.go internal/control/protocol/runtime_test.go
git commit -m "feat(protocol): Status 增加可省略 last_error"
```

---

### Task 3: GET /v1/status 映射 LastError

**Files:**
- Modify: `internal/control/server/server.go`（`status` 方法）
- Test: `internal/control/server/server_test.go`

**Interfaces:**
- Consumes: `state.Snapshot.LastError`、`protocol.Status.LastError`
- Produces: HTTP `GET /v1/status` 在 snapshot 有 LastError 时输出该字段

- [ ] **Step 1: 写失败测试**

在 `internal/control/server/server_test.go` 追加：

```go
func TestStatusIncludesSnapshotLastError(t *testing.T) {
	server := New(Options{
		Token: "test-token",
		Store: state.NewStore(state.Snapshot{
			Version: "dev", Health: state.HealthDegraded, LastError: "managed port is unavailable",
		}),
	})
	request := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	request.Header.Set("Authorization", "Bearer test-token")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var got protocol.Status
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Health != state.HealthDegraded || got.LastError != "managed port is unavailable" {
		t.Fatalf("got=%#v", got)
	}
}
```

补全本文件已有的 `encoding/json`、`net/http`、`net/http/httptest`、`protocol`、`state` 导入（若缺失）。

- [ ] **Step 2: 跑测试确认失败**

```powershell
go test ./internal/control/server -run TestStatusIncludesSnapshotLastError -count=1
```

预期：FAIL，`got.LastError` 为空（handler 未映射）。

- [ ] **Step 3: 最小实现**

`internal/control/server/server.go` 构造 `protocol.Status` 时增加 `LastError: snapshot.LastError`。

- [ ] **Step 4: 跑测试确认通过**

同 Step 2，再跑 `go test ./internal/control/server -count=1`。

- [ ] **Step 5: Commit**

```powershell
git add internal/control/server/server.go internal/control/server/server_test.go
git commit -m "feat(control): status 端点返回 last_error"
```

---

### Task 4: 平台 TCP 占用者查找

**Files:**
- Create: `internal/platform/tcp_occupant.go`（共享类型）
- Create: `internal/platform/tcp_occupant_windows.go`
- Create: `internal/platform/tcp_occupant_linux.go`
- Create: `internal/platform/tcp_occupant_darwin.go`
- Create: `internal/platform/tcp_occupant_test.go`

**Interfaces:**
- Consumes: `net.Listen` 得到的 `host:port`
- Produces: `platform.TCPOccupant{PID int, Process string}`；`LookupTCPOccupant(address string) (TCPOccupant, bool)`

- [ ] **Step 1: 写失败测试**

`internal/platform/tcp_occupant_test.go`：

```go
package platform

import (
	"net"
	"os"
	"runtime"
	"testing"
)

func TestLookupTCPOccupantFindsThisProcessListener(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("darwin occupant lookup is best-effort and may be unavailable without CGO")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	got, ok := LookupTCPOccupant(listener.Addr().String())
	if !ok {
		t.Fatal("expected occupant")
	}
	if got.PID != os.Getpid() {
		t.Fatalf("pid=%d want=%d process=%q", got.PID, os.Getpid(), got.Process)
	}
	if got.Process == "" {
		t.Fatal("empty process name")
	}
}

func TestLookupTCPOccupantUnknownAddress(t *testing.T) {
	if _, ok := LookupTCPOccupant("127.0.0.1:1"); ok {
		t.Fatal("did not expect occupant on unused port 1")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```powershell
go test ./internal/platform -run TestLookupTCPOccupant -count=1
```

预期：`LookupTCPOccupant` 未定义。

- [ ] **Step 3: 最小实现**

`tcp_occupant.go`：

```go
package platform

type TCPOccupant struct {
	PID     int
	Process string
}
```

Windows：用 `iphlpapi.GetExtendedTcpTable`（`AF_INET` / `AF_INET6`，`TCP_TABLE_OWNER_PID_LISTENER`）匹配本地 IP+端口，再用 `windows.OpenProcess` + `QueryFullProcessImageName` 取基名。PID 0 视为未找到。

Linux：解析 `/proc/net/tcp` 与 `/proc/net/tcp6` 的 LISTEN 行（state `0A`），按 inode 扫描 `/proc/[pid]/fd` 的 `socket:[inode]`，进程名读 `/proc/[pid]/comm`。

Darwin：`LookupTCPOccupant` 直接返回 `({}, false)`，满足「无 CGO、不 exec lsof」。

- [ ] **Step 4: 跑测试确认通过**

同 Step 2。Windows 上两条都应 PASS。再跑 `go test ./internal/platform -count=1`。

- [ ] **Step 5: Commit**

```powershell
git add internal/platform/tcp_occupant.go internal/platform/tcp_occupant_windows.go internal/platform/tcp_occupant_linux.go internal/platform/tcp_occupant_darwin.go internal/platform/tcp_occupant_test.go
git commit -m "feat(platform): 查找占用托管端口的进程"
```

---

### Task 5: 端口预检带占用者 details

**Files:**
- Create: `internal/app/ports.go`
- Create: `internal/app/ports_test.go`
- Modify: `internal/app/runtime.go`（把内联循环换成 `probeManagedPorts`）

**Interfaces:**
- Consumes: `platform.LookupTCPOccupant`、`config.Settings` 的三个地址
- Produces: `probeManagedPorts(settings, lookup) error`，失败为 `protocol.APIError`（message 仍为 `managed port is unavailable`）

- [ ] **Step 1: 写失败测试**

`internal/app/ports_test.go`：

```go
package app

import (
	"errors"
	"net"
	"testing"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/platform"
)

func TestProbeManagedPortsReportsOccupantDetails(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	address := listener.Addr().String()
	settings := config.Settings{MixedAddr: address, ControllerAddr: "127.0.0.1:18091", WebAddr: "127.0.0.1:18092"}
	err = probeManagedPorts(settings, func(got string) (platform.TCPOccupant, bool) {
		if got != address {
			return platform.TCPOccupant{}, false
		}
		return platform.TCPOccupant{PID: 4321, Process: "clash.exe"}, true
	})
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeInvalidState || apiError.Message != "managed port is unavailable" {
		t.Fatalf("err=%v", err)
	}
	if apiError.Details["setting"] != "mixed-addr" || apiError.Details["address"] != address {
		t.Fatalf("details=%v", apiError.Details)
	}
	if apiError.Details["pid"] != 4321 || apiError.Details["process"] != "clash.exe" {
		t.Fatalf("details=%v", apiError.Details)
	}
}

func TestProbeManagedPortsOmitsOccupantWhenLookupMisses(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	settings := config.Settings{
		MixedAddr: listener.Addr().String(), ControllerAddr: "127.0.0.1:18091", WebAddr: "127.0.0.1:18092",
	}
	err = probeManagedPorts(settings, func(string) (platform.TCPOccupant, bool) {
		return platform.TCPOccupant{}, false
	})
	var apiError protocol.APIError
	if !errors.As(err, &apiError) {
		t.Fatalf("err=%v", err)
	}
	if _, ok := apiError.Details["pid"]; ok {
		t.Fatalf("details=%v", apiError.Details)
	}
	if _, ok := apiError.Details["process"]; ok {
		t.Fatalf("details=%v", apiError.Details)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```powershell
go test ./internal/app -run TestProbeManagedPorts -count=1
```

预期：`probeManagedPorts` 未定义。

- [ ] **Step 3: 最小实现**

抽出 `probeManagedPorts`。`lookup == nil` 时用 `platform.LookupTCPOccupant`。`runtime.go` 内联循环改为 `if err := probeManagedPorts(settings, nil); err != nil { return nil, err }`。

details 构造：始终放 `setting`/`address`；仅当 lookup 成功且 `PID > 0` 时放 `pid`/`process`。`process` 必须是基名（`filepath.Base`）。

- [ ] **Step 4: 跑测试确认通过**

同 Step 2，再跑 `go test ./internal/app -count=1`。

- [ ] **Step 5: Commit**

```powershell
git add internal/app/ports.go internal/app/ports_test.go internal/app/runtime.go
git commit -m "feat(app): 端口预检返回占用进程 details"
```

---

### Task 6: 降级 Store 与启动错误净化

**Files:**
- Create: `internal/app/degraded.go`
- Create: `internal/app/degraded_test.go`

**Interfaces:**
- Consumes: Task 1 常量；`protocol.APIError` details
- Produces: `NewDegradedStore(version string, err error) *state.Store`；`FormatStartupError(err error) string`

- [ ] **Step 1: 写失败测试**

```go
package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/state"
)

func TestNewDegradedStoreUsesPortOccupantMessage(t *testing.T) {
	err := protocol.APIError{
		Code: protocol.CodeInvalidState, Message: "managed port is unavailable",
		Details: map[string]any{"setting": "mixed-addr", "address": "127.0.0.1:7890", "pid": 4321, "process": "clash.exe"},
	}
	store := NewDegradedStore("v-test", err)
	got := store.Load()
	if got.Version != "v-test" || got.Health != state.HealthDegraded {
		t.Fatalf("got=%#v", got)
	}
	if got.LastError != "managed port mixed-addr 127.0.0.1:7890 is held by clash.exe (pid 4321)" {
		t.Fatalf("LastError=%q", got.LastError)
	}
}

func TestFormatStartupErrorOmitsSecretAndFallsBack(t *testing.T) {
	err := protocol.APIError{
		Code: protocol.CodeDataFailure, Message: "create mihari data directories",
		Details: map[string]any{"secret": "should-not-appear", "token": "also-hidden"},
	}
	got := FormatStartupError(err)
	if got != "create mihari data directories" {
		t.Fatalf("got=%q", got)
	}
	if strings.Contains(got, "should-not-appear") || strings.Contains(got, "also-hidden") {
		t.Fatalf("leaked secret: %q", got)
	}
	if got := FormatStartupError(errors.New("open /x?token=abc")); got != "daemon startup failed" {
		t.Fatalf("got=%q", got)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```powershell
go test ./internal/app -run "TestNewDegradedStore|TestFormatStartupError" -count=1
```

预期：符号未定义。

- [ ] **Step 3: 最小实现**

`FormatStartupError`：

- `errors.As` 到 `APIError`：若 message 为 `managed port is unavailable` 且有 `setting`+`address`，按有无 pid/process 拼设计文档中的两句；否则返回 `APIError.Message`。
- 其它错误返回 `daemon startup failed`。
- 不得把 `details` 里除 `setting`/`address`/`pid`/`process` 以外的键拼进字符串。

`NewDegradedStore`：`Health: state.HealthDegraded`，`LastError: FormatStartupError(err)`，`Version`/`StartedAt` 填入。

- [ ] **Step 4: 跑测试确认通过**

同 Step 2，再跑 `go test ./internal/app -count=1`。

- [ ] **Step 5: Commit**

```powershell
git add internal/app/degraded.go internal/app/degraded_test.go
git commit -m "feat(app): 装配失败生成可查询的 degraded store"
```

---

### Task 7: daemon body 装配失败时驻留降级控制面

**Files:**
- Modify: `cmd/mihari/main.go`
- Test: `internal/daemon/run_test.go`（Ready 在 Runtime nil 时仍关闭；若已有则只补装配失败走 `Runtime: nil` 的行为说明到 main，并用 daemon 测试锁住契约）

**Interfaces:**
- Consumes: `app.NewDegradedStore`、`daemon.Options.Ready`、`daemon.Options.Runtime`
- Produces: 生产 daemon 在 `BuildRuntimeWithOptions` 失败时仍 `Listen` 并关闭 Ready

main 难以单测。先用 daemon 测试锁住「Runtime nil 仍 Ready」，再改 main 的薄装配。

- [ ] **Step 1: 写失败测试**

若 `TestRunStopsWhenContextIsCancelled` 已经在 `Runtime` 为空时关闭 Ready，则新增更明确的名字（可直接复用该测试作为回归，不必新测）。再增加：

```go
func TestRunSignalsReadyWithoutRuntime(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{Endpoint: transporttest.Endpoint(t), Token: "token", Version: "dev", Ready: ready})
	}()
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not become ready")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
```

若与现有测试重复，将现有测试重命名为本名，保持一条即可。然后改 main。

- [ ] **Step 2: 跑测试确认失败或已通过**

```powershell
go test ./internal/daemon -run TestRunSignalsReadyWithoutRuntime -count=1
```

若仅是重命名现有测试且已通过，不要为了红而破坏已有行为。本 task 的红在下一步改 main 之前不需要假失败。**实现 main 前先确认 daemon 契约为绿。**

- [ ] **Step 3: 改 main**

`runDaemonBody` 使用外层传入的 `ready chan struct{}`（在 `main` 里创建一次）：

```go
ready := make(chan struct{})
runDaemonBody := func(ctx context.Context) error {
    // 现有 paths / settings 加载
    assembly, err := app.BuildRuntimeWithOptions(...)
    if err != nil {
        return daemon.Run(ctx, daemon.Options{
            Endpoint: endpoint, Token: token, Version: buildinfo.Version,
            Ready: ready, Store: app.NewDegradedStore(buildinfo.Version, err),
        })
    }
    return daemon.Run(ctx, daemon.Options{
        Endpoint: endpoint, Token: token, Version: buildinfo.Version,
        Ready: ready, Store: assembly.Store, Runtime: assembly.Manager,
    })
}
serviceManager = service.New(service.Options{Run: runDaemonBody, Ready: ready})
```

`Ready` 字段在 Task 8 才加入 `service.Options`。若本 task 先于 Task 8 提交，main 可先只传 `daemon.Options.Ready`，`service.New` 的 Ready 放到 Task 8 再接。

本 task **只改 daemon 失败驻留**（`NewDegradedStore` + `Runtime` 省略）。`service.New` 的 Ready 接线留给 Task 8，避免一个 commit 混两件事。

- [ ] **Step 4: 验证**

```powershell
go test ./internal/daemon ./internal/app -count=1
go build -o NUL ./cmd/mihari
```

- [ ] **Step 5: Commit**

```powershell
git add cmd/mihari/main.go internal/daemon/run_test.go
git commit -m "feat(daemon): 装配失败时驻留仅状态控制面"
```

---

### Task 8: program.Start 等待 Ready 或失败

**Files:**
- Modify: `internal/service/service.go`
- Modify: `cmd/mihari/main.go`（把 `ready` 传入 `service.New`）
- Test: `internal/service/service_test.go`

**Interfaces:**
- Consumes: `service.Options.Ready <-chan struct{}`；`StartTimeout time.Duration`（零值 15s）
- Produces: 生产 `program.Start` 在 Ready 关闭后返回 nil，在 run 提前结束时返回 run 错误

- [ ] **Step 1: 写失败测试**

在 `internal/service/service_test.go` 追加：

```go
func TestProgramStartReturnsRunErrorBeforeReady(t *testing.T) {
	want := errors.New("listen failed")
	ready := make(chan struct{})
	p := &program{run: func(context.Context) error { return want }, ready: ready, startTimeout: time.Second}
	if err := p.Start(nil); !errors.Is(err, want) {
		t.Fatalf("err=%v", err)
	}
}

func TestProgramStartSucceedsAfterReady(t *testing.T) {
	ready := make(chan struct{})
	started := make(chan struct{})
	p := &program{
		run: func(ctx context.Context) error {
			close(ready)
			close(started)
			<-ctx.Done()
			return nil
		},
		ready:        ready,
		startTimeout: time.Second,
	}
	if err := p.Start(nil); err != nil {
		t.Fatal(err)
	}
	<-started
	if err := p.Stop(nil); err != nil {
		t.Fatal(err)
	}
}

func TestProgramStartWithoutReadyReturnsImmediately(t *testing.T) {
	p := &program{run: func(context.Context) error { return errors.New("later") }}
	if err := p.Start(nil); err != nil {
		t.Fatal(err)
	}
	if err := p.Stop(nil); err != nil {
		t.Fatal(err)
	}
}
```

补 `errors`、`time` 导入。现有 `TestProgramStartStopCancelsRun` 不得被破坏。

- [ ] **Step 2: 跑测试确认失败**

```powershell
go test ./internal/service -run "TestProgramStartReturnsRunErrorBeforeReady|TestProgramStartSucceedsAfterReady" -count=1
```

预期：`program` 没有 `ready` 字段，编译失败。

- [ ] **Step 3: 最小实现**

`program` 增加 `ready <-chan struct{}`、`startTimeout time.Duration`、`runErr error`。

`Start`：启动 goroutine 保存 `run` 错误并 `close(done)`。`ready == nil` 时立即返回 nil。否则：

```go
timeout := p.startTimeout
if timeout <= 0 {
    timeout = 15 * time.Second
}
select {
case <-p.ready:
    return nil
case <-p.done:
    return p.runErr
case <-time.After(timeout):
    if p.cancel != nil {
        p.cancel()
    }
    return protocol.APIError{Code: protocol.CodeInvalidState, Message: "service did not become ready"}
}
```

`New` 默认 `NewController` 闭包读取 `opts.Ready` / `opts.StartTimeout` 注入 `program`。`Options` 增加这两个字段。`cmd/mihari/main.go` 把 Task 7 的 `ready` 传给 `service.New`。

- [ ] **Step 4: 跑测试确认通过**

```powershell
go test ./internal/service -count=1
go build -o NUL ./cmd/mihari
```

- [ ] **Step 5: Commit**

```powershell
git add internal/service/service.go internal/service/service_test.go cmd/mihari/main.go
git commit -m "fix(service): Start 等待控制面 Ready 或返回启动错误"
```

---

### Task 9: TUI 重连页脚展示净化原因

**Files:**
- Modify: `internal/tui/ui/strings.go`
- Modify: `internal/tui/model.go`
- Test: `internal/tui/model_test.go` 或 `internal/tui/shell_view_test.go`

**Interfaces:**
- Consumes: `session.Event.Err`、`service.StatusKind`
- Produces: 页脚在 `Event.Err != nil` 时追加短原因；`Err == nil` 时文案与现在一致

- [ ] **Step 1: 写失败测试**

在 `internal/tui/model_test.go` 找一处已有 `EventReconnecting` 断言附近追加：

```go
func TestReconnectingFooterIncludesSanitizedDialError(t *testing.T) {
	model := newTestModel(t) // 使用文件中现有的模型构造辅助函数；若无，则复制邻近测试的最小装配
	model.serviceLoaded = true
	model.serviceStatus = service.StatusRunning
	model.applySessionEvent(session.Event{
		Kind: session.EventReconnecting,
		Err:  errors.New("open \\\\.\\pipe\\mihari-control: The system cannot find the file specified."),
	})
	got := model.footerGlobalSegment()
	if !strings.Contains(got, ui.GlobalStateStaleLabel) {
		t.Fatalf("missing stale label: %q", got)
	}
	if !strings.Contains(got, ui.DaemonNotListening) {
		t.Fatalf("missing sanitized reason: %q", got)
	}
	if !strings.Contains(got, ui.ServiceRunningUnreachable) {
		t.Fatalf("missing service hint: %q", got)
	}
	if strings.Contains(got, `\\.\pipe\`) {
		t.Fatalf("leaked pipe path: %q", got)
	}
}

func TestReconnectingFooterUnchangedWithoutError(t *testing.T) {
	model := newTestModel(t)
	model.applySessionEvent(session.Event{Kind: session.EventReconnecting})
	got := model.footerGlobalSegment()
	if got != ui.GlobalStateStaleLabel {
		t.Fatalf("got=%q", got)
	}
}
```

实现时把 `newTestModel` 换成该文件真实的构造方式（读邻近测试，不要发明新的巨大夹具）。

- [ ] **Step 2: 跑测试确认失败**

```powershell
go test ./internal/tui -run "TestReconnectingFooter" -count=1
```

预期：常量未定义，或页脚不含新文案。

- [ ] **Step 3: 最小实现**

`strings.go`：

```go
DaemonNotListening         = "Daemon is not listening"
DaemonConnectionDenied     = "Permission denied connecting to daemon"
DaemonConnectionFailed     = "Daemon connection failed"
ServiceRunningUnreachable  = "Service is running but the control plane is not reachable"
DaemonDegradedLabel        = "Daemon degraded"
```

`model.go` 增加 `daemonHint string`。`EventReconnecting` 时：

```go
model.daemonHint = sanitizeDaemonDialError(event.Err)
if model.serviceLoaded && model.serviceStatus == service.StatusRunning && event.Err != nil {
    model.daemonHint = joinHints(model.daemonHint, ui.ServiceRunningUnreachable)
}
```

`EventConnected` 且后续 status 非 degraded 时清空。`footerGlobalSegment` 在 stale 标签后若 `daemonHint != ""` 追加 ` — ` + hint。

`sanitizeDaemonDialError`：nil → `""`；错误字符串（小写）含 `cannot find the file`、`no such file`、`connect: connection refused`、`pipe`+`not` → `DaemonNotListening`；含 `access is denied` / `permission denied` → `DaemonConnectionDenied`；其它 → `DaemonConnectionFailed`。

- [ ] **Step 4: 跑测试确认通过**

同 Step 2，再跑 `go test ./internal/tui -count=1`。无 Err 的 golden / `GlobalStateStaleLabel` 断言必须仍过。

- [ ] **Step 5: Commit**

```powershell
git add internal/tui/ui/strings.go internal/tui/model.go internal/tui/model_test.go
git commit -m "feat(tui): 重连页脚显示净化后的连接失败原因"
```

---

### Task 10: TUI 展示降级 last_error

**Files:**
- Modify: `internal/tui/model.go`
- Test: `internal/tui/model_test.go`

**Interfaces:**
- Consumes: `protocol.Status.Health`、`protocol.Status.LastError`
- Produces: 已连接且 degraded 时页脚为 `Daemon degraded — {last_error}`

- [ ] **Step 1: 写失败测试**

```go
func TestConnectedDegradedFooterShowsLastError(t *testing.T) {
	model := newTestModel(t)
	model.applySessionEvent(session.Event{Kind: session.EventConnected})
	model.applySessionEvent(session.Event{
		Kind: session.EventStatus,
		Status: protocol.Status{Health: "degraded", LastError: "managed port mixed-addr 127.0.0.1:7890 is unavailable"},
	})
	got := model.footerGlobalSegment()
	if !strings.Contains(got, ui.DaemonDegradedLabel) {
		t.Fatalf("got=%q", got)
	}
	if !strings.Contains(got, "managed port mixed-addr") {
		t.Fatalf("got=%q", got)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```powershell
go test ./internal/tui -run TestConnectedDegradedFooterShowsLastError -count=1
```

预期：页脚不含 `Daemon degraded`。

- [ ] **Step 3: 最小实现**

`EventStatus` 更新 `model.status` 之后，`footerGlobalSegment` 在非 spinner 路径优先：

```go
if model.connected && model.status.Health == state.HealthDegraded && model.status.LastError != "" {
    return ui.DaemonDegradedLabel + " — " + model.status.LastError
}
```

TUI 可直接比较字符串 `"degraded"`，或导入 `state`。优先用 `state.HealthDegraded`，避免魔数。

- [ ] **Step 4: 跑测试确认通过**

```powershell
go test ./internal/tui -count=1
```

- [ ] **Step 5: Commit**

```powershell
git add internal/tui/model.go internal/tui/model_test.go
git commit -m "feat(tui): 连接降级控制面时展示 last_error"
```

---

### Task 11: CLI status 打印 Error 行

**Files:**
- Modify: `internal/cli/status.go`
- Test: `internal/cli/status_test.go`

**Interfaces:**
- Consumes: `protocol.Status.LastError`
- Produces: 文本多一行 `Error: ...`；空值时输出与现在完全一致

- [ ] **Step 1: 写失败测试**

在 `TestStatusHumanOutput` 后追加：

```go
func TestStatusHumanOutputIncludesLastError(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := Execute(context.Background(), []string{"status"}, stdout, stderr, Dependencies{
		StatusClient: fakeStatusClient{status: protocol.Status{
			DaemonVersion: "v0.1.0",
			Revision:      4,
			Health:        "degraded",
			LastError:     "managed port is unavailable",
			StartedAt:     time.Unix(100, 0).UTC(),
		}},
	})
	if code != ExitOK || stderr.Len() != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	want := "Daemon: v0.1.0\nHealth: degraded\nError: managed port is unavailable\nRevision: 4\nStarted: 1970-01-01T00:01:40Z\n"
	if stdout.String() != want {
		t.Fatalf("stdout=%q want=%q", stdout.String(), want)
	}
}
```

现有 `TestStatusHumanOutput` 与 `TestStatusJSON` 必须继续按原字符串精确匹配。

- [ ] **Step 2: 跑测试确认失败**

```powershell
go test ./internal/cli -run TestStatusHumanOutputIncludesLastError -count=1
```

预期：stdout 缺少 `Error:` 行。

- [ ] **Step 3: 最小实现**

`status.go` 在打印 Health 之后：

```go
if status.LastError != "" {
    if _, err = fmt.Fprintf(command.OutOrStdout(), "Error: %s\n", status.LastError); err != nil {
        return err
    }
}
```

- [ ] **Step 4: 跑测试确认通过**

```powershell
go test ./internal/cli -count=1
```

- [ ] **Step 5: Commit**

```powershell
git add internal/cli/status.go internal/cli/status_test.go
git commit -m "feat(cli): status 在降级时打印 Error 行"
```

---

### Task 12: 架构文档与命令示例

**Files:**
- Modify: `docs/architecture.md`（控制面一节）
- Modify: `docs/commands.md`（仅当其中有 `mihari status` 文本示例时）

**Interfaces:**
- Consumes: 本设计已实现行为
- Produces: 文档与行为一致

- [ ] **Step 1: 核对 commands.md 是否有 status 示例**

若有 Health 示例，补 `Error:` 行。没有则只改 `docs/architecture.md`。

- [ ] **Step 2: 更新 architecture.md**

在控制面列表增加：

- daemon 装配失败但控制通道可 listen 时驻留降级控制面，`GET /v1/status` 的 `health` 为 `degraded`，并带可省略 `last_error`。
- OS 服务 `Start` 等待控制通道 Ready；listen 失败则向 SCM 返回错误，不得保持假 running。
- 托管端口预检失败时 details 可含占用 PID 与进程基名；不自动杀进程。

- [ ] **Step 3: 无需测试**

文档-only。

- [ ] **Step 4: Commit**

```powershell
git add docs/architecture.md docs/commands.md
git commit -m "docs: 记录降级控制面与启动失败可见性"
```

若 `commands.md` 未改，不要 `git add` 它。

---

### Task 13: 全量验证

**Files:** 无新文件

- [ ] **Step 1: 格式与静态检查**

```powershell
Set-Location D:\modular_dev\mihari\.worktrees\fix-daemon-startup-visibility
gofmt -w cmd internal
gofmt -l cmd internal
go vet ./...
```

`gofmt -l` 应无输出。

- [ ] **Step 2: 测试**

```powershell
go test ./internal/state ./internal/control/protocol ./internal/control/server ./internal/platform ./internal/app ./internal/service ./internal/daemon ./internal/tui ./internal/cli -count=1
go test ./internal/integration -count=1
go test ./... -count=1
go test -race ./internal/service ./internal/daemon ./internal/app ./internal/tui -count=1
```

- [ ] **Step 3: 跨平台编译（本机 Windows + 交叉）**

```powershell
$env:CGO_ENABLED = '0'
go build -o bin/mihari-windows-amd64.exe ./cmd/mihari
$env:GOOS = 'linux';   $env:GOARCH = 'amd64'; go build -o bin/mihari-linux-amd64 ./cmd/mihari
$env:GOOS = 'darwin';  $env:GOARCH = 'arm64'; go build -o bin/mihari-darwin-arm64 ./cmd/mihari
Remove-Item Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue
```

交叉编译必须成功（Darwin 的 occupant stub 不得导致 linux/windows 编译失败，也不得在 darwin 文件里引用 linux 符号）。

- [ ] **Step 4: 不要提交 bin/ 产物**

确认 `git status` 无测试垃圾、无 `coverage.out`、无 `bin/`。

---

## Spec coverage

| 设计条款 | Task |
|---|---|
| 端口 details 含 pid/process | 4, 5 |
| 不自动杀进程 | 5（只报 details） |
| 装配失败降级驻留 | 6, 7 |
| `health`/`last_error` 加法 | 1, 2, 3 |
| 不写 sidecar / 不读私有文件 | 6, 7 |
| listen 失败 SCM 报错 | 8 |
| TUI 重连原因 | 9 |
| TUI 降级 last_error | 10 |
| CLI Error 行 | 11 |
| 文档 | 12 |
| CGO-free 三平台 | 4, 13 |

## 执行前注意

本计划写完后先交给用户审核。**未批准前不得开始 Task 1 的实现。** 批准后在 worktree 内按 task 执行 TDD，全绿后再开 PR。
