# 文件日志、轮转与 zip 导出设计

日期：2026-09-02

状态：已批准，准备实施

PR 3 工作分支：`feat/log-export-ui`

PR 3 合并目标分支：`dev`

PR 3 工作目录：`.worktrees/feat-log-export-ui`

## 1. 背景

Mihari 今天的「Logs」只是 mihomo controller 的实时 WebSocket 流：

- TUI Logs 页把事件放进最多 1 万条的内存环形缓冲，关掉 TUI 即丢失。
- CLI `mihari logs` 不带 `--follow` 时只打印一条。
- 数据根预留了 `logs/mihari.log`，`EnsureDirs()` 会创建目录，但没有任何代码写入该文件。
- Mihari daemon / TUI 没有 slog，也没有落盘。质量审计曾拒绝「引入 slog 全局 logger」，那是当时范围外的选择，不是架构禁令。
- 作为 Windows 服务运行时，mihomo stdout/stderr 绑在服务进程标准输出上，用户通常找不到。
- 没有导出、没有轮转、没有 System 页配置。用户反馈问题后无法交出可复盘的历史日志。

排障需要的是：daemon、TUI、mihomo 三类日志随时能写到文件；文件按大小轮转；用户能在 TUI 里改级别、大小与保留份数，并按时间范围打 zip 发出来。

## 2. 目标

- daemon、TUI、mihomo 分别写入数据根下三个 JSONL 日志文件，进程一启动就能写；daemon 未运行时 TUI 仍能写自己的文件。
- 多个 TUI 实例可同时写 `mihari-tui.log`，不得因跨进程 write/rotate 竞争造成丢行、交叉覆盖或错误删除。
- 三个文件共用一套配置：级别、单文件大小、保留份数。配置在 `mihari.yaml`，只经 daemon 控制面修改；daemon 与已连接的 TUI 热更新。
- System 页新增 Logging 区；Logs 页与 Logging 区都能打开同一套导出对话框。
- 导出生成 zip（唯一格式），默认目录 `{dataRoot}/logs-export/`，成功后显示绝对路径并一键复制。
- 导出在日志继续写入、轮转或多个 TUI 并存时仍读取一个有界、无重复的快照；取消或失败不得留下目标 zip。
- 导出按用户本地时区选择时间范围，对话框展示当前时间与时区偏移，避免 UTC/本地混淆。
- Mihari 自己持有的 controller secret、Web credential、control token、订阅 URL 与认证字段不得进入日志或导出；权限在 Unix 和 Windows 上都必须限制到数据所有者及必要的服务账户。
- 保持 `CGO_ENABLED=0`，不新增第三方依赖，不改 `CHANGELOG.md`。

## 3. 非目标

- 不改 TUI Logs 页的数据源：它仍是 mihomo 实时流（过滤、暂停、环形缓冲保持原样）。本设计只在该页加 Export，不把 daemon/TUI 文件日志接进该页。
- 不提供 CLI `mihari logs export` / `mihari logging`（控制面先落地，CLI 可后续加）。
- 不支持 7z、tar.gz、gzip；导出只有 zip。
- 不弹系统文件管理器，不「打开所在文件夹」。
- 不把 Mihari 的 log level 同步成 mihomo 配置里的 `log-level`；mihomo 仍使用生成配置中的 `info`。mihomo 文件只记录它实际写到 stdout/stderr 的内容。
- 不把 status、settings、订阅 catalog、token 打进 zip。
- 不压缩已轮转的 `.log.N`（压缩只发生在导出 zip）。
- 不引入 lumberjack 或其他日志库。
- 不把 slog 做成全局 `slog.SetDefault` 强制所有包使用；logger 由装配点注入或作为包级显式依赖。
- 不为一次性 CLI 子命令写文件日志。
- 不在本功能中修改 coordinator 的全局 revision 溢出行为；该问题由 GitHub Issue #191 独立跟踪。

## 4. 方案比较

### 4.1 采用：分文件落盘 + TUI 本地 zip 导出

三个来源各写各的逻辑文件。配置由 daemon 写入 `mihari.yaml`。导出由 TUI 直接读取 `logs/` 的受控快照、过滤并写 zip。这满足「任何时候都能写、daemon 挂了也能把文件给你」。

对「daemon 单写入者」的明确例外：

- daemon 追加 `mihari-daemon.log` 和 `mihomo.log`；一个或多个 TUI 只追加共享的 `mihari-tui.log`。
- 所有进程只通过 `internal/logging` 的受锁 writer 写日志，不得自行 open/rename/delete 日志文件。
- TUI 不写 `mihari.yaml`、订阅、token、面板或 daemon settings；日志配置变更仍只走 daemon 控制面。
- TUI 读取日志只用于导出，不把文件内容当成第二份控制面真相。
- 该例外必须与第一批 TUI 文件写入代码在同一 PR 落入根 `AGENTS.md` 和 `docs/architecture.md`。

### 4.2 不采用：单一 `mihari.log` 由 daemon 代写

TUI 在连上 daemon 之前无法落盘，恰好丢掉启动和连接失败——这是最需要的日志。

### 4.3 不采用：导出走控制 API

这更贴近单写入者，但 daemon 不可用时导出失败。第一期不采用。控制面仍只负责读写日志配置。

### 4.4 不采用：每个 TUI 写 PID 专属文件

PID/会话专属文件可以绕开写竞争，但会让跨会话保留数量无限增长，并让导出和清理语义依赖进程存活判断。采用共享逻辑文件和跨进程锁后，三个固定来源及统一保留上限都能维持。

### 4.5 不采用：slog 文本 / logfmt

mihomo stdout 是任意文本（引号、等号、空格）。JSONL 把净化后的原行放进 `msg`，转义由 `encoding/json` 负责。导出按 `time` 过滤只需解析 JSON。

## 5. 详细设计

### 5.1 包、平台边界与装配

新增 `internal/logging`：

- 构造 slog handler（JSONL、`slog.LevelVar`、轮转 writer）；
- 同进程与跨进程串行写入、轮转、热更新与失败计数；
- mihomo stdout/stderr 的按行捕获；
- 结构化脱敏；
- 导出快照、时间过滤、zip 与 manifest。

平台差异不散落在 `internal/logging`。以下小能力放在 `internal/platform` 的 `_windows.go` / `_unix.go` / `_linux.go` / `_darwin.go` 实现中，并通过最小接口注入测试：

- 日志文件的跨进程共享锁；
- opaque file identity、checked-open 与 Windows 可删除共享方式的只读快照句柄；
- Unix mode 与 Windows DACL 的安全创建/加固；
- 持有 source/target 目录 identity、私有 workspace，以及 workspace→parent 不覆盖既有目标的文件发布。

装配：

| 进程 | 入口 | 文件 | `component` |
|---|---|---|---|
| daemon | `cmd/mihari` 的 `runDaemonBody`，Settings 与目录准备完成后、`BuildRuntime` 之前打开 | `logs/mihari-daemon.log` | `daemon` 及更细的 slog attr（`supervisor` / `scheduler` / `web-gateway` 等） |
| TUI | `tui.Run` 开始时以保守 bootstrap 配置打开 | `logs/mihari-tui.log` | `tui` |
| mihomo | daemon 里 `supervisor.CommandStarter` 的 Stdout/Stderr 接到两个 line capture writer | `logs/mihomo.log` | `mihomo`，另有 `stream=stdout\|stderr` |

`OnBackgroundError` 在生产装配中接到 daemon logger，不再丢弃。一次性 CLI 仍只把错误写到 stderr。

资源所有权：

