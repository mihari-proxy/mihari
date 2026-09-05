# 日志快照导出与 TUI 交互 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 从三个持续写入/轮转的 JSONL 日志序列中取得有界快照，按时间窗二次脱敏并原子发布不覆盖的 zip，同时为 Logs 页和 System Logging 区提供同一个可取消导出对话框。

**Architecture:** PR 1 `PrivateFS` 用 opaque `FileIdentity`/可关闭 `DirectoryIdentity` 提供 Windows delete-sharing checked snapshot 和可信 LogDir identity；目标侧持有真实父目录 `PublishDir`，并从中创建 0700/protected-DACL 的私有 `PublishWorkspace`，temp、spool 和最终 no-replace publish 全部基于 held handles。`internal/logging.Export` 在每个来源的 shared lock 内枚举/checked-open/记录 size，锁外有界解码、筛选、递归脱敏和 spool，最后生成 manifest+zip 并不覆盖发布。`internal/tui/ui.ExportLogsModel` 在 `Update` 返回前启动并登记自己拥有的 export runner；返回的 Bubble Tea Cmd 只等待 buffered result。根 shell 在 typed switch 之前把每一条 `tea.Msg` 交给 overlay：它独占键盘、当代 result 和文本焦点 paste/clipboard，其它异步消息继续走现有完整页面路由。

**Tech Stack:** Go 1.26.5，标准库 `archive/zip`、`encoding/json`、`bufio`，既有 `golang.org/x/sys/windows`、`golang.org/x/sys/unix`、Bubble Tea v2、`github.com/atotto/clipboard`。

**Spec:** `docs/superpowers/specs/2026-09-02-file-logging-export-design.md`

**Depends on:** `2026-09-02-file-logging-foundation.md` 与 `2026-09-02-logging-control-plane.md` 按顺序完成并通过验收。

**Worktree:** `.worktrees/feat-log-export-ui`

**Branch:** `feat/log-export-ui`
**Delivery:** 本文档对应 spec 第 3 个顺序 PR，目标分支为 `dev`。

## Global Constraints

- 导出完全在 TUI 本地运行，不增 control API/CLI，不读 `mihari.yaml`、token file、订阅、runtime config 或 lock file。
- 每个来源在 shared advisory lock 内由 `PrivateFS.ReadDir` 固定 `FileIdentity`，再用 `OpenReadChecked` 比较已打开 handle identity 并记录 size；解析只读记录 size，不包含快照后 append，不依赖锁外 pathname。不匹配则该来源失败。
- 某来源没有任何匹配文件 → 空 snapshot，不是错误。只有匹配名是 symlink/非普通文件才令该来源失败。
- 快照打开必须走 PR 1 同一套逐段 no-follow：相对已验证 LogDir 句柄打开 basename，禁止完整路径 `os.Open`。Windows 每跳 `FILE_FLAG_OPEN_REPARSE_POINT`，share 为 `FILE_SHARE_READ|FILE_SHARE_WRITE|FILE_SHARE_DELETE`。每个 handle 在成功/失败/取消都关闭。
- TUI logger Open 失败不得拿走 redactor/`PrivateFS`；Export 依赖这两者，不依赖本进程 writer。
- Unix 平台文件必须 `//go:build unix`。目录 DACL 带 `OICI`，文件 DACL 不带继承。
- 一条物理行最多 1 MiB；超限时有界丢弃到下一换行并计 `skipped_invalid`，禁止 `Scanner` 无界 buffer 或 `ReadString` 无界分配。
- JSON decoder 必须 `UseNumber`；每条物理行必须恰好解出一个 top-level object，二次 Decode 必须为 `io.EOF`，数组/标量/第二个 JSON 值/尾随 token 都计 invalid；有效 record 递归脱敏后重新 marshal，不复制原始 JSON bytes。
- 保留每个来源的物理因果顺序（数字 suffix 降序→active），不按 record time 重排；三来源不宣称共享同一纳秒快照。
- 目标必须是绝对 `.zip`；用 `PrivateFS.OpenDirIdentity(LogDir)` 与 `PublishDir.IsWithin` 按 held directory identity 拒绝真实 LogDir 本身及后代，不得比较未解析 symlink 的 Paths 字符串；不覆盖已有文件/symlink，不递归创建自定义父目录。
- 目标校验打开并持有真实父目录 handle，再创建仅 owner/System 可进入的私有 `PublishWorkspace`；temp/spool 只传 workspace 内单段 basename，publish target 只传父目录内单段 basename。禁止在长耗时导出后重新用完整路径 `os.Link`/`MoveFileEx`，父路径或 workspace 可见名称被替换不能改变两个 held directory identities。
- workspace 清理严格遵守 spec 5.9.1：Unix 证明受信 parent/有效 ACL 后才按名删除，否则清理 held 内容并 close/warn；Windows workspace 创建即持有不 share delete 的 mandatory guard。允许的空私有 orphan 以内容清理成功为前提，IO 失败不承诺零残留。
- publish 为不可逆点；publish 前 cancel/失败不留 target，publish 成功后即使 context 立即取消也返回成功绝对路径。
- 三来源都无命中时不发布 zip。某来源无命中时省略该 entry，不生成空日志 entry。
- 对话框运行期 Esc 只发 cancellation，等 runner 回收资源后才回可编辑状态。runner 在 `Update` 返回前同步登记并启动 owned goroutine；Bubble Tea Cmd 只等待容量 1 的结果 channel，不负责启动 Export，因此不存在“pending 已设但 Cmd 未调度”的无人关闭 `done`。
- 不修改 `CHANGELOG.md`；实际 commit 只在用户明确授权时创建。

## File Structure

| 文件 | 职责 |
| --- | --- |
| `internal/platform/snapshot.go` | `OpenSnapshot` = `PrivateFS.OpenReadChecked` |
| `internal/platform/publish.go` | 跨平台 `PublishDir`/`PublishWorkspace` 接口与错误 |
| `internal/platform/publish_unix.go` / `_windows.go` | dirfd/HANDLE、identity containment、handle-relative no-replace publish/Close |
| `internal/platform/privatefs*.go` | 从已验证 LogDir 派生 `DirectoryIdentity`、从默认导出目录派生 `PublishDir` |
| `internal/logging/export.go` | 请求/结果/错误、时间窗、路径校验和顶层编排 |
| `internal/logging/snapshot.go` | 每来源 lock/enumerate/open/stat/close |
| `internal/logging/export_json.go` | 1 MiB 有界行解码、UseNumber、时间筛选、递归脱敏 |
| `internal/logging/export_zip.go` | spool、manifest、Deflate、sync/publish/cleanup |
| `internal/tui/ui/exportlogs.go` | 共享表单、pending/cancel/success/copy overlay |
| `internal/tui/model.go` | overlay 所有权、输入优先级和渲染 |
| `internal/tui/pages/logs/model.go` | Controls Export + `e` 入口 |
| `internal/tui/pages/system/model.go` | Logging `Export logs` 行 |
| `cmd/mihari/main.go` / `internal/tui/run.go` | 从 PR 1 `LoggingResources` 构造 exporter，按 session→Export→applier→runtime→PrivateFS 清理 |

---

### Task 1: 快照 handle、外部私有 temp 与 publish-no-replace

**Files:**
- Create: `internal/platform/snapshot.go`
- Create: `internal/platform/snapshot_test.go`
- Create: `internal/platform/publish.go`
- Create: `internal/platform/publish_unix.go`
- Create: `internal/platform/publish_windows.go`
- Create: `internal/platform/publish_test.go`
- Modify: `internal/platform/privatefs.go`
- Modify: `internal/platform/privatefs_unix.go`
- Modify: `internal/platform/privatefs_windows.go`
- Modify: `internal/platform/privatefs_unix_test.go`
- Modify: `internal/platform/privatefs_windows_test.go`

