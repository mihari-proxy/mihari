# 架构说明

本文档记录 mihari 的运行时架构与关键机制。对外行为见 [README](../README.zh-CN.md),完整命令见 [commands.md](commands.md)。

## 控制面

Mihari 围绕一个由守护进程持有的控制面(control plane)设计,由 CLI、TUI 和浏览器面板共享:

- CLI、TUI 和浏览器面板通过本地命名管道 / Unix 域套接字连接同一守护进程控制面。
- 控制 API 从不绑定 TCP 端口。
- 控制面经过认证:令牌存储在数据根目录的 `control.token` 中。
- 守护进程可以安装、校验、托管、查询并重启 mihomo,同时将控制器保持在内环回。
- 守护进程还负责订阅持久化、有界的自动刷新、校验过的配置生成、重载回滚与离线配置切换。
- 控制面新增只读端点 `GET /v1/service/status`,返回 mihari 自身的 OS 服务注册状态(`running`/`stopped`/`not_installed`/`unknown`);`GET /v1/core` 增加可选 `localReady`/`localVersion` 字段反映本地 core 就绪。两者均为向后兼容增量,不改变现有协议字段、onboarding `Complete` 契约或持久化格式。
- `/v1` 的 `CoreStatus`、`CoreInstallResult` 增加可选 `channel`;`MutationRequest` 增加可选 `channel` 以显式指定本次安装通道。均为向后兼容增量。
- `GET /v1/logging` 与 `PATCH /v1/logging` 是稳定的 v1 本地控制协议：前者返回完整 Logging 状态，后者在 revision 预检后更新级别、单文件大小或保留数量。它们供 TUI 使用，不增加 CLI 命令。
- Windows 私有日志的授权主体优先为数据根的个人 owner SID。owner 为 Administrators 时，从既有 DACL 的显式个人用户 full-control ACE 解析主体，再写入个人用户与 LocalSystem 的受保护 DACL，避免未提权用户失去读取权限；多个用户、deny ACE 或无法解析的授权主体均拒绝猜测。旧 ACL 已丢失个人授权时，仅成功以 WRITE_DAC 打开数据根的交互进程可补回自身 SID，LocalSystem 不猜测桌面用户。日志写入器持有自身序列锁后，修复三个固定日志序列的当前文件、归档和锁文件，通过 no-follow handle 核对文件 identity，不改内容；其他序列轮转导致 identity 变化时有界重试。SYSTEM 尚不能确定用户时仅保留受保护的旧 BA/SYSTEM 根权限，不回写根目录。子项创建及加固重新读取根策略，并在应用后复查，避免服务缓存或并发迁移覆盖个人授权。
- daemon 装配失败但控制通道可 listen 时驻留降级控制面,`GET /v1/status` 的 `health` 为 `degraded`,并带可省略 `last_error`。
- OS 服务 `Start` 等待控制通道 Ready;listen 失败则向 SCM 返回错误,不得保持假 running。
- 托管端口预检失败时 details 可含占用 PID 与进程基名;不自动杀进程。

## Settings 提交与降级

- Settings 的单文件 replace 是提交点：replace 前失败时磁盘仍为旧文件；replace 成功后的目录 sync 失败仅作为已提交后的 durability warning，上报诊断但不回滚、不把已生效的 mutation 报为失败。
- onboarding、系统代理或 TUN 等需要补偿的 mutation，若补偿写在提交点前失败，daemon 会按已经提交的磁盘状态收敛内存、推进 revision，并将 health 标为 `degraded`。只读请求仍可用；后续 mutation 返回 `invalid_state`，必须重启后重新加载并重试。
- 该 degraded 边界不新增事务文件或持久化 schema。旧版二进制以 `KnownFields(true)` 严格解码 `mihari.yaml`，不能读取非默认 `log:` 块；降级前须在 System → Logging 恢复 `info` / 10 MiB / 3 份文件以自动移除该块，或备份后手动删除 `log:`。

## 核心安装

守护进程通过同一条下载、校验、替换链路安装 mihomo,并支持 `stable` 与 `alpha` 两个通道:

