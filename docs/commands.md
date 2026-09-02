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

更新二进制后,用 `service reinstall` 从当前二进制重新注册服务(升级路径),然后 `service restart` 使其生效。

守护进程本身可手动在前台运行(OS 服务与 TUI 的 System 页面使用同一入口);正常使用无需手动执行,且前台运行时关闭终端会停止守护进程:

```console
mihari daemon
```

更新 mihari 二进制本身(同样需要提权)。通道查看与切换不提权；`self update` 先读取数据根下的 `mihari-channel` sidecar（缺文件视为 `main`）。若目标版本低于当前版本，必须加 `--yes` 确认降级风险（旧二进制可能无法加载当前配置）：

```console
mihari self version
mihari self channel
mihari self channel [main|dev]
mihari self update
mihari self update --yes
```

## 状态查询

查询守护进程状态(服务/守护进程启动后无需管理员即可使用):

```console
mihari status
mihari status --json
```

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