**Interfaces:**

```go
func OpenSnapshot(fs *PrivateFS, path string, expected FileIdentity) (*os.File, error) // 薄封装：fs.OpenReadChecked
type PublishDir struct { /* held directory identity + canonical absolute path */ }
type PublishWorkspace struct { /* private child directory held by identity */ }
func OpenPublishDir(path string) (*PublishDir, error)
func (fs *PrivateFS) OpenPublishDir(path string) (*PublishDir, error)
func (d *PublishDir) Path() string
func (d *PublishDir) Exists(name string) (bool, error)
func (d *PublishDir) IsWithin(ancestor *DirectoryIdentity) (bool, error)
func (d *PublishDir) CreateWorkspace() (*PublishWorkspace, error)
func (w *PublishWorkspace) CreateTemp(pattern string) (*os.File, string, error)
func (w *PublishWorkspace) Remove(name string) error
func (d *PublishDir) PublishNoReplace(workspace *PublishWorkspace, tempName, targetName string, onWarning func(error)) error
func (w *PublishWorkspace) Close() error
func (d *PublishDir) Close() error
var ErrPublishDirectoryChanged = errors.New("publish directory changed")
```

`OpenPublishDir` 用于 TUI 自定义输出父目录：允许在打开 parent 时跟随用户选择的 symlink/junction，随后 canonicalize、fstat 并持有该目录 identity；不修改父 ACL/mode。`PrivateFS.OpenPublishDir` 只接受已验证的默认 `LogExportDir`，从 dataRoot capability 相对打开。`CreateWorkspace` 在 held parent 下以不可预测 basename 创建并持有一个 Unix 0700 或 Windows current interactive user SID + LocalSystem protected-DACL 私有子目录；temp/spool 是其中的 0600/protected-DACL 文件。workspace 必须来自调用 `PublishNoReplace` 的同一个 `PublishDir`，并在 Dir 前关闭。Close 幂等：先尝试清空 held workspace 内容；按 spec 5.9.1 的 Unix owner/sticky/有效 ACL 信任证明或 Windows mandatory guard 清理目录项。Unix 可信 parent 内已完成的受信改名可按 identity 枚举当前 basename 删除空目录；原 basename replacement 不碰。Unix 无法证明 namespace 安全或 workspace 移出 parent 时关闭/warn，内容清理成功才可称空私有 orphan；IO 失败保留 warning 并继续所有 Close。Windows 验证 identity/parent/empty 后才在原 guard handle 设置 disposition，不依赖 delete-pending rollback。所有 name 参数必须是单段 basename，不接受绝对路径、分隔符、`.`/`..`。`Path()` 是打开时捕获的不可变字符串，Close 后仍可读；其它 capability 方法在 Close 后返回 `os.ErrClosed`。

- [ ] **Step 1: 写快照/发布 Red**

测试覆盖：`OpenSnapshot` 读得已记录 bytes，另一 handle 仍可 append/rename/delete；枚举 identity 与打开 handle 不同、或 pathname 换成 symlink/reparse point 时必须关闭并失败。`PublishDir`/workspace 拒绝非法 basename，Close 幂等，Close 后 Dir.Path 仍返回原 canonical string、其它方法返回 `os.ErrClosed`；CreateWorkspace/CreateTemp/Remove 都锚定各自已打开目录。workspace mode/DACL 拒绝非 owner/System，Unix 非受信 parent writer 即使 rename workspace entry 也不能替换其内容或让 publish 读取攻击者文件；同 UID 并发攻击在受信边界外，测试不得宣称隔离同 UID。Windows 必须用独立外部 handle 断言 rename/delete 因 sharing violation 失败，child IO 与正常 temp publish 仍成功。PublishNoReplace 成功后 target 为完整 temp 内容且 workspace 中 temp 不在；target 已存在时返回可 `errors.Is(err, os.ErrExist)` 的错误并保留 temp；publish 后 cleanup/sync 失败走 `onWarning`，不得把已发布 target 报成失败。打开 parent 后把可见路径 rename 并替换为指向外部的 symlink/junction，publish 必须安全失败或只作用于原 handle identity，替换目标零写入；测试选用实现的确定语义——publish 前 identity check 发现路径已变时返回 `ErrPublishDirectoryChanged` 并保留 target 不存在。用 symlink dataRoot、目标 parent 直接等于真实 LogDir、经 symlink/junction 指入 LogDir、sibling prefix 和普通外部目录覆盖 `IsWithin`；前两种 inside 场景必须基于 held `DirectoryIdentity` 返回 true。

```powershell
go test -run '^Test(OpenSnapshot|PublishDir|PublishWorkspace|DirectoryContainment)' ./internal/platform
```

Expected: FAIL，缺少平台符号。

补充 Red：Unix 可信 owner 且无额外 ACL 授权的普通目录、可信 owner 的 sticky 目录正常无 warning 清理；非可信 parent owner 的 sticky、非 sticky 0777/0770、ACL-only namespace 授权、ACL 无法查询均 close/warn 并仅留空私有目录（内容删除成功时）。覆盖打开后权限/owner/ACL 变更并重新判定、同 parent 已完成的受信 rename、replacement 与移出 parent；在不可信 parent 最终检查到删除 seam 注入 replacement，断言从未调用按名目录删除。注入内容 Remove/ReadDir/Close 失败时报告不完整清理且仍尝试后续资源关闭，不断言空 orphan。Windows 从创建起检查不 share delete，验证失败零 disposition；允许测试经同一受信 handle 预先移出，但不能拿它模拟外部竞争者。创建后检查失败的清理也需 guard 与错误合并。

- [ ] **Step 2: 实现 Unix 语义**

`OpenSnapshot` 实现为 `return fs.OpenReadChecked(path, expected)`，平台 no-follow/identity 语义全部留在 PR 1，不写第二套 source `openat`。Unix `PublishDir` 与 workspace 各持有 `O_DIRECTORY|O_CLOEXEC` dirfd；Exists、CreateWorkspace、CreateTemp、Remove/RemoveDir 分别使用相应 parent dirfd 的 `fstatat(AT_SYMLINK_NOFOLLOW)`、`mkdirat`、`openat(O_CREAT|O_EXCL|O_CLOEXEC)`、`unlinkat`。`IsWithin` 从 PublishDir 的 duplicated dirfd 开始，用 `openat(current, "..", O_DIRECTORY|O_NOFOLLOW|O_CLOEXEC)`/`fstat` 向上比较 `DirectoryIdentity` 的 dev+ino，遇到相同 parent/current identity 即到文件系统根；每个临时 parent fd 都关闭。Publish 先把保存的 parent canonical path 再打开并 fstat 对比 identity，不同即 `ErrPublishDirectoryChanged`；随后用 `linkat(workspace.dirfd,tempName,publishDir.dirfd,targetName,0)`，私有 workspace 保证 source basename 不可被非受信主体替换。成功 link 是不可逆发布点，再 unlink workspace temp 并 fsync 两个 dirfd。link 后 cleanup/sync 错误调用 `onWarning`，不得将已发布 target 报为“未成功”。禁止 `os.Link`/完整路径 Remove。Unix publish 文件顶部写 `//go:build unix`。打开 parent 时记录信任评估，cleanup 最终删除前重新查询 owner/mode/sticky/有效 ACL；信任主体仅当前有效 UID、默认导出已验证的数据根 owner 与本机 root/管理员，自定义 parent owner 不自动受信。sticky 不能保护非受信 parent owner；未知/不可证明 ACL fail closed。不得添加依赖或修改 parent mode/ACL；遵守 spec 5.9.1 的普通安全目录正常清理与不可信 namespace close/warn 分支。检查后到删除期间的受信主体恶意变更不在保证范围。

