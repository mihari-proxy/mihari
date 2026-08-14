# Mihari 一期代码质量提升实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Every task requires an independent spec-and-quality review before the next task starts.

**Goal:** 关闭 AQ-01、AQ-04、AQ-05，并恢复可作为后续质量治理依据的默认测试与 coverage 信号。

**Architecture:** 配置初始化使用单一 deadline 的“设置就绪或获得创建锁”协调器；CLI 测试显式表达空参数；self updater 精确选择唯一平台资产，并在替换前严格校验同一 Release 的 SHA-256 manifest。三个行为单元独立 TDD、独立提交，最后统一执行静态、测试、coverage、跨平台和构建验收。

**Tech Stack:** Go 1.26.5、标准库 `testing`/`httptest`/`crypto/sha256`、`golang.org/x/sys/windows`（既有依赖）、Cobra、GitHub Actions、golangci-lint v2.12.2。

**Spec:** `docs/superpowers/specs/2026-08-13-code-quality-phase-1-design.md`

**Roadmap:** [`2026-08-13-code-quality-roadmap.md`](2026-08-13-code-quality-roadmap.md)。本计划仅实施 Phase 1。

## Global Constraints

- 只在非 `main` 分支和独立 worktree 中实施；不得覆盖用户已有修改。
- 不修改 `/v1` DTO、JSON envelope、错误码、CLI 退出码、settings schema、持久化格式或平台范围。
- 不新增依赖，不修改 `go.mod` / `go.sum`，不启用 coverage 百分比门槛或批量 linter。
- 所有行为修改严格 Red–Green–Refactor；每个 Red 必须因目标行为缺失而失败，不以编译错误代替行为失败。
- 测试不得访问公网、真实用户目录、真实 mihomo、真实订阅或系统服务。
- AQ-05 使用单一绝对 deadline，不得仅扩大 timeout；普通 permission/data error 不得被重试为 timeout。
- AQ-01 exact asset、manifest 或 binary 任一异常均在替换前 fail closed；旧 binary 不变且 `AfterReplace` 不调用。
- Go 文件必须 `gofmt`；最终保持 `CGO_ENABLED=0` 六目标构建。
- 用户已在当前目标中明确授权每个 Phase 创建 commit；该授权不包含 push、PR 或 merge。
- 每个 Task 独立 commit（`git commit -s`）；只有 task review 同时通过 spec compliance 与 code quality 才进入下一 Task。
- 任一 task review 出现 finding 时：同一 implementer 以独立 signed fix commit 修正，重跑该 Task 的全部 focused/adjacent gates；复审 package 同时包含 fix range 和 Task-start-to-current-HEAD 累积 diff，重新给出 spec/quality verdict；Critical/Important 清零前不得进入下一 Task。

---

## Task 0：固化已审治理文档

**Files:**
- Add: `docs/code-quality-audit-2026-08-13.md`
- Add: `docs/superpowers/plans/2026-08-13-code-quality-roadmap.md`
- Add: `docs/superpowers/specs/2026-08-13-code-quality-phase-1-design.md`
- Add: `docs/superpowers/plans/2026-08-13-code-quality-phase-1.md`

**Interfaces:** 形成 Phase 1 binding spec、audited plan 和路线索引；不修改生产行为。

- [ ] **Step 1：确认两轮审计门禁**

读取 `.superpowers/reviews/phase1-design-review.md` 与 `.superpowers/reviews/phase1-plan-review.md`，要求 design 和 plan 的两个 verdict 均 PASS 且 0 Critical/Important。

- [ ] **Step 2：机械验证并提交四份权威文档**

```powershell
git add docs/code-quality-audit-2026-08-13.md docs/superpowers/plans/2026-08-13-code-quality-roadmap.md docs/superpowers/specs/2026-08-13-code-quality-phase-1-design.md docs/superpowers/plans/2026-08-13-code-quality-phase-1.md
git diff --cached --check
if ($LASTEXITCODE -ne 0) { throw 'staged governance docs check failed' }
git commit -s -m "docs: 建立代码质量治理路线图"
if ($LASTEXITCODE -ne 0) { throw 'governance docs commit failed' }
```

