# TUI Mihari Self-Update Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a System-page Mihari update action that checks GitHub Releases on page entry, uses existing lifecycle chips, safely replaces the binary, and automatically enters the updated TUI after Bubble Tea restores the terminal.

**Architecture:** Extend `internal/update.SelfUpdater` with a read-only `Check` operation, inject it into the System page, and route confirmed updates through the existing typed `ActionIntentMsg` dispatcher. A successful or partially successful binary replacement emits a typed relaunch request; `tui.Run` waits for `program.Run()` to return before invoking an injected platform relaunch callback.

**Tech Stack:** Go 1.26, Bubble Tea v2, Lip Gloss v2, Cobra, `net/http`, platform-specific Go files with `CGO_ENABLED=0`.

## Global Constraints

- Work only on branch `feat/tui-self-update`; do not modify or commit the user's existing `.gitignore` or `.claude/` changes.
- Use Red–Green–Refactor for every behavior change and defect found during implementation.
- Do not add dependencies, alter `/v1` DTOs, error codes, JSON envelopes, CLI exit codes, or persistence formats.
- Version copy is exactly `v0.3.1 · v0.4.0 available` or `v0.3.1 · Up to date`; `·` is a plain middle dot.
- `Checking` and `Updating` use the existing Pending chip/spinner; failures use Failed; only a completed binary update uses Done.
- Automatic checks are read-only and unprivileged; update execution requires existing administrator/root privilege and never auto-elevates.
- Release downloads keep existing response and binary size limits and sanitized errors.
- Platform relaunch code must remain CGO-free and isolated in `_windows.go` and `_unix.go` files.
- Tests must not access public networks, real user directories, real subscriptions, real services, or installed mihomo.

---

## File Structure

- Modify `internal/update/self.go`: add `CheckResult`, `Check`, and shared latest-release comparison.
- Modify `internal/update/self_test.go`: prove read-only checks, available/up-to-date results, and error behavior.
- Modify `internal/tui/ui/action.go`: add the typed Mihari update action and relaunch message.
- Modify `internal/tui/actions.go`: register confirmation and mark self-update as daemon-independent.
- Modify `internal/tui/ui/strings.go`: centralize Mihari update labels, confirmation copy, progress copy, and safe fallback errors.
- Modify `internal/tui/pages/system/model.go`: inject updater/configuration, start automatic checks, render the row, execute confirmed updates, and emit relaunch requests.
- Modify `internal/tui/pages/system/model_test.go`: cover the complete System-page state machine and lifecycle rendering.
- Modify `internal/tui/model.go`: configure the System page and consume typed relaunch requests.
- Modify `internal/tui/model_test.go`: cover confirmation dispatch and root relaunch state.
- Modify `internal/tui/run.go`: accept updater/relaunch dependencies and invoke relaunch only after the Bubble Tea program exits.
- Modify `internal/tui/run_test.go`: verify normal exit versus relaunch and callback ordering through a small extracted coordinator.
- Create `internal/platform/relaunch_windows.go`: start the replacement binary attached to the current console.
- Create `internal/platform/relaunch_unix.go`: replace the current process with the replacement binary.
- Modify `cmd/mihari/main.go`: inject current version, executable path, updater, privilege checker, and relaunch callback.
- Modify `README.md`, `README.zh-CN.md`, and `docs/architecture.md`: document the new TUI capability and security boundary.

---

### Task 1: Read-Only Release Check

**Files:**
- Modify: `internal/update/self_test.go`
- Modify: `internal/update/self.go`

**Interfaces:**
- Produces: `type CheckResult struct { Current string; Latest string; Available bool }`
- Produces: `func (u SelfUpdater) Check(ctx context.Context, currentVersion string) (CheckResult, error)`
- Preserves: `func (u SelfUpdater) Update(ctx context.Context, binaryPath, currentVersion string) (Result, error)`

- [ ] **Step 1: Write failing tests for available/current versions and read-only behavior**

Add table-driven HTTP tests with literal expectations:

```go
func TestSelfUpdaterCheckReportsAvailability(t *testing.T) {
    tests := []struct {
        name      string
        current   string
        latest    string
        available bool
    }{
        {name: "new release", current: "v1.0.0", latest: "v1.1.0", available: true},
        {name: "same release", current: "v1.0.0", latest: "v1.0.0", available: false},
    }
    for _, test := range tests {
        t.Run(test.name, func(t *testing.T) {
            server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
                _ = json.NewEncoder(w).Encode(Release{TagName: test.latest})
            }))
            defer server.Close()
            result, err := (SelfUpdater{HTTPClient: server.Client(), APIBase: server.URL}).Check(context.Background(), test.current)
            if err != nil || result.Current != test.current || result.Latest != test.latest || result.Available != test.available {
                t.Fatalf("result=%#v err=%v", result, err)
            }
        })
    }
}
```