- daemon/TUI 各自拥有并在退出时关闭自己的 slog handler 与 rotator；装配点同时拥有一个持有 dataRoot 目录 handle 的 `PrivateFS`，不得把它交给 logger 或 Export 隐式关闭；
- supervisor 在每次 `Start()` 复用同一对 capture writer；capture **跨 mihomo 重启存活**，只在 daemon 关闭路径 `Close`。`CommandStarter` 包一层 Child：`Wait()` 返回后对 Stdout/Stderr 若实现 `Flush() error` 则 Flush，**不得 Close**（否则下次 `Write` 得到 `os.ErrClosed`，mihomo 日志中断）。这样上一代半行不会和下一次 `Start` 的首 chunk 粘成一行；
- capture writer 的 `Close`/`Flush` 只处理自己的半行，不关闭共享 mihomo logger；
- daemon 先停止 supervisor，再 `Close` capture，再关闭 mihomo/daemon logger，最后关闭不再有使用者的 `PrivateFS`；
- TUI 先停止 session 事件生产，再 cancel+wait 导出 runner 与本地 Logging applier，随后关闭 logger/lock，最后关闭 `PrivateFS`；全部使用同一条 once-cleanup，在 `finishRun` 里、调用 `Relaunch` **之前**完成。`tui.Run` 必须持有并向 root/cleanup 注入同一份全生命周期 ExportLogsModel，不从 final model 类型断言回找 runner；每次打开只重置表单，generation 单调不复用。导出 runner 必须在 `Update` 返回前同步登记并启动自己拥有的 goroutine；返回给 Bubble Tea 的 `tea.Cmd` 只等待一个容量为 1 的结果 channel，不能承担启动 Export 的职责。runner 在写入/关闭 result channel 后才关闭本次 `done`，且此后不再访问所拥有资源。这样即使 Bubble Tea 在 `Update` 返回后、Cmd 入队前取消，cleanup 仍有一个必定结束并关闭 `done` 的实际 runner 可等待。Unix 自更新是 `syscall.Exec`，成功则不会跑 `finishRun` 之后的 `defer`；
- Unix 打开日志/lock/temp 一律 `O_CLOEXEC`；Windows `InheritHandle=false`，避免 Exec/`CreateProcess` 继承未关的 fd 导致 flock 自锁；
- `PrivateFS.Close`、派生 `DirectoryIdentity`、logger/lock/publish-directory/workspace Close 与 TUI runner/applier cleanup 都必须幂等；Close 后再调用文件能力返回 `os.ErrClosed`。所有锁、文件/目录句柄、workspace 和临时导出文件都有 `Close`/cleanup 路径，不启动无所有者后台 goroutine。

### 5.2 路径与权限

`platform.Paths` 现有 `Log` 改为三个明确路径，并增加导出目录：

| 字段 | 路径 |
|---|---|
| `LogDir` | `{root}/logs` |
| `DaemonLog` | `{root}/logs/mihari-daemon.log` |
| `TUILog` | `{root}/logs/mihari-tui.log` |
| `MihomoLog` | `{root}/logs/mihomo.log` |
| `LogExportDir` | `{root}/logs-export` |

删除不再使用的单一 `Log` 字段（测试与 `EnsureDirs` 一起改）。目录名用连字符 `logs-export`，与现有 `core-channel`、`mihari-channel` 一致，不用下划线。

需要本地数据根的选定命令在第一次读取 Settings/token 或创建目录前，把 `platform.DefaultPaths()` 通过一个可返回错误的 `Paths.Absolute()` 解析一次，并立即对该绝对 Root 调 `NewPrivateFS` 建立进程级 root capability；只有成功后才允许 `Paths.EnsureDirs`、默认 in-root credential 的 `LoadOrCreate` 或 Settings IO。该步骤发生在 Cobra `PersistentPreRun`，不得发生在 `main` 解析 argv 之前。相对 `MIHARI_DATA` 以进程启动时的 working directory 为基准；解析后重新从绝对 `Root` 构造所有派生字段，daemon/TUI logger、Settings、状态 DTO 与默认导出都只使用这一份绝对 `Paths`。control credential 保留现有 `MIHARI_CONTROL_CREDENTIAL` 契约：环境变量非空时也以同一启动 working directory 为基准只做一次 `filepath.Abs/Clean`，为空时使用 `absolutePaths.ControlToken`；local client、daemon、TUI 和 redactor 必须共用这个最终 credential path 加载出的同一 token，不得再由各 closure 重算。不得只把对外展示的 `dir` 转绝对而让 writer 继续使用相对路径。服务安装已有的绝对数据根规则不变。root/LocalSystem 遇到缺失 Root 时必须在任何 `MkdirAll`/token 写入前 fail closed；不能先创建 root-owned 根，再把它当作“既有根”接受。`NewPrivateFS` 失败不得写入 CLI `SetupError` 去拦截 TUI：TUI 继续运行并接管 `PrivateFS=nil`。凡最终 credential path 落在 dataRoot 内（默认或显式指回根内）都只 `Load` 已有文件，不得 `MkdirAll`/`LoadOrCreate`；该 `Load` 失败（含缺文件）也不得变成 `SetupError`。只有解析到 dataRoot **外** 的显式 `MIHARI_CONTROL_CREDENTIAL` 才允许在 `PrivateFS` 失败后 `LoadOrCreate`。daemon 在 `runDaemonWith` 见到 nil `PrivateFS` 时以 data failure 退出，且不得补跑 `EnsureDirs` 或 Settings IO。Cobra `--help`/`--version` 必须零 `Absolute`/`NewPrivateFS`/`MkdirAll`/`LoadOrCreate`。

`LogDir` 由 `PrivateFS.EnsureDir` 创建并加固：`NewPrivateFS` 只接受已经绝对化的 dataRoot；dataRoot 不存在时，交互用户进程以 Unix 0700/Windows 当前用户+LocalSystem protected DACL 安全创建；root/LocalSystem 直接 fail closed，服务安装必须预先创建固定 dataRoot 并赋予桌面 owner，不创建仅 root/System 可用或宽权限的替代根。已存在时允许跟随用户配置的最后一跳（支持 `~/.mihari` 指向其他盘的 symlink/junction），随后 `fstat` 确认为目录、收紧权限并持有该 identity。这样即使 control credential 显式放在 dataRoot 外，首次离线启动 TUI 也能建立本地日志能力。其后 `logs/` 及文件的每一跳 no-follow，拒绝中间及最终的符号链接 / reparse / junction。轮转所需的枚举、改名、删除、只读打开必须走同一套已验证目录 fd + basename API（`ReadDir`/`Rename`/`Remove`/`OpenReadChecked`），禁止 `os.Rename`/`os.Remove`/`os.ReadDir` 完整路径。`PrivateFS` 是必须显式关闭的进程级 capability；它从 `prepareLocalRoot` 转交给实际运行的 daemon 或 TUI cleanup，不随任一子 logger 关闭。`Paths.EnsureDirs` 只能在该 root capability 建立后预创建 `logs/`，且不算安全边界，不能代替 `EnsureDir`。daemon 与 TUI 在打开 logger 之前都必须调用 `EnsureDir(LogDir)`；`logging.Open` 对 `BasePath` 父目录再确保一次。`LogExportDir` 在首次默认导出时由 `PrivateFS.EnsureDir` 创建。轮转文件命名为 `mihari-daemon.log.1` … `.N`，与当前文件同目录。当前文件算一份，因此 `max-files=3` 时最多存在 `*.log`、`*.log.1`、`*.log.2`。

每个逻辑文件有一个相邻 lock file（例如 `mihari-tui.log.lock`），只用于 Mihari 进程间协调，不进入导出。lock file 不承载配置或业务数据。

权限语义：

- Unix：`LogDir`/默认 `LogExportDir` 为 0700；日志、lock file、临时导出与最终 zip 为 0600。对象归数据根 owner UID/GID 所有；root daemon 创建时必须按数据根 owner 修正 ownership，使桌面用户可读写，而 root 仍可执行服务职责。打开 dataRoot 允许跟随用户配置的最后一跳；之后从该目录 fd 对 `LogDir` 及文件逐段 `openat(..., O_NOFOLLOW)` 并 `fstat`，禁止对完整路径一次 `Open`/`O_NOFOLLOW`。无法安全确定或设置 owner 时不创建宽权限替代物。
- Windows：不能把 0600/0700 当作 ACL。创建或打开时应用受保护 DACL，只授予数据根所有者 SID 和 LocalSystem 必要访问；不授予 `Everyone`、`Users` 或匿名主体。目录 ACE 继承到日志与 lock file。TUI 导出到数据根外时授予当前交互用户及 LocalSystem，且不放宽父目录 ACL。打开 dataRoot 允许跟随用户配置的最后一跳；之后每一跳相对打开必须带 `FILE_FLAG_OPEN_REPARSE_POINT`（或等价 `NtCreateFile`），看到 reparse/junction 立即失败，禁止先跟随再检查。日志、lock、temp、快照打开使用 `FILE_SHARE_READ|FILE_SHARE_WRITE|FILE_SHARE_DELETE`；导出 workspace directory handle 是明确例外：创建时请求 `DELETE`，只 share read/write，整个生命周期不 share delete（见 5.9.1）。
- Windows 服务使用安装时固定的数据根；以 LocalSystem 创建日志时，从既有数据根的 owner SID 识别桌面数据所有者。无法可靠确定 owner 或无法加固 ACL 时，不创建一个宽权限文件。
- daemon 无法安全准备 `LogDir` 或日志文件时启动失败；TUI 日志初始化失败则降级到净化后的 stderr，并保持 TUI/导出错误可见。

### 5.3 JSONL 行与 mihomo 捕获

