# Core install timeout Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 核心包下载不设超时；只有安装阶段有时限，并且是 1 分钟。

**Architecture:** 本地控制客户端仍是默认 10 秒，单个调用可以覆盖。`InstallCore` 与 `ReinstallCore` 覆盖为不设超时：复制客户端并把 `Timeout` 设为 0，也不给整次 RPC 加 deadline。daemon 的资产下载沿用这个没有 deadline 的请求 context，默认 core HTTP 客户端的 `Timeout` 也是 0。`Manager.Install` / `Manager.Reinstall` 在 `Prepare` 成功返回后才 `context.WithTimeout` 1 分钟，包住停旧进程、试运行、健康检查和提交。检查最新版仍用自己的 8 秒 context。父 context 取消仍然打断下载和尚未结束的安装。事务接受之后的补偿继续走 `context.WithoutCancel` 加现有 90 秒。

**Tech Stack:** Go，`net/http` 的 `Timeout == 0` 表示没有客户端超时，`context.WithTimeout` 只包安装阶段，现有 `runtimeRequestOptions`。

**Spec:** 本文件第 1 节。没有单独的设计文档。需求来自 2026-10-08 的 v0.9.6 失败日志，以及随后把安装时限定为 1 分钟、下载不设超时的决定。

## Global Constraints

- 不在 `main` 或 `dev` 上提交。工作区是 `.worktrees/fix-core-install-timeout`，分支 `fix/core-install-timeout`，基线 `origin/dev` `3ed2c2524717b4279d57a9b9d921dd9188285a91`。PR 基线是 `dev`。
- 指向 `dev` 的功能 PR 不改 `CHANGELOG.md`。
- 不新增依赖，不改 `/v1` DTO、错误码、JSON envelope、CLI flag 或持久化格式。
- 发布构建保持 `CGO_ENABLED=0`。本改动无平台分支。
- 行为先有失败测试。测试不访问公网、真实用户目录、真实 mihomo 或系统服务。
- 普通本地控制请求、mihomo controller HTTP 客户端、core 重启、GeoIP、订阅超时都保持原预算。
- 归档大小上限 `maxCoreArchiveSize` 保留。不设超时不等于取消体积校验。
- 日志与错误不脱敏。安装失败仍由 runtime owner 记一次 `operation.failed`。

---

## 1. 需求

v0.9.6 在 2026-10-08 00:30 更新核心失败。界面只显示 `local control operation failed`。

- `00:30:10` 操作 `tui-system-a3de817c364fb8aad6119cd9`：TUI 对 `POST /v1/core/install` 报 `Client.Timeout exceeded while awaiting headers`。同一毫秒 daemon 正在读 GitHub 资产，HTTP 200，随后 `context canceled`。
- `00:30:28` 操作 `tui-system-d7dbb05c3f91a62808e2706e`：客户端再次 10 秒超时。daemon 已进入试运行，健康检查被同一个取消打断。旧核心恢复为 v1.19.30。

固定行为：

| 阶段 | 预算 | 起算点 |
| --- | --- | --- |
| 检查最新版 | 保持 8 秒 | `LatestVersion` / `recheckTarget` 自己的 context。这是元数据请求，不是包体下载 |
| 资产下载 | 不设超时 | `readTargetArchive`、`downloadAsset` 只用父 context。默认 `http.Client.Timeout` 为 0。不再使用 15 分钟 |
| 安装阶段 | 1 分钟 | `Prepare` 成功返回之后。包括受保护更新、旧式提交、试运行、健康检查、提交或作出回滚决定 |
| 回滚补偿 | 保持 90 秒，且不跟随客户端取消 | `installCoreUpdate` 里已有的 `context.WithoutCancel` |
| 本地控制等待 | 安装与重装不设超时 | 清掉默认 10 秒，并且不加整次 RPC deadline |
| 其它本地控制请求 | 保持 10 秒 | 含 `GET /v1/core`、重启核心。订阅添加/刷新仍是 180 秒 |

setup 已存在核心的快速路径在 `Prepare` 之前返回，不进入 1 分钟阶段。

健康检查的宽限 5 秒和间隔 10 秒不改。1 分钟是外层上限。用户取消和更短的父 deadline 优先。

下载没有时钟：传输结束、连接失败或用户取消时才停。不要用一个很大的 duration 假装「不设超时」。

## 2. 文件

- 修改 `internal/core/install.go`：默认 HTTP 客户端 `Timeout` 为 0。更新旁边「下载仍用 15 分钟」的注释。`targetHTTPClient` 会复制该客户端，因此资产下载和重校验走同一份 0。重校验仍由 `checkTimeout` 的 8 秒 context 限制。
- 修改 `internal/control/client/runtime.go`：`runtimeRequestOptions` 增加 `noTimeout`。`InstallCore` 与 `ReinstallCore` 使用它。
- 修改 `internal/runtime/manager.go`：`Options.CoreInstallTimeout`；`Install` 在 `Prepare` 之后进入安装阶段。
- 修改 `internal/runtime/core_update.go`：安装阶段 helper；`Reinstall` 在 `Prepare` 之后使用它。
- 测试 `internal/control/client/core_install_timeout_test.go`。
- 测试 `internal/core/core_install_timeout_test.go`（`package core_test`，复用 `seamManager`）。
- 测试 `internal/core/download_timeout_test.go`：默认下载客户端没有 `Timeout`。

