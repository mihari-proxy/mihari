# Phase B Mutation and Platform Lifecycle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 直接保护公开 mutation 的 coordinator/revision/rollback 语义，并补齐安全、无持久副作用的平台生命周期测试。

**Architecture:** app 的 Web mutation 通过使用方附近的最小接口解除具体 `*runtime.Manager` 测试耦合；runtime 复用现有 fake controller/panels；supervisor 扩展 fake child 验证 terminate→kill；平台包只测试命令构造、路径、分类和注入 backend，不操作真实系统状态。

**Tech Stack:** Go `testing`、现有 `state.Coordinator`、channel-controlled fake、平台 build tags。

## Global Constraints

- 遵守总路线图 Global Constraints 与 Governance Gate。
- 不扩大 daemon 单写入者边界，不新增通用 mock 框架。
- 慢 IO 和等待不得位于 coordinator 临界区。
- 平台测试不得安装服务、写真实 registry/system proxy 或启动提权流程。

---

### Task 1: App Web Mutation Boundary

**Files:**
- Modify: `internal/app/runtime.go:246`
- Modify: `internal/app/runtime_test.go`

**Interfaces:**
- Consumes: runtime manager mutation methods。
- Produces: 使用方最小接口 `webMutationRuntime`，让 `webMutator` 可用 recording fake 测试。

- [ ] **Step 1: 写编译失败的接口驱动测试**

新增 recording fake 并让 `webMutator{manager: fake}` 编译；测试 `SelectProxy`、单个/全部关闭及 TUN patch。Expected: FAIL，因为字段当前要求 `*runtime.Manager`。

- [ ] **Step 2: 引入最小接口**

```go
type webMutationRuntime interface {
    SelectProxy(context.Context, runtimeapi.Operation, string, string) error
    CloseConnection(context.Context, runtimeapi.Operation, string) error
    CloseAllConnections(context.Context, runtimeapi.Operation) error
    EnableTun(context.Context, runtimeapi.Operation) (protocol.TunStatus, error)
    DisableTun(context.Context, runtimeapi.Operation) (protocol.TunStatus, error)
}
```

将 `webMutator.manager` 改为该接口；`*runtime.Manager` 自动满足，不改变装配行为。

- [ ] **Step 3: 完成行为断言**

新增 `TestWebMutatorRoutesOperationsThroughRuntime` 与 `TestWebMutatorConfigPatchAllowlist`，断言 operation source=`web`、ID 非空且前缀正确、参数原样转发；缺失 tun、非 object、非 bool 分别返回稳定错误码。

- [ ] **Step 4: Red–Green 验证**

```console
go test -count=1 -run '^TestWebMutator' ./internal/app
go test -count=1 ./internal/app
```

- [ ] **Step 5: Commit（仅用户明确要求时）**

```console
git add internal/app/runtime.go internal/app/runtime_test.go
git commit -s -m "test(app): 覆盖 Web mutation 装配边界"
```

### Task 2: Runtime Controller Mutations

**Files:**
- Modify: `internal/runtime/manager_test.go`
- Verify: `internal/runtime/manager.go:450`

**Interfaces:**
- Consumes: `fakeController`、`newTestManager`、`Operation`。
- Produces: select/close/config mutation 的 revision、幂等和错误传播测试。

- [ ] **Step 1: 扩展 fakeController 记录**

增加调用计数、参数、可注入 error 和 channel gate；现有方法保持零值可用。

- [ ] **Step 2: 增加 mutation 表驱动测试**

新增 `TestControllerMutationsCommitRevisionAndPropagateErrors`，覆盖 SelectProxy、CloseConnection、CloseAllConnections；断言成功 revision +1，controller error 不提交 revision，参数和 context 传递正确。

- [ ] **Step 3: 增加幂等与 restart 串行测试**

对同一 operation ID 并发调用两次，断言 controller 只执行一次；使用 channel gate 证明 mutation 等待 maintenance/restart 后再进入 controller，不使用 sleep。

- [ ] **Step 4: 验证**

```console
go test -count=1 -run '^(TestControllerMutations|TestDuplicateOperationID|TestRuntimeMutationWaits)' ./internal/runtime
go test -count=1 -race ./internal/runtime
```

- [ ] **Step 5: Commit（仅用户明确要求时）**

```console
git add internal/runtime/manager_test.go
git commit -s -m "test(runtime): 补齐 controller mutation 事务语义"
```

### Task 3: Panel Mutation Symmetry

**Files:**
- Modify: `internal/runtime/panel_test.go`
- Verify: `internal/runtime/panel.go`

**Interfaces:**
- Consumes: `fakePanels`、`Manager` panel methods。
- Produces: update/uninstall/reinstall 的 coordinator、stale revision 和失败不提交测试。

- [ ] **Step 1: 扩展 fakePanels**

为 uninstall/reinstall 增加函数钩子与调用记录；锁只保护记录，不在锁内等待 channel。

- [ ] **Step 2: 写三个表驱动场景**