每一行一个 UTF-8 JSON 对象，无 JSON 数组包装，扩展名仍为 `.log`。

公共字段：

| 键 | 必填 | 说明 |
|---|---|---|
| `time` | 是 | RFC3339Nano，带本地数值时区偏移，例如 `2026-09-02T23:41:08.123456789+08:00` |
| `level` | 是 | `DEBUG` / `INFO` / `WARN` / `ERROR` |
| `msg` | 是 | 人类可读且已经脱敏的消息；mihomo 为净化后的单行文本 |
| `component` | 是 | `daemon` / `tui` / `mihomo` 或更细的 daemon 子系统名 |

可选字段：`stream`（仅 mihomo：`stdout` / `stderr`）、`truncated`、`invalid_utf8` 及业务 attr。所有 attr 都经过同一 redactor。

mihomo 第一期不解析行内格式，其 `level` 明确定义为 **Mihari 的捕获级别**，不是 mihomo 声称的业务级别：

- stdout 行：`level=INFO`，`stream=stdout`；
- stderr 行：`level=WARN`，`stream=stderr`；
- Logging level 会按上述捕获级别过滤；例如 `warn` 会过滤 stdout。这不会改变生成配置中的 mihomo `log-level: info`；
- 文档和 manifest 必须说明该字段不能用来推断核心行内真实严重度。

line capture writer 接受任意 chunk，而不是假定一次 `Write` 等于一行：

1. 跨 `Write` 缓存半行，一次 chunk 可产生零行、一行或多行。
2. `\n` 结束逻辑行并去掉前面的 `\r`；空行也作为 `msg=""` 写出。
3. 每条逻辑行最多保留前 16 KiB；达到上限后丢弃该行余下字节直至换行，并写 `truncated=true`，不得继续扩张缓冲。
4. chunk 可以拆开 UTF-8 rune；到行结束时统一验证，非法或被截断的序列替换为 U+FFFD，并写 `invalid_utf8=true`。
5. `Close` 把最后一个没有换行的半行 flush 一次；空缓冲不生成记录；`Close` 幂等。
6. capture 的底层日志失败由 logging 失败计数与限频 stderr 记录吸收；`Write` 仍返回 `len(p), nil`，不得因诊断落盘失败反向终止 mihomo pipe。
7. `Close` 后的 `Write` 返回 `os.ErrClosed`，且不触碰共享 logger。

TUI/daemon 使用 `log/slog` 的 `JSONHandler` 与 `LevelVar`。handler 的 `ReplaceAttr` 把 `time` 格式化为带本地偏移的 RFC3339Nano，并在最终编码前完成键和值脱敏。

### 5.4 写入、轮转与热更新

`internal/logging` 的 rotator 实现 `io.WriteCloser`。每条完整 JSONL record 的 write、size check、rotate 和 append 在同一逻辑临界区完成：先取进程内 mutex，再取该逻辑文件的跨进程排他锁。活跃文件必须在取得跨进程锁后打开或核对 identity；不得跨锁长期使用一个可能已被其他进程 rename 的陈旧句柄。

跨进程锁使用 OS 随进程退出自动释放的 advisory lock（Windows `LockFileEx`、Unix `flock`），不能把 lock file 是否存在当成“仍被持有”。普通日志 write 最多等待 250ms；超时后丢弃该条、增加失败计数并返回，不能无限阻塞 TUI 或 daemon。Open 初始收敛、Apply 配置维护、rotate 和导出都必须在各自操作期间持有同一逻辑文件的 advisory lock；Open 接收调用方 context，不得在启动时无限等待。等待器和时钟必须可注入，测试不依赖固定 sleep。

轮转前使用不发生加法溢出的判断：

```text
incoming > maxSize                     => 先轮转，再完整写入这一条
incoming <= maxSize 且 current > maxSize-incoming => 先轮转，再写入
其它                                    => 直接写入
```

单条记录超过 `max-size` 时允许新活跃文件暂时超过阈值，以免截断已经编码完成的日志记录；下一条写入前再按正常规则轮转。

`max-files` 包含活跃文件。Open、Apply 缩容和每次 rotate 都在进程内 mutex 及该逻辑文件的跨进程 exclusive lock 下做收敛：删除本逻辑文件、严格数字 suffix、且 `suffix >= max-files` 的普通文件（`max-files=1` 时删除全部 archive）。不得根据锁外枚举或陈旧目录项执行删除。磁盘上因上次缩容失败或崩溃留下的 `.3`–`.9` 不得残留。Open 与 Apply **只收敛，不移位、不 ReplaceEmpty**，不得改写活跃文件内容。经典移位与 `max-files=1` 的空 inode 替换只发生在 rotate。

轮转算法：

- `max-files=1`：收敛后创建一个安全的空同目录临时文件并原子替换活跃路径，不创建 `.1`，也不得在原 inode 上 `truncate`。rotate/`ReplaceEmpty` 前关闭本进程当前 write handle。这样导出已经打开的快照句柄仍能读完旧 inode，不会被并发清空。
- `max-files>1`：收敛后删除 `.max-files-1`（若存在）；将 `.max-files-2` 到 `.1` 由大到小改名为下一号；将活跃文件改名为 `.1`；创建 0600/受限 DACL 的新活跃文件。rename 前关闭本进程当前 write handle。
- rename/delete 只处理严格匹配本逻辑文件的普通文件；符号链接、目录或异常 suffix 记错并跳过，不跟随。

默认 `max-size=10 MiB`、`max-files=3`。合法范围分别为 `1..100 MiB` 和 `1..10`。不按日期轮转。

热更新语义：

- `Apply(context.Context, Config)` 有意不返回 error：level、max-size、max-files 的内存替换是同步、不会失败的提交动作；调用者只传入已经验证的有效值。Runtime 在包内拆成不等待 IO 的 `swapConfig` 与 `convergeArchives` 两阶段；`Runtime.Apply` 依次组合两者，`Group.Apply` 则必须先对 daemon/mihomo 的所有 target 调 `swapConfig`，再逐个调 `convergeArchives`，不能简单顺序调用 `Runtime.Apply`，也不能因为第一个 target 的 context 取消而漏掉后续 target。
- max-size 调小但当前文件已经超限时，不立即制造空轮转；下一条非空记录写入前轮转。
- max-files 调小时在配置内存切换后，立即在同一跨进程锁下 best-effort 删除超出新上限的旧 archive；advisory-lock **获取**受调用方 context 和 250ms deadline 约束。取得锁后同步的 `ReadDir`/`Remove` 不能由 Go context 抢占，文档不承诺整个本地文件系统维护在 250ms 内结束。取消/锁超时/清理失败不回滚已经持久化的配置，也不让 PATCH 失败，但必须计入失败计数并限频写 stderr；后续每次 rotate 都再次收敛到新上限。
- max-files 调大不创建空 archive。
- TUI 与 daemon 各自应用 daemon 返回的完整有效配置，不按本地旧值做增量猜测。

普通 write/rotate 失败不得 panic 或退出进程；该条允许丢失，错误计数增加，并按错误类别限频写一行净化后的 stderr，避免磁盘故障造成错误风暴。

### 5.5 `mihari.yaml`、唯一 Settings owner 与兼容性

schema 保持 `mihari.settings/v1`，新增可省略的 `log` 块：

```yaml
log:
  level: info          # debug | info | warn | error；有效缺省 = info
  max-size-mb: 10      # 1–100；有效缺省 = 10
  max-files: 3         # 1–10；有效缺省 = 3
```

加载旧文件时，缺少 `log` 或字段为零都解析成有效缺省值；非法非零值由 `Validate` 拒绝。保存时如果三个有效值都是缺省值，则省略整个 `log` 块；只有非缺省配置才写入，从而避免用户仅运行新版本就无意义地破坏旧版读取。

daemon 启动加载发生在 Manager 尚不存在之前，是唯一的 bootstrap 写入例外。`LoadOrCreateWithSidecarOutcome` 对首次创建和既有文件 sidecar 更新都返回 `CommitResult`：replace 前失败才返回 error；replace 后 sync 失败随已加载 Settings 返回 `Warning`，启动继续，并在 logger 建立后经净化的 `OnBackgroundError` 上报。旧 `LoadOrCreateWithSidecar` 只作为兼容 wrapper 保留原有“Warning 作为 error”行为，生产 daemon 装配不得继续调用它。

Manager 构造时必须深拷贝 bootstrap Settings；从构造完成起，`runtime.Manager` 是进程内完整 `config.Settings` 的唯一权威 owner，也是 `mihari.yaml` 的唯一写入者：