不改 `scripts/tools` 里构建机自己的 15 分钟客户端，也不改 Mihari 自更新和面板下载。

TUI 与 CLI 已把 `m.ctx` / `command.Context()` 直接交给客户端，没有另外的短 deadline。不要在页面或 CLI 上再包一层超时。

## 3. 任务

### Task 1: 安装与重装的本地控制调用不设超时

**Files:**

- Modify: `internal/control/client/runtime.go`
- Test: `internal/control/client/core_install_timeout_test.go`

**Interfaces:**

- Consumes: `doMutation` 与 `doRuntimeOutcome`。现在只有 `timeout > 0` 时才加 context deadline 并复制客户端（约 308–313 行与 579–584 行）。`timeout == 0` 表示保持默认 10 秒，不能用来表示「不设超时」。
- Produces: `runtimeRequestOptions.noTimeout`。为 true 时不调用 `context.WithTimeout`，并复制客户端后把副本的 `Timeout` 设为 0。父 context 原有的 deadline 和取消保留。

- [ ] **Step 1: 写失败测试**

仿 `TestSubscriptionTimeout_LongRequestsOutliveOrdinaryBudget`。共享客户端 `Timeout` 为 `30 * time.Millisecond`。表驱动覆盖 `InstallCore` 与 `ReinstallCore`，以及静态 token 和 credential provider。

传输在安装路径上堵住，直到同一客户端的 `Core` 请求因 30 毫秒预算失败，再检查安装请求的 `r.Context()`：父 context 没有 deadline 时，`Deadline()` 必须是 `ok == false`。然后返回 `{"schema":"mihari/v1","version":"v9","updated":true}`。安装调用必须成功。原客户端的 `Timeout` 仍是 30 毫秒。

`TestCoreInstallTimeout_ParentCancelWins`：父 context 在 30 毫秒后取消或到期，阻塞中的安装传输得到 `r.Context().Err()`，返回错误与父错误 `errors.Is` 成立。

- [ ] **Step 2: 确认失败原因**

```console
go test -run '^TestCoreInstallTimeout_' ./internal/control/client
```

预期：安装调用被共享客户端的 `Client.Timeout` 切断。

- [ ] **Step 3: 最小实现**

```go
type runtimeRequestOptions struct {
    timeout   time.Duration
    noTimeout bool
}
```

`doMutation`：仅当 `timeout > 0` 且 `noTimeout` 为 false 时包 `context.WithTimeout`。

`doRuntimeOutcome`：`noTimeout` 时复制客户端并设 `Timeout: 0`。否则保持现有 `timeout > 0` 分支。两个分支都不得修改原客户端。

`InstallCore` 与 `ReinstallCore` 传入 `runtimeRequestOptions{noTimeout: true}`。`RestartCore` 与订阅调用不改。

- [ ] **Step 4: 测试通过**

```console
go test -run '^TestCoreInstallTimeout_|^TestSubscriptionTimeout_' ./internal/control/client
```

- [ ] **Step 5: Commit**

```console
git add internal/control/client/runtime.go internal/control/client/core_install_timeout_test.go
git commit -m "fix: 核心安装的本地控制调用不再设超时"
```

### Task 2: 下载客户端不设超时，安装阶段 1 分钟

**Files:**

- Modify: `internal/core/install.go`（`httpClient`，约 404–416 行，以及第 45 行注释）
- Modify: `internal/runtime/manager.go`（`Options`、`New`、`Install` 约 670–758 行）
- Modify: `internal/runtime/core_update.go`（`Reinstall` 约 310–326 行）
- Test: `internal/core/download_timeout_test.go`
- Test: `internal/core/core_install_timeout_test.go`

**Interfaces:**

- Consumes: `Prepare` 的请求 context。`installCoreUpdate` 已有的 90 秒 `context.WithoutCancel` 补偿。
- Produces:
  - `Installer.httpClient` 在 `HTTPClient == nil` 时返回 `Timeout == 0` 的新客户端，并且不是 `http.DefaultClient`。
  - `runtime.CoreInstallPhaseTimeout = time.Minute`
  - `runtime.Options.CoreInstallTimeout`：正数覆盖阶段预算；零使用 `CoreInstallPhaseTimeout`
  - `(*Manager) beginCoreInstallPhase(ctx) (context.Context, context.CancelFunc)`

- [ ] **Step 1: 写失败测试**

`TestCoreDownloadClientHasNoTimeout`：`(Installer{}).httpClient()` 的 `Timeout` 是 0，且指针不等于 `http.DefaultClient`。调用两次，确认没有改到 `http.DefaultClient`。

`TestCoreInstallPhase_BudgetStartsAfterPrepare` 放在 `package core_test`，用 `seamManager`。

