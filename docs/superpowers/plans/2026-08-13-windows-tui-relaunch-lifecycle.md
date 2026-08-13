# Windows TUI Relaunch Lifecycle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复 Windows TUI 自更新后旧 Mihari 提前退出、导致新版 TUI 与外部 shell 争用同一控制台的问题。

**Architecture:** 保持 Bubble Tea 先退出并恢复终端的现有顺序，只在 Windows 平台适配器中把“启动后立即 Release”改为“启动后同步 Wait”。用不导出的最小进程接口隔离 `os.Process`，使单元测试能精确验证等待、标准流继承和等待失败后的句柄清理；Unix `syscall.Exec` 路径不变。

**Execution Status:** Tasks 1-3 and Task 4 local verification/review are complete. Commit, PR creation, and Actions monitoring remain pending until their corresponding steps execute.

**Tech Stack:** Go 1.26、标准库 `os`/`errors`、Windows build tags、Go testing

**Spec:** `docs/superpowers/specs/2026-08-13-windows-tui-relaunch-lifecycle-design.md`

## Global Constraints

- 不改变 `platform.Relaunch(binary string, args, env []string) error` 签名。
- 不改变 CLI/JSON 契约、持久化格式、daemon 边界或 Unix relaunch 行为。
- 不新增依赖，不启动真实新 TUI，不访问公网，不修改系统服务或用户配置。
- 保持 `CGO_ENABLED=0`，Windows amd64/arm64、Linux amd64、macOS arm64 可构建。
- 所有测试使用 channel 握手而不是固定 `time.Sleep`；替换包级 seam 的测试不得 `t.Parallel`。
- 当前修复不传播新版 Windows 子进程的非零退出码。

---

### Task 1: 行为不变地建立可测试进程边界

**Files:**
- Modify: `internal/platform/relaunch_windows.go`
- Modify: `internal/platform/relaunch_windows_test.go`

**Interfaces:**
- Consumes: 当前 `Relaunch` 与 `os.StartProcess`。
- Produces: 私有 `replacementProcess` 接口和返回该接口的 `startProcess` seam，仍保持旧行为 `Wait=0, Release=1`。

- [x] **Step 1: 增加行为不变的生产 seam**

```go
type replacementProcess interface {
	Wait() (*os.ProcessState, error)
	Release() error
}

var startProcess = func(name string, argv []string, attr *os.ProcAttr) (replacementProcess, error) {
	return os.StartProcess(name, argv, attr)
}
```

此步骤只改变 seam 类型；`Relaunch` 仍在启动成功后立即调用 `Release`。

- [x] **Step 2: 用 fake 适配既有测试并锁定旧行为**

增加实现上述接口的 fake，将既有启动测试改为返回 fake，并断言 `waitCalls == 0`、`releaseCalls == 1`。保留 binary、args、env 和标准流断言。替换包级 seam 时用 `t.Cleanup` 恢复，且不使用 `t.Parallel`。

- [x] **Step 3: 格式化并确认准备重构保持 GREEN**

```console
gofmt -w internal/platform/relaunch_windows.go internal/platform/relaunch_windows_test.go
go test -run '^TestRelaunch' ./internal/platform
```

Expected: PASS。不得把编译错误当作 Red。

---

### Task 2: 用失败测试锁定 Windows replacement 生命周期

**Files:**
- Modify: `internal/platform/relaunch_windows_test.go`

**Interfaces:**
- Consumes: Task 1 的 `replacementProcess` 与包级 `startProcess` seam。
- Produces: 对私有 `replacementProcess` 接口的测试约束：`Wait() (*os.ProcessState, error)`、`Release() error`。

- [x] **Step 1: 将测试启动 seam 改为返回可控 fake process**

在测试文件中增加：

```go
type fakeReplacementProcess struct {
	waitEntered  chan struct{}
	waitUnblock  chan struct{}
	waitErr      error
	releaseErr   error
	waitCalls    int
	releaseCalls int
}

func (process *fakeReplacementProcess) Wait() (*os.ProcessState, error) {
	process.waitCalls++
	if process.waitEntered != nil {
		close(process.waitEntered)
	}
	if process.waitUnblock != nil {
		<-process.waitUnblock
	}
	return nil, process.waitErr
}

func (process *fakeReplacementProcess) Release() error {
	process.releaseCalls++
	return process.releaseErr
}
```

每个替换 `startProcess` 的测试保存旧值并用 `t.Cleanup` 恢复。

- [x] **Step 2: 写等待成功路径的回归测试**

把现有 `TestRelaunchStartsWithConsoleInheritedFiles` 改为让 `startProcess` 捕获 `name`、`argv`、`ProcAttr` 并返回 fake。断言 `Relaunch` 返回 nil、`waitCalls == 1`、`releaseCalls == 0`，并保留 binary、args、env 和 stdin/stdout/stderr 的现有断言。

- [x] **Step 3: 写阻塞语义回归测试**