- [ ] **Step 3: 实现 Windows 语义**

`OpenSnapshot` 同样只调 `fs.OpenReadChecked`。Windows `PublishDir` 使用不继承且带 `FILE_SHARE_READ|WRITE|DELETE` 的 directory handle；workspace 从原子创建起请求 `DELETE` 并只带 `FILE_SHARE_READ|FILE_SHARE_WRITE`，持有至 Close，禁止先创建 share-delete handle 再补 guard；相对 CreateWorkspace/CreateTemp/Exists/Remove 使用 `NtCreateFile`/handle-based information APIs，workspace 与 temp DACL 只含交互用户和 LocalSystem。`IsWithin` 只查询 held ancestor/target handles 的 volume/file identity 与 normalized final handle name；读取 ancestor→target→ancestor、target 两次且两组结果必须稳定，否则 fail closed，随后用同 volume、大小写不敏感且分隔符边界正确的相对关系判断，禁止重新打开用户 path。Publish 前重新打开 canonical parent path比较 volume/file identity，不同返回 `ErrPublishDirectoryChanged`；从私有 workspace handle 相对打开 temp，确认普通文件后对该 temp handle 调 `NtSetInformationFile`，rename information 的 RootDirectory=同一 `PublishDir` handle且不设置 replace flag，绝不调用 `MoveFileEx` 完整路径。`NtSetInformationFile` 返回 `windows.NTStatus`；先经 `NTStatus.Errno()` 规范化 `STATUS_OBJECT_NAME_COLLISION`/name-exists，再将 `ERROR_FILE_EXISTS`/`ERROR_ALREADY_EXISTS` 包装为 `os.ErrExist`。rename 后 flush/cleanup 错误同样走 `onWarning`。workspace cleanup 在原 no-delete-sharing guard 内检查 identity、parent containment、empty，全部成功后才对同一 handle 设置 delete disposition 并 Close；失败仅 Close/warn，不置 delete-pending、不依赖 rollback。

```powershell
gofmt -w internal/platform/snapshot.go internal/platform/snapshot_test.go internal/platform/publish_unix.go internal/platform/publish_windows.go internal/platform/publish_test.go internal/platform/privatefs.go internal/platform/privatefs_unix.go internal/platform/privatefs_windows.go internal/platform/privatefs_unix_test.go internal/platform/privatefs_windows_test.go
go test ./internal/platform
go test -race ./internal/platform
```

Expected: PASS；测试后所有 snapshot/DirectoryIdentity/PublishWorkspace/PublishDir/file handle 均关闭。

- [ ] **Step 4: Commit（仅得到授权时）**

```powershell
git add internal/platform/snapshot*.go internal/platform/publish*.go internal/platform/privatefs*.go
git commit -s -m "feat: 增加日志快照与句柄发布"
```

---

### Task 2: 导出请求、时间窗与目标路径

**Files:**
- Create: `internal/logging/export.go`
- Create: `internal/logging/export_test.go`

**Interfaces:**

```go
type RangeKind string
const (
	RangeLast24Hours   RangeKind = "last_24h"
	RangeLast60Minutes RangeKind = "last_60m"
	RangeBetween       RangeKind = "between"
	RangeAll           RangeKind = "all"
)

type ExportRange struct {
	Kind RangeKind
	From time.Time
	To   time.Time
}

type ExportPaths struct {
	LogDir, ExportDir          string
	DaemonLog, TUILog, MihomoLog string
}

type ExportRequest struct {
	Now        time.Time
	Range      ExportRange
	OutputPath string
	AutoNumber bool
	Paths      ExportPaths
	PrivateFS  *platform.PrivateFS
	OpenLock          func(*platform.PrivateFS, string) (platform.AdvisoryLock, error)
	EnterRecordMutex  func(basePath string) func() // 同进程 writer 临界区；nil = no-op
	Redactor          *Redactor
	OnWarning         func(error)
}

type ExportResult struct { Path string }

type exportTarget struct {
	Dir        *platform.PublishDir
	LogDir     *platform.DirectoryIdentity
	Name       string // current candidate basename
	Path       string // current candidate canonical path
	AutoNumber bool
	Base       string // basename without .zip or numeric suffix
	Suffix     int64  // 0 = unsuffixed; positive = -N
}

func (t *exportTarget) Advance() error

var (
	ErrInvalidExportRequest = errors.New("invalid export request")
	ErrExportTargetExists   = errors.New("export target already exists")
	ErrExportTargetChanged  = errors.New("export target changed")
	ErrNoLogLines           = errors.New("no log lines in selected range")
)

func Export(context.Context, ExportRequest) (ExportResult, error) // nil OpenLock 收成 platform.OpenAdvisoryLock
```

- [ ] **Step 1: 写 range Red**

以固定 `Now` 和 non-UTC Local zone 表驱动断言：Last 24h/60m 转 UTC 闭区间；Between 保留用户输入 instant；From==To 合法；From>To/未知 kind 是 `ErrInvalidExportRequest`；All 没有边界。`Now` 只在调用者点击时传入，Export 内不再调 `time.Now()`。

- [ ] **Step 2: 写目标路径 Red**

拒绝相对路径、非 zip 扩展名、不存在/非目录自定义 parent、已有 target（包括 symlink）。先从 `PrivateFS.OpenDirIdentity(LogDir)` 取得 held LogDir capability，再断言 target parent 直接等于、位于其后代，或经 symlink/junction 解析进入时都被 `PublishDir.IsWithin` 拒绝；`MIHARI_DATA` 自身是 symlink 也不能绕过。默认 ExportDir 允许安全创建，默认 target 冲突按 `-1`、`-2` 取第一个空位，自定义 target 不自动改名。断言 resolver 返回的 `exportTarget.Dir` 与 `exportTarget.LogDir` 均已打开、`Name` 是单段 basename、`Path=filepath.Join(Dir.Path(), Name)` 为绝对 canonical path，且 AutoNumber/Base/Suffix 能精确生成下一候选；Suffix 为 `math.MaxInt64` 时 `Advance` 返回稳定错误而不 wrap。失败路径关闭已经取得的两种目录 capability；成功由 Export 接管，并在 publish loop 结束后各 Close 一次。

```powershell
go test -run '^TestExport(Range|Target)' ./internal/logging
```

Expected: FAIL，导出入口不存在。

- [ ] **Step 3: 实现纯校验与真实父目录解析**

`.zip` 检查用 `strings.EqualFold(filepath.Ext(path), ".zip")`。先 `Abs/Clean`，分离 parent 与单段 basename。默认 parent 先 `PrivateFS.EnsureDir(LogExportDir)`，再 `PrivateFS.OpenPublishDir`；自定义 parent 用 `platform.OpenPublishDir`。同时用 `PrivateFS.OpenDirIdentity(LogDir)` 取得 held trusted identity，调用 `Dir.IsWithin(logIdentity)`，结果 true 就拒绝。real target 只用 `filepath.Join(Dir.Path(), Name)` 组成展示值，不能参与 security decision；target 存在性只用 `Dir.Exists(Name)`，不能再 `Lstat` 完整路径。resolver 成功把 Dir 与 LogDir identity 所有权一起放进 `exportTarget` 交给 `Export`；任一步失败由 resolver 关闭已经取得的 capability。默认 autonumber 只在同一个 Dir 上依次 `Exists` basename。时间戳 `±HHMM` 属于 stem，不是 suffix。`Base` 为 `mihari-logs-YYYYMMDD-HHMMSS±HHMM`（可含 `-0500`）；仅 stem 之后的 `-N` 才是 autonumber。禁止用通用 `-\d+` 回剥，否则负偏移会被吃掉。`Advance` 生成 `Base-N.zip`。负偏移表驱动：`…-0500.zip` 的下一候选是 `…-0500-1.zip`。自定义 target 禁止 `Advance`。`Export` 入口把 nil `OpenLock` 收成 `platform.OpenAdvisoryLock`，禁止对 nil func 调用。