`holdingInstaller` 保存替换前的 `options.Installer`。`DetectVersion` 原样转给它。`Prepare` 记录 `ctx.Deadline()`，然后等待 `release` 或 `ctx.Done()`。

`deadlineSupervisor` 实现 `Run`、`Restart`、`Update`、`Reinstall`。后两个只记录 `ctx.Deadline()` 并返回 nil，不调用 work。

子测试 `install` 与 `reinstall` 把 `Options.CoreInstallTimeout` 设为 `80 * time.Millisecond`，装上 holder 和 supervisor。`started` 之后等待 `budget + 40*time.Millisecond`。此时 `Prepare` 的 context 必须仍未取消，并且没有被收成 `budget` 那么短的 deadline。再放开 `Prepare`。安装阶段记录到的 deadline 必须存在，`time.Until` 落在 `(budget/2, budget]`。`install` 只命中 `Update`，`reinstall` 只命中 `Reinstall`。

- [ ] **Step 2: 确认失败原因**

```console
go test -run '^TestCoreDownloadClientHasNoTimeout$|^TestCoreInstallPhase_BudgetStartsAfterPrepare$' ./internal/core
```

预期：默认客户端 `Timeout` 仍是 15 分钟；`Prepare` 被安装预算取消，或安装阶段没有独立 deadline。

- [ ] **Step 3: 最小实现**

```go
func (i Installer) httpClient() *http.Client {
    if i.HTTPClient != nil {
        return i.HTTPClient
    }
    return &http.Client{}
}
```

注释改为：检查最新版默认 8 秒；资产下载不设 `http.Client.Timeout`，只跟随请求 context。

```go
const CoreInstallPhaseTimeout = time.Minute

func (m *Manager) beginCoreInstallPhase(ctx context.Context) (context.Context, context.CancelFunc) {
    budget := m.coreInstallTimeout
    if budget <= 0 {
        budget = CoreInstallPhaseTimeout
    }
    return context.WithTimeout(ctx, budget)
}
```

`Options.CoreInstallTimeout` 的注释写明：只限制 `Prepare` 之后的安装阶段；零值使用 1 分钟。下载不使用这个字段。

`Install` 的 setup 快速路径和 `Prepare` 仍用原来的 `ctx`。`Prepare` 成功并收集 warning 之后创建 `phaseCtx`。`installCoreUpdate`、`commitWork`、`updateSettings`、`updateStateLocked` 和 `supervisor.Restart` 改用 `phaseCtx`。

`Reinstall` 的 `Prepare` 仍用原来的 `ctx`。成功并收集 warning 之后用 `phaseCtx` 调用 `installCoreUpdate`。

不要改 `installCoreUpdate` 内部 `context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)`。`WithoutCancel` 会去掉这 1 分钟，补偿仍有自己的 90 秒。

- [ ] **Step 4: 测试通过，并确认旧更新测试仍通过**

```console
go test -run '^TestCoreDownloadClientHasNoTimeout$|^TestCoreInstallPhase_BudgetStartsAfterPrepare$' ./internal/core
go test -run 'CoreUpdate|Reinstall' ./internal/core
```

- [ ] **Step 5: Commit**

```console
git add internal/core/install.go internal/core/download_timeout_test.go internal/core/core_install_timeout_test.go internal/runtime/manager.go internal/runtime/core_update.go
git commit -m "fix: 核心下载不设超时，安装阶段限时 1 分钟"
```

### Task 3: 入口核对与包级验证

- [ ] **Step 1: 核对没有更短的外层 deadline**

确认这些调用没有 `context.WithTimeout`：

- `internal/tui/pages/system/model.go` 的 `runAction`
- `internal/tui/pages/setup/model.go` 的 `installCore`（`beginExecution` 只 `WithCancel`）
- `internal/cli/core.go` 的 install / update / reinstall

发现较短 deadline 时删掉。不要在 TUI 上再加 1 分钟，否则下载会被算进安装时限。

- [ ] **Step 2: 验证**

```console
gofmt -l internal/core/install.go internal/core/download_timeout_test.go internal/core/core_install_timeout_test.go internal/control/client/runtime.go internal/control/client/core_install_timeout_test.go internal/runtime/manager.go internal/runtime/core_update.go
go test ./internal/control/client ./internal/runtime ./internal/core
go vet ./internal/control/client ./internal/runtime ./internal/core
```

`gofmt -l` 无输出。不声称未运行的 `go test ./...` 或 `-race` 已通过。

## 4. 自检

- 下载不设超时：默认 core 客户端 `Timeout == 0`；安装 RPC 在父 context 无 deadline 时自身也无 deadline，并能活过共享客户端的 10 秒。
- 下载不计入 1 分钟：阶段预算到期后 `Prepare` 的 context 仍活着。
- 安装阶段默认 1 分钟：`CoreInstallPhaseTimeout` 与测试里 `time.Until(deadline) <= budget`。
- 取消仍生效：父 context 测试。
- 补偿不被 1 分钟截断：不改现有 90 秒 `WithoutCancel` 补偿。
- 普通请求仍是 10 秒：`New` 与 `provider_client_unix.go` 不改；订阅测试仍通过。
