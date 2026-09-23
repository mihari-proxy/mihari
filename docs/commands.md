# 完整命令参考

本文档收录 mihari 的全部 CLI 命令。快速上手见 [README](../README.zh-CN.md)。

查询与变更命令都支持人类可读输出与 `--json`。`--json` 输出带版本的成功或错误信封,并为自动化提供稳定的进程退出码。

命令别名:`service` 也可写作 `svc`,`sub` 也可写作 `subscription`/`subscriptions`,`panel` 也可写作 `panels`/`web-gui`。

## 服务管理

安装为 OS 服务,使其在关闭终端后继续运行(**需要管理员 / root 权限**):

```console
# Windows:先打开 "Terminal (Admin)" 或提权的 PowerShell
mihari service install
mihari service start
mihari service status
mihari service stop
mihari service restart
mihari service reinstall
mihari service uninstall
mihari service uninstall --purge --yes
```

执行 `service install` + `start` 后,关闭 TUI 或普通控制台**不会**停止 Mihari;只有 `service stop`、卸载或操作系统才能停止它。同样的控制也在 TUI 的 **System** 页面中提供(变更操作需要提权 shell)。

Unix 的 install/reinstall/update/start/stop/uninstall 走统一安装用例，未完成事务须显式恢复；Windows 保持原服务行为。Unix 自动化入口为 `mihari service apply --request /absolute/request.json`（严格版本化 JSON，请求文件 root0600）；operation=recover 用于恢复，不会隐式导入旧树。普通 `service uninstall` 只注销 OS 服务，保留数据与旧日志。`--purge --yes` 先卸载服务，再删除白名单内的受管文件；未知文件名会停止且不删除。`--purge --yes --force` 跳过内容检查并删除整个目标文件夹。TUI System 页 **Completely Uninstall Mihari** 列出将清空的文件夹并确认两次，等价于 `--force`。需要已提权 shell，不自动弹出 UAC/sudo。

Linux 服务启动失败后，systemd 可能显示 `activating (auto-restart)`。如果此时主进程已退出（`MainPID=0`），`mihari service status` 返回 `stopped`，仍允许进入服务停止或重装流程；这不表示 systemd 已取消自动重启。升级旧版本后若日志提示 `existing data requires recovery or migration`，应使用包含此修复的版本执行 `sudo mihari service reinstall`，由安装事务处理旧数据和服务定义，而不是直接修改 unit 的启动参数。重装失败时保留报错和 `journalctl -u mihari` 日志继续排查。

如果旧目录只有 `mihari-channel`、空的 `install.lock`、空的 `locks` 目录和 `transactions/<ID>/transaction-id` 中的部分或全部，说明该目录尚未包含业务配置。安装事务可在校验这些启动残留后建立新的数据目录，保留旧目录，并由新 daemon 完成首次初始化。含恢复日志、事务备份、非空 `locks`、未知文件或不匹配标记的目录不会按此路径处理；不要通过删除这些文件强行绕过恢复检查。

上述残留迁移要求旧数据根与目标数据根不同，例如从旧私有目录迁移到系统布局的 `/var/lib/mihari/data`。显式设置或继承 `MIHARI_DATA` 后在同一私有目录重装会保留原数据，不会清理这些残留。含 `transactions/<ID>/unit-bootstrap` 的目录也不属于仅有启动标记的情形，应保留现场继续排查。

守护进程本身可手动在前台运行(OS 服务与 TUI 的 System 页面使用同一入口);正常使用无需手动执行,且前台运行时关闭终端会停止守护进程:

```console
mihari daemon
```

更新 mihari 二进制本身需要提权。`self channel` 查询选定 Unix B/P sidecar（缺失为 main）；系统通道写入需要 root，私有 P 需要实际 owner，持 install.lock 并拒绝相关未完成事务。`self update` 先检查服务；没有服务时使用编译通道、只更新 binary，不为选择下载访问 B。已有服务通过统一停机安装事务更新。Windows 自更新同时检查实际服务安装副本：

```console
mihari self version
mihari self channel
mihari self channel [main|dev]
mihari self update
mihari self update --yes
```


### 替换旧版本时的确认

更新先固定下载候选，再检查将覆盖的实际二进制与服务副本。降级或无法确定旧版本兼容性时，`self update` 要求显式 `--yes`；缺少确认返回 `invalid_argument`、退出码 2。正常升级无需增加此参数。已安装 stable 高于当前发布版本时继续显示 ahead，保持现有版本；同基础版本的 dev → stable 视为升级。

