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
- **In-TUI Mihari updates**: the System page checks GitHub Releases on entry, shows `current · latest available` or `current · Up to date`, and—when Mihari was started with administrator/root privileges—replaces the binary, synchronizes and restarts an installed OS-service copy, verifies its daemon version, and automatically enters the updated TUI. The update confirmation keeps safe nonstandard installed build labels as `Unknown[label]`; compatibility remains unknown. Long confirmation content scrolls with ↑/↓ or PgUp/PgDn, with Cancel selected by default.
- **Core channel**: the System page can switch the mihomo core between `stable` and `alpha`.

Proxy latency tests discover provider nodes and use mihomo's provider-specific endpoint when needed. Duplicate names appear once per group and share a test result: a global node takes priority, otherwise the first matching provider in name order is used. The first successful node check after TUI startup warns about duplicates; the tested source may differ from the group's selected source. Provider reads retry transient failures up to three times. If a refresh still fails, Proxies retains the last snapshot with a **Stale data** notice and the key error; the notice clears after recovery. CLI/TUI and daemon should be upgraded together.

Mihomo HTTP failures retain their original error text and upstream status in diagnostic logs, including gateway requests and WebSocket handshakes. File logs and exports are not redacted: credentials, URLs, paths, and configuration fragments carried by errors remain available for diagnosis. User-facing errors remain concise.

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

The interactive setup uses the shared TUI theme and a step-by-step layout, with the current action, an animated waiting indicator and elapsed time. Failures show a safe explanation and next action; press F2 for scrollable, copyable diagnostic details.

Confirmed endpoint changes are saved immediately by the daemon. Saved resources survive interruption; reopening checks required ports/core and resumes missing setup, without requiring optional subscriptions or GeoIP. Core shows local readiness, version, channel and runtime state; GeoIP shows Country/ASN availability and update times separately. Existing subscriptions remain visible as an overview with counts, per-profile state and the current selection; Enter continues without changing them. Add more or manage existing profiles on the Subscriptions page. If a newly registered subscription's first download fails, retry refreshes that same subscription.

Esc during an operation asks to cancel, then checks daemon settlement and saved state. An unknown result is not proof that nothing was saved: recheck or exit without blindly resubmitting. Ctrl+Q confirms exit. A confirmed startup port conflict exposes restricted endpoint recovery over authenticated local IPC; other business mutations remain unavailable. After changing ports, restart the daemon and recheck (or wait for reconnection). Service ownership cannot currently be verified, so setup does not automatically restart a potentially different service instance.

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
| Routing mode | `mihari proxy mode [rule\|global\|direct]` · `mihari proxy select GLOBAL <PROXY>` |
| Subscription management | `mihari sub add <NAME> <URL>` · `mihari sub set <ID> --proxy auto` · `mihari sub use <ID>` |
| System proxy / TUN | `mihari sysproxy enable` · `mihari sysproxy enable --force` · `mihari tun enable` · `mihari tun enable --force` |
| Web panels | `mihari panel list` · `mihari panel open` |
| Service control | `mihari service status` · `mihari service stop` |
| Completely uninstall | System page `Completely Uninstall Mihari` · `mihari service uninstall --purge --yes` · `mihari service uninstall --purge --yes --force` |
| Update mihari | System page `Update Mihari` · `mihari self update` |

See [docs/commands.md](docs/commands.md) for the full command reference, and [docs/architecture.md](docs/architecture.md) for the architecture and security model.

In TUI **Proxies**, the **Routing** card contains **Mode** and **GLOBAL**. Focus **Mode** and press Enter to choose Rule, Global, or Direct; use ↑/↓, Enter to apply, and Esc to cancel. **GLOBAL** expands the core's existing candidate group and scrolls to show the whole section when it fits; larger groups start at the top of the list viewport and remain navigable with the arrow keys. Mihari saves the mode globally and the GLOBAL exit per subscription, including choices made by supported Web panels. Rule is the default. Mode and exit changes preserve existing connections. If a saved exit disappears, Mihari saves DIRECT when available, otherwise Rule; a stopped core shows saved changes as pending.

