# 安全文件日志基础 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在不改变稳定控制协议和 `mihari.yaml` 的前提下，让 daemon、TUI 和 mihomo 分别安全写入 JSONL 文件，实现按大小轮转、多 TUI 跨进程互斥、写入前脱敏与可验证的关闭顺序。

**Architecture:** `internal/platform` 提供绝对路径解析、可关闭的受限文件 capability、owner/ACL 和 advisory lock；`internal/logging` 在其上组合 `Redactor` → `slog.JSONHandler` → `RotatingWriter`，并以 `Runtime`/`Group` 暴露热更新与生命周期。`cmd/mihari` 在 Settings 和目录就绪后装配 daemon/mihomo logger，TUI 则以 debug/100 MiB/10 的保守 bootstrap 独立打开共享日志；进程装配点在最后一个使用者结束后关闭 `PrivateFS`。

**Tech Stack:** Go 1.26.5，标准库 `log/slog`、`sync/atomic`，既有 `golang.org/x/sys/windows`、`golang.org/x/sys/unix`，Bubble Tea v2，Go `testing`/helper subprocess。

**Spec:** `docs/superpowers/specs/2026-09-02-file-logging-export-design.md`

**Worktree:** `.worktrees/feat-file-logging-export`

**Branch:** `feat/file-logging-export`
**Delivery:** 本文档对应 spec 第 1 个顺序 PR，目标分支为 `dev`。

## Global Constraints

- 本 PR 不新增 `/v1/logging`，不改 `config.Settings`，不增 CLI，不修改 `CHANGELOG.md`。
- 首个行为 commit 先修改根 `AGENTS.md` 与 `docs/architecture.md`：TUI 仅可通过 `internal/logging` 向固定 `mihari-tui.log*` 追加/轮转，不得扩展为 settings 或业务状态写入。
- daemon 或 mihomo 任一侧文件日志 `Open` 失败都是启动失败；TUI 初始化失败不退出，只写入限频且脱敏的 stderr。
- `max-files` 包含活跃文件；`1` 仅在 rotate 时原子替换为新空 inode，禁止原 inode truncate，Open/Apply 不得替换活跃文件；`N` 最多留 `.1` 到 `.(N-1)`。
- 写入锁最多等待 250 ms；未获锁就丢弃整条 record 并累加计数，不拆行、不阻塞进程。Open/Apply 的 archive 维护同样只对 advisory-lock **获取**施加调用方 context 与 250ms 上限；获锁后的 `ReadDir`/`Remove` 不能被 Go context 抢占，也不承诺整个本地文件系统维护在 250ms 内结束。
- 活跃文件只能在获得跨进程锁后打开；不缓存跨锁的 file handle，避免写入已被其他 TUI rename 的旧 inode。
- 符号链接、目录、reparse point、非法 suffix 全部 fail closed 或记录后跳过。打开必须从已验证 dataRoot 句柄逐段 no-follow，禁止完整路径一次 `Open`/`O_NOFOLLOW`，禁止 Windows「打开后再拒绝 reparse」。
- `NewPrivateFS` 安全创建尚不存在的绝对 dataRoot，并在任何 `Paths.EnsureDirs`、默认 in-root token 或 Settings IO 前、由**需要本地数据根的选定命令**调用；root/LocalSystem 缺根必须零 IO fail closed。该失败不得写入 CLI `SetupError`。`cmd/mihari` 的 `main` 与 Cobra 解析不得在 `--help`/`--version` 之前调用 `Absolute`/`NewPrivateFS`/`LoadOrCreate`。daemon 与 TUI 复用并接管这一个进程级 capability，不得在 `EnsureDirs`/Settings 之后再调一次 `NewPrivateFS`；在 `Open` 前都调用 `PrivateFS.EnsureDir(LogDir)`；`logging.Open` 对父目录再 EnsureDir 一次。`Paths.EnsureDirs` 的 `MkdirAll` 只能在 capability 建立后调用且不算加固。TUI 在 `PrivateFS=nil` 时继续运行；daemon 见到 nil 则启动失败并跳过目录/Settings IO。
- `cmd/mihari` 在首次使用 Paths 前调用 `DefaultPaths().Absolute()`；相对 `MIHARI_DATA` 以启动 working directory 为基准。保留 `MIHARI_CONTROL_CREDENTIAL`：非空覆盖也只 `Abs/Clean` 一次，为空才用 `absolutePaths.ControlToken`；client/daemon/TUI/redactor 共用最终 path/token。
- `PrivateFS` 持有 dataRoot 目录 handle，必须幂等 `Close`；所有方法在 Close 后返回 `os.ErrClosed`。先关闭/等待所有 logger、lock、Apply/Export 使用者，最后关闭 `PrivateFS`，Unix relaunch 前必须完成。
- Unix 保持 0700 目录/0600 文件；打开一律 `O_CLOEXEC`。root service 对已有数据根使用该根的 owner UID/GID。Windows 目录 DACL 带 `OICI` 继承，文件 DACL 不带继承；只授予数据根 owner SID 与 LocalSystem。Windows 日志/lock/temp 打开一律 `FILE_SHARE_READ|FILE_SHARE_WRITE|FILE_SHARE_DELETE` 且 `InheritHandle=false`；rotate/`ReplaceEmpty` 前关闭本进程 write handle。
- mihomo capture 单行上限 `MaxCaptureLineBytes = 16 << 10`；导出解析上限 `MaxExportRecordBytes = 1 << 20`。JSON 字段名是 `component`，不是 `source`。
- 轮转 size 判断禁止 `currentSize + incoming`，必须用溢出安全比较。
- 不增新依赖；使用现有 `golang.org/x/sys v0.46.0`。保持 `CGO_ENABLED=0` 和 Windows/Linux/macOS amd64/arm64。
- 行为修改必须 Red–Green–Refactor；外部 IO、锁等待和时钟可注入，测试不读用户目录、不访公网、不启动真实 mihomo。
- 实际 commit 仅在用户明确要求时创建；以下 commit 命令是可审查的任务边界，不是预授权。

## File Structure

| 文件 | 职责 |
| --- | --- |
| `AGENTS.md` | 声明 TUI 固定日志写入的狭义例外 |
| `docs/architecture.md` | 记录三日志、锁和 owner/ACL 边界 |
| `internal/platform/paths.go` | 绝对化 Root；`LogDir`、`DaemonLog`、`TUILog`、`MihomoLog`、`LogExportDir` |
| `internal/platform/privatefs.go` | 可关闭的受限目录/文件通用入口与路径边界验证 |
| `internal/platform/privatefs_unix.go` / `_windows.go` | UID/GID、mode 或 protected DACL；Unix 文件必须 `//go:build unix` |
| `internal/platform/filelock.go` | advisory lock 接口和可取消重试器 |
| `internal/platform/filelock_unix.go` / `_windows.go` | `flock` / `LockFileEx` 非阻塞尝试；Unix 文件必须 `//go:build unix` |
| `internal/logging/config.go` | `Config`、`DefaultConfig`/`BootstrapConfig`、`ParseLevel`、size/files 范围；`ConfigFromFields` 由 PR 2 增补 |
| `internal/logging/redactor.go` | 不可变规则快照、通用规则和 exact secrets |
| `internal/logging/rotator.go` | 整 record 写入、溢出安全轮转、热 limits、分类失败计数 |
| `internal/logging/handler.go` | `slog.JSONHandler`、`LevelVar`、RFC3339Nano 本地偏移、`component`、attr 脱敏 |
| `internal/logging/runtime.go` | logger/writer 所有权、`Group` 热更新、`Apply(context.Context, Config)` |
| `internal/logging/capture.go` | mihomo stdout/stderr 行切分、UTF-8 和 16 KiB 截断 |
| `internal/app/runtime.go` | 将 mihomo capture 注入 `supervisor.CommandStarter` |
| `internal/supervisor/command.go` | Child.Wait 后 Flush capture，不 Close |
| `cmd/mihari/main.go` | 抽出 `prepareLocalRoot`；daemon/mihomo 日志打开、背景错误和关闭顺序 |
| `internal/cli/root.go` | `PersistentPreRunE` 调用 `prepareLocalRoot`；help/`self version` 跳过 |
| `internal/control/client/client.go` | `SetToken`：PreRun 之后把最终 token 写入已构造的 local client |
| `internal/tui/run.go` | TUI bootstrap logger 创建、降级、关闭 |
| `internal/tui/model.go` | 注入 `LocalLoggingHealth` |
| `README.md` / `README.zh-CN.md` | 用户可见日志路径、轮转缺省与隐私提示 |
| `docs/commands.md` | daemon/TUI 文件日志行为与排障入口 |