- `stable` 从 GitHub `/releases/latest` 取当前稳定版;`alpha` 从固定滚动 tag `Prerelease-Alpha` 取预览版。
- 展示与协议里的 Version 永远来自 `ParseVersion(mihomo -v)`:稳定版为 semver(如 `v1.19.x`),alpha 为 `alpha-{sha}`(实测 `-v` 形如 `Mihomo Meta alpha-dd7bc4c ...`,不是 `v1.19.x`)。GitHub tag `Prerelease-Alpha` 从不作为版本显示或写入 Version。
- 本次安装意图随请求进入安装链路;`settings.core-channel` 只在 Commit 成功后写入,表示上次成功安装的通道。切换失败或未提交时,持久化通道与界面仍为旧值。
- all-in-one 包在 `data/bin/core-channel` 写入 sidecar(第 1 行通道,第 2 行 stamp)。安装器覆盖 sidecar,不改 `mihari.yaml`;守护进程仅在 stamp 变化时把打包通道写入 settings,避免旧 sidecar 覆盖用户后来在 System 页切换的通道。

## TUI

- TUI 只通过 `internal/control/client` 经原生 IPC 控制面与本地守护进程通信。它从不打开 mihomo 控制器、从不接收控制器密钥。
- TUI 对数据根的唯一直接写入例外是经 `internal/logging` 追加/轮转固定的 `mihari-tui.log*` 序列;不得写 `mihari.yaml`、订阅、token、面板或其他业务状态。日志配置变更仍只走 daemon 控制面。
- 搜索与表单字段中的括号粘贴和 Ctrl+V 使用纯 Go 实现的 `github.com/atotto/clipboard` 辅助库;Mihari 本身从不把密钥写入剪贴板。
- 页面:独立的首次运行 Setup 路由、Overview、可展开的 Proxies、带本地 GeoIP 详情的活动/已关闭 Connections、Rules/Providers、有界的结构化 Logs 流、订阅管理表单、分类的 System 页面,以及驱动面板安装/更新/激活/打开/回滚的 Web GUI 页面(在守护进程通告 `web-gui` 能力之后)。
- Setup 安装核心、可添加初始订阅、准备本地 GeoIP 数据,并请求守护进程持久化校验过的本地端点。
- Setup 第一步用短连接 `net.Listen` 预检三个托管端口的可用性:占用端口标红(Danger)并提供一键自动切换到下一个可用端口(从 `port+1` 起搜索,上限 `+1024`,三端口保持互异);权限等未知错误不标红、不阻塞,仍由守护进程启动时兜底校验。预检以 generation 守卫拒绝迟到的探测结果。
- 进入 core / GeoIP 步骤时,Setup 经只读 `GET /v1/core`、`GET /v1/geoip/status` 探测本地资源就绪:已就绪显示版本并提示「将直接使用、无需下载」,失败回退静态文案且绝不阻塞流程。
- Setup 审查页汇总端口(改端口且守护进程报告需重启时标注「需重启生效」)/ core 来源与版本(本地已有/新装/安装失败)/ 订阅 / GeoIP / mihari 服务注册状态(经 `GET /v1/service/status` 拉取);跳过项如实标注。各步结果在命令闭包内回写 Model,依赖 Bubble Tea 的 cmd→channel→Update happens-before 保证。
- System 页面通过与 `mihari service` 相同的本地服务适配器管理 OS 服务(安装/卸载/启动/停止/重启/状态);这些操作要求进程已经提权,且不经过守护进程控制协议。当守护进程通告相应能力时,System 页面显示实时的系统代理与 TUN 状态,并通过本地控制 API 切换它们(开启外部代理或其他 TUN / mihomo 实例需要强制确认;Mihari 从不清除其他产品的代理)。
- System 页面的 Ports Config 可修改 Mixed / Controller / Web 端口;占用按本实例 PID 显示 `Owned`,或 `Occupied by name (pid)` / `Available`。写入复用 onboarding 更新,应用后通常 `RestartRequired`。没有对应 CLI。
- System 页面的 Logging 区可修改 daemon-owned 的 level、最大文件大小与保留数量；更新经稳定的 `/v1/logging` 控制协议热应用，不需要 daemon restart。Logs 页的 `e` 与 System → Logging 的 **Export logs** 打开同一个本地导出对话框；导出不增加 control API 或 CLI 命令。
- System 页面还在进入时以只读方式检查 Mihari 的最新 GitHub Release,并用 `当前版本 · 最新版本 available`、`当前版本 · Up to date` 或 `ahead of <channel> <latest>` 展示结果。确认更新后,本地 updater 在控制协议之外替换 Mihari 可执行文件并尝试重启已安装服务;该写操作要求 TUI 进程已经具备管理员/root 权限,不会自动触发 UAC 或 sudo。旧 Bubble Tea 程序先退出并恢复终端,随后平台适配器从已替换的二进制自动进入新 TUI。
- Mihari 应用通道 `main`/`dev` 与 mihomo Core 通道 `stable`/`alpha` 分开：应用通道写在数据根的 `mihari-channel` sidecar，不进 `mihari.yaml` / `/v1`；AIO `--channel` 只写该 sidecar；CLI/TUI 自更新仍走 GitHub Releases。
- System 页面的 `Core Channel` 行可在 `stable` / `alpha` 之间切换;切换后由守护进程按新通道重装核心。版本行显示 `ParseVersion(mihomo -v)` 的身份 token,从不显示 `Prerelease-Alpha`。
- 规则顺序从不排序;onboarding、系统、provider、订阅、面板和浏览器变更都经由守护进程变更协调器,破坏性或大范围操作需要确认。