Also add `TestSelfUpdaterCheckDoesNotDownloadAsset`: return a release containing an asset, count `/asset` requests, point a temporary directory at the test fixture, call `Check`, and assert zero asset requests and zero created files. This test fails to compile with the availability test until the new API exists, and then protects the read-only contract from future regressions.

- [ ] **Step 2: Run the focused test and verify RED**

Run: `go test -run '^TestSelfUpdaterCheck' ./internal/update`

Expected: compilation fails because `SelfUpdater.Check` and `CheckResult` do not exist.

- [ ] **Step 3: Implement the minimal read-only check**

Add:

```go
type CheckResult struct {
    Current   string
    Latest    string
    Available bool
}

func (u SelfUpdater) Check(ctx context.Context, currentVersion string) (CheckResult, error) {
    release, err := u.latestRelease(ctx)
    if err != nil {
        return CheckResult{}, err
    }
    return CheckResult{
        Current: currentVersion,
        Latest: release.TagName,
        Available: !sameTag(currentVersion, release.TagName),
    }, nil
}
```

- [ ] **Step 4: Run update package tests and verify GREEN**

Run: `go test ./internal/update`

Expected: PASS.

- [ ] **Step 5: Commit the independently testable update API**

```console
git add internal/update/self.go internal/update/self_test.go
git commit -s -m "feat(update): 增加 Mihari 版本检查"
```

---

### Task 2: Register the Typed Self-Update Action

**Files:**
- Modify: `internal/tui/policy_test.go`
- Modify: `internal/tui/ui/action.go`
- Modify: `internal/tui/actions.go`
- Modify: `internal/tui/ui/strings.go`

**Interfaces:**
- Produces: `ui.ActionUpdateMihari`
- Produces: `ui.RelaunchRequestMsg { Warning string }`
- Produces UI strings: `UpdateMihariLabel`, `UpdateMihariTitle`, `UpdateMihariProgressChecking`, `UpdateMihariProgressUpdating`, `UpdateMihariUpToDate`, and sanitized fallback copy.

- [ ] **Step 1: Write failing policy tests**

Add assertions that `UpdateMihari` is known, requires confirmation, and does not require a daemon:

```go
func TestSelfUpdatePolicyIsConfirmedAndDaemonIndependent(t *testing.T) {
    if !knownAction(UpdateMihari) {
        t.Fatal("self update must be registered")
    }
    if !RequiresConfirmation(UpdateMihari) {
        t.Fatal("self update must require confirmation")
    }
    if RequiresDaemon(UpdateMihari) {
        t.Fatal("self update must work without a daemon connection")
    }
}
```

- [ ] **Step 2: Run the focused test and verify RED**

Run: `go test -run '^TestSelfUpdatePolicyIsConfirmedAndDaemonIndependent$' ./internal/tui`

Expected: compilation fails because `UpdateMihari` does not exist.

- [ ] **Step 3: Add the action and policy mappings**

Add `ActionUpdateMihari Action = "update-mihari"`, its package alias, and include it in `RequiresConfirmation`, `knownAction`, and the daemon-independent branch of `RequiresDaemon`.

Add a typed handoff message:

```go
type RelaunchRequestMsg struct {
    Warning string
}
```

Add exact user-facing strings in `ui/strings.go`, including confirmation copy that names the current and target version without including URLs.

- [ ] **Step 4: Run policy and UI tests and verify GREEN**

Run: `go test ./internal/tui ./internal/tui/ui`

Expected: PASS.

- [ ] **Step 5: Commit action vocabulary**

```console
git add internal/tui/policy_test.go internal/tui/ui/action.go internal/tui/actions.go internal/tui/ui/strings.go
git commit -s -m "feat(tui): 定义 Mihari 更新操作"
```

---

### Task 3: System Page Automatic Check and Stable Rendering

**Files:**
- Modify: `internal/tui/pages/system/model_test.go`
- Modify: `internal/tui/pages/system/model.go`