---

### Task 1: 先固化架构例外与路径契约

**Files:**
- Modify: `AGENTS.md`
- Modify: `docs/architecture.md`
- Modify: `internal/platform/paths.go`
- Modify: `internal/platform/paths_test.go`

**Interfaces:** `platform.Paths` 删除 `Log`，新增 `LogDir`、`DaemonLog`、`TUILog`、`MihomoLog`、`LogExportDir`。`EnsureDirs` 可以预创建 `LogDir`（`MkdirAll`），但不加固、不拒绝中间 symlink；不预创建 `LogExportDir`。加固只认 `PrivateFS.EnsureDir`。

```go
func (p Paths) Absolute() (Paths, error)
```

`Absolute` 对 `p.Root` 调 `filepath.Abs/Clean` 后用 `NewPaths(absRoot)` 重建全部派生字段；不得保留调用方可能混入的相对字段。它不 EvalSymlinks，dataRoot 允许跟随的一跳仍由 `NewPrivateFS` 固定 identity。

- [ ] **Step 1: 写 Paths 失败测试**

在 `paths_test.go` 表驱动断言五个路径的 exact basename，断言 `EnsureDirs` 创建 `logs` 但不创建 `logs-export`。另用 `t.Chdir(t.TempDir())` 和 `NewPaths(filepath.Join(".", "relative-data"))` 断言 `Absolute` 返回绝对 Root、五个派生字段全部来自该 Root，原值不变；绝对 Root round-trip。删除 `Paths.Log` 后迁移全部调用点；Task 9 用 `rg 'Paths\.Log\b|\.Log\b' --glob '*paths*.go'` 以及 `rg 'filepath\.Dir\(p\.Log\)|paths\.Log\b' --glob '*.go'` 验收，不把 `event.Log` 当命中，不写依赖编译失败的测试。

```go
want := map[string]string{
	"LogDir":      filepath.Join(root, "logs"),
	"DaemonLog":   filepath.Join(root, "logs", "mihari-daemon.log"),
	"TUILog":      filepath.Join(root, "logs", "mihari-tui.log"),
	"MihomoLog":   filepath.Join(root, "logs", "mihomo.log"),
	"LogExportDir": filepath.Join(root, "logs-export"),
}
```

Run:

```powershell
go test -run '^Test(NewPathsLoggingLayout|EnsureDirsLoggingLayout)$' ./internal/platform
```

Expected: FAIL，因新字段尚未存在。

- [ ] **Step 2: 最小修改 Paths 并更新架构文档**

`EnsureDirs` 加 `p.LogDir`，删 `filepath.Dir(p.Log)`；不加 `p.LogExportDir`。实现 `Paths.Absolute`，错误包装为 `resolve absolute data root: %w`。调用契约明确：需要本地数据根的选定命令必须在 `EnsureDirs`/默认 token 之前调用 `NewPrivateFS(absolutePaths.Root)`，`EnsureDirs` 自身不是 root 创建/加固入口。`--help`/`--version` 不得调用。TUI 允许该调用失败并继续；daemon 不允许在失败后继续做目录 IO。根规范和架构文档明确：TUI 只写固定日志序列，settings 仍只有 daemon/Manager 可写。

```powershell
gofmt -w internal/platform/paths.go internal/platform/paths_test.go
go test ./internal/platform
git diff --check
```

Expected: PASS；`git diff` 无 `CHANGELOG.md`。

- [ ] **Step 3: Commit（仅得到用户明确提交授权时）**

```powershell
git add AGENTS.md docs/architecture.md internal/platform/paths.go internal/platform/paths_test.go
git commit -s -m "docs: 明确 TUI 日志写入例外"
```

---

### Task 2: 私有文件与数据根 owner/ACL

**Files:**
- Create: `internal/platform/privatefs.go`
- Create: `internal/platform/privatefs_unix.go`
- Create: `internal/platform/privatefs_windows.go`
- Create: `internal/platform/privatefs_test.go`
- Create: `internal/platform/privatefs_unix_test.go`
- Create: `internal/platform/privatefs_windows_test.go`

**Interfaces:**

```go
type PrivateFS struct { /* platform-owned identity */ }
type FileIdentity struct { /* opaque platform file identity */ }
type DirectoryIdentity struct { /* held opaque platform directory identity */ }
type FileEntry struct {
	Name     string
	Mode     os.FileMode
	Identity FileIdentity
}

func NewPrivateFS(dataRoot string) (*PrivateFS, error)
func (fs *PrivateFS) EnsureDir(path string) error
func (fs *PrivateFS) OpenAppend(path string) (*os.File, error)
func (fs *PrivateFS) OpenReadChecked(path string, expected FileIdentity) (*os.File, error)
func (fs *PrivateFS) CreateTemp(dir, pattern string) (*os.File, error)
func (fs *PrivateFS) ReplaceEmpty(path string) error
func (fs *PrivateFS) ReadDir(path string) ([]FileEntry, error)
func (fs *PrivateFS) OpenDirIdentity(path string) (*DirectoryIdentity, error)
func (d *DirectoryIdentity) Close() error
func (fs *PrivateFS) Rename(oldpath, newpath string) error
func (fs *PrivateFS) Remove(path string) error
func (fs *PrivateFS) Close() error
```

`NewPrivateFS` 只接受绝对 dataRoot。若根不存在，交互用户进程以 Unix 0700 或 Windows 当前用户+LocalSystem protected DACL 创建并立即打开/加固；root/LocalSystem 直接 fail closed，服务安装必须预先创建固定 dataRoot 并赋予桌面 owner，不能创建仅 root/System 可访问或宽权限替代根。若已存在，打开时**允许跟随这一跳**（`~/.mihari` 可以是指向其他盘的 symlink/junction）。随后 `fstat` 确认是目录并持有 identity。公开方法接受绝对路径或单段 basename（Task 5/7/8 继续传 `LogDir`/`BasePath`；PR 3 传 `LogExportDir`）。内部把路径 Rel 到已验证 dataRoot：必须落在 dataRoot、LogDir 或默认 ExportDir 下，最后一段无分隔符且不是 `.`/`..`，再对该 parent fd + basename 操作。禁止内部再 walk 完整路径调用 `os.OpenFile` / `os.Rename` / `os.Remove` / `os.ReadDir` / `chmod` / `chown` / `SetNamedSecurityInfo`。Unix 用 `openat`/`renameat`/`unlinkat` 以及 `fchmod`/`fchownat`；Windows 用相对 `NtCreateFile` / `NtSetInformationFile`，DACL 在创建时经 `SECURITY_ATTRIBUTES` 或对已打开 handle 调 `windows.SetSecurityInfo`。`ReadDir` 对每个 no-follow 目录项返回可与 handle 比较的 opaque identity；`OpenReadChecked` 用 delete-sharing/no-follow 打开后比较实际 identity，不符则关闭并返回稳定错误。`OpenDirIdentity` 只从已验证且持有的目录 handle 派生一个独立、幂等可关闭的 capability，供 PR 3 containment 使用，不对路径字符串做 `EvalSymlinks`；PrivateFS 关闭前必须先关闭这些派生 capability。`Rename` 要求 old/new 解析到同一已验证目录 fd。`PrivateFS` 用一个 `sync.RWMutex` 和 `closed bool` 保护 dataRoot/缓存目录 handle；重复 Close 返回 nil，Close 后所有公开文件操作返回可 `errors.Is(err, os.ErrClosed)` 的错误。

- [ ] **Step 1: 写通用边界与 Unix 权限 Red**

