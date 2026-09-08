# Unix Base Dir 统一设计

日期：2026-09-05。修订：R4。状态：完整推荐设计，技术评审结论见独立报告，待用户最终批准；不授权生产实现、提交或发布。

## 1. 范围、基线与产品边界

本设计覆盖 Linux/macOS 的默认系统数据根、控制发现、日志、credential、服务安装/升级和用户树迁移。Windows 的路径、ACL、服务行为和本地导出保持现状，共用 Go 接口必须提供兼容适配器。它完成交接任务的 Unix 设计部分，不宣称 Windows 重设计完成。

基线为 dev 的 `2d00f61e720fa27f115dea52f7b4a95cc35a599f`，包括 #198/#199/#200/#202/#203/#205。依据为 AGENTS.md、CONTRIBUTING、现有 `docs/architecture.md`、代码和本文引用的官方材料。缺失的 2026-08-03 总体架构 spec 不作为隐含依据。旧 v13 与十份评审已备份后移出工作树，不作为增量设计底稿。

推荐并完整设计方向 A：**统一机器控制和业务数据根，TUI 诊断材料按用户隔离**。普通用户无需 sudo 连接；安装、迁移、自更新仍须 root。一机只有一个默认受管 daemon。既有显式 MIHARI_DATA 测试/便携实例保留，属于主动选择的隔离例外，不能注册第二个同名机器服务。

用户最终需批准：上述分区；全体本机用户的代理管理及机器诊断读取权；新增协议/安装入口/journal；旧树保留、配置安全校验及核心版本兼容限制。以下是明确推荐，不以“以后再决定”代替技术合同。

## 2. 已审计的当前实现与方案比较