风险提示说明：旧程序可能无法读取新版本写入的设置、订阅、状态和生成文件，可能无法启动或表现为数据丢失。确认不会迁移配置或回滚磁盘状态。管理员操作用户可写的旧程序时不会执行它来查询版本，因此可能显示 unknown，仍可明确确认后使用既有修复路径。

TUI 的 System 页面先显示 Preparing，再按实际准备的候选和目标版本确认。取消或离开页面会放弃准备结果。确认后关闭界面资源，再执行替换；若最终复核发现安装已变化，应重新打开 Mihari 后重试。

Unix `service apply` 直接调用不询问交互输入。无确认时，风险错误的 details 包含安全版本、角色和 `preview_id`。交互脚本接受后对同一请求传 `--yes --expected-preview <原 preview_id>`；该参数必须与 `--yes` 同时使用。指纹变化返回 `invalid_state`，需重新开始，不自动忽略指纹重试。初始无人值守命令可以只传 `--yes`，本次执行仍复核目标。明确的 recover 请求保持原行为，确认不允许覆盖未完成事务。

`--json` 保持成功 stdout JSON 和失败 stderr 错误 envelope。风险信息使用 stderr；已确认但执行失败时，JSON 风险说明合入错误 message。Windows 主程序成功替换后若服务副本或注册定义变化，会保留已更新结果并报告服务同步警告。Windows 在各实际操作边界复核，外部程序仍可能在检查与复制之间更改安装，未提供跨文件原子事务。

## 状态查询

查询守护进程状态(服务/守护进程启动后无需管理员即可使用):

```console
mihari status
mihari status --json
```

## 文件日志

Unix daemon/mihomo 日志位于 D/logs，本用户 TUI 日志位于 U/logs；当前 UID 的 TUI 实例共享固定序列。Windows/显式私有 P 保留单根。具体路径和 root 安全兼容约束见 [Unix 布局与恢复](unix-layout.md)。

守护进程与捕获的 mihomo 日志默认级别为 `info`，每个活跃文件达到 10 MiB 时轮转，最多保留三份文件（活跃文件与最多两份归档）。TUI 启动时使用 bootstrap 配置：`debug`、100 MiB、10 份文件，以便在守护进程设置可用前也能记录日志；在后续控制面同步前保持该 bootstrap 配置。mihomo 捕获按文本/JSON 中识别到的真实级别过滤，原文保持；无法识别时 stdout 回退为 `INFO`、stderr 回退为 `WARN`。

System → Logging → Level 同时控制 Mihari 文件日志与 mihomo 全局 `log-level`，生成配置覆盖订阅值但不修改原缓存。主动可选 `debug`、`info`、`warn`、`error`。在线修改经过校验、内核确认、reload 与保存，失败补偿恢复；内核停止时保存到下次启动应用。外部内核变化每 2 秒观察：保存失败保留内核现状、显示未保存并重试最新值。仅被动采纳内核的 `silent`，保留历史文件和操作错误提示，用户可切回四档。旧版本可能拒绝已保存的 `silent`，降级前应切回支持级别或恢复兼容的停机备份。TUI/Web 实时日志筛选独立于文件级别，silent 也不限制实时订阅。网关允许单字段 `PATCH /configs {"log-level":"debug"}`，混合及未知写入仍拒绝。

TUI 的 System 页面提供 Logging 区，可修改由守护进程持有的级别、单文件最大大小和保留数量；变更通过稳定的本地控制端点 `GET /v1/logging` 与 `PATCH /v1/logging` 完成。它们不是 CLI 命令，因此没有 `mihari logging` 或日志导出子命令。

日志导出只在 TUI 中提供：Logs 页按 `e`，或在 System → Logging 选择 **Export logs**。可选最近 24 小时、最近 60 分钟、本地时间区间或全部记录。Unix 默认目录是本用户的 `U/logs-export/`（Windows/私有 P 保留原目录）；自定义目标必须是既有目录内的绝对 `.zip` 路径。导出永不覆盖已有文件，默认重名时自动编号。