- onboarding service 只拥有 onboarding completion 等自身状态，不保存 Settings 副本，不直接调用 `config.Save`；
- Ports、Logging、system proxy desired、TUN、core channel 等所有 Settings mutation 都调用 Manager 的同一候选配置提交入口；
- 候选入口从当前权威 Settings 克隆，应用 update，校验完整 Settings，原子保存候选文件；单文件写入把 replace 定义为不可逆 commit point，并返回结构化 `CommitResult{Committed, Warning}`：replace 前失败返回 error 且 `Committed=false`，磁盘不变；replace 成功后 `Committed=true` 且调用方必须发布内存，即使随后的 parent-directory sync 失败也只通过 `Warning` 上报，不能把已提交写入伪装成失败；
- 校验或 replace 前保存失败时，磁盘、Manager 内存、运行时 logger/rotator 和全局 revision 全部不变；post-commit warning 不改变成功语义，经脱敏的 `OnBackgroundError` 上报；
- Logging 保存成功后再执行不会失败的 `LevelVar`/limits 内存替换，然后 coordinator 提交 revision；archive 的 best-effort 热缩容属于文件维护，不改变配置 mutation 的成功与否；
- Manager 内所有会调用 coordinator 写 revision 的路径都必须先取得同一 `maintenance` gate；用户 mutation 在取得 gate 后、任何外部副作用或持久化前重新检查 closing/degraded，不能只在 `doOperation` 入队前检查。只读 GET 和后台 runtime observation 允许在 degraded 时继续，但同样通过 maintenance 与 mutation 串行，防止 system proxy/TUN 已产生副作用后被迟到的后台 revision 变更造成最终 conflict；
- 用户 mutation 在等待 maintenance gate 和越过首个 commit point/外部副作用之前遵从请求取消；一旦产生必须由 revision 反映的已提交事实，后续纯内存 publish、补偿结果记录、coordinator commit 与 operation-cache 结果收口必须使用仅移除取消信号的 context 完成，不能因客户端断开留下“磁盘/OS 已变而 revision 未增”。`context.WithoutCancel` 只允许用于已经持 gate 的短小本地收口，不得包住下载、磁盘 replace、controller、进程等待或其它慢 IO；这些慢操作取消失败时仍按各自补偿语义处理。
- coordinator 继续提供最终 revision 检查和串行提交；不得通过多个组件各持一份值拷贝来“共享” Settings，也不得在 maintenance 外直接 `Coordinator.Do` 写 Store。

onboarding 同一请求可能同时修改 `mihari.yaml` 与 `onboarding.json`，普通文件系统不能提供跨文件原子 replace，因此使用显式补偿语义而不虚构全不变：Settings 先 commit 但不 publish，state commit 后才一起 publish；state 在 commit 前失败时回写 before Settings。若该补偿也在 commit 前失败，磁盘确定仍为 after Settings + before state，Manager 必须发布这个实际 Settings、保留实际 onboarding state、置 `restartRequired=true`，把全局 health 标为 degraded，并通过 coordinator 的 committed-error 路径增加一次 revision 后返回稳定 `data_failure`。daemon 在本次进程余下时间拒绝所有 mutation、继续允许只读状态与日志导出；重启从现有两文件重新装载并解除 degraded，用户可重试 onboarding。补偿 replace 成功但 directory sync 警告则按已恢复 before 处理，不进入 degraded。

system proxy/TUN/controller 等外部副作用遵循相同候选与补偿规则：先在 held mutation gate 内完成 revision 预检和 Settings candidate commit/publish，再应用外部状态；外部应用失败则恢复 before Settings，并在可行时恢复已观察的外部状态。Settings 或 live 补偿失败时以内存对齐已提交磁盘值、推进 revision、以固定的 `mutation compensation failed; restart required` 进入只读 degraded，不得返回错误同时留下旧内存/旧 revision；底层 OS/path 错误不进入稳定状态。该极端分支允许请求部分生效，但必须显式可观察；正常校验、首次保存和可成功补偿的失败仍保持请求级原子。Settings 首次保存失败时不得触碰 system proxy/controller。daemon 重启按 desired 收敛 Mihari-owned proxy，但不得清除 foreign proxy。

必须用顺序回归测试证明 Logging→Ports、Ports→Logging，以及 Logging 与现有 Settings mutation 任意先后都不会覆盖对方字段；还要覆盖 replace 前失败、replace 后 sync warning、onboarding state 失败后补偿成功，以及补偿 commit 前失败后的磁盘/Manager/revision/health 对齐。

降级边界：当前旧版使用 `KnownFields(true)`，因此无法读取包含未知 `log` 块的文件。保留 v1 是一次加法扩展，不承诺旧二进制读取自定义 Logging 配置。缓解措施是：

- 默认配置不序列化 `log`，未自定义的用户可直接降级；
- 把三个值恢复默认会移除 `log` 块；
- README/升级文档明确说明：使用自定义 Logging 配置后，降级到不认识 `log` 的版本前必须先恢复默认或手动备份并删除该块；
- 不用偷偷忽略未知字段来削弱当前版本的配置校验。

该块不是 mihomo 的 `log-level`；生成的 runtime config 仍按现有逻辑写 `log-level: info`。

### 5.6 控制协议

daemon Logging runtime 正常初始化时在 capabilities 中新增 `logging`；degraded runtime 不宣告该能力，调用 endpoint 返回 `invalid_state`。新增：

```http
GET /v1/logging
PATCH /v1/logging
```

GET 与成功 PATCH 都返回完整的有效状态：

```json
{
  "schema": "mihari/v1",
  "revision": 13,
  "level": "info",
  "max_size_mb": 10,
  "max_files": 3,
  "dir": "C:\\Users\\…\\.mihari\\logs"
}
```

`dir` 是供本机特权客户端只读展示的绝对路径，不是 secret。PATCH 请求：

```json
{
  "operation_id": "logging-…",
  "if_revision": 12,
  "level": "debug",
  "max_size_mb": 20,
  "max_files": 5
}
```

协议 DTO：

- `operation_id string` 必填且非空；`if_revision *uint64` 可选，`0` 是合法的显式 revision；
- `level *string`、`max_size_mb *int64`、`max_files *int64` 均可选，至少一个非 nil；
- `null` 等价于未提供；如果最终没有修改字段，返回 `invalid_argument`；
- 数值必须是 JSON 整数。字符串、小数、指数形式、负数、零、超过 `int64` 或业务范围的值全部拒绝；
- 先验证 `max_size_mb` 为 `1..100`、`max_files` 为 `1..10`，再把 MiB 转成 `int64` bytes 或把 files 缩窄为内部 `int`；
- 未知字段继续由 `DisallowUnknownFields` 拒绝；handler 与 runtime/domain 边界都校验，非 HTTP 调用不能绕过。

PATCH 是请求级原子操作：先构造并验证完整候选配置，再原子持久化，随后切换 Manager、daemon logger 与 rotator，最后增加 revision。失败请求不得部分生效。`Apply` 的不会失败部分是所有 target 的 level/limits 内存切换；archive 删除是有界 best-effort 维护。磁盘替换和并发日志 write 不要求物理同一瞬间发生，但成功响应返回前所有配置消费者必须收敛到响应值。请求在 Settings replace 后取消时内部仍完成 publish/runtime/revision/operation-cache 收口；客户端可以收不到响应，但相同 operation ID 重试必须取得第一次已提交结果。

成功与幂等语义：

- PATCH 返回 HTTP 200 和上述完整 `LoggingStatus`，不另包一层，也不把 `operation_id` 放入状态 DTO；
- 使用新的 `operation_id` 提交与当前值相同的字段，仍是成功 mutation，revision 增加一次；实现可跳过无意义的 YAML 重写和热应用；
- 接入现有 `doOperation`，key 为 `logging:<operation_id>`。同一 ID 的相同逻辑请求在进程内缓存存在时返回第一次结果，不重复提交或增加 revision；客户端不得把同一 ID 用于不同 payload；
- revision 的全局耗尽不在本功能中处理，见 Issue #191。

错误契约：

| 场景 | HTTP | code |
|---|---:|---|
| JSON/类型/未知字段错误、缺 operation ID、空 PATCH、非法 level/数值 | 400 | `invalid_argument` |
| `if_revision` 不匹配 | 409 | `revision_conflict`，details 含 expected/current |
| Logging runtime 不可用 | 409 | `invalid_state`（GET 与 PATCH） |
| Manager 因补偿失败要求重启 | 409 | `invalid_state`（仅 PATCH；GET/只读/Export 仍成功） |
| Settings 在 replace commit point 前持久化失败 | 422 | `data_failure` |
| 未分类内部错误 | 500 | `internal` |

