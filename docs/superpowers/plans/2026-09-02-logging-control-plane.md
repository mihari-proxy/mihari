# Logging Settings 与控制面 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 `runtime.Manager` 收敛为唯一 Settings owner，在 `mihari.settings/v1` 中增加可省略 Logging 配置，并以稳定 `/v1/logging` 、session 重同步和 System Logging 区完成 daemon/TUI 热更新闭环。

**Architecture:** daemon bootstrap 用 outcome-aware API 加载/创建 Settings，Manager 构造时取得深拷贝并从此成为唯一 owner。所有 Settings mutation 在 Manager 的 maintenance 串行路径上使用深拷贝 candidate，锁外校验并用显式 commit outcome 原子保存，越过 replace commit point 后才短锁 publish；所有 runtime revision 写入也参加同一 maintenance gate，用户 mutation 取得 gate 后复查 degraded，后台 observation 可继续但不能插入外部副作用与最终 coordinator 提交之间。请求取消可终止 gate 等待和慢 IO，但不能截断已越过 commit point 的本地 publish/revision/cache 收口。`onboarding.Service` 缩为只持有 `onboarding.json`。补偿写无法 commit 时，Manager 对齐已提交磁盘、由 coordinator 记录 revision+degraded 后拒绝后续 mutation。Logging PATCH committed 后向 PR 1 的 `logging.Group` 应用完整配置。TUI 以连接 epoch + 单调 revision 只采用未过期完整状态，并由 `tui.Run` 所有的可取消串行 applier 应用到本地 writer。

**Tech Stack:** Go 1.26.5，既有 `internal/config`、`internal/state.Coordinator`、`internal/control/{protocol,server,client}`、Bubble Tea v2，PR 1 `internal/logging`。

**Spec:** `docs/superpowers/specs/2026-09-02-file-logging-export-design.md`

**Depends on:** `docs/superpowers/plans/2026-09-02-file-logging-foundation.md` 完成并通过验收。

**Worktree:** `.worktrees/feat-file-logging-export`

**Branch:** `feat/file-logging-export`
**Delivery:** 本文档对应 spec 第 2 个顺序 PR，目标分支为 `dev`。

## Global Constraints

