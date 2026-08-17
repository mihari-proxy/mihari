# Daemon 启动失败可观测性设计

日期：2026-08-17
状态：待审核
目标分支：`fix/daemon-startup-visibility`
工作目录：`.worktrees/fix-daemon-startup-visibility`

## 1. 背景

Windows 上 Mihari 作为 OS 服务运行时，用户会遇到「服务显示 running，CLI/TUI 却连不上 daemon」的组合。根因不是端口探测失效，而是启动失败被吞掉：

1. `internal/service.program.Start` 把 `run` 丢进 goroutine 后**立刻返回 `nil`**。SCM 因此把服务标成 running。
2. `cmd/mihari` 的 daemon body 在 `app.BuildRuntimeWithOptions` 或 `daemon.Run` 的 named pipe listen 处失败后，goroutine 结束，**进程并不退出**。服务壳还在，控制通道已经不存在。
3. `BuildRuntimeWithOptions` 在创建 `state.Store` **之前**做端口预检。失败时没有任何可查询状态。
4. TUI `session.EventReconnecting` 已经携带 `Err`，根模型只切到 `Reconnecting`，不展示原因。服务 running + daemon 不可达与服务 stopped 无法区分。

端口预检本身是有效的：`net.Listen` 失败即表示地址不可用。缺的是占用进程身份、失败后仍可查询的控制面，以及 SCM / TUI 对失败的可见性。

`daemon.Run` 已支持 `Runtime == nil`；`control/server.requireRuntime` 已对空 runtime 返回 `invalid_state`。降级控制面可以复用这两条现成路径，不必新开监听协议。

## 2. 目标

- 托管端口不可用时，错误 details 在可获得时带上占用 PID 与进程基名，并给出可读的清理说明（不自动杀进程）。
- `BuildRuntimeWithOptions` 或更早的 daemon body 失败时，只要控制通道能 listen，就启动**仅状态**的降级控制面：`GET /v1/status` 返回 `health=degraded` 与 `last_error`。
- 控制通道 listen 失败时，`program.Start` 向 SCM 返回错误，服务不得继续显示 running。
- TUI 在重连时展示净化后的连接原因；连上降级控制面时展示 `last_error`。
- CLI `mihari status` 在降级时打印 `Error:` 行；JSON 多一个可省略的 `last_error`。
- 保持 `CGO_ENABLED=0`，不改 `/v1` 既有字段语义，不让客户端读取 daemon 私有文件。

## 3. 非目标

- 不给 named pipe / Unix socket listen 加重试或备用通道名（原调查 P2）。
- 不把启动失败写入磁盘 sidecar。客户端仍只走控制协议；listen 失败时只能靠 SCM 状态 + 连接错误。
- 不在降级模式下提供设置修改、换端口或杀占用进程。恢复路径是看清占用者后停掉冲突进程或改设置，再重启服务。
- 不改变 mihomo supervisor 已有的 core `LastError` 行为。core 缺失或反复重启仍由 `Manager.Run` 处理，控制面保持在线。
- 不扩大控制 API 暴露面，不绑定 TCP 控制端口。
- 不把上游/系统错误原文或凭据、订阅 URL、controller secret 写进 `last_error`、日志或 TUI。

## 4. 方案比较

### 4.1 采用：listen 成功则降级驻留，listen 失败则向 SCM 报失败

启动拆成两段：

- **控制通道能 listen**：即使 runtime 装配失败，也 `daemon.Run` 且 `Runtime: nil`，进程保持为合法服务。TUI/CLI 能连上并读到 `health=degraded`。
- **控制通道不能 listen**：`Ready` 永不关闭，`Run` 返回错误，`program.Start` 把该错误交给 SCM。服务显示启动失败/stopped。TUI 只能显示本地服务状态 + 净化后的连接错误。

端口占用查询是 best-effort：预检仍然以 `net.Listen` 为准；查找失败时错误仍成立，只是没有 PID。

该方案复用已有 `Ready`、`Runtime == nil` 和 `requireRuntime`，不破坏「客户端只走控制协议」。

### 4.2 不采用：只改 SCM 上报，不提供降级控制面

`Start` 等待失败并退出，能修「假 running」，但用户仍然只能看到泛化的 `daemon is unavailable`。端口冲突的具体原因继续埋在 Windows 事件日志里。

### 4.3 不采用：客户端直接读 last-error 文件

listen 失败时文件也能读到，但违反 daemon 单写入者 / 本地控制协议边界，并制造第二份真相。本设计明确放弃这条路。

## 5. 详细设计

### 5.1 端口预检与占用者

预检仍在 `app.BuildRuntimeWithOptions` 内对 `mixed-addr`、`controller-addr`、`web-addr` 依次 `net.Listen` + 立即 `Close`。失败时返回现有稳定报文：

```text
code    = invalid_state
message = "managed port is unavailable"
```

`details` 增加可省略字段：

