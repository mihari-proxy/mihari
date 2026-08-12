# TUI Mihari 自更新设计

日期：2026-08-12
状态：已实现并完成本地验证，待 Pull Request
目标分支：`feat/tui-self-update`

## 1. 背景

Mihari 已通过 `mihari self update` 提供基于 GitHub Releases 的自更新能力，但 TUI 的 System 页目前只支持更新和重启 mihomo Core，没有更新 Mihari 自身的入口。

本设计在 TUI System 页增加 Mihari 版本检查与更新操作。更新成功后，旧 TUI 必须完整恢复终端，再从已替换的新二进制自动进入新版本 TUI。用户不需要手动重新运行命令。

## 2. 目标

- 在 System 页提供清晰、可聚焦的 `Update Mihari` 操作行。
- 进入 System 页时异步检查 GitHub Releases，并显示当前版本与最新状态。
- 复用 System 页现有异步操作的 Pending、Done、Failed chip 和 spinner 渲染。
- 在用户确认且当前进程具备管理员或 root 权限时更新 Mihari。
- 更新成功后重启已安装的 Mihari 服务，并自动进入磁盘上的新版本 TUI。
- 保持现有 CLI 自更新行为兼容。
- 不修改稳定的 `/v1` 控制协议、持久化格式或 daemon 单写入者边界。

## 3. 非目标

- 不新增 UAC、`sudo`、`pkexec` 或其他自动提权流程。
- 不让 daemon 负责替换 Mihari 自身二进制。
- 不新增自动定时检查；仅在 System 页加载或用户手动重试时检查。
- 不增加后台常驻更新任务、更新通知中心或版本忽略策略。
- 不修改 mihomo Core 更新行为。
- 不重构与本功能无关的 TUI、CLI、服务或更新代码。

## 4. 用户界面

### 4.1 位置

在 System 页的 `Daemon` section 中增加独立操作行 `Update Mihari`。该行放在 daemon 与 endpoint 信息之后、`Run Setup` 之前，并沿用 System 页现有上下移动、Enter 操作和焦点样式。

```text
┌─ Daemon ──────────────────────────────────────────────┐
│   Daemon          ● Healthy  v0.3.1                   │
│   Proxy Endpoint  127.0.0.1:7890                      │
│   Mihomo Core API 127.0.0.1:9090                      │
│                                                      │
│ > Update Mihari   v0.3.1 · v0.4.0 available          │
│   Run Setup                                          │
└──────────────────────────────────────────────────────┘
```

`·` 是普通中点分隔符，不是状态点，也不承载颜色语义。

### 4.2 稳定状态

版本检查成功后，值区域使用普通行内容：

```text
有新版    v0.3.1 · v0.4.0 available
最新版    v0.3.1 · Up to date
```

当前版本取自 `internal/buildinfo.Version`。最新版本使用 GitHub Release 的 tag，保留其规范显示形式。

### 4.3 异步生命周期状态

版本检查和实际更新复用 System 页现有的行级异步状态槽，而不是将 `Checking`、`Updating` 或 `Failed` 作为普通文本拼接到稳定版本值中。

```text
检查中    Update Mihari   [⠋ Checking]
检查失败  Update Mihari   [ Failed ]  fetch release failed
更新中    Update Mihari   [⠋ Updating]
更新成功  Update Mihari   [ Done ]
更新失败  Update Mihari   [ Failed ]  replace binary failed
```

- `Checking`、`Updating` 使用现有黄色 Pending chip 和动态 braille spinner。
- `Done` 使用现有绿色 Done chip。
- `Failed` 使用现有红色 Failed chip。
- 检查成功后直接进入 `available` 或 `Up to date` 稳定状态，不短暂显示 Done。
- 失败时，行尾继续显示现有长度限制下的错误摘要；完整且已清理的错误沿用 System 页顶部错误区域显示。
- 状态渲染不得增加行高或另建提示行。

### 4.4 确认框

仅在检查结果表明有新版时，按 Enter 打开现有风格的确认框。确认框应明确显示：

- 当前版本与目标版本；
- 将替换当前 Mihari 可执行文件；
- 已安装的 Mihari 服务会在替换后重启；
- 当前 TUI 会退出并自动进入新版本。