## Web 网关

- 守护进程在 `web-addr`(默认 `127.0.0.1:9191`)上启动回环 Web 网关。
- 浏览器认证使用存储在数据根目录下的专用 Web 访问凭据;它绝不是 mihomo 控制器密钥,也不会出现在状态 DTO、默认 CLI 输出或日志中。
- `panel open` 铸造一次性本地 URL、启动 OS 浏览器,且不打印令牌。
- 面板静态资产位于 `web/{panel}/{build}/` 下,使用原子 `active.json` 切换,并保留一个先前构建用于回滚。
- 浏览器 REST 与 WebSocket 流量在网关处认证;网关只将控制器密钥注入被代理的控制器请求。
- 未知写入默认拒绝;核心升级与托管字段写入永远不会到达 mihomo。

## 订阅

- 订阅 URL 仅存储在守护进程私有的目录中,并从 list/show 响应与常规错误中省略。
- 每个有效配置都有独立缓存,因此 `sub use` 在无 provider 网络访问时也能工作。
- 每个订阅可独立配置拉取代理(`direct` / `proxy` / `auto`);`auto` 在代理失败时回退直连。
- 生成的配置总是在 `mihomo -t` 与重载之前恢复 Mihari 托管的内环回控制器、密钥与端口不变量。

## 系统代理与 TUN

- `sysproxy enable` 将桌面 HTTP/HTTPS/SOCKS 系统代理指向 Mihari 的混合端点。如果另一产品已持有代理,enable 会以 `system_proxy_conflict` 失败,除非传入 `--force`(TUI 会要求确认)。
- `sysproxy disable` 只清除**由 Mihari 持有**的代理;它不会关闭外部代理。
- 在 Windows 上,当 Mihari 作为 LocalSystem 服务运行时,它写入**交互式控制台用户**的 WinINET 配置单元(`HKEY_USERS\<SID>\…`),而不是 SYSTEM 自己的 `HKCU`,因此桌面浏览器能感知到变更。
- `tun enable|disable` 持久化托管 TUN 块、将其注入生成的 mihomo 配置,并在可用时通过控制器实时生效。TUN 根据 OS 不同可能需要提权或安装服务。
- `tun enable` 前检测其他 TUN 网卡与其他 mihomo 进程,并按本实例内核 PID 与 live `tun.device` 扣除自身;Down 状态的残留适配器忽略。冲突时以 `tun_conflict` 失败,除非传入 `--force`(TUI 会要求确认)。`--force` 只绕过冲突门控,不绕过 live 核对:内核未真正开启则回滚 Desired。