| 当前文件 | 已确认事实及需要改变的接线 |
| --- | --- |
| platform/paths.go、channel_unix.go | HOME/.mihari 与 root 的 SUDO_USER channel 解析分叉；Absolute 按 Root 重建 Paths |
| control/transport/unix.go | XDG_RUNTIME_DIR 优先，socket 0600，Listen 无实例锁就删除 socket |
| platform/privatefs_unix.go | 根打开后 chmod 0700；缺失根拒绝 root 创建；首次打开不是完整 no-follow 检查 |
| cmd/mihari/main.go、control/client/client.go | 进程缓存 FS/token；客户端可创建 token；SetToken 空值不清缓存 |
| logging/export*.go、snapshot.go | 一个 FS 读取三类日志；逐来源锁下打开文件及记录尺寸，解锁后读取 |
| control/server/server.go | token 启动时固定；ReadHeaderTimeout 5 秒；Shutdown 5 秒 |
| service/service.go、update/self.go | controller 无定义备份能力；self update 先替换再 AfterReplace，同一事务需要新用例 |
| subscription/document.go、generator.go | 通用 YAML map 大量字段原样传入核心，不能当作 root 输入安全验证 |
| scripts/install/*.sh | 重复 HOME/SUDO_USER 规则，AIO 直接 overlay，必须统一安装事务 |

| 方向 | 离线/多用户 | 代价与结论 |
| --- | --- | --- |
| A：私有机器日志 + 每用户 TUI 日志 | 未安装时仍记录 TUI；其他 UID 不可读写 | 增加固定机器快照协议；推荐 |
| B：系统根按 UID 日志子树 | 新用户需特权分配目录；需处理 UID 复用和额度 | 权限治理复杂，离线首次运行受限 |
| C：daemon 写全部日志 | 断线需缓冲、丢弃或额外本地兜底 | 增加事件上传、限流、防伪和重放；不采用 |

A 不承诺离线读取私有机器日志；TUI 可明确选择只导出自己的日志。所有控制用户本来可通过 connections/proxies 等读取机器连接元数据，新增机器日志不是首次授予机器级观察权。日志仍可能含节点、域名/IP 和流量信息，不能宣称完全匿名。

## 3. 布局、模式和无 IO 解析

| 路径角色 | Linux 默认系统模式 | macOS 默认系统模式 |
| --- | --- | --- |
| BaseDir B | /var/lib/mihari | /Library/Application Support/mihari |
| DataDir D | B/data | B/data |
| endpoint E、credential C、channel | B/control.sock、B/control.token、B/mihari-channel | 同左 |
| 安装根 I | /usr/local/lib/mihari | /usr/local/lib/mihari |
| 用户诊断根 U | XDG_STATE_HOME/mihari，默认可信 HOME/.local/state/mihari | 可信 HOME/Library/Logs/mihari |
| daemon/mihomo 日志 | D/logs/mihari-daemon.log、D/logs/mihomo.log | 同左 |
| TUI 日志/导出 | U/logs/mihari-tui.log、U/logs-export | 同左 |

保留现有 I，不自动切换 Homebrew 或其他目录。I 祖先不满足第 5 节安全规则时，安装明确失败；root 可显式选择安全的 MIHARI_INSTALL_ROOT，例如 macOS 的 /Library/PrivilegedHelperTools/mihari。不得自动 chmod/chown /usr/local。

Linux 状态目录参考 [FHS](https://xdg.pages.freedesktop.org/xdg-specs/fhs/latest-single/)，macOS 参考 [Apple 目录指南](https://developer.apple.com/library/archive/documentation/FileManagement/Conceptual/FileSystemProgrammingGuide/MacOSXDirectories/MacOSXDirectories.html)，用户日志参考 [XDG Base Directory](https://specifications.freedesktop.org/basedir/latest/)。将私有日志与状态聚合在 D、把 socket 留在 B 是项目的统一布局选择，偏离 FHS 的日志/runtime 分类，不宣称严格 FHS 合规。XDG_RUNTIME_DIR 不参与默认整机发现。

| 模式 | B/D/U | 创建者及权限 |
| --- | --- | --- |
| root 默认 daemon/服务 | B 如上；D=B/data；TUI U 独立 | root bootstrap 创建 B/D；daemon 创建 C |
| 普通默认 CLI/TUI | 同一 B/D；U 当前 UID | 只读 B 的公开入口；TUI 仅创建 U |
| 非 root 显式 MIHARI_DATA=P | B=D=P；U=P（旧式私有布局） | 同 UID daemon 创建 P/C；TUI 可创建 P/logs，不创建 C/settings |
| root 显式 MIHARI_DATA=P | B=D=P 私有布局；root TUI 可本地日志 | root 创建；跨 UID 客户端无权，不能伪称共享默认模式 |
| 旧 root 服务指向用户可写 P | 仅识别为迁移来源 | 新 daemon 不从该树启动；安装用例迁移，失败恢复旧定义 |

P 是绝对化、清理后的现有数据树，不重解释为 P/data。便携 P 与默认 B/D 相等、祖先或后代时拒绝，避免模式别名绕过。P、I、E、C 可接受已有相对覆盖，但只在进程入口按初始 cwd 固定一次；创建能力另做安全验证。MIHARI_CONTROL_ENDPOINT/CREDENTIAL 分别覆盖 E/C，不互相推导；MIHARI_INSTALL_ROOT 只影响 I。空值等于未设置。

默认服务定义移除 MIHARI_DATA，保存绝对 E/C/I，依靠固定 B/D 默认值；显式私有服务保存绝对 P/E/C/I。默认系统目录不能通过“MIHARI_DATA=B”模拟。自定义共享系统 BaseDir 不新增机器发现机制；既有覆盖是私有模式，跨进程使用者必须显式保持一致。所有正常/恢复服务定义不继承 HOME/SUDO_USER/XDG_* 路径判断。

E 拒绝 NUL、非绝对归一化结果和非 pathname socket；按目标 OS 的 sockaddr_un 容量验证：Linux 路径字节数不超过 107，macOS 不超过 103（均预留终止 NUL）；超长返回 invalid_argument，不截断或隐式 hash。路径长度验证作用于最终实际绑定路径。

## 4. 类型、所有权及 Windows 兼容

新增纯值 `ResolvedLayout{Mode,BaseDir,Data Paths,ControlEndpoint,CredentialPath,ChannelPath,InstallRoot,ClientLogs}`，Mode 由显式输入决定，不靠目录存在性或 owner 猜测。`ResolveLayout(input, platformDefaults)` 无文件 IO。`NewPaths(root)` 和现有 `Paths.Absolute()` 保留旧单根布局语义；系统模式单独构造 Data=NewPaths(D)，C/channel/E 不放回 Paths 重建。所有调用方接收最终 layout，不重新调用 DefaultPaths 猜测。

| 能力/使用方 | 最小职责 | 关闭所有者 |
| --- | --- | --- |
| SystemRoot / daemon、installer | 验证和创建 root-owned B/D、按固定表发布公开文件；锁目录 | daemon bootstrap 或 installer |
| PrivateLogRoot / logging | 从已验证 D/U 目录句柄构造 PrivateFS，不能自行改变 B 权限 | 各自 logging resources |
| ControlLocator / CLI/TUI | 只读验证 C、发现 E；不得 EnsureDirs/LoadOrCreate | 每次请求关闭读句柄 |
| CredentialProvider / client | Load(ctx) 每请求/每重连读；返回分类错误 | client，无后台缓存 goroutine |
| MachineSnapshotSource / app+logging | 固定两类机器日志快照与脱敏，禁止路径参数 | 每次请求 |
| LocalSnapshotSource / TUI | 当前 U 或显式私有 P 的固定日志 | 导出 worker |
| ExportAssembler / logging | 多 SourceReader 输入；PublishDir 保持用户目标身份 | 导出 worker |
| InstallTransaction / app+service | 候选、journal、服务定义与二进制提交/恢复 | installer 用例 |

PrivateFS 增加接受已验证并转移所有权的根 capability 的构造入口，旧 NewPrivateFS 继续供 Windows/旧式适配器使用。禁止调用方先检查 pathname 再让构造器重新按路径打开；重复 Close 幂等，转移后原 owner 不再释放。

Windows 使用原 NewPaths/PrivateFS、静态 token 构造及本地三来源 export adapter，不通告新机器快照 capability；现有 New(endpoint,token)/NewHTTP 固定 token 用于 Windows与测试，新增 WithCredentialProvider 用于 Unix。共用导出内部抽象可变，Windows zip manifest 保持 v1、ACL 修复保持现状。Unix 安装事务在平台策略处分派，Windows service/update 保持现状。本设计不将 Unix root-input 新策略悄悄应用到 Windows 或非 root 私有实例。

TUI 的 logging 配置继续来自 GET /v1/logging/session 同步，离线启动沿用 debug/100 MiB/10 文件 bootstrap，在线使用 daemon 发布值。既有 LoggingStatus.dir 在 Unix 表示机器日志目录 D/logs，不能改成 U；TUI 显示“机器日志目录”和“本用户日志目录”两行，默认导出目录独立显示。Windows 保留当前单目录展示。

## 5. Unix 文件能力与权限算法

| 系统模式对象 | owner / mode |
| --- | --- |
| B | root / 0711 |
| C、channel | root / 0644 |
| E | root / 0666；仍验证 token |
| install.lock、daemon.lock、endpoint lock、journal、备份 | root / 0600 |
| D、staging、private 子目录 | root / 0700 |
| 普通业务、日志、zip、锁、临时文件 | root / 0600 |
| D/bin/mihomo | root / 0700 |
| I / I/mihari | root / 0755 |
| U 与子目录 / 文件 | 当前 UID / 0700、0600 |

私有 P 模式统一 owner=daemon UID，P及子目录0700、C/channel/锁0600、E0600、核心0700；root P 无普通用户读写权限。显式外置 C/E 的 parent 必须属于该模式 owner、其他 UID 无写/删除子项能力；默认共享 C 即使外置仍0644、E0666。不存在外置父目录则 daemon 拒绝，不替用户创建未知目录。读取 C 必须为 regular、nlink=1、owner正确、权限不比该模式更宽、大小恰为合法64 hex加可选换行；不跟随 symlink。

威胁模型：root（及能改变系统挂载/服务定义的管理员）可信；普通 UID、订阅源、下载 staging、应用树内路径不可信。同 UID 可读写自己的 U，不能要求防御自身 UID。root 安装器不能将“所有本机用户可控制代理”等同于“允许其修改特权文件”。

能力获取：
1. 从 / 开始逐段目录句柄打开，组件使用 O_DIRECTORY|O_NOFOLLOW|O_CLOEXEC，最终普通文件 O_NOFOLLOW|O_NONBLOCK 后 Fstat 验证再清除 NONBLOCK。拒绝 .、.. 和不合法组件；记录 dev/ino、owner/type/link count。创建用 mkdirat 或 O_CREAT|O_EXCL，失败不接管未知现存对象。
2. 系统祖先要求 root owner、group/other 无写、ACL 不赋予非 root write/delete/add_child/delete_child/chown/write-security。不修改祖先。Linux access ACL 若有，仅允许能证明不增加写权限的 POSIX ACL（含 mask 有效权限）；Darwin解析 fgetattrlist 的 ACL ACE，仅接受非 root 无上述写权限的项。无法解析即 permission_denied。只读/遍历授权不导致拒绝系统祖先。Darwin祖先的任何ALLOW写入/删除/改权限ACE均保守拒绝，不必解析GUID是否root；DENY以及仅读/遍历ACE可接受。创建目标的父目录另须通过creation ACL规则。
3. B/D/I/U 等应用自有目录采用更严格规则：无扩展 ACL；Linux同时清除新建对象继承的 access/default ACL，再 fchmod/fchown 和读取复核；Darwin用 fchmod_extended 的 fd ACL 移除入口清除继承 ACL后复核。只修复身份和 owner 已匹配的应用对象。既有异常非 owner 对象拒绝，不 chown 接管。
4. Linux Fstatfs 要求本地 ext4/ext2/ext3（同 magic）、xfs、btrfs、tmpfs、ramfs；Darwin要求 local APFS/HFS且未忽略owner。该名单约束新的特权文件操作和迁移；未知/FUSE/network FS返回 permission_denied（安全语义不能证明），不是新增整个程序最低OS版本。普通 U 无法满足则内存诊断，其他功能继续。
5. 系统祖先允许合法 OS 挂载边界；到选定 B/D/I/P 或迁移 source 锚点后拒绝嵌套挂载。Linux首选 openat2 RESOLVE_BENEATH|NO_SYMLINKS|NO_MAGICLINKS|NO_XDEV；不支持时逐组件openat并用fd的 statx MNT_ID核对，回退只读受信任procfs的 /proc/self/fdinfo/<fd> mnt_id；都不可用则拒绝该特权操作。Darwin逐组件openat+Fstat/Fstatfs核对dev/fsid、local APFS/HFS；其他挂载类型拒绝。所有后续操作使用已打开fd，不重新跨路径解析。
6. Darwin /var、/tmp、/etc 仅接受 root-owned、不可被非 root 替换且目标分别为 /private/var、/private/tmp、/private/etc 的 OS 别名；/Library 与 /usr/local 不接受任意 symlink。别名后的安全祖先与挂载检查仍执行。显式用户路径中别名同样有限允许，应用层 symlink拒绝。
7. 修改/发布前用 fstatat(AT_SYMLINK_NOFOLLOW) 验证名字仍指向持有 identity；regular files nlink必须1。修改已验证对象用fd；原子替换采用同父目录临时文件、fsync、no-replace/受控replace、父目录fsync。通过 dev/ino 和父句柄比较重叠，不只比较字符串。
8. U：非root用实际euid绑定身份；HOME只有为绝对、祖先无其他非root可写、最终home owner=euid时才是候选，否则用系统UID查找返回的home并同样验证。Linux绝对XDG_STATE_HOME作为路径候选，验证相同规则；相对值忽略。root忽略HOME/SUDO_USER/XDG，Linux固定/root，Darwin固定/var/root。仅在可信home或用户owner的XDG锚点下逐级创建缺失父目录，不chmod现有用户祖先。系统UID查找失败不猜测用户名，内存诊断。

**creation parent检查独立于祖先穿越**：凡要新建regular/socket/lock/临时目录的最终父目录，Linux必须无access/default ACL，Darwin必须无任何ACL（含inherit-only ACE）；覆盖外置C/E、I、binary-only staging父目录。外置父目录有ACL则拒绝，不自动清ACL，可显式选私有无ACL应用目录。创建B/D/U时同样要求creation parent，不能先创建再靠chmod撤销已打开fd。

通过creation检查后，regular文件创建0600，C/channel/lock/journal/备份/binary写入前验证owner/type/nlink/无扩展ACL，写后sync，按最终mode调整复核后原子发布并同步父目录。出现不符合父目录检查的继承ACL立即失败、不写敏感数据、不修复后继续使用。应用目录已有ACL修复只针对可证明没有旧写fd影响的维护流程；否则在安全私有staging新建inode再替换，不原地可信化。公开C由daemon执行；根0711不会被日志capability改回0700。用户自选export目标保持已有PublishDir namespace/cleanup合同，不使用root创建能力。

实现依据：当前 x/sys v0.46.0 提供 Openat2/Statx/Fstatfs、Darwin fd syscall wrappers；[Go Sys](https://github.com/golang/sys/tree/v0.46.0/unix)；Linux mount crossing语义见[内核路径解析](https://www.kernel.org/doc/html/v6.2/filesystems/path-lookup.html)。Darwin ACL ABI需由平台测试验证，不能用Windows编译代替。

## 6. 锁域、发现与 credential 生命周期

固定获取顺序：默认B全局服务install.lock → 私有P的install.lock（仅私有系统服务）→ daemon数据锁 → endpoint锁。所有同名系统service的apply/recover/start/stop/reinstall/uninstall都先取B全局锁；安装器停旧daemon后才等数据锁。普通daemon不取安装锁但检查journal；验证子进程例外见第10节。非root便携P不取B锁、不用root journal，直接P数据锁→endpoint锁；维护P可先取P/install.lock。无service binary-only只有binary父目录更新锁，不取B/P锁。

数据锁为模式根内固定daemon.lock的flock EX|NB，句柄保持到完全退出，永不unlink锁文件。P相同、E不同也互斥。endpoint锁位于E同父目录、名称为固定前缀加socket basename的SHA256，锁文件owner/mode/link检查同上；规范化父目录identity+basename决定同一锁。不同D同E必须争同一endpoint锁。锁对象取得前后复查名字身份，发现替换失败退出。禁止绑定与系统默认E相同的私有endpoint覆盖。

持两个锁后才处理E。若E存在，类型/owner正确后做500ms连接探测：连接成功或超时/权限/未知错误均视为占用，返回invalid_state；只有ECONNREFUSED允许复核inode后移除。即使对方是无锁旧daemon也不删除live socket。默认客户端验证E受信任父目录，并在连接后核验服务端peer euid=root；私有模式为P owner。Linux SO_PEERCRED、Darwin LOCAL_PEERCRED，无CGO。失败关闭连接，不发送token。daemon关闭时禁用标准listener的无条件unlink，只有当前E身份仍为本实例时才清理。

socket绑定期间parent是不可被非owner替换的路径；其长度检查见第3节。恶意root不在威胁模型。同UID便携实例信任同UID，但仍执行双锁避免误删。

token只有daemon LoadOrCreate；缺失生成，损坏不覆盖。C从不迁移旧token。轮换合同限定为**停daemon后由管理员移除旧C，再启动由daemon生成**；不新增在线轮换接口。运行时外部改写C属于不支持操作，server继续用内存旧token；客户端收到permission_denied并提示重启服务，不反复尝试旧token。协议不承诺双token接受窗口。

每个Unix请求（包括status/普通REST/WebSocket重连）调用provider，只读一次完整C，错误不返回之前缓存值；成功值用于该次认证及客户端redactor，进程redactor保留该进程用过的token以免旧值出现在导出。缺失后新daemon启动无需重启TUI。流重连使用新provider结果；禁止自动重放mutation。

统一错误边界在control/client：C/E缺失或拒绝连接→daemon_unavailable（退出3）；访问/peer/token拒绝→permission_denied（5）；C内容损坏→data_failure（9）；非法路径→invalid_argument（2）；锁冲突→invalid_state（4）；其他IO→data_failure。HTTP现有ErrorEnvelope原样保留。cli/status、runtime及TUI session不得把这些APIError重新包成daemon_unavailable。error文本仅操作名，不带token/完整URL。

help/version纯解析不打开B/D/U；service status只读管理器；channel查询只读sidecar，缺失仍main；channel写由root安装用例原子写默认B，私有模式由P owner写。客户端logging启动失败不阻塞控制连接。安全根/锁/credential失败则daemon不启动mihomo；后续业务配置错误允许现有degraded控制面，但不得执行无效核心配置。

## 7. 代理管理权限与 root 输入策略

| 能力 | 所有认证本机控制用户 | root本地管理器 |
| --- | --- | --- |
| 订阅、代理选择、TUN/系统代理、端口、核心/GeoIP/面板更新 | 现有受管操作允许；仍走mutation/revision、安全输入检查 | 相同控制能力 |
| 连接/节点/流量元数据、脱敏机器日志 | 允许；不提供每用户机器数据隔离 | 允许 |
| 任意路径读取/写入、指定任意可执行文件、任意shell、任意监听 | 不允许 | 不通过控制API增加这些操作 |
| OS服务注册、应用自更新、迁移、channel修改 | 不允许通过共享token | 本地提权命令，固定事务入口 |
| 其他UID的TUI日志 | 不允许 | root固有OS权限，不经快照协议代读 |

新增系统模式 `RootConfigPolicy`，在在线新增/刷新/切换、设置变更、迁移和启动读取最后有效配置时统一执行；Manager以候选校验→原子提交→reload→失败回滚接线。不能只在迁移时检查或只依赖mihomo -t。该策略是本次共享root控制安全边界的一部分。

策略以**显式字段语义注册表**构建输出，不把未知map复制给root核心：
- 系统监听/controller/UI/进程：只生成Mihari托管loopback mixed/controller、secret；拒绝用户external-controller-unix/pipe/tls、listeners、redir/tproxy/socks/port、external-ui*、脚本/插件执行或外部可执行路径；未知同类字段拒绝。TUN只用Manager生成的已知typed字段，订阅中的tun不生效。DNS如启用只能loopback监听，用户输入不能扩大监听面。
- 网络纯数据：proxies/proxy-groups/rules/rule-providers/proxy-providers/dns/sniffer/hosts/ipv6/mode/log-level/连接策略仅接受该核心版本注册的typed字段；协议类型的嵌套字段同样注册，未知key/type拒绝，不以危险词黑名单当安全证明。规则按现有顺序保留，节点名称/密码作为数据，不用于文件名或命令参数。
- 文件能力：provider缓存路径由系统根据对象ID生成到D/runtime/core-home/providers下，忽略用户提供的相对缓存建议但拒绝绝对/..；file provider只能引用已经验证并复制入该私有资源区的对象ID。证书/私钥只接受inline内容，不接受从本机路径加载。GeoIP/GeoSite、profile store、core工作目录由Manager固定，不能引用B/control.token或D其他业务文件。provider内容也必须经过同一typed策略，不能让mihomo直接下载未验证的provider再绕过策略；由Mihari获取、验证并原子更新固定本地provider资源。
- 子进程环境用固定allowlist构造，删除SAFE_PATHS、SKIP_SAFE_PATH_CHECK、LD_*/DYLD_*和调用者注入的配置变量；-d指向专门D/runtime/core-home，-f指向生成候选；需要的受管资源复制入core-home。校验进程和正式核心使用同一环境和工作根，不执行旧用户树binary。
- RootConfigPolicy初始核心语义固定v1.19.30。兼容表为Mihari编译内置的版本化资源，含policy_id/core tag/channel/OS/arch/压缩制品SHA256；初始四个Unix条目来自基线scripts/release/release-inputs.lock.json。发布服务器或候选自报的标识不能修改此表。stable/alpha更新仅命中内置条目才允许；初版alpha无条目时invalid_state且保留旧核心，不自动降级stable。未注册版本/字段返回invalid_state（不支持核心策略）或data_failure（配置不能安全处理），旧有效配置/核心继续运行。不能把未来latest/alpha自动当安全兼容。不支持字段须反馈安全字段路径，不打印其值。
- 迁移先导入typed业务数据，不自动启动未通过策略的订阅。若被拒，事务不得静默删除或降级原订阅；整体迁移失败并保留旧服务/树。root可修订配置后重试。普通私有非root模式及Windows维持已有行为。