Expected: 四份文档进入同一 signed commit；ignored 的 `.superpowers/` 审计 scratch 不提交。

---

## Task 1：配置初始化单 deadline 收敛（AQ-05）

**Files:**
- Modify: `internal/config/settings.go`
- Create: `internal/config/settings_conflict_windows.go`
- Create: `internal/config/settings_conflict_unix.go`
- Create: `internal/config/settings_conflict_windows_test.go`
- Create: `internal/config/settings_conflict_unix_test.go`
- Modify: `internal/config/settings_test.go`

**Interfaces:**
- Produce private `settingsCreationOps` with `now`, `wait`, `load`, `openLock`, `transientConflict` function fields.
- Produce private `loadOrCreateWithOps(path, sidecar string, ops settingsCreationOps) (Settings, bool, error)`; public wrappers construct real ops per call.
- Produce private `waitForSettingsOrCreationLock(path string, deadline time.Time, ops settingsCreationOps) (Settings, *os.File, error)`.
- Produce private `isSettingsConflict(error) bool` in paired platform files; Unix file has `//go:build !windows`.
- Preserve public `LoadOrCreate`, `LoadOrCreateResult`, `LoadOrCreateWithSidecar` signatures and exact `created` semantics.

- [ ] **Step 1：保存可复现基线**

```powershell
go test -count=1 ./...
go test -count=1 ./...
go test -count=20 -run '^TestConcurrentLoadOrCreateUsesOneControllerSecret$' ./internal/config
```

Expected: 审计基线的默认全仓运行至少一次出现 `timed out waiting for settings initialization`；单包重复通常通过。若本机本轮未复现，记录审计报告已有的两次失败，不伪造新 Red。

- [ ] **Step 2：提取可编译、行为不变的 per-call ops 与装配入口**

先把当前真实操作封装为每次调用构造的 `settingsCreationOps`。此纯重构阶段使用 `legacySettingsConflict` 精确保留旧 `os.ErrExist || os.ErrPermission` 语义，尚不引用平台 helper。公共 `loadOrCreate` 只调用 `loadOrCreateWithOps(path, sidecar, defaultSettingsCreationOps())`；旧读取/锁循环移动到 ops 版本但行为不变。

```go
type settingsCreationOps struct {
	now               func() time.Time
	wait              func(time.Duration)
	load              func(string) (Settings, error)
	openLock          func(string) (*os.File, error)
	transientConflict func(error) bool
}

func defaultSettingsCreationOps() settingsCreationOps {
	return settingsCreationOps{
		now: time.Now,
		wait: time.Sleep,
		load: Load,
		openLock: func(path string) (*os.File, error) {
			return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		},
		transientConflict: legacySettingsConflict,
	}
}

func legacySettingsConflict(err error) bool {
	return errors.Is(err, os.ErrExist) || errors.Is(err, os.ErrPermission)
}
```

Run:

```powershell
gofmt -w internal/config/settings.go
go test -count=1 ./internal/config
```

Expected: PASS；这是行为不变重构。

- [ ] **Step 3：写单 deadline、二选一返回与错误分类的失败测试**

在 `settings_test.go` 新增确定性测试，使用局部 ops（不得修改包级变量）：

- `TestWaitForSettingsOrCreationLock_ReturnsCompletedSettingsDuringConflict`：第一次 `openLock` 返回 `os.ErrExist`，`load` 返回 literal valid Settings，断言无等待且 lock 为 nil。
- `TestWaitForSettingsOrCreationLock_RejectsMalformedSettingsImmediately`：`load` 返回 `dataError("invalid settings file")`，断言原错误返回且 wait=0。
- `TestWaitForSettingsOrCreationLock_ReturnsTerminalPermissionImmediately`：`openLock` 返回 wrapped `os.ErrPermission`、`transientConflict=false`，断言非 timeout 且 wait=0。
- `TestWaitForSettingsOrCreationLock_UsesOneDeadline`：fake clock 从 `start` 推进到 `start+10s`，持续 `os.ErrExist`/`os.ErrNotExist`，断言稳定 timeout 且总 fake elapsed 不超过 10s+一次 10ms tick。
- `TestWaitForSettingsOrCreationLock_ReturnsOnlyAcquiredLock`：`openLock` 返回真实 temp file，断言 settings 为零值、lock 非 nil、load 未调用。
- `TestLoadOrCreateWithOps_RetriesTransientInitialRead`：顶层第一次 `ops.load` 返回注入 transient sentinel，下一次返回 valid settings；同一 fake deadline/wait 下成功返回且 permission/data 对照仍立即失败。
- `TestWaitForSettingsOrCreationLock_RetriesTransientObservedRead`：lock conflict 后第一次 re-observe 返回 transient sentinel，下一次返回 valid settings；在同一 deadline 内成功且 wait 次数精确可断言。

