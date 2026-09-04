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

## 这是什么?

**TLDR**:Mihari 是 mihomo 的终端管理器——和 Clash Party、Sparkle 等 mihomo GUI 是同类工具,但它运行在终端里,并由一个守护进程在后台托管,CLI、TUI 和浏览器面板共享同一个控制面。

具体功能:

- **订阅管理**:添加、刷新、切换订阅配置,支持离线切换、独立刷新间隔与按订阅的拉取代理
- **核心管理**:安装、更新、重启 mihomo 核心
- **服务监控**:以 OS 服务方式在后台运行,崩溃自动重启
- **系统代理 / TUN**:开启系统代理或 TUN;若其他产品已占用系统代理或存在其他 TUN/mihomo 实例,需确认或传入 `--force`
- **Web 面板**:一键安装并打开 zashboard / MetaCubeXD 面板
- **连接与规则**:实时查看连接、代理组与规则,本地 GeoIP 解析

## 特性

- **一个守护进程,三种界面**:CLI、TUI 和浏览器面板经本地命名管道 / Unix 域套接字连接同一守护进程控制面,控制 API 从不绑定 TCP 端口。
- **OS 服务托管**:可安装为 Windows 服务 / systemd 单元 / launchd 代理,带崩溃退避重启。
- **订阅配置**:每个订阅独立缓存、离线切换、按配置独立的刷新间隔、按订阅的拉取代理(`direct` / `proxy` / `auto`;`auto` 在代理失败时回退直连),以及经过校验的原子化配置生成与回滚。
- **Web 面板**:一键安装 / 更新 / 激活 / 回滚 zashboard 与 MetaCubeXD,置于带独立访问凭据的回环 Web 网关之后。
- **系统代理与 TUN**:跨平台的系统代理控制与托管 TUN,均由守护进程持有并持久化。若其他产品已持有系统代理(`system_proxy_conflict`),或检测到其他 TUN / mihomo 实例(`tun_conflict`),enable 会失败,除非传入 `--force`(TUI 会要求确认)。
- **端口配置**:System 页面可修改 Mixed / Controller / Web 端口;占用显示 `Owned` 或 `Occupied by name (pid)`。应用后通常需要重启守护进程。
- **TUI 内更新 Mihari**：System 页面进入时检查 GitHub Releases，显示 `当前版本 · 最新版本 available` 或 `当前版本 · Up to date`；以管理员/root 权限启动时可替换二进制、同步并重启已安装的系统服务副本、验证 daemon 版本，并自动进入更新后的 TUI。
- **内核通道**:System 页面可在 mihomo 的 `stable` / `alpha` 通道之间切换。

单个无 CGO 的静态二进制(< 15 MB)即包含全部功能,内置 GitHub Releases 自动更新与本地 GeoIP 解析。

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

交互式设置会安装 mihomo 核心、引导添加首个订阅并准备本地 GeoIP 数据。它会在首页预检托管端口(冲突时一键切换到可用端口),复用已有的本地 core/GeoIP,并在最后的审查页汇总端口、core、订阅、GeoIP 与服务注册状态。

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
| 订阅管理 | `mihari sub add <NAME> <URL>` · `mihari sub set <ID> --proxy auto` · `mihari sub use <ID>` |
| 系统代理 / TUN | `mihari sysproxy enable` · `mihari sysproxy enable --force` · `mihari tun enable` · `mihari tun enable --force` |
| Web 面板 | `mihari panel list` · `mihari panel open` |
| 服务控制 | `mihari service status` · `mihari service stop` |
| 更新 mihari | System 页 `Update Mihari` · `mihari self update` |

完整命令参考见 [docs/commands.md](docs/commands.md),架构与安全机制见 [docs/architecture.md](docs/architecture.md)。

## 平台目标

- Windows amd64 与 arm64
- Linux amd64 与 arm64
- macOS amd64 与 arm64

所有发行二进制均为无 CGO。

## 数据路径

| 平台 | 数据根目录(`MIHARI_DATA` 可覆盖) | 默认控制端点 |
|----------|-----------|------------------|
| Windows | `%USERPROFILE%\.mihari` | `\\.\pipe\mihari-control`(命名管道;无文件) |
| Linux | `$HOME/.mihari` | `$XDG_RUNTIME_DIR/mihari/control.sock`,否则 `$DATA/control.sock` |
| macOS | `$HOME/.mihari` | `$DATA/control.sock` |

设置、控制令牌、运行时配置、核心二进制、订阅、GeoIP、面板资产、日志与暂存都在数据根目录下。

## 文件日志

Mihari 会在数据根目录下写入三个 JSONL（每行一个 JSON 对象）文件：

| 来源 | 路径 |
| --- | --- |
| Mihari 守护进程 | `logs/mihari-daemon.log` |
| TUI（所有 TUI 实例共享） | `logs/mihari-tui.log` |
| 捕获的 mihomo 输出 | `logs/mihomo.log` |

守护进程与捕获的 mihomo 文件日志默认级别为 `info`，每个活跃文件到 10 MiB 时轮转，并保留三份文件（活跃文件加最多两份归档）。TUI 启动时使用 bootstrap 配置——级别 `debug`、100 MiB、10 份文件——以便在守护进程设置可用前也能记录日志；在后续控制面同步前会保持该 bootstrap 配置。捕获的 mihomo stdout 记为 `INFO`，stderr 记为 `WARN`；这些捕获级别不代表 mihomo 行内文本本身的严重程度。

脱敏仅为尽力而为，仍应将所有日志文件按敏感资料处理，并在分享前审阅内容。本版本尚未提供日志配置 UI 或日志导出。

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