provider最小生命周期（仅服务于root输入边界）：
- 身份=(subscription ID,generation,provider kind,name)，源定义仍在subscription cache；对外名称保留，文件名为完整身份的SHA256。支持proxy provider的http/file/inline YAML，rule provider的http/file/inline YAML或纯文本（domain/ipcidr/classical）；MRS/未知编码初版data_failure，不交核心代解析。最多256 providers，单源16MiB、合计256MiB，HTTP最多5跳/30秒，复用订阅direct/proxy/auto；URL保密。
- 下载在D/staging/providers/<random>/，验证后生成D/runtime/core-home/providers/<id>.yaml或.txt；核心配置仅固定相对路径、type=file，无url/native自动刷新。inline转文件，file只引用已验证资源对象ID。GeoIP/profile/cert/provider均复制/生成到core-home，D其余路径不进SAFE_PATHS，不保留第二份provider-cache别名。
- scheduler由daemon context拥有；首次激活前准备全部provider，失败不激活。http interval正秒数、默认3600秒、最小60秒，非法值显式data_failure。重启从当前合法缓存重建调度；离线有合法文件继续用，缺必要文件不能启动相关订阅；后台失败留旧缓存、报告错误、interval后重试。
- 下载/解析/RootConfigPolicy在mutation锁外；提交重查active subscription ID/generation和provider旧hash，变化则revision_conflict并丢候选。coordinator内保留同父旧文件→原子替换→通知核心reload本地provider，失败还原文件并reload旧资源，双失败标degraded并停止核心，磁盘保留最后有效版。provider commit journal schema=mihari.provider-commit/v1（id、旧/新hash、phase、旧备份引用），位于D/staging/providers，root0600；替换前durable intent，成功reload后durable done。启动先恢复未完成替换为旧文件再加载核心；不信半提交cache。
- 现有Manager.UpdateRuleProvider、POST /v1/rule-providers/{name}/update及已允许Web同类mutation改调同一RefreshProvider，保留operation ID/IfRevision/响应语义。未注册name为invalid_argument；禁止继续直接让controller下载。新增/刷新订阅、手动刷新、scheduler走同一路径，退出先等worker再关FS。
- 核心安装先验证内置压缩asset hash，在root staging解压并记binary hash，随binary提交root0600 provenance receipt，schema=mihari.core-provenance/v1，含policy_id/asset_sha256/binary_sha256/os/arch/tag。启动重hash binary并核对内置表和receipt；旧用户树receipt不可信、不迁移，须从可信制品重建。receipt+binary采用同一写前journal，缺失/不匹配不执行核心。