每个测试用手工 literal Settings；目标生产 mutation 分别是“删除 settings re-observe”“把 data/permission 当暂态”“每层创建新 deadline”。

Run:

```powershell
go test -count=1 -run '^(TestWaitForSettingsOrCreationLock_.*|TestLoadOrCreateWithOps_RetriesTransientInitialRead)$' ./internal/config
```

Expected: FAIL on assertions because extracted helper still only waits for lock or misclassifies errors；initial-read Red 至少由 terminal permission/data assertion 触发（legacy transient retry 子例可能已 Green）；不得是缺符号/语法错误。

- [ ] **Step 4：实现单 deadline 协调循环和平台分类**

协调循环按 spec §4.2/§4.3 执行：尝试 exact lock；冲突后调用 `ops.load(path)`；有效 settings 立即返回；`ops.transientConflict(loadErr)` 为 true 时在同一 deadline 内 wait/retry；data/terminal error 立即返回；NotExist 继续；同一 deadline 到期返回 `dataError("timed out waiting for settings initialization")`。顶层 initial `ops.load` 使用同一分类：transient 进入协调重试，NotExist 进入创建协调，data/terminal 立即返回。

在行为 Red 已观察后创建平台文件。`settings_conflict_windows.go`：

```go
package config

import (
	"errors"
	"golang.org/x/sys/windows"
)

func isSettingsConflict(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
		errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
```

通用调用点负责 `os.ErrExist`；平台 helper 只判断 Windows error code。

`settings_conflict_unix.go`：

```go
//go:build !windows

package config

func isSettingsConflict(error) bool { return false }
```

通用代码将 `errors.Is(err, os.ErrExist) || ops.transientConflict(err)` 作为暂态竞争；普通 `os.ErrPermission` 立即返回。此步将 default ops 从 `legacySettingsConflict` 切换到 `isSettingsConflict`，随后删除 legacy helper。

增加 paired platform tests：`settings_conflict_windows_test.go` 断言两个 Windows error code 为 true、`os.ErrPermission` 为 false；`settings_conflict_unix_test.go` 带 `//go:build !windows` 并断言普通 errors 为 false。

- [ ] **Step 5：验证错误矩阵 Green**

```powershell
gofmt -w internal/config/settings.go internal/config/settings_conflict_windows.go internal/config/settings_conflict_unix.go internal/config/settings_test.go internal/config/settings_conflict_windows_test.go internal/config/settings_conflict_unix_test.go
go test -count=1 -run '^(TestWaitForSettingsOrCreationLock_.*|TestLoadOrCreateWithOps_RetriesTransientInitialRead)$' ./internal/config
```

Expected: PASS，且测试无需真实 sleep 或 OS permission 操作。

- [ ] **Step 6：写 `created`、single-secret、sidecar 和 owner cleanup 失败测试**

将现有并发测试改为调用 `LoadOrCreateResult` 并收集 `created`；断言 32 个结果、恰好 1 个 true、31 个 false、一个 literal-consistent secret、最终 `Load(path)` 成功。

新增 `TestLoadOrCreateWithOps_WaiterPersistsSidecarWithoutCreating`：通过 per-call ops 的 channel/latch 让首次 `load` 返回 NotExist，确认 `openLock` 已返回 conflict 后再让下一次 `load` 返回 stable valid settings；调用 `loadOrCreateWithOps` 应返回 `created=false`、alpha channel/bundle，并由真实 `Load` 证明 sidecar 已持久化。该顺序在旧只等 lock 的 helper 上稳定触发 injected deadline，不依赖 scheduler 或固定 sleep。