测试覆盖：拒绝相对 dataRoot、data root 外路径、`logs/../outside`、`.`/`..` 最后一段；尚不存在的绝对 dataRoot 被安全创建并持有，control credential 位于根外也不影响；接受绝对 `LogDir`/`BasePath`/`LogExportDir` 与单段 `"logs"` / `"logs-export"`；dataRoot 本身是指向普通目录的 symlink/junction 时 `NewPrivateFS` 成功且后续打开落在目标目录；`logs/` 换成 junction/symlink 后 `EnsureDir(LogDir)`/`OpenAppend`/`ReadDir`/`Rename`/`Remove`/`OpenReadChecked` 失败且外部目标零写入、零改名、零删除、DACL 不被打到 junction 目标；目录项 identity 与打开 handle 不符时 `OpenReadChecked` 关闭 handle 并失败；`OpenDirIdentity(LogDir)` 持有真实目录而不是 symlink dataRoot 字符串，Close 幂等且关闭后 capability 操作返回 `os.ErrClosed`；最终名指向根内 `mihari.yaml` 的链接时失败且 yaml 不被追加；已有宽权限目录收紧为 0700；新文件为 0600（root + umask 022 也必须 `fchmod`）；`ReplaceEmpty` 产生新 inode 且经 `OpenReadChecked` 打开的旧 handle 仍可读完（Windows DELETE share）。显式断言 `Close` 关闭持有的根/子目录 handle、重复调用幂等、Close 后每个公开文件操作都是 `os.ErrClosed`。Unix owner 测试通过注入 `effectiveUID`/`fstat`/`fchownat` 模拟 root，断言从已有 data root fd 取 UID/GID，不使用 `/root` 或受污染环境变量。

```powershell
go test -run '^TestPrivateFS_' ./internal/platform
```

Expected: FAIL，缺少 `PrivateFS`。

- [ ] **Step 2: 实现通用/Unix 路径**

Unix 文件顶部写 `//go:build unix`。打开 dataRoot 允许跟随最后一跳。`EnsureDir` 可接收绝对 `LogDir` 或 `"logs"`，内部 Rel 后对 parent fd + 单段名 `mkdirat`/`openat`。**文件操作只允许 LogDir / 默认 LogExportDir 的单段子名**，不得对 dataRoot 下任意 basename 做 `OpenAppend`（`logs/../outside` Rel 后变成兄弟名也必须拒绝）。操作只对已验证目录 fd + basename：`openat(..., O_NOFOLLOW|O_CLOEXEC)`，append 加 `O_APPEND|O_CREAT|O_WRONLY`，read 加 `O_RDONLY`；mode/owner 用 `fchmod`/`fchownat`，禁止路径名 `chmod`/`chown`。`renameat`/`unlinkat` 同样相对该 fd。root 时 owner 来自 data root fd 的 `Stat_t`；非 root 不 chown。`ReplaceEmpty` 本身不接收已打开 handle：调用方（rotator）必须先关掉本进程 write handle，再 `CreateTemp` 于同一目录 fd，`Sync`、`Close` 后 `Rename`，任何失败 `Remove` temp。公开操作在 `RWMutex.RLock` 覆盖的区间内检查 closed 并完成所有依赖根/目录 handle 的 syscall；`Close` 取得写锁，等待这些调用离开后原子标记 closed、逐一关闭全部目录 fd 并 `errors.Join`，避免 Close 与相对路径操作交错。

- [ ] **Step 3: 实现 Windows protected DACL 并写平台测试**

Windows 从 data root security descriptor 取 owner SID。目录与文件使用不同 ACL 模板：目录 `D:P(A;OICI;FA;;;<OWNER>)(A;OICI;FA;;;SY)`，文件 `D:P(A;;FA;;;<OWNER>)(A;;FA;;;SY)`。DACL 在 `NtCreateFile`/`CreateFile` 时经 `SECURITY_ATTRIBUTES` 带入，或对已打开 handle 调 `windows.SetSecurityInfo(..., DACL_SECURITY_INFORMATION|PROTECTED_DACL_SECURITY_INFORMATION, ...)`；**禁止** `SetNamedSecurityInfo`。实际 owner 为 SYSTEM 时只保留一个 SYSTEM ACE。打开 dataRoot 允许跟随用户配置的最后一跳；之后每一跳相对打开：`CreateFile` 用 `FILE_FLAG_OPEN_REPARSE_POINT`，`NtCreateFile` 用 CreateOptions `FILE_OPEN_REPARSE_POINT`。看到 reparse/junction 立即失败。对已存在对象加固 DACL 需要 `WRITE_DAC`。`NtSetInformationFile` 带 `FILE_RENAME_POSIX_SEMANTICS` 以支撑「旧 OpenReadChecked handle 仍可读完」。dataRoot、目录、`OpenAppend`/`OpenReadChecked`/`CreateTemp`/`ReplaceEmpty`/lock 一律 `FILE_SHARE_READ|FILE_SHARE_WRITE|FILE_SHARE_DELETE`，`bInheritHandle=false`；`Close` 与操作用同一 closed/handle 所有权协议，关闭全部 `windows.Handle`。平台测试枚举 DACL，拒绝 `Everyone`/`Users`/匿名 SID，验证 owner 和 SYSTEM 可打开，断言子文件继承后仍只有 owner/System，并覆盖父目录 junction 零写入且 DACL 未打到外部目标。

```powershell
gofmt -w internal/platform/privatefs.go internal/platform/privatefs_unix.go internal/platform/privatefs_windows.go internal/platform/privatefs_unix_test.go internal/platform/privatefs_windows_test.go
go test ./internal/platform
```

Expected: 当前 Windows 测试 PASS。Unix 权限/no-follow 测试由 GitHub Actions 的 Ubuntu 或 macOS job 实际执行 `go test ./internal/platform`，六目标 `go build` 不编译 `*_test.go`，不能代替 Unix 测试。

- [ ] **Step 4: Commit（仅得到授权时）**

```powershell
git add internal/platform/privatefs*.go
git commit -s -m "feat: 增加日志私有文件边界"
```

---

### Task 3: 可取消的跨进程 advisory lock

**Files:**
- Create: `internal/platform/filelock.go`
- Create: `internal/platform/filelock_unix.go`
- Create: `internal/platform/filelock_windows.go`
- Create: `internal/platform/filelock_test.go`
- Create: `internal/platform/filelock_subprocess_test.go`

**Interfaces:**

```go
type LockMode uint8
const (
	LockShared LockMode = iota
	LockExclusive
)

type AdvisoryLock interface {
	Lock(context.Context, LockMode) error
	Unlock() error
	Close() error
}

func OpenAdvisoryLock(fs *PrivateFS, path string) (AdvisoryLock, error)
```

lock file 必须经 `fs.OpenAppend` 打开，因此 `OpenAdvisoryLock` 接收 `*PrivateFS`，不得自行 `os.OpenFile`。平台文件导出 `tryLock() (busy bool, err error)`：Unix 把 `EWOULDBLOCK`/`EAGAIN` 标 `busy=true`，Windows 把 `ERROR_LOCK_VIOLATION` 标 `busy=true`；其它 errno 原样返回。无 build tag 的通用等待器只看 `busy`，禁止 import `golang.org/x/sys/unix` 或 `windows`。重试 tick 为 5 ms 且通过包内 ops 注入。Unix lock 文件同样 `//go:build unix`。

- [ ] **Step 1: 写锁语义 Red**

测试不用固定 sleep：channel 同步证明 exclusive 互斥、shared 可共存、context cancel/deadline 返回 `context.Canceled`/`DeadlineExceeded`、Unlock 后可重新获取、Close/子进程退出释放锁。锁文件存在不等于锁被持有。

```powershell
go test -run '^TestAdvisoryLock_' ./internal/platform
```

Expected: FAIL，缺少锁类型。

- [ ] **Step 2: 实现两组平台调用**

- Unix: 文件顶部 `//go:build unix`；`unix.Flock(fd, LOCK_SH|LOCK_NB)` / `LOCK_EX|LOCK_NB` / `LOCK_UN`。lock fd 来自带 `O_CLOEXEC` 的 `OpenAppend`。
- Windows: `windows.LockFileEx` 配 `LOCKFILE_FAIL_IMMEDIATELY` 及可选 `LOCKFILE_EXCLUSIVE_LOCK`，固定锁 0..1 byte；`UnlockFileEx` 使用同一 `Overlapped`。handle 不可继承。
- lock file 通过 `PrivateFS.OpenAppend` 打开，不通过删除 lock file 表示释放。