默认焦点保持在取消选项，与现有破坏性或高影响操作一致。

## 5. 交互与状态机

### 5.1 自动检查

首次进入或重新进入 System 页时，如果没有正在进行的检查或更新，则启动一次版本检查：

```text
System 页加载
  → Checking
  ├─ 有新版：current · latest available
  ├─ 最新版：current · Up to date
  └─ 失败：Failed
```

检查是只读网络操作，不要求管理员或 root 权限。重复的页面刷新不得并发启动多个版本检查；迟到的旧结果不得覆盖较新的检查或更新状态。

### 5.2 Enter 行为

- `available`：打开更新确认框。
- `Up to date`：重新检查。
- 检查 `Failed`：重新检查。
- `Checking` 或 `Updating`：忽略重复 Enter。
- 确认框取消：关闭确认框，不执行写操作。

### 5.3 更新流程

确认后按以下顺序执行：

```text
确认更新
  → 检查管理员/root 权限
  ├─ 权限不足：Failed，保留当前 TUI
  └─ 权限充足
       → Updating
       → 重新查询最新 Release
       → 选择当前 OS/架构资产
       → 下载到同目录 staging 区
       → 执行大小限制与现有校验
       → 替换 Mihari 二进制
       → 尝试重启已安装的 Mihari 服务
       → 发送 relaunch 请求
       → 旧 Bubble Tea 完整退出并恢复终端
       → 从磁盘启动新二进制
       → 自动进入新版本 TUI
```

执行更新时必须重新查询最新 Release，不能直接把页面早先检查到的 tag 当作提交依据。这样可以避免检查与更新之间 Release 状态变化造成陈旧结果。

## 6. 架构与组件边界

### 6.1 `internal/update`

在现有 `SelfUpdater` 上增加只读版本检查能力。建议返回明确类型：

```go
type CheckResult struct {
    Current   string
    Latest    string
    Available bool
}

func (u SelfUpdater) Check(ctx context.Context, currentVersion string) (CheckResult, error)
```

`Check` 复用 Release 查询和 tag 比较逻辑，但不下载资产、不创建 staging 目录、不检查提权，也不修改文件。

`Update` 继续负责：

- 查询最新 Release；
- 选择当前 GOOS/GOARCH 对应资产；
- 限制 Release 响应和二进制大小；
- 下载候选二进制；
- 设置必要权限；
- 调用平台专用二进制替换；
- 调用现有 `AfterReplace` 服务重启回调。

检查与更新共享 Release 获取逻辑，避免形成两套 GitHub API 解析与错误分类。

### 6.2 `internal/tui/pages/system`

System 页通过使用方附近的最小接口依赖自更新能力：

```go
type SelfUpdater interface {
    Check(context.Context, string) (update.CheckResult, error)
    Update(context.Context, string, string) (update.Result, error)
}
```

页面新增独立的 Mihari 更新状态，至少区分：未检查、检查中、有新版、已是最新版、检查失败、更新中、更新失败和更新成功。它负责：

- 生成检查与更新命令；
- 维护检查 generation 或等价身份，拒绝迟到结果；
- 构建 `Update Mihari` 行；
- 复用现有 pending/outcome chip 渲染；
- 在更新前调用提权检查；
- 发出类型化 relaunch 请求。

页面不负责启动新进程，也不直接操作 daemon 控制协议。

当前 System 页的全局 `pending` 只允许一个行级操作并发。本功能遵守这一约束：检查或更新进行中时，不启动其他 Mihari 自更新操作；不得破坏既有 Core、service、system proxy 和 TUN 行的 pending/outcome 行为。

### 6.3 TUI 根模型

System 页更新成功后向根模型发送类型化 `RelaunchRequestMsg`。该消息可携带已经过 System 页现有错误清理逻辑处理的 warning 文本，用于表示“二进制已替换但服务重启失败”。根模型记录需要重新进入 TUI 及 warning，并返回 `tea.Quit`。

根模型不得在 Bubble Tea 的 `Update` 内直接启动新进程。必须等 `program.Run()` 返回，使 Alt Screen、光标、输入模式和终端状态完全恢复后再进行进程交接。

### 6.4 `internal/tui.Run` 与调用边界