新增 `TestLoadOrCreateWithOps_CreatorClosesAndRemovesLock`：使用真实 temp path/lock，creator 成功返回后断言 `.lock` 不存在，并能在 Windows 上重新打开和删除同一路径，证明 handle 已关闭。

Run:

```powershell
go test -count=1 -run '^(TestConcurrentLoadOrCreateUsesOneControllerSecret|TestLoadOrCreateWithOps_WaiterPersistsSidecarWithoutCreating|TestLoadOrCreateWithOps_CreatorClosesAndRemovesLock)$' ./internal/config
```

Expected: waiter sidecar test FAIL through injected deadline because旧 helper never re-observes settings；不得依靠 wall-clock 调度超时。

- [ ] **Step 7：写顶层单 deadline 的行为 Red**

新增 `TestLoadOrCreateWithOps_SharesDeadlineAcrossInitialReadAndCoordination`：fake clock/ops 让 initial read 先经历 transient、随后 NotExist、再进入 lock conflict/re-observe；断言总 fake elapsed 受入口一个 10 秒 deadline 限制。针对 Step 2 的行为不变双循环运行：

```powershell
go test -count=1 -run '^TestLoadOrCreateWithOps_SharesDeadlineAcrossInitialReadAndCoordination$' ./internal/config
```

Expected: FAIL，因为行为不变版本仍可重置/嵌套 deadline；失败必须是 elapsed/attempt assertion，不得缺符号。

- [ ] **Step 8：实现顶层单 deadline并接回 sidecar 路径**

`loadOrCreateWithOps` 只计算一次 `deadline := ops.now().Add(10*time.Second)`，初始读取也使用一次 attempt 而不是旧 `loadSettings` 的内部循环。首次有效读取、协调器返回 settings、lock owner 二次有效读取均调用 `persistSidecarIfChanged`；只有 creator 在首次 Save 前调用 `applySidecarIfPresent`。lock owner defer 负责 close/remove，且 helper 永不关闭调用方持有的 lock。删除旧 `loadSettings`/`acquireCreationLock` 的独立 deadline 循环。

- [ ] **Step 9：验证 AQ-05 全部行为**

```powershell
gofmt -w internal/config/settings.go internal/config/settings_conflict_windows.go internal/config/settings_conflict_unix.go internal/config/settings_test.go internal/config/settings_conflict_windows_test.go internal/config/settings_conflict_unix_test.go
go test -count=1 ./internal/config
go test -count=20 -run '^TestConcurrentLoadOrCreateUsesOneControllerSecret$' ./internal/config
go test -count=1 -race ./internal/config
go test -count=1 ./...
go test -count=1 ./...
```

Expected: 全部 PASS；默认全仓不得使用 `-p 1`。

- [ ] **Step 10：提交并生成 task review package**

```powershell
git diff --check
git add internal/config/settings.go internal/config/settings_conflict_windows.go internal/config/settings_conflict_unix.go internal/config/settings_test.go internal/config/settings_conflict_windows_test.go internal/config/settings_conflict_unix_test.go
git commit -s -m "fix: 修复配置初始化锁队列超时"
```

Expected: 独立 AQ-05 commit；task reviewer 两个 verdict 均 PASS 后才进入 Task 2。

## Task 2：稳定 coverage 下的 CLI 空参数测试（AQ-04）

**Files:**
- Modify: `internal/cli/runtime_test.go`
- Verify only: `.github/workflows/ci.yml`

**Interfaces:** 不新增接口；生产 `Execute` 和 Cobra wiring 不变。

- [ ] **Step 1：运行 coverage Red 基线**

```powershell
$profile = Join-Path $env:TEMP 'mihari-phase1-aq04-red.out'
go test -count=1 -covermode=atomic -coverpkg=./... -coverprofile="$profile" ./...
```

Expected on audited baseline: `TestExecute_NoArgsInteractiveRunsTUI` 可能返回 `exit=2 called=false`。若已因 Task 1 调度变化未复现，审计报告的连续两次复现是 Red 证据。

- [ ] **Step 2：把两个无参数测试改为显式 `[]string{}`**