Unix 系统导出使用 mihari-logs-export/v2，组合认证机器快照与本用户日志，离线必须明确选择仅本用户日志；Windows/显式私有 P 保持本地 v1。zip 固定使用 `manifest.json`、`daemon/mihari-daemon.log`、`tui/mihari-tui.log`、`mihomo/mihomo.log` 这些 entry，某来源无匹配记录时省略对应日志 entry。文件日志、快照与导出均不脱敏，错误自带的密码、访问令牌、完整 URL、配置片段及路径会保留；导出开始前及完成后都有红色说明，分享前应自行检查。

Unix 自定义目标的同 UID 进程和本机 root/管理员属于受信主体。不可信共享父目录下，若内容已清理，仍可能留下空的 0700 私有 workspace；若清理 IO 失败，界面会报告可能存在内容残留。导出持有目标父目录 identity，生成期间替换父路径会安全失败而不会跟随；发布后外部再次改名目标目录，可能使已显示路径失效。

旧版二进制以 `KnownFields(true)` 严格解码 `mihari.yaml`，不能读取非默认的 `log:` 块。降级前应在 System → Logging 恢复 `info` / 10 MiB / 3 份文件，使该块自动移除；也可以先备份设置文件后手动删除 `log:`。

诊断文本、HTTP 失败正文和 mihomo 单个逻辑输出行各限 256 KiB，超出明确标记截断。最坏 JSON 转义可能使一条逻辑诊断分成多条 JSONL；通过 `record_id`、`fragment_index`、`fragment_count` 关联和重组，缺片可识别。历史脱敏日志无法恢复原文，旧客户端仍可能按旧策略处理导出。

普通 CLI 不创建或探测日志文件；当前执行路径已有可用文件 logger/reporter 时记录，否则跳过。预期拒绝和主动取消为 INFO、重试和可恢复警告为 WARN、最终未恢复失败为 ERROR，均遵循用户配置的级别。用户提示与日志原文独立。

## 核心与代理管理

通过守护进程管理和检查 mihomo:

```console
mihari core status
mihari core install
mihari core update
mihari core reinstall
mihari core restart
mihari proxy groups
mihari proxy mode
mihari proxy mode rule
mihari proxy mode global
mihari proxy mode direct
mihari proxy select GLOBAL PROXY
mihari proxy select GROUP PROXY
mihari proxy test GROUP
mihari connections list
mihari connections close ID
mihari connections close-all --yes
mihari rules list
mihari traffic --follow
mihari logs --follow
```

`core install` / `core update` 从所选 stable/alpha 通道获取官方最新版，先检查候选，再替换和验收；常规失败保留或恢复旧核心与原通道。更新 Mihari 本身保留已有核心，离线首次安装可使用包内版本。本地已有核心不要求官方来源凭据，但仍遵守平台已有权限和文件身份保护。

`core reinstall` 重新下载原通道官方最新版，即使当前版本相同也会重装；TUI System 的 **Reinstall core** 提供同一操作。更新中断后，它沿用更新前通道，保留订阅和配置，失败继续保留备份与阻断，验收成功才解除阻断。普通 `core restart` 不修复中断更新。诊断会列出数据根 `staging/core` 中的材料位置；不要删除中断记录来绕过阻断。损坏的记录、无效配置或权限错误仍须按诊断处理，重装不能保证修复所有故障。

`proxy mode` 查询保存模式、实际模式和应用状态，带参数则切换；支持 `--json`。模式只有 `rule` / `global` / `direct`，默认 Rule，覆盖订阅自带 mode。模式由 Mihari 全局持久化，GLOBAL 出口按订阅记忆；Rule/Direct 下也能预选 GLOBAL，选择本身不会切换模式。Global 选择 `DIRECT` 表示所有新连接通过 GLOBAL 直连；Direct 是独立运行模式，不依赖 GLOBAL 的选择。

TUI Proxies 顶部选中 **Mode** 按 Enter 打开弹窗，↑/↓ 选择、Enter 应用、Esc 取消。下一行 **GLOBAL** 展开同页的 GLOBAL 组，候选完全来自 mihomo。切换保留已有连接。有效候选消失时，有 DIRECT 则持久保存 DIRECT，否则保存 Rule，不会在节点重新出现时自动切回。内核明确停止时可保存模式，显示 `pending`；无法确认实际状态时显示 `unknown`，不会把保存值伪装成运行值。

settings 新增可选 `routing.mode`、`routing.global-selections`（订阅 ID → 出口）与 `routing.bootstrap-global`。它们由 daemon 管理；支持的 Web 面板也走相同保存路径。旧版本可能拒绝新增 settings 字段，降级前应备份并迁移设置。旧 daemon 不通告 `routing-mode-v1` 时，TUI 隐藏新入口。