TUI 运行边界通过根模型上的类型化 relaunch 标志区分“正常退出”和“请求重新进入”。`tui.Options` 注入一个无参数 `Relaunch func() error` 回调；该回调已经由装配层绑定好可执行文件路径、进入 TUI 所需的参数、标准流和环境。

`tui.Run` 保持现有 `func Run(context.Context, Options) error` 签名。它在 `program.Run()` 返回后检查最终根模型的类型化 relaunch 标志：只有标志为真时才调用 `Options.Relaunch`。如果同时存在 warning，则先在已恢复的终端输出安全 warning，再调用 relaunch。这样既保证终端已经恢复，也避免把 relaunch 请求编码成文本错误或改变 CLI 的通用 `RunTUI` 契约。

`cmd/mihari` 继续负责依赖装配：

- 将现有 `update.SelfUpdater` 注入 TUI；
- 提供已经绑定当前可执行文件路径与 TUI 启动参数的 relaunch 回调；
- relaunch 回调只在 TUI 完成退出后由 `tui.Run` 调用。

普通的 `q`、Ctrl+C 或 context 取消仍是正常退出，不触发重新进入。

### 6.5 平台进程交接

平台差异通过小接口以及 `_unix.go`、`_windows.go` 文件隔离：

- Unix：优先使用进程替换，使新二进制接管当前进程和终端。
- Windows：启动附着于当前控制台、使用相同标准输入输出的新进程，然后旧进程退出；不得打开额外控制台窗口。

重新进入只保留进入 TUI 所需的原始命令形式和适用参数，不携带 `self update`、`daemon` 或其他会改变命令语义的参数。环境继承当前进程环境，包括用户显式提供的 `MIHARI_DATA` 和控制端点覆盖。

## 7. 权限与安全

- 自动版本检查不要求提权。
- 真正更新前调用现有 `elevate.RequireElevated` 或等价注入检查。
- 权限不足时不调用 updater，不退出 TUI，不触发 UAC 或 sudo。
- 提示用户从管理员/root 终端重新启动 Mihari。
- GitHub 响应、下载和文件大小继续使用现有限制。
- 错误、日志和测试失败信息不得包含凭据、controller secret、订阅 token、完整敏感 URL 或本地敏感配置。
- 本功能不新增网络监听，不扩大 controller 或 Web gateway 暴露面。
- 本功能不修改 daemon 管理的持久化文件，因此不违反 daemon 单写入者约束。

## 8. 错误与部分成功

### 8.1 检查失败

进入 `Failed`，保留当前 TUI。按 Enter 重新检查。错误仅影响 `Update Mihari` 行，不得让 System 页其他状态加载失败。

### 8.2 权限不足

进入 `Failed`，显示需要管理员/root 权限的明确提示。不得调用下载或替换逻辑。

### 8.3 更新前失败

Release 查询、资产不匹配、下载、大小限制、候选文件准备或替换失败时进入 `Failed`，保留当前 TUI。旧二进制仍应保持可运行；现有平台替换回滚语义保持不变。

### 8.4 二进制已替换但服务重启失败

`update.Result.Updated` 为 true 但 `AfterReplace` 返回错误时，视为“更新已提交、服务重启部分失败”：

- 不回退到旧 TUI；
- 记录并向控制台保留经过清理的服务重启错误；
- 仍执行 relaunch，进入已安装的新版本 TUI；
- 新 TUI 通过现有 daemon 连接状态呈现服务暂不可用。

这一区分必须基于类型化结果，而不是匹配错误文本。

### 8.5 新进程启动失败

旧 Bubble Tea 已经退出并恢复终端。将经过清理的启动错误写到控制台，并使当前命令返回非零退出状态。不得尝试在已结束的 Bubble Tea model 中恢复 UI。

## 9. 并发与生命周期

- 检查和更新命令传播 TUI 的 context，不保存新 context 到长期结构。
- 所有网络请求继承现有超时和取消语义。
- 同一时间最多存在一个 Mihari 检查或更新操作。
- 使用 generation、operation ID 或等价身份拒绝迟到的检查结果。
- spinner 的启动、tick 和停止继续由 System 页生命周期拥有；完成、失败、离页或退出后不得遗留 goroutine 或 tick 循环。
- relaunch 只能发生一次；重复消息或退出路径不得启动多个新进程。