| 键 | 条件 | 内容 |
|---|---|---|
| `setting` | 已有 | `mixed-addr` / `controller-addr` / `web-addr` |
| `address` | 已有 | 侦听地址 |
| `pid` | 查到占用者 | 整数 PID |
| `process` | 查到映像名 | 仅基名，如 `clash.exe` |

占用查找放到 `internal/platform`：

```go
type TCPOccupant struct {
    PID     int
    Process string
}

func LookupTCPOccupant(address string) (TCPOccupant, bool)
```

平台文件：`tcp_occupant_windows.go`、`tcp_occupant_linux.go`、`tcp_occupant_darwin.go`。通用文件不得出现 `GOOS` 分支。

- Windows：`GetExtendedTcpTable`（`golang.org/x/sys/windows` 已是依赖）匹配 LISTEN 的本地地址，再用 `QueryFullProcessImageName` 取基名。
- Linux：读 `/proc/net/tcp` 与 `/proc/net/tcp6`，按 inode 扫 `/proc/*/fd`。
- Darwin：尽力而为；实现若无法在无 CGO、不 exec `lsof` 的前提下稳定取 PID，允许返回 `(TCPOccupant{}, false)`。预检本身仍然有效。

查找失败、权限不足或地址解析失败一律视为「无占用者」，不得覆盖 `net.Listen` 失败。禁止根据 PID 杀进程。

`app` 通过可注入的 `func(string) (platform.TCPOccupant, bool)` 调用查找，测试不依赖本机 TCP 表。

降级 `last_error` 文案由同一套 details 生成，例如：

```text
managed port mixed-addr 127.0.0.1:7890 is held by clash.exe (pid 4321)
managed port controller-addr 127.0.0.1:9090 is unavailable
```

### 5.2 状态模型与协议（仅加法）

`state.Snapshot` 增加：

```go
const (
    HealthOK       = "ok"
    HealthDegraded = "degraded"
)

type Snapshot struct {
    // 现有字段...
    LastError string
}
```

现有 `Health: "ok"` 字面量逐步改用常量，语义不变。`LastError` 只表示控制面级启动/驻留原因，与 `Config.LastError`、`Core.LastError`、订阅 `LastError` 分开。

`protocol.Status` 增加：

```go
LastError string `json:"last_error,omitempty"`
```

`GET /v1/status` 把 `snapshot.LastError` 原样映射过去。旧客户端忽略未知字段；旧 JSON 缺省该字段时解码为零值。这是加法，不需要新协议版本。

允许的 `health` 值：`ok`（现有）、`degraded`（本设计新增）。CLI 已按字符串打印 `Health`，无需新退出码。降级守护进程对 `mihari status` 仍是成功（进程在、协议通）。

### 5.3 降级控制面

`cmd/mihari` 的 daemon body 改为：

1. 准备目录与设置（失败则进入第 3 步）。
2. `BuildRuntimeWithOptions` 成功则 `daemon.Run` 传入完整 `Manager`。
3. 装配失败则构造 `Health=degraded` 的内存 store，`daemon.Run` 传入 `Runtime: nil`。
4. 两种路径都把同一个 `Ready` channel 传给 `daemon.Run`。`Listen` 成功后关闭 `Ready`（现有行为）。
5. `Listen` 失败则直接返回错误，不驻留。

`LastError` 只使用已净化文案：优先 `APIError.Message` 加上允许的 details；未知错误映射为 `daemon startup failed`，不得包含路径中的 token、secret 或完整订阅 URL。

`Runtime == nil` 时：

- `GET /v1/status` 正常返回，`capabilities` 为空。
- 其余 runtime 路由走现有 `requireRuntime`（`invalid_state` / `mihomo runtime is unavailable`）。
- TUI `poll` 因能力列表为空而跳过资源拉取。
- TUI 流会失败并按现有逻辑重试，但 `Status` 成功，会话保持 Connected，不把降级误判成断线。

不在降级模式下载、解压、写配置或启动 mihomo / Web 网关。

### 5.4 服务 Start 与 SCM

保持 `service.RunFunc` 签名不变，避免测试里大量 `NewController` 假实现改签。

`service.Options` 增加：

```go
Ready        <-chan struct{}
StartTimeout time.Duration // 零值 = 15s
```

`New` 在默认 `NewController` 闭包里把 `Ready` / `StartTimeout` 注入 `program`。测试若自备 `NewController`，行为与现在相同（`Start` 立即返回 nil）。

`program.Start`：

1. 启动 `run` goroutine。
2. 若 `Ready == nil`，保持当前行为并立即返回 nil（兼容测试）。
3. 否则等待：`Ready` 关闭 → 返回 nil；`run` 先结束 → 返回其错误；超过 `StartTimeout` → 取消并返回 `invalid_state` / `service did not become ready`。
4. `run` 在已经 Ready 之后失败：仍写事件日志。不在本次把已成功的 Start 改写成失败；是否把事后崩溃升级为自动停服务不在范围。本次要修的是「从未 listen 却显示 running」。