迁移对引用的旧file-provider资源做handle与16MiB验证，导入私有staging生成完整provider图；不能忽略旧runtime而丢活动订阅资源。新布局升级retain core-home/providers及provenance；初迁缺必要资源整体失败。provider journal先于核心恢复，无额外索引数据库。这些provider journal/provenance均属于本设计新增持久化合同，必须随最终spec批准。

这会限制高级配置和未经审计的新核心版本，是明确的兼容成本。支持表必须作为实现时的版本化资源与每字段安全fixture一同落地，不能新增“未知字段放行”开关。此设计不声称修复mihomo本身的任意实现漏洞；可信、受支持的核心及OS仍是信任前提。上游[provider路径规范](https://wiki.metacubex.one/en/config/proxy-providers/)的SAFE_PATHS仅是纵深约束，不替代输入策略。

## 8. 机器快照 wire 与导出

新增可选capability `machine-log-snapshot-v1`，仅Unix系统模式通告。endpoint为 `POST /v1/logging/snapshot`，只读快照，不进入业务mutation锁，使用专用资源配额；Windows/旧daemon不通告，现有GET/PATCH /v1/logging DTO不变。

请求：application/json，最多4KiB，未知字段/重复key拒绝。
`{"schema":"mihari.machine-log-request/v1","from":"RFC3339Nano UTC","to":"RFC3339Nano UTC"}`；from可省略表示无起点，to必填；from<=to，to不晚于服务端当前时刻5分钟；不接受相对时间/路径/UID/source选择。TUI点击Export时固定now及本地时区，将24h/60min/between/all归一为同一窗口供本地与机器来源使用。all也以to为上限，避免导出启动后追加记录。

成功HTTP200 application/x-ndjson，由以下有序帧组成，每帧均schema=mihari.machine-log-stream/v1：
1. header：snapshot_id（随机128bit hex）、from（可省略）、to、sources固定["daemon","mihomo"]。
2. record：source、payload_b64（RFC4648标准base64，有padding、无空白）、redacted布尔。payload解码是发送方一次生成的UTF-8合法JSON对象、不带换行；按daemon再mihomo，每源归档旧→新、文件内顺序。记录已服务端脱敏且在窗口内。
3. source_end：source、lines、skipped_invalid、redacted、sources（固定文件basename及.1… .9）、sha256（每条payload解码原字节加单LF拼接的SHA256，小写hex）、bytes（该拼接字节数）。空源也必须有source_end，禁止把permission/IO错误当空源。
4. complete：snapshot_id、source_count=2、total_bytes。每帧必有type=header|record|source_end|complete|error。complete为最后一帧、以LF结束。客户端先按payload原字节验证hash，再二次脱敏/重编码；source_end.lines为record帧数、skipped_invalid为服务端丢弃行数、redacted为true帧数，source_count为source_end帧数。complete验证后须于5秒内读到正常HTTP body EOF才发布；complete前EOF、之后任何额外字节/空白行、计数/hash错误或超时均data_failure。
失败：HTTP头前按现有ErrorEnvelope，invalid_argument=400、permission_denied=403、invalid_state（忙/预算）=409、data_failure=500。流开始后发送error帧（现有APIError嵌入，随后关闭，无complete）；无法写error直接断连。客户端error、complete前EOF、非正常EOF或预算失败删除spool，绝不发布半成品。

限额固定：每记录原行和payload解码字节各1MiB；每帧含LF的wire上限2MiB，base64最多4*ceil(1MiB/3)，其余为字段预算；每源最多10文件，机器最多20；扫描总量2GiB、输出1GiB，超限失败不截断；同时最多2个机器快照、全局每分钟最多6次接纳（无等待队列，第7次invalid_state）。请求总时限120秒、单次流写deadline5秒、源锁等待2秒；客户端流读空闲10秒/总125秒，独立流client不复用现有10秒普通HTTP timeout。仅保留至多一个frame缓冲，不用无界channel；断连取消context。普通本地TUI来源最多10文件/1GiB，合并spool总上限2GiB。CPU解析每行检查ctx；regular本地文件读取分32KiB批次检查，磁盘不可用报错。

快照一致性是每来源的有限前缀，不承诺三个来源同一全局时刻。锁下打开带identity的fd、记录长度及文件列表，然后解锁读取固定长度；后续append不进入，rotation unlink不改变持有fd。若原inode被截短导致提前EOF，失败而非当完整。服务端redactor由daemon维护的token/controller/web credential及所有当前订阅URL的秘密集合建立，读取期间保留启动时快照并合并新出现秘密；不得把秘密集合发客户端。客户端用通用递归脱敏+自身token对记录再次处理。

新增Unix导出manifest schema `mihari-logs-export/v2`，保留v1所有已有字段含义并加 `scope:"machine_and_current_user"|"current_user_only"`、`source_status:{daemon:collected|not_requested,mihomo:...,tui:collected|unavailable}`。允许entry名仅manifest.json、daemon/mihari-daemon.log、tui/mihari-tui.log、mihomo/mihomo.log；manifest必有，日志仅lines>0时创建，files与非空日志entry一一对应，不创建零字节日志entry，collected且零条与not_requested可区分；全零条沿用ErrNoLogLines。notes保留敏感元数据提示，并在TUI-only写机器来源未请求。损坏行按既有skipped_invalid统计；redacted为至少一阶段改变的记录数，不能把两阶段计数简单相加。客户端按record.redacted与本地修改的OR计数。

完整wire fixture，每行LF，最后正常EOF：
```jsonl
{"schema":"mihari.machine-log-stream/v1","type":"header","snapshot_id":"00000000000000000000000000000001","to":"2026-09-05T00:00:00Z","sources":["daemon","mihomo"]}
{"schema":"mihari.machine-log-stream/v1","type":"record","source":"daemon","payload_b64":"eyJ0aW1lIjoiMjAyNi0wOS0wNVQwMDowMDowMFoiLCJtc2ciOiLoioLngrkiLCJuIjoxZTB9","redacted":false}
{"schema":"mihari.machine-log-stream/v1","type":"source_end","source":"daemon","lines":1,"skipped_invalid":0,"redacted":0,"sources":["mihari-daemon.log"],"sha256":"1625f1821f85ab2dc68c7da55c4fbe769637b7752174c7d5be8a83cd8d388a48","bytes":55}
{"schema":"mihari.machine-log-stream/v1","type":"source_end","source":"mihomo","lines":0,"skipped_invalid":0,"redacted":0,"sources":[],"sha256":"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855","bytes":0}
{"schema":"mihari.machine-log-stream/v1","type":"complete","snapshot_id":"00000000000000000000000000000001","source_count":2,"total_bytes":55}
```
payload解码为`{"time":"2026-09-05T00:00:00Z","msg":"节点","n":1e0}`，原字节54加LF共55。mihomo已采集为空，manifest为collected但不创建entry；本地TUI非空时另加其entry。hash不依赖接收端key排序/数字/Unicode重编码。

默认完整导出必须有本地TUI存储和机器能力；缺失/离线时可由用户显式选择current_user_only，绝不自动降级。显式私有P模式和Windows沿用本地三来源v1导出，不调用机器快照。所有用户最终目标用现有PublishDir/Workspace持有父目录身份、临时0600、sync、no-replace发布；目标不能在本地日志树内，Unix系统默认D本来也不可访问。daemon不接收目标路径。保留既有cleanup warning语义，发布后目录被外部重命名时显示路径可能过期。

## 9. 安装用例与平台服务适配器

新增 `internal/app.InstallTransaction`，service install/reinstall、Unix self update及所有sh安装入口调用同一用例。平台接口为 InspectDefinition、DisableAutostartAndStop、WaitOwnedTreeExit、WriteDefinition、RestoreDefinition、Start、Probe；保存定义原始字节、绝对binary/args/env、owner/mode、enabled/running，不通过字符串拼接执行shell。

新增root CLI `mihari service apply --request <absolute-json-file> [--json]`。输入上限64KiB，schema=mihari.install-request/v1，字段：
operation=install|reinstall|update|recover；binary（绝对候选路径）；bundle（可省略AIO绝对路径）；source（可省略旧业务树）；channel=main|dev；layout=system|private；data（private必填）；endpoint/credential/install_root/path_binary（可省略绝对覆盖）；release_tag、artifact_sha256、bundle_sha256（有bundle必填）。未知/重复字段拒绝。recover只允许operation/schema，不接受外部source/候选路径，读取受信任journal。
成功JSON为 `{"schema":"mihari.install-result/v1","changed":true,"service_status":"running|stopped|not_installed","transaction_id":"hex","source_retained":true|false}`，文本输出同一状态；失败既有ErrorEnvelope/退出码，绝不输出原env内容。root授权由进程euid检查，不由token或request声明。

请求中的hash不是信任根。root进程将候选以no-follow fd打开、检查regular/nlink和大小，在私有staging复制并重新hash；通过官方TLS发布源独立获取固定tag/asset checksum，AIO通过发行包的受信任锁定清单核对，或使用root预置只读离线checksum清单。下载文件旁的用户可写checksum不得授权执行。binary上限256MiB、bundle压缩1GiB/展开2GiB/10000文件；解压拒绝链接、设备、重复、绝对路径、穿越，提权后永不直接执行用户staging中的程序。下载Only不调用apply，不写系统数据/channel/service，下载产物由用户拥有。root直接运行用户提供的安装器本身属于用户显式信任的代码，不声称可防御已被执行的恶意installer。

operation=update且Inspect确认无服务走binary-only：不读写B/D/C/channel/journal，仅在受信任binary父目录取得专属更新锁，创建同目录私有staging、验证制品、单次原子rename替换唯一当前binary并同步父目录。替换前失败旧binary保持，替换后新binary权威且Updated=true；cleanup失败不伪报未更新。崩溃只遗留未发布candidate，下次仅清理身份匹配的本工具staging，无跨对象journal。禁止bundle/source/data/path_binary及改变channel字段，不能借此注册service；普通self update继承发布channel只作下载选择。

Linux adapter支持systemd：读取系统unit和相关drop-in原始字节；每步写前journal后移除/记录enable链接，将实际/etc/systemd/system/mihari.service安全原子替换成symlink→/dev/null（明确的服务mask特例，不属于数据树链接）；daemon-reload后核验LoadState=masked、实际FragmentPath。原unit/drop-in/mask/enable各自备份并记录identity，不用/run runtime mask代替/etc高优先级unit。mask确认后stop，确认unit inactive、MainPID=0及其cgroup无残留；限30秒，随后只对已验证该unit cgroup发TERM，5秒后KILL，再5秒仍非空则失败。保留原enable和mask状态，已有陌生ExecStart/多个命令/不支持drop-in拒绝变更，不能近似重建。
macOS adapter支持launchd system域：保存/校验root plist（非用户LaunchAgent）、label、Program/Arguments/Environment及disabled状态。在bootout前先写durable disable intent，执行持久disable system/<label>并查询核验，再bootout、确认job卸载、等待记录的daemon/mihomo身份退出。禁用状态必须跨reboot保存；disable失败不得进入复制。PID以启动identity重查，TERM5秒/KILL5秒仍存活则失败，不按裸PID或名称杀进程；无法证明旧进程归属/退出拒绝迁移。plist发布/备份、disabled变更、bootout/bootstrap分别进入actions。验证子进程由installer直接启动，实际service在activation前持续disabled且unloaded。中途重启后root recover重新核验disabled和journal，收敛之前不放行旧job。

definition_committed只保存私有候选服务定义。activation前回滚使用旧definition/running/enabled；activation后只使用target对应值。Linux激活后替换mask为新unit、daemon-reload，按目标enable/running恢复。macOS状态分支如下；启用与当前运行不是同一字段：

| 目标状态 | macOS恢复/激活动作 |
| --- | --- |
| stopped + enabled | 原子发布plist，恢复enabled，保持job unloaded，**不bootstrap**；本次保持stopped，下次系统启动或显式service start可加载 |
| stopped + disabled | 发布plist，保留持久disabled，保持unloaded，不bootstrap |
| running + enabled | 发布plist，enable并核验，再bootstrap system域，确认进程identity/版本/就绪 |
| running + disabled | 发布plist，写前记录受控enable→bootstrap→恢复持久disabled，核验job仍running且disabled已恢复；无法恢复或查询证明则报invalid_state，不伪报完成 |

macOS Inspect以可信已安装plist结合system job状态判断：plist存在但job unloaded为stopped，plist缺失且无受管job为not_installed；未知或冲突状态invalid_state。运行+disabled的恢复步骤若中途失败，activation前按旧状态恢复，activation后保留target journal继续修复，不能回旧树。service install只注册、原stopped升级均走表中stopped分支，不能bootstrap后再stop来模拟“未正式运行”。从不使用gui域。
管理器工具不可用或状态未知→invalid_state，停止任何提交；不自动降级到其他service manager。OS工具通过绝对受信任路径调用。

服务安装/重装的既有CLI行为保留：install注册但不自动启动；reinstall注册并启动；apply install用于sh安装时启动；self update有服务时保持原running/stopped和enabled状态，无服务只更新已验证当前binary且不造B/D/token。service start执行journal恢复检查。stop/uninstall不迁移；uninstall保留数据且要先终止受管进程。

PATH binary与I/mihari可能相同，按inode/规范路径去重；不同则分别保留备份，拒绝用户可写PATH安装祖先。顺序为journal备份完成→停机→D提交→I/mihari替换→PATH binary替换→channel替换→服务定义→启动/激活。每次替换前复核identity并同步父目录；失败按第10节恢复。当前执行binary在Unix可被原子替换，旧进程继续运行；TUI先结束export/session/logging句柄后进入用例，完成后从已验证新binary relaunch。更新发生前的下载可在TUI仍运行时准备。Unix updater新增Prepare阶段，不再使用AfterReplace作为事务入口；Windows保持现有流。

## 10. journal、激活与恢复状态机

journal为B/install-transaction.json（private系统服务使用默认B事务目录，以data_root字段指向P），schema=mihari.install-transaction/v1，root0600。字段：transaction_id、operation、phase、mode、canonical source/target/install/endpoint/credential路径、boot_id及同boot的dev/ino/mount身份、候选和备份hash、data_action=create|retain、服务定义备份引用、old_running/old_enabled、recovery_authority=source|target、created_at。备份在B/transactions/<id>/0700，同模式regular0600；env只存在私有备份，不进日志。单journal最多1MiB，未知schema/损坏拒绝恢复，不从路径字符串盲写。同boot比较dev/ino/mount；跨boot的mount ID/dev不作为持久等值判据，重新从可信祖先获得对象，验证type/owner/ACL、root私有事务标识和旧/候选内容hash。随机事务目录内root0600标识必须匹配journal；对象无法与旧值/候选唯一对应则拒绝。source只保留不删，不根据不可信source名字恢复root文件。

所有journal更新：O_EXCL临时写→fsync→原子替换→父目录fsync。新增actions数组：seq、kind、target_role、old_state/backup_ref、new_state/candidate_ref、status=intent|done。每个外部动作先durable intent→执行并同步→durable done；覆盖disable/mask/stop、D发布、两个binary、channel、定义/drop-in/enable/disabled、验证进程启动停止和activation，恢复的逆向动作也写intent/done。phase仅分组摘要，不证明下一动作未发生。prepared以后可能已部分mask/stop，恢复必须检查全部actions；实际状态等old表示尚未完成，等new表示已完成，未知则停止。进程identity含boot_id+启动时间，跨boot不杀旧PID。跨目录替换同步两父目录后记done。

安装锁由最外层公开入口acquire一次，传入OwnedInstallLease给内部RecoverLocked/ApplyLocked/StartLocked；内部只借用，不再flock或调用会重入的公开service方法，lease只有外层释放。普通daemon见pending不取安装锁、不写数据，返回需要恢复；此处pending严格指prepared到definition_committed。activation_committed/complete允许目标daemon启动，残余定义/enable actions由root恢复者收敛到target，绝不回旧树。service-start包装入口用同一lease先recover再启动，其他installer持锁立即报忙。验证子进程按下面的私有租约进入，绕开普通pending分支但没有任意用户bypass。

| phase（持久化后） | 允许的状态与崩溃恢复 |
| --- | --- |
| prepared | 候选/备份完整，来源权威；actions可能已有部分mask/stop，逆序恢复所有intent涉及的原状态再清理候选 |
| stopped | 旧服务被禁启并已停；来源权威。恢复定义/启用/运行状态 |
| data_committed | data_action=create的新D已发布，仍来源权威；失败隔离新D并恢复来源。data_action=retain的同布局升级绝不移动/回滚现有D |
| binaries_committed | 受管及PATH binary、channel已替换；仍可按备份逐项回滚 |
| definition_committed | 私有候选定义就绪，实际service仍masked/bootout；installer直接启动验证，仍可回滚 |
| activation_committed | **先持久化target权威，再允许新daemon首个业务mutation/自动刷新/核心启动**；此后任何失败只修复target，不自动回旧树 |
| complete | 新服务符合预期running/stopped、所有记录一致；保留来源标记和最小完成journal，清理已验证临时备份 |

内部启动形式`daemon --install-validation <transaction_id>`必须euid=root、继承installer匿名双向pipe租约、握手nonce hash匹配root journal的validation intent、父启动identity和候选binary hash匹配。普通用户仅知道transaction_id无法进入；未带租约必拒绝。installer持全局/P安装锁，释放自身数据/endpoint锁再启动子进程；子进程只取数据→endpoint锁、不recover/不重拿安装锁。pipe EOF立即取消退出；ready.json后installer经pipe请求退出并等待锁释放。验证进程崩溃/父失联按启动identity由下一recover清理。

验证模式daemon可仅创建新credential、验证布局/settings/catalog、打开日志及IPC，所有外部写请求/后台写任务/核心启动被gate拒绝。IPC Ready≠业务健康≠允许mutation。installer通过私有B/transactions/<id>/ready.json（root0600，含transaction_id、daemon进程身份、binaryhash、layout identity、validation=ok|failed）确认验证结果；一般status响应不用于授权激活。无core、未onboarding是合法validation=ok/setup_required，不当失败。credential缺失可由验证daemon创建，配置损坏/策略拒绝为failed。

installer收到匹配ready后停止验证daemon并等待释放数据/endpoint锁，原子写activation_committed，再正常启动新daemon。正常daemon启动读取持久activation记录才释放业务gate；默认root前台绿装先取B/install.lock、识别旧service/source及pending journal，确认无迁移后才创建D、执行无业务验证和activation，再释放安装锁进入正常daemon；有source须走迁移，不先EnsureDirs(D)。普通已安装daemon只检查activation/complete不创建事务。root私有P非service前台用P内journal/锁走同一bootstrap；非root便携P保留旧单根初始化、不用root activation。写activation后启动失败仍target权威，报告invalid_state，可recover重启target，不回旧树。stopped服务升级也必须通过一次无业务验证进程，写activation后恢复stopped不正式启动。service install只注册场景同样验证并激活后保持stopped。

已有新布局的普通版本升级不复制/替换D，只在停止业务后验证现有D及新binary。回滚binary不得写旧格式到D；本设计不改变settings/catalog schema。布局或格式不兼容的未来版本须新迁移方案，不复用本事务假装可逆。完整完成journal阻止再次导入旧source；旧source只记在root journal，绝不在用户树写“已迁移”标记。

## 11. 迁移来源、清单和静止性

首次迁移只接受不存在的新D；B可能已有bootstrap空目录。已有受管理新布局只做升级；有未知数据且无匹配journal的D→invalid_state；有pending journal先recover；source=target或任一嵌套关系→invalid_argument。比较canonical路径及目录identity，防路径别名。总扫描上限10000文件/2GiB、深度16；业务候选上限256MiB；超限不部分成功。未知顶层对象不自动删，报告不支持来源而停止。

| 旧Paths/对象 | 策略 |
| --- | --- |
| mihari.yaml | <=1MiB，typed验证、RootConfigPolicy；生成新controller secret，保留合法settings含logging |
| subscriptions/catalog.yaml | <=1MiB，ID唯一/引用有效；URL仅私有存储；保留enabled/interval/channel语义 |
| subscriptions/cache | 每文档<=16MiB、合计<=256MiB；仅catalog实际引用代际，解析/策略验证；缺失活动cache使迁移失败，不能悄悄清ActiveID |
| runtime/config.yaml | 不复制，从通过策略的catalog/cache/settings重新生成 |
| onboarding.json | <=64KiB，验证后由daemon按实际资源重算完成度，不能声称丢失资源仍ready |
| preferences/tui.json | <=1MiB，既有typed校验，仍daemon管理 |
| mihari-channel / bin/core-channel | 应用channel仅main/dev；core-channel仅合法channel+stamp；按受信任新制品重建stamp，不信旧sidecar证明binary |
| control.token、web/credential | 不迁移；daemon重建，旧客户端需新credential |
| bin/mihomo、geoip、web面板资源 | 不直接信任旧字节；根据受信任发行清单/hash复制匹配制品或准备重下载；active面板引用须重建为已验证版本，不能指向缺失目录 |
| logs、logs-export、staging、锁、socket/fifo临时项 | 明确忽略且留在来源；不扫描其内容，不把继续写日志当业务不静止 |
| 其他symlink/hardlink/设备/嵌套mount | 若位于业务白名单及祖先则拒绝；不遍历忽略目录来“清理” |

资源重建在停机前准备，不能证明可信的binary/GeoIP/panel不提升权限。已有有效来源需要这些资源才能维持既有运行能力时，缺少重建资源就失败并保留原服务；不以迁移后“未安装”代替成功保留功能。绿装无资源仍合法setup_required。发布清单对现有来源未知版本不猜测，用户可明确选择升级到受支持制品后重试。

来源来自root服务定义固定MIHARI_DATA；无服务时必须显式source。执行以下顺序：预检来源及全部候选→禁自动重启→停止受管daemon/mihomo并确认identity退出→取得目标事务/数据能力→逐文件fd复制。旧TUI只可继续日志写；若其行为能写settings/catalog，迁移拒绝并要求退出该旧客户端。

无法阻止恶意来源owner写自身树；不声称stop+hash证明其可信或静止。读取每个regular fd前后核对identity,size,mtime/ctime、hash并重列业务集合，再对**已复制私有候选**做跨文件语义一致性验证；改变即失败。安全来自不执行旧binary、typed数据验证及候选隔离，即使恶意writer制造自洽内容也仅是它有权提供的代理配置，不能形成root文件能力。迁移保证一个通过验证的候选快照，不保证包含恶意writer所有并发更新；受管正常写者停止后的变化则视为冲突。

不自动杀任意旧手工daemon；无可信进程归属或活跃核心冲突时拒绝迁移并恢复旧服务。旧版本不认识新锁，因此兼容承诺限定受管升级，不保证任意手工旧binary与新默认daemon全机互斥。成功后旧日志/zip保留供人工取回，不纳入新导出的完整性定义；所有新业务写入仅target。

## 12. 资源关闭、错误与验证矩阵

TUI正常/失败/更新：停止新命令→取消export及session/config worker→强制关闭export HTTP响应/连接→等待worker（最多5秒网络终止，文件IO逐批可取消）→flush/close logging→关闭用户FS→relaunch。每个goroutine必须由Run等待，不能超时后遗弃仍用FS的worker；不可中断的内核磁盘IO只可报告卡住，不能承诺任意硬件故障下绝对有界。daemon先gate新请求、取消快照、http.Close终止慢连接，等待所有owned workers，再停止mihomo/capture、logging、listener、FS，最后释放endpoint/data锁。初始化失败只关闭已取得资源，禁止重复release转移的root。

| 测试层次 | 具体验收 |
| --- | --- |
| layout单元 | 全模式/覆盖/relative/路径长度；HOME/SUDO/XDG不影响默认B；NewPaths/Absolute旧语义；Windows原值 |
| 权限与身份 | root/两个普通UID的正负矩阵；ACL继承/写权限、hardlink、symlink、nested/bind mount、父路径替换、异常FS失败 |
| 锁/credential集成 | 同D异E/异D同E/live旧socket/陈旧socket/锁替换；仅清本socket；停机轮换、损坏/缺失恢复、peer拒绝、无mutation重放 |
| 代理配置安全 | 所有注册字段正例；未知字段/外部controller/文件provider逃逸/环境注入/恶意provider负例；迁移/刷新/切换同策略；不支持核心保持原有效版本 |
| 快照/导出 | 每帧顺序/hash/计数/限额/终帧/截断；两并发和速率；慢消费者/取消；空与未采集区分；v2与Windows v1；三来源非全局瞬时语义 |
| 安装/迁移 | 每个journal动作前后崩溃注入；activation前可回旧、后只target；正常/stop/no-service/update/AIO；来源并发日志与业务变化；资源和cache引用不丢 |
| 资源/兼容 | 初始化各步失败关闭一次；relaunch前无worker；GET/PATCH logging兼容、退出码2/3/4/5/9、Windows token/ACL/local export不变 |
| CI | 单元、internal/integration、race/vet；Linux/macOS真实mode/UID/ACL运行；Windows回归；六目标CGO_ENABLED=0编译 |

真实订阅/mihomo、实际服务安装、用户数据迁移不在本次设计任务授权内。平台安全测试使用临时目录和fake；需root/cgroup/mount的测试在隔离CI环境显式运行，默认go test不改主机服务。不新增依赖或Go版本。

## 13. 技术完成与产品批准

本稿给出推荐合同；技术审核必须检查全部章节，不将“待产品批准”当技术缺口的豁免。最终用户批准范围包括：A方向/平台路径及持久socket、全体本地控制权和机器元数据、RootConfigPolicy兼容影响、快照v1/zip v2、service apply/安装journal/provider journal/core provenance及安装FS限制、便携例外/旧版本边界。Astra通过后记录具体spec hash和审核报告，交付用户审阅；未获批准不写实施计划和生产代码。