**Interfaces:**
- Consumes: `update.CheckResult`, `SelfUpdater.Check`
- Produces: `type SelfUpdater interface { Check(...); Update(...) }`
- Produces: `func (m *Model) SetSelfUpdater(updater SelfUpdater, currentVersion, binaryPath string, elevated func() bool)`
- Produces row ID: `rowMihariUpdate = "mihari-update"`

- [ ] **Step 1: Write a failing test for automatic Checking state**

Create a `fakeSelfUpdater` whose `Check` returns a complete `update.CheckResult`. Configure the model with `SetSelfUpdater`, call `Load`, and assert the immediate view contains the Pending chip label `Checking` before executing the returned command.

- [ ] **Step 2: Run the focused test and verify RED**

Run: `go test -run '^TestSystemLoadStartsMihariVersionCheck$' ./internal/tui/pages/system`

Expected: compilation fails because `SetSelfUpdater` and the row do not exist.

- [ ] **Step 3: Implement updater injection, check messages, and Load integration**

Add the local interface and state:

```go
type SelfUpdater interface {
    Check(context.Context, string) (update.CheckResult, error)
    Update(context.Context, string, string) (update.Result, error)
}

type selfCheckResultMsg struct {
    generation uint64
    result     update.CheckResult
    err        error
}
```

Store updater, current version, binary path, elevation checker, check generation, and check status on `Model`. `SetSelfUpdater` defaults a nil elevation checker to `elevate.IsElevated`. `Load` starts one check only when no System row operation is pending and batches it with existing loads.

- [ ] **Step 4: Run the focused test and verify GREEN**

Run: `go test -run '^TestSystemLoadStartsMihariVersionCheck$' ./internal/tui/pages/system`

Expected: PASS.

- [ ] **Step 5: Write failing stable-rendering and failure tests**

Feed literal results into `Update` and assert visible output:

```go
func TestSystemMihariVersionCheckRendersStableStates(t *testing.T) {
    // available => "v0.3.1 · v0.4.0 available"
    // same => "v0.3.1 · Up to date"
}

func TestSystemMihariVersionCheckFailureUsesFailedChip(t *testing.T) {
    // error => Failed chip, safe error detail, no available/up-to-date text
}
```

- [ ] **Step 6: Run both tests and verify RED**

Run: `go test -run '^TestSystemMihariVersionCheck' ./internal/tui/pages/system`

Expected: FAIL because result messages are not reconciled into row values/outcomes.

- [ ] **Step 7: Implement stable values, Failed outcome, and retry**

Handle only the current check generation. On success, clear pending/outcome and store the result. On error, clear pending and call the existing row outcome helper for `rowMihariUpdate`. In `rows()`, insert the action row before `Run Setup` and use a helper that returns the exact stable copy. Enter on up-to-date or failed state starts a new check.

- [ ] **Step 8: Run System tests and verify GREEN**

Run: `go test ./internal/tui/pages/system`

Expected: PASS.

- [ ] **Step 9: Add and pass a stale-result regression test**

Start check generation 1, retry to generation 2, deliver generation 1 after generation 2 starts, and assert generation 1 does not replace the visible Pending state or latest result.

Run: `go test -run '^TestSystemMihariVersionCheckIgnoresStaleResult$' ./internal/tui/pages/system`

Expected before implementation guard: FAIL; after generation guard: PASS.

- [ ] **Step 10: Commit automatic check behavior**

```console
git add internal/tui/pages/system/model.go internal/tui/pages/system/model_test.go
git commit -s -m "feat(tui): 在 System 页检查 Mihari 版本"
```

---

### Task 4: Confirmed Update, Privilege Gate, and Relaunch Request

**Files:**
- Modify: `internal/tui/pages/system/model_test.go`
- Modify: `internal/tui/pages/system/model.go`

**Interfaces:**
- Consumes: `ui.ActionUpdateMihari`, `SelfUpdater.Update`
- Produces: `selfUpdateResultMsg` implementing `Err() error`; it returns nil after a committed replacement even when service restart produced a warning
- Produces: `ui.RelaunchRequestMsg { Warning string }` after `Result.Updated == true`

- [ ] **Step 1: Write a failing confirmation-intent test**

Set an available result, focus `rowMihariUpdate`, press Enter, and assert the returned `ui.ActionIntentMsg` has action `ActionUpdateMihari`, page `PageSystem`, no daemon capability, exact current/target versions in its object/impact, and a non-nil `Execute` command.

- [ ] **Step 2: Run the focused test and verify RED**