## 订阅管理

通过守护进程变更协调器管理订阅:

```console
mihari sub add NAME URL
mihari sub add NAME URL --proxy auto
mihari sub list
mihari sub show ID
mihari sub refresh ID
mihari sub use ID
mihari sub enable ID
mihari sub disable ID
mihari sub set ID --interval 6h --auto-refresh=true
mihari sub set ID --proxy auto
mihari sub remove ID --yes
```

订阅 URL 由守护进程持久化,并从普通 list/show 业务响应中省略；错误本身携带的 URL 会保留在诊断详情中。每个有效配置都有独立缓存,因此 `sub use` 在无 provider 网络访问时也能工作。`--proxy` 为主订阅 YAML 的拉取渠道:`direct`(默认)、`proxy` 或 `auto`。`auto` 在代理连接超时、拒绝、重置或成功响应正文读取超时后尝试直连；HTTP 错误、无效文档和整次操作取消不触发回退。生成的配置总是在 `mihomo -t` 与重载之前恢复 Mihari 托管的内环回控制器、密钥与端口不变量。

`sub add` / `sub refresh` 的控制请求允许等待 180 秒，daemon 正常执行共用 120 秒上限，单次代理/直连下载各保留 30 秒；剩余等待余量用于已开始事务的有界补偿与响应。Ctrl+C 或更短的调用方 deadline 仍可提前取消。普通控制请求不使用该长预算。添加已注册但首次下载失败时保留订阅，应刷新同一 ID；客户端响应丢失不代表服务器未保存，不要盲目重复添加。CLI 的 `auto` 参数和输出不变，TUI 展示为 `PROXY w Fallback to DIRECT`，批量刷新每条使用独立预算。Provider override 不在该设置范围，CLI/TUI 与 daemon 应同步升级。

`sub set` 修改 URL 保留旧缓存与 InUse，不立即拉取或重载；修改单条 interval 重置调度并标记 Expired，成功刷新后清除。CLI 参数仍为 `--proxy`，JSON 字段仍为 `proxy_mode`；仅新增公开缓存状态字段，没有新增 reveal CLI 命令。普通 list/show 业务响应继续省略完整 URL；专门的认证本地 API 是支持的读取入口。文件日志、导出及 CLI/TUI 错误详情保留错误自带的 URL，不额外读取或转储订阅。TUI 操作、结果未知处理和配套升级/降级备份要求见 [README](../README.zh-CN.md)。

## 系统代理与 TUN

```console
mihari sysproxy status
mihari sysproxy enable
mihari sysproxy enable --force
mihari sysproxy disable
mihari tun status
mihari tun enable
mihari tun enable --force
mihari tun disable
```

`sysproxy enable` 将桌面 HTTP/HTTPS/SOCKS 系统代理指向 Mihari 的混合端点。如果另一产品已持有代理,enable 会以 `system_proxy_conflict` 失败,除非传入 `--force`(TUI 会要求确认)。`sysproxy disable` 只清除**由 Mihari 持有**的代理;它不会关闭外部代理。在 Windows 上,当 Mihari 作为 LocalSystem 服务运行时,它写入**交互式控制台用户**的 WinINET 配置单元(`HKEY_USERS\<SID>\…`),而不是 SYSTEM 自己的 `HKCU`,因此桌面浏览器能感知到变更。

`tun enable|disable` 仅覆盖 `tun.enable`；stack、device、DNS、路由等其他 TUN 参数保留订阅原值，不自动注入默认 stack。未通过 Mihari 托管开关时，订阅的 enable 也保持原值。开关意图由 daemon 持久化，并在可用时通过控制器实时生效。开启前会检测系统上的其他 TUN 网卡与其他 mihomo 进程(忽略 Down 状态的残留适配器);冲突时以 `tun_conflict` 失败,除非传入 `--force`(TUI 会要求确认)。`--force` 只绕过冲突门控:若内核未真正开启 TUN,Desired 会回滚。TUN 根据 OS 不同可能需要提权或安装服务。没有用于修改端口的 CLI 命令;Mixed / Controller / Web 端口在 TUI System 页的 Ports Config 中修改。

## egress — 出口网卡

```console
mihari egress list
mihari egress status --json
mihari egress set "Ethernet" --if-revision 12
mihari egress auto
```

