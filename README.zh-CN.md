> 免责声明：本项目仅供 Go TUI 工具的学习与交流。这是一个非营利开源项目，现在及未来均不接受任何赞助或捐赠。
>
> 本应用目前处于非正式开发阶段，bug 多是普遍的。

# Mihari — Mihomo / Clash 的 CLI 与 TUI 管理器

[English](README.md) · [简体中文](README.zh-CN.md)

[![license](https://img.shields.io/github/license/mihari-proxy/mihari)](LICENSE)
[![ci](https://img.shields.io/github/actions/workflow/status/mihari-proxy/mihari/ci.yml?branch=main)](https://github.com/mihari-proxy/mihari/actions)
[![go version](https://img.shields.io/github/go-mod/go-version/mihari-proxy/mihari)](go.mod)
[![release](https://img.shields.io/github/v/release/mihari-proxy/mihari)](https://github.com/mihari-proxy/mihari/releases)

[官网](https://mihari-proxy.github.io/mihari/zh/) · [Releases](https://github.com/mihari-proxy/mihari/releases)

Mihari 是面向 Windows、Linux 和 macOS 的跨平台 [mihomo](https://github.com/MetaCubeX/mihomo)（Clash Meta）管理器。它提供 CLI、终端界面（TUI）、订阅管理、系统代理、TUN 模式、mihomo 核心管理与 Web 面板。

它是 Clash Party、Sparkle 等图形化 Mihomo / Clash 客户端的开源终端替代，CLI、TUI 与浏览器面板共享同一个守护进程控制面。

![Overview](assets/overview.png)

Overview 的 Core 卡片会缩短流量趋势图，为速度值及其单位保留同一行的显示空间。

## 这是什么?

**TLDR**:Mihari 是 mihomo 的终端管理器——和 Clash Party、Sparkle 等 mihomo GUI 是同类工具,但它运行在终端里,并由一个守护进程在后台托管,CLI、TUI 和浏览器面板共享同一个控制面。

具体功能:

- **订阅管理**:添加、刷新、切换订阅配置,支持离线切换、独立刷新间隔与按订阅的拉取代理
- **核心管理**:安装、更新、重装和重启 mihomo 核心。在线更新使用官方 stable/alpha 最新版，本地已有核心不要求官方来源凭据；更新 Mihari 保留核心与通道。核心更新中断后阻止不确定启动，可通过 **System → Reinstall core** 或 `mihari core reinstall` 按原通道重装最新版，保留订阅和配置。
- **服务监控**:以 OS 服务方式在后台运行,崩溃自动重启
- **系统代理 / TUN**:开启系统代理或 TUN;若其他产品已占用系统代理或存在其他 TUN/mihomo 实例,需确认或传入 `--force`
- **Web 面板**:一键安装并打开 zashboard / MetaCubeXD 面板
- **连接与规则**:实时查看连接、代理组与规则,本地 GeoIP 解析

## 特性

- **一个守护进程,三种界面**:CLI、TUI 和浏览器面板经本地命名管道 / Unix 域套接字连接同一守护进程控制面,控制 API 从不绑定 TCP 端口。
- **OS 服务托管**:可安装为 Windows 服务 / systemd 单元 / launchd 代理,带崩溃退避重启。
- **订阅配置**:每个订阅独立缓存、离线切换、按配置独立的刷新间隔、按订阅的拉取代理(`direct` / `proxy` / `auto`;`auto` 在可回退的代理网络错误后尝试直连),以及经过校验的原子化配置生成与回滚。
- **Web 面板**:一键安装 / 更新 / 激活 / 回滚 zashboard 与 MetaCubeXD,置于带独立访问凭据的回环 Web 网关之后。
- **系统代理与 TUN**:跨平台的系统代理控制与托管 TUN,均由守护进程持有并持久化。若其他产品已持有系统代理(`system_proxy_conflict`),或检测到其他 TUN / mihomo 实例(`tun_conflict`),enable 会失败,除非传入 `--force`(TUI 会要求确认)。
- **端口配置**:System 页面可修改 Mixed / Controller / Web 端口;占用显示 `Owned` 或 `Occupied by name (pid)`。应用后通常需要重启守护进程。
- **TUI 内更新 Mihari**：System 页面进入时检查 GitHub Releases，显示 `当前版本 · 最新版本 available` 或 `当前版本 · Up to date`；以管理员/root 权限启动时可替换二进制、同步并重启已安装的系统服务副本、验证 daemon 版本，并自动进入更新后的 TUI。更新确认会将安全的非标准已安装构建标识显示为 `Unknown[标识]`，兼容性仍为未知；长内容可用 ↑/↓ 或 PgUp/PgDn 滚动，默认选择 Cancel。
- **内核通道**:System 页面可在 mihomo 的 `stable` / `alpha` 通道之间切换。
- **自动版本检查**：进入 System 时检查 core 当前通道，进入 Web GUI 时逐项检查所有支持的面板，包括尚未安装的面板。检查显示 `Checking…`、最新版本/构建、`Up to date` 或 `Check failed`；成功结果在本次 TUI 会话内缓存 5 分钟，失败时重新进入页面可重试。core 安装/通道切换，以及面板安装/更新/回滚/重装/卸载成功后，立即刷新对应版本检查。检查仅通过 daemon 查询元数据，安装仍需确认。

Windows 更新可使用同一用户的非管理员令牌查询用户目录中的安装版本，包括默认的 AppData 安装位置。若降权 UAC 令牌只能识别身份，Mihari 会在核验同一用户、同一登录会话和非管理员权限后，使用桌面 Shell 的令牌。目录权限不安全或无法取得通过核验的令牌时，版本仍显示 unknown；版本查询不会以管理员权限执行用户可写的文件。

单个无 CGO 的静态二进制(< 15 MB)即包含全部功能,内置 GitHub Releases 自动更新与本地 GeoIP 解析。

代理节点测速会读取 provider 节点，并在需要时调用 mihomo 的 provider 专用接口。同名节点在每个组内合并显示、共享测速结果：优先全局普通节点，否则按 provider 名排序选择首个匹配项。TUI 启动后的首次成功检查会对重名弹窗提示，测速来源可能与组实际选中的来源不同。provider 读取对瞬时故障最多尝试三次；持续失败时保留旧列表，显示 **Stale data** 和关键原因，恢复后自动清除提示。CLI/TUI 与 daemon 应配套升级。

TUI 节点测速进行中时，卡片在协议名称旁仅显示盲文加载动画。

TUI 订阅表格的 Name 和 Traffic 列按内容分配宽度，分别最多占 32 和 24 个终端字符格；多余空间留在右侧，窄屏优先隐藏次要字段。

订阅下载 Mode 将 `auto` 显示为 **PROXY w Fallback to DIRECT**。回退覆盖主订阅 YAML 的连接超时和成功响应正文读取超时等可重试网络错误；HTTP 错误、无效文档不触发回退。每次代理/直连尝试保留 30 秒预算，daemon 的 Add/Refresh 整次执行上限为 120 秒，CLI/TUI 每条等待最多 180 秒以容纳有界回滚和响应。更短的调用方 deadline 与主动取消仍优先，批量刷新逐条计时。窄列表必要时整列隐藏 Mode，进入详情可查看完整值。Provider 下载策略及 Proxies 页 Routing Mode 独立于此设置。

mihomo HTTP 失败的原始报错与上游状态会写入诊断日志，范围包括 gateway 和 WebSocket 握手。日志、导出及本地 CLI/TUI 错误汇报均不脱敏，保留错误自带的凭据、URL、路径与配置片段。CLI 分段展示概要、错误分类和原始详情，JSON 增加可选诊断和 warnings，业务退出码保持不变。TUI 所有页面均可按 F2 打开统一诊断历史，滚动查看详情并复制原文。终端控制字符仅在显示时转义。

F2 每次发生保留独立记录，以级别颜色和选中高亮帮助浏览。宽终端左右显示列表和详情，窄终端上下排列。Tab 切换窗格，方向键、PgUp/PgDn、Home/End 导航，c 复制原始详情，Esc 返回；新记录不会抢走当前选择。Web gateway 允许来自 mihomo 的单条消息最大 1 MiB，与 mihomo 流客户端一致；浏览器发送方向仍限制为 32 KiB。

## 快速开始

**安装**

**main release 通道**（GitHub）

```sh
# Linux / macOS
curl -fsSL https://raw.githubusercontent.com/mihari-proxy/mihari/main/scripts/install/install.sh | bash
```

```powershell
# Windows (PowerShell)
irm https://raw.githubusercontent.com/mihari-proxy/mihari/main/scripts/install/install.ps1 | iex
```

**dev release 通道**（GitHub）

```sh
# Linux / macOS
curl -fsSL https://raw.githubusercontent.com/mihari-proxy/mihari/dev/scripts/install/install.sh | bash -s -- --channel dev
```

```powershell
# Windows (PowerShell)
$env:MIHARI_CHANNEL = 'dev'
irm https://raw.githubusercontent.com/mihari-proxy/mihari/dev/scripts/install/install.ps1 | iex
```

或从 [Releases 页面](https://github.com/mihari-proxy/mihari/releases) 下载对应平台的二进制。

**国内 / 无 GitHub 访问（离线）**

整合包（mihari 二进制 + mihomo 核心 + GeoIP,含 sha256 校验）镜像在自建 AList 网盘上,安装全程不触碰 GitHub。下载器始终从稳定 AList 根目录获取。

**main release 通道**（AList / 离线）

```sh
# Linux / macOS
curl -fsSL https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari/install-aio-remote.sh | bash
```

```powershell
# Windows (PowerShell)
& ([scriptblock]::Create((irm https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari/install-aio-remote.ps1)))
```

**dev release 通道**（AList / 离线）

```sh
# Linux / macOS
curl -fsSL https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari/install-aio-remote.sh | bash -s -- --channel dev
```

```powershell
# Windows (PowerShell)
& ([scriptblock]::Create((irm https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari/install-aio-remote.ps1))) -Channel dev
```

离线分发设计见 [docs/distribution.md](docs/distribution.md)。

**首次运行**

```console
mihari
```

交互式设置沿用现有 TUI 主题和分步布局，长任务显示当前动作、动态等待指示与耗时。失败时显示安全的具体原因和下一步建议，按 F2 可打开可滚动、可复制的诊断详情。

端口修改经确认后立即由 daemon 保存，已保存资源不会因中断丢失。重新进入时检查必需的端口与核心，继续未完成的设置；可选订阅、GeoIP 缺失不会强制进入向导。Core 分别展示本地可用性、版本、通道和运行状态，GeoIP 分别展示 Country/ASN 可用性及更新时间。已有订阅时保留概览步骤，展示数量、各项状态及当前使用项，按 Enter 继续且不修改已有订阅；继续添加或管理请进入 Subscriptions 页面。新订阅已注册但首次下载失败时，重试会刷新同一订阅，不重复添加。

操作中按 Esc 会先确认取消，再核对 daemon 执行是否收尾及实际保存结果。“结果待确认”不代表没有保存，此时可重新检查或退出，不盲目重复提交。Ctrl+Q 确认退出。已确认的启动端口冲突会通过认证本地 IPC 开放受限端口恢复，其他业务变更仍不可用。改端口后需重启 daemon，再重新检查或等待重连；当前服务接口不能验证服务是否属于所连接的实例，因此向导不会自动重启可能不相干的服务。

**添加订阅并启用系统代理**

```console
mihari sub add 我的订阅 https://example.com/subscribe
mihari sub list
mihari sub use <ID>
mihari sysproxy enable
```

## 常用命令

| 场景 | 命令 |
|------|------|
| 查看状态 | `mihari status` |
| 核心管理 | `mihari core status` · `mihari core restart` |
| 代理组 | `mihari proxy groups` · `mihari proxy select <GROUP> <PROXY>` |
| 运行模式 | `mihari proxy mode [rule\|global\|direct]` · `mihari proxy select GLOBAL <PROXY>` |
| 订阅管理 | `mihari sub add <NAME> <URL>` · `mihari sub set <ID> --proxy auto` · `mihari sub use <ID>` |
| 系统代理 / TUN | `mihari sysproxy enable` · `mihari sysproxy enable --force` · `mihari tun enable` · `mihari tun enable --force` |
| Web 面板 | `mihari panel list` · `mihari panel open` |
| 服务控制 | `mihari service status` · `mihari service stop` |
| 全量卸载 | System 页 `Completely Uninstall Mihari` · `mihari service uninstall --purge --yes` · `mihari service uninstall --purge --yes --force` |
| 更新 mihari | System 页 `Update Mihari` · `mihari self update` |

完整命令参考见 [docs/commands.md](docs/commands.md),架构与安全机制见 [docs/architecture.md](docs/architecture.md)。

**Connections** 为 Chain 分配更多宽度，窄窗口中优先于 Source、Destination、Rule 保留。Traffic 使用固定宽度的上下行紧凑速率，`K/M/G/T/P/E` 按 1024 进制表示字节每秒；连接详情仍显示完整速率与代理链。**Rules** 用居中弹窗展示规则或 provider 的完整详情，↑/↓ 或 PgUp/PgDn 滚动，Enter/Esc 关闭后返回原行。

**Conns、Rules、Logs** 支持 Ctrl+F 从侧栏或页面内容区直接聚焦检索框，保留已有文字并将光标移到末尾；页面内容区仍支持 `/`。弹窗打开时不抢走焦点。Conns 在当前 TUI 会话中保留最新 **5000 条已关闭连接记录**，切页保留，重连或退出后清空；活动连接不占此配额。

**Logs** 中选中 **Level** 后按 Enter 打开多选小窗。↑/↓ 移动，Space 勾选 DEBUG、INFO、WARNING、ERROR；**Select all** 用于全选或清空。Enter 应用、Esc 放弃，至少勾选一个级别。连续选到 ERROR 的组合显示为 `DEBUG+`、`INFO+` 或 `WARNING+`，其他组合完整列出，例如 `DEBUG, WARNING`。这只是显示摘要，筛选按所选精确级别匹配，再与文字检索取交集。切页和重连保留选择，重启 TUI 恢复全选；全选时也保留未知级别记录。筛选不修改 System 日志设置或实时流订阅。

连接详情采用单个居中页面，以 **Application → Routing → Outbound → Destination** 纵向展示处理链路，字段归入对应阶段。Routing 合并显示入站名称／类型／协议和 **Rule Matched**，并从外层代理组到出站逐级展开上报的选择链；Outbound 展示 **Remote** 及其 GeoIP，Destination 保留自己的目标地址及 GeoIP，选择树不代表完整网络中转拓扑。上传速率与累计量为绿色，下载为蓝色；拒绝出站以断线连接灰色的请求目标节点。长字段自动换行，深层选择树保留层级序号，面板最大 88 个终端字符列。↑/↓ 滚动，Enter/Esc 返回选中行。**Paused** 表示观测数据已冻结；已关闭连接显示最后观测速率与累计流量，**Closed observed** 是 TUI 发现连接消失的时间，不是内核报告的精确关闭时间。

**Web GUI** 面板卡片宽屏并排、窄屏纵排。Tab/Shift+Tab 或 ←/→ 选择 Open/Install 或 Manage，Enter 执行，↑/↓ 切换面板。顶部以三行对齐展示 Gateway、Default panel 和 Browser sessions。安装、重装或更新期间，橘色 Installing／Reinstalling／Updating 动画 badge 显示在 Manage 后（首次安装在 Install 后）；窄卡片中 badge 整体换到按钮下方，操作结束后清除。Update available 保留在 Latest 版本后，更新成功后刷新版本状态；原有面板快捷键保留。Manage 包含更新、设为默认、重装、回滚及卸载，不可用项标明原因。黄色 **Ctrl+Shift+R** 刷新提示始终保留在卡片上方，网关保护说明移至 `?` 帮助。**System** 的 Network 分区移至 Ports Config 之后。

TUI **Proxies** 页顶部的 **Routing** 卡片包含 **Mode** 和 **GLOBAL**。**Mode** 按 Enter 打开 Rule / Global / Direct 选择弹窗，↑/↓ 选择、Enter 应用、Esc 取消；**GLOBAL** 入口展开 mihomo 返回的候选组，并自动滚动到整个 section 完整可见；超过一屏时从列表视口顶部展示，继续用方向键浏览候选。Mihari 全局保存模式、按订阅保存 GLOBAL 出口，支持面板发起的相同操作。默认使用 Rule，切换模式和出口保留已有连接。保存的出口消失时，有 DIRECT 候选则保存 DIRECT，否则保存 Rule；内核停止时保存的模式显示为 pending，待启动应用。

Routing 标签为白色、值为绿色。仅焦点行在值后紧跟显示 `· Press Enter to Change` 或 `· Press Enter to Select`；窄屏优先保留值，空间不足时隐藏操作提示。状态说明不随失焦隐藏。

已选代理卡片使用蓝色 **●** 标记，颜色与日志 **INFO** 一致。Proxies 每个组（含 GLOBAL）的当前选择右侧都有 **→ Jump to Selected**（窄窗口缩短为 **→ Selected**）。在组标题上按 → 聚焦按钮，再按 Enter 自动展开并定位到当前选中的卡片；← 返回组标题。定位只移动键盘焦点，后续刷新改变选中项时不自动跟随。保留的 **Last selected** 数据仍可定位；选中项为空或不在候选列表中时按钮置灰。

TUI **Subs** 页按 Enter 打开可编辑详情，`a` 添加订阅。Tab/Shift+Tab 或 ↑/↓ 切换字段，←/→/Space 在 **Auto refresh** 和 **Mode** 行循环选择；文本框 Enter 进入下一项，仅 **Save** 焦点上的 Enter 提交。PgUp/PgDn 滚动正文，长 URL 单行横向滚动。所有 TUI 内置文案均为英文。列表显示 **InUse**、**Enabled**、**Status**、**Mode**，`p` 循环切换拉取模式。

添加和编辑共用居中紧凑表单，详情按运行状态和设置分组；没有错误时隐藏错误行，时间显示为本地时间并精确到分钟。窗口较矮时正文滚动，**Save** 始终可见。循环字段使用反色焦点，文本框使用浅色输入底和白色光标。快捷键仅在终端底部显示一份并随焦点变化。**Interval** 留空时以占位文字显示当前全局间隔，保存空值仍表示继承。

修改 URL 会保留旧缓存和 InUse，不立即下载或 reload；**Outdated** 表示缓存来自旧 URL，仍可离线 Use。修改单条 interval 会重置下次刷新时间，并持久标记 **Expired**，直到刷新成功（包括有效 304）；Disabled、Failed、Missing、Outdated 等更高优先级状态仍优先显示。关闭 Auto refresh 时 Next 显示 **Manual**。

保存等待结果后关闭。revision 冲突会询问是否仅覆盖本次实际修改的字段。结果未知时先只读查询操作和当前状态，不自动重放保存；**Submit again** 需要再次确认，添加场景可能产生重复条目。关闭界面不代表撤销保存。已经添加但首次下载失败时，返回列表选中该订阅，按 `r` 重试下载。

TUI 与 daemon 必须配套升级，不保证混用版本。升级前停止 daemon，按实际布局备份完整业务数据（Unix B/D，或 Windows/私有 P），将 catalog、缓存、settings/state、运行配置作为一致整体保存。旧二进制无法读取新增 catalog 字段，不支持直接降级；回退二进制时应停机恢复兼容的完整备份，不通过单独删除 YAML 字段降级。

## 平台目标

- Windows amd64 与 arm64
- Linux amd64 与 arm64
- macOS amd64 与 arm64

所有发行二进制均为无 CGO。

## 数据路径

| 平台 | 默认机器入口 B | 业务数据 D | 本用户诊断 U |
| --- | --- | --- | --- |
| Windows | `%USERPROFILE%\.mihari` | 同左 | 同左 |
| Linux | `/var/lib/mihari` | `B/data` | 绝对 `XDG_STATE_HOME/mihari`，否则可信 home 的 `.local/state/mihari` |
| macOS | `/Library/Application Support/mihari` | `B/data` | 可信 home 的 `Library/Logs/mihari` |

Unix 的 E/C/channel 分别为 `B/control.sock`、`B/control.token`、`B/mihari-channel`；I 默认 `/usr/local/lib/mihari`。B 为 root0711，D 为 root0700，C/channel 为 root0644，E 为 root0666。普通用户无需 sudo 即可认证并管理同一代理及读取受控机器诊断；不能直接读取 D 或其他用户的 U。Windows 继续使用 `\\.\pipe\mihari-control`。

显式 `MIHARI_DATA=P` 保留 P 本身的私有单根语义与 0700/0600 权限，不是 P/data，也不能与默认 B/D 重叠。root 不信 HOME/SUDO_USER/XDG；默认共享发现不使用 XDG_RUNTIME_DIR。root 安装与迁移采用停机、校验、原子提交和可重复恢复；旧数据树及旧日志保留。各平台统一保留订阅配置，仅覆盖 Mihari 托管参数；TUN 开关只覆盖 `tun.enable`，保留其余字段。配置语义和原生 provider 由 mihomo 处理，Mihari 保留候选校验与 reload 回滚。Unix root 仍要求内置可信核心 v1.19.30，二进制身份校验与配置生成相互独立。具体覆盖项、I/FS 限制、停机 credential 轮换和恢复入口见 [Unix 布局与安装恢复](docs/unix-layout.md)。

## 文件日志

Unix 机器日志写入 D，本用户 TUI 日志写入 U；Windows/显式私有 P 保持单根。日志采用 JSONL（每行一个 JSON 对象）：

| 来源 | 路径 |
| --- | --- |
| Mihari 守护进程 | `D/logs/mihari-daemon.log` |
| TUI（当前 UID 的实例共享） | `U/logs/mihari-tui.log` |
| 捕获的 mihomo 输出 | `D/logs/mihomo.log` |

守护进程与捕获的 mihomo 文件日志默认级别为 `info`，每个活跃文件到 10 MiB 时轮转，并保留三份文件（活跃文件加最多两份归档）。TUI 启动时使用 bootstrap 配置——级别 `debug`、100 MiB、10 份文件——以便在守护进程设置可用前也能记录日志；在后续控制面同步前会保持该 bootstrap 配置。TUI 的 System 页面可修改由守护进程持有的级别、单文件最大大小和保留数量，变更无需重启守护进程。捕获 mihomo 输出时会识别文本/JSON 中的真实级别并保留原行；无法识别时，stdout 回退为 `INFO`，stderr 回退为 `WARN`。

System → Logging → Level 同时控制 Mihari 文件日志与 mihomo 全局 `log-level`，生成配置覆盖订阅值但不修改原缓存。主动可选 `debug`、`info`、`warn`、`error`。在线修改经过校验、内核确认、reload 与保存，失败补偿恢复；内核停止时保存到下次启动应用。外部内核变化每 2 秒观察：保存失败保留内核现状、显示未保存并重试最新值。仅被动采纳内核的 `silent`，保留历史文件和操作错误提示，用户可切回四档。旧版本可能拒绝已保存的 `silent`，降级前应切回支持级别或恢复兼容的停机备份。TUI/Web 实时日志筛选独立于文件级别，silent 也不限制实时订阅。网关允许单字段 `PATCH /configs {"log-level":"debug"}`，混合及未知写入仍拒绝。

选中 **Level** 后按 Enter，右侧以 `< INFO >` 整块高亮当前级别。←/→ 在 DEBUG、INFO、WARN、ERROR 间循环选择，再按 Enter 应用；Esc 放弃候选并显示最新实际值。编辑期间 ↑/↓ 和 Tab 不移动焦点；提交时显示 Applying 旋转动画并锁定编辑，成功返回 Level，失败保留候选以便重试。未改变值时直接退出，不发送请求；外部更新不会覆盖候选。从 SILENT 开始编辑时，→ 选 DEBUG、← 选 ERROR，SILENT 不加入主动选项。

`GET /v1/logging` 与 `PATCH /v1/logging` 是供 TUI 使用的稳定 v1 本地控制端点，并非 CLI 命令。日志导出仅在 TUI 提供：可在 Logs 页按 `e`，或在 System → Logging 选择 **Export logs**。对话框支持最近 24 小时、最近 60 分钟、本地时间区间和全部记录。默认输出到 `U/logs-export/`；已有 zip 永不覆盖，自定义目标必须是既有目录中的绝对 `.zip` 路径。没有 CLI 日志导出命令。

Logging 位于 Network 下方、About 上方。Unix 分别显示机器日志目录与本用户日志目录；Windows 的 **Logging Dir** 保持单目录只读路径，选中后按 Enter 复制。Export Logs 中的 **Current Time** 每秒刷新；↑/↓ 选择字段，Enter 进入编辑或应用修改，编辑时 Esc 弹窗确认放弃。Range 编辑支持方向键及 Tab/Shift+Tab 切换模式，自定义区间在行末提示 `Use YYYY-MM-DD HH:MM format`。选中 **Export** 后按 Enter 开始导出。

Windows 私有日志授权给具体的数据用户及 LocalSystem，兼容提权进程创建、owner 为 Administrators 的数据目录；写入器启动时会修复 daemon、TUI、mihomo 的现有日志、保留归档及锁文件的 ACL，不改动内容。运行中的服务创建或加固文件时会重新读取根目录权限策略，避免轮转后恢复旧权限。如果旧版本已经移除了普通用户访问权限，需要更新后的程序以管理员权限运行一次完成修复。TUI 内的提权更新流程会以该权限进入新 TUI；手动替换二进制的用户可能需要首次以管理员权限启动。

Unix 系统模式使用 `mihari-logs-export/v2`，通过认证的机器快照协议组合机器与本用户日志；离线时须明确选择仅本用户日志。Windows/显式私有 P 保持本地 v1。zip 固定包含 `manifest.json`，以及有内容时才出现的 `daemon/mihari-daemon.log`、`tui/mihari-tui.log`、`mihomo/mihomo.log`。记录经过有效性与时间范围筛选，并保留原始 JSON 记录字节；快照及导出均不脱敏。导出前与成功页面以红色文字说明：日志可能包含密码、访问令牌、完整订阅地址及用户配置，分享前请自行检查。

诊断文本、HTTP 失败正文与 mihomo 单个逻辑输出行各限 256 KiB，超限明确标记。JSON 转义使记录超过既有快照限额时，使用带 `record_id`、`fragment_index`、`fragment_count` 的有界分片；轮转或写入中断造成的缺片仍可识别。保留已有堆栈，普通错误不额外采集堆栈。

错误仅使用当前可用的文件 logger；普通 CLI 不创建日志，也不打开历史日志文件。预期拒绝和主动取消为 INFO，可恢复失败及重试尝试为 WARN，最终失败为 ERROR，并遵循配置的级别过滤。同一失败由实际执行 owner 记录，重放同一结果不重复记错。

导出全程持有已打开的目标父目录 identity，生成期间父路径被替换时不会跟随被替换后的路径。Unix 清理以同 UID 与本机 root/管理员为受信主体；若自定义父目录初始为不可信共享目录，即使导出期间收紧权限，内容清理成功后仍可能留下空的私有 workspace，清理 IO 失败则会报告可能存在内容残留。发布成功后若目标目录又被外部改名，界面显示的绝对路径也可能失效。

旧版二进制使用 `KnownFields(true)` 解码 `mihari.yaml`，无法读取自定义 `log:` 块。降级前，请在 System → Logging 恢复 `info` / 10 MiB / 3 份文件，使该块自动移除；或先备份设置文件，再手动删除 `log:`。历史脱敏日志无法恢复原文，旧客户端仍可能对导出内容执行脱敏。

## 开发

```console
go test ./...
go test -race ./...
go vet ./...
```

构建本地二进制:

```console
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o bin/mihari ./cmd/mihari
```

架构不变量、包边界与贡献指南见 [AGENTS.md](AGENTS.md) 与 [CONTRIBUTING.md](.github/CONTRIBUTING.md),发布流程见 [docs/RELEASE.md](docs/RELEASE.md)。

## 社区

本项目完整开源，认可 [LINUX DO](https://linux.do/) 社区，感谢其对开源项目的支持。

## 许可

[GPL-3.0](LICENSE) © 2026 Mihar1

Mihari 是一个独立项目,与 mihomo 项目或 MetaCubeX 无关联,也不受其背书。

## Star History

[![Star History Chart](https://api.star-history.com/chart?repos=mihari-proxy/mihari&type=date&legend=bottom-right)](https://www.star-history.com/?repos=mihari-proxy%2Fmihari&type=date&legend=bottom-right)
