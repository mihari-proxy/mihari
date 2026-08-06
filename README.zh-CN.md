# Mihari

[English](README.md) · [简体中文](README.zh-CN.md)

[![license](https://img.shields.io/github/license/mihari-proxy/mihari)](LICENSE)
[![ci](https://img.shields.io/github/actions/workflow/status/mihari-proxy/mihari/ci.yml?branch=main)](https://github.com/mihari-proxy/mihari/actions)
[![go version](https://img.shields.io/github/go-mod/go-version/mihari-proxy/mihari)](go.mod)
[![release](https://img.shields.io/github/v/release/mihari-proxy/mihari)](https://github.com/mihari-proxy/mihari/releases)

Mihari 是一款全新的、独立的 [mihomo](https://github.com/MetaCubeX/mihomo) 本地管理器。它平等地支持 Windows、Linux 和 macOS,围绕由 CLI、TUI 和浏览器面板共享的单一守护进程控制面进行设计。

## 这是什么?

**TLDR**:Mihari 是 mihomo 的终端管理器——和 Clash Party、Sparkle 等 mihomo GUI 是同类工具,但它运行在终端里,并由一个守护进程在后台托管,CLI、TUI 和浏览器面板共享同一个控制面。

具体功能:

- **订阅管理**:添加、刷新、切换订阅配置,支持离线切换与独立的刷新间隔
- **核心管理**:安装、更新、重启 mihomo 核心
- **服务监控**:以 OS 服务方式在后台运行,崩溃自动重启
- **系统代理 / TUN**:一键开启系统代理或 TUN 模式
- **Web 面板**:一键安装并打开 zashboard / MetaCubeXD 面板
- **连接与规则**:实时查看连接、代理组与规则,本地 GeoIP 解析

![Overview](assets/overview.png)

## 特性

- **一个守护进程,三种界面**:CLI、TUI 和浏览器面板经本地命名管道 / Unix 域套接字连接同一守护进程控制面,控制 API 从不绑定 TCP 端口。
- **OS 服务托管**:可安装为 Windows 服务 / systemd 单元 / launchd 代理,带崩溃退避重启。
- **订阅配置**:每个订阅独立缓存、离线切换、按配置独立的刷新间隔,以及经过校验的原子化配置生成与回滚。
- **Web 面板**:一键安装 / 更新 / 激活 / 回滚 zashboard 与 MetaCubeXD,置于带独立访问凭据的回环 Web 网关之后。
- **系统代理与 TUN**:跨平台的系统代理控制与托管 TUN,均由守护进程持有并持久化。

单个无 CGO 的静态二进制(< 15 MB)即包含全部功能,内置 GitHub Releases 自动更新与本地 GeoIP 解析。

## 快速开始

**安装**

```sh
# Linux / macOS
curl -fsSL https://raw.githubusercontent.com/mihari-proxy/mihari/main/install.sh | bash
```

```powershell
# Windows (PowerShell)
irm https://raw.githubusercontent.com/mihari-proxy/mihari/main/install.ps1 | iex
```

或从 [Releases 页面](https://github.com/mihari-proxy/mihari/releases) 下载对应平台的二进制。

**首次运行**

```console
mihari
```

交互式设置会安装 mihomo 核心、引导添加首个订阅并准备本地 GeoIP 数据。

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
| 订阅管理 | `mihari sub add <NAME> <URL>` · `mihari sub use <ID>` · `mihari sub refresh <ID>` |
| 系统代理 / TUN | `mihari sysproxy enable` · `mihari tun enable` |
| Web 面板 | `mihari panel list` · `mihari panel open` |
| 服务控制 | `mihari service status` · `mihari service stop` |
| 更新 mihari | `mihari self update` |

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

## 开发

```console
go test ./...
go test -race ./...
go vet ./...
```

构建本地二进制:

```console
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o mihari ./cmd/mihari
```

架构不变量、包边界与贡献指南见 [AGENTS.md](AGENTS.md) 与 [CONTRIBUTING.md](CONTRIBUTING.md),发布流程见 [RELEASE.md](RELEASE.md)。

## 许可

[GPL-3.0](LICENSE) © 2026 LeeShunEE

Mihari 是一个独立项目,与 mihomo 项目或 MetaCubeX 无关联,也不受其背书。