错误 envelope 沿用 `/v1` 既有格式，消息不得包含凭据、完整路径中的敏感片段或底层错误全文。Logging 热更新不要求 daemon/mihomo restart，不设置 `RestartRequired`，也不塞进 onboarding DTO。

### 5.7 TUI bootstrap 与 System Logging 区

TUI 在控制连接建立前不能读取 `mihari.yaml`，也不能把编译期缺省值当成权威配置，否则默认 `max-files=3` 可能误删用户配置保留的 4..10 号 archive。启动 writer 使用合法范围内最保守的 bootstrap：

- `level=debug`，避免连接前丢掉可能被权威配置允许的级别；
- `max-size=100 MiB`、`max-files=10`，保证不会比任何合法权威配置更早轮转或删除历史；
- 连上 daemon 并 GET 成功后，一次性应用返回的 level / max-size / max-files；
- daemon 不可用时，System Logging 配置值显示 `Unavailable`/未知，不显示伪造的默认值；配置行不可提交，Export 仍可用；
- GET/PATCH 成功后保存响应 revision；即使 revision 为 0，后续 PATCH 也要显式发送 `if_revision: 0`；root 为每次连接维护单调递增的 logging sync epoch，重连或 capability 消失时先递增 epoch、清除已观察 revision 并回 bootstrap；
- PATCH 成功只采用响应中的完整有效状态。`revision_conflict` 重新 GET，不用旧表单自动覆盖新状态。
- 已连接 TUI 利用现有 session Status poll：首次连接、重连或观察到全局 revision 改变时重新 GET Logging。发现 revision 改变到 GET 成功之间，writer 临时回到 debug/100/10 的保守策略，避免另一个 TUI 把保留上限调大后，本实例长期拿旧的小上限继续删除 archive。session GET、System PATCH/冲突 GET 都把发起时的 sync epoch 随结果交给 root；root 只在 epoch 等于当前值且 revision 不小于当前已观察 revision 时采用完整状态，旧结果只清理其来源页面的 pending/error，不得回退 root/System/applier。`tui.Run` 拥有一个独立的串行 Logging applier：Model 的值接收 `Update` 只用非阻塞 `Submit` 覆盖 heap 上的 latest desired；单 worker 调 `Apply(ctx, config)`，若执行期间又收到新 generation，返回后立即再应用最新值。不得把阻塞 Apply 作为裸 `tea.Cmd`，因为 Bubble Tea 退出不等待 Cmd goroutine。GET 成功的完整配置不得被更早的 bootstrap Apply 晚到后改回 debug/100/10；applier 提供幂等 `CloseAndWait`，退出时 cancel 正在等待锁的 Apply 并等待 worker 结束。

System 页新 section `Logging` 放在 `Ports Config` 之后、`Daemon` 之前：

| 行 | 交互 |
|---|---|
| Level | Enter 在 `debug / info / warn / error` 间循环，立即 PATCH |
| Max file size | Enter 行内编辑，单位 MiB；`ParseInt(..., 10, 64)` 后检查 `1..100`；Enter PATCH，Esc 取消 |
| Files to keep | Enter 行内编辑；`ParseInt(..., 10, 64)` 后检查 `1..10`；Enter PATCH，Esc 取消 |
| Directory | 只读显示 daemon 返回的 `dir`，离线时显示不可用 |
| Export logs | Enter 打开导出对话框，不要求 daemon |

配置失败显示 Failed 徽章和净化后的稳定原因；不把底层转换或文件错误原文显示给用户。帮助 catalog 新建 `ModeLoggingEdit`（Type value / Enter apply / Esc cancel），不复用 Ports 的 address 文案。

### 5.8 导出对话框

Logs 页与 System Logging 区共用 `internal/tui/ui/exportlogs.go` 的对话框组件，避免页面模型互相依赖。

Logs 页 Controls 增加 `Export`；快捷键 `e` 打开。`e` 在搜索、详情或其它文本输入模式下不触发。页脚与 `?` 帮助同步更新。

字段：

1. **Now（只读）**：`2026-09-02 23:41:08 +08:00`。打开对话框时取样一次用于显示和默认文件名预览。提交 Export 时再取一次 Now，专用于时间窗 UTC 转换；若用户未改 Output path，默认 basename 按提交时刻重算。数值偏移是校对依据；`time.Local.String()` 只可作辅助。
2. **Range**：Enter 循环 `Last 24 hours` → `Last 60 minutes` → `Between` → `All`；默认 Last 24 hours。
3. **From / To（仅 Between）**：本地时间 `2006-01-02 15:04`；切入时 From=Now-24h、To=Now。
4. **Output path**：默认 `{dataRoot}/logs-export/mihari-logs-YYYYMMDD-HHMMSS±HHMM.zip`。必须是绝对 `.zip` 路径。自定义父目录必须已存在；默认目录可安全创建。

默认路径若已存在，UI 选择第一个空闲的 `-1`、`-2` 后缀；自定义目标已经存在时拒绝，永不覆盖。

Tab 在可编辑字段间移动；文本焦点下 Enter 也提交导出；Esc 在编辑态关闭对话框。导出期间显示 Pending，禁止重复提交；此时 Esc 请求 context cancellation，等待 runner 回收资源后回到可编辑表单并显示 `Export cancelled`。

根 overlay 打开时必须先收到每一条 `tea.Msg`（在 root typed switch / `dispatchPage` 之前）。它独占键盘、当前 generation 的结果消息，以及文本焦点下的 `tea.PasteMsg` / clipboard 回程；这些返回 `consumed=true`。其它消息——包括 `ui.LoggingSyncMsg`、`ui.LoggingObservedMsg`、session status/logging、service/network poll、`ui.PageResultMsg` 与 action completion——返回 `consumed=false` 后必须继续经过根模型原有完整路由。overlay 的 View 必须画在 Setup 短路和其它 `model.modal` 整屏替换之前，不得因为页面暂时不可见就阻止页面清除 pending 或吸收异步结果。其它 modal 仍可入队，但在导出关闭前不抢键盘、不覆盖导出 View。

成功态：

```text
Export complete
C:\Users\…\.mihari\logs-export\mihari-logs-20260902-234108+0800.zip
Enter copy path  Esc close
```

Enter 用已有 clipboard 写绝对路径。复制失败时路径仍显示，并提示 `Could not copy path`。其它失败在对话框内显示一行净化原因，保留参数以便重试。

### 5.9 导出快照与 zip 算法

TUI 调用 `internal/logging.Export(ctx, request)`；库不依赖 daemon。算法：

1. 在点击 Export 时再取一次 `Now`，把本地时间窗转换成 UTC 闭区间；All 不过滤。打开对话框时的只读 Now 只用于显示和默认文件名预览。From > To 返回 `invalid_argument` 风格 UI 错误。默认路径请求必须 `AutoNumber=true`；自定义路径禁止自动改名。
2. 规范化并验证目标路径：要求绝对 `.zip`；默认 `LogExportDir` 可按安全权限创建，自定义父目录不递归创建。`PrivateFS.OpenDirIdentity(LogDir)` 从已经持有的可信 LogDir handle 派生一个必须关闭的 opaque `DirectoryIdentity` capability；打开目标父目录得到 `PublishDir` 后，用 `PublishDir.IsWithin(logDirIdentity)` 逐级比较持有目录的真实 identity，拒绝目标父目录等于或位于真实 LogDir 内。不得从未解析 symlink 的 `Paths.LogDir` 字符串构造安全结论。该 LogDir capability 与 `PublishDir` 一起转交给导出并保持到发布结束；每次 no-replace publish 尝试紧邻提交前都重新检查 held `PublishDir` 是否进入 held LogDir，检查失败或 containment 改变均 fail closed。target 只保留一个已校验的 basename，拒绝已经存在的对象（包括 symlink）和非目录父级。随后从该 `PublishDir` 创建 0700/protected-DACL 的私有 `PublishWorkspace`；temp、spool 只相对 workspace handle 操作，exists/final publish 只相对 parent handle 操作，不再用已校验的完整路径重新寻址。
3. 对 daemon、tui、mihomo 三个来源依次取得有界快照。获取对应共享锁，在锁内通过 `PrivateFS.ReadDir` 枚举严格匹配的普通文件及其 opaque `FileIdentity`、按数值 suffix 从大到小排列 archive，最后是活跃 `.log`；`OpenReadChecked(path, expectedIdentity)` 相对同一可信目录打开允许 writer 后续 rename/delete 的只读句柄并比较实际 handle identity，不匹配就关闭并使该来源失败。记录当时 size 后释放锁。解析阶段只从每个句柄读取记录的 size，忽略之后 append 的字节。
4. 快照锁保证不会截在一条由 Mihari writer 正在写的 JSONL 中间。锁释放后的 rotate 不改变已打开句柄指向的文件 identity；Windows 使用显式 delete-sharing open。某一来源的顺序是最旧 archive → … → `.1` → 活跃文件。不同来源不宣称共享同一纳秒级快照。
5. 逐物理行使用启用 `UseNumber` 的 `json.Decoder` 解码到 `map[string]any`，避免二次编码把大整数先转成 `float64` 而损失精度。首次 Decode 必须得到 object，随后第二次 Decode 必须得到 `io.EOF`；数组、标量、同一行的第二个 JSON 值或任意尾随 token 都是一个 invalid record。单行解析上限为 1 MiB；超过上限时有界丢弃到下一个换行并计入 `skipped_invalid`，不得无界分配。缺 `time`、时间无效或 JSON 损坏同样计入 `skipped_invalid`，不写 zip。保留文件顺序和文件内记录顺序，不根据 wall clock 重新排序，避免系统时钟回拨改变因果顺序。
6. 每个有效对象在导出边界再次递归脱敏，然后重新 `json.Marshal` 为一行；不保留原始 JSON 字节或字段顺序。窗内记录写到该来源唯一的 zip entry。某来源没有命中则省略 entry。
7. 在枚举来源、读取批次、解析行、写 zip 和发布前检查 `ctx.Err()`。成功、取消或失败均关闭所有快照/zip/temp 句柄，尝试清理 workspace 内临时文件，再按 `PublishWorkspace`→`PublishDir`→LogDir `DirectoryIdentity` 的所有权顺序关闭。目录项清理严格遵守 5.9.1 的 Unix 信任前提与 Windows guard；降级或 IO 失败通过净化 warning 报告，不追删路径、不触碰 replacement、不改报已提交结果。