```go
exit := Execute(context.Background(), []string{}, io.Discard, io.Discard, Dependencies{/* existing fields */})
```

只替换 `TestExecute_NoArgsNonInteractiveRejectsTUI` 与 `TestExecute_NoArgsInteractiveRunsTUI` 的 nil；不修改生产 `Execute`。

- [ ] **Step 3：验证 focused 与完整 coverage Green**

```powershell
gofmt -w internal/cli/runtime_test.go
go test -count=20 -run '^TestExecute_NoArgs' ./internal/cli
1..3 | ForEach-Object {
  $profile = Join-Path $env:TEMP "mihari-phase1-aq04-$_.out"
  go test -count=1 -covermode=atomic -coverpkg=./... -coverprofile="$profile" ./...
  if ($LASTEXITCODE -ne 0) { throw "coverage run $_ failed" }
  go run ./scripts/coverage-gate report -profile "$profile"
  if ($LASTEXITCODE -ne 0) { throw "coverage report $_ failed" }
}
```

Expected: focused 20 次与 coverage 3 次全部 PASS，profile 可解析。

- [ ] **Step 4：提交并通过 task review**

```powershell
git diff --check
git add internal/cli/runtime_test.go
git commit -s -m "test: 稳定无参数 CLI 覆盖率测试"
```

Expected: 独立 test commit；review clean 后进入 Task 3。

## Task 3：self update exact asset 与 SHA-256 fail-closed（AQ-01）

**Files:**
- Modify: `internal/update/self.go`
- Modify: `internal/update/self_test.go`
- Verify only: `.github/workflows/release.yml`

**Interfaces:**
- Preserve public `SelfUpdater.Update`, `Check`, `Result`, `CheckResult`.
- Tighten public `SelectSelfAsset` to exact case-sensitive unique selection.
- Add private `selectChecksumAsset`, `parseChecksumManifest`, `fetchExpectedChecksum` as implementation details driven through public `Update` behavior.
- Change private `download` to accept `Asset`, `[sha256.Size]byte`, and a narrow candidate opener; tests inject open/write/close failures without package globals.

- [ ] **Step 1：写 exact asset 选择 Red 测试**

表驱动覆盖：Linux/Windows exact success、missing、duplicate exact、prefix `-debug`、case variant、archive neighbor。对 neighbor case，Release 同时含 neighbor 与 exact 时必须选 exact；只有 neighbor 时必须 missing。

```powershell
go test -count=1 -run '^TestSelectSelfAsset' ./internal/update
```

Expected: duplicate/prefix/case 用例 FAIL，证明当前 prefix-first selector 有歧义。

- [ ] **Step 2：实现 exact unique selector 并 Green**

目标名严格为 Windows `mihari-windows-<arch>.exe`，其他 `mihari-<goos>-<arch>`；遍历计数 exact case-sensitive matches，0 或 >1 返回 `CodeDataFailure`。

```powershell
gofmt -w internal/update/self.go internal/update/self_test.go
go test -count=1 -run '^TestSelectSelfAsset' ./internal/update
```

- [ ] **Step 3：提取行为不变的 candidate writer seam**

在 `SelfUpdater` 增加非导出 `openCandidate func(string) (io.WriteCloser, error)`，默认 helper 使用现有 `os.OpenFile(..., 0o755)`；当前 `download` 仅把原 open 调用改走 seam，不加入 checksum 行为。

```powershell
gofmt -w internal/update/self.go
go test -count=1 ./internal/update
```

Expected: PASS；这是可编译的行为不变重构，为后续 close failure Red 提供 seam。

- [ ] **Step 4：写 checksum/manifest 的 public Update Red 矩阵**

先升级成功 fixture：Release 增加 exact `SHA256SUMS.txt` 并返回手工计算的目标摘要；旧实现忽略它，成功测试仍保持 Green。随后新增 `TestSelfUpdateRejectsInvalidChecksumManifest`，全部通过现有 public `Update` 驱动：checksum asset missing/duplicate/negative metadata/>1MiB、response non-200/read error/actual oversize、target missing/duplicate、invalid target digest、malformed unrelated line、extra field、short/long digest。每个失败断言：`errors.As` 得到 `protocol.APIError`，数据边界为 `CodeDataFailure`、网络边界为 `CodeNetworkFailure`，错误文本不含 server/asset URL，old binary 不变、`AfterReplace=false`、staging 不存在。