Run: `go test -run '^TestSystemMihariUpdateOffersConfirmationWhenAvailable$' ./internal/tui/pages/system`

Expected: FAIL because Enter does not produce the intent.

- [ ] **Step 3: Implement the confirmation intent and Updating state mapping**

Add `ActionUpdateMihari` to `rowProgressForAction` with `rowMihariUpdate` and `Updating`. The intent's Execute closure checks elevation before calling `Update`; it returns a typed `selfUpdateResultMsg` with both `update.Result` and `error`.

- [ ] **Step 4: Run the focused test and verify GREEN**

Run: `go test -run '^TestSystemMihariUpdateOffersConfirmationWhenAvailable$' ./internal/tui/pages/system`

Expected: PASS.

- [ ] **Step 5: Write failing permission and update-result tests**

Add tests proving observable behavior:

- privilege failure yields a Failed row and leaves `fakeSelfUpdater.updateCalls == 0`;
- update error with `Updated=false` yields Failed and no relaunch command;
- update success yields Done and a command returning `ui.RelaunchRequestMsg`;
- service restart error with `Updated=true` still yields a relaunch request while retaining the sanitized partial-failure error.

- [ ] **Step 6: Run the result tests and verify RED**

Run: `go test -run '^TestSystemMihariUpdate' ./internal/tui/pages/system`

Expected: FAIL because result reconciliation and partial-success behavior are absent.

- [ ] **Step 7: Implement result reconciliation**

For `Updated=false`, `Err()` returns the update error, the page marks Failed, and the TUI stays open. For `Updated=true`, `Err()` returns nil so the Recent operations ledger records the committed replacement as successful; the page marks Done and emits `ui.RelaunchRequestMsg{Warning: sanitizedWarning}`. Derive `sanitizedWarning` through the existing `actionErrorDetail` path and never infer partial success from error text.

- [ ] **Step 8: Run System page tests and verify GREEN**

Run: `go test ./internal/tui/pages/system`

Expected: PASS.

- [ ] **Step 9: Commit confirmed update behavior**

```console
git add internal/tui/pages/system/model.go internal/tui/pages/system/model_test.go
git commit -s -m "feat(tui): 支持确认更新 Mihari"
```

---

### Task 5: Root Model Relaunch State and Dependency Wiring

**Files:**
- Modify: `internal/tui/model_test.go`
- Modify: `internal/tui/model.go`

**Interfaces:**
- Consumes: `ui.RelaunchRequestMsg`, System page `SetSelfUpdater`
- Produces: `func (model Model) RelaunchRequested() bool`
- Produces: `func (model Model) RelaunchWarning() string`
- Produces: `func (model *Model) SetSelfUpdater(updater systempage.SelfUpdater, currentVersion, binaryPath string, elevated func() bool)`

- [ ] **Step 1: Write a failing root-model relaunch test**

```go
func TestModelRelaunchRequestQuitsAndRecordsIntent(t *testing.T) {
    model := NewModel()
    updated, cmd := model.Update(ui.RelaunchRequestMsg{Warning: "service restart failed"})
    got := updated.(Model)
    if !got.RelaunchRequested() || got.RelaunchWarning() != "service restart failed" || cmd == nil || cmd() != tea.Quit() {
        t.Fatalf("requested=%v warning=%q cmd=%v", got.RelaunchRequested(), got.RelaunchWarning(), cmd != nil)
    }
}
```

- [ ] **Step 2: Run the focused test and verify RED**

Run: `go test -run '^TestModelRelaunchRequestQuitsAndRecordsIntent$' ./internal/tui`

Expected: compilation fails because `RelaunchRequested` does not exist.

- [ ] **Step 3: Implement typed root state and System forwarding**

Add `relaunchRequested bool` and `relaunchWarning string` to `Model`, handle `ui.RelaunchRequestMsg` before normal page dispatch by storing both fields and returning `tea.Quit`, add accessors, and add a setter that configures the concrete System page.

- [ ] **Step 4: Run root model tests and verify GREEN**

Run: `go test ./internal/tui`

Expected: PASS.

- [ ] **Step 5: Commit root coordination**

```console
git add internal/tui/model.go internal/tui/model_test.go
git commit -s -m "feat(tui): 协调更新后的界面重启"
```

---

### Task 6: Relaunch Only After Terminal Restoration

**Files:**
- Modify: `internal/tui/run_test.go`
- Modify: `internal/tui/run.go`