生产路径：`main` 创建 `ready := make(chan struct{})`，同时交给 `daemon.Run` 与 `service.New`。交互式前台 `runDaemonBody` 不经过 `program.Start`，`Ready` 可闲置。

### 5.5 TUI

继续只用 `internal/control/client`。服务 badge 仍来自本地 `service.Manager`。

重连（未连上控制面）：

- 保存净化后的 `Event.Err`。
- 页脚在现有 `Daemon disconnected — actions paused` 后追加 ` — ` + 短原因。
- 静态映射：named pipe / Unix socket 不存在或 connection refused → `Daemon is not listening`；permission / access denied → `Permission denied connecting to daemon`；其余 → `Daemon connection failed`。
- 若本地服务状态为 `running`，再追加 `Service is running but the control plane is not reachable`。
- `Event.Err == nil` 时文案与现在完全一致，避免无故改 golden。

已连接且 `health=degraded`：

- 页脚显示 `Daemon degraded` + `last_error`（来自 `EventStatus`，daemon 已净化）。
- 右上角仍为 `Service running · Connected`，因为控制通道是通的。
- 能力页保持 Unavailable；用户应先看页脚原因。

`EventConnected` 且 `health=ok` 时清掉提示。

### 5.6 CLI

`mihari status` 文本在 `Health` 之后、已有 Config 行之前，若 `LastError != ""` 增加一行：

```text
Error: managed port mixed-addr 127.0.0.1:7890 is held by clash.exe (pid 4321)
```

`--json` 通过 DTO 字段自动带上 `last_error`。连不上时仍是 `daemon_unavailable`，不读任何本地错误文件。退出码不变。

## 6. 用户界面

重连且服务假 running：

```text
Service running · Reconnecting
...
Daemon disconnected — actions paused — Daemon is not listening — Service is running but the control plane is not reachable
```

连上降级控制面：

```text
Service running · Connected
...
Daemon degraded — managed port mixed-addr 127.0.0.1:7890 is held by clash.exe (pid 4321)
```

CLI：

```text
Daemon: v0.7.1
Health: degraded
Error: managed port mixed-addr 127.0.0.1:7890 is held by clash.exe (pid 4321)
Revision: 0
Started: 2026-08-17T02:00:00Z
```

## 7. 测试计划

全部使用 `t.TempDir` / `httptest` / 注入 lookup 与 fake service，不访问公网、不读真实用户目录、不要求已安装的 mihomo。

| 包 | 行为 |
|---|---|
| `internal/state` | `LastError` 随 Store 往返；常量 `HealthDegraded` |
| `internal/control/protocol` | `last_error` 加法编解码；旧 JSON 仍能解码 |
| `internal/control/server` | snapshot `LastError` 出现在 `GET /v1/status`；`Runtime==nil` 时 status 200、core 为 invalid_state |
| `internal/platform` | 本进程 `Listen` 后 lookup 在 Windows/Linux 上命中自身 PID；查找失败返回 false |
| `internal/app` | 占用地址返回 `managed port is unavailable`；注入 occupant 后 details 含 pid/process；无 occupant 时只有 setting/address |
| `internal/app` 或 `internal/daemon` | 装配失败可得到 degraded store 文案，且不含 secret |
| `internal/service` | `Ready==nil` 时 Start 立即成功；Ready 先关闭则 Start 返回 nil；run 在 Ready 前失败则 Start 返回该错误；超时则取消 |
| `internal/tui` | 重连带 Err 时页脚含净化原因；无 Err 时文案不变；`health=degraded` 时页脚含 `last_error` |
| `internal/cli` | 文本多 `Error:` 行；空 `LastError` 时输出与现在一致 |
| `internal/daemon` | 现有 Ready 测试保持；必要时覆盖 Runtime nil 时仍关闭 Ready |
| `internal/integration` | 仅当单包测不够证明「装配失败仍 listen 且 status 可读」时再补一条 |

## 8. 风险

- **SCM 超时**：`Start` 改为等待 Ready。装配含本地 `mihomo -v`，应远小于 15s/30s。超时路径必须取消 `run`，避免孤儿 goroutine。
- **假阳性占用者**：TCP 表与 `Listen` 之间有竞态。PID 仅作提示，以 `Listen` 结果为准。
- **Darwin 占用者可能缺失**：可接受；地址仍足够定位。
- **TUI 流在降级下重试**：现有 supervise 在 Status 成功时不把会话打成断线。页脚展示 `last_error` 即可，不在本次改流生命周期。
- **协议加法**：`last_error` 与 `health=degraded` 必须有编解码测试，证明旧 JSON 仍能解码。
- **工作区**：实现必须在 `fix/daemon-startup-visibility` worktree 中进行，禁止改主工作区的 `main`。

## 9. 文档与发布

实现阶段同步 `docs/architecture.md` 控制面一节：降级驻留、`health`/`last_error`、SCM 等待 Ready。不改 README 命令列表。用户可见的 `status` 多一行时，若 `docs/commands.md` 有示例则一并更新。