成功子测试同时覆盖空行、GNU `*` marker 和 uppercase digest。预期摘要由固定 payload 的手工/标准库 test fixture 计算，不调用生产 parser。

```powershell
go test -count=1 -run '^(TestSelfUpdateDownloadsAndReplaces|TestSelfUpdateRejectsInvalidChecksumManifest)$' ./internal/update
```

Expected: invalid manifest cases以“old binary 被替换/无 error”等目标断言 FAIL；不得是 compile error。

- [ ] **Step 5：实现 bounded checksum fetch 与 strict parser 并 Green**

实现 `maxChecksumManifestSize = 1 << 20`、exact unique checksum asset、context-aware/User-Agent request、status/LimitReader、严格两字段 parser。测试 transport 捕获 request，断言 `User-Agent: mihari`、使用注入 client；增加 canceled context 用例，断言请求终止且 old binary 不变。所有非空行 digest 均须有效，目标 exact 且唯一。

```powershell
gofmt -w internal/update/self.go internal/update/self_test.go
go test -count=1 -run '^(TestSelfUpdateDownloadsAndReplaces|TestSelfUpdateRejectsInvalidChecksumManifest|TestSelfUpdateChecksumRequestUsesContextAndHeaders)$' ./internal/update
```

- [ ] **Step 6：写 verified binary 与 writer failure 的 public Update Red 矩阵**

新增 `TestSelfUpdateRejectsInvalidBinary`，覆盖 binary non-200、response read error、actual >128MiB（流式 reader）、positive metadata mismatch、digest mismatch。通过 `openCandidate` seam 覆盖 candidate open error、writer write error、writer close error。精确错误契约表：HTTP non-200/read error/write error/close error → `CodeNetworkFailure`；actual max/positive metadata mismatch/digest mismatch → `CodeDataFailure`；candidate open 保持现有 contextual raw error `create mihari candidate`（`errors.As` 不要求 APIError）。所有分支断言错误不含 URL、old binary、`AfterReplace=false`、staging/candidate cleanup。

```powershell
go test -count=1 -run '^TestSelfUpdateRejectsInvalidBinary$' ./internal/update
```

Expected: positive metadata mismatch 与 digest mismatch 以目标断言 FAIL；HTTP/read/actual-max/open/write/close 是既有错误与 cleanup 的 characterization Green；不得缺符号。

- [ ] **Step 7：实现一次写盘 verified download**

binary request 使用 `io.MultiWriter(writer, sha256.New())`；close 后验证 actual max、positive metadata size、digest。错误映射严格采用 Step 6 表，保持 candidate open 的既有 contextual raw error 与 write/close 的 `CodeNetworkFailure`；所有错误不包含 URL并清理 candidate。

```powershell
gofmt -w internal/update/self.go internal/update/self_test.go
go test -count=1 -run '^TestSelfUpdateRejectsInvalidBinary$' ./internal/update
```

Expected: PASS；Step 6 的两个行为 Red 与 characterization cases 全部 Green。

- [ ] **Step 8：补充 ambiguous target 的 Update 级 Green 集成验证**

新增 `TestSelfUpdateRejectsAmbiguousTargetAsset`，通过 public `Update` 覆盖 binary missing、duplicate exact、prefix-only、case-variant-only、archive/debug-only；统一断言 `CodeDataFailure`、错误不含 URL、old/AfterReplace/staging 不变。selector 单测与此 Update 级测试分别证明选择算法和 pre-replacement 保证。

扩展 `TestSelfUpdaterCheckDoesNotDownloadAsset`：分别统计 checksum 与 binary 请求，均为 0。same-version Update 也断言二者为 0。

```powershell
go test -count=1 -run '^(TestSelfUpdateRejectsAmbiguousTargetAsset|TestSelfUpdaterCheckDoesNotDownloadAsset|TestSelfUpdateSkipsSameVersion)$' ./internal/update
```

