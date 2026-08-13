# Windows TUI 自更新进程交接修复设计

日期：2026-08-13
状态：已实施，待 Pull Request
关联 Issue：#55
目标分支：`fix/issue-55-tui-relaunch`

## 1. 问题

Windows 上从 TUI 完成 Mihari 自更新后，旧进程通过 `os.StartProcess` 启动新版 Mihari，并立即对新进程调用 `Release`。旧进程随后从 `tui.Run`、Cobra 根命令和 `main` 返回，使启动旧进程的外部 shell 认为前台命令已经结束并恢复提示符。

新进程仍继承旧进程的标准输入、标准输出和标准错误，并附着于同一控制台。因此外部 shell 与新版 TUI 同时读写同一控制台，造成界面叠加、输入争用以及 `Ctrl+C` 无法按预期退出当前会话。

Bubble Tea v2.0.8 的 `Program.Run` 在返回前会停止输入循环、停止 renderer 并恢复终端状态。问题不在 Bubble Tea 退出顺序，而在 Windows relaunch 启动新进程后过早结束旧前台进程。

Unix 使用 `syscall.Exec` 原位替换进程，外部 shell 始终等待同一进程身份，不受此问题影响。

## 2. 目标

- Windows 自更新后只允许新版 Mihari TUI 与控制台交互。
- 外部 shell 必须在新版 TUI 退出后才恢复提示符。
- 新版进程继续继承当前标准流、环境和默认 TUI 启动参数。
- 启动失败或等待失败时返回带操作上下文的错误。
- 不改变 `platform.Relaunch` 签名、CLI/JSON 契约、持久化格式或 Unix 行为。
- 保持 `CGO_ENABLED=0` 和现有 Windows amd64/arm64 支持。

## 3. 方案比较

### 3.1 采用：旧 Windows 进程等待新版进程

旧进程在 `os.StartProcess` 成功后调用 `Process.Wait`，直到新版 TUI 退出。等待期间旧进程不再运行 Bubble Tea，也不读取或写入控制台；它只保留外部 shell 正在等待的前台进程生命周期。新版进程独占继承的标准流。新版进程退出后，旧进程返回，外部 shell 才恢复提示符。

该方案只修改 Windows 平台适配器，直接修复错误的生命周期边界，且不需要 shell、脚本或新协议。

### 3.2 不采用：为新进程创建独立进程组

独立进程组可改变控制事件分发，但不会阻止外部 shell 在旧进程退出后恢复并与新版 TUI共享控制台。它不能解决核心的前台生命周期问题，还可能改变 `Ctrl+C` 语义。

### 3.3 不采用：通过 `cmd.exe` 或 PowerShell 包装启动

使用 shell 的 `start /wait` 或等价命令会引入额外的命令行转义、路径、环境和宿主差异，并扩大跨 shell 测试矩阵。Go 已提供所需的进程启动与等待能力，无需外部包装器。

## 4. 详细设计

### 4.1 Windows 平台适配器

`internal/platform/relaunch_windows.go` 保持现有输入校验和 `os.ProcAttr`：

- `Env` 原样传递调用方环境；
- `Files` 仍为 `os.Stdin`、`os.Stdout`、`os.Stderr`；
- 不设置 `CREATE_NEW_CONSOLE`、`DETACHED_PROCESS` 或新进程组标志。

启动成功后不再立即调用 `Process.Release`，而是同步等待新进程退出。正常退出（无论子进程退出码为何）表示进程交接已经完成；只有操作系统等待本身失败时，`Relaunch` 才返回 `wait for updated Mihari: ...` 错误。

平台文件定义不导出的最小进程接口：

```go
type replacementProcess interface {
	Wait() (*os.ProcessState, error)
	Release() error
}
```

包级启动函数返回该接口；生产默认实现调用 `os.StartProcess`，其 `*os.Process` 直接满足接口。测试返回受控 fake，而不手工构造内部句柄无效的 `*os.Process`。

测试通过 fake 证明：

1. 启动成功后一定等待同一个进程；
2. 等待完成前 `Relaunch` 不返回；
3. 等待错误得到包装并返回；
4. 正常路径不再走立即 `Release` 式的脱离路径；
5. 等待失败时 best-effort 调用 `Release` 清理仍可能持有的 Windows process handle。

