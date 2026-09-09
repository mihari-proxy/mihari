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
```

执行 `service install` + `start` 后,关闭 TUI 或普通控制台**不会**停止 Mihari;只有 `service stop`、卸载或操作系统才能停止它。同样的控制也在 TUI 的 **System** 页面中提供(变更操作需要提权 shell)。

Unix 的 install/reinstall/update/start/stop/uninstall 走统一安装用例，未完成事务须显式恢复；Windows 保持原服务行为。Unix 自动化入口为 `mihari service apply --request /absolute/request.json`（严格版本化 JSON，请求文件 root0600）；operation=recover 用于恢复，不会隐式导入旧树。卸载保留数据与旧日志。

Linux 服务启动失败后，systemd 可能显示 `activating (auto-restart)`。如果此时主进程已退出（`MainPID=0`），`mihari service status` 返回 `stopped`，仍允许进入服务停止或重装流程；这不表示 systemd 已取消自动重启。升级旧版本后若日志提示 `existing data requires recovery or migration`，应使用包含此修复的版本执行 `sudo mihari service reinstall`，由安装事务处理旧数据和服务定义，而不是直接修改 unit 的启动参数。重装失败时保留报错和 `journalctl -u mihari` 日志继续排查。

如果旧目录只有 `mihari-channel`、空的 `install.lock`、空的 `locks` 目录和 `transactions/<ID>/transaction-id` 中的部分或全部，说明该目录尚未包含业务配置。安装事务可在校验这些启动残留后建立新的数据目录，保留旧目录，并由新 daemon 完成首次初始化。含恢复日志、事务备份、非空 `locks`、未知文件或不匹配标记的目录不会按此路径处理；不要通过删除这些文件强行绕过恢复检查。

上述残留迁移要求旧数据根与目标数据根不同，例如从旧私有目录迁移到系统布局的 `/var/lib/mihari/data`。显式设置或继承 `MIHARI_DATA` 后在同一私有目录重装会保留原数据，不会清理这些残留。含 `transactions/<ID>/unit-bootstrap` 的目录也不属于仅有启动标记的情形，应保留现场继续排查。

守护进程本身可手动在前台运行(OS 服务与 TUI 的 System 页面使用同一入口);正常使用无需手动执行,且前台运行时关闭终端会停止守护进程:

```console
mihari daemon
```

更新 mihari 二进制本身需要提权。`self channel` 查询选定 Unix B/P sidecar（缺失为 main）；系统通道写入需要 root，私有 P 需要实际 owner，持 install.lock 并拒绝相关未完成事务。`self update` 先检查服务；没有服务时使用编译通道、只更新 binary，不为选择下载访问 B。已有服务通过统一停机安装事务更新。Windows 保留原行为：

```console
mihari self version
mihari self channel
mihari self channel [main|dev]
mihari self update
```

## 状态查询

查询守护进程状态(服务/守护进程启动后无需管理员即可使用):

```console
mihari status
mihari status --json
```

## 文件日志

Unix daemon/mihomo 日志位于 D/logs，本用户 TUI 日志位于 U/logs；当前 UID 的 TUI 实例共享固定序列。Windows/显式私有 P 保留单根。具体路径和 root 安全兼容约束见 [Unix 布局与恢复](unix-layout.md)。

守护进程与捕获的 mihomo 日志默认级别为 `info`，每个活跃文件达到 10 MiB 时轮转，最多保留三份文件（活跃文件与最多两份归档）。TUI 启动时使用 bootstrap 配置：`debug`、100 MiB、10 份文件，以便在守护进程设置可用前也能记录日志；在后续控制面同步前保持该 bootstrap 配置。mihomo stdout 捕获为 `INFO`，stderr 捕获为 `WARN`；这只描述 Mihari 的捕获级别，不表示 mihomo 行内消息的实际严重程度。

TUI 的 System 页面提供 Logging 区，可修改由守护进程持有的级别、单文件最大大小和保留数量；变更通过稳定的本地控制端点 `GET /v1/logging` 与 `PATCH /v1/logging` 完成。它们不是 CLI 命令，因此没有 `mihari logging` 或日志导出子命令。

日志导出只在 TUI 中提供：Logs 页按 `e`，或在 System → Logging 选择 **Export logs**。可选最近 24 小时、最近 60 分钟、本地时间区间或全部记录。Unix 默认目录是本用户的 `U/logs-export/`（Windows/私有 P 保留原目录）；自定义目标必须是既有目录内的绝对 `.zip` 路径。导出永不覆盖已有文件，默认重名时自动编号。

Unix 系统导出使用 mihari-logs-export/v2，组合认证机器快照与本用户日志，离线必须明确选择仅本用户日志；Windows/显式私有 P 保持本地 v1。zip 固定使用 `manifest.json`、`daemon/mihari-daemon.log`、`tui/mihari-tui.log`、`mihomo/mihomo.log` 这些 entry，某来源无匹配记录时省略对应日志 entry。每条记录会递归二次脱敏并重新编码。自动遮蔽不保证移除节点名、目标域名/IP 或流量元数据，发送前必须自查这些内容。

Unix 自定义目标的同 UID 进程和本机 root/管理员属于受信主体。不可信共享父目录下，若内容已清理，仍可能留下空的 0700 私有 workspace；若清理 IO 失败，界面会报告可能存在内容残留。导出持有目标父目录 identity，生成期间替换父路径会安全失败而不会跟随；发布后外部再次改名目标目录，可能使已显示路径失效。

旧版二进制以 `KnownFields(true)` 严格解码 `mihari.yaml`，不能读取非默认的 `log:` 块。降级前应在 System → Logging 恢复 `info` / 10 MiB / 3 份文件，使该块自动移除；也可以先备份设置文件后手动删除 `log:`。

日志脱敏是尽力而为，所有日志与导出包仍须按敏感资料处理。

## 核心与代理管理

通过守护进程管理和检查 mihomo:

```console
mihari core status
mihari core install
mihari core update
mihari core restart
mihari proxy groups
mihari proxy select GROUP PROXY
mihari proxy test GROUP
mihari connections list
mihari connections close ID
mihari connections close-all --yes
mihari rules list
mihari traffic --follow
mihari logs --follow
```

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

订阅 URL 仅存储在守护进程私有的目录中,并从 list/show 响应与常规错误中省略。每个有效配置都有独立缓存,因此 `sub use` 在无 provider 网络访问时也能工作。`--proxy` 为该订阅的拉取代理:`direct`(默认)、`proxy` 或 `auto`;`auto` 在代理失败时回退直连。生成的配置总是在 `mihomo -t` 与重载之前恢复 Mihari 托管的内环回控制器、密钥与端口不变量。

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

`tun enable|disable` 持久化托管 TUN 块、将其注入生成的 mihomo 配置,并在可用时通过控制器实时生效。开启前会检测系统上的其他 TUN 网卡与其他 mihomo 进程(忽略 Down 状态的残留适配器);冲突时以 `tun_conflict` 失败,除非传入 `--force`(TUI 会要求确认)。`--force` 只绕过冲突门控:若内核未真正开启 TUN,Desired 会回滚。TUN 根据 OS 不同可能需要提权或安装服务。没有用于修改端口的 CLI 命令;Mixed / Controller / Web 端口在 TUI System 页的 Ports Config 中修改。

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