Expected: 全部 PASS。此矩阵不是新的 Red；其行为已由 Step 1 的 selector Red 驱动，当前步骤只证明 public `Update` 的 old/AfterReplace/staging fail-closed 集成保证。

- [ ] **Step 9：按 fail-closed 顺序接入 Update**

严格顺序：metadata → same-version return → exact unique binary → binary metadata bound → unique checksum asset → fetch/parse expected → staging → verified binary download → chmod → replace → `AfterReplace`。任何 pre-replace error 返回前 staging defer 清理。Step 4–8 的全部 public behavior tests在此步后 Green。

- [ ] **Step 10：验证 AQ-01 完整矩阵和邻接包**

```powershell
gofmt -w internal/update/self.go internal/update/self_test.go
go test -count=1 ./internal/update
go test -count=1 ./internal/cli
go test -count=1 ./internal/tui
go test -count=1 -race ./internal/update
```

Expected: 全部 PASS，且无公网访问。

- [ ] **Step 11：提交并通过 task review**

```powershell
git diff --check
git add internal/update/self.go internal/update/self_test.go
git commit -s -m "fix: 校验自更新二进制摘要"
```

Expected: 独立 AQ-01 commit；review clean 后进入 Task 4。

## Task 4：Phase 1 全量验收与文档闭环

**Files:**
- Modify: `docs/code-quality-audit-2026-08-13.md`
- Modify: `docs/superpowers/plans/2026-08-13-code-quality-roadmap.md`

**Interfaces:** 只记录实际证据，不改变生产接口。

- [ ] **Step 1：运行本机全量门禁**

```powershell
$unformatted = @(gofmt -l .)
if ($LASTEXITCODE -ne 0 -or $unformatted.Count) { $unformatted; throw 'gofmt failed' }
go vet ./...
if ($LASTEXITCODE -ne 0) { throw 'go vet failed' }
golangci-lint run ./...
if ($LASTEXITCODE -ne 0) { throw 'golangci-lint failed' }
go test -count=1 ./...
if ($LASTEXITCODE -ne 0) { throw 'go test run 1 failed' }
go test -count=1 ./...
if ($LASTEXITCODE -ne 0) { throw 'go test run 2 failed' }
go test -count=1 -race ./...
if ($LASTEXITCODE -ne 0) { throw 'go race failed' }
python scripts/install/test_parallel_download.py -v
if ($LASTEXITCODE -ne 0) { throw 'installer tests failed' }
```

Expected: 全部 exit 0；golangci-lint 为 0 issues。

- [ ] **Step 2：运行 coverage 三轮最终门禁**

```powershell
1..3 | ForEach-Object {
  $profile = Join-Path $env:TEMP "mihari-phase1-final-$_.out"
  go test -count=1 -covermode=atomic -coverpkg=./... -coverprofile="$profile" ./...
  if ($LASTEXITCODE -ne 0) { throw "coverage run $_ failed" }
  go run ./scripts/coverage-gate report -profile "$profile"
  if ($LASTEXITCODE -ne 0) { throw "coverage report $_ failed" }
}
```

- [ ] **Step 3：六目标 CGO-free 构建**

```powershell
$oldCGO = $env:CGO_ENABLED
$oldGOOS = $env:GOOS
$oldGOARCH = $env:GOARCH
$buildRoot = Join-Path $env:TEMP 'mihari-phase1-build'
New-Item -ItemType Directory -Force -Path $buildRoot | Out-Null
try {
  $env:CGO_ENABLED = '0'
  $targets = @(
    @('windows','amd64','mihari-windows-amd64.exe'),
    @('windows','arm64','mihari-windows-arm64.exe'),
    @('linux','amd64','mihari-linux-amd64'),
    @('linux','arm64','mihari-linux-arm64'),
    @('darwin','amd64','mihari-darwin-amd64'),
    @('darwin','arm64','mihari-darwin-arm64')
  )
  foreach ($target in $targets) {
    $env:GOOS = $target[0]
    $env:GOARCH = $target[1]
    $output = Join-Path $buildRoot $target[2]
    go build -trimpath -o $output ./cmd/mihari
    if ($LASTEXITCODE -ne 0) { throw "build failed: $($target[0])/$($target[1])" }
  }
} finally {
  $env:CGO_ENABLED = $oldCGO
  $env:GOOS = $oldGOOS
  $env:GOARCH = $oldGOARCH
}
```