```powershell
gofmt -w internal/platform/filelock.go internal/platform/filelock_unix.go internal/platform/filelock_windows.go internal/platform/filelock_test.go internal/platform/filelock_subprocess_test.go
go test -run '^TestAdvisoryLock_' ./internal/platform
go test -race -run '^TestAdvisoryLock_' ./internal/platform
```

Expected: PASS，race 无报告。

- [ ] **Step 3: Commit（仅得到授权时）**

```powershell
git add internal/platform/filelock*.go
git commit -s -m "feat: 增加跨进程日志锁"
```

---

### Task 4: Redactor 与 JSON handler

**Files:**
- Create: `internal/logging/redactor.go`
- Create: `internal/logging/redactor_test.go`
- Create: `internal/logging/handler.go`
- Create: `internal/logging/handler_test.go`

**Interfaces:**

```go
type Redactor struct { rules atomic.Pointer[redactionRules] }

func NewRedactor(exact ...string) *Redactor
func (r *Redactor) ReplaceExact(values []string)
func (r *Redactor) String(value string) string
func (r *Redactor) ReplaceAttr(groups []string, attr slog.Attr) slog.Attr

func NewJSONHandler(out io.Writer, level *slog.LevelVar, component string, redactor *Redactor) slog.Handler
```

- [ ] **Step 1: 写通用规则与 attr 递归 Red**

表驱动覆盖 exact control token/secret/web credential/subscription URL，sensitive key（`secret`、`token`、`authorization`、`password`、`credential`、`cookie`、`api-key`），`http/https/ws/wss` URL，Basic/Bearer/auth 形式，64 位 hex，group/`LogValuer`、error 字符串、非敏感数字和 bool。断言原值不出现，并按 taxonomy 替换：敏感 key 整值为 `***`，URL 为 `[REDACTED_URL]`，已注册 exact secret/credential/URL 为 `***`，不得统一成一个 `[REDACTED]`。过短通用字符串不得进入 exact 集合。另覆盖短 token 不误伤、非 64-hex credential 仍被 exact 替换。

```powershell
go test -run '^TestRedactor_' ./internal/logging
```

Expected: FAIL，包不存在或缺少符号。

- [ ] **Step 2: 实现不可变规则快照**

`ReplaceExact` 复制、去空和按长度降序排序，编译通用 regex 后一次 `Store`；读路径只 `Load`，不在记录时持锁。`ReplaceAttr` 递归 group，先按 key 整值替换，再处理 string/error/`LogValuer`。

- [ ] **Step 3: 写 handler 格式 Red 并实现**

用固定时间断言每行为单个 JSON object，`time` 为 RFC3339Nano 且含数值偏移，`level`、`msg`、`component` 存在，`component` 仅为 `daemon`/`tui`/`mihomo`（或更细 daemon 子系统名），JSON 中不出现 `source`。复合 attr 在 JSON 编码前已脱敏。

```powershell
gofmt -w internal/logging/redactor.go internal/logging/redactor_test.go internal/logging/handler.go internal/logging/handler_test.go
go test ./internal/logging
go test -race ./internal/logging
```

Expected: PASS。

- [ ] **Step 4: Commit（仅得到授权时）**

```powershell
git add internal/logging/redactor*.go internal/logging/handler*.go
git commit -s -m "feat: 增加日志结构化脱敏"
```

---

### Task 5: 多进程安全 rotator

**Files:**
- Create: `internal/logging/config.go`
- Create: `internal/logging/rotator.go`
- Create: `internal/logging/rotator_test.go`
- Create: `internal/logging/rotator_subprocess_test.go`

**Interfaces:**

```go
const (
	MaxCaptureLineBytes   = 16 << 10
	MaxExportRecordBytes  = 1 << 20
)

type Config struct {
	Level        slog.Level
	MaxSizeBytes int64
	MaxFiles     int
}

func DefaultConfig() Config   // info, 10 MiB, 3
func BootstrapConfig() Config // debug, 100 MiB, 10

type FailureClass string
const (
	FailureDropped FailureClass = "dropped"
	FailureWrite   FailureClass = "write"
	FailureRotate  FailureClass = "rotate"
	FailureCleanup FailureClass = "cleanup"
)

type FailureReporter interface {
	Report(class FailureClass, err error)
}

func NewFailureReporter(out io.Writer, redactor *Redactor, now func() time.Time) FailureReporter

type RotatorOptions struct {
	BasePath  string
	Config    Config
	PrivateFS *platform.PrivateFS
	OpenLock  func(*platform.PrivateFS, string) (platform.AdvisoryLock, error)
	WriteWait time.Duration
	Reporter  FailureReporter
}

type RotatingWriter struct { /* mutex + atomic config/counters */ }

func OpenRotatingWriter(context.Context, RotatorOptions) (*RotatingWriter, error)
func (w *RotatingWriter) Write([]byte) (int, error)
func (w *RotatingWriter) Apply(context.Context, Config)
func (w *RotatingWriter) Dropped() uint64
func (w *RotatingWriter) Close() error
```

- [ ] **Step 1: 写单进程轮转 Red**

测试矩阵：整条 record 超阈值前轮转；预先放置 `.1`–`.9` 且 `max-files=3` 时 Open **只删 `.3+`、不改写活跃内容**，一次 rotate 后只留 base/.1/.2；`max-files=1` 的 **rotate** 换新 inode、全部 archive 被删且已打开旧 handle 不被清空；Apply 缩到 `max-files=1` 只删 archive、**不 ReplaceEmpty 活跃文件**；固定匹配 basename 的普通 suffix 才可 rename/delete；symlink/目录/异常 suffix 不跟随。溢出安全矩阵覆盖 `currentSize`/`incoming`/`maxSize` 接近 `math.MaxInt64` 的组合，禁止使用 `currentSize + len(record)`。失败风暴测试断言 `FailureReporter` 只输出限频、经 redactor 净化且不含完整路径的稳定类别。

```powershell
go test -run '^TestRotatingWriter_' ./internal/logging
```

Expected: FAIL，缺少 rotator。

- [ ] **Step 2: 实现单进程核心和热 limits**

`Write` 要求输入为一条完整 JSONL record；先进程 mutex，再以 `context.WithTimeout(..., 250*time.Millisecond)` 获 exclusive lock，获锁后打开/`Stat` base。轮转判定固定为：

```go
incoming := int64(len(record))
needRotate := incoming > maxSize || (incoming <= maxSize && currentSize > maxSize-incoming)
```

禁止 `currentSize + incoming`。Open、Apply 缩容和每次 rotate 都在进程内 mutex 及同一 exclusive advisory lock 下先 `ReadDir` 再 `Remove` 掉 `suffix >= max-files` 的匹配普通文件，不能使用锁外目录项。**仅 rotate** 再执行经典移位（`max-files>1` 用 `Remove`+`Rename`；`max-files=1` 才 `ReplaceEmpty`）。枚举/改名/删除 **只** 走 `PrivateFS`，禁止 `os.ReadDir`/`os.Rename`/`os.Remove`。Open 用传入 context 可取消地等待锁；失败则关闭已取得资源并返回，不能无期限卡住启动。Open/Apply 不得移位或替换活跃 inode。rotate/`ReplaceEmpty` 前关闭本进程当前 write handle，再改名，再 `OpenAppend` 新 base。`Apply(ctx, cfg)` 有意无 error：先同步原子切换 config 值，再以 `min(ctx deadline, 250ms)` 等待 advisory lock；取得锁后的 `ReadDir`/`Remove` 同步执行、不受 context 抢占，文档不承诺整个维护在 250ms 内结束。`ctx` 取消或锁超时只停止尚未开始的维护，已切换的 level/limits 保留。失败分别计 `dropped`/`write`/`rotate`/`cleanup`，交给 `FailureReporter`，不把底层全文或完整路径交给 stderr。

- [ ] **Step 3: 写两子进程 Red 并实现陈旧 handle 防护**