zip 固定布局：

```text
manifest.json
daemon/mihari-daemon.log
tui/mihari-tui.log
mihomo/mihomo.log
```

zip entry 名是常量，不使用用户路径。`manifest.json`：

```json
{
  "schema": "mihari-logs-export/v1",
  "exported_at": "2026-09-02T23:41:08+08:00",
  "timezone": "+08:00",
  "range": { "kind": "last_24h", "from": "…", "to": "…" },
  "files": [
    {
      "name": "daemon/mihari-daemon.log",
      "lines": 120,
      "skipped_invalid": 1,
      "redacted": 2,
      "sources": ["mihari-daemon.log.2", "mihari-daemon.log.1", "mihari-daemon.log"]
    }
  ],
  "notes": ["mihomo level is Mihari capture classification; core-emitted node names and traffic metadata may remain"]
}
```

三个来源都无命中时不发布 zip，提示 `No log lines in the selected range`。

原子发布：

- 目标解析阶段打开并持有真实父目录 handle；默认目录从 `PrivateFS` 的已验证子目录 handle 派生，自定义目录只跟随用户选择的父路径这一次，确认是目录并记录 canonical absolute path/identity，之后不修改其 mode/ACL；
- 从该 `PublishDir` handle 创建不可预测名称的私有 `PublishWorkspace` 子目录，Unix mode 0700，Windows protected DACL 只含当前交互用户与 LocalSystem；非受信主体即使可在 Unix 自定义 parent 改名目录项，也不能进入 workspace 或替换其中 source basename；同 UID/管理员属于 5.9.1 的受信边界。Windows workspace 从创建起禁止 delete sharing。相对 workspace 创建随机 temp/spool basename并应用 0600/受限 DACL；使用标准库 `archive/zip` Deflate 写入；所有 workspace remove 与 parent exists 也只接受单段 basename；
- 完成所有 entry 和 manifest 后依次关闭 zip writer、sync 临时文件；每次提交尝试先用仍持有的 LogDir `DirectoryIdentity` 重新执行 `PublishDir.IsWithin`，确认目标目录仍在真实 LogDir 外，再调用 handle-relative `PublishNoReplace(workspace, tempName, targetName)`；检查失败、结果为 inside 或检查期间 identity 不稳定均 fail closed。Unix 从 workspace dirfd `linkat` 到 parent dirfd 后 unlink source，Windows 对从 workspace handle 相对打开的 temp handle 调 `NtSetInformationFile` 并把 parent handle设为 RootDirectory，不得退回路径型 `os.Link`/`MoveFileEx`；必要时 sync 两个目录 handle；
- 自定义目标在校验后被别的进程抢先创建时返回 `already exists`，不得覆盖；默认目标发生竞争时复用同一 held `PublishDir`、workspace 和已关闭 temp，直接以 no-replace publish 依次尝试下一个编号后缀，每次重试前检查 context，防止先查后用竞态。只有发布成功后才把实际 basename/canonical path 写入 `ExportResult.Path`；suffix 的 `int64` 递增溢出返回稳定失败；
- 校验后原父路径被 rename 或替换为 symlink/junction 时，写入仍只落到已打开的目录 identity，绝不跟随替换路径；实现可以在 publish 前比较一次可见路径 identity 并选择安全失败，但不能把完整路径重新解析作为提交依据；
- publish 是成功的不可逆点。publish 前取消必须无目标文件；publish 已成功后即使 context 随后取消也返回成功路径；
- 任意 publish 前失败都尝试删除仍能从 held workspace 访问的文件，再依 5.9.1 清理私有目录项并关闭 workspace、PublishDir 与 LogDir identity；Unix 不可信 namespace、move-out 与 IO 失败遵守该节 warning/残留边界。publish 后的 cleanup/sync 失败只报警，不能把已发布 target 报成失败。不得先创建一个空目标再慢慢写 zip。

#### 5.9.1 workspace 清理的信任边界（2026-09-05 已批准修订）

此节统一约束成功、取消、失败及创建后检查失败的清理，不改变 no-replace 发布、权限、协议或依赖。Unix 的 `unlinkat(parentFD, basename, AT_REMOVEDIR)` 没有 expected-identity 参数；held fd 和 advisory lock 均不能排斥非协作 namespace writer。重复 identity 检查不等于原子条件删除。

- 受信主体固定为当前有效 UID、默认导出时已验证的数据根 owner UID，以及可绕过权限的本机 root/管理员；自定义目录的任意 owner 或组成员不会因“拥有父目录”而自动受信。同 UID 进程属于受信主体；本设计不防御其恶意并发改名、权限修改或访问日志。Windows 同样信任交互用户与 LocalSystem/管理员。受信主体须不在最后验证到删除期间主动破坏已验证的 namespace/权限前提。
- Unix 打开 held parent 时，在数据根 owner 上下文确定后评估一次 namespace mutation authority，并在最终 identity 检查/删除前重新评估 owner、mode、sticky 与实际有效 ACL。初始与最终评估均可信，且非受信主体不能替换 workspace entry，才允许按名删除；初始不可信或查询失败，即使后来权限收紧，仍清理内容、关闭句柄并以 warning 保留空私有目录。普通 owner 管理目录在非受信 group/other 无写权限且 ACL 无额外授予时正常清理。sticky parent 还要求 workspace owner 受信，且系统 sticky 规则及 ACL 确实阻止所有非受信主体删除/替换该 entry；`01777` 本身不充分，非受信 parent owner 仍可删除，必须拒绝清理目录项。
- Unix ACL（包括平台或文件系统扩展的 delete/delete-child 权限）必须被检查或能证明不存在；不得只看 `mode & 022`，不得把未知 ACL 当作无 ACL。若现有无 CGO 能力无法证明某文件系统的有效授权语义，保守判为不可信。无 sticky 的 `0777`/`0770` 或 ACL 允许非受信主体改名/替换时走此降级；owner 受信的 `01777` 在有效权限可证明时正常清理。清理前权限变宽、owner/ACL 改变或查询失败须重新判定并 fail closed，不 chmod/chown 父目录，不添加 ACL 或新依赖。
- 两平台都先经 held workspace 清理仍可访问的 temp/spool；不靠可见路径定位内容。Unix 只有上述证明成立时，才在 held parent 中按 identity 查当前 basename，重查 identity/empty 后删除该空目录；同 parent 内已完成的受信改名仍可清理。原 basename 的 replacement 不可删除。workspace 已移出 held parent，或 parent 权限不可证明安全时，不调用按名目录删除，关闭所有持有句柄并返回净化 cleanup warning；禁止扫描其它目录或沿旧路径追删。
- Unix 降级时，若内容清理成功，允许留下不可预测名称、0700 的空私有目录，不含日志、spool、zip temp、凭据或元数据。若内容删除、枚举、权限检查或其他 IO 本身失败，只能保证尝试所有后续清理并关闭句柄；warning 必须反映清理不完整，不能声称空 orphan 或零残留。不会自动按路径重试遗留对象。
- Windows workspace 从原子创建成功起持有请求 `DELETE`、不含 `FILE_SHARE_DELETE` 的不继承目录 handle，直至关闭；不得先以 share-delete 创建再补 guard。parent、日志、temp 与 snapshot 仍按各自原共享规则打开。外部独立 handle 的 workspace rename/delete 必须被 mandatory share compatibility 拒绝，但不妨碍 child IO/temp publish。清理须在同一 guard 仍有效时证明 held identity、当前 parent containment 和 empty，然后在同一 handle 设置 delete disposition 并关闭；验证失败不得设置 disposition。不得先置 delete-pending 再依赖清除状态的可失败 rollback。同一受信 held handle 若已把 workspace 移到 parent 外，仍只关闭/warn，不删移出的目录。
- 发布提交点仍是 Unix 成功 link / Windows 成功 no-replace rename。提交前 failure/cancel 不创建本次 target；竞争者已创建的 target 保留。所有 cleanup 错误保留主要错误并报告净化 warning；提交后 sync、unlink-source 或 Close 失败仅 warning，返回成功路径且不删已发布 target。无 IO 故障且安全前提成立时要求正常完整清理；不承诺对抗权限变更、恶意受信主体、进程崩溃或文件系统故障的无条件零残留。