Expected: 六个唯一 `$env:TEMP` 输出存在，仓库不产生 binary，环境变量恢复。

- [ ] **Step 4：更新审计和 roadmap**

仅根据实际结果将 AQ-01/AQ-04/AQ-05 标记为“本地整改完成”，记录 commit、平台、命令和次数。若没有远端 CI 证据，roadmap Phase 1 状态保持“实施中（本地门禁通过，三 OS CI 待验证）”，不得写“已验收”。AQ-02/AQ-03 保持开放。

- [ ] **Step 5：提交文档闭环并进行 Phase final review**

```powershell
git diff --check
git add docs/code-quality-audit-2026-08-13.md docs/superpowers/plans/2026-08-13-code-quality-roadmap.md
git commit -s -m "docs: 记录一期质量整改结果"
```

生成从 Phase 1 起点到 HEAD 的完整 review package，使用最强可用 reviewer 审查全部 commit。Critical/Important 必须修复并复审。

- [ ] **Step 6：请求 push/PR 授权并完成 CI-1**

push/PR 未由 commit 授权涵盖，必须请求用户。获得授权后推送本地完成 HEAD；记录 exact commit、run URL 和 lint、Windows/Linux/macOS unit、`test` fan-in、Windows race、vet-format、coverage、六目标 build、`cross-build` fan-in、DCO。全部 green 才进入 Step 7。未获授权则停在“本地实现完成”，不得进入 Phase 2。

- [ ] **Step 7：创建条件生效的 closure docs commit**

审计报告和 roadmap 记录 CI-1 的 commit/run/jobs；roadmap 状态列写受控 token `已验收`，相邻证据说明写入“此状态仅在本 closure commit 的 required jobs 全部 green 后生效”。提交：

```powershell
git diff --check
if ($LASTEXITCODE -ne 0) { throw 'closure docs diff check failed' }
git add docs/code-quality-audit-2026-08-13.md docs/superpowers/plans/2026-08-13-code-quality-roadmap.md
git commit -s -m "docs: 完成一期质量验收闭环"
if ($LASTEXITCODE -ne 0) { throw 'closure docs commit failed' }
```

对 closure docs diff 做独立 docs review，0 Critical/Important 后推送该 exact commit。

- [ ] **Step 8：CI-2 激活已提交状态**

closure commit 的 lint、三 OS unit、`test`、Windows race、vet-format、coverage、build、`cross-build`、DCO 必须全部 green。CI-2 通过 GitHub commit checks/PR 与 exact closure commit 关联；其成功使 Step 7 已提交的条件状态生效，不再创建第三个 tracked 状态 commit。保存外部 run URL 作为交付证据。

- [ ] **Step 9：最终工作区与提交清单核验**

```powershell
$dirty = @(git status --short)
if ($LASTEXITCODE -ne 0 -or $dirty.Count) { $dirty; throw 'worktree is not clean' }
git log --format='%h %s%n%(trailers:key=Signed-off-by)' --max-count=10
if ($LASTEXITCODE -ne 0) { throw 'git log failed' }
```

Expected: worktree clean；提交历史包含治理文档、AQ-05、AQ-04、AQ-01、本地证据和 closure docs 的 signed commits；`.superpowers/` scratch 因 ignore 不出现。

## Phase 1 完成定义

- Task 0–4 均有独立实现/文档 commit 和独立 task review；Phase final review clean。
- AQ-05 单 deadline/错误分类/created/single-secret/sidecar/cleanup 矩阵通过。
- AQ-04 focused 20 次、coverage 完整命令三次通过。
- AQ-01 exact asset/checksum metadata+response/parser/binary status+read+size+digest/cleanup 矩阵通过。
- 本机全量静态、test、race、coverage、installer、六目标 build 通过。
- 三 OS unit matrix 与 Windows race 有 CI-1/CI-2 green 证据，closure 状态已条件生效。
- 审计报告与 roadmap 记录真实证据；无临时产物或无关变更。