helper subprocess 各写带 writer/sequence ID 的 2,000 条 JSONL，故意使轮转频繁。合并 base+archives 后每行可解码、不交叉、不重复，且没有 record 写到 rename 后的陈旧 inode。另让一个进程在 Open 收敛枚举点暂停、第二个进程 rotate，断言 Open 只有取得 exclusive lock 后才枚举/删除。注入永不成功锁，断言 Write 的 250ms deadline 路径返回整条丢弃、`Dropped()==1`，Open 的 context cancel 关闭已经取得的资源并及时返回。

```powershell
gofmt -w internal/logging/config.go internal/logging/rotator.go internal/logging/rotator_test.go internal/logging/rotator_subprocess_test.go
go test ./internal/logging
go test -race ./internal/logging
```

Expected: PASS，race 无报告。

- [ ] **Step 4: Commit（仅得到授权时）**

```powershell
git add internal/logging/config.go internal/logging/rotator*.go
git commit -s -m "feat: 实现多进程安全日志轮转"
```

---

### Task 6: mihomo 行捕获与 logging runtime

**Files:**
- Create: `internal/logging/capture.go`
- Create: `internal/logging/capture_test.go`
- Create: `internal/logging/runtime.go`
- Create: `internal/logging/runtime_test.go`

**Interfaces:**

```go
type LineCaptureWriter interface {
	io.WriteCloser
	Flush() error
}
func NewLineCaptureWriter(logger *slog.Logger, level slog.Level, stream string) LineCaptureWriter

type RuntimeOptions struct {
	BasePath  string
	Component string
	Config    Config
	PrivateFS *platform.PrivateFS
	OpenLock  func(*platform.PrivateFS, string) (platform.AdvisoryLock, error)
	Redactor  *Redactor
	Reporter  FailureReporter
}

type Runtime struct { /* logger + level + rotator */ }
func Open(context.Context, RuntimeOptions) (*Runtime, error)
func (r *Runtime) Logger() *slog.Logger
func (r *Runtime) Apply(context.Context, Config)
func (r *Runtime) Config() Config
func (r *Runtime) Close() error // 只关 rotator/lock/当前 write handle，不得 Close 共享的 PrivateFS
func (r *Runtime) EnterRecordMutex() func() // PR 3 同进程 snapshot：先拿该 mutex 再 shared flock

type Group struct { /* daemon + mihomo targets */ }
func NewGroup(dir string, config Config, targets ...*Runtime) *Group
func (g *Group) Apply(context.Context, Config)
func (g *Group) Config() Config
func (g *Group) Dir() string
```

`Runtime` 包内拆成不等待 IO 的 `swapConfig(Config)` 与 `convergeArchives(context.Context)`：前者同步替换 `LevelVar` 与 rotator limits，后者才获取 advisory lock 做 archive 收敛。`Runtime.Apply` 依次组合两者。`Group.Apply` 必须先对 daemon/mihomo 的**所有** target 调 `swapConfig`，再逐个调 `convergeArchives`；禁止 `for _, t := range targets { t.Apply(ctx, cfg) }`，否则第一个 target 的锁等待被取消时后续 target 会残留旧 config。`convergeArchives` 的 250ms/context 只约束锁获取。

- [ ] **Step 1: 写 LineCaptureWriter 边界 Red**

覆盖：一次多行、跨 Write 分割、CRLF、空行、非法 UTF-8 转 U+FFFD **并写 `invalid_utf8=true`**、16 KiB 内单条、超 16 KiB 截断并带 `truncated=true`、Flush 写出半行后仍可再 Write、Close 刷新未结尾行、Close 后 Write 返回稳定错误。stdout 映射 INFO，stderr 映射 WARN，attr 带 `component=mihomo`/`stream`，JSON 不含 `source`。注入永远失败的 logger 时，`Write` 仍返回 `len(p), nil`，不得把 slog/rotator 错误传出管道。

```powershell
go test -run '^TestLineCaptureWriter_' ./internal/logging
```

Expected: FAIL，缺少 writer。

- [ ] **Step 2: 最小实现 capture**

每次 `Write` 返回原输入 `len(p), nil`，即使日志 level 过滤或底层 JSONL 落盘失败也不误导子进程；失败只计入 logging 失败计数。只有 Close 后返回 `os.ErrClosed`。非法 UTF-8 替换为 U+FFFD 且 JSON 带 `invalid_utf8=true`。`Flush` 写出半行但不关闭。缓冲区永不超 `MaxCaptureLineBytes+utf8.UTFMax`。capture 必须可在多次子进程生命周期中继续 `Write`。测试：Close 前连续两次「写半行→Flush→再写」都成功；「写半行 → 子进程 Wait/Flush → 再 Start 写一行」得到两条独立 JSONL，不得粘成一行。

- [ ] **Step 3: 写 Runtime/Group Red 并实现生命周期**

断言 `Runtime.Open(ctx, ...)` 立即创建 base file且 ctx 取消能终止初始锁等待；`Apply(ctx, cfg)` 同步更新 `LevelVar` 与 rotator limits，即使传入已取消 context 也先完成内存切换，随后停止 archive 收敛。`Group.Apply` 必须先让 daemon/mihomo 的完整 config 全部 `swapConfig`，再逐 target `convergeArchives`；阻塞第一个 target 的 lock 并取消 context，第二个 target 也不得保留旧 config。另覆盖获锁后的假 `Remove` 忽略取消：维护会做完，不把已开始的本地文件系统调用伪装成 250ms 可中断。`Close` 幂等并不遗留 handle。JSON 固定 `component=daemon|tui|mihomo`。

```powershell
gofmt -w internal/logging/capture.go internal/logging/capture_test.go internal/logging/runtime.go internal/logging/runtime_test.go
go test ./internal/logging
go test -race ./internal/logging
```

Expected: PASS。

- [ ] **Step 4: Commit（仅得到授权时）**

```powershell
git add internal/logging/capture*.go internal/logging/runtime*.go
git commit -s -m "feat: 增加 mihomo 行捕获与日志 runtime"
```

---

### Task 7: daemon/mihomo 生产装配与关闭顺序

**Files:**
- Modify: `internal/app/runtime.go`
- Modify: `internal/app/runtime_test.go`
- Modify: `cmd/mihari/main.go`
- Modify: `cmd/mihari/main_test.go`
- Modify: `internal/cli/root.go`
- Create: `internal/cli/root_test.go`
- Modify: `internal/control/client/client.go`
- Modify: `internal/control/client/client_test.go`
- Modify: `internal/supervisor/command.go`
- Modify: `internal/supervisor/command_test.go`

**Interfaces:**

```go
type RuntimeBuildOptions struct {
	InitialSetupRequired bool
	SettingsPath         string
	ServiceStatus        func() (string, error)
	MihomoStdout         io.Writer
	MihomoStderr         io.Writer
	OnBackgroundError    func(component string, err error)
}

type daemonRunDeps struct {
	Paths         platform.Paths
	PrivateFS     *platform.PrivateFS
	Token         string
	Version       string
	Endpoint      string
	Ready         chan<- struct{}
	ServiceStatus func() (string, error)
}
func runDaemonWith(ctx context.Context, deps daemonRunDeps) error

func resolveCredentialPath(absolutePaths platform.Paths) (string, error)
func (c *client.Client) SetToken(token string)

type processLocalRoot struct {
	Paths platform.Paths
	FS    *platform.PrivateFS
	Token string
}
func prepareLocalRoot() (processLocalRoot, error)

// cli.Dependencies 新增；nil = 测试跳过数据根 IO
PrepareLocalRoot func() error

type daemonLoggingResources struct {
	StdoutCapture io.Closer
	StderrCapture io.Closer
	MihomoRuntime io.Closer
	DaemonRuntime io.Closer
	PrivateFS     io.Closer
}
func (r *daemonLoggingResources) Close() error
```

`BuildRuntime` 的旧 stdout/stderr 入参保留为兼容 wrapper。`MihomoStdout`/`MihomoStderr` 非 nil 时 **只** 把它们接到 `CommandStarter`；生产调用 positional 传 `io.Discard`，不得把 `os.Stdout`/`os.Stderr` 与 capture 并联（禁止 tee 到服务控制台）。`CommandStarter.Start` 返回的 Child 在 `Wait()` 返回后对 Stdout/Stderr 若实现 `Flush() error` 则 Flush，不得 Close capture。`OnBackgroundError` 传入 `runtime.Options`。