```powershell
gofmt -w internal/logging/export.go internal/logging/export_test.go
go test -run '^TestExport(Range|Target)' ./internal/logging
```

Expected: PASS。

- [ ] **Step 4: Commit（仅得到授权时）**

```powershell
git add internal/logging/export.go internal/logging/export_test.go
git commit -s -m "feat: 定义日志导出时间与路径契约"
```

---

### Task 3: 有界多文件快照

**Files:**
- Create: `internal/logging/snapshot.go`
- Create: `internal/logging/snapshot_test.go`
- Create: `internal/logging/snapshot_subprocess_test.go`

**Interfaces:**

```go
type snapshotHandle struct {
	name string
	size int64
	file *os.File
}

func snapshotSource(ctx context.Context, fs *platform.PrivateFS, basePath string, enterMutex func(string) func(), openLock func(*platform.PrivateFS, string) (platform.AdvisoryLock, error), openSnapshot func(*platform.PrivateFS, string, platform.FileIdentity) (*os.File, error)) ([]snapshotHandle, error)
func closeSnapshots([]snapshotHandle) error
```

- [ ] **Step 1: 写枚举/顺序/类型 Red**

在 temp LogDir 中混入 base、.1/.2/.9、.0/.10/.01/.x、sibling prefix、directory、symlink。断言严格匹配只接受 base 与 `.1..9`，`.01` 不是合法 suffix，顺序 `.9→.2→.1→base`；匹配名称是 symlink/非普通文件时整个来源失败。该来源没有任何匹配文件时返回空 snapshot，不是错误。fake `ReadDir` 返回确定 `platform.FileIdentity`，断言相同 identity 传入 `openSnapshot`；另覆盖打开前 pathname 被替换、`OpenReadChecked` 检出 handle identity 与枚举 identity 不匹配并关闭、base 在枚举后消失。离线导出测试只存在 TUI 日志时仍成功，daemon/mihomo 来源为空。

- [ ] **Step 2: 写并发快照 Red**

用两子进程：一个持续写/轮转 TUI base，一个反复 snapshot。每次快照的 handle identity/size 在 shared lock 内固定，释放后继续 rotate/delete 不导致 read error；读取绝不超记录 size，不出现截断 JSONL record。context cancel 在等锁中立即终止。

```powershell
go test -run '^TestSnapshotSource_' ./internal/logging
go test -race -run '^TestSnapshotSource_' ./internal/logging
```

Expected: FAIL，快照实现尚不存在。

- [ ] **Step 3: 实现 shared-lock snapshot 与 cleanup**

每个来源锁路径与 writer 一致（`basePath+".lock"`，例如 `mihari-tui.log.lock`）。快照顺序固定为：先 `EnterRecordMutex(basePath)`（同进程 TUI writer 的记录 mutex；跨进程来源 no-op），**再** `OpenAdvisoryLock` 取 **shared**。mutex 保证本进程不会同时持 exclusive 与 shared：writer 与 snapshot 都是 **先 mutex 再 flock**（writer exclusive，snapshot shared）；不要改 PR 1 rotator 顺序。禁止跳过 OS shared lock：否则另一 TUI 可在本进程 snapshot 期间 exclusive rotate。Darwin 转换问题靠「mutex 内不同时持两种模式」解决，不是靠省略 flock。获 shared 后 `fs.ReadDir` 返回已经由平台 no-follow 枚举取得的 `FileEntry{Name, Mode, Identity}`，通用层做名称/Mode 检查，再把 entry Identity 传给 `OpenSnapshot`/`OpenReadChecked`；实际 handle identity 不匹配由 platform 关闭并返回错误，整个来源失败。`snapshotHandle.name` 必须是 basename。再 Stat/size。任一步失败先 close 已打开 handles，再 unlock/close lock，再释放 mutex。不在 snapshot 结构中保留 lock。零匹配文件直接返回空切片。unix 测试：同进程 snapshot 持 shared 期间，第二进程不得获得 exclusive。通用 logging 不读取 `FileInfo.Sys()`，不自行解释 Unix/Windows identity。

```powershell
gofmt -w internal/logging/snapshot.go internal/logging/snapshot_test.go internal/logging/snapshot_subprocess_test.go
go test ./internal/logging
go test -race ./internal/logging
```

Expected: PASS。

- [ ] **Step 4: Commit（仅得到授权时）**

```powershell
git add internal/logging/snapshot*.go
git commit -s -m "feat: 实现日志序列有界快照"
```

---

### Task 4: 有界 JSONL 解码、二次脱敏与 manifest

**Files:**
- Create: `internal/logging/export_json.go`
- Create: `internal/logging/export_json_test.go`
- Modify: `internal/logging/redactor.go`
- Modify: `internal/logging/redactor_test.go`

**Interfaces:**

```go
func (r *Redactor) Value(any) (value any, changed bool)

type exportFile struct {
	Name           string   `json:"name"`
	Lines          int64    `json:"lines"`
	SkippedInvalid int64    `json:"skipped_invalid"`
	Redacted       int64    `json:"redacted"`
	Sources        []string `json:"sources"`
}

type manifestRange struct {
	Kind string `json:"kind"`
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

type exportManifest struct {
	Schema     string        `json:"schema"`
	ExportedAt string        `json:"exported_at"`
	Timezone   string        `json:"timezone"`
	Range      manifestRange `json:"range"`
	Files      []exportFile  `json:"files"`
	Notes      []string      `json:"notes"`
}
```

`RangeAll` 的 manifest `from`/`to` 省略，不写空字符串。其他 kind 使用已规范化的 UTC RFC3339Nano 边界。

- [ ] **Step 1: 写有界 line reader Red**

覆盖 exact 1 MiB，1 MiB+1，超长行分成多个 bufio fragment，超长后紧跟有效行，最后无 newline 行，context 取消。断言峰值缓冲不超 `MaxExportRecordBytes+bufio` 固定 chunk，超长行只计一次 invalid。

- [ ] **Step 2: 写 JSON/time/UseNumber/order Red**

输入包含大于 2^53 的整数，导出后数字字面值不变；损坏 JSON、缺 time、非 RFC3339 time、top-level 数组/标量、`{"time":"…"} {"time":"…"}` 第二个 JSON 值及有效 object 后尾随垃圾都各计一个 invalid 且不输出；object 后仅有 JSON whitespace 合法。窗口两端的 record 都包含；窗外不计 invalid；故意 wall-clock 回拨的记录仍保持文件/行顺序。实现每行以 `UseNumber` 首次 Decode 到 `map[string]any`，随后第二次 Decode 必须得到 `io.EOF`。

- [ ] **Step 3: 写递归脱敏 Red 并实现**

覆盖 `map[string]any`、`[]any`、嵌套 sensitive key/string URL/auth/64-hex，断言 `changed` 每 record 只计一次，原对象不就地修改，`json.Number` 保留。

- [ ] **Step 4: 实现 manifest 值**

timezone 从 `Now.Format("-07:00")` 取数值偏移，`exported_at` 用 RFC3339Nano，`RangeAll` 省略 from/to，其余 kind 写 UTC 边界，notes 使用 spec 固定提醒。不写入 secret hash、原值、绝对源路径或 lock file。

```powershell
gofmt -w internal/logging/export_json.go internal/logging/export_json_test.go internal/logging/redactor.go internal/logging/redactor_test.go
go test -run '^(TestExportJSON|TestRedactorValue)' ./internal/logging
go test -race ./internal/logging
```