- daemon 构造 Manager 前只允许 `LoadOrCreateWithSidecarOutcome` 执行一次 bootstrap 写；replace 后 sync Warning 不伪装成启动失败，并在 logger 建立后净化上报。Manager 构造时深拷贝 Settings；此后它是进程内唯一 owner 和 `mihari.yaml` 的唯一写入者，`onboarding.Service` 不再保存 Settings 副本。
- 不在 `settingsMu` 内做磁盘、网络、进程或 controller IO。串行性由现有 `maintenance` 锁保证，`settingsMu` 只保护 snapshot/publish。
- candidate 必须深拷贝 `Tun map[string]any` 的嵌套 map/slice；不允许失败 Save 通过共享 map 泄漏到 Manager。
- 单文件 replace 是 commit point：replace 前错误意味着磁盘未变；replace 后 directory-sync 错误只作为 `CommitResult.Warning`，调用方仍 publish 并成功提交 revision。不得把 post-commit warning 当成回滚条件。
- onboarding/system proxy/TUN 等补偿写若在 commit 前失败，Manager 必须对齐磁盘已提交值、通过 coordinator committed-error 增加 revision 并把 health 置 degraded；本进程后续 mutation（包括已经通过 `doOperation` 入口但仍在等待 maintenance 的请求）取得 gate 后返回 `invalid_state`，只读和 Export 保持可用，重启重新装载后解除。
- Logging 默认 `info/10/3`，默认值不序列化 `log:`；任一非默认值序列化完整的三字段块；恢复默认删除该块。YAML 缺字段或显式 `0` 在加载时按默认补齐；只有 PATCH 显式 `0` 在 server/domain 拒绝。
- 协议数值字段使用 `*int64`；HTTP handler 与 runtime/domain 双重验证。校验通过前不做 MiB→bytes 或 `int64`→`int` 转换。
- 所有 `internal/runtime` 的 `Coordinator.Do` 写入都必须发生在持有同一 `maintenance` gate 时；用户 mutation 用 `lockMutation`，只读 GET 和后台 state observation 用 `lockMaintenance`。同一 gate 内读取 Settings snapshot、dir 和 global revision，禁止 GET 拼出“新配置 + 旧 revision”，也禁止后台 core/subscription observation 在 system proxy/TUN 的 revision 预检与提交之间插入。
- daemon 启动时用已加载 `settings.EffectiveLogging()` 打开/Apply logging.Group，首条 JSONL 即使用持久化自定义 level/limits，不得等下一次 PATCH。
- `logging.Group.Apply` / `Runtime.Apply` 签名为有意无 error 的 `Apply(context.Context, logging.Config)`：所有 target 的 level/limits 内存替换同步且不会失败；`Group.Apply` 先对全部 target `swapConfig` 再逐个 `convergeArchives`。archive 收敛的 **锁获取**才受 context/250ms deadline 约束并只上报 warning，获锁后的本地文件系统调用不可抢占。PATCH 传请求 context。TUI 不把 Apply 作为裸 Bubble Tea Cmd，而由 `tui.Run` 创建的单 worker applier 传其可取消 context，并在退出时 `CloseAndWait`。
- mutation 在取得 gate、完成 revision 预检后才进入副作用阶段；越过 Settings replace 或外部成功副作用后，必须用 `context.WithoutCancel(requestCtx)` 完成短小的内存 publish、`updateStateLocked` 与 operation-cache 结果记录。该 context 不得用于 Save、controller、OS、下载或进程等慢 IO。
- 新 operation ID 的 no-op PATCH 仍增加一次 revision，但可跳过 YAML Save 和 runtime Apply；同 ID 由 `doOperation("logging:"+id)` 返回首次结果。
- Logging 热更新不设 restart required，不改 onboarding DTO，不增 CLI，不修改 `CHANGELOG.md`。
- 全局 revision 溢出不在本计划处理；跟踪 Issue [#191](https://github.com/mihari-proxy/mihari/issues/191)。
- System 页离线时不显示伪默认值，不允许提交；TUI writer 仍使用 debug/100 MiB/10 bootstrap。session 在重连/capability 消失时增加 logging epoch；root 只跟随。只采用相同 epoch 且 revision 不小于当前值的 GET/PATCH/Event 状态。
- 实际 commit 仅在用户明确要求时创建；计划中的 commit 是审查边界。

## File Structure

| 文件 | 职责 |
| --- | --- |
| `internal/config/{atomic,settings}.go` | replace commit outcome；outcome-aware bootstrap sidecar；`LoggingSettings`、effective/default/canonical save、Settings 深拷贝 |
| `internal/state/coordinator.go` | committed-error 仍存储 next snapshot/revision，同时向调用方返回错误 |
| `internal/runtime/settings.go` | candidate prepare/save/publish/rollback 唯一入口 |
| `internal/runtime/{manager,geoip,sysproxy,tun,onboarding,subscription,panel,preferences}.go` | 所有 revision 写入统一 maintenance；迁移 Settings mutation；subscription 只读 `settingsSnapshot()` |
| `internal/onboarding/service.go` | 只保存 onboarding complete state |
| `internal/runtime/logging.go` | Logging status/PATCH 与 `logging.Group` 热应用 |
| `internal/control/protocol/logging.go` | 稳定 DTO |
| `internal/control/server/logging.go` | GET/PATCH、边界验证、错误映射 |
| `internal/control/client/runtime.go` | 类型化 Logging client |
| `internal/tui/session/{client,session}.go` | revision-aware GET 与 ordered event |
| `internal/tui/logging_applier.go` | latest-wins 串行 Apply、cancel、join 与关闭 gate |
| `internal/tui/model.go` | logging epoch/revision 单调观察、保守 reset、向 applier Submit 完整状态和 System 同步 |
| `internal/tui/ui/logging.go` | root/System 共用的 epoch-tagged Logging sync/observed message |
| `internal/tui/pages/system/model.go` | Logging section、edit/cycle/PATCH/conflict reload |
| `internal/tui/ui/{focus,keymap,strings}.go` | `ModeLoggingEdit` 和稳定文案 |

---

### Task 1: Logging settings 持久化契约与深拷贝

**Files:**
- Modify: `internal/config/atomic.go`
- Modify: `internal/config/atomic_test.go`
- Modify: `internal/config/settings.go`
- Modify: `internal/config/settings_test.go`

**Interfaces:**

```go
const (
	DefaultLogLevel     = "info"
	DefaultLogMaxSizeMB int64 = 10
	DefaultLogMaxFiles  int64 = 3
)

type LoggingSettings struct {
	Level     string `yaml:"level"`
	MaxSizeMB int64  `yaml:"max-size-mb"`
	MaxFiles  int64  `yaml:"max-files"`
}

func DefaultLoggingSettings() LoggingSettings
func (s Settings) EffectiveLogging() LoggingSettings
func (s *Settings) SetLogging(LoggingSettings)
func (s Settings) Clone() Settings

type CommitResult struct {
	Committed bool
	Warning   error
}
func AtomicWriteWithCommit(path string, content []byte, mode os.FileMode) (CommitResult, error)
func SaveWithCommit(path string, settings Settings) (CommitResult, error)
func LoadOrCreateWithSidecarOutcome(path, sidecar string) (settings Settings, created bool, result CommitResult, err error)
```

`Settings` 新增 `Logging *LoggingSettings `yaml:"log,omitempty"``。`SetLogging(default)` 设 nil，非默认时存入完整副本。

- [ ] **Step 1: 写 commit-point 与 load/validate/save Red**

给 `writeAtomic` 增加可注入 replace/sync ops：写 temp、Sync、Close 或 replace 前失败返回 error、`Committed=false` 且旧目标不变；replace 成功返回 `Committed=true`，随后 parent sync 失败只放入 `Warning` 且 error 为 nil，目标是完整新内容。`SaveWithCommit` 保留相同语义并 canonicalize Settings。对 `LoadOrCreateWithSidecarOutcome` 分别覆盖首次创建、已有 Settings 应用 sidecar、已有文件无变化：前两者 replace 前失败返回 error/未 committed，replace 后 sync 失败返回已加载的 after Settings、`Committed=true`/Warning 且 err=nil；无写入返回零值 CommitResult。再覆盖：无 `log` 得到 info/10/3；空/部分 block 以及 YAML 显式 `max-size-mb: 0` / `max-files: 0` 都按默认补齐（加载路径不能区分缺省与零值，spec 要求两者都当默认）；debug/info/warn/error 合法；非法 level、size 101、files 11 拒绝。PATCH 显式 0 的拒绝放在 Task 6/7，不在 YAML 加载测试里。非默认 round-trip 后三字段完整；默认和恢复默认时 YAML 不含 `log:`。

```powershell
go test -run '^TestSettingsLogging_' ./internal/config
```

Expected: FAIL，缺少 Logging 类型/方法。

- [ ] **Step 2: 实现 effective 与 canonical Save**

`AtomicWriteWithCommit` 在 replace 返回 nil 的瞬间构造 `Committed=true`；之后的 sync-directory 错误只能写入 `Warning`。现有 `AtomicWrite` wrapper 调 outcome 版本：pre-commit error 原样返回，post-commit Warning 仍作为 error 返回以保持旧调用方行为；`Save` 同样包装 `SaveWithCommit`。`LoadOrCreateWithSidecarOutcome` 内部只能调用 `SaveWithCommit`；旧 `LoadOrCreateWithSidecar` 包装 outcome 版本并把 Warning 作为 error 返回以保持兼容，但 PR 2 生产 daemon 装配改用 outcome 入口。本计划所有 Settings/onboarding mutation 改用 outcome 版本，不能再用旧 wrapper 推断 commit 状态。`EffectiveLogging` 对 nil/零字段补默认；`Validate` 校验 effective 值。`SaveWithCommit` 复制 settings，执行 `canonical.SetLogging(settings.EffectiveLogging())`，只 marshal canonical，不修改调用者。

- [ ] **Step 3: 写嵌套 Tun clone Red 并实现**

构造 `map[string]any{"dns": map[string]any{"nameserver": []any{"a"}}}`，修改 clone 的 map/slice 后断言原 Settings 不变；Logging pointer 也必须独立。递归只处理 YAML 可产生的 `map[string]any`、`map[any]any`、`[]any`，标量原样返回。

```powershell
gofmt -w internal/config/atomic.go internal/config/atomic_test.go internal/config/settings.go internal/config/settings_test.go
go test ./internal/config
```

Expected: PASS。

- [ ] **Step 4: Commit（仅得到授权时）**

```powershell
git add internal/config/atomic.go internal/config/atomic_test.go internal/config/settings.go internal/config/settings_test.go
git commit -s -m "feat: 增加日志配置与持久化提交结果"
```

---

### Task 2: Manager 唯一 candidate 提交入口

**Files:**
- Create: `internal/runtime/settings.go`
- Create: `internal/runtime/settings_test.go`
- Modify: `internal/state/coordinator.go`
- Modify: `internal/state/coordinator_test.go`
- Modify: `internal/runtime/manager.go`
- Modify: `internal/runtime/manager_test.go`
- Modify: `internal/runtime/geoip.go`
- Modify: `internal/runtime/onboarding.go`
- Modify: `internal/runtime/panel.go`
- Modify: `internal/runtime/preferences.go`
- Modify: `internal/runtime/webgui.go`
- Modify: `internal/runtime/subscription.go`
- Modify: `internal/runtime/subscription_test.go`
- Modify: `internal/runtime/sysproxy.go`
- Modify: `internal/runtime/tun.go`

**Interfaces:**

```go
type settingsCandidate struct {
	before  config.Settings
	after   config.Settings
	changed bool
}

func (m *Manager) settingsSnapshot() config.Settings
func (m *Manager) prepareSettings(func(*config.Settings) error) (settingsCandidate, error)
func (m *Manager) saveSettingsCandidate(settingsCandidate) (config.CommitResult, error)
func (m *Manager) publishSettings(settingsCandidate)
func (m *Manager) updateSettings(func(*config.Settings) error) (settingsCandidate, error)
func (m *Manager) restoreSettings(config.Settings) (config.CommitResult, error)
func (m *Manager) enterMutationDegraded(snapshot *state.Snapshot) error
func (m *Manager) checkIfRevision(*uint64) error
func (m *Manager) lockMaintenance(context.Context) error
func (m *Manager) lockMutation(context.Context) error
func (m *Manager) updateStateLocked(context.Context, state.CommandMeta, func(state.Snapshot) (state.Snapshot, error)) (state.Snapshot, error)

// CommittedError marks a mutation whose snapshot must be stored even though the caller receives Err.
type CommittedError struct { Err error }
```

`state.CommittedError` 实现 `Error`/`Unwrap`。`Coordinator.Do` 遇到普通 error 仍不存 snapshot；遇到 `CommittedError` 则先把 `next.Revision=current.Revision+1` 并 Store，再返回 `next` 和包装后的原错误。这样补偿失败后的实际磁盘状态能推进 revision，同时保留稳定 APIError。Manager 构造时把 `options.Settings.Clone()` 保存为唯一初始值，调用者随后修改原 map/slice/pointer 不得影响 Manager。

Manager 新增进程内 `mutationDegraded atomic.Bool`。唯一签名是 `enterMutationDegraded(snapshot *state.Snapshot) error`：置位、把传入 snapshot 的 Health 设为 `degraded`、LastError 固定为 `mutation compensation failed; restart required`，并返回包装 `data_failure: mutation compensation failed` 的 `state.CommittedError`。调用方必须已持 maintenance gate，helper 内不得再调 `Coordinator.Do` 或再次取 gate。调用方不能注入底层文本。此固定分类同时覆盖 Settings 回写失败和 system proxy live restore 失败，不把 OS/path 底层错误写入稳定状态。`lockMaintenance` 只取得现有 channel gate并检查 closing；GET/status 和后台 runtime observation 使用它，在 degraded 时仍可运行。`lockMutation` 先取得同一 gate，再检查 closing/degraded；检查失败时由 helper 自己归还 gate。因此已经通过 `doOperation` 入口、之后排队的请求也不能越过 degraded。`doOperation` 仍必须先查已有 operation ID cache：触发 degraded 的同 ID 重试返回首次缓存的 `data_failure`；未命中新缓存的执行闭包必须在副作用前调用 `lockMutation`。`updateSettings` 在任何 prepare/save 前再做防御性 degraded 检查；`restoreSettings` 是当前失败事务的补偿路径，不受该检查阻挡。

`updateStateLocked` 是 `internal/runtime` 写 Coordinator 的唯一入口，调用者必须已经持有 maintenance；生产文件中除该 helper 外不得直接出现 `m.coordinator.Do`。现有 `markConfigDegraded` 发生在订阅 mutation 已经持 lock 且外层 `Do` 返回之后，必须改为直接 `updateStateLocked(context.WithoutCancel(ctx), …)`，禁止再 `lockMaintenance`/`Coordinator.Do`（会与外层 `lockMutation` 自死锁）。system proxy/TUN 等用户 mutation 从 revision 预检、Settings 提交、外部副作用直到 `updateStateLocked` 都持续持有 `lockMutation`；`setCoreState`、subscription runtime sync 等后台 observation 先取得 `lockMaintenance`，因此只能在正在执行的 mutation 完成后推进 revision。用户事务一旦已有 committed fact，必须以 `context.WithoutCancel(requestCtx)` 调短小的 `updateStateLocked` 并收口 operation cache；后台 observation 仍使用自身 context。所有 GET/status、日志写入与 PR 3 Export 不检查 degraded 位。重启通过正常构造新 Manager 自然解除。`runtime.Options` 新增 `SaveSettings func(string, config.Settings) (config.CommitResult, error)`，默认 `config.SaveWithCommit`；`SettingsPath==""` 时不调用 saver而返回 `CommitResult{Committed:true}`，随后照常 publish 内存。Manager 复用既有 `OnBackgroundError` 上报 Warning，但只传 `component=settings` 与固定 `parent directory sync failed after commit`，不得把可能含完整路径的底层 Warning 或其文本返回协议/直接写 stderr。

- [ ] **Step 1: 写 candidate/Save 失败 Red**

断言：构造 Manager 后修改调用方原 Settings 的 Logging pointer/Tun 深层 map 不影响 snapshot；update 修改 candidate 不会提前改 Manager；validation 失败不调 saver；`Committed=false`+error 后内存与深层 Tun 不变；`Committed=true`+Warning 时 publish candidate、调用 warning reporter 并返回成功；saver 调用期间另一 goroutine可读 settings snapshot，证明未持 `settingsMu`；no-op 不调 saver/publish；`restoreSettings` 只在回滚 `Committed=true` 后 publish。Coordinator 测试普通 error 不存 next，`state.CommittedError{Err: APIError(data_failure)}` 存 next、revision 恰加一，且 `errors.As` 仍取得原 APIError。Manager gate 测试先让 operation A 通过 `doOperation` 并排队等待 maintenance，operation B 在持锁时触发 degraded；放锁后 A 必须在副作用 spy 前得到 `invalid_state`。触发 degraded 的 operation ID 重试仍得到缓存的原 `data_failure`；GET 和后台 `setCoreState` 在 degraded 时可运行，但必须等当前 mutation 释放 gate后才增加 revision。另在 saver 返回 `Committed=true` 后取消 request context，断言 Manager 仍用 without-cancel context 完成 publish/revision/cache；相同 operation ID 重试得到首次成功，不重复 Save。

```powershell
go test -run '^TestManagerSettings_' ./internal/runtime
```

Expected: FAIL，缺少 candidate helper。

- [ ] **Step 2: 实现短锁 snapshot/publish 与稳定错误**

`NewManager` 和 `prepareSettings` 都使用 `Settings.Clone()`；后者更新后 `Validate`，以 `reflect.DeepEqual` 计算 changed。`saveSettingsCandidate` 不持 mutex；只有 `Committed=false` 的底层 error 映射为 `protocol.APIError{Code: data_failure, Message: "persist settings"}`，`Committed=true` 的 Warning 交 reporter 后视为保存成功。防御性检查非法组合（error 与 `Committed=true`，或 nil error 与 `Committed=false`）并映射为 data failure，不能猜测磁盘状态；默认 config 实现不会产生非法组合。`publishSettings` 在短锁内存入 clone。实现 `lockMaintenance`/`lockMutation`/`updateStateLocked`，把所有 runtime mutation 的旧 `m.lock(ctx)` 改为 `lockMutation`，GET/status 改为 `lockMaintenance`；后台 coordinator writer 显式取得 `lockMaintenance`。禁止在已经持 gate 的路径里再次获取造成重入死锁。

- [ ] **Step 3: 统一读路径**

`WebListenAddr`、`webgui.go`、core channel 以及 `internal/runtime/subscription.go` 中 `core.BootstrapConfig(m.settings)` / `subscription.Generate(..., m.settings)` 全部改为 `settingsSnapshot()`。`subscription.go` 的 prepare 可发生在 maintenance 外，直接读 `m.settings` 会与 Logging PATCH 形成 data race。此步不迁移业务语义，但要把所有 coordinator writer 路由到 `updateStateLocked`。最终验收同时运行 `rg 'm\.settings' internal/runtime` 与 `rg 'm\.coordinator\.Do' internal/runtime --glob '*.go' --glob '!**/*_test.go'`；后一个只能命中 `updateStateLocked` 的实现。

```powershell
gofmt -w internal/runtime/settings.go internal/runtime/settings_test.go internal/state/coordinator.go internal/state/coordinator_test.go internal/runtime/manager.go internal/runtime/manager_test.go internal/runtime/geoip.go internal/runtime/onboarding.go internal/runtime/panel.go internal/runtime/preferences.go internal/runtime/webgui.go internal/runtime/subscription.go internal/runtime/subscription_test.go internal/runtime/sysproxy.go internal/runtime/tun.go
go test ./internal/state ./internal/runtime
go test -race ./internal/state ./internal/runtime
```

Expected: PASS。`rg 'm\.settings' internal/runtime` 只剩 `settingsSnapshot`/`publishSettings` 等 helper 内部写入。

- [ ] **Step 4: Commit（仅得到授权时）**

```powershell
git add internal/runtime/settings*.go internal/state/coordinator.go internal/state/coordinator_test.go internal/runtime/manager.go internal/runtime/manager_test.go internal/runtime/webgui.go internal/runtime/subscription.go internal/runtime/subscription_test.go
git commit -s -m "refactor: 集中 Manager Settings candidate 提交"
```

---

### Task 3: 迁移 core/system proxy/TUN Settings mutation

**Files:**
- Modify: `internal/runtime/manager.go`
- Modify: `internal/runtime/manager_test.go`
- Modify: `internal/runtime/sysproxy.go`
- Modify: `internal/runtime/sysproxy_test.go`
- Modify: `internal/runtime/tun.go`
- Modify: `internal/runtime/tun_test.go`

- [ ] **Step 1: 先写顺序与失败回归 Red**

用注入 outcome saver 覆盖：core channel 与已有 Tun/log-independent 字段不互相覆盖；setup sidecar stale revision 在 Save 前拒绝。system proxy 首次 replace 前失败时 Manager desired 不变且 OS write spy 为零；foreign/not-owned 与 TUN-conflict 在 candidate save 之前拒绝，零 YAML 写入、零 OS write、desired 不变；replace 后 Warning 仍 publish并继续 OS apply；OS apply 失败时补偿 `Committed=true` 后 publish old，补偿在 commit 前失败则保持与磁盘一致的 next 内存、coordinator revision +1、health degraded。TUN 首次 save 在 commit 前失败不 publish；controller apply/live 失败时同样补偿；回滚在 commit 前失败则保持与磁盘一致的 next 内存，coordinator revision +1、health degraded、当前请求返回 `data_failure`，之后另一 operation ID 的 mutation返回 `invalid_state`，GET 仍读到 next。另覆盖：foreign/TUN-conflict 在 save 前拒绝且零 YAML。TUN apply 已成功且 live 已确认之后、coordinator commit 之前取消：用 without-cancel 完成 revision/cache，后台 `setCoreState` 阻塞到它完成且随后 revision 再独立加一。live 确认尚未完成时取消：走补偿，不把未确认 live 当成功。不得在已经产生 committed fact 后返回 context error 或迟到 conflict。

```powershell
go test -run '^(TestInstall.*Settings|TestSystemProxy.*Settings|TestTun.*Settings)' ./internal/runtime
```

Expected: 至少一条因当前先改 `m.settings` 再 Save 而 FAIL。

- [ ] **Step 2: 迁移 core channel**

setup sidecar fast path 在 `DetectVersion` 成功后获 `lockMutation`，先在同一 gate 内完成 closing/degraded 与 `IfRevision` 预检，再用 candidate 调 `ApplyCoreChannelSidecar`，最后以 without-cancel context提交 revision；普通 install 的 `CoreChannel=channel` 也用 `updateSettings`。不把下载/Prepare 移入锁内，不扩大 core binary transaction 语义。

- [ ] **Step 3: 迁移 system proxy 与 TUN**

system proxy 改为与 TUN 相同的 Settings-first 候选事务：取得 `lockMutation` 并预检 revision，捕获 before Settings/observed OS。**candidate save 之前**完成 foreign / not-owned / TUN-conflict（及 force）判断；这些拒绝必须零 YAML 写入、零 OS write、desired 不变。通过门闩后才 `updateSettings(next desired)`，committed 后才 Enable/Disable；首次 Save 在 commit 前失败时 OS write 必须为零。OS apply 失败调 `restoreSettings(before)`，并以 held gate 下的 `restoreSystemProxy(beforeObserved)` best-effort 恢复 live state：before disabled 调 Disable，before enabled 则用 `net.SplitHostPort` 解析其规范化 Server 后调 Enable；空值、解析或 backend 失败都算 restore 失败。live restore 失败或 Settings 回滚在 commit 前失败都通过 `enterMutationDegraded` 的 committed-error 推进 revision并进入 degraded。daemon 启动 reconcile 在 desired=false 时只清除与 Mihari target 匹配的 observed proxy，绝不清除 foreign proxy，使重启可收敛一次失败后残留的 Mihari-owned live state。TUN 同样在 gate 内从当前 snapshot 生成 `before`/next，初次 `updateSettings` 成功后才 apply controller，apply/live 失败调 `restoreSettings(before)`。回滚 committed（含 Warning）时内存回到 before并返回普通错误，coordinator 不增 revision；回滚 commit 前失败时磁盘仍是 next，确保内存也是 next，然后对当前 `updateStateLocked` callback 的 snapshot 调 `enterMutationDegraded`，让 coordinator 以 committed-error推进一次 revision。外部 apply 与 live/OS 确认仍使用请求 ctx；取消或确认失败走补偿，`WithoutCancel` 不得包住 `controller.Configs` 或 `sysProxy.Get`。apply **与 live 确认都已成功** 之后，不得把随后的 `ctx.Err()` 当成未生效；只用 `context.WithoutCancel(ctx)` 完成 `updateStateLocked` 和成功结果缓存。最终 status 构造也不能仅因原 context 已取消把已提交 mutation 改报失败。`lockMaintenance`/`lockMutation` 不可重入。`Restart`/`Install` 在同步等待 `supervisor.Restart` 之前必须释放 gate（更新 `TestRuntimeMutationWaitsForRestart`）。`setCoreState`/`Observe` **始终** `lockMaintenance`，不得用进程级 held 标志在用户 mutation 的 IfRevision 与提交之间插入 revision。同栈补偿路径（如 `markConfigDegraded`）才直接 `updateStateLocked`。启动 reconcile 的 `ApplyDesiredSystemProxy`（含 desired=false 时 Disable owned proxy）必须持 `lockMaintenance` 或 `lockMutation`，不得与用户 Enable 无锁交错。state 失败但 Settings 回滚已 committed 时，返回映射后的稳定 `persist settings`/`data_failure`，不得把带路径的 `%w` 底层错误送出协议。不得先释放 maintenance 再在末尾调用 coordinator，也不得在已持 gate 时调用会再次获取 gate 的 helper。删除 `persistSettings()` 和所有就地 mutation。

```powershell
gofmt -w internal/runtime/manager.go internal/runtime/manager_test.go internal/runtime/sysproxy.go internal/runtime/sysproxy_test.go internal/runtime/tun.go internal/runtime/tun_test.go
go test ./internal/runtime
go test -race ./internal/runtime
```

Expected: PASS；`rg -n 'm\.settings\.[A-Za-z]+\s*=|persistSettings' internal/runtime` 不再找到 helper 之外的 Settings 就地写。

- [ ] **Step 4: Commit（仅得到授权时）**

```powershell
git add internal/runtime/manager.go internal/runtime/manager_test.go internal/runtime/sysproxy.go internal/runtime/sysproxy_test.go internal/runtime/tun.go internal/runtime/tun_test.go
git commit -s -m "fix: 迁移 Settings mutation 到候选提交路径"
```

---

### Task 4: 将 onboarding 缩为 state-only 并由 Manager 跨文件编排

**Files:**
- Modify: `internal/onboarding/service.go`
- Modify: `internal/onboarding/service_test.go`
- Modify: `internal/runtime/onboarding.go`
- Modify: `internal/runtime/manager.go`
- Modify: `internal/runtime/manager_test.go`
- Modify: `internal/app/runtime.go`
- Modify: `internal/app/runtime_test.go`

**Interfaces:**

```go
type State struct { Complete bool }

type Options struct {
	StatePath            string
	InitialSetupRequired bool
	SaveState            func(string, State) (config.CommitResult, error)
	OnPersistenceWarning func(error)
}

func (s *Service) State() State
func (s *Service) Update(complete *bool) (State, error)
```

`onboarding.Status`、`Snapshot`、`Update` 保留为 runtime/control 域类型，但 Service 不再保存 endpoints、SettingsPath、Settings 或 restartRequired。`SaveState` 默认用 `config.AtomicWriteWithCommit` 编码 `onboarding.json`；replace 前错误不改 Service 内存，committed+Warning 更新内存并经 callback 报警后返回成功。callback 只能收到固定 `onboarding parent directory sync failed after commit`，不得转发底层路径。Manager 新增 `onboardingRestartRequired bool`，在正常 endpoint commit 或补偿失败导致 after Settings 留存时置 true。

- [ ] **Step 1: 写 state-only Service Red**

断言 Open 只创建/读取 `onboarding.json`，Update nil 是 no-op，Update true/false 原子持久化；`Committed=false`+error 不改内存，`Committed=true`+Warning 更新内存、调用 warning callback 且返回成功。删除旧测试对 Service endpoint 的断言，将它们迁到 runtime test。

```powershell
go test ./internal/onboarding
```

Expected: 新 state-only 测试 FAIL，因 Service 仍持有 Settings。

- [ ] **Step 2: 写 Manager 跨文件顺序 Red**

覆盖 Logging-independent endpoint update：Settings candidate 先 committed 但不 publish；onboarding state committed（含 post-commit Warning）后 publish settings/restartRequired；state 在 commit 前失败时回滚 settings file，回滚 committed 后内存仍 before；回滚也在 commit 前失败时磁盘必为 after Settings + before state，Manager publish after、保留 before state、restartRequired=true，coordinator revision +1/health degraded，当前请求返稳定 `data_failure`，后续 mutation 为 `invalid_state`。Status 永远从 Manager snapshot 组合 endpoints，不读 Service 副本。

- [ ] **Step 3: 实现 runtime 编排**

编排形状必须与 Task 3 相同，**禁止**把 Settings/onboarding 磁盘 replace 放进 `Coordinator.Do` callback：

```go
if err := m.lockMutation(ctx); err != nil { return err }
defer m.unlock()
if err := m.checkIfRevision(meta.IfRevision); err != nil { return err } // save 前预检；失败零磁盘 IO
candidate, err := m.prepareSettings(applyEndpointUpdate(update))
if err != nil { return err }
if _, err := m.saveSettingsCandidate(candidate); err != nil { return err } // 可取消慢 IO，仍在 gate 内
updatedState, err := m.onboarding.Update(update.Complete)
if err != nil {
	if candidate.changed {
		if rollback, rollbackErr := m.restoreSettings(candidate.before); rollbackErr != nil && !rollback.Committed {
			m.publishSettings(candidate)
			m.onboardingRestartRequired = true
			_, degErr := m.updateStateLocked(context.WithoutCancel(ctx), meta, func(snapshot state.Snapshot) (state.Snapshot, error) {
				degErr := m.enterMutationDegraded(&snapshot)
				return snapshot, degErr
			})
			return degErr
		}
	}
	return mapPersistError(err)
}
m.publishSettings(candidate)
m.onboardingRestartRequired = m.onboardingRestartRequired || candidate.changed
_, err = m.updateStateLocked(context.WithoutCancel(ctx), meta, func(snapshot state.Snapshot) (state.Snapshot, error) {
	return snapshot, nil
})
```

`composeOnboardingStatus` 从 `updatedState` 取 complete，从 Manager Settings snapshot 取 endpoints，从 Manager 取 restartRequired。`enterMutationDegraded` 就地修改传入 snapshot 的 Health/LastError 并返回 `CommittedError`。callback 必须先调用 helper 再 `return snapshot, degErr`，让 coordinator 存下已 degraded 的 next；禁止 `return snapshot, m.enterMutationDegraded(&snapshot)`（Go 先拷贝未改 snapshot）。禁止在 helper 内另调 coordinator。state 失败且 Settings 回滚已 committed 时，把 `err` 映射为稳定 `data_failure`/`persist settings` 再返回。Settings 已 replace 后请求取消仍必须完成本地 publish/revision/cache。`app.BuildRuntimeWithOptions` 只给 Service 传 StatePath/initial flag、outcome saver 与脱敏 warning callback。

```powershell
gofmt -w internal/onboarding/service.go internal/onboarding/service_test.go internal/runtime/onboarding.go internal/runtime/manager.go internal/runtime/manager_test.go internal/app/runtime.go internal/app/runtime_test.go
go test ./internal/onboarding ./internal/runtime ./internal/app
go test -race ./internal/onboarding ./internal/runtime ./internal/app
```

Expected: PASS。

- [ ] **Step 4: Commit（仅得到授权时）**

```powershell
git add internal/onboarding internal/runtime/onboarding.go internal/runtime/manager.go internal/runtime/manager_test.go internal/app/runtime.go internal/app/runtime_test.go
git commit -s -m "refactor: 收敛 onboarding Settings 所有权"
```

---

### Task 5: 稳定 Logging DTO

**Files:**
- Create: `internal/control/protocol/logging.go`
- Create: `internal/control/protocol/logging_test.go`
- Modify: `internal/control/protocol/status.go`
- Modify: `internal/control/protocol/runtime_test.go`

**Interfaces:**

```go
const CapabilityLogging = "logging"

type LoggingStatus struct {
	Schema    string `json:"schema"`
	Revision  uint64 `json:"revision"`
	Level     string `json:"level"`
	MaxSizeMB int64  `json:"max_size_mb"`
	MaxFiles  int64  `json:"max_files"`
	Dir       string `json:"dir"`
}

type LoggingUpdateRequest struct {
	OperationID string  `json:"operation_id"`
	IfRevision  *uint64 `json:"if_revision,omitempty"`
	Level       *string `json:"level,omitempty"`
	MaxSizeMB   *int64  `json:"max_size_mb,omitempty"`
	MaxFiles    *int64  `json:"max_files,omitempty"`
}
```

本任务只落地类型与 JSON 契约，不注册 HTTP 路由。Task 6 的 runtime 测试从第一天 `import protocol` 即可编译。

- [ ] **Step 1: 写 DTO exact JSON Red**

断言 schema/revision/level/max_size_mb/max_files/dir exact keys，`if_revision:&zero` 编码为 `0`，nil 字段省略。`CapabilityLogging="logging"` 与现有 `CapabilityLogs="logs"` 同时保留。

```powershell
go test -run '^TestLogging' ./internal/control/protocol
```

Expected: FAIL，类型不存在。

- [ ] **Step 2: 实现类型并验证**

```powershell
gofmt -w internal/control/protocol/logging.go internal/control/protocol/logging_test.go internal/control/protocol/status.go internal/control/protocol/runtime_test.go
go test ./internal/control/protocol
```

Expected: PASS。

- [ ] **Step 3: Commit（仅得到授权时）**

```powershell
git add internal/control/protocol/logging.go internal/control/protocol/logging_test.go internal/control/protocol/status.go internal/control/protocol/runtime_test.go
git commit -s -m "feat: 增加 Logging v1 DTO"
```

---

### Task 6: Logging runtime domain 与原子 mutation

**Files:**
- Create: `internal/runtime/logging.go`
- Create: `internal/runtime/logging_test.go`
- Modify: `internal/runtime/manager.go`
- Modify: `internal/runtime/capabilities.go`
- Modify: `internal/runtime/capabilities_test.go`
- Modify: `internal/runtime/subscription.go`
- Modify: `internal/runtime/subscription_test.go`
- Modify: `internal/app/runtime.go`
- Modify: `cmd/mihari/main.go`
- Modify: `cmd/mihari/main_test.go`

**Interfaces:**

```go
type LoggingRuntime interface {
	Apply(context.Context, logging.Config)
	Config() logging.Config
	Dir() string
}

type LoggingUpdate struct {
	Level     *string
	MaxSizeMB *int64
	MaxFiles  *int64
}

func (m *Manager) LoggingStatus(context.Context) (protocol.LoggingStatus, error)
func (m *Manager) UpdateLogging(context.Context, Operation, LoggingUpdate) (protocol.LoggingStatus, error)
```

`runtime.Options`/`RuntimeBuildOptions` 新增 `Logging LoggingRuntime` 和可选 `RefreshLogSecrets func(catalogURLs []string)`。`cmd/mihari` 闭包把 PR 1 的启动加载改为 `LoadOrCreateWithSidecarOutcome`：pre-commit error 仍终止；`Committed=true`+Warning 时继续使用返回的 after Settings，打开 daemon logger 后通过净化的 `OnBackgroundError("settings", fixedWarning)` 上报，不能返回启动失败或泄露底层路径。无写入的零值 CommitResult 不上报。redactor 必须 `ReplaceExact(append(append([]string{}, baseSecrets...), catalogURLs...))`，`baseSecrets` 为装配时的 token、controller secret、web credential；**禁止** `ReplaceExact(catalogURLs)` 把凭据从表里抹掉。`cmd/mihari` 在打开两个 Runtime 之前把 `settings.EffectiveLogging()` 转成 `logging.Config` 传入 `Open(ctx, ...)`，不得再使用 PR 1 的 `DefaultConfig()` 作为生产启动值。订阅 add/set/remove 成功后调用 `RefreshLogSecrets` 并传入当前 catalog 完整 URL。

- [ ] **Step 1: 写 capability/status/update Red**

覆盖：nil runtime 不宣告 capability 且 GET/PATCH `invalid_state`；GET 与 PATCH 分别用 `lockMaintenance`/`lockMutation`，同一 gate 内读取 Settings snapshot、dir 和 Store revision；阻塞 saver/Apply 的并发 GET 只能看到完整 before 或完整 committed after，不得出现新配置配旧 revision。所有 domain 范围校验，PATCH 显式 0 拒绝。`IfRevision` 不匹配时零 YAML/零 Apply/不增 revision；replace 前 Save 失败不 publish/不 Apply/不增 revision；committed+Warning 记录 warning并按成功顺序 Save→Manager publish→Group `Apply(ctx, cfg)`→revision；no-op 跳 Save/Apply 但 revision +1；同 operation ID 不重复；Manager 已 mutation-degraded 时 GET 仍成功、PATCH 在 Save 前返回 `invalid_state`。订阅 URL 变更后 exact 快照必须同时包含 **token 和新 URL**（`TestLogging_RefreshSecretsKeepsToken`）。另写 `TestManagerSettings_LoggingThenPorts`、`TestManagerSettings_PortsThenLogging`、`TestManagerSettings_LoggingThenSystemProxy`：Logging 改 `max-files` 再 `UpdateOnboarding` 改端口，YAML 与内存两边字段都在；反过来；Logging 再 `EnableSystemProxy` 的 desired 不丢。`cmd/mihari` 覆盖 bootstrap sidecar 首次创建/已有文件的 post-commit Warning：daemon 继续启动，logger 收到固定 warning，Manager 初始 snapshot 为 after Settings；自定义 YAML 重启后首条 JSONL 的测试必须打在 PR 1 的 `runDaemonWith` 缝上（`TestRunDaemon_EffectiveLoggingOnRestart`，同样用 `transporttest.Endpoint(t)` + goroutine + Ready 后 cancel），不得只测 Manager。

```powershell
go test -run '^(TestLogging|TestCapabilities.*Logging)$' ./internal/runtime
go test -run '^TestRunDaemon_(EffectiveLoggingOnRestart|BootstrapSettingsWarning)$' ./cmd/mihari
```

Expected: FAIL，缺少 Logging domain（`protocol.LoggingStatus` 已在 Task 5 存在，失败必须是行为红而不是编译失败）。

- [ ] **Step 2: 实现完整候选和转换**

domain 先对 pointer 值做 exact 校验，再调 `prepareSettings`。只在范围验证后转：

```go
logging.Config{
	Level:        parsedLevel,
	MaxSizeBytes: effective.MaxSizeMB * 1024 * 1024,
	MaxFiles:     int(effective.MaxFiles),
}
```

`LoggingStatus` 用 `lockMaintenance(ctx)` 后在同一 maintenance 临界区读 `settingsSnapshot()`、`Logging.Dir()` 和 `store.Load().Revision`，然后 unlock。`UpdateLogging` 走 `doOperation("logging:"+id)`，callback 内用 `lockMutation`；**Save 之前**完成 `IfRevision` 预检，不匹配则零 YAML 写入、不 Apply、不增 revision。Save committed（Warning 只上报）→publish→`Apply(ctx, cfg)`→`updateStateLocked(context.WithoutCancel(ctx), ...)` revision 都在该 gate 内完成，返回 DTO 时使用 committed revision。生产装配必须 `NewGroup(logDir, cfg, daemonRT, mihomoRT)` 并把 Group 交给 `Options.Logging`；不得只 Open 两个 Runtime 却不组 Group。`Apply` 先对全部 target `swapConfig` 再逐个 `convergeArchives`，context/250ms 只约束锁获取。不得在锁外分别读 Settings 与 Store。GET 不走 `doOperation`，因为没有 operation ID。coordinator 成功后用 committed revision 构造 `LoggingStatus`；不从 Group 反向推导持久化值。`cmd/mihari` 打开 Runtime 前调用：

```go
cfg := logging.Config{
	Level:        parseLevel(settings.EffectiveLogging().Level),
	MaxSizeBytes: settings.EffectiveLogging().MaxSizeMB * 1024 * 1024,
	MaxFiles:     int(settings.EffectiveLogging().MaxFiles),
}
```

```powershell
gofmt -w internal/runtime/logging.go internal/runtime/logging_test.go internal/runtime/manager.go internal/runtime/capabilities.go internal/runtime/capabilities_test.go internal/runtime/subscription.go internal/runtime/subscription_test.go internal/app/runtime.go cmd/mihari/main.go cmd/mihari/main_test.go
go test ./internal/runtime ./internal/app ./cmd/mihari
go test -race ./internal/runtime ./internal/app ./cmd/mihari
```

Expected: PASS。

- [ ] **Step 3: Commit（仅得到授权时）**

```powershell
git add internal/runtime/logging*.go internal/runtime/manager.go internal/runtime/capabilities*.go internal/runtime/subscription.go internal/runtime/subscription_test.go internal/app/runtime.go cmd/mihari/main.go cmd/mihari/main_test.go
git commit -s -m "feat: 增加 Logging runtime mutation"
```

---

### Task 7: Logging server 与 client

**Files:**
- Create: `internal/control/server/logging.go`
- Create: `internal/control/server/logging_test.go`
- Modify: `internal/control/server/runtime.go`
- Modify: `internal/control/client/runtime.go`
- Modify: `internal/control/client/runtime_test.go`

**Interfaces:**

```go
func (c *Client) Logging(context.Context) (protocol.LoggingStatus, error)
func (c *Client) UpdateLogging(context.Context, protocol.LoggingUpdateRequest) (protocol.LoggingStatus, error)
```

DTO 已在 Task 5 落地。本任务只接 HTTP 与 client。

- [ ] **Step 1: 写 server 错误矩阵 Red**

表驱动 HTTP 输入：空 operation ID、空 PATCH、`null`-only、unknown field、string/float/exponent/overflow 数字、非法 level/range 都为 400 `invalid_argument`；revision conflict/invalid state 为 409；Save failure 为 422；成功 GET/PATCH 是不包 envelope 的完整 status。

```powershell
go test -run '^TestLogging' ./internal/control/server
```

Expected: FAIL，route 不存在（DTO 已存在）。

- [ ] **Step 2: 实现 optional `loggingAPI` route**

```go
type loggingAPI interface {
	LoggingStatus(context.Context) (protocol.LoggingStatus, error)
	UpdateLogging(context.Context, runtimeapi.Operation, runtimeapi.LoggingUpdate) (protocol.LoggingStatus, error)
}
```

`runtimeRoutes` 始终注册 GET/PATCH；handler 内 type assert 失败返 `invalid_state`。复用 `decodeControlJSON`、`requireOperationID`、`writeControlError`，不改错误 envelope。

- [ ] **Step 3: 实现 client 并验证**

```powershell
gofmt -w internal/control/server/logging.go internal/control/server/logging_test.go internal/control/server/runtime.go internal/control/client/runtime.go internal/control/client/runtime_test.go
go test ./internal/control/protocol ./internal/control/server ./internal/control/client
```

Expected: PASS。

- [ ] **Step 4: Commit（仅得到授权时）**

```powershell
git add internal/control/server internal/control/client
git commit -s -m "feat: 增加 Logging v1 控制协议"
```

---

### Task 8: TUI session revision 重同步与本地 writer 收敛

**Files:**
- Modify: `internal/tui/session/client.go`
- Modify: `internal/tui/session/session.go`
- Modify: `internal/tui/session/session_test.go`
- Modify: `internal/logging/config.go`
- Modify: `internal/logging/config_test.go`
- Create: `internal/tui/logging_applier.go`
- Create: `internal/tui/logging_applier_test.go`
- Create: `internal/tui/ui/logging.go`
- Create: `internal/tui/ui/logging_test.go`
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/model_test.go`
- Modify: `internal/tui/run.go`
- Modify: `internal/tui/run_test.go`
- Modify: `internal/tui/pages/system/model.go`

**Interfaces:**

```go
const EventLogging EventKind = "logging"

type Event struct {
	// existing fields
	Logging protocol.LoggingStatus
	Epoch   uint64 // session 持有的 loggingEpoch；GET 发起时写入，root 只跟随不自增
}

func (c *session.Client) Logging(context.Context) (protocol.LoggingStatus, error)

// internal/tui/ui/logging.go
type LoggingSyncMsg struct {
	Epoch     uint64
	Status    protocol.LoggingStatus
	Available bool
}
type LoggingObservedMsg struct {
	Epoch  uint64
	Status protocol.LoggingStatus
}

type localLogging interface {
	Apply(context.Context, logging.Config)
}
type loggingApplier interface {
	Submit(logging.Config) bool
	CloseAndWait()
}
func newLoggingApplier(context.Context, localLogging) loggingApplier // localLogging 可为 nil：Submit 仍成功，worker 跳过 Apply
type LocalLoggingHealth interface{ Available() bool }
func (model *Model) SetLoggingApplier(loggingApplier)
func (model *Model) SetLocalLoggingHealth(LocalLoggingHealth)
```

- [ ] **Step 1: 写 session GET 触发 Red**

可控 poll 测试断言：首次 status+capability GET 一次；同 revision 的 3s poll 不重复 GET；revision 变化先 Submit bootstrap 再 GET（epoch 不变）；reconnect 即使 revision 相同也 GET 且 epoch+1；`logging` capability 从 absent→present 或 present→absent 即使 revision 不变也必须处理（出现时强制 GET，消失时清 `loggingRevision`）；GET 失败不标记已同步，下次 poll 重试，**且不得 `return err` 中断**同轮 Core/Proxies/Rules 等其余 snapshot。反之：Core/Proxies/Rules 失败也不得跳过 Logging GET（对照现有 `session.poll` 任一失败即中断的模式，Logging 必须独立调用）。事件在 ordered channel 中保持 Status 在 Logging 前。保存上一轮 capability presence，不要只看 revision。root 测试再交错注入：epoch 1 PATCH result、reconnect/capability reset 到 epoch 2、epoch 2 EventLogging、迟到的 epoch 1 result；以及同 epoch revision 12 Event 后迟到 revision 11 result。两种迟到结果都继续路由到来源页面清 pending，但不得改变 root/System/applier 的 epoch 2/revision 12 配置。

- [ ] **Step 2: 写 owned applier 与 root bootstrap/reset Red**

断言 `newLoggingApplier` 启动恰一个由自身拥有的 worker；`Submit` 非阻塞并把 heap 上 latest desired `logging.Config`/generation 覆盖后用容量 1 wake channel 唤醒。先阻塞 bootstrap Apply、期间 Submit effective，放开后最终 config 必须是 effective；同一时刻最多一个 Apply。`CloseAndWait` 幂等：先关 closing gate、cancel worker context，再等待 done；阻塞在 advisory lock 的 fake Apply 收到 context cancellation 后退出；Close 后 Submit 返回 false，且 Runtime/PrivateFS close spy 只会在 worker done 后触发。Run 必须在 `newModelWithClientContext` 整棵替换之后注入 applier/health（与 `SetServiceController` 相同）。`resources.Runtime == nil` 时仍构造 applier，传入 nil `localLogging`：Submit 不 panic，worker 跳过 `Apply`。EventReconnecting / revision 变化 / capability 消失 / EventLogging 的 `Update` 只调用非阻塞 `Submit`，不直接 Apply、也不创建承载 Apply 的 `tea.Cmd`，因此 Bubble Tea 退出不遗留 Cmd goroutine。session 是 `loggingEpoch` 的唯一生产者，**初始值为 1**；root 的 `loggingEpoch` **同样初始为 1**，首次 `EventLogging{Epoch:1}` / `EventStatus{Epoch:1}` 必须被采用，不得因 root 零值丢掉首次 GET。reconnect 或 capability 消失时 session **先** epoch++，`EventReconnecting`/`EventStatus` **必须带新 epoch**，再 GET。root 跟随该 epoch、Submit bootstrap、发 `LoggingSyncMsg`。全局 revision 改变 **不** 递增 epoch：session 先通知 root Submit bootstrap，再 GET，事件带当前 epoch。root 只在 `Event.Epoch == loggingEpoch` 且 revision 单调时采用。session 在 `putOrdered(EventLogging)` 成功后即可标记「本次 GET 的 epoch + 返回 revision」已抓取；仅当当前 epoch 仍相同且已标记 revision ≥ Status.revision 时跳过 GET。不需要 root→session 回通道。root 不再自行 epoch++。EventLogging 作为当前 epoch observation；只有相同 epoch 且 revision >= 已观察 revision 的 `LoggingObservedMsg` 才一次提交完整 config并向 System 同步。较旧 observation 仍按原完整路由下发 page completion，但 root/applier 不采用。revision 0 仍被记为已观察。local health 为 false 时 System 仍可展示/修改 daemon 配置，但 section 明确标记本地 writer unavailable。

```powershell
go test -run '^TestSession.*Logging' ./internal/tui/session
go test -run '^TestModel.*Logging' ./internal/tui
```

Expected: FAIL，session/root 尚无 Logging 状态。

- [ ] **Step 3: 实现 revision-aware poll 和 root 应用**

Session 保存 `loggingEpoch`、`loggingRevision *uint64` 和上一轮 `logging` capability presence。reconnect/capability 消失时 session 先 epoch++ 再 GET。revision 改变不改 epoch。session 在成功发出 `EventLogging` 后即可标记该 revision 已抓取；丢弃重试由后续 reconnect/capability/revision 触发，不需要 root→session 回通道。Logging GET **不得**接在 `poll()` 的 fail-fast 链里：Core/Proxies/Rules 失败仍必须 GET Logging（daemon 控制面与 mihomo 无关）。Logging GET 失败只保持 unsynced，不 abort 其余 snapshot。测试：注入 Core 失败时仍发出 `EventLogging`。Root 保存 `loggingEpoch uint64`（初始 1）、`loggingRevision *uint64`、`loggingLoaded bool`、local health，以及 `Run` 注入的 applier，并提供单一 `observeLogging(epoch, status)` 路径执行 epoch/revision gate；所有 session GET 和 PR 2 System PATCH/conflict GET 都转成 `ui.LoggingObservedMsg` 进入该路径。root 收到带新 epoch 的 `EventReconnecting`/capability 消失事件时跟随 epoch、清 `loggingRevision`、Submit bootstrap、发 `LoggingSyncMsg`；**不得**再 `epoch++`。`EventReconnecting` 必须带 session 已递增的新 epoch。revision 变化事件带当前 epoch，只 Submit bootstrap 再采用随后的 GET。applier worker 收到 wake 后循环：锁内读取 latest/generation；`localLogging == nil` 则跳过 Apply，否则锁外 `Apply(workerCtx, cfg)`；返回后若 generation 已变则立即处理最新值，否则等待下一次 wake。TUI 与 daemon 必须调用同一对 `internal/logging` 函数（本 PR Task 8 增补，不依赖 `config.Settings`）：

```go
func ParseLevel(string) (slog.Level, error)
func ConfigFromFields(level string, maxSizeMB, maxFiles int64) (Config, error) // MaxSizeBytes = maxSizeMB * 1024 * 1024
```

禁止 `internal/tui` import `cmd/mihari`，禁止再手写第二套 MiB→bytes。`tui.Run` 的 once-cleanup 顺序更新为 session Close → applier `CloseAndWait` → PR 1 `LoggingResources.Close`；PR 3 会在 session 与 applier 之间插入 Export runner `CancelAndWait`，形成 session→Export→applier→Runtime→PrivateFS。不得在 worker 内 Close Runtime/PrivateFS。

```powershell
gofmt -w internal/tui/session/client.go internal/tui/session/session.go internal/tui/session/session_test.go internal/tui/logging_applier.go internal/tui/logging_applier_test.go internal/tui/ui/logging.go internal/tui/ui/logging_test.go internal/tui/model.go internal/tui/model_test.go internal/tui/run.go internal/tui/run_test.go internal/tui/pages/system/model.go
go test ./internal/tui/session ./internal/tui
go test -race ./internal/tui/session ./internal/tui
```

Expected: PASS。

- [ ] **Step 4: Commit（仅得到授权时）**

```powershell
git add internal/tui/session internal/tui/logging_applier.go internal/tui/logging_applier_test.go internal/tui/ui/logging.go internal/tui/ui/logging_test.go internal/tui/model.go internal/tui/model_test.go internal/tui/run.go internal/tui/run_test.go internal/tui/pages/system/model.go
git commit -s -m "feat: 同步 TUI Logging runtime 配置"
```

---

### Task 9: System Logging 配置 UI

**Files:**
- Modify: `internal/tui/pages/system/model.go`
- Modify: `internal/tui/pages/system/model_test.go`
- Modify: `internal/tui/pages/system/section_test.go`
- Modify: `internal/tui/pages/system/scroll_test.go`
- Modify: `internal/tui/ui/keymap.go`
- Modify: `internal/tui/ui/keymap_test.go`
- Modify: `internal/tui/ui/strings.go`
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/model_test.go`
- Modify: `internal/tui/testdata/**/*.golden`

**Interfaces:** System `Client` 新增 `Logging`/`UpdateLogging`；System 保存 root 下发的 `loggingEpoch`/revision，PATCH 与 conflict GET command 捕获发起时 epoch并在结果中返回 `ui.LoggingObservedMsg{Epoch, Status}`。rows 依次为 `log-level`、`log-max-size`、`log-max-files`、`log-directory`，放在 Ports Config 后、Daemon 前。PR 3 再添加 `log-export`。

- [ ] **Step 1: 写布局/离线/交互 Red**

测试：Logging section exact 顺序；离线四行显示 `Unavailable`且 Enter 不发 PATCH；本地 writer unavailable 时配置仍可展示/修改，但 section 有稳定 “Local file log unavailable” 标记；Level Enter 按 debug→info→warn→error→debug 循环并立即提交；size/files 进入数字 textinput；超长、非数字、0/101、0/11 稳定失败；Esc 不提交；Enter PATCH 始终带 `if_revision` 指针，包括 0。

- [ ] **Step 2: 写成功/conflict/错误 Red**

断言 PATCH 成功只采用完整响应、带发起时 epoch 向 root 发布 Logging observed；`revision_conflict` 立即 GET，不自动重放旧表单，冲突 GET 也携带同一 epoch。收到 root 的较新 `LoggingSyncMsg` 后，旧 epoch PATCH/GET result 只清对应 pending并保持当前较新值，不更新 System/root/applier；同 epoch 较小 revision 同样不回退。其他失败显示 Failed + 稳定净化原因，不显示底层路径/错误；成功 no-op 也更新 revision。

- [ ] **Step 3: 实现 rows 和独立 help mode**

`ui.ModeLoggingEdit = "logging-edit"`，catalog exact footer：`Type value  Enter apply  Esc cancel`。System `HelpMode()` 根据 `editID` 区分 ports/logging，不复用 address 文案。数字解析使用：

```go
value, err := strconv.ParseInt(strings.TrimSpace(m.editInput.Value()), 10, 64)
```

只在检查范围后构造 `LoggingUpdateRequest`。

- [ ] **Step 4: 更新 scroll/golden 并验证 TUI**

```powershell
gofmt -w internal/tui/pages/system/model.go internal/tui/pages/system/model_test.go internal/tui/pages/system/section_test.go internal/tui/pages/system/scroll_test.go internal/tui/ui/keymap.go internal/tui/ui/keymap_test.go internal/tui/ui/strings.go internal/tui/model.go internal/tui/model_test.go
go test ./internal/tui/pages/system ./internal/tui/ui ./internal/tui
go test -race ./internal/tui/pages/system ./internal/tui
```

Expected: PASS；System 短高度测试中聚焦行仍可见。

- [ ] **Step 5: Commit（仅得到授权时）**

```powershell
git add internal/tui/pages/system internal/tui/ui internal/tui/model.go internal/tui/model_test.go internal/tui/testdata
git commit -s -m "feat: 增加 System Logging 配置区"
```

---

### Task 10: 兼容性文档与 PR 2 总验收

**Files:**
- Modify: `README.md`
- Modify: `README.zh-CN.md`
- Modify: `docs/commands.md`
- Modify: `docs/architecture.md`
- Add: `docs/superpowers/plans/2026-09-02-logging-control-plane.md`

- [ ] **Step 1: 记录降级边界**

明确说明：旧 binary 因 `KnownFields(true)` 无法读自定义 `log` block；降级前需在 System 恢复 info/10/3 使 block 自动移除，或备份并手动删除。文档列出 GET/PATCH 属于稳定 v1，但不宣称有 CLI 命令。架构文档补充 replace commit point：post-commit sync 只报警；补偿无法 commit 时 daemon health degraded、只读仍可用、mutation 需重启后重试，不新增事务文件或持久化 schema。

- [ ] **Step 2: 运行顺序回归**

```powershell
$names = @('TestManagerSettings_LoggingThenPorts','TestManagerSettings_PortsThenLogging','TestManagerSettings_LoggingThenSystemProxy','TestManagerSettings_PostCommitWarning','TestOnboarding_CompensationFailureCommitsDegraded')
foreach ($name in $names) {
  $out = go test -count=1 -v -run "^$name`$" ./internal/runtime 2>&1 | Out-String
  if ($LASTEXITCODE -ne 0) { throw "$name failed" }
  if ($out -notmatch [regex]::Escape("=== RUN   $name")) { throw "$name did not run" }
}
```

Expected: 五个测试都实际 RUN 且 PASS。`go test -run` 零匹配退出码 0 不得当成功。

- [ ] **Step 3: 全量验证**

```powershell
gofmt -l .
go test ./internal/config ./internal/state ./internal/onboarding ./internal/runtime ./internal/control/protocol ./internal/control/server ./internal/control/client ./internal/tui/session ./internal/tui/pages/system ./internal/tui ./internal/app ./cmd/mihari
go test -race ./internal/state ./internal/runtime ./internal/control/server ./internal/tui/session ./internal/tui
go test ./internal/integration
go test ./...
go vet ./...
git diff --check
```

Expected: 全部 PASS，`gofmt -l .` 无输出。

- [ ] **Step 4: 六目标 CGO-free 编译与范围审查**

```powershell
$env:CGO_ENABLED='0'
$targets=@(@('windows','amd64','.exe'),@('windows','arm64','.exe'),@('linux','amd64',''),@('linux','arm64',''),@('darwin','amd64',''),@('darwin','arm64',''))
foreach($t in $targets){$env:GOOS=$t[0];$env:GOARCH=$t[1];go build -o (Join-Path $env:TEMP ("mihari-{0}-{1}{2}" -f $t[0],$t[1],$t[2])) ./cmd/mihari;if($LASTEXITCODE -ne 0){throw "build failed: $($t[0])/$($t[1])"}}
git status --short
git diff --name-only
```

Expected: 六目标成功；无 `CHANGELOG.md`、无 CLI 新命令、无 export 实现。Windows 本地测试覆盖 Windows 路径；Unix 权限/锁相关回归由 CI Ubuntu 或 macOS job 实际 `go test`，交叉编译不能代替。

- [ ] **Step 5: Commit 文档（仅得到授权时）**

```powershell
git add README.md docs/commands.md docs/architecture.md docs/superpowers/plans/2026-09-02-logging-control-plane.md
git commit -s -m "docs: 记录 Logging 配置与降级边界"
```

---

## Self-Review

| Spec 要求 | 任务 |
| --- | --- |
| replace commit outcome、bootstrap sidecar Warning 成功语义与 Logging 默认/canonical round-trip | Task 1、6 |
| Tun 嵌套深拷贝、Manager 构造深拷贝，replace 前失败不泄漏 candidate | Task 1–2 |
| Manager bootstrap 后唯一 owner，`lockMaintenance`/`lockMutation`、排队请求 degraded 复查、committed-error 推进 revision/degraded；commit point 后取消不截断本地 revision/cache 收口 | Task 2 |
| 所有 runtime coordinator 写入参加 maintenance；core/sysproxy/TUN 全部迁移，sysproxy Settings-first 且首次 Save 失败零 OS write；后台 observation 不插入外部副作用与 revision commit，补偿失败时内存对齐磁盘并拒绝后续 mutation | Task 2–3 |
| onboarding state-only；两文件补偿成功回到 before，补偿 commit 前失败显式 partial+degraded | Task 4 |
| exact GET/PATCH DTO、`*int64`、revision 0 | Task 5 |
| Logging committed Save→publish→无失败且全 target 先切换的 `Apply(ctx)`→without-cancel revision，warning 不伪装失败，no-op 也增 revision；GET/PATCH 同一 maintenance 快照 | Task 6 |
| 自定义 YAML 重启即生效；订阅 mutation 刷新 exact secrets | Task 6 |
| `logging` capability 与 degraded `invalid_state` | Task 6–7 |
| YAML 零值当默认、PATCH 0 拒绝 | Task 1、6–7 |
| session 生产 loggingEpoch；重连/capability 消失才 epoch++；revision 变化同 epoch bootstrap+GET；root 跟随；owned applier latest-wins、cancel+join；Logging GET 失败不中断其余 poll | Task 8–9 |
| 自定义 YAML 经 `runDaemonWith` 重启即生效 | Task 6 |
| Logging→Ports / Ports→Logging / Logging→sysproxy 字段互不覆盖 | Task 6 |
| 订阅刷新 exact 集合保留 token+URL | Task 6 |
| System Logging 顺序、离线、本地 writer health、level cycle、数字 edit、conflict reload | Task 9 |
| 顺序 mutation 不互相覆盖 | Task 10 |
| 旧版 KnownFields 降级说明 | Task 10 |

**Placeholder scan:** 无占位任务或未命名类型。`CommitResult`、`LoadOrCreateWithSidecarOutcome`、`state.CommittedError`、`lockMaintenance`/`lockMutation`/`updateStateLocked`、`enterMutationDegraded`、服务层 `LoggingUpdate`、稳定 `LoggingUpdateRequest`、`LoggingRuntime`、`settingsCandidate`、onboarding `State`、`ui.LoggingSyncMsg`/`LoggingObservedMsg`、`loggingApplier` 的签名已固定。

**Type consistency:** YAML/DTO/domain 的 MiB/files 均使用 `int64`；只在 domain 验证后转为 logging runtime 的 bytes `int64` 与 files `int`。revision 可缺性一律用 `*uint64`，TUI 用独立 bool/指针区分未观察与数值 0；logging sync epoch 为不可持久化的 `uint64`，只比较同一次 TUI 进程内消息。
