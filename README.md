> Disclaimer: This project is for learning and exchanging ideas about Go TUI tools. It is a non-profit open-source project and does not accept sponsorships or donations, now or in the future.
>
> This application is currently in informal development; bugs are common.

# Mihari — Mihomo / Clash CLI & TUI Manager

[English](README.md) · [简体中文](README.zh-CN.md)

[![license](https://img.shields.io/github/license/mihari-proxy/mihari)](LICENSE)
[![ci](https://img.shields.io/github/actions/workflow/status/mihari-proxy/mihari/ci.yml?branch=main)](https://github.com/mihari-proxy/mihari/actions)
[![go version](https://img.shields.io/github/go-mod/go-version/mihari-proxy/mihari)](go.mod)
[![release](https://img.shields.io/github/v/release/mihari-proxy/mihari)](https://github.com/mihari-proxy/mihari/releases)

[Website](https://mihari-proxy.github.io/mihari/) · [Releases](https://github.com/mihari-proxy/mihari/releases)

Mihari is a cross-platform [mihomo](https://github.com/MetaCubeX/mihomo) (Clash Meta) manager for Windows, Linux, and macOS. It provides a CLI, terminal UI (TUI), subscription management, system proxy, TUN mode, mihomo core management, and web panels.

An open-source terminal alternative to graphical Mihomo / Clash clients such as Clash Party and Sparkle. CLI, TUI, and browser panels share one daemon-owned control plane.

![Overview](assets/overview.png)

## What is this?

**TLDR**: Mihari is a terminal manager for mihomo — the same family of tools as mihomo GUIs like Clash Party and Sparkle, but it runs in the terminal and is hosted by a daemon in the background, so the CLI, TUI, and browser panels share one control plane.

Specifically:

- **Subscription management**: add, refresh, and switch subscription profiles, with offline switching, independent refresh intervals, and per-profile fetch proxy
- **Core management**: install, update, and restart the mihomo core
- **Service supervision**: run in the background as an OS service, with crash auto-restart
- **System proxy / TUN**: enable system proxy or TUN; a foreign proxy or another TUN/mihomo instance requires confirmation or `--force`
- **Web panels**: one-click install and open of the zashboard / MetaCubeXD panels
- **Connections & rules**: live view of connections, proxy groups, and rules, with local GeoIP resolution

## Features

- **One daemon, three surfaces**: CLI, TUI, and browser panels talk to the same daemon-owned control plane over a local named pipe / Unix domain socket. The control API never binds a TCP port.
- **OS service supervision**: install as a Windows service / systemd unit / launchd agent, with crash backoff restart.
- **Subscription profiles**: per-subscription independent caches, offline switching, per-profile refresh intervals, per-profile fetch proxy (`direct` / `proxy` / `auto`; `auto` falls back to direct), and validated atomic config generation with rollback.
- **Web panels**: one-click install / update / activate / rollback for zashboard and MetaCubeXD, served behind a loopback Web gateway with its own access credential.
- **System proxy & TUN**: cross-platform system proxy control and managed TUN, both daemon-owned and persisted. Enable refuses a foreign system proxy (`system_proxy_conflict`) or another TUN / mihomo instance (`tun_conflict`) unless `--force` (TUI asks for confirmation).
- **Ports Config**: the System page can change Mixed / Controller / Web ports; occupancy shows `Owned` or `Occupied by name (pid)`. Applying a change typically requires a daemon restart.
- **In-TUI Mihari updates**: the System page checks GitHub Releases on entry, shows `current · latest available` or `current · Up to date`, and—when Mihari was started with administrator/root privileges—replaces the binary, synchronizes and restarts an installed OS-service copy, verifies its daemon version, and automatically enters the updated TUI.
- **Core channel**: the System page can switch the mihomo core between `stable` and `alpha`.

A single CGO-free static binary (< 15 MB) contains everything, with built-in GitHub Releases self-update and local GeoIP resolution.

## Quick start

**Install**

**main release channel** (GitHub)

```sh
# Linux / macOS
curl -fsSL https://raw.githubusercontent.com/mihari-proxy/mihari/main/scripts/install/install.sh | bash
```

```powershell
# Windows (PowerShell)
irm https://raw.githubusercontent.com/mihari-proxy/mihari/main/scripts/install/install.ps1 | iex
```

**dev release channel** (GitHub)

```sh
# Linux / macOS
curl -fsSL https://raw.githubusercontent.com/mihari-proxy/mihari/dev/scripts/install/install.sh | bash -s -- --channel dev
```

```powershell
# Windows (PowerShell)
$env:MIHARI_CHANNEL = 'dev'
irm https://raw.githubusercontent.com/mihari-proxy/mihari/dev/scripts/install/install.ps1 | iex
```

Or download the binary for your platform from the [Releases page](https://github.com/mihari-proxy/mihari/releases).

**China / no GitHub access (offline)**

An all-in-one bundle (mihari binary + mihomo core + GeoIP, sha256-verified) is mirrored on a self-hosted AList drive, so installs never touch GitHub. The downloader is always taken from the stable AList root.

**main release channel** (AList / offline)

```sh
# Linux / macOS
curl -fsSL https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari/install-aio-remote.sh | bash
```

```powershell
# Windows (PowerShell)
& ([scriptblock]::Create((irm https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari/install-aio-remote.ps1)))
```

**dev release channel** (AList / offline)

```sh
# Linux / macOS
curl -fsSL https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari/install-aio-remote.sh | bash -s -- --channel dev
```

```powershell
# Windows (PowerShell)
& ([scriptblock]::Create((irm https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari/install-aio-remote.ps1))) -Channel dev
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
| Subscription management | `mihari sub add <NAME> <URL>` · `mihari sub set <ID> --proxy auto` · `mihari sub use <ID>` |
| System proxy / TUN | `mihari sysproxy enable` · `mihari sysproxy enable --force` · `mihari tun enable` · `mihari tun enable --force` |
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

## File logs

Mihari writes newline-delimited JSON (JSONL) to three files under the data root:

| Source | Path |
| --- | --- |
| Mihari daemon | `logs/mihari-daemon.log` |
| TUI (shared by all TUI instances) | `logs/mihari-tui.log` |
| Captured mihomo output | `logs/mihomo.log` |

Daemon and captured-mihomo file logs use the default `info` level, rotate each active file at 10 MiB, and retain three files (the active file plus up to two archives). The TUI starts with its bootstrap configuration—`debug`, 100 MiB, and 10 files—so it can log before daemon settings are available; it remains on this bootstrap configuration until a later control-plane synchronization. The TUI System page can change the daemon-owned level, maximum file size, and retained-file count; changes take effect without a daemon restart. Captured mihomo stdout is recorded as `INFO` and stderr as `WARN`; these capture levels do not infer the severity encoded in mihomo's own message text.

`GET /v1/logging` and `PATCH /v1/logging` are stable v1 local-control endpoints used by the TUI. They are not CLI commands, and this release does not provide log export.

Older binaries decode `mihari.yaml` with `KnownFields(true)` and cannot read a custom `log:` block. Before downgrading, use System → Logging to restore `info` / 10 MiB / 3 files, which removes that block automatically; alternatively, back up the settings file and remove `log:` manually. Redaction is best effort only. Treat all log files as sensitive material and share them only after reviewing their contents.

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

## Community

Mihari is fully open source. This project recognizes [LINUX DO](https://linux.do/) and thanks the community for supporting open-source software.

## License

[GPL-3.0](LICENSE) © 2026 Mihar1

Mihari is an independent project and is not affiliated with or endorsed by the mihomo project or MetaCubeX.