选择按网卡精确名称保存，作用于整个实例，切换订阅后保留。候选包括本次枚举到的物理或虚拟网卡，以及已保存但缺失的网卡；不能预填未知名称。自身 TUN 显示但禁选。类型无法可靠识别时显示 Unknown，可用状态只反映本地网卡状态，不探测互联网。

手动模式在生成配置中覆盖全局、节点、provider override 和 DNS 的显式网卡绑定，保留原订阅、DNS 服务器及代理链选择。若 DNS 需要覆盖的接口名与代理名或 `RULES` 等保留名冲突，或包含原生片段语法的 `&` / `=`，生成失败并保留原配置，避免被核心误解为代理选择。No-Override 撤销 Mihari 的覆盖，恢复订阅原有语义。

核心运行时完整重载、读回确认并关闭其跟踪的活动连接；不重启核心，不承诺清除原生所有连接池或后台任务。核心停止时只保存，显示 Saved，下次启动使用。网卡 Down、缺失或恢复不会触发自动回退、停核或额外重载；同名重建按同一选择处理。失败补偿至旧配置，已关闭连接无法复活；恢复无法确认时进入 degraded，后续修改被拒绝。

TUI 入口为 **System → Network → Outbound Interface Override**。No-Override 固定在左侧滚动区上方；↑/↓ 浏览、到首尾停止，PgUp/PgDn 滚动详情，回车使用当前行。失败保留当前行，F2 查看完整诊断。此操作与启用 TUN 的已有冲突确认相互独立，不增加二次确认。

采用 mihomo 原生接口绑定；系统 DNS、DHCP DNS 来源、回环/链路本地等原生例外仍按核心语义执行。它不解决两个全局 TUN 的入口路由竞争，也不提供操作系统级隔离或 kill switch。真实双 TUN 流量归属需要在隔离环境单独验证。

新增本地能力 `egress-interface-v1` 与 GET/PATCH `/v1/egress`，不向 Web gateway 开放。JSON 包含 `selection`、`state`（saved/applied/unknown）、`interfaces`、`revision`，set/auto 支持 `--if-revision`。`interfaces[].device` 是与连接名不同的操作系统设备描述；没有单独描述时省略。设置字段 `egress-interface` 只在手动模式保存；降级到不认识该字段的旧 Mihari 前先执行 `egress auto`。

## Web 面板

通过守护进程持有的生命周期管理浏览器面板:

```console
mihari panel list
mihari panel install ID
mihari panel update ID
mihari panel use ID
mihari panel open [ID]
mihari panel rollback ID --yes
mihari panel uninstall ID --yes
mihari panel reinstall ID --yes
```

`panel open` 省略 ID 时打开当前激活的面板;`rollback`、`uninstall` 与 `reinstall` 需要 `--yes` 确认。`uninstall` 删除本地构建,`reinstall` 先卸载再安装最新构建(若是默认面板则重新激活)。

支持的面板适配器:**Zashboard**(发行 dist zip,可用时优先 no-fonts 版)与 **MetaCubeXD**(按 commit SHA 索引的 `gh-pages` 树)。默认的 `go test ./...` 只使用 fixtures,不访问公共网络下载面板。


## 错误详情与全局历史

CLI 文本错误统一展示概要、既有错误码与原始详情。非流式 `--json` 保持单一 envelope，兼容新增 `diagnostic`、`warnings`、`warnings_omitted`；成功后的 warning 保持成功退出码、已提交状态和 revision。普通 CLI 不创建日志文件，详情查询失败也不会重发业务操作。

TUI 九个页面均可按 F2 打开诊断历史，在列表和详情间切换、滚动并复制已采集原文，关闭后恢复原有输入和确认弹窗。后台发生记录也会进入有界历史，不自动抢焦点。日志级别和文件 logger 的可用性不会抑制错误详情；主动取消和正常 EOF 不新增错误，取消伴随的实际清理失败仍保留。

所有日志与本地错误汇报不脱敏。原文中的凭据、完整 URL、路径和配置片段均保留；终端展示只转义控制字符，JSON 和复制保留已采集文本。单条采集上限 256 KiB，截断会明确标记。daemon 历史最多 256 条/32 MiB，本地 TUI 历史最多 128 条/16 MiB；淘汰、重启、旧 daemon 不支持或详情获取失败均有明确状态，历史不持久化。每次实际发生分别记录，同一记录 ID 的重复传输不新增发生记录。
