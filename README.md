# Mihari

[English](README.md) · [简体中文](README.zh-CN.md)

[![license](https://img.shields.io/github/license/mihari-proxy/mihari)](LICENSE)
[![ci](https://img.shields.io/github/actions/workflow/status/mihari-proxy/mihari/ci.yml?branch=main)](https://github.com/mihari-proxy/mihari/actions)
[![go version](https://img.shields.io/github/go-mod/go-version/mihari-proxy/mihari)](go.mod)
[![release](https://img.shields.io/github/v/release/mihari-proxy/mihari)](https://github.com/mihari-proxy/mihari/releases)

Mihari is a new, independent local manager for [mihomo](https://github.com/MetaCubeX/mihomo). It targets Windows, Linux, and macOS equally and is designed around a single daemon-owned control plane shared by the CLI, TUI, and browser panels.

![Overview](assets/overview.png)

## What is this?

**TLDR**: Mihari is a terminal manager for mihomo — the same family of tools as mihomo GUIs like Clash Party and Sparkle, but it runs in the terminal and is hosted by a daemon in the background, so the CLI, TUI, and browser panels share one control plane.

Specifically:

- **Subscription management**: add, refresh, and switch subscription profiles, with offline switching and independent refresh intervals
- **Core management**: install, update, and restart the mihomo core
- **Service supervision**: run in the background as an OS service, with crash auto-restart
- **System proxy / TUN**: enable system proxy or TUN mode in one click
- **Web panels**: one-click install and open of the zashboard / MetaCubeXD panels
- **Connections & rules**: live view of connections, proxy groups, and rules, with local GeoIP resolution

## Features

- **One daemon, three surfaces**: CLI, TUI, and browser panels talk to the same daemon-owned control plane over a local named pipe / Unix domain socket. The control API never binds a TCP port.
- **OS service supervision**: install as a Windows service / systemd unit / launchd agent, with crash backoff restart.
- **Subscription profiles**: per-subscription independent caches, offline switching, per-profile refresh intervals, and validated atomic config generation with rollback.
- **Web panels**: one-click install / update / activate / rollback for zashboard and MetaCubeXD, served behind a loopback Web gateway with its own access credential.
- **System proxy & TUN**: cross-platform system proxy control and managed TUN, both daemon-owned and persisted.
- **In-TUI Mihari updates**: the System page checks GitHub Releases on entry, shows `current · latest available` or `current · Up to date`, and—when Mihari was started with administrator/root privileges—replaces the binary and automatically enters the updated TUI.
- **Core channel**: the System page can switch the mihomo core between `stable` and `alpha`.

A single CGO-free static binary (< 15 MB) contains everything, with built-in GitHub Releases self-update and local GeoIP resolution.

## Quick start

**Install**

```sh
# Linux / macOS
curl -fsSL https://raw.githubusercontent.com/mihari-proxy/mihari/main/scripts/install/install.sh | bash
```

```powershell
# Windows (PowerShell)
irm https://raw.githubusercontent.com/mihari-proxy/mihari/main/scripts/install/install.ps1 | iex
```

Or download the binary for your platform from the [Releases page](https://github.com/mihari-proxy/mihari/releases).

**China / no GitHub access (offline)**

An all-in-one bundle (mihari binary + mihomo core + GeoIP, sha256-verified) is mirrored on a self-hosted AList drive, so installs never touch GitHub. One fixed command per platform — copy and run:

```sh
# Linux / macOS
curl -fsSL https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari/install-aio-remote.sh | bash
```

```powershell
# Windows (PowerShell)
& ([scriptblock]::Create((irm https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari/install-aio-remote.ps1)))
```

See [docs/distribution.md](docs/distribution.md) for the offline distribution design.

**First run**

```console
mihari
```

The interactive setup installs the mihomo core, guides you through adding your first subscription, and prepares local GeoIP data. It pre-checks the managed ports up front (with one-key auto-fix on conflict), reuses any local core/GeoIP already present, and the final review summarizes ports, core, subscription, GeoIP and service registration.

**Add a subscription and enable the system proxy**

```console
mihari sub add my-sub https://example.com/subscribe
mihari sub list
mihari sub use <ID>
mihari sysproxy enable
```

## Common commands

| Scenario | Command |
|----------|---------|
| View status | `mihari status` |
| Core management | `mihari core status` · `mihari core restart` |
| Proxy groups | `mihari proxy groups` · `mihari proxy select <GROUP> <PROXY>` |
| Subscription management | `mihari sub add <NAME> <URL>` · `mihari sub use <ID>` · `mihari sub refresh <ID>` |
| System proxy / TUN | `mihari sysproxy enable` · `mihari tun enable` |
| Web panels | `mihari panel list` · `mihari panel open` |
| Service control | `mihari service status` · `mihari service stop` |
| Update mihari | System page `Update Mihari` · `mihari self update` |

See [docs/commands.md](docs/commands.md) for the full command reference, and [docs/architecture.md](docs/architecture.md) for the architecture and security model.

## Platform targets

- Windows amd64 and arm64
- Linux amd64 and arm64
- macOS amd64 and arm64

All release binaries are CGO-free.

## Data paths

| Platform | Data root (`MIHARI_DATA` overrides) | Default control endpoint |
|----------|-----------|------------------|
| Windows | `%USERPROFILE%\.mihari` | `\\.\pipe\mihari-control` (named pipe; no file) |
| Linux | `$HOME/.mihari` | `$XDG_RUNTIME_DIR/mihari/control.sock`, else `$DATA/control.sock` |
| macOS | `$HOME/.mihari` | `$DATA/control.sock` |

Settings, control token, runtime config, core binary, subscriptions, GeoIP, panel assets, logs, and staging all live under the data root.

## Development

```console
go test ./...
go test -race ./...
go vet ./...
```

Build a local binary:

```console
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o bin/mihari ./cmd/mihari
```

The architecture invariants, package boundaries, and contribution guidance are recorded in [AGENTS.md](AGENTS.md) and [CONTRIBUTING.md](.github/CONTRIBUTING.md). See [docs/RELEASE.md](docs/RELEASE.md) for the release process.

## License

[GPL-3.0](LICENSE) © 2026 LeeShunEE

Mihari is an independent project and is not affiliated with or endorsed by the mihomo project or MetaCubeX.
