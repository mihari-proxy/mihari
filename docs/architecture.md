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

## TUI

- TUI 只通过 `internal/control/client` 经原生 IPC 控制面与本地守护进程通信。它从不打开 mihomo 控制器、从不接收控制器密钥、也从不自己执行持久化写入。
- 搜索与表单字段中的括号粘贴和 Ctrl+V 使用纯 Go 实现的 `github.com/atotto/clipboard` 辅助库;Mihari 本身从不把密钥写入剪贴板。
- 页面:独立的首次运行 Setup 路由、Overview、可展开的 Proxies、带本地 GeoIP 详情的活动/已关闭 Connections、Rules/Providers、有界的结构化 Logs 流、订阅管理表单、分类的 System 页面,以及驱动面板安装/更新/激活/打开/回滚的 Web GUI 页面(在守护进程通告 `web-gui` 能力之后)。
- Setup 安装核心、可添加初始订阅、准备本地 GeoIP 数据,并请求守护进程持久化校验过的本地端点。
- Setup 第一步用短连接 `net.Listen` 预检三个托管端口的可用性:占用端口标红(Danger)并提供一键自动切换到下一个可用端口(从 `port+1` 起搜索,上限 `+1024`,三端口保持互异);权限等未知错误不标红、不阻塞,仍由守护进程启动时兜底校验。预检以 generation 守卫拒绝迟到的探测结果。
- 进入 core / GeoIP 步骤时,Setup 经只读 `GET /v1/core`、`GET /v1/geoip/status` 探测本地资源就绪:已就绪显示版本并提示「将直接使用、无需下载」,失败回退静态文案且绝不阻塞流程。
- Setup 审查页汇总端口(改端口且守护进程报告需重启时标注「需重启生效」)/ core 来源与版本(本地已有/新装/安装失败)/ 订阅 / GeoIP / mihari 服务注册状态(经 `GET /v1/service/status` 拉取);跳过项如实标注。各步结果在命令闭包内回写 Model,依赖 Bubble Tea 的 cmd→channel→Update happens-before 保证。
- System 页面通过与 `mihari service` 相同的本地服务适配器管理 OS 服务(安装/卸载/启动/停止/重启/状态);这些操作要求进程已经提权,且不经过守护进程控制协议。当守护进程通告相应能力时,System 页面显示实时的系统代理与 TUN 状态,并通过本地控制 API 切换它们(开启外部代理需要强制确认;Mihari 从不清除其他产品的代理)。
- System 页面还在进入时以只读方式检查 Mihari 的最新 GitHub Release,并用 `当前版本 · 最新版本 available` 或 `当前版本 · Up to date` 展示结果。确认更新后,本地 updater 在控制协议之外替换 Mihari 可执行文件并尝试重启已安装服务;该写操作要求 TUI 进程已经具备管理员/root 权限,不会自动触发 UAC 或 sudo。旧 Bubble Tea 程序先退出并恢复终端,随后平台适配器从已替换的二进制自动进入新 TUI。
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
- 生成的配置总是在 `mihomo -t` 与重载之前恢复 Mihari 托管的内环回控制器、密钥与端口不变量。

## 系统代理与 TUN

- `sysproxy enable` 将桌面 HTTP/HTTPS/SOCKS 系统代理指向 Mihari 的混合端点。如果另一产品已持有代理,enable 会以 `system_proxy_conflict` 失败,除非传入 `--force`(TUI 会要求确认)。
- `sysproxy disable` 只清除**由 Mihari 持有**的代理;它不会关闭外部代理。
- 在 Windows 上,当 Mihari 作为 LocalSystem 服务运行时,它写入**交互式控制台用户**的 WinINET 配置单元(`HKEY_USERS\<SID>\…`),而不是 SYSTEM 自己的 `HKCU`,因此桌面浏览器能感知到变更。
- `tun enable|disable` 持久化托管 TUN 块、将其注入生成的 mihomo 配置,并在可用时通过控制器实时生效。TUN 根据 OS 不同可能需要提权或安装服务。

## GeoIP

- GeoIP 连接详情由守护进程本地解析。国家与 ASN MMDB 文件从公共 `Loyalsoldier/geoip` release 分支下载,与对应的 `.sha256sum` 文件校验,并作为 MMDB 数据库验证。
- 当任一本地文件缺失或至少 30 天未更新时刷新。刷新失败会保留上一对有效的数据库,且不会禁用其他连接详情。

## 数据路径

- 每次安装保持**一个数据根目录**(可用 `MIHARI_DATA` 覆盖):Windows 默认 `%USERPROFILE%\.mihari`,macOS/Linux 默认 `$HOME/.mihari`。
- 几乎所有内容都位于该根目录下:设置、控制令牌(`control.token`)、运行时配置、核心二进制、订阅、GeoIP、面板资产、日志与暂存。
- `service install` 将**绝对**的 `MIHARI_DATA=<data root>` 写入 OS 服务环境,使 LocalSystem/root 服务与安装它的用户共享同一棵树(而非 `systemprofile` 或 `/root`)。
- `service uninstall` 只移除 OS 注册并**保留**数据根目录。请手动删除数据目录(或未来的 `--purge`)以清除残留文件。`%AppData%\mihari` 或 `%ProgramData%\mihari` 下的旧树不会自动迁移或删除。

覆盖项:

```text
MIHARI_DATA=/abs/path
MIHARI_CONTROL_ENDPOINT=...
MIHARI_CONTROL_CREDENTIAL=...
```