抽出可测的 `prepareLocalRoot()`（`package main`）。`cli.Dependencies` 新增 `PrepareLocalRoot func() error`：nil 时 PreRun 不做数据根 IO（现有 `cli.Execute` 测试保持隔离）。`main` 在 `cli.Execute` **之前**只构造 endpoint 与 **空 token** 的 `controlclient.New`，不得调用 `DefaultPaths().Absolute`、`NewPrivateFS` 或 `credential.LoadOrCreate`。生产注入：

```go
PrepareLocalRoot: func() error {
	root, err := prepareLocalRoot() // 进程内 Once；测试可重置；Close 只关已缓存结果，禁止为 Close 再入 Once
	if err != nil { return err }
	localClient.SetToken(root.Token)
	return nil
}
```

`PersistentPreRunE` 先调用该回调（若非 nil），再保留现有 `SetupError` 短路，禁止删掉 `TestSetupErrorUsesDataExitCode` 路径。禁止 `internal/cli` import `cmd/mihari` 或自己调 `NewPrivateFS`。Cobra `--help`、`help` 子命令、以及只打印 buildinfo 的 `self version` 必须跳过该回调。`main` 不得 `os.Exit(cli.Execute(...))` 直接丢掉 Close：必须先收退出码，只 Close **已经缓存** 的 `processLocalRoot.FS`（含 nil），再 `os.Exit`。**禁止**为 Close 再次进入 `prepareLocalRoot`/`Once`。`--help`/`self version` 跳过回调时 Once 未跑，缓存为 nil，收尾 Close 为零 IO。`cmd/mihari` 表驱动测试必须重置 Once（或每例 helper 子进程），禁止跨用例共享第一次的 Root/token/FS。`PrepareLocalRoot` 失败以返回值把 `APIError{CodeDataFailure}` 交给 PreRun，不要依赖 Execute 前拷贝的 `Dependencies.SetupError` 值字段。跳过回调的 help 路径仍保留现有 `SetupError` 短路。`SetToken` 对空串是 no-op；测试覆盖 PreRun 后 `status` 使用的 Bearer 与 daemon/TUI 相同。`prepareLocalRoot` 顺序：`absolutePaths, pathsErr := platform.DefaultPaths().Absolute()`，成功后立刻 `processFS, fsErr := platform.NewPrivateFS(absolutePaths.Root)`，随后只调用一次 `resolveCredentialPath(absolutePaths)`。该 helper 在 `MIHARI_CONTROL_CREDENTIAL` 非空时对其 `filepath.Abs/Clean`，为空时返回 `absolutePaths.ControlToken`；两种相对路径都以进程启动 working directory 为基准。`NewPrivateFS` 成功后才允许默认 in-root token 的 `LoadOrCreate`。失败时：凡最终 credential path 落在 `absolutePaths.Root` 下（默认或 `MIHARI_CONTROL_CREDENTIAL` 显式指回根内）都只 `Load`，禁止 `MkdirAll`/`LoadOrCreate`；`Load` 的缺文件、权限或损坏错误也不得写入 CLI `SetupError`，token 可为空。只有解析后位于 dataRoot **外** 的显式 credential 才允许在 `PrivateFS` 失败后 `LoadOrCreate`。`fsErr` 本身不得写入 `SetupError`。`SetupError` 仅用于 `Absolute`/`resolveCredentialPath` 失败，以及 `PrivateFS` **成功之后** 的默认 token `LoadOrCreate` 失败。token 只从最终 path 加载一次，local client、daemon/TUI closures 与 redactor 共用该 token，不再调用 `transport.DefaultCredentialPath()` 重算。路径或 credential 解析失败时不做任何 data-root/credential IO，把固定 `resolve Mihari data root` 或 `resolve Mihari control credential` 放入 `SetupError`。`runDaemonBody` 装配 `daemonRunDeps{Paths: absolutePaths, PrivateFS: processFS, Token: token, ServiceStatus: 现有 serviceManager.Status 闭包, ...}` 并调用 `runDaemonWith`，该闭包继续传给 `RuntimeBuildOptions.ServiceStatus`。`processFS` 由 `prepareLocalRoot` 创建：daemon/TUI 接管后由各自 cleanup Close；CLI/`SetupError` 提前返回时 main 在 `cli.Execute` 之后幂等 Close。Unix relaunch 前必须由 TUI cleanup 关闭，不能依赖 main 在 `Run` 返回之后的 defer。双重 Close 安全。测试分别注入相对 `MIHARI_DATA`、未设置和相对/绝对 `MIHARI_CONTROL_CREDENTIAL`，断言默认 credential/Settings/logger 共用绝对 Root，而显式 credential 保留其独立绝对位置但 daemon/TUI 仍使用同一 token；另注入 Abs 失败 seam，断言没有 token/目录 IO。再覆盖：`mihari --help`、`mihari help`、`mihari daemon --help`、`mihari self version` 经 `cli.Execute` 时 `NewPrivateFS`/`MkdirAll`/`LoadOrCreate` spy 为零；`mihari status` 经 PreRun 后 client token 非空（注入已有 token 文件）；`NewPrivateFS` 失败后，默认 in-root 与显式 in-root credential 的 `MkdirAll`/`LoadOrCreate`/`EnsureDirs` spy 为零；缺 token 文件时 `SetupError` 仍为 nil。必须经 `cli.Execute`（Interactive、无子命令）断言会调用 `RunTUI` 且 `PrivateFS=nil`。daemon `runDaemonWith` 在 nil fs 时返回稳定 data failure 且零 Settings IO。`runDaemonWith` 测试继续使用 `t.TempDir()` Paths、先 `NewPrivateFS` 再传入 deps，以及 `transporttest.Endpoint(t)`，禁止打到真实用户数据根或默认 named pipe。

- [ ] **Step 1: 写装配 Red**

测试注入 bytes buffer/capture spy，证明 `CommandStarter.Stdout/Stderr` 只来自 `MihomoStdout/MihomoStderr`，positional stdout/stderr 即使非 Discard 也不进入 Starter。调用 Manager 背景错误路径后 daemon logger 收到脱敏 component/error。用五个 `io.Closer` spy 单测 `daemonLoggingResources.Close` 的 capture stdout→capture stderr→mihomo runtime→daemon runtime→PrivateFS 顺序、`errors.Join` 与全部继续执行，不要求替换具体 `NewPrivateFS`/`logging.Open` 构造器。`TestRunDaemon_LoggingOpensBeforeBuildRuntime` 与 `TestRunDaemon_CatalogLoadFailureStillOpensLogger` 都必须：`Endpoint: transporttest.Endpoint(t)`、`Ready` channel、在 goroutine 里调 `runDaemonWith`、看到 Ready 或 JSONL 后 cancel、`t.Cleanup` 等待退出。不得同步调用卡在 `Serve`，不得使用空 Endpoint（Windows 会撞默认 `\\.\pipe\mihari-control`）。前者：注入 temp Paths，Open 成功后故意让 BuildRuntime 失败，degraded daemon 的 JSONL 仍存在且含 token/secret 的记录已被脱敏。后者：损坏 catalog 时仍 Open，不在 Open 前 return。

```powershell
go test -run '^TestBuildRuntime.*(Capture|Background)$' ./internal/app
go test -run '^TestRunDaemon_(LoggingOpensBeforeBuildRuntime|CatalogLoadFailureStillOpensLogger)$' ./cmd/mihari
```

Expected: FAIL，新 options 或 `runDaemonWith` 尚未接入。

- [ ] **Step 2: 在 `runDaemonWith` 打开 logger**