Expected: PASS。

- [ ] **Step 5: Commit（仅得到授权时）**

```powershell
git add internal/logging/export_json*.go internal/logging/redactor*.go
git commit -s -m "feat: 增加导出二次脱敏与 manifest"
```

---

### Task 5: zip spool、取消与原子发布

**Files:**
- Create: `internal/logging/export_zip.go`
- Create: `internal/logging/export_zip_test.go`
- Modify: `internal/logging/export.go`
- Modify: `internal/logging/export_test.go`

**Interfaces:** zip entry 名为常量：`manifest.json`、`daemon/mihari-daemon.log`、`tui/mihari-tui.log`、`mihomo/mihomo.log`。来源配置由 `ExportPaths` 内部映射，用户路径绝不进 entry name。内部测试 seam 固定为：

```go
type exportStage uint8
const (
	stageEnumerate exportStage = iota
	stageReadBatch
	stageDecodeLine
	stageWriteSpool
	stageWriteZip
	stageBeforeZipClose
	stageBeforeSync
	stageBeforePublish
)

type zipWriter interface {
	CreateHeader(*zip.FileHeader) (io.Writer, error)
	Close() error
}

type exportOps struct {
	Checkpoint   func(exportStage) error
	NewZipWriter func(io.Writer) zipWriter
	Sync         func(*os.File) error
	Publish      func(*platform.PublishDir, *platform.PublishWorkspace, string, string, func(error)) error
}

func exportWithOps(context.Context, ExportRequest, exportOps) (ExportResult, error)
```

生产 `Export` 只调用 `exportWithOps` + default ops；Checkpoint 默认只返回 nil，不能改变生产语义。

- [ ] **Step 1: 写 zip layout/统计 Red**

三来源 fixture 含有效、越窗、损坏、需脱敏 record。解压断言 manifest exact schema/range/files/stats/sources/notes，有命中来源只有一个 entry，无命中来源无 entry，entry 全部 Deflate，内容为重新 marshal 的 JSONL。

- [ ] **Step 2: 写失败/取消/竞争 Red**

通过 `exportOps` 在枚举来源、每个读取 batch、解码行、写 spool、写 zip、zip Close、Sync、publish 前分别注入 failure/cancel；fake `zipWriter.Close`、Sync、Publish 自身也返回错误。每个提交前故障路径断言本次未发布 target（竞争者目标保留）；无 cleanup IO 故障且 parent 可信时 temp/spool 与目录均删除，workspace、PublishDir 与 LogDir identity 均已 Close。Unix 不可信 namespace 按 spec 5.9.1 留空私有 orphan/warn；cleanup 某一步失败仍执行后续 cleanup、报告可能残留，不断言无条件清空。自定义 target 校验后被抢占返 `ErrExportTargetExists` 且不覆盖，temp 留在 workspace 供 cleanup；默认 target 连续被抢占两次时在同一 held Dir/workspace 上发布到下一编号，返回路径必须是实际成功的 `-N.zip`。另在校验后、publish 前替换可见 parent 名为 symlink/junction，并在 Unix 覆盖 workspace 名 replacement（Windows 外部 rename/delete 必须被 guard 阻止），断言 `ErrPublishDirectoryChanged` 或仍只作用于原 handles、外部目录零写入且原 workspace 中 temp/spool 被 handle-relative Remove 清理；把 held PublishDir 移入 LogDir 后，紧邻 publish 的 `IsWithin(target.LogDir)` 必须令导出以 `ErrExportTargetChanged` fail closed且不调用 Publish。若 workspace entry 被移出 held parent，Close 先清理 held 内容再关闭 handle、返回 cleanup warning且不得追随路径删除；测试不要求对抗外部主体阻止清理后仍实现零 orphan。publish 成功后注入 cancel 仍返回 success path。

- [ ] **Step 3: 实现有界磁盘 spool 和 cleanup stack**

target resolver 成功后立即 `workspace := target.Dir.CreateWorkspace()`，并保证 workspace 在 Dir 前、Dir 在 `target.LogDir` 前关闭。为每个有命中来源通过 `workspace.CreateTemp` 创建 0600/protected-DACL spool，只在状态中保存返回的单段 basename，避免在内存保存最大 1 GiB 日志。解析完后先写 `manifest.json`，再按 daemon/tui/mihomo 顺序将存在的 spool 拷入 zip entry。spool→zip 禁止 `io.Copy`：使用固定 32 KiB buffer 循环，每个 chunk 前后检查 `ctx.Err()`。cleanup stack 先关闭打开的 file/zip handles，再逐个 `workspace.Remove(name)`，最后依次 `workspace.Close`、`target.Dir.Close`、`target.LogDir.Close`；用 `errors.Join` 保留主错误但对外只返稳定 export error。中段取消测试必须及时退出；无 IO 故障且安全前提成立时零残留，其它情况按 spec 5.9.1 验证空私有 orphan 或不完整 cleanup warning；不得为 cleanup 重新拼接完整父路径。workspace 移出 held parent 或 Unix parent 信任证明失败时，仍清理 held 内容并关闭，再记录 cleanup warning，不沿路径追删未知对象。publish 前的 cleanup error 保留主错误；publish 后 workspace/Dir/LogDir cleanup error 只走净化 `OnWarning`，不能把已存在 target 报成失败。

- [ ] **Step 4: 实现关闭/sync/publish 顺序**

顺序固定：关闭 spool reader → `zip.Writer.Close()`（`CreateHeader` 返回的 `io.Writer` 没有独立 Close，entry 边界由下一个 entry 或 Writer.Close 完成）→ zip temp `Sync` → zip temp `Close` → final `ctx.Err()` 检查 → publish loop。loop 每轮先检查 ctx，再调用 `target.Dir.IsWithin(target.LogDir)`；检查错误或结果为 true 都映射 `ErrExportTargetChanged` 并且不进入 Publish，false 才调用 `target.Dir.PublishNoReplace(workspace, tempName, target.Name, onWarning)`。成功后设 `published=true` 并返回当前 `target.Path`；自定义 target 的 `os.ErrExist` 立即映射 `ErrExportTargetExists`；默认 target 的 `os.ErrExist` 调 `target.Advance()` 后重试，复用同一 workspace temp，不重新生成 zip，suffix 溢出返回稳定 export failure。其它错误立即失败。发布成功后平台层尝试移除 workspace temp；Unix unlink-source 失败为 post-commit warning，cleanup 继续尝试，不能假设 temp 必已消失；defer 不删除 target。返回前依次关闭 workspace/PublishDir/LogDir identity，publish 后 cleanup/sync warning仍只走净化 `onWarning`。

```powershell
gofmt -w internal/logging/export.go internal/logging/export_test.go internal/logging/export_json.go internal/logging/export_json_test.go internal/logging/export_zip.go internal/logging/export_zip_test.go
go test ./internal/logging
go test -race ./internal/logging
```

Expected: PASS，三来源无命中返 `ErrNoLogLines` 且没有 zip。

- [ ] **Step 5: Commit（仅得到授权时）**

```powershell
git add internal/logging/export*.go
git commit -s -m "feat: 实现可取消原子日志导出"
```

---

### Task 6: 共享 Export Logs 对话框

**Files:**
- Create: `internal/tui/ui/exportlogs.go`
- Create: `internal/tui/ui/exportlogs_test.go`
- Modify: `internal/tui/ui/textfield.go`
- Modify: `internal/tui/ui/strings.go`
- Modify: `internal/tui/ui/keymap.go`
- Modify: `internal/tui/ui/keymap_test.go`

**Interfaces:**