每个 mutation 验证：成功 revision +1；stale revision 时 service 未调用；service error 时 revision 不变。Update 另用 channel 证明准备/下载在提交锁外。

- [ ] **Step 3: 运行并最小修复真实缺陷**

```console
go test -count=1 -run 'Panel' ./internal/runtime
```

Expected: 测试先暴露任何不对称行为；只修改 `panel.go` 中被证明有问题的路径。

- [ ] **Step 4: Commit（仅用户明确要求时）**

```console
git add internal/runtime/panel.go internal/runtime/panel_test.go
git commit -s -m "test(runtime): 补齐面板 mutation 回滚与 revision 路径"
```

### Task 4: Supervisor Terminate-to-Kill Lifecycle

**Files:**
- Modify: `internal/supervisor/supervisor_test.go`
- Modify only if needed: `internal/supervisor/supervisor.go:234`

**Interfaces:**
- Consumes: `fakeChild`、fake waiter、`Supervisor.stopChild`。
- Produces: graceful terminate、timeout kill、kill error、wait completion和取消路径测试。

- [ ] **Step 1: 让 fakeChild 可分别控制 Terminate/Kill/Wait**

增加 `terminateErr`、`killErr`、`terminateCalled`、`killCalled` 和独立 `done` 控制，避免 `Terminate` 自动结束所有场景。

- [ ] **Step 2: 增加测试**

```go
func TestSupervisorStopsChildAfterTerminate(t *testing.T)
func TestSupervisorKillsChildAfterGraceTimeout(t *testing.T)
func TestSupervisorPropagatesKillError(t *testing.T)
func TestSupervisorCancellationWaitsForChildCleanup(t *testing.T)
```

用可注入 waiter/短测试 timeout 触发事件，不通过大幅缩短生产常量制造竞态。

- [ ] **Step 3: 验证 race**

```console
go test -count=1 ./internal/supervisor
go test -count=20 -race ./internal/supervisor
```

Expected: 20 次无 flaky、无竞态。

- [ ] **Step 4: Commit（仅用户明确要求时）**

```console
git add internal/supervisor/supervisor.go internal/supervisor/supervisor_test.go
git commit -s -m "test(supervisor): 覆盖子进程强制终止生命周期"
```

### Task 5: Native Platform Tests Without Side Effects

**Files:**
- Modify: `internal/platform/paths_test.go`
- Create as applicable: `internal/platform/relaunch_windows_test.go`
- Create as applicable: `internal/platform/relaunch_unix_test.go`
- Modify: `internal/service/service_test.go`
- Modify: `internal/sysproxy/*_test.go`
- Modify: `internal/elevate/elevate_test.go`

**Interfaces:**
- Consumes: 现有 command/backend/checker 注入点与 build-tagged helpers。
- Produces: 三平台各自能运行、但不改变系统状态的原生测试集合。

- [ ] **Step 1: 盘点当前平台覆盖**

```console
go test -count=1 -coverprofile=platform-before.out ./internal/platform ./internal/service ./internal/sysproxy ./internal/elevate
go tool cover -func=platform-before.out
```

- [ ] **Step 2: 补纯逻辑与注入边界**

覆盖路径 override、service status/error mapping、sysproxy owned/foreign/off、elevation checker 恢复。测试使用 `t.Setenv`、`t.TempDir`、`t.Cleanup`，不调用平台真实写操作。

- [ ] **Step 3: 补 build-tagged relaunch/command tests**

若现代码无法观察命令构造，引入包内 `commandFactory` 函数变量并在 `t.Cleanup` 恢复；Windows 断言隐藏窗口/控制台继承语义，Unix 断言参数与环境继承。禁止实际替换当前进程。

- [ ] **Step 4: 当前平台验证与交叉编译**

```console
go test -count=1 ./internal/platform ./internal/service ./internal/sysproxy ./internal/elevate ./internal/supervisor
$env:CGO_ENABLED='0'; $env:GOOS='windows'; $env:GOARCH='amd64'; go test -c -o "$env:TEMP\mihari-platform-windows-amd64.test.exe" ./internal/platform
$env:GOOS='linux'; $env:GOARCH='amd64'; go test -c -o "$env:TEMP\mihari-platform-linux-amd64.test" ./internal/platform
$env:GOOS='darwin'; $env:GOARCH='arm64'; go test -c -o "$env:TEMP\mihari-platform-darwin-arm64.test" ./internal/platform
```

Expected: 当前平台测试通过，三个目标测试包可编译；生成的测试二进制放到临时目录且不提交。

- [ ] **Step 5: 全阶段验证**

```console
go test -count=1 ./internal/app ./internal/runtime ./internal/supervisor ./internal/platform ./internal/service ./internal/sysproxy ./internal/elevate
go test -count=1 -race ./...
go vet ./...
gofmt -l internal
```

- [ ] **Step 6: Commit（仅用户明确要求时）**

```console
git add internal/platform internal/service internal/sysproxy internal/elevate
git commit -s -m "test(platform): 补齐无副作用的原生生命周期测试"
```