顺序必须为：`deps.PrivateFS`（进程入口已创建；nil 则立即 data failure，零 `EnsureDirs`/Settings IO）→ `EnsureDirs` → `LoadOrCreateWithSidecar` → `EnsureDir(LogDir)` → 收集 exact secrets 并 `ReplaceExact` → 用 daemon context 打开 daemon/mihomo Runtime →创建 stdout/stderr capture → `BuildRuntimeWithOptions(..., io.Discard, io.Discard, options{MihomoStdout, MihomoStderr, ...})` → `daemon.Run`。`runDaemonWith` 不得再次调用 `NewPrivateFS`。本 PR Settings 尚无 `log` 块，因此打开时使用 `DefaultConfig()`；PR 2 必须在同一打开点改用 `settings.EffectiveLogging()` 并把启动加载迁移到 outcome API，本任务不得把自定义 YAML 永久绑死成 info/10/3。

`ReplaceExact` 必须发生在 `Open` 之前。token 与 `settings.ControllerSecret` 已有则注册。`panel.LoadOrCreateCredential` 与 catalog URL（`subscription.LoadOrCreate` / 一次性 `Open` 后只读 Snapshot，不保留第二份长期 Service）成功才加入 exact 集合；**任一步失败不得在 Open 前 `return`**，跳过那些值，仍 EnsureDir+Open，再让 BuildRuntime 失败走 degraded。cleanup `defer` 必须包住 degraded 与正常两条 `daemon.Run` 路径。通用 URL/64-hex regex 不能替代 exact 值。`FailureReporter` 接到限频 stderr。`logging.Open` 对 `filepath.Dir(BasePath)` 再调一次 `EnsureDir`。

关闭顺序由 `daemonLoggingResources.Close` 和外层 `sync.Once` 固化，且 **只在 `daemon.Run` 返回后** 执行：supervisor 已等子进程；先 `Close` stdout/stderr capture 刷新残行，再关 mihomo Runtime，再关 daemon Runtime，最后 `PrivateFS.Close`。capture 在 mihomo 重启之间保持打开，子进程退出不得 Close；任何 Runtime/lock 仍可能使用 fs 时不得提前关 fs。错误用 `errors.Join` 合并，不提前跳过后续 close。daemon 或 mihomo 任一侧 `logging.Open` 失败都是启动失败，必须 `return` 该错误，不得带着缺失的 `mihomo.log`/`mihari-daemon.log` 继续 `daemon.Run`。已打开的一侧与 fs 仍走同一 cleanup。正常、degraded、BuildRuntime 失败都走同一 cleanup；组合类型允许直接注入 `io.Closer` spy，断言 fs 最后且只关一次。

- [ ] **Step 3: 验证正常/降级生命周期**

```powershell
gofmt -w internal/app/runtime.go internal/app/runtime_test.go cmd/mihari/main.go cmd/mihari/main_test.go internal/supervisor/command.go internal/supervisor/command_test.go
go test ./internal/app ./internal/supervisor ./internal/cli ./internal/control/client ./cmd/mihari
go test -race ./internal/app ./internal/supervisor ./internal/cli ./internal/control/client ./cmd/mihari
```

Expected: PASS，测试 temp dir 中出现 daemon/mihomo JSONL，不含注入 secret。`TestCommandStarter_FlushesCaptureOnWait`：半行 stdout 在 Wait 后成为一条 JSONL，随后再 Start 写入的一行是另一条。

- [ ] **Step 4: Commit（仅得到授权时）**

```powershell
git add internal/app/runtime.go internal/app/runtime_test.go cmd/mihari/main.go cmd/mihari/main_test.go internal/cli/root.go internal/cli/root_test.go internal/control/client/client.go internal/control/client/client_test.go internal/supervisor/command.go internal/supervisor/command_test.go
git commit -s -m "feat: 接入 daemon 与 mihomo 文件日志"
```

---

### Task 8: TUI bootstrap 日志与失败降级

**Files:**
- Modify: `internal/tui/run.go`
- Modify: `internal/tui/run_test.go`
- Modify: `internal/tui/model.go`
- Modify: `cmd/mihari/main.go`
- Modify: `cmd/mihari/main_test.go`

**Interfaces:**

```go
type LocalLoggingHealth interface{ Available() bool }

type LoggingResources struct {
	Runtime   *logging.Runtime
	Redactor  *logging.Redactor
	PrivateFS *platform.PrivateFS
	Health    LocalLoggingHealth
}
func (r *LoggingResources) Close() error

type LoggingFactory func(context.Context) (LoggingResources, error)

type Options struct {
	// existing fields remain
	OpenLogging LoggingFactory // nil = 不打开 TUI 文件日志，health.Available()==false
	ErrorOutput io.Writer
}

func finishRun(final tea.Model, runErr error, warningWriter io.Writer, relaunch func() error, cleanup func(tea.Model) error) error
```

抽出 `openTUILogging(ctx context.Context, paths platform.Paths, token string, fs *platform.PrivateFS) (tui.LoggingResources, error)`。`paths.Root` 必须已经绝对化；`fs` 必须是进程入口已经创建的那一份，本函数不得再调 `NewPrivateFS`。顺序固定：复用 `fs` → `EnsureDir(LogDir)` → `NewRedactor(token)` → 再用 ctx 尝试 `Open`（`BootstrapConfig()`）。`fs == nil` 时保存该错误，仍可用 token 构造 redactor，返回 `PrivateFS=nil` 且不再尝试 EnsureDir/Open。`EnsureDir` 失败时保存该错误但仍构造 redactor，不再尝试 Runtime.Open，并返回包含 fs/redactor 的 partial resources。`cmd/mihari` 的 TUI closure 复用进程入口已经解析的 absolute Paths、最终 credential token 与 `processFS`；测试先对 temp Root 调 `NewPrivateFS` 再注入，或显式传 nil fs。返回值即使伴随 error，也必须保留已经成功创建的 redactor/`PrivateFS`，让 `Run` 接管并最终关闭。Open/EnsureDir/`PrivateFS=nil` 时 TUI 仍运行，`health.Available()==false`，stderr 降级；redactor 与非 nil `PrivateFS` 仍交给 PR 3 Export。`LoggingResources.Close` 幂等，先 Close Runtime，再 Close `PrivateFS`（nil 跳过），使用 `errors.Join`；PR 2/3 会在它之前插入 applier/export wait。本 PR 把 health 从 `Run` 注入 root Model，UI 文案在 PR 2 落地。

- [ ] **Step 1: 写 TUI 启动/失败 Red**

`internal/tui` 测试断言 factory 以 Run context 在 session 之前调用；正常退出、Bubble Tea 返错、relaunch 路径都只 Close 一次，顺序为 session → Runtime → `PrivateFS`，且全部 **发生在 relaunch 回调之前**（对标 `TestFinishRunCleansUpSessionBeforeRelaunch`）。`finishRun` 把 final model 传给 cleanup，PR 1 暂不使用该值；PR 3 改为 `Run` 持有稳定 ExportLogsModel 指针并直接 `CancelAndWait`，不得从 final model 类型断言回找 runner。factory 返回部分 resources+error 时 TUI 仍运行并最终关闭非 nil fs；传入 nil fs 时 TUI 仍运行、不创建数据根、`health.Available()==false`；`ErrorOutput` 只出现稳定净化文案且限频；bootstrap exact 为 debug/100 MiB/10。`cmd/mihari` 的 `TestOpenTUILogging_UsesInjectedPaths` 用 absolute temp Paths 和调用方创建的 `PrivateFS`，并增加 relative Paths 被入口 `Absolute`+`NewPrivateFS` 后成功、credential override 位于根外且根尚不存在仍由进程入口创建 TUI 日志、ctx 取消初始锁等待、入口 `NewPrivateFS` 失败后默认 token 零创建的测试，不得读真实用户目录。

```powershell
go test -run '^TestRun.*Logging$' ./internal/tui
go test -run '^Test(OpenTUILogging|ResolveCredentialPath|PrepareLocalRoot|HelpCreatesNoDataRoot)_' ./cmd/mihari
```

Expected: FAIL，Options 或 `openTUILogging` 尚未接入。

- [ ] **Step 2: 实现装配与幂等 cleanup**