**Interfaces:**
- Consumes: `Model.RelaunchRequested()`
- Adds to `tui.Options`: `SelfUpdater`, `CurrentVersion`, `BinaryPath`, `Elevated`, `Relaunch`
- Produces internal helper: `func finishRun(final tea.Model, runErr error, warningWriter io.Writer, relaunch func() error) error`

- [ ] **Step 1: Write failing coordinator tests**

Test the extracted post-`program.Run` coordinator with real `Model` values:

```go
func TestFinishRunRelaunchesOnlyWhenRequested(t *testing.T) {
    calls := 0
    model := NewModel()
    model.relaunchRequested = true
    var warnings bytes.Buffer
    if err := finishRun(model, nil, &warnings, func() error { calls++; return nil }); err != nil || calls != 1 {
        t.Fatalf("calls=%d err=%v", calls, err)
    }
}

func TestFinishRunNormalExitDoesNotRelaunch(t *testing.T) {
    calls := 0
    if err := finishRun(NewModel(), nil, io.Discard, func() error { calls++; return nil }); err != nil || calls != 0 {
        t.Fatalf("calls=%d err=%v", calls, err)
    }
}
```

Also test that a `runErr` prevents relaunch, that a relaunch error is returned, and that a stored warning is written exactly once before the relaunch callback observes execution.

- [ ] **Step 2: Run coordinator tests and verify RED**

Run: `go test -run '^TestFinishRun' ./internal/tui`

Expected: compilation fails because `finishRun` does not exist.

- [ ] **Step 3: Implement post-run coordination and options wiring**

Configure the System page before creating the program. Capture the final model returned by `program.Run()`, then call `finishRun`; never invoke relaunch inside a Bubble Tea command or model update.

- [ ] **Step 4: Run TUI tests and verify GREEN**

Run: `go test ./internal/tui`

Expected: PASS.

- [ ] **Step 5: Commit post-terminal relaunch coordination**

```console
git add internal/tui/run.go internal/tui/run_test.go
git commit -s -m "feat(tui): 在终端恢复后重新进入界面"
```

---

### Task 7: Platform Relaunch and Main Wiring

**Files:**
- Create: `internal/platform/relaunch_windows.go`
- Create: `internal/platform/relaunch_unix.go`
- Modify: `cmd/mihari/main.go`
- Modify: `cmd/mihari/main_test.go`

**Interfaces:**
- Produces: `func platform.Relaunch(binary string, args, env []string) error`
- Consumes: `tui.Options.Relaunch`

- [ ] **Step 1: Write a failing platform-independent argument test**

Extract a pure helper in `cmd/mihari`:

```go
func tuiRelaunchArgs(binary string) []string {
    return []string{binary}
}
```

Write a test with literal expectation proving no `daemon`, `self update`, or unrelated CLI arguments are propagated.

- [ ] **Step 2: Run the focused test and verify RED**

Run: `go test -run '^TestTUIRelaunchArgsStartsDefaultTUI$' ./cmd/mihari`

Expected: compilation fails because `tuiRelaunchArgs` does not exist.

- [ ] **Step 3: Implement platform relaunch adapters**

Unix implementation calls `syscall.Exec(binary, args, env)`. Windows implementation calls `os.StartProcess` with inherited stdin/stdout/stderr and environment, then releases the child process handle. Both validate non-empty binary and args and wrap errors with operation context.

- [ ] **Step 4: Wire production dependencies**

Resolve `os.Executable()` once during startup. Populate `tui.Options` with:

```go
SelfUpdater:    selfUpdater,
CurrentVersion: buildinfo.Version,
BinaryPath:     executable,
Elevated:       elevate.IsElevated,
Relaunch: func() error {
    return platform.Relaunch(executable, tuiRelaunchArgs(executable), os.Environ())
},
```

Keep the existing CLI `SelfUpdater` dependency unchanged.

- [ ] **Step 5: Run current-platform tests and compile all target adapters**

Run:

```console
go test ./cmd/mihari ./internal/platform ./internal/tui
$env:CGO_ENABLED='0'; $env:GOOS='windows'; $env:GOARCH='amd64'; go test -run '^$' ./cmd/mihari ./internal/platform
$env:CGO_ENABLED='0'; $env:GOOS='linux'; $env:GOARCH='amd64'; go test -run '^$' ./cmd/mihari ./internal/platform
$env:CGO_ENABLED='0'; $env:GOOS='darwin'; $env:GOARCH='arm64'; go test -run '^$' ./cmd/mihari ./internal/platform
```