若等待与清理都失败，使用 `errors.Join` 同时保留 `wait for updated Mihari` 与 `release updated Mihari after wait failure` 上下文；等待错误始终是主要失败原因。注入点只服务 Windows 平台边界测试，不形成导出 API。

### 4.2 TUI 与装配层

`internal/tui.Run` 保持 Bubble Tea 先退出的顺序：`Program.Run` 完成 shutdown 与终端恢复后，立即关闭旧 control session，再由 `finishRun` 调用 relaunch 回调。session cleanup 使用 `sync.Once` 包装，并保留 defer 作为 panic 或提前返回的兜底，避免旧进程在同步等待新版 TUI 期间继续持有 WebSocket streams、轮询 goroutine 和 daemon 活动。

`cmd/mihari` 继续向回调传入当前可执行文件、默认 TUI 参数和当前环境。由于 `platform.Relaunch` 在 Windows 上等待新版进程，`RunTUI`、Cobra 和旧 `main` 也会自然等待新版 TUI 生命周期结束。

不向 `context.Context` 添加新的等待取消逻辑。新版 TUI 进入 Bubble Tea raw mode 后，`Ctrl+C` 按既有 TUI 按键语义处理；在启动或退出过渡期，Windows 仍可能把控制事件广播到同一控制台的多个进程。即使旧进程的 signal context 被取消，无 context 的 `Process.Wait` 也不会因此提前返回或重新暴露外部 shell。

## 5. 错误处理

- 参数无效：保持现有 `relaunch Mihari: binary and arguments are required`。
- 新进程启动失败：保持现有 `start updated Mihari: ...`。
- 等待新进程失败：返回 `wait for updated Mihari: ...`。
- 新版 Mihari 自身的普通非零退出码不视为 Windows API 等待失败。旧进程随后保持当前的零退出行为，因此 Windows 外层进程不会传播新版进程的非零退出码。本修复只修复前台生命周期，不扩大范围实现与 Unix `exec` 相同的退出码传播；如需统一语义，应另行设计。
- 等待失败时尝试释放进程句柄；清理失败与等待失败一并返回，不覆盖原始等待错误。

## 6. 测试

### 6.1 Windows 单元测试

- 保留参数校验测试。
- 更新标准流继承测试，使伪启动返回实现最小接口的 fake，并断言该 fake 的 `Wait` 被调用。
- 用 `entered`、`unblock` 和结果 channel 握手，证明等待完成前 `Relaunch` 不会返回；不使用固定 `time.Sleep`。
- 注入等待错误并断言错误包含 `wait for updated Mihari`、可通过 `errors.Is` 找到原始错误，并调用一次 `Release`。
- 注入等待与清理双重错误，断言两个原始错误均可通过 `errors.Is` 找到。
- 所有替换包级启动函数的测试使用 `t.Cleanup` 恢复，不调用 `t.Parallel`。
- TUI 运行边界测试证明 control session cleanup 在可能阻塞的 relaunch 回调之前完成。

测试不得启动真实新 TUI、访问公网、修改系统服务或用户配置。

### 6.2 回归验证

按风险执行：

```console
go test ./internal/platform
go test ./internal/tui ./cmd/mihari
go test ./...
go test -race ./...
go vet ./...
gofmt -l cmd internal
```

执行 `CGO_ENABLED=0` 的 Windows amd64/arm64、Linux amd64 和 macOS arm64 编译检查，证明 Windows 专用注入点未泄漏到其他平台。

真实 TUI 自更新、真实 Windows 控制台 `Ctrl+C`、管理员权限、服务重启和真实 GitHub Release 下载不进入自动测试；它们仍属于需用户单独授权的 testenv 范围。CI 证明等待与调用链，不声称验证真实控制台交互。

## 7. 验收标准

- Windows 更新完成后，旧进程在新版 Mihari 退出前保持存活但不访问控制台。
- 外部 shell 在新版 TUI 退出前不会恢复提示符。
- 新版 TUI 继续使用同一控制台和标准流，`Ctrl+C`/`q` 可按既有 TUI 行为退出。
- 启动和等待错误具有稳定、无敏感信息的操作上下文。
- Unix relaunch、公开 CLI/JSON 契约、持久化语义和 daemon 边界不变。
- 本地测试、race、vet、格式化和受影响的无 CGO 跨平台构建通过。