## 10. 测试策略

行为变更采用 Red–Green–Refactor。

### 10.1 `internal/update` 单元测试

- `Check` 在 tag 相同时返回 `Available=false`。
- `Check` 在 tag 不同时返回当前和最新版本。
- `Check` 对 GitHub 非 200、过大响应和无效 JSON 保持现有类型化错误。
- `Check` 不下载资产、不创建 staging 文件。
- `Update` 现有测试保持通过。
- CLI `mihari self update` 行为与 JSON 契约保持兼容。

### 10.2 System 页单元测试

- 页面加载启动检查并渲染 `Checking` Pending chip 与 spinner。
- 有新版渲染 `v0.3.1 · v0.4.0 available`。
- 最新版渲染 `v0.3.1 · Up to date`。
- 检查失败渲染 Failed chip 和安全错误摘要。
- 失败与最新版状态下按 Enter 会重新检查。
- 有新版时按 Enter 打开确认框；取消不调用 updater。
- 权限不足不调用 updater、不产生 relaunch 请求。
- 更新期间渲染 `Updating` Pending chip。
- 更新成功渲染 Done 并产生一次 relaunch 请求。
- 更新失败渲染 Failed 并保留当前页面。
- 迟到的检查结果不会覆盖更新中或更新后的状态。
- Mihari 更新状态不会错误覆盖其他 System 行的 pending/outcome。

### 10.3 TUI 根模型与运行测试

- relaunch 请求使 Bubble Tea 退出。
- 正常退出不触发 relaunch。
- `program.Run()` 返回后才调用 relaunch 适配器。
- relaunch 只执行一次并使用进入 TUI 所需的参数。
- relaunch 失败会返回错误并保持终端恢复。

### 10.4 平台与回归验证

按风险逐步执行：

```console
go test ./internal/update
go test ./internal/tui/pages/system
go test ./internal/tui
go test ./internal/cli
go test ./...
go test -race ./...
go vet ./...
gofmt -l cmd internal
```

平台交接代码至少执行当前平台测试，并保持 `CGO_ENABLED=0` 的 Windows、Linux、macOS 受影响目标编译检查。无法在当前平台真实验证的进程交接必须通过注入 fake 和跨平台编译覆盖，并在交付时明确列出。

真实 GitHub Release 下载、真实系统服务重启和真实提权环境不进入默认测试；如需真实环境验证，必须由用户另行明确授权。

## 11. 文档影响

实现完成后更新：

- `README.md` 与 `README.zh-CN.md` 的 TUI 能力说明；
- `docs/architecture.md` 的 System 页本地生命周期操作说明；
- 相关命令或帮助文本（仅当用户可见行为实际变化）。

现有 `mihari self update` 命令文档继续保留。

## 12. 验收标准

- System 页存在可聚焦的 `Update Mihari` 操作行。
- 进入页面后自动检查版本，并正确显示稳定状态或生命周期 chip。
- 普通中点 `·` 的版本文案与已确认格式完全一致。
- 有新版时可确认更新；无新版或失败时 Enter 可重试检查。
- 权限不足不会下载、替换、退出或自动提权。
- 更新成功后旧 TUI 完整恢复终端，随后自动进入磁盘上的新版本 TUI。
- daemon `/v1` DTO、错误码、JSON envelope、CLI 退出码和持久化格式均未改变。
- 现有 CLI 自更新、mihomo Core 更新和其他 System 行行为无回归。
- 所有新增或修改行为具有对应测试，并完成与风险相称的跨平台验证。

## 13. 已选方案与否决方案

采用“向 TUI 注入本地自更新器，并在 Bubble Tea 退出后进行平台进程交接”的方案。

未采用：

1. **TUI 启动 `mihari self update` 子进程**：需要解析子进程结果，终端所有权与进程交接更复杂，也会重复 CLI 表现层逻辑。
2. **由 daemon 执行自更新**：需要新增稳定控制协议、协调 daemon 更新自身及 IPC 断线，范围和风险远超当前需求。

选定方案最大程度复用现有更新实现与 System 页交互模式，同时避免扩展稳定协议。