新增 `TestRelaunchWaitsForReplacementBeforeReturning`：在 goroutine 中调用 `Relaunch`，fake `Wait` 关闭 `entered` 后阻塞在 `unblock`。等待 `entered` 本身必须有 timeout，使旧实现以明确断言失败而不是永久挂起：

```go
select {
case <-entered:
case err := <-result:
	t.Fatalf("Relaunch returned without waiting for replacement: %v", err)
case <-time.After(time.Second):
	t.Fatal("Relaunch did not call replacement Wait")
}
```

随后执行非阻塞断言：

```go
select {
case err := <-result:
	t.Fatalf("Relaunch returned before replacement exited: %v", err)
default:
}
close(unblock)
select {
case err := <-result:
	if err != nil {
		t.Fatal(err)
	}
case <-time.After(time.Second):
	t.Fatal("Relaunch did not return after replacement exited")
}
```

timeout 只防止测试永久挂起，不用于同步行为。

- [x] **Step 4: 写等待错误与清理错误测试**

新增：

```go
func TestRelaunchReleasesProcessAfterWaitFailure(t *testing.T)
func TestRelaunchPreservesWaitAndReleaseErrors(t *testing.T)
```

第一个 fake 返回 `waitErr`，断言错误含 `wait for updated Mihari`、`errors.Is(err, waitErr)` 且 `releaseCalls == 1`。第二个同时返回 `waitErr` 与 `releaseErr`，断言 `errors.Is` 可找到两者，错误文本分别包含等待与等待失败后释放的操作上下文。

- [x] **Step 5: 运行最小测试并确认 RED**

```console
go test -run '^TestRelaunch' ./internal/platform
```

Expected: 测试编译成功，但生命周期断言失败，因为 `Relaunch` 仍立即 `Release` 而不等待。不得接受编译错误或测试自身 hang。

---

### Task 3: 实现最小 Windows 等待与错误清理

**Files:**
- Modify: `internal/platform/relaunch_windows.go`
- Test: `internal/platform/relaunch_windows_test.go`

**Interfaces:**
- Consumes: Task 1 定义的测试期望。
- Produces: 不导出的 `replacementProcess` 接口；`startProcess func(string, []string, *os.ProcAttr) (replacementProcess, error)`；公开 `Relaunch` 签名保持不变。

- [x] **Step 1: 用 Wait 替代成功后的立即 Release**

启动参数与 `ProcAttr` 保持原样。启动成功后实现：

```go
if _, err := process.Wait(); err != nil {
	waitErr := fmt.Errorf("wait for updated Mihari: %w", err)
	if releaseErr := process.Release(); releaseErr != nil {
		return errors.Join(waitErr, fmt.Errorf("release updated Mihari after wait failure: %w", releaseErr))
	}
	return waitErr
}
return nil
```

导入标准库 `errors`。成功路径不再调用 `Release`，因为 `Wait` 已释放资源。

- [x] **Step 2: 格式化修改文件**

```console
gofmt -w internal/platform/relaunch_windows.go internal/platform/relaunch_windows_test.go
```

- [x] **Step 3: 运行最小测试并确认 GREEN**

```console
go test -run '^TestRelaunch' ./internal/platform
```

Expected: PASS。

- [x] **Step 4: 运行直接相关回归测试**

```console
go test ./internal/platform ./internal/tui ./cmd/mihari
```

Expected: PASS，证明平台修复未改变 TUI relaunch 请求顺序和默认启动参数。

---

### Task 4: 完整验证、审查与交付

**Files:**
- Verify: `internal/platform/relaunch_windows.go`
- Verify: `internal/platform/relaunch_windows_test.go`
- Verify: `docs/superpowers/specs/2026-08-13-windows-tui-relaunch-lifecycle-design.md`
- Verify: `docs/superpowers/plans/2026-08-13-windows-tui-relaunch-lifecycle.md`

**Interfaces:**
- Consumes: Task 2 的 Windows relaunch 行为。
- Produces: 可提交、可跨平台构建并可由 CI 验证的 issue #55 修复。

- [x] **Step 1: 运行全仓测试**

```console
go test ./...
```

Expected: PASS。

- [x] **Step 2: 运行 race、静态检查和格式检查**

```console
go test -race ./...
go vet ./...
gofmt -l cmd internal
```

Expected: 前两条 PASS；`gofmt -l` 无输出。

- [x] **Step 3: 执行无 CGO 跨平台编译**

输出到经过显式解析的系统临时子目录，完成后只删除该子目录：