## GeoIP

- GeoIP 连接详情由守护进程本地解析。国家与 ASN MMDB 文件从公共 `Loyalsoldier/geoip` release 分支下载,与对应的 `.sha256sum` 文件校验,并作为 MMDB 数据库验证。
- 当任一本地文件缺失或至少 30 天未更新时刷新。刷新失败会保留上一对有效的数据库,且不会禁用其他连接详情。

## 数据路径

- 每次安装保持**一个数据根目录**(可用 `MIHARI_DATA` 覆盖):Windows 默认 `%USERPROFILE%\.mihari`,macOS/Linux 默认 `$HOME/.mihari`。
- 几乎所有内容都位于该根目录下:设置、控制令牌(`control.token`)、运行时配置、核心二进制、订阅、GeoIP、面板资产、日志与暂存。
- 文件日志布局:`logs/mihari-daemon.log`、`logs/mihari-tui.log`、`logs/mihomo.log`。TUI 在本地对各来源取得有界快照，按时间窗过滤并递归二次脱敏后，把固定 entry 与 `manifest.json` 原子发布到 `logs-export/` 或用户选择的既有目录；不读取 settings、token、订阅、runtime config 或 lock file，也不覆盖已有目标。
- 日志与默认导出目录经 `PrivateFS` 创建/加固:Unix `0700` 目录/`0600` 文件;Windows 受保护 DACL 授予解析出的个人数据用户与 LocalSystem（尚未迁移的旧 SYSTEM 启动暂保留 BA/SYSTEM）。跨进程协调使用相邻 `*.lock` 文件;打开 `logs/` 及文件后每一跳 no-follow,拒绝中间与最终的 symlink/reparse/junction。
- 导出从校验目标到提交结束一直持有真实父目录 identity，并只用目录句柄内的 basename 创建 workspace 和发布；生成期间外部替换可见父路径会安全失败，不会跟随到替代目录。成功发布后父目录若被外部再次改名，先前显示的绝对路径可能失效。
- Unix workspace 清理只在能证明父目录 namespace 由同 UID、数据根 owner 或本机 root/管理员等受信主体控制时按名删除；不可信共享父目录在内容清理成功时允许留下空的 0700 私有 workspace，清理 IO 失败则只能报告可能有内容残留。Windows 从创建起持有不 share-delete 的 workspace guard，直至验证并清理完成。
- 需要本地数据根的选定命令必须先 `Paths.Absolute()`,再 `NewPrivateFS(absolutePaths.Root)`,然后才允许 `EnsureDirs`、默认 in-root token 或 Settings IO。`EnsureDirs` 可预创建 `logs/` 但不是 root 创建/加固入口。`--help`/`--version` 不得调用该路径。root/LocalSystem 遇到缺失 data root 必须零目录 IO fail closed,不得创建仅 root/System 可用的替代根。该 `NewPrivateFS` 失败不得写入 CLI `SetupError`(避免拦截 TUI);TUI 可继续运行并接管 `PrivateFS=nil`,daemon 不得在失败后继续目录/Settings IO。daemon 与 TUI 复用并接管这一个进程级 `PrivateFS` capability,不得在 `EnsureDirs`/Settings 之后再次调用 `NewPrivateFS`。
- `service install` 将**绝对**的 `MIHARI_DATA=<data root>` 写入 OS 服务环境,使 LocalSystem/root 服务与安装它的用户共享同一棵树(而非 `systemprofile` 或 `/root`)。
- `service uninstall` 只移除 OS 注册并**保留**数据根目录。请手动删除数据目录(或未来的 `--purge`)以清除残留文件。`%AppData%\mihari` 或 `%ProgramData%\mihari` 下的旧树不会自动迁移或删除。

覆盖项:

```text
MIHARI_DATA=/abs/path
MIHARI_CONTROL_ENDPOINT=...
MIHARI_CONTROL_CREDENTIAL=...
```