Each Proxies group, including GLOBAL, has **Locate** beside its current selection. On the group header, press → to focus Locate and Enter to expand the group and scroll to its selected card; ← returns to the header. This only moves keyboard focus. Later refreshes do not move that focus when the selection changes. Locate remains usable with retained **Last selected** data, and is disabled when the selection is empty or absent from the candidate list.

In TUI **Subs**, Enter opens editable details; `a` adds a profile. Use Tab/Shift+Tab or ↑/↓ to move between fields, and ←/→/Space to cycle **Auto refresh** or **Mode**. Enter advances to the next field; only Enter on **Save** submits. PgUp/PgDn scroll the details; long URLs scroll horizontally. All built-in TUI text is English. The list uses **InUse**, **Enabled**, **Status**, and **Mode**; `p` cycles fetch mode.

Adding and editing share a compact centered form. Details group the live status above the settings; empty errors stay hidden and timestamps use local time to the minute. In short windows the body scrolls while **Save** stays visible. Cycle fields use reverse highlighting; text fields use a lightened input background and white cursor. Contextual shortcuts appear once at the bottom of the terminal. An empty **Interval** shows the current global interval as a placeholder; leaving it blank keeps inheritance.

Changing a subscription URL preserves its existing cache and InUse selection, without downloading or reloading immediately. **Outdated** means the cache came from another URL; you can still use it offline. Changing a profile's interval resets its next refresh and marks its cache **Expired** until a successful refresh, including a valid 304 response. Higher-priority states such as Disabled, Failed, Missing, and Outdated still take precedence. With Auto refresh off, Next shows **Manual**.

Save waits for a result before closing. A revision conflict asks whether to overwrite only your changed fields. If the result is unknown, Mihari checks the operation and current state without replaying the save; **Submit again** requires another confirmation and can create a duplicate when adding. Closing does not cancel a save. If a profile was created but its first download failed, select it and press `r` to retry the download.

Upgrade the TUI and daemon together; mixed versions are not supported. Before upgrading, stop the daemon and back up the complete business data for your layout (Unix B/D, or Windows/private P), including catalog, caches, settings/state, and runtime configuration as one consistent set. New catalog fields cannot be read by old binaries. Direct downgrade is unsupported: stop the daemon and restore a compatible complete backup when reverting the binary. Removing individual YAML fields is not a supported downgrade procedure.

## Platform targets

- Windows amd64 and arm64
- Linux amd64 and arm64
- macOS amd64 and arm64

All release binaries are CGO-free.

## Data paths

| Platform | Default machine entry B | Business data D | Current-user diagnostics U |
| --- | --- | --- | --- |
| Windows | `%USERPROFILE%\.mihari` | Same root | Same root |
| Linux | `/var/lib/mihari` | `B/data` | Absolute `XDG_STATE_HOME/mihari`, otherwise trusted home `.local/state/mihari` |
| macOS | `/Library/Application Support/mihari` | `B/data` | Trusted home `Library/Logs/mihari` |

Unix E/C/channel are `B/control.sock`, `B/control.token`, and `B/mihari-channel`; I defaults to `/usr/local/lib/mihari`. B is root0711, D root0700, C/channel root0644, and E root0666. Every local user can authenticate to manage the same proxy and obtain controlled machine diagnostics without sudo; users cannot directly read D or another user's U. Windows retains `\\.\pipe\mihari-control`.

Explicit `MIHARI_DATA=P` retains the private single-root P layout and 0700/0600 permissions, never P/data; it cannot overlap default B/D. Root ignores HOME/SUDO_USER/XDG, and shared discovery does not use XDG_RUNTIME_DIR. Root installation/migration uses stopped, validated, atomic transactions and repeatable recovery, preserving the old tree and logs. All platforms preserve subscription configuration and override only Mihari-managed parameters; TUN controls change only `tun.enable`, leaving its other fields intact. Configuration semantics and native providers are handled by mihomo, with candidate validation and reload rollback. Unix root still requires the built-in trusted v1.19.30 core; its binary identity checks remain independent of configuration generation. See [Unix layout and recovery](docs/unix-layout.md) for overrides, I/filesystem requirements, stopped credential rotation and recovery entrypoints.

## File logs

Unix machine logs live in D and current-user TUI logs in U. Windows/explicit private P retain one root. Logs use newline-delimited JSON (JSONL):