zip 只含重新编码且再次脱敏的日志与 manifest，不含 `mihari.yaml`、token、订阅或 lock file。

### 5.10 安全与脱敏

仅靠“调用方不要 log secret”不足以形成安全边界。采用写入边界和导出边界两层防护。

`logging.Redactor` 在 daemon 打开 logger **之前**注册已取得的 controller secret、control token、Web credential 及完整订阅 URL；凭据变化时通过显式更新方法替换不可变规则快照。token 与 controller secret 在 Open 前必须注册。Web credential 或 catalog 加载失败时跳过那些 exact 值，**仍 EnsureDir+Open**，再让 `BuildRuntime` 失败走既有 degraded daemon；不得因 catalog/credential 错误在 Open 前 `return`。degraded 路径只要 token/secret 已注册即可记录。TUI redactor 不读取 `mihari.yaml`，注册自身已经持有的 control token，并使用同一通用规则。TUI Runtime/logger Open 失败不得拿走已经创建的 redactor/`PrivateFS`；Export 依赖这两者，不依赖本进程 writer 是否成功。若失败发生在 `NewPrivateFS` 本身，则不能绕过安全边界读取日志：Export 对话框仍可打开，但提交返回固定 `Local log storage unavailable`。

写入边界对 `msg`、error text 和所有嵌套 slog attrs 执行：

- key 规范化后命中 `secret`、`token`、`password`、`credential`、`authorization`、`cookie`、`api-key` 等敏感名称时，值整体替换为 `***`；
- 已注册的非空 secret/credential/full subscription URL 做精确字符串替换；过短的通用字符串不得注册，避免把正常文本抹掉；
- 任意文本中的 `http://` / `https://` / `ws://` / `wss://` URL 整体替换为 `[REDACTED_URL]`，不尝试保留可能把 token 放在 path/userinfo/query 的“安全部分”；
- Bearer、Basic、token/secret/password 等常见 `name[:=]value` 形式替换 value；独立的 64 字符十六进制 controller-secret 形态也必须遮蔽；
- 不记录原始 HTTP/control body、完整 runtime config、环境变量或 `panel open` 一次性 URL；业务代码优先记录对象 ID、状态与稳定错误码。

mihomo 捕获行在进入 JSON `msg` 前也执行通用文本规则；不得把它作为“无法解析所以不脱敏”的例外。通用规则无法判定节点名、目标域名、IP、流量元数据是否属于用户敏感业务信息，因此 UI 成功态和 manifest 提醒用户发送前自查；这不放宽 Mihari 已知凭据必须遮蔽的要求。

导出边界对历史 JSON 对象再次递归检查敏感 key 和字符串并重新编码，防止旧版本日志或漏网调用点被直接打包。若成员的键本身经 `Redactor.String` 会改变（例如包含已知凭据或 URL），省略该成员并标记本条记录已脱敏；不重命名键，避免与其它敏感键或普通 `***` / `[REDACTED_URL]` 键碰撞。保留安全兄弟成员并继续递归，时间和数值保持原语义，不修改原对象，也不计入 `skipped_invalid`。`redacted` 只统计发生过替换或敏感键成员省略的记录数，不把原值、匹配规则或 secret hash 写进 manifest。

stderr fallback 和 UI/protocol 错误同样先经 redactor；底层路径、URL 或 credential 不得因失败路径反向泄露。

### 5.11 错误处理

| 情况 | 行为 |
|---|---|
| daemon 无法安全创建/加固 `logs/` 或初始日志 | daemon 启动失败，返回净化错误 |
| Web credential / 订阅 catalog 在 Open 前加载失败 | 跳过对应 exact 值，仍打开 logger；随后 BuildRuntime 失败则走 degraded daemon |
| TUI Runtime/logger Open 失败但 `PrivateFS` 可用 | TUI 继续运行，使用限频且净化的 stderr；System 显示本地 writer 不可用；Export 仍可用 |
| TUI `NewPrivateFS` 失败 | TUI 继续运行；Export 对话框可打开，提交只显示 `Local log storage unavailable`，不得回退普通路径 IO |
| 日志 write/rotate/热缩容失败 | 不退出进程；当前记录可丢失或多保留旧 archive；失败计数 + 限频 stderr |
| GET `/v1/logging` 失败 | 配置值显示不可用，配置行禁用；bootstrap writer 与 Export 继续工作 |
| PATCH 校验/revision/replace 前持久化失败 | 按 5.6 稳定错误契约；不部分生效 |
| replace 后目录 sync 警告 | 写入已经 committed；发布内存/runtime/revision 并返回成功，净化 warning 后限频记录 |
| Settings 补偿写在 commit 前失败 | 内存对齐已提交磁盘、revision +1、health degraded、后续 mutation 返回 `invalid_state`，当前请求返回稳定 `data_failure`；只读与 Export 保持可用 |
| 源快照中有 symlink/非普通文件 | 不跟随；该源导出失败并显示净化错误 |
| 导出无命中、路径非法、目标存在、写入或发布失败 | 不留下目标 zip；对话框保留参数并显示错误 |
| 导出取消 | 回收 runner/句柄，按 5.9.1 尝试清理 workspace/temp，降级或失败报 warning；回到表单显示 cancelled |
| clipboard 失败 | 路径仍可见，不影响已经成功的导出 |

### 5.12 测试

全部使用 `t.TempDir()`、可注入 clock/file ops/locker，不读真实用户目录、不访问公网、不依赖已安装 mihomo。