```go
type OpenExportLogsMsg struct{}

type ExportLogsOptions struct {
	Context        context.Context
	Now            func() time.Time
	DefaultDir     string
	Exists         func(dir, name string) (bool, error) // held-handle basename probe; nil fs 时恒为 false
	Export         func(context.Context, logging.ExportRequest) (logging.ExportResult, error)
	WriteClipboard func(string) error
}

type ExportLogsModel struct { /* form + generation + cancel + result */ }
type exportResultMsg struct {
	Generation uint64
	Result     logging.ExportResult
	Err        error
}
type exportRunner struct { /* mutex + current cancel/done; no idle goroutine */ }

func NewExportLogsModel(ExportLogsOptions) *ExportLogsModel
func newExportRunner(context.Context, func(context.Context, logging.ExportRequest) (logging.ExportResult, error)) *exportRunner
func (r *exportRunner) Start(generation uint64, request logging.ExportRequest) (<-chan exportResultMsg, bool)
func (r *exportRunner) Cancel()
func (r *exportRunner) CancelAndWait()
func (m *ExportLogsModel) Open()
func (m *ExportLogsModel) Update(tea.Msg) (cmd tea.Cmd, consumed bool)
func (m *ExportLogsModel) View(width, height int) string
func (m *ExportLogsModel) Closed() bool
func (m *ExportLogsModel) Pending() bool
func (m *ExportLogsModel) CancelAndWait()
```

`NewExportLogsModel` 把 nil `Context` 收成 `context.Background()`。每个 generation 由 `exportRunner.Start` 新建 child context、容量 1 的 result channel 和 `done`。Start 在持锁登记 cancel/done/running 后、返回前同步启动唯一 goroutine；goroutine 必须 `defer`+`recover` 把 panic 转成稳定 export failure，仍写入/关闭 result 并关闭 `done`，不得让进程崩溃。先把唯一 `exportResultMsg` 写入 buffered result channel并关闭 result channel，再在锁内清 running并关闭本次 `done`，此后不再访问 runner 拥有的资源。`Open()` 显示打开时刻的只读 Now，并生成默认 basename 预览。默认 ExportDir 不存在或 `OpenPublishDir` 失败时 `Exists` 视为 false，使用无后缀名，且 **不得** `EnsureDir`；探测用的 `PublishDir` 必须在 `Open()` 返回前 Close。禁止 `os.Lstat`/`os.Stat` 完整路径。nil `Exists` 或 `PrivateFS==nil` 时不探测，直接使用无后缀候选。`LogExportDir` 只在真正提交默认导出时由 Export resolver `EnsureDir`。`Closed()` 时除 root 将处理的 `OpenExportLogsMsg` 路径外全部 `consumed=false`，否则 Logs `e` / System Enter 打不开对话框。打开时对未知 `tea.Msg` 默认 `consumed=false`，包括 `ui.LoggingSyncMsg`、`ui.LoggingObservedMsg`、`EventStatus`、`EventLogging`。打开时 `ctrl+c` 以及非文本焦点的 `q` 也 `consumed=false`，交给根模型 `tea.Quit`；`finishRun` 仍 `CancelAndWait`。`done` 是该 generation 所有实质工作已结束的最后完成信号，因此不依赖 Bubble Tea Cmd 开始才结束。Start 在前一 generation 仍 running 时返回 false。`Cancel` 只在锁外调用当前 cancel、不等待，供 Esc 使用；`CancelAndWait` 快照当前 cancel/done，非 running 或 `done == nil` 立即返回，否则锁外 cancel 再等待 done；两者幂等且不 close channel。Model 的 `CancelAndWait` 只委托 runner；result 路径只清 pending，不再 close done。`Pending()` 不能代替 Wait。

- [ ] **Step 1: 写默认值/格式/导航 Red**

固定时钟在 +08:00。`Open()` 显示打开时刻 `2026-09-02 23:41:08 +08:00`，默认 range Last 24 hours，默认 path 预览 exact `mihari-logs-20260902-234108+0800.zip`。注入 `Exists`：目录不存在时仍用无后缀名且零 `EnsureDir`；无后缀名已存在时 Open 预览选用 `-1`。submit 再取一次 Now 专用于时间窗 UTC 转换（Last 24h/60m）；若 Output path 仍等于 Open 时的默认预览，submit 用这次 Now 重算默认 basename 并再跑 Exists。用户改过 path 则不改名。默认路径提交 `ExportRequest.AutoNumber=true`，自定义路径 `false`。若预览已是 `…±HHMM-N.zip`，`Base` 仍是完整时间戳 stem（含 `±HHMM`）、`Suffix=N`，`Advance` 得到 `Base-(N+1).zip`。不得把 `-0500` 或 `…-1` 整段误当新 Base。同一 model 关闭后再次 Open 会重置表单但保持 generation 单调递增。Range Enter 循环 24h→60m→Between→All；Between 初次值为 Open Now-24h/Now；Tab/Shift+Tab 只经过当前可编辑字段；文本焦点下 `tea.PasteMsg`/`ClipboardPasteMsg` 被 overlay 消费；未知消息 `consumed=false`；Esc 在 editable 直接 closed。

- [ ] **Step 2: 写提交/pending/cancel Red**

断言 From/To 用本地 `2006-01-02 15:04` 严格 parse，From>To 显示稳定错误，参数保留；文本焦点 Enter 提交；runner 期间 Pending 禁止再提交/Tab/编辑；Esc 调 cancel 但仍 Pending，收到当代 generation 结果后显示 `Export cancelled` 并回 editable；过期 generation 忽略。连续两次成功导出不得 panic。关键回归：调用 submit `Update` 得到 Cmd 后**不执行该 Cmd**，立即触发外部 context cancel并调用 `CancelAndWait`；fake Export 必须观察 cancel、runner 关闭 done、Wait 及时返回。另覆盖 q/Ctrl+C/Bubble Tea 错误退出路径阻塞到 runner 结束；非 pending 时立即返回；若 Cmd 已启动等待 result，也必须因 buffered result 解阻而退出。

- [ ] **Step 3: 写 success/copy/error Red**

成功显示 exact 绝对路径；Enter 调 clipboard，失败仍保留路径并显示 `Could not copy path`；Esc close。`ErrNoLogLines`、invalid target、exists、IO failure 映射为一行净化文案，不展示底层 error/path secret。

```powershell
go test -run '^TestExportLogsModel_' ./internal/tui/ui
```

Expected: FAIL，对话框不存在。

- [ ] **Step 4: 实现同步登记的 owned export runner**

`NewExportLogsModel` 创建初始 closed、且不含 idle goroutine 的 runner；`tui.Run` 在整个 Program 生命周期只创建这一份 model，后续打开复用它。`Open()` 在非 pending 时用打开时刻 Now 重置表单但不重置 generation。submit 再取一次 Now 计算时间窗；默认路径请求必须 `AutoNumber=true`。submit 先 `generation++`，同步调用 runner.Start；成功后立刻标 pending，并返回一个只执行 `<-resultChannel` 的 `tea.Cmd`，Cmd 不创建 Export context、不启动 Export、不拥有 done。若 Start 返回 false，保持表单并显示固定 busy 错误。Pending 下 Esc 调 `runner.Cancel()` 后保持 Pending，直到 result 到达；收到 result 时先清 pending，再转状态，不 close runner channel。`tui.Run` 的 once-cleanup 直接捕获这份稳定 model 指针并调用 `CancelAndWait()`，不依赖 `Program.Run` 返回的 final model 类型；即使 Bubble Tea context 在 Model.Update 与 command 入队之间取消，实际 Export goroutine也已经存在并由 cleanup 回收。Clipboard 默认实现用 `clipboard.WriteAll`，测试全部注入，不读写真实剪贴板。