```powershell
$previousCGO = $env:CGO_ENABLED
$previousGOOS = $env:GOOS
$previousGOARCH = $env:GOARCH
$tempRoot = (Resolve-Path -LiteralPath ([System.IO.Path]::GetTempPath())).Path
$buildDir = Join-Path $tempRoot ('mihari-issue55-' + [guid]::NewGuid())
New-Item -ItemType Directory -Path $buildDir | Out-Null
$resolvedBuildDir = (Resolve-Path -LiteralPath $buildDir).Path
if ([System.IO.Path]::GetDirectoryName($resolvedBuildDir) -ne $tempRoot) { throw 'unexpected build directory parent' }
try {
  $env:CGO_ENABLED='0'
  $env:GOOS='windows'; $env:GOARCH='amd64'; go build -o (Join-Path $resolvedBuildDir 'mihari-windows-amd64.exe') ./cmd/mihari
  if ($LASTEXITCODE -ne 0) { throw "windows/amd64 build failed with exit code $LASTEXITCODE" }
  $env:GOOS='windows'; $env:GOARCH='arm64'; go build -o (Join-Path $resolvedBuildDir 'mihari-windows-arm64.exe') ./cmd/mihari
  if ($LASTEXITCODE -ne 0) { throw "windows/arm64 build failed with exit code $LASTEXITCODE" }
  $env:GOOS='linux';   $env:GOARCH='amd64'; go build -o (Join-Path $resolvedBuildDir 'mihari-linux-amd64') ./cmd/mihari
  if ($LASTEXITCODE -ne 0) { throw "linux/amd64 build failed with exit code $LASTEXITCODE" }
  $env:GOOS='darwin';  $env:GOARCH='arm64'; go build -o (Join-Path $resolvedBuildDir 'mihari-darwin-arm64') ./cmd/mihari
  if ($LASTEXITCODE -ne 0) { throw "darwin/arm64 build failed with exit code $LASTEXITCODE" }
} finally {
  $env:CGO_ENABLED = $previousCGO
  $env:GOOS = $previousGOOS
  $env:GOARCH = $previousGOARCH
  Remove-Item -LiteralPath $resolvedBuildDir -Recurse -Force
}
```

Expected: 四个构建均成功，且只清理显式创建并解析过的临时子目录。

- [x] **Step 4: 检查精确变更范围并请求独立代码审查**

```console
git status --short
git diff --check
git diff --stat
git diff -- internal/platform docs/superpowers/specs/2026-08-13-windows-tui-relaunch-lifecycle-design.md docs/superpowers/plans/2026-08-13-windows-tui-relaunch-lifecycle.md
```

Expected: 只有计划内的 Windows 平台实现、测试、设计与计划文档；无临时产物或无关文件。

审查若发现同步等待扩大了资源生命周期，必须在提交前补充 TUI cleanup 顺序测试，并确保旧 control session 在 relaunch 回调前关闭。实现使用 `sync.Once` 包装 cleanup，同时保留 defer 兜底。

- [ ] **Step 5: 创建单一签名提交**

```console
git add internal/platform/relaunch_windows.go internal/platform/relaunch_windows_test.go docs/superpowers/specs/2026-08-13-windows-tui-relaunch-lifecycle-design.md docs/superpowers/plans/2026-08-13-windows-tui-relaunch-lifecycle.md
git commit -s -m "fix(tui): 修复 Windows 自更新后的终端争用"
```

- [ ] **Step 6: 推送并创建 PR**

```console
git push -u origin fix/issue-55-tui-relaunch
```

读取 `.github/PULL_REQUEST_TEMPLATE.md` 后，用完整正文直接调用 `gh pr create --body`。正文必须包含：`Fixes #55`、根因、父进程同步等待 replacement 的修复方式、全部实际运行的验证命令、模板检查项，以及“真实 Windows TUI 自更新/控制台 Ctrl+C 未在自动化中执行”的明确说明。例如：

```powershell
$prBody = @'
## 变更内容

- wait for the replacement Mihari process on Windows so the invoking shell does not resume early
- preserve inherited console streams and clean up the process handle after wait failures
- add lifecycle regression tests

## 类型

- [ ] feat: 新功能
- [x] fix: Bug 修复
- [ ] refactor: 重构
- [x] docs: 文档
- [x] test: 测试
- [ ] chore: 构建/工具

## 检查清单

- [x] `go test -race ./...` 通过
- [x] `go vet ./...` 通过
- [x] `gofmt -l cmd internal` 无输出
- [x] 提交包含 `Signed-off-by`（DCO 签名）
- [x] 跨平台行为影响已在本 PR 描述：Windows relaunch 现在等待 replacement；Unix 行为不变

## 相关 Issues

Fixes #55

## 其他

本地验证：

- `go test ./...`
- `go test -race ./...`
- `go vet ./...`
- `gofmt -l cmd internal`
- CGO-free cross-builds: windows/amd64, windows/arm64, linux/amd64, darwin/arm64

未执行真实的管理员 Windows TUI 自更新与控制台 Ctrl+C 流程；它们需要另行 testenv 授权。
'@
gh pr create --repo mihari-proxy/mihari --base main --head fix/issue-55-tui-relaunch --title "fix(tui): 修复 Windows 自更新后的终端争用" --body $prBody
```

- [ ] **Step 7: 持续监控 Actions**

```console
gh pr checks <pr-number> --watch --interval 15
```

Expected: 所有非 skipped checks 均为 success，而不只是 required checks。若失败，读取失败 job 日志，按 systematic debugging 回到根因调查，修复后重新完成相关本地验证、创建签名提交并推送，再次 watch。最后运行一次不带 `--watch` 的 `gh pr checks <pr-number>`，保存全部 Actions 已绿色的静态终态证据。