Expected: PASS/compile success for all commands.

- [ ] **Step 6: Commit platform wiring**

```console
git add cmd/mihari/main.go cmd/mihari/main_test.go internal/platform/relaunch_windows.go internal/platform/relaunch_unix.go
git commit -s -m "feat(platform): 支持更新后重新进入 TUI"
```

---

### Task 8: Documentation and Full Verification

**Files:**
- Modify: `README.md`
- Modify: `README.zh-CN.md`
- Modify: `docs/architecture.md`
- Include: `docs/superpowers/specs/2026-08-12-tui-self-update-design.md`
- Include: `docs/superpowers/plans/2026-08-12-tui-self-update.md`

**Interfaces:**
- Documents the already-tested behavior; no production interface changes.

- [ ] **Step 1: Update user-facing documentation**

State that System automatically checks the latest Mihari release, displays `current · latest available` or `current · Up to date`, requires an already elevated process to update, and automatically enters the updated TUI after replacement. Preserve `mihari self update` documentation.

- [ ] **Step 2: Format modified Go files**

Run:

```powershell
$taskGoFiles = git diff --name-only origin/main...HEAD -- '*.go'
if ($taskGoFiles) { gofmt -w $taskGoFiles }
```

- [ ] **Step 3: Run focused and full verification**

Run:

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

Expected: every command exits 0; `gofmt -l` produces no output for modified files.

- [ ] **Step 4: Run CGO-free cross-platform builds**

Run:

```console
$env:CGO_ENABLED='0'; $env:GOOS='windows'; $env:GOARCH='amd64'; go build ./cmd/mihari
$env:CGO_ENABLED='0'; $env:GOOS='linux'; $env:GOARCH='amd64'; go build ./cmd/mihari
$env:CGO_ENABLED='0'; $env:GOOS='darwin'; $env:GOARCH='arm64'; go build ./cmd/mihari
```

Remove only the explicitly generated local build artifact after verifying its resolved path is inside the repository and untracked.

- [ ] **Step 5: Audit the exact diff**

Run:

```console
git status --short
git diff --check
git diff --stat origin/main...HEAD
git diff origin/main...HEAD -- . ':(exclude).gitignore' ':(exclude).claude/**'
```

Confirm no credentials, temporary artifacts, user-owned changes, protocol changes, unrelated refactors, or generated coverage files are included.

- [ ] **Step 6: Commit documentation and plan artifacts**

```console
git add README.md README.zh-CN.md docs/architecture.md docs/superpowers/specs/2026-08-12-tui-self-update-design.md docs/superpowers/plans/2026-08-12-tui-self-update.md
git commit -s -m "docs: 记录 TUI 自更新流程"
```

---

### Task 9: Review, Push, and Pull Request

**Files:**
- No new code files; review committed branch state and remote PR metadata.

**Interfaces:**
- Produces a pushed `feat/tui-self-update` branch and a GitHub pull request targeting `main`.

- [ ] **Step 1: Use verification-before-completion and requesting-code-review**

Re-run the required evidence commands from Task 8, inspect the complete committed diff, and address every material review finding through a new failing regression test before changing production code.

- [ ] **Step 2: Verify commit scope and DCO sign-offs**

Run:

```console
git log --format='%h %s%n%(trailers:key=Signed-off-by)' origin/main..HEAD
git status --short
```

Confirm every task commit is signed off and only `.gitignore`/`.claude/` remain as user-owned uncommitted state.

- [ ] **Step 3: Push the feature branch**

Run: `git push -u origin feat/tui-self-update`

Expected: remote branch is created or fast-forwarded successfully.

- [ ] **Step 4: Create the pull request**

Run:

```console
gh pr create --base main --head feat/tui-self-update --title "feat(tui): 支持更新 Mihari" --body-file .git/pr-tui-self-update.md
```

Create `.git/pr-tui-self-update.md` as an untracked Git-internal temporary file. The PR body must summarize UI behavior, architecture boundaries, privilege behavior, relaunch semantics, tests, race/vet/cross-build evidence, and any unverified real-environment scenarios. Do not claim commands that were not executed.

- [ ] **Step 5: Verify PR state**

Run: `gh pr view --json url,state,baseRefName,headRefName,title,statusCheckRollup`

Expected: open PR targeting `main` from `feat/tui-self-update`; report its URL and current checks.