`Run` 首先尝试 `OpenLogging` 并无论 err 是否为空都接管返回的 `LoggingResources`，成功后记录 TUI start。现有 `Run` 在 `options.Client != nil` 时执行 `model = newModelWithClientContext(...)` **整棵替换** Model。health、OpenLogging 注入、以及后续 PR 的 applier/`exportLogs` 必须在这次替换之后写入最终 model，仿照现有 `SetServiceController`；禁止写进第一份 `NewModel()`。session Close 与 resources Close 使用同一个 `sync.Once` cleanup，并以 `func(final tea.Model) error` 传给 `finishRun`；本 PR 顺序为 session → Runtime → `PrivateFS`，并在调用 `Relaunch` **之前**完成。cleanup error 经 redactor 后写 warning，仍继续后续 close；`finishRun` 不得因首个 close error 跳过 Relaunch 前其余清理。禁止只靠 `Run` 返回之后的 `defer Close`：Unix `syscall.Exec` 成功不会跑到那之后。初始化失败时使用只记首次+固定时窗的 stderr reporter，优先使用返回的 Redactor.String；无 redactor 时只输出固定文案，不输出完整路径或底层错误。

- [ ] **Step 3: 验证 TUI 包**

```powershell
gofmt -w internal/tui/run.go internal/tui/run_test.go internal/tui/model.go cmd/mihari/main.go cmd/mihari/main_test.go
go test ./internal/tui ./cmd/mihari
go test -race ./internal/tui ./cmd/mihari
```

Expected: PASS。

- [ ] **Step 4: Commit（仅得到授权时）**

```powershell
git add internal/tui/run.go internal/tui/run_test.go internal/tui/model.go cmd/mihari/main.go cmd/mihari/main_test.go
git commit -s -m "feat: 接入 TUI bootstrap 文件日志"
```

---

### Task 9: 文档与 PR 1 总验收

**Files:**
- Modify: `README.md`
- Modify: `docs/commands.md`
- Add: `docs/superpowers/specs/2026-09-02-file-logging-export-design.md`
- Add: `docs/superpowers/plans/2026-09-02-file-logging-foundation.md`

- [ ] **Step 1: 更新用户文档**

说明三个 JSONL 路径、默认 info/10 MiB/3、mihomo stdout=INFO/stderr=WARN、多 TUI 共享文件、脱敏是 best effort 且日志仍应按敏感资料处理。本 PR 不宣称配置 UI 或导出已可用。

- [ ] **Step 2: 包级、race、vet 与全仓验证**

```powershell
gofmt -l .
go test ./internal/platform ./internal/logging ./internal/supervisor ./internal/app ./internal/tui ./cmd/mihari
go test -race ./internal/platform ./internal/logging ./internal/supervisor ./internal/app ./internal/tui ./cmd/mihari
go test ./...
go vet ./...
git diff --check
```

Expected: `gofmt -l .` 无输出，全部 PASS，`git diff --check` 无错误。

- [ ] **Step 3: 六目标 CGO-free 编译**

```powershell
$env:CGO_ENABLED='0'
$targets=@(@('windows','amd64','.exe'),@('windows','arm64','.exe'),@('linux','amd64',''),@('linux','arm64',''),@('darwin','amd64',''),@('darwin','arm64',''))
foreach($t in $targets){$env:GOOS=$t[0];$env:GOARCH=$t[1];go build -o (Join-Path $env:TEMP ("mihari-{0}-{1}{2}" -f $t[0],$t[1],$t[2])) ./cmd/mihari;if($LASTEXITCODE -ne 0){throw "build failed: $($t[0])/$($t[1])"}}
```

Expected: 六目标编译成功。

- [ ] **Step 4: 审查准确变更范围**

```powershell
git status --short
git diff --stat
git diff --name-only
```

不得出现 `CHANGELOG.md`、`internal/control/protocol/**`、`internal/config/settings.go`。除 spec 和本 plan 外，只应有本文 File Structure 所列生产/测试/文档（含 `internal/cli/root.go`、`internal/control/client/client.go`、`README.zh-CN.md`）。Windows 本地 `go test` 覆盖 Windows 文件；Unix no-follow/mode/lock 测试必须由 CI 的 Ubuntu 或 macOS job 实际执行，不能只靠六目标 `go build`。

- [ ] **Step 5: Commit 文档（仅得到授权时）**

```powershell
git add README.md README.zh-CN.md docs/commands.md docs/superpowers/specs/2026-09-02-file-logging-export-design.md docs/superpowers/plans/2026-09-02-file-logging-foundation.md
git commit -s -m "docs: 记录文件日志行为"
```

---

## Self-Review

| Spec 要求 | 任务 |
| --- | --- |
| TUI 日志狭义架构例外 | Task 1 |
| 五个日志/导出路径、相对 Root 一次性绝对化；保留并绝对化 credential override；`EnsureDir(LogDir)` 才是加固，`EnsureDirs` 不是 | Task 1–2、7–8 |
| 缺失 dataRoot 安全创建；Unix 0700/0600 与 owner；Windows protected DACL、逐段 no-follow、`FILE_SHARE_DELETE`；opaque `FileIdentity`、可关闭 `DirectoryIdentity`、`OpenReadChecked`；幂等 `PrivateFS.Close`/`os.ErrClosed` | Task 2、7–8 |
| `flock` / `LockFileEx`、context 取消、进程退出释放 | Task 3 |
| exact secrets + URL/auth/key/64-hex 脱敏 taxonomy，JSONHandler 时间格式与 `component` | Task 4 |
| 整 record 临界区、溢出安全 size 判断、250ms、陈旧 handle、Open/Apply/rotate 收敛均持 exclusive lock、Open context 可取消、仅 rotate 移位/`max-files=1` 新 inode | Task 5 |
| stdout INFO/stderr WARN、chunk/UTF-8/16 KiB/Close 语义 | Task 6 |
| Runtime/Group 无失败 `Apply(ctx, Config)` 先对全部 target `swapConfig`，再逐个做锁获取受 250ms/context 约束的 `convergeArchives` | Task 5–6 |
| 选定命令的 `prepareLocalRoot` 先 `NewPrivateFS` 再默认 token/`EnsureDirs`；`--help`/`--version` 零数据根 IO；Settings 后 `EnsureDir(LogDir)`、Open 前 ReplaceExact（catalog/credential 失败仍 Open）、`FailureReporter`、degraded 可记录；nil fs 时 daemon 零 Settings IO | Task 7 |
| capture 独占 CommandStarter、跨 mihomo 重启存活，仅 daemon 关闭路径 Close | Task 6–7 |
| `runDaemonWith` 可注入绝对 Paths 与已创建的 `PrivateFS`；`resolveCredentialPath` 保留 override；positional stdout 为 Discard；可注入 closer composite 固化 capture→runtime→PrivateFS 关闭 | Task 7 |
| TUI debug/100 MiB/10；缺失 root 由进程入口创建；`openTUILogging(ctx, paths, token, fs)` 复用入口 fs 并返回可接管的完整 resources；nil fs 时 TUI 仍运行；session→logger→PrivateFS Close 在 Relaunch 之前；`O_CLOEXEC`/`InheritHandle=false` | Task 2、8 |
| 文档、race/vet/全仓/六目标 CGO-free、Windows+Unix CI 实测 | Task 9 |

**Placeholder scan:** 计划不含占位任务或未定接口；`Paths.Absolute`、`prepareLocalRoot`/`processLocalRoot`、`Client.SetToken`、`resolveCredentialPath`、`RuntimeOptions`、`RuntimeBuildOptions`、`daemonRunDeps`（含 `PrivateFS`）/`runDaemonWith`、`daemonLoggingResources`、`LoggingResources`/`openTUILogging(ctx, paths, token, fs)`、`PrivateFS`（含 `FileIdentity`/`DirectoryIdentity`/`ReadDir`/`OpenDirIdentity`/`OpenReadChecked`/`Rename`/`Remove`/`Close`）、包内 `swapConfig`/`convergeArchives`、`tryLock() (busy bool, err error)`、`OpenAdvisoryLock(*PrivateFS, path)`、`FailureReporter`、`LineCaptureWriter.Flush`、`Config`、`Runtime`、`Group` 的输入输出均已固定。

**Type consistency:** 大小在 logging runtime 使用 `int64` bytes，份数在内部使用 `int`；PR 2 的稳定 DTO 使用 `int64` 后只在经过 `1..10` 校验后转换。写入路径通过 `io.Writer`，捕获关闭路径通过 `io.WriteCloser`。