| Source | Path |
| --- | --- |
| Mihari daemon | `D/logs/mihari-daemon.log` |
| TUI (shared by this UID’s instances) | `U/logs/mihari-tui.log` |
| Captured mihomo output | `D/logs/mihomo.log` |

Daemon and captured-mihomo file logs use the default `info` level, rotate each active file at 10 MiB, and retain three files (the active file plus up to two archives). The TUI starts with its bootstrap configuration—`debug`, 100 MiB, and 10 files—so it can log before daemon settings are available; it remains on this bootstrap configuration until a later control-plane synchronization. The TUI System page can change the daemon-owned level, maximum file size, and retained-file count; changes take effect without a daemon restart. Captured mihomo stdout is recorded as `INFO` and stderr as `WARN`; these capture levels do not infer the severity encoded in mihomo's own message text.

`GET /v1/logging` and `PATCH /v1/logging` are stable v1 local-control endpoints used by the TUI. They are not CLI commands. Log export is TUI-only: press `e` on the Logs page, or select **Export logs** under System → Logging. The dialog supports the last 24 hours, last 60 minutes, a local-time interval, or all records. Its default destination is `U/logs-export/`; an existing archive is never overwritten, and a custom destination must be an absolute `.zip` path in an existing directory. There is no CLI log-export command.

Logging appears between Network and About. Unix shows separate machine and current-user log directories. Windows retains its single read-only **Logging Dir**: select it and press Enter to copy the path. In Export Logs, **Current Time** refreshes every second. Use ↑/↓ to select a field, Enter to edit or apply, and Esc to confirm discarding the current edit. While editing Range, arrows or Tab/Shift+Tab cycle modes; a custom interval displays `Use YYYY-MM-DD HH:MM format`. Select **Export** and press Enter to start.

On Windows, private logs grant the individual data user and LocalSystem access, including when an elevated process created an Administrators-owned data directory. Writer startup repairs existing daemon, TUI and mihomo logs, retained archives and lock files without changing their contents. Running services refresh the root permission policy when creating or hardening files, preserving the repaired user access after rotation. If an older version removed ordinary-user access, the updated application must run once with administrator privileges to repair it. The elevated in-TUI update flow starts the new TUI with those privileges; users who replace the binary manually may need an elevated first start.

System Unix exports use `mihari-logs-export/v2`, combining authenticated machine snapshots with current-user logs. Offline export requires explicitly choosing current-user logs only. Windows/explicit private P retain local v1. An archive contains `manifest.json` plus only the non-empty fixed entries `daemon/mihari-daemon.log`, `tui/mihari-tui.log`, and `mihomo/mihomo.log`. Records are validated and filtered by time while preserving the original JSON record bytes. Neither snapshots nor exports redact their content. The export dialog shows a red notice before export and after completion: logs may contain passwords, access tokens, full subscription URLs, and user configuration. Review them before sharing.

Diagnostic text, retained HTTP failure bodies, and logical mihomo output lines each have a 256 KiB limit, with explicit truncation indicators. JSON escaping can make a record larger than the existing snapshot limit; such records use bounded fragments carrying `record_id`, `fragment_index`, and `fragment_count`. Missing fragments remain identifiable after rotation or an interrupted write. Existing stacks are retained; ordinary errors do not trigger new stack capture.

Errors use the currently available file logger. Ordinary CLI commands do not create logs or open historical log files. Expected rejections and active cancellation are recorded at INFO, recoverable failures and retry attempts at WARN, and final failures at ERROR, subject to the configured level. A failure is recorded by its execution owner; replaying the same result does not duplicate it.

Export keeps the opened destination-directory identity for the entire operation and will not follow a path replaced while the archive is being generated. On Unix, cleanup assumes the same UID and local root/administrators are trusted. An untrusted shared parent can therefore leave an empty private workspace after its contents were removed, even if its permissions were tightened during export; a cleanup I/O failure reports that content may remain. A destination directory renamed externally after successful publication can also make the displayed absolute path stale.

Older binaries decode `mihari.yaml` with `KnownFields(true)` and cannot read a custom `log:` block. Before downgrading, use System → Logging to restore `info` / 10 MiB / 3 files, which removes that block automatically; alternatively, back up the settings file and remove `log:` manually. Historical redacted logs cannot be restored to their original content, and older clients may still redact exported records.

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