```powershell
gofmt -w internal/tui/ui/exportlogs.go internal/tui/ui/exportlogs_test.go internal/tui/ui/textfield.go internal/tui/ui/strings.go internal/tui/ui/keymap.go internal/tui/ui/keymap_test.go
go test ./internal/tui/ui
go test -race ./internal/tui/ui
```

Expected: PASS。

- [ ] **Step 5: Commit（仅得到授权时）**

```powershell
git add internal/tui/ui/exportlogs*.go internal/tui/ui/textfield.go internal/tui/ui/strings.go internal/tui/ui/keymap*.go
git commit -s -m "feat: 增加共享日志导出对话框"
```

---

### Task 7: 根 overlay 与 Logs/System 双入口

**Files:**
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/model_test.go`
- Modify: `internal/tui/run.go`
- Modify: `internal/tui/run_test.go`
- Modify: `internal/tui/pages/logs/model.go`
- Modify: `internal/tui/pages/logs/model_test.go`
- Modify: `internal/tui/pages/logs/section_test.go`
- Modify: `internal/tui/pages/system/model.go`
- Modify: `internal/tui/pages/system/model_test.go`
- Modify: `internal/tui/pages/system/section_test.go`
- Modify: `cmd/mihari/main.go`
- Modify: `cmd/mihari/main_test.go`
- Modify: `internal/tui/testdata/**/*.golden`

**Interfaces:** `tui.Options` 新增 `BuildExportLogs func(LoggingResources) ui.ExportLogsOptions`；root `Model` 新增稳定的 `exportLogs *ui.ExportLogsModel`。`Run` 在 `options.Client != nil` 导致 `newModelWithClientContext` **整棵替换之后**，调用 factory 并只构造一次该 model，把同一指针注入最终 root 与 once-cleanup，并用 Program context 填 `Options.Context`。禁止写进第一份 `NewModel()`。logger Open 失败但 `PrivateFS`/Redactor 可用时仍能构造真实 Export。现有 `NewModel()`/`model_test.go` 路径必须 nil-guard `exportLogs`。Logs/System 只返 `ui.OpenExportLogsMsg{}`，不相互 import。

- [ ] **Step 1: 写 root overlay 优先级 Red**

断言 Open message 对 `Run` 注入的同一 model 调 `Open()`，不得为每次打开新建 runner；连续关闭/重开时 generation 不复用。root `Update` 在 typed switch / `dispatchPage` **之前**对每一条 `tea.Msg` 调 `exportLogs.Update`；`exportLogs` 为 nil 时跳过。打开时 overlay 消费键盘、当代 result、文本焦点 paste/clipboard（`consumed=true`）；`EventStatus`、`EventLogging`、`ui.LoggingSyncMsg`、`ui.LoggingObservedMsg`、service/network poll、`ui.PageResultMsg`、action result 等其它消息 `consumed=false` 后继续走根模型**原有完整路由**，包括对目标页面的 `dispatchPageTo`。不得只按 `tea.KeyPressMsg` 才进入 overlay，否则未导出的 `exportResultMsg` 会被 `dispatchPage` 丢掉。测试一：Pending 导出期间注入 revision/`LoggingObservedMsg`，关闭 overlay 后根/System 状态已更新；测试二：先启动页面 action并打开 overlay，再注入 completion，断言根与来源页面 pending 都清除且结果已应用；测试三：overlay 期间 `ui.PageResultMsg` 仍更新对应页面。View：导出 overlay 打开时必须在 `active == PageSetup` 短路和 `model.modal != nil` 整屏替换 **之前**把 overlay 写入 `content`，然后走现有 `tea.NewView` 收尾（`AltScreen=true`、`WindowTitle`）；禁止在函数入口直接 `return tea.NewView(overlay.View(...))`。其它 modal 可入队但不覆盖 View、不抢键盘，直到 overlay `Closed()`。closed 后回原页/原 focus。

`finishRun` 使用 PR 1 建立的 once-cleanup；顺序固定为 session Close → 调 `Run` 直接持有的稳定 ExportLogsModel 指针 `CancelAndWait` → PR 2 applier `CloseAndWait` → `LoggingResources.Close`（Runtime 后 PrivateFS），全部在调用 `Relaunch` **之前**完成。清理不从 final model 做类型断言；任何一步的 warning 不得跳过后续步骤。覆盖 `q`、Ctrl+C、外部 context cancel、Bubble Tea 错误退出、final model 为 nil/非预期类型和 Unix relaunch，包括“submit Update 已返回、Cmd 尚未执行”场景，均无遗留实质工作或未关的 log/lock/directory/workspace fd。

- [ ] **Step 2: 写 Logs 入口 Red**

Controls 顺序为 Level/Wrap/Pause/Export，左右上限更为 3，Enter 在 Export 发 open message；导航态的 `e` 从 control/row 都打开，searching、search focus、detail 时 `e` 不打开；footer 和 help 含 `e export`。

- [ ] **Step 3: 写 System 入口 Red**

Logging section 在 Directory 后增 `Export logs`；无 daemon/Logging capability 时仍可聚焦并打开；Enter 发同一 open message；不发 PATCH，不要求 revision。短高度 scroll 和 golden 更新后聚焦仍可见。

- [ ] **Step 4: 装配真实 exporter**

`cmd/mihari` 继续由 PR 1 `OpenLogging` 创建并返回 `tui.LoggingResources`。新增的 `BuildExportLogs` closure 捕获进程入口已绝对化的 Paths，从 resources 取同一 redactor/`PrivateFS` 并注入五条路径；**不依赖 logger Open 成功**（`Runtime==nil` 时 `EnterRecordMutex` 为 no-op，仍可导出 daemon/mihomo）。`Run` 必须把 `tea.WithContext` 使用的那个 ctx 写入 `ExportLogsOptions.Context`（factory 本身无 ctx 时由 `Run` 填充）；nil Context 在 `NewExportLogsModel` 收成 Background。`logging.Export` 把 nil `OpenLock` 收成 `platform.OpenAdvisoryLock`。`BuildExportLogs` 必须从 TUI `Runtime`（若非 nil）注入 `EnterRecordMutex`，把该进程 rotator 的记录 mutex 接到 `mihari-tui.log`；daemon/mihomo 路径传 nil/no-op。不依赖 logger Open 成功仍可导出 daemon/mihomo 文件。`Exists` 必须经 held default-export `PublishDir`/`PrivateFS` 做 basename 探测，不得 `os.Lstat`；目录缺失时返回 false 且不创建目录，探测 handle 在 Open 返回前 Close。`Run` 在最后一次重建 Model 之后用返回 options 创建唯一 ExportLogsModel，root 与 cleanup 共用该指针。默认提交 `AutoNumber=true`。resources.PrivateFS 为 nil 时仍可打开对话框，但提交只返回固定 `Local log storage unavailable`，不得尝试普通 `os.Open` 绕过安全边界。logger 失败但 fs/redactor 可用时 Export 正常；无匹配文件走空 snapshot。`Now` 默认 `func() time.Time { return time.Now().In(time.Local) }`，clipboard 默认 `clipboard.WriteAll`。测试覆盖：OpenLogging 返回部分 resources+error 时仍能打开 overlay并提交 fake/真实 temp export；注入 temp paths/fake export/copy，不访问真实用户路径；Run 只由 `LoggingResources.Close` 关闭共享 PrivateFS，Export/overlay 不关闭它。

```powershell
gofmt -w internal/tui/model.go internal/tui/model_test.go internal/tui/run.go internal/tui/run_test.go internal/tui/pages/logs/model.go internal/tui/pages/logs/model_test.go internal/tui/pages/logs/section_test.go internal/tui/pages/system/model.go internal/tui/pages/system/model_test.go internal/tui/pages/system/section_test.go cmd/mihari/main.go cmd/mihari/main_test.go
go test ./internal/tui/pages/logs ./internal/tui/pages/system ./internal/tui ./cmd/mihari
go test -race ./internal/tui ./cmd/mihari
```

Expected: PASS。

- [ ] **Step 5: Commit（仅得到授权时）**

```powershell
git add internal/tui/model.go internal/tui/model_test.go internal/tui/run.go internal/tui/run_test.go internal/tui/pages/logs internal/tui/pages/system cmd/mihari internal/tui/testdata
git commit -s -m "feat: 在 Logs 与 System 接入日志导出"
```

---

### Task 8: 模糊/集成回归、文档与 PR 3 总验收

**Files:**
- Create: `internal/logging/export_fuzz_test.go`
- Modify: `internal/integration/*_test.go`
- Modify: `README.md`
- Modify: `README.zh-CN.md`
- Modify: `docs/commands.md`
- Modify: `docs/architecture.md`
- Add: `docs/superpowers/plans/2026-09-02-log-export-ui.md`

- [ ] **Step 1: 加入有界 parser/path fuzz 与集成竞争测试**

fuzz seed 含合法 JSONL、非法 UTF-8、极长无 newline、嵌套 sensitive fields、大整数。不变量：不 panic、不无界分配、输出每行可 JSON decode、已知 secret 不出现。集成测试运行两个 TUI writer+一个 exporter，导出的快照无重复/无半行，同时 writer 继续轮转。

```powershell
go test -run '^TestFileLoggingExport_' ./internal/integration
go test -run '^FuzzExport' ./internal/logging
```

Expected: PASS。Fuzz 只运行 seeds，不开无限 fuzz session。

- [ ] **Step 2: 更新用户文档**

记录两个 TUI 入口、时间窗、默认 `logs-export` 目录、不覆盖规则、zip 固定 entry、二次脱敏，解释 Unix 不可信共享父目录可能留下空私有 workspace（内容清理成功时）、清理 IO 失败会报告可能残留，同 UID/管理员信任前提，以及“发送前自查节点名/目标域名/IP/流量元数据”提醒。架构文档说明导出全程持有目标父目录 identity，父路径在生成期间被替换会安全失败而不会跟随；成功后父目录被外部再次改名可能令已显示路径失效。明确没有 CLI export 命令。

- [ ] **Step 3: 全量验证**

```powershell
gofmt -l .
go test ./internal/platform ./internal/logging ./internal/tui/ui ./internal/tui/pages/logs ./internal/tui/pages/system ./internal/tui ./cmd/mihari
go test -race ./internal/platform ./internal/logging ./internal/tui ./cmd/mihari
go test ./internal/integration
go test ./...
go vet ./...
git diff --check
```

Expected: 全部 PASS，`gofmt -l .` 无输出，失败/cancel 测试无本次 target 且关闭所有 handle；可信 parent 且无 cleanup IO 故障时无 temp/workspace 残留，Unix 降级和故障按 spec 5.9.1 验证 warning/残留边界。

- [ ] **Step 4: 六目标 CGO-free 编译**

```powershell
$env:CGO_ENABLED='0'
$targets=@(@('windows','amd64','.exe'),@('windows','arm64','.exe'),@('linux','amd64',''),@('linux','arm64',''),@('darwin','amd64',''),@('darwin','arm64',''))
foreach($t in $targets){$env:GOOS=$t[0];$env:GOARCH=$t[1];go build -o (Join-Path $env:TEMP ("mihari-{0}-{1}{2}" -f $t[0],$t[1],$t[2])) ./cmd/mihari;if($LASTEXITCODE -ne 0){throw "build failed: $($t[0])/$($t[1])"}}
```

Expected: 六目标成功。Windows 本地 `go test` 覆盖 snapshot share-delete；Unix no-follow/identity 测试由 CI Ubuntu 或 macOS job 实际执行，交叉编译不能代替。

- [ ] **Step 5: 范围与产物审查**

```powershell
git status --short
git diff --stat
git diff --name-only
```

不得出现 `CHANGELOG.md`、新 control endpoint、新 CLI command、测试生成的 zip/temp/coverage 产物。

- [ ] **Step 6: Commit 文档（仅得到授权时）**

```powershell
git add internal/logging/export_fuzz_test.go internal/integration README.md README.zh-CN.md docs/commands.md docs/architecture.md docs/superpowers/plans/2026-09-02-log-export-ui.md
git commit -s -m "docs: 记录日志导出与安全边界"
```

---

## Self-Review

| Spec 要求 | 任务 |
| --- | --- |
| Windows delete-sharing snapshot、PR 1 `FileEntry`/`OpenReadChecked` 从 LogDir fd 逐段 no-follow 并核对 identity | Task 1、3 |
| held `DirectoryIdentity` + `PublishDir.IsWithin`；私有 `PublishWorkspace`；Unix workspace→parent linkat / Windows temp handle→RootDirectory no-replace rename；幂等 Close/warning | Task 1 |
| 绝对 zip、held real LogDir containment、symlink data-root/exists/parent 规则；resolver 同时转交 LogDir identity/PublishDir 所有权，每轮 publish 紧邻提交前重查 containment | Task 1–2、5 |
| Export 库内部不再调 `time.Now()`；对话框打开取样一次用于显示/预览，提交再取一次算 UTC 窗；All 省略 manifest from/to | Task 2、4、6 |
| shared lock 内 suffix 降序枚举/open/stat，空来源不是错误 | Task 3 |
| 1 MiB 有界 invalid discard、UseNumber、恰好一个 top-level object/二次 Decode EOF、无 wall-clock 重排 | Task 4 |
| 递归二次脱敏、record 级 redacted 计数 | Task 4 |
| manifest exact schema/files/sources/notes，无敏感信息 | Task 4–5 |
| 无命中 entry 省略，三源无命中不发布 | Task 5 |
| workspace-relative spool→zip、命名的 `exportOps` 故障 seam、可取消拷贝、cancel/失败 cleanup，父/workspace 路径替换不逃逸，auto-number publish loop 与不可逆点 | Task 1、2、5 |
| Now/range/from/to/path 表单、Pending/Esc cancel、Run 全生命周期稳定 model/单调 generation、同步登记 owned runner/result 后关闭 `done`/unscheduled Cmd cancellation/`CancelAndWait`/success/copy | Task 6–7 |
| Logs `e`/Controls 与 System 离线 Export 共用组件；Export 不依赖本进程 logger | Task 7 |
| overlay 只独占键盘和自身 result，其它消息保留完整页面路由；finishRun 在 Relaunch 前 session→Export→applier→runtime→PrivateFS | Task 7 |
| 并发 writer/export 集成、fuzz、race、Windows+Unix CI 实测、六目标编译 | Task 8 |

**Placeholder scan:** 无占位任务、未定 entry 或未命名 message。`ExportRequest`、`ExportResult`、`ExportRange`、`exportTarget.Advance`、`manifestRange`、`exportStage`/`exportOps`/`zipWriter`、`ExportLogsOptions`、`exportRunner.Start`/`Cancel`/`CancelAndWait`、`OpenSnapshot(FileIdentity)`、`DirectoryIdentity`/`PublishDir.IsWithin`、`PublishWorkspace`/handle-relative `PublishNoReplace`、平台入口与 zip entry 均已固定。

**Type consistency:** 时间输入为 `time.Time`，窗口规范化后用 UTC instant 比较，manifest 用 string；行/跳过/脱敏计数、auto-number suffix 与 snapshot size 为 `int64`；JSON 数字保持 `json.Number`；PublishDir/PublishWorkspace 只接收各自目录内单段 basename，ExportResult 使用实际成功候选的 canonical absolute Path；TUI async 结果按 `uint64` generation 区分过期 runner。