- **轮转**：跨 max-size 产生 `.1`；`max-files=1` 永不产生 archive；2/3/10 的精确上限；删除最旧；安全比较覆盖接近 `MaxInt64` 的 size/incoming；单条超限；权限；max-size 下调下一写轮转；max-files 下调立即 best-effort 清理与失败重试。
- **跨进程协议**：两个独立 writer 交替写/轮转同一 TUI base，记录不交叉、不丢失、不写到被 rename 的陈旧句柄；Open 收敛与另一进程 rotate 并发时也只在 exclusive lock 内枚举/删除；advisory lock 随进程退出释放；250ms write 超时、Open/Apply/export context 取消和锁失败；Windows/Unix 平台实现各有目标测试。
- **JSONL/capture**：time 带偏移；chunk 拆行/多行/CRLF/空行；半行 Close；16 KiB 截断后不增长；UTF-8 rune 跨 chunk 与非法字节；Close 幂等；底层失败不终止 pipe；capture level/stream 语义。
- **脱敏**：嵌套敏感 key、注册 secret、64 hex、Bearer/Basic、URL path/query、error 和 mihomo 行都不泄露；导出能再次净化故意构造的旧日志；manifest 不含原值。
- **权限**：Unix 目录 0700、文件 0600、owner UID/GID 正确、root service 与桌面 owner 均可按设计访问、拒绝 symlink；Windows DACL 只含预期数据 owner/System 且可由两者访问，拒绝无法安全加固的对象。
- **settings owner**：缺 `log` 为有效缺省；默认不序列化块；非默认 round-trip；非法 level/size/files；bootstrap sidecar outcome 的 pre/post-commit 语义；Manager 构造深拷贝；Logging↔Ports 及其它 Settings mutation 顺序不互相覆盖；replace 前失败不改变内存；replace 后 sync warning 仍发布；system proxy 首次 Save 失败零 OS write，外部应用失败补偿；补偿 commit 前失败时磁盘/内存/revision/health 对齐并拒绝后续 mutation；排队 mutation 在取得 maintenance 后复查 degraded；后台 revision observation 不能插入 system proxy/TUN 的预检与提交之间；请求取消不能截断已越过 commit point 的本地 revision/cache 收口；恢复默认移除块。
- **协议**：GET/PATCH 精确 JSON round-trip；revision 包含在两种响应；空/null PATCH；未知字段；整数类型/溢出/范围；显式 revision 0；冲突 details；data failure=422；degraded capability；相同新值 revision +1；相同 operation ID 重试不重复提交。
- **TUI 配置**：离线 bootstrap 为 debug/100/10 且不删除合法 archive；离线显示 unavailable；GET/PATCH 应用完整响应；revision 0 仍发送；冲突 reload；连接 epoch 不同或 revision 较旧的迟到响应不回退 root/System/applier；另一个 TUI 改变全局 revision 后经 session poll 进入保守策略并重新同步；数字输入超长安全拒绝。
- **导出快照**：archive 数字顺序从旧到新；枚举 `FileIdentity` 与打开 handle identity 不匹配时 fail closed；快照后 append 不进入结果；快照后并发 rotate/max-files=1 replacement 无重复、漏读或 inode 清空；`UseNumber` 保留大整数；数组/标量/第二 JSON 值/尾随 token、超 1 MiB 和其它无效 JSON 有界跳过并计数；All/Last 24h/Between；保留文件因果顺序；三个来源无命中不发布。
- **导出发布**：用保持到提交结束的 trusted LogDir `DirectoryIdentity` 拒绝相同/后代目录及 symlink data-root 绕过，并在每次 publish 尝试紧邻提交前重新检查 containment；拒绝相对/非 zip/既有目标和不存在的自定义父目录；workspace 为 0700/protected-DACL 且来源 basename 不可由非受信 parent writer 替换；默认目录/编号创建；取消和各阶段注入失败按 5.9.1 清理且不发布本次目标；覆盖可信 owner/ACL 无额外授权、可信 owner 的 sticky、非可信 owner 的 sticky、0777/0770、ACL-only 授权、ACL 查询失败及清理前权限变化；Unix 降级在内容清理成功时仅空私有 orphan+warning，IO 失败必须报告可能的内容残留；覆盖同 parent 已完成的受信改名、replacement 和 move-out；Windows 用独立外部 handle 验证 rename/delete 被拒绝，guard 下验证后才 disposition，并覆盖验证失败无 disposition；默认 publish 抢占后在同一 held Dir/workspace 重试并返回实际路径，自定义目标不重试；校验后替换父路径也不能改变已打开的发布目录 identity；最终 zip 权限与固定 entry；manifest 字段。
- **TUI 导出**：打开时显示 Now、提交时再取 Now 算时间窗；Pending 防重复；Esc cancellation；成功绝对路径；clipboard 失败仍保留路径；Logs 页 `e` 与 System 共用对话框；搜索/详情态 `e` 不打开；Run 持有稳定 model/runner且重开 generation 不复用，在 `Update` 已登记 runner 但 Cmd 尚未入队时取消仍能 `CancelAndWait`；result channel 先完成、`done` 后关闭；root 在 typed switch 前把每条消息交给 overlay；paste/clipboard 在文本焦点下被 overlay 消费；未知消息含 LoggingSync/Observed 默认不消费；默认路径 Open 时目录缺失不建目录，提交 `AutoNumber=true`；overlay View 优先于 Setup/其它 modal；overlay 期间 action/page result 仍走原页面路由并清除 pending；golden 按 Controls/System 布局更新。
- **装配/生命周期**：相对 `MIHARI_DATA` 在选定命令 PersistentPreRun 绝对化；`--help`/`--version` 零数据根 IO；在任何 EnsureDirs/默认 token/Settings IO 前建立进程级 PrivateFS，root/LocalSystem 缺根零 IO fail closed；该失败不写入 CLI SetupError；TUI 收到 nil fs 仍运行，daemon 见到 nil fs 零 Settings IO 后失败；health/`exportLogs` 在 `newModelWithClientContext` 替换之后注入；Settings 加载后创建 daemon logger；`OnBackgroundError` 接入；CommandStarter stdout/stderr 进入 mihomo.log；先关闭 capture 后关闭 logger；TUI applier/Export runner 可取消并等待；TUI/daemon 所有 file/lock/directory/workspace handle 关闭且 `PrivateFS.Close` 幂等。
- **跨平台构建**：保持 `CGO_ENABLED=0`，至少检查 Windows/Linux/macOS 的 amd64/arm64 受影响目标；平台文件与 build tag 配对。

覆盖率不得靠空测试抬高。功能 PR 不改 `CHANGELOG.md`。

## 6. 用户可见变化

- `{dataRoot}/logs/` 出现三个 JSONL 文件及其轮转文件；多个 TUI 共享同一个 TUI 日志序列。
- `{dataRoot}/logs-export/` 在首次默认导出后出现 zip；导出永不覆盖已有文件。
- System 页新增 Logging；离线时配置显示不可用而不是伪造默认值。
- Logs 页 Controls 增加 Export，快捷键为 `e`。
- `mihari.yaml` 只在使用非默认 Logging 配置后出现 `log:` 块；恢复默认后移除。
- README、`docs/commands.md`、`docs/architecture.md` 同步日志路径、导出目录、跨进程写入例外、Windows ACL、脱敏边界与降级说明；不再把单一 `mihari.log` 写成事实。

## 7. 风险与约束

- TUI 写数据根日志是 daemon 单写入者约束的窄例外；只允许 `internal/logging` 追加/轮转固定日志，不扩展到 settings 或业务状态。
- 跨进程锁和 Windows delete-sharing 处理错误会造成陈旧句柄或 rename 失败，因此不能只靠单元测试；需要 Windows 与至少一个 Unix 平台的并发集成测试。
- 跨进程锁增加每条日志的系统调用开销。实现可在锁内验证并复用安全句柄，但不得以性能优化为由跨锁保留无法检测 rename 的句柄。
- mihomo 捕获仍可能含节点名、目标域名/IP 和流量元数据；凭据与 URL 必须自动遮蔽，业务信息由导出提示要求用户自查。
- max-files 热缩容的 archive 删除是 best-effort：配置立即生效，删除失败时可能暂时多保留文件，但不会因诊断清理失败回滚用户设置。
- Settings 单文件 replace 后的 directory-sync 失败已经越过 commit point，只能作为 durability warning；补偿写也无法 commit 时会进入显式只读 degraded，而不是谎称请求完全未生效。该分支不新增持久化格式，重启后按磁盘现状恢复。
- 自定义导出目录可能在长耗时生成期间被外部进程改名；持有 `PublishDir` identity 保证不会写入后来替换到同一路径的目录，但成功提示中的绝对路径是打开目录时解析的 canonical path，外部随后再次改名仍可能使显示路径失效。
- 自定义 `log` 块不兼容不认识该字段的旧版严格 parser；默认省略、恢复默认移除和明确降级说明用于降低风险，不伪称完全 downgrade-compatible。
- JSONL 对用记事本快速浏览不如纯文本友好；这是结构化过滤与安全重编码的已接受取舍。

## 8. PR 切分与交付顺序

实现阶段用 writing-plans 细化。预期三个顺序 PR 指向 `dev`，避免把平台文件系统、安全边界、Settings 重构、稳定协议和两页 TUI 交互塞进一个不可审查的大 PR：

1. **安全文件日志基础**：第一 commit 先更新根 `AGENTS.md`/架构文档，声明 TUI 固定日志追加例外；随后实现平台锁/owner/ACL、Paths、logging/redactor/rotator/capture、daemon/TUI/mihomo 装配与生命周期。daemon 使用正常缺省值，TUI 暂用安全 bootstrap。该 PR 合并时三类文件日志已经能安全落盘和轮转，不修改稳定控制协议或持久化配置。
2. **Settings 与 Logging 控制面**：集中 Manager Settings owner、修复所有 Settings mutation 的候选提交、增加可省略的 `log` 配置、GET/PATCH、client、session 重同步和 System Logging 配置 UI。稳定 DTO、持久化格式、降级说明与用户入口在同一个 PR 中落地，不留下只有 API、没有 UI 的中间状态。
3. **快照导出与交互**：实现安全快照、二次脱敏、可取消的原子 zip 发布、共享对话框、Logs/System 入口及对应文档。

每个 PR 独立通过相关包测试、race、vet、gofmt 和受影响平台的 CGO-free 编译检查；用户可见文档随产生该行为的 PR 更新，不全部拖到第二个 PR。禁止在功能 PR 修改 `CHANGELOG.md`。
